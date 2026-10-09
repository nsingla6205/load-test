package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/metricsinterface"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/flexcache_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/jobmanageractivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/resource_events_activities"
	orchcommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources/ccfe"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/trial"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows/background_kms_workflows"
	expertmodeworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/expertMode"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/flexcache_workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/jobmanagerworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/kms_workflows"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/replicationWorkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	database2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/connection"
	metricsdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/metrics"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
	tManagerPkg "github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/temporalmanager"
	workflowEngine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	workflowutil "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/temporalbackend"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	workerSDK "go.temporal.io/sdk/worker"
	"golang.org/x/sync/errgroup"
)

const (
	harvestMetricsRestoreInterval = time.Minute
	harvestMetricsRestoreTimeout  = 10 * time.Second
)

func init() {
	// Register the workflow executor to break circular dependency
	// This must be initialized before any activities that use ExecuteWorkflowSequentially
	backgroundactivities.RegisterWorkflowExecutor(workflows.ExecuteWorkflowSequentially)
}

// SDK default deadlock detection is 1s. Zero would also mean "use SDK default"
// (or unlimited when TEMPORAL_DEBUG is set), so callers pass a positive duration.
const defaultDeadlockDetectionTimeout = 2 * time.Second

const (
	workflowPanicPolicyBlock = "BlockWorkflow"
	workflowPanicPolicyFail  = "FailWorkflow"
)

// parseWorkflowPanicPolicy maps CUSTOMER/BACKGROUND_WORKER_WORKFLOW_PANIC_POLICY to a Temporal policy.
// Empty values use BlockWorkflow so a transient panic (for example TMPRL1101 from a slow process slog
// write) retries the workflow task instead of failing the execution. FailWorkflow restores fail-fast.
func parseWorkflowPanicPolicy(value string) (workerSDK.WorkflowPanicPolicy, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", strings.ToLower(workflowPanicPolicyBlock):
		return workerSDK.BlockWorkflow, nil
	case strings.ToLower(workflowPanicPolicyFail):
		return workerSDK.FailWorkflow, nil
	default:
		return workerSDK.BlockWorkflow, fmt.Errorf("invalid workflow panic policy %q: want %s or %s", value, workflowPanicPolicyBlock, workflowPanicPolicyFail)
	}
}

func workflowPanicPolicyName(policy workerSDK.WorkflowPanicPolicy) string {
	if policy == workerSDK.FailWorkflow {
		return workflowPanicPolicyFail
	}
	return workflowPanicPolicyBlock
}

func mustParseWorkflowPanicPolicy(value string, logger log.Logger) workerSDK.WorkflowPanicPolicy {
	policy, err := parseWorkflowPanicPolicy(value)
	if err != nil {
		logger.Error("Invalid workflow panic policy", "error", err.Error())
		os.Exit(1)
	}
	return policy
}

// buildWorkerOptions constructs a worker.Options value from the supplied concurrency, rate-limit,
// deadlock, and panic-policy parameters.
func buildWorkerOptions(maxActivity, maxWorkflow, maxLocalActivity int, activitiesPerSec, taskQueuePerSec float64, deadlockTimeout time.Duration, panicPolicy workerSDK.WorkflowPanicPolicy) workerSDK.Options {
	if deadlockTimeout <= 0 {
		deadlockTimeout = defaultDeadlockDetectionTimeout
	}
	return workerSDK.Options{
		MaxConcurrentActivityExecutionSize:      maxActivity,
		MaxConcurrentWorkflowTaskExecutionSize:  maxWorkflow,
		MaxConcurrentLocalActivityExecutionSize: maxLocalActivity,
		WorkerActivitiesPerSecond:               activitiesPerSec,
		TaskQueueActivitiesPerSecond:            taskQueuePerSec,
		WorkflowPanicPolicy:                     panicPolicy,
		DeadlockDetectionTimeout:                deadlockTimeout,
		Interceptors:                            []interceptor.WorkerInterceptor{workflowutil.NewLoggerInterceptor()},
	}
}

// newWorkerWithOpts builds worker options from the supplied concurrency/rate-limit parameters,
// logs them, and constructs the Temporal worker for the given task queue.
func newWorkerWithOpts(c client.Client, taskQueue string, maxActivity, maxWorkflow, maxLocalActivity int, activitiesPerSec, taskQueuePerSec float64, deadlockTimeout time.Duration, panicPolicy workerSDK.WorkflowPanicPolicy, logger log.Logger) *tManagerPkg.Worker {
	opts := buildWorkerOptions(maxActivity, maxWorkflow, maxLocalActivity, activitiesPerSec, taskQueuePerSec, deadlockTimeout, panicPolicy)
	logger.Info("worker concurrency options",
		"taskQueue", taskQueue,
		"maxConcurrentActivity", opts.MaxConcurrentActivityExecutionSize,
		"maxConcurrentWorkflowTask", opts.MaxConcurrentWorkflowTaskExecutionSize,
		"maxConcurrentLocalActivity", opts.MaxConcurrentLocalActivityExecutionSize,
		"workerActivitiesPerSec", opts.WorkerActivitiesPerSecond,
		"taskQueueActivitiesPerSec", opts.TaskQueueActivitiesPerSecond,
		"deadlockDetectionTimeout", opts.DeadlockDetectionTimeout,
		"workflowPanicPolicy", workflowPanicPolicyName(opts.WorkflowPanicPolicy),
	)
	return tManagerPkg.NewWorker(c, taskQueue, opts)
}

func restoreHarvestMetrics(ctx context.Context, storage database.Storage, emitter metricsinterface.HarvestMetricsEmitter, logger log.Logger) {
	config, err := storage.GetAppConfig(ctx, metricsinterface.HarvestReconcileStateAppConfigKey)
	if err != nil {
		if isCheckpointNotFoundErr(err) {
			logger.Debug("Harvest metrics checkpoint unavailable", "error", err.Error())
			return
		}
		logger.Warn("Harvest metrics checkpoint unavailable", "error", err.Error())
		return
	}

	var state metricsinterface.HarvestReconcileState
	if json.Unmarshal([]byte(config.Value), &state) == nil && !state.LastSuccessfulRunStartAt.IsZero() {
		emitter.SetHarvestDriftSnapshot(metricsinterface.HarvestDriftStageDetected, state.DetectedSnapshot)
		emitter.SetHarvestDriftSnapshot(metricsinterface.HarvestDriftStageRemaining, state.RemainingSnapshot)
		emitter.SetHarvestRetryBacklog(state.PendingRetries, state.ExhaustedRetries)
		emitter.SetHarvestLastSuccessfulReconcile(state.LastSuccessfulRunStartAt)
		return
	}

	runStart, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(config.Value))
	if err != nil {
		logger.Warn("Harvest metrics checkpoint is invalid", "error", err.Error())
		return
	}
	emitter.SetHarvestLastSuccessfulReconcile(runStart)
}

func isCheckpointNotFoundErr(err error) bool {
	if utilerrors.IsNotFoundErr(err) {
		return true
	}
	var customErr *vsaerrors.CustomError
	return vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrResourceNotFound)
}

func restoreHarvestMetricsPeriodically(
	ctx context.Context,
	storage database.Storage,
	emitter metricsinterface.HarvestMetricsEmitter,
	logger log.Logger,
	interval time.Duration,
	timeout time.Duration,
) {
	if interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			restoreCtx, cancel := context.WithTimeout(ctx, timeout)
			restoreHarvestMetrics(restoreCtx, storage, emitter, logger)
			cancel()
		}
	}
}

// main is the entry point of the worker application. It initializes the Temporal worker,
// database connection, registers workflows and activities, and starts the worker.

func main() {
	logger := log.NewLogger()

	ctx := context.WithValue(context.Background(), utilsmiddleware.CorrelationContextKey, uuid.NewString())
	eg, ctx := errgroup.WithContext(ctx)

	workerType := env.GetString("WORKER_TASK_QUEUE", workflowEngine.CustomerWorkerType)

	logger.Info("Starting worker [taskQueue = " + workerType + "]")

	// Install the Temporal heartbeat hook so shared activities that record
	// heartbeats through the engine-agnostic orchestrator.RecordHeartbeat (e.g.
	// IdentifyVMs, CreateQoSPolicyAndApplyToSVM, which are also used by the VCM
	// go-workflows path) delegate to activity.RecordHeartbeat in this Temporal
	// worker. Without this the abstraction stays a no-op in this process.
	temporalbackend.SetupHeartbeat()

	// Fail fast on invalid OCI client-cert config instead of aborting pool creation later.
	if err := env.ValidateOCIClientCertConfig(); err != nil {
		logger.Error("Invalid OCI client certificate configuration", "error", err.Error())
		os.Exit(1)
	}

	// Fail fast on invalid OCI service (read-only) cert config for the same reason.
	if err := env.ValidateOCIServiceCertConfig(); err != nil {
		logger.Error("Invalid OCI service certificate configuration", "error", err.Error())
		os.Exit(1)
	}

	// Setup OpenTelemetry for metrics export
	shutdown, err := log.SetupOpenTelemetry(ctx)
	if err != nil {
		logger.Error("Failed to set up OpenTelemetry", "error", err.Error())
		os.Exit(1)
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			logger.Error("Failed to shutdown OpenTelemetry", "error", err.Error())
		}
	}()

	// Register GCP-only custom metrics (not used on OCI)
	if env.GetHyperscaler() != orchcommon.ProviderOCI {
		metrics.RegisterJobStatusCounter()
		metrics.RegisterAutoTierEnabledGauge()
		metrics.RegisterCRREnabledGauge()
		metrics.RegisterLargeVolumeEnabledGauge()
		metrics.RegisterCBSEnabledGauge()
		metrics.RegisterEligibilityStringGauge()
		metrics.RegisterBackupSizeGauge()
		metrics.RegisterCertificateRotationFailureCounter()
		metrics.RegisterPasswordRotationFailureCounter()
		metrics.RegisterZoneSwitchWorkflowCounter()
		metrics.RegisterKmsKeyLimitReachedCounter()
		metrics.RegisterKmsRotationFailureCounter()
		metrics.RegisterCmekBackupRewriteErrorGauge()
		metrics.RegisterTrialAccountSyncRunCounter()
		metrics.RegisterTrialAccountSyncAccountOutcomeCounter()
		metrics.RegisterTrialInfoCreateOutcomeCounter()
	} else {
		// OCI worker: register the OCI-specific certificate-rotation counters emitted by
		// the OCI rotation workflows so they are exported at /metrics. The GCP block above
		// is skipped on OCI. These are dedicated OCI counters (separate from the shared
		// GCP CertificateRotationFailureCounter) so they can carry the pool_external_identifier
		// (OCID) label; OCI no longer emits into the shared failure counter.
		metrics.RegisterOciCertificateRotationFailureCounter()
		metrics.RegisterOciCertificateRotationSuccessCounter()
	}

	// Start metrics HTTP server
	metricsPort := os.Getenv("METRICS_PORT")
	if metricsPort == "" {
		metricsPort = "9090"
	}
	eg.Go(func() error {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		server := &http.Server{
			Addr:         ":" + metricsPort,
			Handler:      mux,
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 10 * time.Second,
			IdleTimeout:  120 * time.Second,
		}
		logger.Info("Starting metrics server", "port", metricsPort)
		go func() {
			<-ctx.Done()
			logger.Info("Shutting down metrics server")
			if err := server.Shutdown(context.Background()); err != nil {
				logger.Error("Metrics server shutdown error", "error", err.Error())
			}
		}()
		return server.ListenAndServe()
	})

	// Create a Temporal client
	workflowClient, err := initializeTemporalClient(logger)
	if err != nil {
		logger.Error("Failed to initialize Temporal client", "error", err.Error())
		os.Exit(1)
	}

	// Initialize FetchTemporalClient function for use in activities
	workflowEngine.FetchTemporalClient = func() (client.Client, error) {
		return workflowClient.GetTemporalClient(), nil
	}

	// create database connection
	dbConn, err := database2.GetVcpDbConnection(ctx, logger)
	if err != nil {
		logger.Error("Failed to get database connection", "error", err.Error())
		os.Exit(1)
	}
	defer database2.CloseDatabase(dbConn, logger)
	logger.Info("Database connection established", "connection", dbConn)

	var harvestMetricsEmitter metricsinterface.HarvestMetricsEmitter
	if env.GetHyperscaler() != orchcommon.ProviderOCI && workerType == workflowEngine.BackgroundWorkerType {
		if err := metrics.RegisterHarvestMetrics(); err != nil {
			logger.Error("Failed to register Harvest metrics", "error", err.Error())
			os.Exit(1)
		}
		harvestMetricsEmitter = metrics.NewPrometheusHarvestMetricsEmitter()
		restoreHarvestMetrics(ctx, dbConn, harvestMetricsEmitter, logger)
		eg.Go(func() error {
			restoreHarvestMetricsPeriodically(
				ctx,
				dbConn,
				harvestMetricsEmitter,
				logger,
				harvestMetricsRestoreInterval,
				harvestMetricsRestoreTimeout,
			)
			return nil
		})
	}

	var telemetryDBConn metricsdb.Storage
	metricsDbCleanupEnabled := env.GetBool("METRICS_DB_CLEANUP_ENABLED", false)
	if metricsDbCleanupEnabled {
		// create database connection
		telemetryDBConn, err = database2.GetTelemetryDbConnection(ctx, logger)
		if err != nil {
			logger.Error("Failed to get telemetry database connection", "error", err.Error())
			os.Exit(1)
		}
		defer database2.CloseTelemetryDatabase(telemetryDBConn, logger)
		logger.Info("Telemetry Database connection established", "connection", telemetryDBConn)
	}

	// Initialize the temporal server client
	temporalManager := tManagerPkg.TemporalManager{
		Client:          workflowClient.GetTemporalClient(),
		Config:          workflowClient.LoadConfig(),
		DBConn:          dbConn,
		TelemetryDBConn: telemetryDBConn,
	}

	defer workflowClient.CloseClient(workflowClient.GetTemporalClient())

	// Initialise the error handler
	// TODO: add a flag to enable/disable the error handler
	// TODO: add middleware to handle error codes
	// Keeping errors.json in core for now, if needed we can merge two jsons together one in core and one in proxy layer later.
	_, err = vsaerrors.NewErrorHandler()
	if err != nil {
		logger.Error("Failed to create error handler", "error", err.Error())
		os.Exit(1)
	}

	err = env.ValidateEnvironmentVariables()
	if err != nil {
		logger.Error("Failed to validate environment variables", "error", err.Error())
		os.Exit(1)
	}

	// Validate certificate lifetime before starting the worker
	err = env.ValidateCertificateLifetime()
	if err != nil {
		logger.Error("Certificate lifetime validation failed", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("Certificate lifetime validation passed")

	var worker *tManagerPkg.Worker

	cloudProvider := env.GetHyperscaler()
	logger.Info("Cloud provider", "provider", cloudProvider)

	switch cloudProvider {
	case orchcommon.ProviderOCI:
		vlm.SetActiveProvider(vlm.OCICloud)
	case orchcommon.ProviderGCP:
		vlm.SetActiveProvider(vlm.GCPCloud)
	default:
		logger.Error("Unsupported cloud provider, cannot initialize VLM provider factory", "provider", cloudProvider)
		os.Exit(1)
	}
	logger.Info("VLM active provider set", "provider", cloudProvider)

	// Eagerly build the process-wide OCI clients before the worker starts serving
	// activities.
	if cloudProvider == orchcommon.ProviderOCI {
		if err := hyperscaler.InitSharedOCIClients(ctx); err != nil {
			logger.Error("Failed to initialize OCI clients at startup", "error", err.Error())
			os.Exit(1)
		}
		logger.Info("OCI clients initialized at startup")
	}

	switch workerType {
	case workflowEngine.CustomerWorkerType:
		worker = newWorkerWithOpts(
			temporalManager.GetClient(),
			workflowEngine.CustomerTaskQueue,
			env.CustomerWorkerMaxConcurrentActivityExecution,
			env.CustomerWorkerMaxConcurrentWorkflowTaskExecution,
			env.CustomerWorkerMaxConcurrentLocalActivityExecution,
			env.CustomerWorkerActivitiesPerSecond,
			env.CustomerWorkerTaskQueueActivitiesPerSecond,
			env.CustomerWorkerDeadlockDetectionTimeout,
			mustParseWorkflowPanicPolicy(env.CustomerWorkerWorkflowPanicPolicy, logger),
			logger,
		)

		if cloudProvider == orchcommon.ProviderOCI {
			if err := ociworkflows.ValidateOCIWorkerStartupEnv(); err != nil {
				logger.Error("OCI worker startup environment validation failed", "error", err.Error())
				os.Exit(1)
			}
			logger.Info("OCI worker startup environment validation passed")
			logger.Info("registering OCI customer workflows and activities")
			RegisterOCICustomerWorkflowsAndActivities(*worker, dbConn, workflowClient.GetTemporalClient())
		} else {
			logger.Info("registering customer workflows and activities")
			RegisterCustomerWorkflowsAndActivities(*worker, dbConn, workflowClient.GetTemporalClient())
		}

	case workflowEngine.BackgroundWorkerType:
		worker = newWorkerWithOpts(
			temporalManager.GetClient(),
			workflowEngine.BackgroundTaskQueue,
			env.BackgroundWorkerMaxConcurrentActivityExecution,
			env.BackgroundWorkerMaxConcurrentWorkflowTaskExecution,
			env.BackgroundWorkerMaxConcurrentLocalActivityExecution,
			env.BackgroundWorkerActivitiesPerSecond,
			env.BackgroundWorkerTaskQueueActivitiesPerSecond,
			env.BackgroundWorkerDeadlockDetectionTimeout,
			mustParseWorkflowPanicPolicy(env.BackgroundWorkerWorkflowPanicPolicy, logger),
			logger,
		)

		if cloudProvider == orchcommon.ProviderOCI {
			logger.Info("registering OCI background workflows and activities")
			RegisterOCIBackgroundWorkflowsAndActivities(*worker, workflowClient.GetTemporalClient(), dbConn, telemetryDBConn)
		} else {
			logger.Info("registering background workflows and activities")
			RegisterBackgroundWorkflowsAndActivities(
				*worker,
				workflowClient.GetTemporalClient(),
				dbConn,
				telemetryDBConn,
				harvestMetricsEmitter,
			)
		}

	default:
		logger.Error("Unknown worker type", "type", workerType)
		os.Exit(1)
	}

	eg.Go(func() error {
		if err := worker.Run(); err != nil {
			logger.Error("Failed to run worker", "error", err.Error())
			return err
		}
		return nil
	})

	// Wait for all goroutines to complete
	if err := eg.Wait(); err != nil {
		logger.Error("Error while running worker", "error", err.Error())
		os.Exit(1)
	}

	logger.Info("All goroutines completed successfully")
}

// initializeTemporalClient initializes and returns a TemporalWorkflowEngine client.
// It loads the configuration, initializes the client, and logs any errors encountered.
func initializeTemporalClient(logger log.Logger) (workflowEngine.WorkflowEngine, error) {
	workflowClient := workflowEngine.WorkflowEngine{}
	workflowCfg := workflowClient.LoadConfig()
	err := workflowClient.InitializeClient(workflowCfg, logger)
	if err != nil {
		logger.Error("client error: %w", "error", err.Error())
		return workflowClient, err
	}
	return workflowClient, nil
}

// RegisterOCICustomerWorkflowsAndActivities registers only OCI-related workflows and activities for the customer worker.
// Used when HYPERSCALER=oci. For GCP, RegisterCustomerWorkflowsAndActivities registers the full set.
func RegisterOCICustomerWorkflowsAndActivities(worker tManagerPkg.Worker, dbcon database.Storage, temporal client.Client) {
	// Register OCI workflows
	worker.RegisterWorkflow(ociworkflows.OCICreatePoolWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCIUpdatePoolWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCIDeletePoolWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCICreateSVMWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCIDeleteSVMWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCIUpdateSVMWorkflow)

	worker.RegisterWorkflow(ociworkflows.OCIClusterUpgradeWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCIRefreshRbacForPoolWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCIResetOciAdminPasswordWorkflow)
	worker.RegisterWorkflow(ociworkflows.OCIRotateFabricPoolKeysWorkflow)

	// OCI certificate rotation: dispatcher + per-pool rotation run on the customer worker
	// (OCI has no background worker). The dispatcher is triggered by a Temporal Schedule
	// (see core/scheduler/ocischedules) and fans out per-pool rotations as top-level workflows.
	worker.RegisterWorkflow(backgroundworkflows.RotateOciCertificatesWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotateOciPoolCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotateOciPoolClientCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotateOciPoolServiceCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotateOciPoolServerCertificateWorkflow)
	worker.RegisterActivity(&backgroundactivities.OciCertificateRotationActivity{SE: dbcon})
	worker.RegisterActivity(backgroundactivities.EmitOciCertificateRotationFailureMetric)
	worker.RegisterActivity(backgroundactivities.EmitOciCertificateRotationSuccessMetric)

	worker.RegisterWorkflow(backgroundworkflows.VerifyOciTempPasswordsWorkflow)
	worker.RegisterActivity(&backgroundactivities.OciTempPasswordVerificationActivity{SE: dbcon})

	worker.RegisterWorkflow(ociworkflows.OCIExecutePoolMaintenanceWorkflow)
	worker.RegisterActivity(&activities.ClusterUpgradeActivity{SE: dbcon})
	// Register activities used by OCI workflows
	// CommonActivities is needed for UpdateJobStatus and EnsureJobState (used by BaseWorkflow)
	worker.RegisterActivity(&activities.CommonActivities{SE: dbcon})
	// PoolActivity is needed for pool operations (SaveVSANodeDetails, UpdatePoolState, GetPool, DeletePoolResources)
	worker.RegisterActivity(&activities.PoolActivity{SE: dbcon})
	// SvmActivity is needed for SVM operations (AllocateSVMName, SaveSVMAndLifData, QoS policy, expert mode credentials)
	worker.RegisterActivity(&activities.SvmActivity{SE: dbcon})
	// CancellationActivity is needed for workflow cancellation support
	worker.RegisterActivity(activities.NewCancellationActivity(temporal))
}

// RegisterCustomerWorkflowsAndActivities registers all customer workflows and activities (GCP and shared).
func RegisterCustomerWorkflowsAndActivities(worker tManagerPkg.Worker, dbcon database.Storage, temporal client.Client) {
	hyperscaler.SetCloudResolver(hyperscaler.NewCloudServiceResolver(env.GetHyperscaler()))
	worker.RegisterWorkflow(workflows.SequenceWorkflow)
	worker.RegisterWorkflow(workflows.SequenceWorkflowV2)
	worker.RegisterWorkflow(workflows.CreatePoolWorkflow)
	worker.RegisterWorkflow(workflows.DataSubnetSequentialPoller)
	worker.RegisterWorkflow(workflows.PoolDataSubnetWorkFlow)
	worker.RegisterWorkflow(workflows.ConfigureNetworkWorkflow)
	worker.RegisterWorkflow(workflows.ConfigurePSCEndpointWorkflow)
	worker.RegisterWorkflow(workflows.ReleasePSCEndpointWorkflow)
	worker.RegisterWorkflow(workflows.UpdatePoolWorkflow)
	worker.RegisterWorkflow(workflows.DeletePoolWorkflow)
	worker.RegisterWorkflow(workflows.POICLifEnsureWorkflow)
	worker.RegisterWorkflow(workflows.CleanupServiceAccountPermissionsWorkflow)
	worker.RegisterWorkflow(workflows.CreateVolumeWorkflow)
	worker.RegisterWorkflow(flexcache_workflows.CreateFlexCacheWorkflow)
	worker.RegisterWorkflow(flexcache_workflows.DeleteFlexCacheVolumeWorkflow)
	worker.RegisterWorkflow(workflows.PreBlockVolumeWorkflow)
	worker.RegisterWorkflow(workflows.PostBlockVolumeWorkflow)
	worker.RegisterWorkflow(workflows.PreFileVolumeWorkflow)
	worker.RegisterWorkflow(workflows.PostFileVolumeWorkflowForSMB)
	worker.RegisterWorkflow(workflows.PostFileVolumeWorkflow)
	worker.RegisterWorkflow(workflows.WaitForGCPNetworkOperationStatusWorkflow)
	worker.RegisterWorkflow(workflows.EnsureKerberosConfigWorkflow)
	worker.RegisterWorkflow(workflows.UpdateVolumeWorkflow)
	worker.RegisterWorkflow(workflows.UpdateVolumePerformanceGroupWorkflow)
	worker.RegisterWorkflow(workflows.DeleteVolumePerformanceGroupWorkflow)
	worker.RegisterWorkflow(workflows.RevertVolumeWorkflow)
	worker.RegisterWorkflow(workflows.DeleteVolumeWorkflow)
	worker.RegisterWorkflow(workflows.SmbTeardownWorkflow)
	worker.RegisterWorkflow(workflows.DeleteSnapshotWorkflow)
	worker.RegisterWorkflow(workflows.CreateSnapshotWorkflow)
	worker.RegisterWorkflow(workflows.CreateQuotaRuleWorkflow)
	worker.RegisterWorkflow(workflows.PinAccountHostsWorkflow)
	worker.RegisterWorkflow(workflows.OnboardHostWorkflow)
	worker.RegisterWorkflow(workflows.CreateVolumePerformanceGroupWorkflow)
	worker.RegisterWorkflow(workflows.UpdateQuotaRuleWorkflow)
	worker.RegisterWorkflow(workflows.DeleteQuotaRuleWorkflow)
	worker.RegisterWorkflow(workflows.AcceptClusterPeerWorkflow)
	worker.RegisterWorkflow(kms_workflows.DeleteKmsConfigWorkflow)
	worker.RegisterWorkflow(kms_workflows.CreateKmsConfigWorkflow)
	worker.RegisterWorkflow(kms_workflows.MigrateKmsConfigWorkflow)
	worker.RegisterWorkflow(kms_workflows.EncryptPoolWorkflow)
	worker.RegisterWorkflow(background_kms_workflows.RotateKmsConfigWorkflow)
	worker.RegisterWorkflow(background_kms_workflows.RotateKmsKeyChildWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.CreateInternalVolumeReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.UpdateInternalVolumeReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.CreateVolumeReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.UpdateVolumeReplicationWorkflow)
	worker.RegisterWorkflow(workflows.CreateBackupWorkflow)
	worker.RegisterWorkflow(workflows.ADCWorkflow)
	worker.RegisterWorkflow(workflows.RestoreFilesFromBackupWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.RestoreForOntapModeVolumeWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.GetMultipleReplicationsInternalWorkflow)
	worker.RegisterWorkflow(workflows.DeleteBackupWorkflow)
	worker.RegisterWorkflow(workflows.UpdateBackupWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.PerformMountCheckWorkflow)
	worker.RegisterWorkflow(workflows.CreateSMCTokenRotationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ResumeInternalVolumeReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ResumeReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ReverseAndResumeVolumeReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ReverseInternalVolumeReplicationWorkflow)
	worker.RegisterWorkflow(workflows.UpdateHostGroupWorkflow)
	worker.RegisterWorkflow(workflows.StartProjectEventOffStateWorkflow)
	worker.RegisterWorkflow(workflows.StartProjectEventOnStateWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ReleaseVolumeReplicationInternalWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.DeleteInternalVolumeReplicationWorkflow)
	worker.RegisterWorkflow(workflows.CreateBackupVaultWorkflow)
	worker.RegisterWorkflow(workflows.UpdateBackupVaultWorkflow)
	worker.RegisterWorkflow(workflows.RotateCmekBackupsWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.DeleteInternalSnapshotWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.StopInternalVolumeReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.StopReplicationWorkflow)
	worker.RegisterWorkflow(workflows.RegisterNodeToHarvestFarmWorkflow)
	worker.RegisterWorkflow(workflows.UnRegisterNodeFromHarvestFarmWorkflow)
	worker.RegisterWorkflow(workflows.ReconcileHarvestNodeGroupMapWorkflow)
	worker.RegisterWorkflow(workflows.HarvestPollerUpgradeWorkFlow)
	worker.RegisterWorkflow(replicationWorkflows.ReplicationDeleteWorkflow)
	worker.RegisterWorkflow(ontaprest.PollOntapJob)
	worker.RegisterWorkflow(workflows.DeleteBackupVaultWorkflow)
	worker.RegisterWorkflow(workflows.UpdateBackupPolicyWorkflow)
	worker.RegisterWorkflow(workflows.UpdateResourceStateONWorkflow)
	worker.RegisterWorkflow(workflows.UpdateResourceStateOFFWorkflow)
	worker.RegisterWorkflow(workflows.UpdateResourceStateCommonResourceONWorkflow)
	worker.RegisterWorkflow(workflows.UpdateResourceStateCommonResourceOFFWorkflow)
	worker.RegisterWorkflow(workflows.UpdateResourceStateDELETEWorkflow)
	worker.RegisterWorkflow(workflows.FinishProjectEventDeleteStateWorkflow)
	worker.RegisterWorkflow(workflows.DeleteBackupPolicyWorkflow)
	worker.RegisterWorkflow(workflows.RestoreBackupWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ReplicationCleanupWorkflow)
	worker.RegisterWorkflow(workflows.DeletePoolWorkflowInternal)
	worker.RegisterWorkflow(replicationWorkflows.UpdateVolumeReplicationAttributesWorkflow)
	worker.RegisterWorkflow(workflows.UpdateVolumeInReplicationWorkflow)
	worker.RegisterWorkflow(workflows.CreateBackupWorkflowWithContext)
	worker.RegisterWorkflow(workflows.RestoreBackupWorkflowWithContext)
	worker.RegisterWorkflow(workflows.CreateActiveDirectoryWorkflow)
	worker.RegisterWorkflow(workflows.UpdateActiveDirectoryWorkflow)
	worker.RegisterWorkflow(workflows.DeleteActiveDirectoryWorkflow)
	worker.RegisterWorkflow(workflows.DiagnoseActiveDirectoryWorkflow)
	worker.RegisterWorkflow(workflows.VolumePollSplitWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.CreateHybridReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.HybridReplicationDeleteWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.HybridDeleteDestinationVolumeWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.EstablishPeeringWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.InternalEstablishWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ReverseHybridReplicationWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ReverseHybridReplicationPollWorkflow)
	worker.RegisterWorkflow(replicationWorkflows.ReverseHybridFallbackReplicationWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotatePoolCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ForceRotatePoolCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.TrackedForceRotatePoolCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotatePoolPasswordWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ReconcilePoolCAWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.TrackedReconcilePoolCAWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ReconcilePoolAuthTypeForDesiredCAWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ReconcilePoolAuthTypeAPIWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ReconcilePoolAuthTypesAPIWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.ManageBackupConfigWorkflow)

	temporalScheduler := scheduler.NewTemporalScheduler(temporal.ScheduleClient())
	worker.RegisterActivity(&activities.CommonActivities{SE: dbcon})
	worker.RegisterActivity(&activities.PoolActivity{SE: dbcon})
	worker.RegisterActivity(&activities.AccountHostActivity{SE: dbcon})
	worker.RegisterActivity(&activities.HostOnboardActivity{SE: dbcon})
	worker.RegisterActivity(&activities.SvmActivity{SE: dbcon})
	worker.RegisterActivity(&activities.POOntapActivity{SE: dbcon})
	worker.RegisterActivity(&activities.CVNActivity{SE: dbcon})
	worker.RegisterActivity(&activities.IPRangeActivity{SE: dbcon})
	worker.RegisterActivity(&activities.PODataLifActivity{SE: dbcon})
	worker.RegisterActivity(&activities.POICVNActivity{SE: dbcon, Config: orchcommon.DefaultPOICLifConfig()})
	worker.RegisterActivity(&activities.POICLifActivity{SE: dbcon})
	worker.RegisterActivity(&expertmodeactivities.RBACUpdateActivity{SE: dbcon})
	worker.RegisterActivity(&activities.PSCActivity{SE: dbcon})
	worker.RegisterActivity(activities.NewCancellationActivity(temporal))
	worker.RegisterActivity(&workflows.SubnetActivity{SE: dbcon})
	worker.RegisterActivity(&activities.VolumeCreateActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&activities.OntapModeRestoreActivity{SE: dbcon})
	worker.RegisterActivity(&expertmodeactivities.ExpertModeVolumeActivity{SE: dbcon})
	worker.RegisterActivity(&expertmodeactivities.ExpertModeBackupConfigActivity{})
	worker.RegisterActivity(&activities.VolumePerformanceGroupActivity{SE: dbcon})
	worker.RegisterActivity(&flexcache_activities.FlexCacheVolumeCreateActivity{SE: dbcon})
	worker.RegisterActivity(&flexcache_activities.FlexCacheVolumeDeleteActivity{SE: dbcon})
	worker.RegisterActivity(&flexcache_activities.FlexCacheVolumeUpdateActivity{SE: dbcon})
	worker.RegisterActivity(&activities.VolumeUpdateActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&activities.VolumeDeleteActivity{SE: dbcon})
	worker.RegisterActivity(&activities.VolumeRevertActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&activities.SnapshotCreateActivity{SE: dbcon})
	worker.RegisterActivity(&activities.SnapshotDeleteActivity{SE: dbcon})
	worker.RegisterActivity(&activities.QuotaRuleCreateActivity{SE: dbcon})
	worker.RegisterActivity(&activities.QuotaRuleUpdateActivity{SE: dbcon})
	worker.RegisterActivity(&activities.QuotaRuleDeleteActivity{SE: dbcon})
	worker.RegisterActivity(&activities.QuotaRuleCommonActivity{SE: dbcon})
	worker.RegisterActivity(&activities.ClusterPeerActivity{SE: dbcon})
	worker.RegisterActivity(&kms_activities.KmsConfigActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.InternalVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.VolumeReplicationCreateActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.VolumeReplicationUpdateActivity{SE: dbcon})
	worker.RegisterActivity(&activities.BackupActivity{SE: dbcon})
	worker.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{SE: dbcon})
	worker.RegisterActivity(&activities.ADCActivity{SE: dbcon})
	worker.RegisterActivity(&activities.SFRActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.ReplicationInternalGetMultipleActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.MountJobActivity{SE: dbcon})
	worker.RegisterActivity(&activities.SmcTokenRotationActivity{SE: dbcon})
	worker.RegisterActivity(&activities.HostGroupUpdateActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.InternalVolumeReplicationResumeActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.ReverseVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.InternalVolumeReplicationReverseActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.ResumeVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&resource_events_activities.StartProjectEventActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.InternalVolumeReplicationRowDeleteActivity{SE: dbcon})
	worker.RegisterActivity(&activities.RegisterNodeToHarvestFarmActivity{SE: dbcon})
	worker.RegisterActivity(&activities.UploadHarvestTemplateActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.InternalVolumeReplicationDeleteActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.InternalVolumeReplicationUpdateActivity{SE: dbcon})
	worker.RegisterActivity(&activities.BackupVaultActivity{SE: dbcon, CmekMetricsEmitter: metrics.NewPrometheusCmekBackupMetricsEmitter()})
	worker.RegisterActivity(&replicationActivities.InternalSnapshotsDeleteActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.InternalStopVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.StopVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&activities.UnRegisterNodeFromHarvestActivity{SE: dbcon})
	worker.RegisterActivity(&activities.HarvestNodesRefreshActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.DeleteVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&activities.BackupPolicyActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&resource_events_activities.ResourceEventsActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&resource_events_activities.FinishProjectEventActivity{SE: dbcon})
	worker.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{SE: dbcon, MetricsEmitter: metrics.NewPrometheusKmsMetricsEmitter()})
	worker.RegisterActivity(ontaprest.PollOntapJobActivity)
	worker.RegisterActivity(&replicationActivities.CleanupVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: dbcon})
	worker.RegisterActivity(&activities.UpdateVolumeInReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&backgroundactivities.SyncBackupZiZsActivity{SE: dbcon})
	worker.RegisterActivity(&active_directory_activities.ActiveDirectoryCreateActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&active_directory_activities.ActiveDirectoryUpdateActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&backgroundactivities.ScheduledBackupActivity{SE: dbcon})
	worker.RegisterActivity(&active_directory_activities.ActiveDirectoryDeleteActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&active_directory_activities.ActiveDirectoryDiagnoseActivity{SE: dbcon})
	worker.RegisterActivity(&activities.VolumeSplitActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(&replicationActivities.HybridReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.HybridDeleteVolumeReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&replicationActivities.ReverseHybridReplicationActivity{SE: dbcon})
	worker.RegisterActivity(&backgroundactivities.RotateVcpToVsaCertificateActivity{SE: dbcon})
	// The on-demand auth-type reconciliation parent and per-pool workflows run on
	// the customer queue, so their control activities must be available here too.
	worker.RegisterActivity(&backgroundactivities.ControlWorkflowActivity{})
	worker.RegisterActivity(backgroundactivities.EmitCertificateRotationFailureMetric)
	worker.RegisterActivity(backgroundactivities.EmitPasswordRotationFailureMetric)
	worker.RegisterActivity(backgroundactivities.EmitZoneSwitchWorkflowMetric)
	worker.RegisterActivity(&active_directory_activities.ActiveDirectorySyncActivity{SE: dbcon, Scheduler: temporalScheduler})
	worker.RegisterActivity(backgroundactivities.EmitKmsKeyLimitReachedMetric)
	worker.RegisterActivity(backgroundactivities.EmitKmsRotationFailureMetric)
}

// RegisterOCIBackgroundWorkflowsAndActivities registers only OCI-related background workflows and activities.
// Used when HYPERSCALER=oci. For GCP, RegisterBackgroundWorkflowsAndActivities registers the full set.
func RegisterOCIBackgroundWorkflowsAndActivities(worker tManagerPkg.Worker, temporal client.Client, conn database.Storage, telemetryDBConn metricsdb.Storage) {
	worker.RegisterWorkflow(ociworkflows.OCIClusterUpgradeWorkflow)
	worker.RegisterActivity(&activities.ClusterUpgradeActivity{SE: conn})
	worker.RegisterActivity(&activities.PoolActivity{SE: conn})
	worker.RegisterActivity(&activities.CommonActivities{SE: conn})
	worker.RegisterActivity(activities.NewCancellationActivity(temporal))
}

func RegisterBackgroundWorkflowsAndActivities(
	worker tManagerPkg.Worker,
	temporal client.Client,
	conn database.Storage,
	telemetryDBConn metricsdb.Storage,
	harvestMetricsEmitter metricsinterface.HarvestMetricsEmitter,
) {
	hyperscaler.SetCloudResolver(hyperscaler.NewCloudServiceResolver(env.GetHyperscaler()))
	worker.RegisterWorkflow(jobmanagerworkflows.JobManagerWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.VolumeDetailsWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SnapshotsSyncParentWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SnapshotsSyncChildWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SyncLatestBackupLogicalSizeWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CreateScheduledBackupInitWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CreateScheduledBackupWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CreateScheduledBackupWorkflowWithContext)
	worker.RegisterWorkflow(backgroundworkflows.DeleteScheduledBackupWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.OrphanJobSchedulerWorkflow)
	worker.RegisterWorkflow(workflows.VolumeRefreshWorkflow)
	worker.RegisterWorkflow(background_kms_workflows.RotateKmsSAKeyWorkflow)
	worker.RegisterWorkflow(background_kms_workflows.RotateKmsKeyChildWorkflow)
	worker.RegisterWorkflow(workflows.RestoreBackupWorkflow)
	worker.RegisterWorkflow(workflows.PreBlockVolumeWorkflow)
	worker.RegisterWorkflow(workflows.PostBlockVolumeWorkflow)
	worker.RegisterWorkflow(workflows.PostFileVolumeWorkflowForSMB)
	worker.RegisterWorkflow(workflows.PreFileVolumeWorkflow)
	worker.RegisterWorkflow(workflows.PostFileVolumeWorkflow)
	worker.RegisterWorkflow(workflows.EnsureKerberosConfigWorkflow)
	worker.RegisterWorkflow(workflows.WaitForGCPNetworkOperationStatusWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SyncForHardDeleteWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.HardDeleteResourcesAndAccountWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CleanupHydratedMetricsTableWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CleanupAggregatedUsageTableWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CleanupJobsTableWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CleanupBackupChainHistoryWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SyncVSAAutoTieringWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SyncPoolZIZSDetailsWorkflow)
	worker.RegisterWorkflow(workflows.SyncPoolComplianceForPoolWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.AutoTieringPauseResumeWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.AutoTieringHotTierAutoResizeWorkflow)
	worker.RegisterWorkflow(workflows.UpdatePoolWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ResourceCleanupParentWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ResourceCleanupChildWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SyncBackupZiZsWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.EligibilityStringWorkflow)
	worker.RegisterWorkflow(workflows.ClusterUpgradeWorkflow)
	worker.RegisterWorkflow(workflows.BatchClusterUpgradeWorkflow)
	worker.RegisterWorkflow(workflows.RestoreBackupWorkflowWithContext)
	worker.RegisterWorkflow(backgroundworkflows.UpdateBackupScheduleWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SyncFlexCachePrepopulateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotateVsaCertificateAndPasswordWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ReconcileVsaCAOnChangeWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ForceVsaCertificateRotationWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotatePoolCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ForceRotatePoolCertificateWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ForcePoolCertificateRotationWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.RotatePoolPasswordWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.BackupSizeDetailsWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.FetchCCFEPoolsWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.FetchCCFEBackupVaultsWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.GetRegionZonesWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.VolumeCreateReconciliationWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.VolumeDeleteReconciliationWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.VolumeUpdateReconciliationWorkflow)
	worker.RegisterWorkflow(workflows.VolumePollSplitWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.ExpertModeFlexCloneSplitWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ScanGCEInstancesWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ScanRegionalAddressesWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ScanGCEDisksWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.UpdateRbacForPoolsWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.UpdateRbacForSinglePoolWorkflow)
	worker.RegisterWorkflow(expertmodeworkflows.UpdateSinglePoolRbacChildWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.PollerRebalanceWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.HarvestPollerDriftReconcileWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SnapshotAndPlanWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.CleanupEmptyLeasesWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.TrialAccountSyncWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.ExpertModeVolumeSyncParentWorkflow)
	worker.RegisterWorkflow(backgroundworkflows.SyncExpertModeVolumesForPoolBatchWorkflow)

	temporalScheduler := scheduler.NewTemporalScheduler(temporal.ScheduleClient())
	worker.RegisterActivity(&jobmanageractivities.JobManagerActivity{SE: conn, Scheduler: temporalScheduler})
	worker.RegisterActivity(&activities.CommonActivities{SE: conn})
	worker.RegisterActivity(&activities.ClusterUpgradeActivity{SE: conn})
	worker.RegisterActivity(&activities.BatchClusterUpgradeActivity{SE: conn})
	worker.RegisterActivity(&activities.VolumeUpdateActivity{SE: conn})
	worker.RegisterActivity(&activities.VolumeRefreshActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.SyncSnapshotActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.SyncExpertModeVolumesActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.ResourceDeleteActivity{SE: conn})
	worker.RegisterActivity(&activities.BackupActivity{SE: conn})
	worker.RegisterActivity(&activities.ADCActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.ScheduledBackupActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.RotateKmsSAKeyActivity{SE: conn, MetricsEmitter: metrics.NewPrometheusKmsMetricsEmitter()})
	worker.RegisterActivity(&backgroundactivities.OrphanJobActivity{SE: conn})
	worker.RegisterActivity(&activities.VolumeCreateActivity{SE: conn, Scheduler: temporalScheduler})
	worker.RegisterActivity(&activities.VolumePerformanceGroupActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.CustomerAdoptionActivity{SE: conn, Scheduler: temporalScheduler})
	worker.RegisterActivity(&backgroundactivities.HardDeleteResourcesAndAccountActivity{SE: conn})
	worker.RegisterActivity(&backgroundworkflows.HardDeleteResourcesAndAccountworkflow{})
	worker.RegisterActivity(&resource_events_activities.FinishProjectEventActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.VolumeBackupSyncActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.MetricsCleanupActivity{SE: conn, MetricsDB: telemetryDBConn})
	worker.RegisterActivity(&backgroundactivities.AutoTierSyncActivity{SE: conn})
	worker.RegisterActivity(&activities.WFLastExecutionActivity{TemporalClient: temporal})
	worker.RegisterActivity(&activities.PoolActivity{SE: conn})
	worker.RegisterActivity(&activities.SvmActivity{SE: conn})
	worker.RegisterActivity(&activities.VolumeSplitActivity{SE: conn, Scheduler: temporalScheduler})
	worker.RegisterActivity(&backgroundactivities.SyncBackupZiZsActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.EligibilityStringActivity{SE: conn, Scheduler: temporalScheduler})
	worker.RegisterActivity(&backgroundactivities.UpdateBackupScheduleActivity{SE: conn, ScheduleClient: temporal.ScheduleClient()})
	worker.RegisterActivity(&backgroundactivities.FlexCachePrepopulateActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.RotateVcpToVsaCertificateActivity{SE: conn})
	worker.RegisterActivity(&backgroundactivities.ControlWorkflowActivity{})
	worker.RegisterActivity(&backgroundactivities.FetchStoragePoolsActivity{
		CCFE: ccfe.NewClient(auth.GenerateCallbackToken),
	})
	worker.RegisterActivity(&backgroundactivities.FetchBackupVaultsActivity{
		CCFE: ccfe.NewClient(auth.GenerateCallbackToken),
	})
	worker.RegisterActivity(&backgroundactivities.GetRegionZonesActivity{})
	worker.RegisterActivity(&expertmodeactivities.ExpertModeVolumeActivity{SE: conn})
	worker.RegisterActivity(&expertmodeactivities.ExpertModeBackupConfigActivity{})
	worker.RegisterActivity(&expertmodeactivities.RBACUpdateActivity{SE: conn})
	worker.RegisterActivity(&active_directory_activities.ActiveDirectoryActivity{SE: conn})
	worker.RegisterActivity(backgroundactivities.EmitCertificateRotationFailureMetric)
	worker.RegisterActivity(backgroundactivities.EmitPasswordRotationFailureMetric)
	worker.RegisterActivity(backgroundactivities.EmitZoneSwitchWorkflowMetric)
	worker.RegisterActivity(backgroundactivities.EmitKmsKeyLimitReachedMetric)
	worker.RegisterActivity(backgroundactivities.EmitKmsRotationFailureMetric)
	worker.RegisterActivity(&backgroundactivities.ScanGCEInstancesActivity{})
	worker.RegisterActivity(&backgroundactivities.ScanRegionalAddressesActivity{})
	worker.RegisterActivity(&backgroundactivities.ScanGCEDisksActivity{})
	worker.RegisterActivity(&activities.PollerRebalanceActivities{SE: conn})
	worker.RegisterActivity(&activities.HarvestPollerDriftReconcileActivity{
		SE:             conn,
		MetricsEmitter: harvestMetricsEmitter,
	})
	worker.RegisterActivity(&backgroundactivities.TrialAccountSyncActivity{
		SE:   conn,
		CCFE: trial.NewClient(auth.GenerateCallbackToken),
	})
}
