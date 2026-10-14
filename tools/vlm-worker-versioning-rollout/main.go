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
	"go.temporal.io/api/serviceerror"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
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
	mode := os.Getenv("MODE")
	log.Info("rollout starting", "mode", mode)
	code := 1
	switch mode {
	case "ensure-drain":
		code = runEnsureDrain(ctx, log)
	case "promote-and-retire":
		code = runPromoteAndRetire(ctx, log)
	case modeDrainAndRetire:
		code = runDrainAndRetire(ctx, log)
	default:
		log.Error("unknown or missing MODE", "mode", mode)
	}
	log.Info("rollout finished", "mode", mode, "exitCode", code)
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
	log.Info("istio sidecar not present")
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
	tc, err := dialTemporal(ctx, log)
	if err != nil {
		log.Error("temporal dial failed", "error", err)
		return 1
	}
	defer tc.Close()
	wdc := workerdeployment.FromTemporal(tc)

	poll := time.Duration(envInt("POLL_EVERY_SECONDS", 15)) * time.Second
	timeout := time.Duration(envInt("DRAIN_READY_TIMEOUT_MINUTES", 15)) * time.Minute
	if err := ensureDrainTargets(ctx, k8s, wdc, spec.Targets, poll, timeout, retryingWaitForPolling(log), retryingCurrentBuild(log, poll), retryingBuildDrained(log, poll), log); err != nil {
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

// pinUnversionedFunc pins running unversioned workflows on a task queue to oldBuildID.
type pinUnversionedFunc func(ctx context.Context, taskQueue, deploymentName, oldBuildID string, pollEvery time.Duration) error

// listUnversionedFunc returns workflow IDs on taskQueue that are not using worker versioning.
type listUnversionedFunc func(ctx context.Context, taskQueue string) ([]string, error)

type promoteAndRetireOptions struct {
	dryRun                  bool
	deleteAfter             bool
	pollEvery               time.Duration
	readyTimeout            time.Duration
	drainTimeout            time.Duration
	pollingTimeout          time.Duration
	waitForPolling          waitForPollingFunc
	promote                 promoteFunc
	currentBuild            currentBuildFunc
	buildDrained            buildDrainedFunc
	waitForDrain            waitForDrainFunc
	deleteBuild             deleteBuildFunc
	pinUnversionedWorkflows bool
	pinUnversioned          pinUnversionedFunc
	listUnversioned         listUnversionedFunc
	// handoffDrain starts the drain wait outside this process. Helm waits for
	// the promote hook, and that wait is only 20 minutes while drainage can
	// take hours. Nil keeps the wait in-process.
	handoffDrain func(context.Context, []RolloutTarget) error
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

	tc, err := dialTemporal(ctx, log)
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

	pollEvery := time.Duration(envInt("POLL_EVERY_SECONDS", 15)) * time.Second
	opts := promoteAndRetireOptions{
		dryRun:                  os.Getenv("DRY_RUN") == "true",
		deleteAfter:             os.Getenv("DELETE_VERSION_AFTER_DRAIN") == "true",
		pinUnversionedWorkflows: os.Getenv("PIN_UNVERSIONED_WORKFLOWS") == "true",
		pollEvery:               pollEvery,
		readyTimeout:            time.Duration(envInt("DRAIN_READY_TIMEOUT_MINUTES", 15)) * time.Minute,
		drainTimeout:            time.Duration(envInt("DRAIN_TIMEOUT_MINUTES", 360)) * time.Minute,
		pollingTimeout:          10 * time.Minute,
		waitForPolling:          retryingWaitForPolling(log),
		promote:                 retryingPromote(log, pollEvery),
		currentBuild:            retryingCurrentBuild(log, pollEvery),
		buildDrained:            retryingBuildDrained(log, pollEvery),
		waitForDrain:            retryingWaitForDrain(log),
		deleteBuild:             retryingDeleteBuild(log, pollEvery),
		handoffDrain: func(ctx context.Context, targets []RolloutTarget) error {
			return k8s.ensureBackgroundDrainJob(ctx, drainJobNameFor(targets), os.Getenv("ROLLOUT_IMAGE"), os.Getenv("SERVICE_ACCOUNT"), drainJobEnv())
		},
		pinUnversioned: func(ctx context.Context, taskQueue, deploymentName, oldBuildID string, pollEvery time.Duration) error {
			return pinUnversionedWorkflows(ctx, tc, taskQueue, deploymentName, oldBuildID, pollEvery)
		},
		listUnversioned: func(ctx context.Context, taskQueue string) ([]string, error) {
			return listUnversionedWorkflowIDs(ctx, tc, taskQueue)
		},
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

const (
	modeDrainAndRetire = "drain-and-retire"
	drainJobPrefix     = "vlm-worker-versioning-drain"
)

// drainJobNameFor names the background Job after the builds it retires.
// A later release with a different old build gets a new Job, so a finished
// drain from the previous cutover does not block the next one.
func drainJobNameFor(targets []RolloutTarget) string {
	parts := []string{drainJobPrefix}
	for _, target := range targets {
		if target.OldBuildID == "" {
			continue
		}
		part := strings.ToLower(target.OldBuildID)
		part = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return '-'
		}, part)
		parts = append(parts, strings.Trim(part, "-"))
	}
	name := strings.Join(parts, "-")
	if len(name) > 63 {
		name = strings.TrimRight(name[:63], "-")
	}
	return name
}

// drainJobEnv is the promote hook's environment with MODE switched to the
// background drain Job. That Job is not a Helm hook, so Helm does not wait for it.
func drainJobEnv() []jobEnv {
	keys := []string{
		"NAMESPACE",
		"ROLLOUT_SPEC",
		"TEMPORAL_HOST",
		"TEMPORAL_PORT",
		"TEMPORAL_NAMESPACE",
		"POLL_EVERY_SECONDS",
		"DRAIN_READY_TIMEOUT_MINUTES",
		"DRAIN_TIMEOUT_MINUTES",
		"DRY_RUN",
		"DELETE_VERSION_AFTER_DRAIN",
	}
	env := make([]jobEnv, 0, len(keys)+1)
	env = append(env, jobEnv{Name: "MODE", Value: modeDrainAndRetire})
	for _, key := range keys {
		env = append(env, jobEnv{Name: key, Value: os.Getenv(key)})
	}
	return env
}

func runDrainAndRetire(ctx context.Context, log *slog.Logger) int {
	spec, err := loadSpec()
	if err != nil {
		log.Error("load ROLLOUT_SPEC failed", "error", err)
		return 1
	}
	tc, err := dialTemporal(ctx, log)
	if err != nil {
		log.Error("temporal dial failed", "error", err)
		return 1
	}
	defer tc.Close()
	k8s, err := newK8sClient(os.Getenv("NAMESPACE"))
	if err != nil {
		log.Error("k8s client failed", "error", err)
		return 1
	}
	pollEvery := time.Duration(envInt("POLL_EVERY_SECONDS", 15)) * time.Second
	opts := promoteAndRetireOptions{
		deleteAfter:  os.Getenv("DELETE_VERSION_AFTER_DRAIN") == "true",
		pollEvery:    pollEvery,
		drainTimeout: time.Duration(envInt("DRAIN_TIMEOUT_MINUTES", 360)) * time.Minute,
		currentBuild: retryingCurrentBuild(log, pollEvery),
		buildDrained: retryingBuildDrained(log, pollEvery),
		waitForDrain: retryingWaitForDrain(log),
		deleteBuild:  retryingDeleteBuild(log, pollEvery),
	}
	if err := drainAndRetireTargets(ctx, k8s, workerdeployment.FromTemporal(tc), spec.Targets, opts, log); err != nil {
		log.Error("drain-and-retire failed", "error", err)
		return 1
	}
	return 0
}

func drainAndRetireTargets(
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
	drainTimeout := opts.drainTimeout
	if drainTimeout <= 0 {
		drainTimeout = 360 * time.Minute
	}
	for _, target := range targets {
		if target.NewBuildID == "" || target.OldBuildID == "" {
			continue
		}
		depName := workerdeployment.ResolveDeploymentName(target.DeploymentName, target.TaskQueue)
		if opts.currentBuild != nil {
			current, err := opts.currentBuild(ctx, wdc, depName)
			if err != nil {
				return fmt.Errorf("read current build for %q: %w", depName, err)
			}
			if current != target.NewBuildID {
				log.Info("new build is not current, skip drain", "workerDeployment", depName, "build", target.NewBuildID)
				continue
			}
		}
		finished, err := cutoverFinished(ctx, wdc, depName, target.NewBuildID, target.OldBuildID, opts.currentBuild, opts.buildDrained)
		if err != nil {
			return fmt.Errorf("read cutover state for %q: %w", depName, err)
		}
		if finished {
			log.Info("cutover already finished, skip drain", "workerDeployment", depName, "build", target.NewBuildID)
			if err := k8s.recordFinishedCutover(ctx, target.ReleaseName, target.OntapVersion, target.NewBuildID, target.OldBuildID); err != nil {
				return fmt.Errorf("record finished cutover for %q: %w", target.OntapVersion, err)
			}
			continue
		}
		log.Info("drain-and-retire", "workerDeployment", depName, "old", target.OldBuildID)
		if err := retireDrainedTarget(ctx, k8s, wdc, depName, target, opts, pollEvery, drainTimeout, true, log); err != nil {
			return err
		}
	}
	return nil
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

	var pendingDrain []RolloutTarget
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

		// Pinning unversioned workflows is off unless PIN_UNVERSIONED_WORKFLOWS=true.
		// With the flag off, this job promotes the new build directly, as before.
		if opts.pinUnversionedWorkflows {
			if err := pinUnversionedBeforePromote(ctx, wdc, depName, target, opts, pollEvery, log); err != nil {
				return err
			}
		} else {
			log.Info("pin unversioned workflows disabled", "workerDeployment", depName)
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

		// Helm's deployer waits 20 minutes for this hook. Drainage can take hours,
		// so the hook hands that wait to a Job Helm does not track.
		if opts.handoffDrain != nil && !opts.dryRun {
			pendingDrain = append(pendingDrain, target)
			continue
		}
		if err := retireDrainedTarget(ctx, k8s, wdc, depName, target, opts, pollEvery, drainTimeout, false, log); err != nil {
			return err
		}
	}
	if len(pendingDrain) > 0 {
		log.Info("drain continues outside helm", "job", drainJobNameFor(pendingDrain))
		if err := opts.handoffDrain(ctx, pendingDrain); err != nil {
			return fmt.Errorf("start drain job: %w", err)
		}
	}
	return nil
}

// retireDrainedTarget waits for oldBuildID to drain, scales its Deployment to 0,
// and records the cutover. failOnDrainWait makes a background drain Job exit
// non-zero so a later upgrade can start it again.
func retireDrainedTarget(
	ctx context.Context,
	k8s retireResourceClient,
	wdc workerdeployment.Client,
	depName string,
	target RolloutTarget,
	opts promoteAndRetireOptions,
	pollEvery time.Duration,
	drainTimeout time.Duration,
	failOnDrainWait bool,
	log *slog.Logger,
) error {
	drainCtx, drainCancel := context.WithTimeout(ctx, drainTimeout)
	defer drainCancel()
	if opts.dryRun {
		log.Info("DRY-RUN: would wait for drain", "build", target.OldBuildID)
	} else if err := opts.waitForDrain(drainCtx, wdc, depName, target.OldBuildID, pollEvery); err != nil {
		log.Error("wait for drain failed — leaving drain pods up", "build", target.OldBuildID, "error", err)
		if failOnDrainWait {
			return err
		}
		return nil
	} else if !opts.dryRun {
		log.Info("old build drained", "build", target.OldBuildID)
	}

	if target.DrainDeployment != "" {
		if opts.dryRun {
			log.Info("DRY-RUN: would scale drain to 0", "deployment", target.DrainDeployment)
		} else if err := k8s.scaleDeployment(ctx, target.DrainDeployment, 0); err != nil {
			log.Error("scale drain to 0 failed", "deployment", target.DrainDeployment, "error", err)
			if failOnDrainWait {
				return err
			}
		} else {
			log.Info("scaled drain to 0", "deployment", target.DrainDeployment)
			if err := k8s.recordFinishedCutover(ctx, target.ReleaseName, target.OntapVersion, target.NewBuildID, target.OldBuildID); err != nil {
				return fmt.Errorf("record finished cutover for %q: %w", target.OntapVersion, err)
			}
		}
	}

	if opts.deleteAfter && !opts.dryRun {
		if err := opts.deleteBuild(ctx, wdc, depName, target.OldBuildID); err != nil {
			log.Error("DeleteVersion failed (optional)", "build", target.OldBuildID, "error", err)
		} else {
			log.Info("deleted old build version", "workerDeployment", depName, "build", target.OldBuildID)
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

const pinUnversionedStableEmptyPasses = 2

type workflowExecutionPinner interface {
	ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error)
	UpdateWorkflowExecutionOptions(ctx context.Context, options client.UpdateWorkflowExecutionOptionsRequest) (client.WorkflowExecutionOptions, error)
}

// pinUnversionedBeforePromote runs only when the pin flag is on.
// A first versioned rollout starts with Current set to unversioned (an empty
// build ID). Make the old build Current before pinning or promoting the new
// build. The old build uses the same binary as the unversioned worker, so any
// workflow task dispatched during this migration replays against compatible code.
// Pinning to the old build is only safe while Current is unset or is still the
// old build. If Current is already the new build, a workflow that has already
// run there must not be sent back to the old binary.
func pinUnversionedBeforePromote(
	ctx context.Context,
	wdc workerdeployment.Client,
	depName string,
	target RolloutTarget,
	opts promoteAndRetireOptions,
	pollEvery time.Duration,
	log *slog.Logger,
) error {
	currentIsNew := false
	if opts.currentBuild != nil {
		current, err := opts.currentBuild(ctx, wdc, depName)
		if err != nil {
			return fmt.Errorf("read current build for %q: %w", depName, err)
		}
		log.Info("read current build", "workerDeployment", depName, "current", current)
		switch current {
		case "":
			if opts.dryRun {
				log.Info("DRY-RUN: would set old build as migration current", "build", target.OldBuildID)
			} else if err := opts.promote(ctx, wdc, depName, target.OldBuildID); err != nil {
				return fmt.Errorf("set old build %q as migration current on %q: %w", target.OldBuildID, depName, err)
			} else {
				log.Info("set old build as migration current", "build", target.OldBuildID)
			}
		case target.OldBuildID:
			log.Info("current is the old build", "workerDeployment", depName, "build", current)
		case target.NewBuildID:
			currentIsNew = true
		default:
			return fmt.Errorf(
				"deployment %q current is %q; expected unversioned, old build %q, or new build %q",
				depName,
				current,
				target.OldBuildID,
				target.NewBuildID,
			)
		}
	}

	if currentIsNew {
		return refusePinBackToOldBuild(ctx, target, opts, pollEvery, log)
	}
	if opts.dryRun {
		log.Info("DRY-RUN: would pin unversioned workflows", "build", target.OldBuildID, "taskQueue", target.TaskQueue)
		return nil
	}
	if opts.pinUnversioned == nil {
		return nil
	}
	// Pin before set-current. Prod workflows on an unversioned poller have no
	// build id, so the versioned old-image drain pod would otherwise not receive
	// them. The next task would run on the new build and could nondeterministically
	// replay. Pinning to the old build keeps that history on the same binary.
	// The scan continues until two empty lists. The caller then promotes the new
	// build. A failed visibility list is retried up to 3 times inside that scan.
	if err := opts.pinUnversioned(ctx, target.TaskQueue, depName, target.OldBuildID, pollEvery); err != nil {
		return fmt.Errorf("pin unversioned workflows on %q to %q: %w", target.TaskQueue, target.OldBuildID, err)
	}
	return nil
}

// refusePinBackToOldBuild stops a resume that would pin workflows onto the old
// build after Current is already the new build. It returns nil only after two
// empty lists, pollEvery apart. A workflow that shows up on either list is
// returned to the operator instead of being pinned backward.
func refusePinBackToOldBuild(ctx context.Context, target RolloutTarget, opts promoteAndRetireOptions, pollEvery time.Duration, log *slog.Logger) error {
	if opts.dryRun {
		log.Info("DRY-RUN: current is already the new build; would stop if unversioned workflows remain", "build", target.NewBuildID)
		return nil
	}
	if opts.listUnversioned == nil {
		return fmt.Errorf("current is already %q; cannot list unversioned workflows on %q", target.NewBuildID, target.TaskQueue)
	}
	if pollEvery <= 0 {
		pollEvery = time.Millisecond
	}
	emptyPasses := 0
	listFailures := 0
	for {
		ids, err := opts.listUnversioned(ctx, target.TaskQueue)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("list unversioned workflows on %q: %w", target.TaskQueue, ctx.Err())
			}
			listFailures++
			if listFailures > temporalMaxRetries {
				return fmt.Errorf("list unversioned workflows on %q: %w", target.TaskQueue, err)
			}
			log.Warn("list unversioned workflows failed, will retry", "taskQueue", target.TaskQueue, "attempt", listFailures, "error", err)
			emptyPasses = 0
		} else if len(ids) > 0 {
			return fmt.Errorf("current is already %q; refusing to pin unversioned workflows [%s] back to %q", target.NewBuildID, strings.Join(ids, ", "), target.OldBuildID)
		} else {
			listFailures = 0
			emptyPasses++
			if emptyPasses >= pinUnversionedStableEmptyPasses {
				break
			}
		}
		timer := time.NewTimer(pollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("list unversioned workflows on %q: %w", target.TaskQueue, ctx.Err())
		case <-timer.C:
		}
	}
	log.Info("current is already the new build and no unversioned workflows remain", "build", target.NewBuildID)
	return nil
}

// unversionedWorkflowQuery lists running workflows on taskQueue that are not
// using worker versioning. TemporalWorkflowVersioningBehavior is null for
// those executions. BuildIds is not used: server 1.32 stops writing it.
func unversionedWorkflowQuery(taskQueue string) string {
	return fmt.Sprintf(`ExecutionStatus="Running" AND TaskQueue=%q AND TemporalWorkflowVersioningBehavior IS NULL`, taskQueue)
}

func listUnversionedWorkflowIDs(ctx context.Context, tc workflowExecutionPinner, taskQueue string) ([]string, error) {
	query := unversionedWorkflowQuery(taskQueue)
	var ids []string
	var pageToken []byte
	for {
		resp, err := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         query,
			NextPageToken: pageToken,
			PageSize:      200,
		})
		if err != nil {
			return nil, fmt.Errorf("list unversioned workflows: %w", err)
		}
		for _, execution := range resp.GetExecutions() {
			workflowID := execution.GetExecution().GetWorkflowId()
			if workflowID == "" {
				continue
			}
			ids = append(ids, workflowID)
		}
		if len(resp.GetNextPageToken()) == 0 {
			return ids, nil
		}
		pageToken = resp.GetNextPageToken()
	}
}

// pinUnversionedWorkflows pins every running unversioned workflow on taskQueue
// to deploymentName/oldBuildID. A pin error on one workflow, or a failed
// visibility list, is retried on the next scan, up to temporalMaxRetries.
// It returns nil only after two consecutive scans that find nothing left to
// pin, so a workflow that shows up late is pinned before the caller promotes
// the new build. The caller must not promote when this returns an error.
func pinUnversionedWorkflows(
	ctx context.Context,
	tc workflowExecutionPinner,
	taskQueue, deploymentName, oldBuildID string,
	pollEvery time.Duration,
) error {
	if pollEvery <= 0 {
		pollEvery = time.Millisecond
	}
	query := unversionedWorkflowQuery(taskQueue)
	emptyPasses := 0
	failures := 0
	for {
		pinned, failed, err := pinUnversionedPage(ctx, tc, query, deploymentName, oldBuildID)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("wait for unversioned workflows to be pinned on %q: %w", taskQueue, ctx.Err())
			}
			failures++
			if failures > temporalMaxRetries {
				return fmt.Errorf("list unversioned workflows on %q: %w", taskQueue, err)
			}
			slog.Warn("list unversioned workflows failed, will retry", "taskQueue", taskQueue, "attempt", failures, "error", err)
			emptyPasses = 0
		} else if failed > 0 {
			failures++
			emptyPasses = 0
			slog.Info("pin scan", "taskQueue", taskQueue, "pinned", pinned, "failed", failed, "attempt", failures)
			if failures > temporalMaxRetries {
				return fmt.Errorf("pin unversioned workflows on %q failed after %d retries", taskQueue, temporalMaxRetries)
			}
		} else if pinned == 0 {
			failures = 0
			emptyPasses++
			slog.Info("pin scan empty", "taskQueue", taskQueue, "emptyPasses", emptyPasses)
			if emptyPasses >= pinUnversionedStableEmptyPasses {
				slog.Info("unversioned workflows pinned", "taskQueue", taskQueue, "build", oldBuildID)
				return nil
			}
		} else {
			failures = 0
			emptyPasses = 0
			slog.Info("pin scan", "taskQueue", taskQueue, "pinned", pinned, "failed", failed)
		}
		timer := time.NewTimer(pollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for unversioned workflows to be pinned on %q: %w", taskQueue, ctx.Err())
		case <-timer.C:
		}
	}
}

func pinUnversionedPage(ctx context.Context, tc workflowExecutionPinner, query, deploymentName, oldBuildID string) (int, int, error) {
	pinned := 0
	failed := 0
	var pageToken []byte
	for {
		resp, err := tc.ListWorkflow(ctx, &workflowservice.ListWorkflowExecutionsRequest{
			Query:         query,
			NextPageToken: pageToken,
			PageSize:      200,
		})
		if err != nil {
			return pinned, failed, fmt.Errorf("list unversioned workflows: %w", err)
		}
		for _, execution := range resp.GetExecutions() {
			info := execution.GetExecution()
			if info.GetWorkflowId() == "" {
				continue
			}
			_, err := tc.UpdateWorkflowExecutionOptions(ctx, client.UpdateWorkflowExecutionOptionsRequest{
				WorkflowId: info.GetWorkflowId(),
				RunId:      info.GetRunId(),
				WorkflowExecutionOptionsChanges: client.WorkflowExecutionOptionsChanges{
					VersioningOverride: &client.VersioningOverrideChange{
						Value: &client.PinnedVersioningOverride{
							Version: worker.WorkerDeploymentVersion{
								DeploymentName: deploymentName,
								BuildID:        oldBuildID,
							},
						},
					},
				},
			})
			if err != nil {
				var notFound *serviceerror.NotFound
				if errors.As(err, &notFound) {
					slog.Info("workflow not found, skip pin", "workflowID", info.GetWorkflowId(), "error", err)
					continue
				}
				failed++
				slog.Warn("pin workflow failed, will retry", "workflowID", info.GetWorkflowId(), "error", err)
				continue
			}
			slog.Info("pinned workflow", "workflowID", info.GetWorkflowId(), "runID", info.GetRunId(), "workerDeployment", deploymentName, "build", oldBuildID)
			pinned++
		}
		if len(resp.GetNextPageToken()) == 0 {
			return pinned, failed, nil
		}
		pageToken = resp.GetNextPageToken()
	}
}

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

// temporalMaxRetries is how many times a failed Temporal call is tried again.
// The first call is not a retry, so the call runs at most 4 times.
const temporalMaxRetries = 3

// retryTemporal repeats a Temporal call until it succeeds, the retry limit is
// hit, or ctx ends. A canceled RPC is not the same as the Job context ending.
func retryTemporal(ctx context.Context, pollEvery time.Duration, log *slog.Logger, op string, call func() error) error {
	if pollEvery <= 0 {
		pollEvery = 15 * time.Second
	}
	var err error
	for attempt := 0; ; attempt++ {
		err = call()
		if err == nil {
			if attempt > 0 {
				log.Info("temporal call succeeded", "op", op, "attempts", attempt+1)
			}
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
		if attempt >= temporalMaxRetries {
			log.Warn("temporal call failed", "op", op, "attempts", attempt+1, "error", err)
			return err
		}
		log.Warn("temporal call failed, will retry", "op", op, "attempt", attempt+1, "error", err)
		timer := time.NewTimer(pollEvery)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func dialTemporal(ctx context.Context, log *slog.Logger) (client.Client, error) {
	addr := fmt.Sprintf("%s:%s", envOr("TEMPORAL_HOST", "temporal-frontend.temporal.svc.cluster.local"), envOr("TEMPORAL_PORT", "7233"))
	namespace := envOr("TEMPORAL_NAMESPACE", "default")
	pollEvery := time.Duration(envInt("POLL_EVERY_SECONDS", 15)) * time.Second
	var tc client.Client
	err := retryTemporal(ctx, pollEvery, log, "dial", func() error {
		dialed, dialErr := client.Dial(client.Options{HostPort: addr, Namespace: namespace})
		if dialErr != nil {
			if dialed != nil {
				dialed.Close()
			}
			return dialErr
		}
		tc = dialed
		return nil
	})
	if err != nil {
		return nil, err
	}
	log.Info("temporal dial succeeded", "address", addr, "namespace", namespace)
	return tc, nil
}

func retryingWaitForPolling(log *slog.Logger) waitForPollingFunc {
	return func(ctx context.Context, wdc workerdeployment.Client, taskQueue, deploymentName, buildID string, pollEvery time.Duration) error {
		return retryTemporal(ctx, pollEvery, log, "wait for polling "+buildID, func() error {
			return workerdeployment.WaitForPolling(ctx, wdc, taskQueue, deploymentName, buildID, pollEvery)
		})
	}
}

func retryingPromote(log *slog.Logger, pollEvery time.Duration) promoteFunc {
	return func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string) error {
		return retryTemporal(ctx, pollEvery, log, "promote "+buildID, func() error {
			// Read Current before SetCurrentVersion. A lost success plus a failed
			// describe must retry the describe, not set Current again. On Temporal
			// 1.29 a second SetCurrentVersion writes DrainageStatus draining.
			current, curErr := workerdeployment.CurrentBuildID(ctx, wdc, deploymentName)
			if curErr != nil {
				return curErr
			}
			if current == buildID {
				log.Info("build already current, skip set-current", "workerDeployment", deploymentName, "build", buildID)
				return nil
			}
			return workerdeployment.PromoteBuild(ctx, wdc, deploymentName, buildID)
		})
	}
}

func retryingCurrentBuild(log *slog.Logger, pollEvery time.Duration) currentBuildFunc {
	return func(ctx context.Context, wdc workerdeployment.Client, deploymentName string) (string, error) {
		var buildID string
		err := retryTemporal(ctx, pollEvery, log, "describe deployment "+deploymentName, func() error {
			var callErr error
			buildID, callErr = workerdeployment.CurrentBuildID(ctx, wdc, deploymentName)
			return callErr
		})
		return buildID, err
	}
}

func retryingBuildDrained(log *slog.Logger, pollEvery time.Duration) buildDrainedFunc {
	return func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string) (bool, error) {
		var drained bool
		err := retryTemporal(ctx, pollEvery, log, "describe version "+buildID, func() error {
			var callErr error
			drained, callErr = workerdeployment.IsBuildDrained(ctx, wdc, deploymentName, buildID)
			return callErr
		})
		return drained, err
	}
}

func retryingWaitForDrain(log *slog.Logger) waitForDrainFunc {
	return func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string, pollEvery time.Duration) error {
		if pollEvery <= 0 {
			pollEvery = 15 * time.Second
		}
		// Retry each describe. Wrapping WaitForDrain would spend the 3 retries
		// on the whole multi-hour wait, so a few transient errors fail the Job.
		ticker := time.NewTicker(pollEvery)
		defer ticker.Stop()
		for {
			var drained bool
			err := retryTemporal(ctx, pollEvery, log, "wait for drain "+buildID, func() error {
				var callErr error
				drained, callErr = workerdeployment.IsBuildDrained(ctx, wdc, deploymentName, buildID)
				return callErr
			})
			if err != nil {
				return err
			}
			if drained {
				return nil
			}
			log.Info("build is not drained yet", "workerDeployment", deploymentName, "build", buildID)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
}

func retryingDeleteBuild(log *slog.Logger, pollEvery time.Duration) deleteBuildFunc {
	return func(ctx context.Context, wdc workerdeployment.Client, deploymentName, buildID string) error {
		return retryTemporal(ctx, pollEvery, log, "delete version "+buildID, func() error {
			err := workerdeployment.DeleteBuild(ctx, wdc, deploymentName, buildID)
			if err == nil {
				return nil
			}
			var notFound *serviceerror.NotFound
			if errors.As(err, &notFound) {
				return nil
			}
			return err
		})
	}
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
