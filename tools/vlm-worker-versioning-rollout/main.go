package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal/workerdeployment"
	"go.temporal.io/sdk/client"
)

// RolloutSpec is injected by Helm as ROLLOUT_SPEC JSON.
type RolloutSpec struct {
	Targets []RolloutTarget `json:"targets"`
}

type RolloutTarget struct {
	OntapVersion         string                       `json:"ontapVersion"`
	TaskQueue            string                       `json:"taskQueue"`
	DeploymentName       string                       `json:"deploymentName"`
	NewBuildID           string                       `json:"newBuildId"`
	OldBuildID           string                       `json:"oldBuildId"`
	DrainDeployment      string                       `json:"drainDeployment"`
	DrainConfigMap       string                       `json:"drainConfigMap"`
	DrainImage           string                       `json:"drainImage"`
	DrainImagePullPolicy string                       `json:"drainImagePullPolicy"`
	DrainReplicas        int                          `json:"drainReplicas"`
	ServiceAccountName   string                       `json:"serviceAccountName"`
	SecretName           string                       `json:"secretName"`
	Resources            map[string]map[string]string `json:"resources"`
	ConfigData           map[string]string            `json:"configData"`
	ReleaseName          string                       `json:"releaseName"`
	ReleaseNamespace     string                       `json:"releaseNamespace"`
}

func main() {
	log := slog.Default()
	ctx := context.Background()
	switch os.Getenv("MODE") {
	case "ensure-drain":
		os.Exit(runEnsureDrain(ctx, log))
	case "promote-and-retire":
		os.Exit(runPromoteAndRetire(ctx, log))
	default:
		log.Error("unknown or missing MODE")
		os.Exit(1)
	}
}

func runEnsureDrain(ctx context.Context, log *slog.Logger) int {
	spec, err := loadSpec()
	if err != nil {
		log.Error("load ROLLOUT_SPEC failed", "error", err)
		return 1
	}
	if len(spec.Targets) == 0 {
		log.Info("no D-patch targets — ensure-drain no-op")
		return 0
	}
	k8s, err := newK8sClient(os.Getenv("NAMESPACE"))
	if err != nil {
		log.Error("k8s client failed", "error", err)
		return 1
	}
	addr := fmt.Sprintf("%s:%s", envOr("TEMPORAL_HOST", "temporal-frontend.temporal.svc.cluster.local"), envOr("TEMPORAL_PORT", "7233"))
	namespace := envOr("TEMPORAL_NAMESPACE", "default")
	tc, err := client.Dial(client.Options{HostPort: addr, Namespace: namespace})
	if err != nil {
		log.Error("temporal dial failed", "error", err)
		return 1
	}
	defer tc.Close()
	wdc := workerdeployment.FromTemporal(tc)

	poll := time.Duration(envInt("POLL_EVERY_SECONDS", 15)) * time.Second
	timeout := time.Duration(envInt("DRAIN_READY_TIMEOUT_MINUTES", 15)) * time.Minute
	if err := ensureDrainTargets(ctx, k8s, wdc, spec.Targets, poll, timeout, workerdeployment.WaitForPolling, log); err != nil {
		log.Error("ensure-drain failed", "error", err)
		return 1
	}
	return 0
}

type drainResourceClient interface {
	deploymentReadinessClient
	ensureDrainResources(ctx context.Context, target RolloutTarget) error
}

func ensureDrainTargets(
	ctx context.Context,
	k8s drainResourceClient,
	wdc workerdeployment.Client,
	targets []RolloutTarget,
	pollEvery time.Duration,
	timeout time.Duration,
	waitForPolling waitForPollingFunc,
	log *slog.Logger,
) error {
	for _, target := range targets {
		if err := validateEnsureDrainTarget(target); err != nil {
			return fmt.Errorf("invalid target for ONTAP %q: %w", target.OntapVersion, err)
		}
		log.Info("creating or patching drain resources", "deployment", target.DrainDeployment, "build", target.OldBuildID)
		if err := k8s.ensureDrainResources(ctx, target); err != nil {
			return fmt.Errorf("ensure drain resources for %q: %w", target.DrainDeployment, err)
		}
		readyCtx, readyCancel := context.WithTimeout(ctx, timeout)
		_, err := waitForDeploymentReady(readyCtx, k8s, target.DrainDeployment, pollEvery)
		readyCancel()
		if err != nil {
			return fmt.Errorf("wait for drain Deployment %q ready: %w", target.DrainDeployment, err)
		}
		deploymentName := workerdeployment.ResolveDeploymentName(target.DeploymentName, target.TaskQueue)
		pollCtx, pollCancel := context.WithTimeout(ctx, timeout)
		err = waitForPolling(
			pollCtx,
			wdc,
			target.TaskQueue,
			deploymentName,
			target.OldBuildID,
			pollEvery,
		)
		pollCancel()
		if err != nil {
			return fmt.Errorf("wait for old build %q polling on %q: %w", target.OldBuildID, deploymentName, err)
		}
		log.Info("drain build ready and polling", "deployment", target.DrainDeployment, "build", target.OldBuildID)
	}
	return nil
}

func validateEnsureDrainTarget(target RolloutTarget) error {
	required := map[string]string{
		"old build ID":         target.OldBuildID,
		"drain Deployment":     target.DrainDeployment,
		"drain ConfigMap":      target.DrainConfigMap,
		"drain image":          target.DrainImage,
		"service account name": target.ServiceAccountName,
		"secret name":          target.SecretName,
		"release name":         target.ReleaseName,
		"release namespace":    target.ReleaseNamespace,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	if target.DrainReplicas <= 0 {
		return fmt.Errorf("drain replicas must be greater than zero")
	}
	if len(target.ConfigData) == 0 {
		return fmt.Errorf("drain ConfigMap data is required")
	}
	return nil
}

type retireResourceClient interface {
	deploymentReadinessClient
	scaleDeployment(ctx context.Context, name string, replicas int) error
}

type promoteFunc func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string) error

type waitForDrainFunc func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string, pollEvery time.Duration) error

type deleteBuildFunc func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string) error

type promoteAndRetireOptions struct {
	dryRun         bool
	deleteAfter    bool
	pollEvery      time.Duration
	readyTimeout   time.Duration
	drainTimeout   time.Duration
	pollingTimeout time.Duration
	waitForPolling waitForPollingFunc
	promote        promoteFunc
	waitForDrain   waitForDrainFunc
	deleteBuild    deleteBuildFunc
}

func runPromoteAndRetire(ctx context.Context, log *slog.Logger) int {
	spec, err := loadSpec()
	if err != nil {
		log.Error("load ROLLOUT_SPEC failed", "error", err)
		return 1
	}
	if len(spec.Targets) == 0 {
		log.Info("no D-patch targets — promote-and-retire no-op")
		return 0
	}

	addr := fmt.Sprintf("%s:%s", envOr("TEMPORAL_HOST", "temporal-frontend.temporal.svc.cluster.local"), envOr("TEMPORAL_PORT", "7233"))
	ns := envOr("TEMPORAL_NAMESPACE", "default")
	tc, err := client.Dial(client.Options{HostPort: addr, Namespace: ns})
	if err != nil {
		log.Error("temporal dial failed", "error", err)
		return 1
	}
	defer tc.Close()
	wdc := workerdeployment.FromTemporal(tc)

	k8s, err := newK8sClient(os.Getenv("NAMESPACE"))
	if err != nil {
		log.Error("k8s client failed", "error", err)
		return 1
	}

	if err := promoteAndRetireTargets(ctx, k8s, wdc, spec.Targets, promoteAndRetireOptions{
		dryRun:         os.Getenv("DRY_RUN") == "true",
		deleteAfter:    os.Getenv("DELETE_VERSION_AFTER_DRAIN") == "true",
		pollEvery:      time.Duration(envInt("POLL_EVERY_SECONDS", 15)) * time.Second,
		readyTimeout:   time.Duration(envInt("DRAIN_READY_TIMEOUT_MINUTES", 15)) * time.Minute,
		drainTimeout:   time.Duration(envInt("DRAIN_TIMEOUT_MINUTES", 360)) * time.Minute,
		pollingTimeout: 10 * time.Minute,
		waitForPolling: workerdeployment.WaitForPolling,
		promote:        workerdeployment.PromoteBuild,
		waitForDrain:   workerdeployment.WaitForDrain,
		deleteBuild:    workerdeployment.DeleteBuild,
	}, log); err != nil {
		log.Error("promote-and-retire failed", "error", err)
		return 1
	}
	return 0
}

func promoteAndRetireTargets(
	ctx context.Context,
	k8s retireResourceClient,
	wdc workerdeployment.Client,
	targets []RolloutTarget,
	opts promoteAndRetireOptions,
	log *slog.Logger,
) error {
	pollEvery := opts.pollEvery
	if pollEvery <= 0 {
		pollEvery = 15 * time.Second
	}
	readyTimeout := opts.readyTimeout
	if readyTimeout <= 0 {
		readyTimeout = 15 * time.Minute
	}
	drainTimeout := opts.drainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 360 * time.Minute
	}
	pollingTimeout := opts.pollingTimeout
	if pollingTimeout <= 0 {
		pollingTimeout = 10 * time.Minute
	}

	for _, target := range targets {
		if target.NewBuildID == "" || target.OldBuildID == "" {
			log.Info("skip target missing build ids", "ontap", target.OntapVersion)
			continue
		}
		depName := workerdeployment.ResolveDeploymentName(target.DeploymentName, target.TaskQueue)
		log.Info("promote-and-retire", "workerDeployment", depName, "new", target.NewBuildID, "old", target.OldBuildID)

		readyCtx, readyCancel := context.WithTimeout(ctx, readyTimeout)
		replicas, err := waitForDeploymentReady(readyCtx, k8s, target.DrainDeployment, pollEvery)
		readyCancel()
		if err != nil {
			log.Error("wait for drain Deployment ready failed", "deployment", target.DrainDeployment, "error", err)
			continue
		}
		log.Info("drain Deployment ready", "deployment", target.DrainDeployment, "replicas", replicas)

		waitCtx, waitCancel := context.WithTimeout(ctx, pollingTimeout)
		err = waitForBuildsPolling(
			waitCtx,
			wdc,
			target.TaskQueue,
			depName,
			target.OldBuildID,
			target.NewBuildID,
			pollEvery,
			opts.waitForPolling,
		)
		waitCancel()
		if err != nil {
			log.Error("wait for builds polling failed", "error", err)
			continue
		}

		if opts.dryRun {
			log.Info("DRY-RUN: would promote", "build", target.NewBuildID)
		} else if err := opts.promote(ctx, wdc, depName, target.NewBuildID); err != nil {
			log.Error("promote failed", "error", err)
			continue
		} else {
			log.Info("promoted build", "build", target.NewBuildID)
		}

		drainCtx, drainCancel := context.WithTimeout(ctx, drainTimeout)
		if opts.dryRun {
			log.Info("DRY-RUN: would wait for drain", "build", target.OldBuildID)
			drainCancel()
		} else if err := opts.waitForDrain(drainCtx, wdc, depName, target.OldBuildID, pollEvery); err != nil {
			drainCancel()
			log.Error("wait for drain failed — leaving drain pods up", "build", target.OldBuildID, "error", err)
			continue
		} else {
			drainCancel()
			log.Info("old build drained", "build", target.OldBuildID)
		}

		if target.DrainDeployment != "" {
			if opts.dryRun {
				log.Info("DRY-RUN: would scale drain to 0", "deployment", target.DrainDeployment)
			} else if err := k8s.scaleDeployment(ctx, target.DrainDeployment, 0); err != nil {
				log.Error("scale drain to 0 failed", "deployment", target.DrainDeployment, "error", err)
			} else {
				log.Info("scaled drain to 0", "deployment", target.DrainDeployment)
			}
		}

		if opts.deleteAfter && !opts.dryRun {
			if err := opts.deleteBuild(ctx, wdc, depName, target.OldBuildID); err != nil {
				log.Error("DeleteVersion failed (optional)", "error", err)
			}
		}
	}
	return nil
}

type deploymentReadinessClient interface {
	deploymentReady(ctx context.Context, name string) (ready bool, replicas int, err error)
}

func waitForDeploymentReady(
	ctx context.Context,
	k8s deploymentReadinessClient,
	deploymentName string,
	pollEvery time.Duration,
) (int, error) {
	if strings.TrimSpace(deploymentName) == "" {
		return 0, fmt.Errorf("drain Deployment name is required")
	}
	if pollEvery <= 0 {
		pollEvery = 5 * time.Second
	}
	ticker := time.NewTicker(pollEvery)
	defer ticker.Stop()
	for {
		ready, replicas, err := k8s.deploymentReady(ctx, deploymentName)
		if err != nil {
			return 0, err
		}
		if ready && replicas > 0 {
			return replicas, nil
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-ticker.C:
		}
	}
}

type waitForPollingFunc func(
	context.Context,
	workerdeployment.Client,
	string,
	string,
	string,
	time.Duration,
) error

func waitForBuildsPolling(
	ctx context.Context,
	wdc workerdeployment.Client,
	taskQueue string,
	deploymentName string,
	oldBuildID string,
	newBuildID string,
	pollEvery time.Duration,
	wait waitForPollingFunc,
) error {
	if err := wait(ctx, wdc, taskQueue, deploymentName, oldBuildID, pollEvery); err != nil {
		return fmt.Errorf("wait for old build %q polling: %w", oldBuildID, err)
	}
	if err := wait(ctx, wdc, taskQueue, deploymentName, newBuildID, pollEvery); err != nil {
		return fmt.Errorf("wait for new build %q polling: %w", newBuildID, err)
	}
	return nil
}

func loadSpec() (RolloutSpec, error) {
	raw := strings.TrimSpace(os.Getenv("ROLLOUT_SPEC"))
	if raw == "" || raw == "[]" || raw == "{}" {
		return RolloutSpec{}, nil
	}
	var spec RolloutSpec
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		// Allow a bare array of targets
		var targets []RolloutTarget
		if err2 := json.Unmarshal([]byte(raw), &targets); err2 != nil {
			return RolloutSpec{}, err
		}
		spec.Targets = targets
	}
	return spec, nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil || n <= 0 {
		return def
	}
	return n
}
