package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal/workerdeployment"
	"go.temporal.io/sdk/client"
)

// RolloutSpec is injected by Helm as ROLLOUT_SPEC JSON.
type RolloutSpec struct {
	Targets         []RolloutTarget  `json:"targets"`
	InitialCurrents []InitialCurrent `json:"initialCurrents"`
}

// InitialCurrent is a first enablement: buildId is set and drainingBuilds is empty.
type InitialCurrent struct {
	OntapVersion   string `json:"ontapVersion"`
	TaskQueue      string `json:"taskQueue"`
	DeploymentName string `json:"deploymentName"`
	BuildID        string `json:"buildId"`
	ReleaseName    string `json:"releaseName"`
}

type RolloutTarget struct {
	OntapVersion    string `json:"ontapVersion"`
	TaskQueue       string `json:"taskQueue"`
	DeploymentName  string `json:"deploymentName"`
	NewBuildID      string `json:"newBuildId"`
	OldBuildID      string `json:"oldBuildId"`
	DrainDeployment string `json:"drainDeployment"`
	DrainReplicas   int    `json:"drainReplicas"`
	ReleaseName     string `json:"releaseName"`
}

func main() {
	log := slog.Default()
	ctx := context.Background()
	code := 1
	switch os.Getenv("MODE") {
	case "ensure-drain":
		code = runEnsureDrain(ctx, log)
	case "promote-and-retire":
		code = runPromoteAndRetire(ctx, log)
	default:
		log.Error("unknown or missing MODE")
	}
	// os.Exit skips defers, so quit the sidecar before exiting. Otherwise a Job
	// with an Istio proxy stays Running after the rollout container finishes.
	shutdownIstioSidecar(log)
	os.Exit(code)
}

// istioQuitPorts are the pilot-agent admin ports that accept POST /quitquitquit.
// 15020 is current Istio; 15000 is the older envoy admin port.
var istioQuitPorts = []string{"15020", "15000"}

// shutdownIstioSidecar asks the injected proxy to exit so the Job pod can complete.
// A missing sidecar is normal for local runs and is not a failure.
func shutdownIstioSidecar(log *slog.Logger) {
	const request = "POST /quitquitquit HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n"
	for _, port := range istioQuitPorts {
		address := net.JoinHostPort("127.0.0.1", port)
		conn, err := net.DialTimeout("tcp", address, 2*time.Second)
		if err != nil {
			continue
		}
		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, err = conn.Write([]byte(request))
		_ = conn.Close()
		if err != nil {
			log.Warn("failed to quit istio sidecar", "port", port, "error", err)
			return
		}
		log.Info("istio sidecar shutdown signal sent", "port", port)
		return
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
	if err := ensureDrainTargets(ctx, k8s, wdc, spec.Targets, poll, timeout, workerdeployment.WaitForPolling, workerdeployment.CurrentBuildID, workerdeployment.IsBuildDrained, log); err != nil {
		log.Error("ensure-drain failed", "error", err)
		return 1
	}
	return 0
}

type cutoverRecorder interface {
	recordFinishedCutover(ctx context.Context, releaseName, ontapVersion, newBuildID, oldBuildID string) error
}

type drainResourceClient interface {
	deploymentReadinessClient
	cutoverRecorder
	scaleDeployment(ctx context.Context, name string, replicas int) error
}

func ensureDrainTargets(
	ctx context.Context,
	k8s drainResourceClient,
	wdc workerdeployment.Client,
	targets []RolloutTarget,
	pollEvery time.Duration,
	timeout time.Duration,
	waitForPolling waitForPollingFunc,
	currentBuild currentBuildFunc,
	buildDrained buildDrainedFunc,
	log *slog.Logger,
) error {
	for _, target := range targets {
		if err := validateEnsureDrainTarget(target); err != nil {
			return fmt.Errorf("invalid target for ONTAP %q: %w", target.OntapVersion, err)
		}
		deploymentName := workerdeployment.ResolveDeploymentName(target.DeploymentName, target.TaskQueue)
		finished, err := cutoverFinished(ctx, wdc, deploymentName, target.NewBuildID, target.OldBuildID, currentBuild, buildDrained)
		if err != nil {
			return fmt.Errorf("read cutover state for %q: %w", deploymentName, err)
		}
		if finished {
			log.Info("cutover already finished, skip ensure-drain", "ontap", target.OntapVersion, "build", target.NewBuildID, "old", target.OldBuildID)
			if err := k8s.recordFinishedCutover(ctx, target.ReleaseName, target.OntapVersion, target.NewBuildID, target.OldBuildID); err != nil {
				return fmt.Errorf("record finished cutover for %q: %w", target.OntapVersion, err)
			}
			continue
		}
		ready, replicas, err := k8s.deploymentReady(ctx, target.DrainDeployment)
		if errors.Is(err, errDeploymentNotFound) {
			// Helm creates the chart Deployment. Creating it here makes the first
			// upgrade fail: the object exists, but the previous release does not list it.
			log.Info("drain deployment is not on the cluster yet; Helm creates it in this release", "deployment", target.DrainDeployment, "build", target.OldBuildID)
			continue
		}
		if err != nil {
			return fmt.Errorf("read drain Deployment %q: %w", target.DrainDeployment, err)
		}
		if !ready || replicas == 0 {
			if target.DrainReplicas < 1 {
				return fmt.Errorf("drain replicas for %q must be positive", target.DrainDeployment)
			}
			log.Info("scaling drain deployment up", "deployment", target.DrainDeployment, "replicas", target.DrainReplicas)
			if err := k8s.scaleDeployment(ctx, target.DrainDeployment, target.DrainReplicas); err != nil {
				return fmt.Errorf("scale drain Deployment %q: %w", target.DrainDeployment, err)
			}
		}
		readyCtx, readyCancel := context.WithTimeout(ctx, timeout)
		_, err = waitForDeploymentReady(readyCtx, k8s, target.DrainDeployment, pollEvery)
		readyCancel()
		if err != nil {
			return fmt.Errorf("wait for drain Deployment %q ready: %w", target.DrainDeployment, err)
		}
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
		"old build ID":     target.OldBuildID,
		"drain Deployment": target.DrainDeployment,
		"release name":     target.ReleaseName,
	}
	for field, value := range required {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", field)
		}
	}
	if target.DrainReplicas <= 0 {
		return fmt.Errorf("drain replicas must be greater than zero")
	}
	return nil
}

type retireResourceClient interface {
	deploymentReadinessClient
	cutoverRecorder
	scaleDeployment(ctx context.Context, name string, replicas int) error
}

type promoteFunc func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string) error

type currentBuildFunc func(ctx context.Context, wdc workerdeployment.Client, deploymentName string) (string, error)

type buildDrainedFunc func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string) (bool, error)

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
	currentBuild   currentBuildFunc
	buildDrained   buildDrainedFunc
	waitForDrain   waitForDrainFunc
	deleteBuild    deleteBuildFunc
}

func runPromoteAndRetire(ctx context.Context, log *slog.Logger) int {
	spec, err := loadSpec()
	if err != nil {
		log.Error("load ROLLOUT_SPEC failed", "error", err)
		return 1
	}
	if len(spec.Targets) == 0 && len(spec.InitialCurrents) == 0 {
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

	opts := promoteAndRetireOptions{
		dryRun:         os.Getenv("DRY_RUN") == "true",
		deleteAfter:    os.Getenv("DELETE_VERSION_AFTER_DRAIN") == "true",
		pollEvery:      time.Duration(envInt("POLL_EVERY_SECONDS", 15)) * time.Second,
		readyTimeout:   time.Duration(envInt("DRAIN_READY_TIMEOUT_MINUTES", 15)) * time.Minute,
		drainTimeout:   time.Duration(envInt("DRAIN_TIMEOUT_MINUTES", 360)) * time.Minute,
		pollingTimeout: 10 * time.Minute,
		waitForPolling: workerdeployment.WaitForPolling,
		promote:        workerdeployment.PromoteBuild,
		currentBuild:   workerdeployment.CurrentBuildID,
		buildDrained:   workerdeployment.IsBuildDrained,
		waitForDrain:   workerdeployment.WaitForDrain,
		deleteBuild:    workerdeployment.DeleteBuild,
	}
	if len(spec.Targets) > 0 {
		if err := promoteAndRetireTargets(ctx, k8s, wdc, spec.Targets, opts, log); err != nil {
			log.Error("promote-and-retire failed", "error", err)
			return 1
		}
	}
	if err := promoteInitialCurrents(ctx, k8s, wdc, spec.InitialCurrents, opts, log); err != nil {
		log.Error("promote-and-retire failed", "error", err)
		return 1
	}
	return 0
}

// cutoverFinished reports that newBuildID is already Current and oldBuildID is drained.
// A later Helm upgrade must not promote again or scale the old build's drain pods back up.
func cutoverFinished(
	ctx context.Context,
	wdc workerdeployment.Client,
	deploymentName string,
	newBuildID string,
	oldBuildID string,
	currentBuild currentBuildFunc,
	buildDrained buildDrainedFunc,
) (bool, error) {
	if currentBuild == nil || buildDrained == nil {
		return false, nil
	}
	current, err := currentBuild(ctx, wdc, deploymentName)
	if err != nil {
		return false, err
	}
	if current == "" || current != newBuildID {
		return false, nil
	}
	return buildDrained(ctx, wdc, deploymentName, oldBuildID)
}

func buildAlreadyCurrent(
	ctx context.Context,
	wdc workerdeployment.Client,
	deploymentName string,
	newBuildID string,
	currentBuild currentBuildFunc,
) (bool, error) {
	if currentBuild == nil {
		return false, nil
	}
	current, err := currentBuild(ctx, wdc, deploymentName)
	if err != nil {
		return false, err
	}
	return current != "" && current == newBuildID, nil
}

func promoteInitialCurrents(
	ctx context.Context,
	k8s cutoverRecorder,
	wdc workerdeployment.Client,
	currents []InitialCurrent,
	opts promoteAndRetireOptions,
	log *slog.Logger,
) error {
	pollEvery := opts.pollEvery
	if pollEvery <= 0 {
		pollEvery = 15 * time.Second
	}
	pollingTimeout := opts.pollingTimeout
	if pollingTimeout <= 0 {
		pollingTimeout = 10 * time.Minute
	}
	for _, current := range currents {
		if strings.TrimSpace(current.BuildID) == "" {
			continue
		}
		depName := workerdeployment.ResolveDeploymentName(current.DeploymentName, current.TaskQueue)
		existing, err := opts.currentBuild(ctx, wdc, depName)
		if err != nil {
			return fmt.Errorf("read current build for %q: %w", depName, err)
		}
		if existing == current.BuildID {
			log.Info("build already current", "build", current.BuildID, "ontap", current.OntapVersion)
		} else if existing != "" {
			return fmt.Errorf("deployment %q current is %q; set drainingBuilds to move current to %q", depName, existing, current.BuildID)
		} else {
			log.Info("setting initial current", "build", current.BuildID, "ontap", current.OntapVersion)
			waitCtx, waitCancel := context.WithTimeout(ctx, pollingTimeout)
			err = opts.waitForPolling(waitCtx, wdc, current.TaskQueue, depName, current.BuildID, pollEvery)
			waitCancel()
			if err != nil {
				return fmt.Errorf("wait for build %q polling on %q: %w", current.BuildID, depName, err)
			}
			if opts.dryRun {
				log.Info("DRY-RUN: would set initial current", "build", current.BuildID)
				continue
			}
			if err := opts.promote(ctx, wdc, depName, current.BuildID); err != nil {
				return fmt.Errorf("set initial current %q on %q: %w", current.BuildID, depName, err)
			}
		}
		if opts.dryRun {
			continue
		}
		if err := k8s.recordFinishedCutover(ctx, current.ReleaseName, current.OntapVersion, current.BuildID, initialCutoverMarker); err != nil {
			return fmt.Errorf("record initial current for %q: %w", current.OntapVersion, err)
		}
	}
	return nil
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
		finished, err := cutoverFinished(ctx, wdc, depName, target.NewBuildID, target.OldBuildID, opts.currentBuild, opts.buildDrained)
		if err != nil {
			return fmt.Errorf("read cutover state for %q: %w", depName, err)
		}
		if finished {
			log.Info("cutover already finished, skip promote-and-retire", "workerDeployment", depName, "build", target.NewBuildID, "old", target.OldBuildID)
			if !opts.dryRun {
				if err := k8s.recordFinishedCutover(ctx, target.ReleaseName, target.OntapVersion, target.NewBuildID, target.OldBuildID); err != nil {
					return fmt.Errorf("record finished cutover for %q: %w", target.OntapVersion, err)
				}
			}
			continue
		}
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
		} else if already, err := buildAlreadyCurrent(ctx, wdc, depName, target.NewBuildID, opts.currentBuild); err != nil {
			log.Error("read current build failed", "error", err)
			continue
		} else if already {
			// SetCurrentVersion on an already-current build writes DrainageStatus
			// draining onto the deployment summary on Temporal 1.29.
			log.Info("build already current, skip set-current", "build", target.NewBuildID)
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
				if err := k8s.recordFinishedCutover(ctx, target.ReleaseName, target.OntapVersion, target.NewBuildID, target.OldBuildID); err != nil {
					return fmt.Errorf("record finished cutover for %q: %w", target.OntapVersion, err)
				}
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
		if err != nil && !errors.Is(err, errDeploymentNotFound) {
			return 0, err
		}
		if err == nil && ready && replicas > 0 {
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
