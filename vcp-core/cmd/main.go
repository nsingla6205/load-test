package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-faster/errors"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/robfig/cron"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/tasks"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	_ "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/drivers/postgres"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	api "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/handlers"
	coregenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
	_ "go.uber.org/automaxprocs"
	"golang.org/x/sync/errgroup"
)

func init() {
	// Register the workflow executor to break circular dependency
	backgroundactivities.RegisterWorkflowExecutor(workflows.ExecuteWorkflowSequentially)
}

const (
	syncVSAClusterHealthJobType            = "SYNC_VSA_CLUSTER_HEALTH_STATUS"
	syncVSAClusterHealthCronExpression     = "*/30 * * * * *"
	syncVSAClusterHealthLockTimeoutSeconds = 30

	workflowSupervisorJobType            = "WORKFLOW_SUPERVISOR_SWEEP"
	workflowSupervisorScheduleExpression = "0 */5 * * * *"
	workflowSupervisorCronExpression     = "0 */5 * * * *"
	workflowSupervisorLockTimeoutSeconds = 300

	leakedResourcesMonitoringJobType               = "LEAKED_RESOURCES_MONITORING"
	leakedResourcesMonitoringCronExpressionDefault = "0 0 0 * * *" // once per day at midnight (sec min hour day month dow)
	leakedResourcesMonitoringLockTimeoutSeconds    = 2 * 60 * 60   // 2 hours: must exceed max pipeline run time so a concurrent cron tick cannot steal the lock mid-run
	leakedResourcesMonitoringRunTimeoutSeconds     = 90 * 60       // 90 min: pipeline can take ~1h in prod; 90 min gives headroom before context cancellation
)

func main() {
	ctx := context.WithValue(context.Background(), utilsmiddleware.CorrelationContextKey, uuid.NewString())

	// Use signal.NotifyContext to handle termination signals
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := log.NewLogger()
	logger.Info("Starting VCP Core API Service")

	// Setup metrics, tracing, and context propagation
	shutdown, err := log.SetupOpenTelemetry(ctx)
	if err != nil {
		logger.ErrorContext(ctx, "error setting up OpenTelemetry", "error", err)
		shutdown = func(ctx context.Context) error { return nil }
	}
	defer func() {
		if err := shutdown(ctx); err != nil {
			logger.ErrorContext(ctx, "error shutting down OpenTelemetry", "error", err)
		}
	}()

	metrics.RegisterBackgroundTaskSchedulerMetrics()

	cfg := common.LoadConfig()

	// Validate certificate lifetime before starting the service
	err = env.ValidateCertificateLifetime()
	if err != nil {
		logger.Error("Certificate lifetime validation failed", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("Certificate lifetime validation passed")

	// initialize the database - this can be moved to a separate function
	dbCon, err := InitializeDatabase(ctx, cfg, logger)
	if err != nil {
		logger.Error("Failed to initialize database", "error", err.Error())
		os.Exit(1)
	}
	defer closeDatabase(dbCon, logger)

	// Initialize Temporal client
	workflowClient, err := initializeTemporalClient(logger)
	if err != nil {
		logger.Error("Failed to initialize Temporal client", "error", err.Error())
		os.Exit(1)
	}
	defer workflowClient.CloseClient(workflowClient.GetTemporalClient())

	// Initialize FetchTemporalClient function for use in activities
	workflowengine.FetchTemporalClient = func() (client.Client, error) {
		return workflowClient.GetTemporalClient(), nil
	}

	if err := workflows.LaunchHarvestRefreshIfNeeded(ctx, cfg, dbCon, workflowClient.GetTemporalClient(), logger); err != nil {
		logger.Error("Failed to launch harvest refresh workflow", "error", err.Error())
	}

	// Create GCP proxy server and inject required dependencies
	orch := factory.GetOrchestratorForProvider(dbCon, workflowClient.GetTemporalClient())
	newHandler := api.NewHandler(orch)
	oasserver, err := coregenserver.NewServer(newHandler)
	if err != nil {
		logger.Error("Failed to create server", "error", err.Error())
		os.Exit(1)
	}

	_, err = vsaerrors.NewErrorHandler()
	if err != nil {
		logger.Error("Failed to create error handler", "error", err.Error())
		os.Exit(1)
	}

	httpServer := setupHTTPServer(cfg, oasserver)

	// Use errgroup to manage goroutines and context
	eg, ctx := errgroup.WithContext(ctx)

	// Start HTTP server
	eg.Go(func() error {
		logger.Info("Starting HTTP server on " + cfg.CoreHost + ":" + cfg.CorePort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Failed to start HTTP server", "error", err.Error())
			return err
		}
		return nil
	})

	// Start background task scheduler
	if cfg.EnableBackgroundTask {
		eg.Go(func() error {
			return startBackgroundTaskScheduler(ctx, dbCon, workflowClient.GetTemporalClient())
		})
	}

	// Handle graceful shutdown on signal or context cancellation
	eg.Go(func() error {
		<-ctx.Done()
		logger.Info("Shutting down server")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shut down server gracefully", "error", err.Error())
			return err
		}
		return nil
	})
	handleGracefulShutdown(eg, ctx, httpServer, logger)

	// Wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		logger.Error("Server error", "error", err.Error())
		os.Exit(1)
	}

	logger.Info("Server stopped gracefully")
}

func InitializeDatabase(ctx context.Context, cfg *common.Config, logger log.Logger) (database.Storage, error) {
	dbConfig := GetDBConfig(cfg)
	db, err := database.New(dbConfig, logger)
	if err != nil {
		return nil, err
	}
	for {
		err = db.Connect(false)
		if err == nil {
			break
		}
		logger.Error("Failed to connect to the database, retrying...", "error", err.Error())
		time.Sleep(2 * time.Second) // Add a delay between retries to avoid overwhelming the database
	}

	if cfg.RunMigrationOnStart {
		// this flag is used to run the migration on start in local env
		// this works only if the app user has the necessary permission
		if err := db.Migrate(ctx); err != nil {
			return nil, err
		}
	}
	return db, nil
}

func closeDatabase(dbCon database.Storage, logger log.Logger) {
	if err := dbCon.Close(); err != nil {
		logger.Error("Failed to close database connection", "error", err.Error())
	}
}

func initializeTemporalClient(logger log.Logger) (workflowengine.WorkflowEngine, error) {
	workflowClient := workflowengine.WorkflowEngine{}
	workflowCfg := workflowClient.LoadConfig()
	err := workflowClient.InitializeClient(workflowCfg, logger)
	if err != nil {
		logger.Error("client error: %w", "error", err.Error())
		return workflowClient, err
	}
	return workflowClient, nil
}

func GetDBConfig(cfg *common.Config) dbutils.DbConfig {
	dbConfig := dbutils.DbConfig{
		Type:            cfg.DBType,
		Host:            cfg.DBHost,
		Port:            cfg.DBPort,
		User:            cfg.DBUser,
		Password:        cfg.DBPassword,
		Name:            cfg.DBName,
		SSLMode:         cfg.DBSSLMode,
		TimeZone:        cfg.DBTimeZone.String(),
		MaxOpenConns:    cfg.DBMaxOpenConns,
		MaxIdleConns:    cfg.DBMaxIdleConns,
		ConnMaxLifetime: cfg.DBConnMaxLifetime,
		AdminUser:       cfg.DBAdminUser,
		AdminPassword:   cfg.DBAdminPassword,
	}
	if cfg.MSIEnabled {
		// When MSI is enabled, set the database user credentials from MSIDBUser
		dbConfig.User = cfg.MSIDBUser
		dbConfig.AdminUser = cfg.MSIDBUser
	}
	return dbConfig
}

func setupHTTPServer(cfg *common.Config, handler http.Handler) *http.Server {
	mux := chi.NewRouter()
	mux.Use(httphelpers.LoggingHttpHandler)
	mux.Use(log.LoggingMiddleware)
	// AuthMiddleware will skip JWT authentication for:
	// - /health and /metrics endpoints
	// - /v1/expertMode/... endpoints (ontap-proxy; Istio mTLS + AuthorizationPolicies)
	// - /version and CVN /v1 infra parity endpoints (CVN; Istio mTLS + AuthorizationPolicies)
	// All other routes (e.g., /v1beta/projects/...) will require JWT authentication
	mux.Use(auth.AuthMiddleware(true)) // true = skip project number validation
	mux.Use(log.RecoverMiddleware)
	mux.Mount("/", handler)
	mux.Handle("/metrics", promhttp.Handler())

	return &http.Server{
		Addr:              cfg.CoreHost + ":" + cfg.CorePort,
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
}

// startBackgroundTaskScheduler initializes and starts the cron scheduler for background tasks
func startBackgroundTaskScheduler(ctx context.Context, se database.Storage, temporal client.Client) error {
	logger := util.GetLogger(ctx)
	logger.Info("Starting background task scheduler")

	logger.InfoContext(ctx, "Starting background task scheduler")

	cronScheduler := cron.New()

	// Schedule the VSA Cluster Health Sync Task to run every 30 seconds with database-backed locking
	syncErr := cronScheduler.AddFunc(syncVSAClusterHealthCronExpression, func() { syncVSAClusterHealthWithLock(ctx, se, logger) })
	if syncErr != nil {
		metrics.IncBackgroundTaskError(syncVSAClusterHealthJobType, "schedule_registration")
		logger.ErrorContext(ctx, "Failed to schedule VSA Cluster Health Sync Task", "error", syncErr)
		return syncErr
	}

	// Schedule the workflow supervisor task to run every 5min with database-backed locking
	supervisorErr := cronScheduler.AddFunc(workflowSupervisorScheduleExpression, func() { runLockedWorkflowSupervisorTask(ctx, se, temporal, logger) })
	if supervisorErr != nil {
		metrics.IncBackgroundTaskError(workflowSupervisorJobType, "schedule_registration")
		logger.ErrorContext(ctx, "Failed to schedule Workflow Supervisor Task", "error", supervisorErr)
		return supervisorErr
	}

	// Schedule leaked resources monitoring when enabled (LEAKED_RESOURCES_MONITORING_ENABLED, default true).
	// When enabled: default once per day at midnight; override via LEAKED_RESOURCES_MONITORING_CRON_EXPRESSION.
	if env.GetBool("LEAKED_RESOURCES_MONITORING_ENABLED", false) {
		leakedResourcesCronExpr := env.GetString("LEAKED_RESOURCES_MONITORING_CRON_EXPRESSION", leakedResourcesMonitoringCronExpressionDefault)
		logger.InfoContext(ctx, "Leaked resources monitoring cron", "expression", leakedResourcesCronExpr)
		leakedResourcesErr := cronScheduler.AddFunc(leakedResourcesCronExpr, func() { runLockedLeakedResourcesMonitoringTask(ctx, se, temporal, logger) })
		if leakedResourcesErr != nil {
			metrics.IncBackgroundTaskError(leakedResourcesMonitoringJobType, "schedule_registration")
			logger.ErrorContext(ctx, "Failed to schedule Leaked Resources Monitoring Task", "error", leakedResourcesErr)
			return leakedResourcesErr
		}
	} else {
		logger.InfoContext(ctx, "Leaked resources monitoring disabled via LEAKED_RESOURCES_MONITORING_ENABLED")
	}

	cronScheduler.Start()
	logger.InfoContext(ctx, "Background task scheduler started successfully")

	<-ctx.Done()

	logger.InfoContext(ctx, "Stopping background task scheduler")
	cronScheduler.Stop()

	return nil
}

func releaseAdminJobSpecLock(ctx context.Context, se database.Storage, jobType string, lockTimeoutSeconds int, logger log.Logger) {
	if lockTimeoutSeconds <= 0 {
		return
	}

	jobSpec, err := se.GetAdminJobSpecByJobType(ctx, jobType)
	if err != nil {
		logger.WarnContext(ctx, "Failed to load admin job spec while releasing lock", "jobType", jobType, "error", err)
		return
	}

	releaseReference := time.Now()
	releaseCandidate := releaseReference.Add(-time.Duration(lockTimeoutSeconds) * time.Second)
	if releaseCandidate.Before(jobSpec.CreatedAt) {
		releaseCandidate = jobSpec.CreatedAt
	}

	lockThreshold := releaseReference
	if lockThreshold.Before(releaseCandidate) {
		lockThreshold = releaseCandidate
	}

	if _, err := se.UpdateAdminJobSpecWithLock(ctx, jobType, scheduler.JobStatusScheduled, lockThreshold, releaseCandidate); err != nil {
		logger.WarnContext(ctx, "Failed to release admin job spec lock", "jobType", jobType, "error", err)
	}
}

// syncVSAClusterHealthWithLock implements database-backed cron job with locking mechanism
func syncVSAClusterHealthWithLock(ctx context.Context, se database.Storage, logger log.Logger) {
	logger.InfoContext(ctx, "Starting VSA cluster health sync with lock")

	// Try to create the job spec atomically (pod initialization phase)
	newJobSpec := &datamodel.AdminJobSpec{
		JobType:        syncVSAClusterHealthJobType,
		CronExpression: syncVSAClusterHealthCronExpression,
		State:          scheduler.JobStatusScheduled,
	}

	createdJobSpec, createErr := se.CreateAdminJobSpecIfNotExists(ctx, newJobSpec)
	if createErr == nil {
		// Successfully created the job spec - we have the "lock", execute immediately
		logger.InfoContext(ctx, "Created new admin job spec, executing task immediately", "jobUUID", createdJobSpec.UUID)
		// Generate correlation ID for tracing (use job UUID if available, otherwise generate new UUID)
		correlationID := createdJobSpec.UUID
		if correlationID == "" {
			correlationID = uuid.NewString()
		}
		metrics.IncBackgroundTaskRun(syncVSAClusterHealthJobType)
		tasks.SyncVSAClusterHealth(ctx, se, correlationID)
		return
	}

	if !errors.Is(createErr, vsaerrors.ErrAdminJobSpecAlreadyExists) {
		logger.ErrorContext(ctx, "Failed to create admin job spec", "error", createErr, "jobType", syncVSAClusterHealthJobType)
		metrics.IncBackgroundTaskError(syncVSAClusterHealthJobType, "create_admin_job_spec")
		return
	}

	// Job spec already exists, try to acquire lock by updating it
	logger.InfoContext(ctx, "Job spec already exists, attempting to acquire lock by updating", "jobType", syncVSAClusterHealthJobType)

	currentTime := time.Now()
	// Lock threshold: only allow lock acquisition if last update was >= 30 seconds ago
	// This handles the case where a pod crashes - after 30 seconds, other pods can take over
	lockThreshold := currentTime.Add(-time.Duration(syncVSAClusterHealthLockTimeoutSeconds) * time.Second)

	// Try to acquire the lock by updating the job spec
	rowsAffected, updateErr := se.UpdateAdminJobSpecWithLock(ctx, syncVSAClusterHealthJobType, scheduler.JobStatusScheduled, lockThreshold, currentTime)

	if updateErr != nil {
		logger.ErrorContext(ctx, "Failed to acquire lock for admin job spec", "error", updateErr, "jobType", syncVSAClusterHealthJobType)
		metrics.IncBackgroundTaskError(syncVSAClusterHealthJobType, "acquire_lock")
		return
	}

	if rowsAffected > 0 {
		// Successfully acquired the lock, now get the job spec to retrieve UUID
		jobSpec, getErr := se.GetAdminJobSpecByJobType(ctx, syncVSAClusterHealthJobType)
		if getErr != nil {
			logger.ErrorContext(ctx, "Failed to retrieve job spec after acquiring lock", "error", getErr, "jobType", syncVSAClusterHealthJobType)
			metrics.IncBackgroundTaskError(syncVSAClusterHealthJobType, "load_job_spec")
			return
		}

		logger.InfoContext(ctx, "Successfully acquired lock, executing task", "jobUUID", jobSpec.UUID)
		// Generate correlation ID for tracing (use job UUID if available, otherwise generate new UUID)
		correlationID := jobSpec.UUID
		if correlationID == "" {
			correlationID = uuid.NewString()
		}
		metrics.IncBackgroundTaskRun(syncVSAClusterHealthJobType)
		tasks.SyncVSAClusterHealth(ctx, se, correlationID)
	} else {
		logger.InfoContext(ctx, "Could not acquire lock - another pod is currently executing the task or not enough time has passed since last execution")
	}
}

func runLockedWorkflowSupervisorTask(ctx context.Context, se database.Storage, temporal client.Client, logger log.Logger) {
	logger.InfoContext(ctx, "Starting workflow supervisor task with lock")

	// Try to create the job spec atomically (pod initialization phase)
	newJobSpec := &datamodel.AdminJobSpec{
		BaseModel:      datamodel.BaseModel{UUID: utils.RandomUUID()},
		JobType:        workflowSupervisorJobType,
		CronExpression: workflowSupervisorCronExpression,
		State:          scheduler.JobStatusScheduled,
	}

	_, createErr := se.CreateAdminJobSpecIfNotExists(ctx, newJobSpec)

	// if createErr is not nil and createErr is not already exist then return error
	if createErr != nil {
		if !errors.Is(createErr, vsaerrors.ErrAdminJobSpecAlreadyExists) {
			logger.ErrorContext(ctx, createErr.Error(), "error", createErr.Error())
			metrics.IncBackgroundTaskError(workflowSupervisorJobType, "create_admin_job_spec")
			return
		}
	}

	logger.InfoContext(ctx, "Job spec already exists, attempting to acquire lock by updating", "jobType", workflowSupervisorJobType)

	currentTime := time.Now()
	// Lock threshold: only allow lock acquisition if last update was >= 300 seconds ago
	// This handles the case where a pod crashes - after 300 seconds, other pods can take over
	lockThreshold := currentTime.Add(-time.Duration(workflowSupervisorLockTimeoutSeconds) * time.Second)

	// Try to acquire the lock by updating the job spec
	rowsAffected, updateErr := se.UpdateAdminJobSpecWithLock(ctx, workflowSupervisorJobType, scheduler.JobStatusScheduled, lockThreshold, currentTime)

	if updateErr != nil {
		logger.ErrorContext(ctx, "Failed to acquire lock for admin job spec", "error", updateErr, "jobType", workflowSupervisorJobType)
		metrics.IncBackgroundTaskError(workflowSupervisorJobType, "acquire_lock")
		return
	}

	if rowsAffected > 0 {
		// Successfully acquired the lock, now get the job spec to retrieve UUID
		jobSpec, getErr := se.GetAdminJobSpecByJobType(ctx, workflowSupervisorJobType)
		if getErr != nil {
			logger.ErrorContext(ctx, "Failed to retrieve job spec after acquiring lock", "error", getErr, "jobType", workflowSupervisorJobType)
			metrics.IncBackgroundTaskError(workflowSupervisorJobType, "load_job_spec")
			return
		}

		logger.InfoContext(ctx, "Successfully acquired lock, executing task", "jobUUID", jobSpec.UUID)
		// Generate correlation ID for tracing (use job UUID if available, otherwise generate new UUID)
		correlationID := jobSpec.UUID
		if correlationID == "" {
			correlationID = uuid.NewString()
		}
		metrics.IncBackgroundTaskRun(workflowSupervisorJobType)
		defer releaseAdminJobSpecLock(ctx, se, workflowSupervisorJobType, workflowSupervisorLockTimeoutSeconds, logger)
		tasks.WorkflowSupervisorTask(ctx, se, temporal, correlationID)
	} else {
		logger.InfoContext(ctx, "Could not acquire lock - another pod is currently executing the task or not enough time has passed since last execution")
	}
}

func runLockedLeakedResourcesMonitoringTask(ctx context.Context, se database.Storage, temporal client.Client, logger log.Logger) {
	logger.InfoContext(ctx, "Starting leaked resources monitoring task with lock")

	cronExpr := env.GetString("LEAKED_RESOURCES_MONITORING_CRON_EXPRESSION", leakedResourcesMonitoringCronExpressionDefault)
	newJobSpec := &datamodel.AdminJobSpec{
		BaseModel:      datamodel.BaseModel{UUID: utils.RandomUUID()},
		JobType:        leakedResourcesMonitoringJobType,
		CronExpression: cronExpr,
		State:          scheduler.JobStatusScheduled,
	}

	createdJobSpec, createErr := se.CreateAdminJobSpecIfNotExists(ctx, newJobSpec)
	if createErr == nil {
		// Successfully created the job spec - we have the "lock", execute immediately (same as syncVSAClusterHealthWithLock).
		logger.InfoContext(ctx, "Created new admin job spec, executing leaked resources monitoring immediately", "jobUUID", createdJobSpec.UUID)
		metrics.IncBackgroundTaskRun(leakedResourcesMonitoringJobType)
		defer releaseAdminJobSpecLock(ctx, se, leakedResourcesMonitoringJobType, leakedResourcesMonitoringLockTimeoutSeconds, logger)
		runCtx, cancel := context.WithTimeout(ctx, time.Duration(leakedResourcesMonitoringRunTimeoutSeconds)*time.Second)
		defer cancel()
		runCtx = injectLeakedResourcesRunID(runCtx, logger)
		if err := leakedresources.Run(runCtx, se, temporal); err != nil {
			logger.ErrorContext(runCtx, "Leaked resources monitoring failed", "error", err)
			metrics.IncBackgroundTaskError(leakedResourcesMonitoringJobType, "run")
		}
		return
	}

	if !errors.Is(createErr, vsaerrors.ErrAdminJobSpecAlreadyExists) {
		logger.ErrorContext(ctx, createErr.Error(), "error", createErr.Error())
		metrics.IncBackgroundTaskError(leakedResourcesMonitoringJobType, "create_admin_job_spec")
		return
	}

	// Job spec already exists, try to acquire lock by updating it
	logger.InfoContext(ctx, "Job spec already exists, attempting to acquire lock by updating", "jobType", leakedResourcesMonitoringJobType)

	currentTime := time.Now()
	lockThreshold := currentTime.Add(-time.Duration(leakedResourcesMonitoringLockTimeoutSeconds) * time.Second)

	rowsAffected, updateErr := se.UpdateAdminJobSpecWithLock(ctx, leakedResourcesMonitoringJobType, scheduler.JobStatusScheduled, lockThreshold, currentTime)
	if updateErr != nil {
		logger.ErrorContext(ctx, "Failed to acquire lock for admin job spec", "error", updateErr, "jobType", leakedResourcesMonitoringJobType)
		metrics.IncBackgroundTaskError(leakedResourcesMonitoringJobType, "acquire_lock")
		return
	}

	if rowsAffected > 0 {
		jobSpec, getErr := se.GetAdminJobSpecByJobType(ctx, leakedResourcesMonitoringJobType)
		if getErr != nil {
			logger.ErrorContext(ctx, "Failed to retrieve job spec after acquiring lock", "error", getErr, "jobType", leakedResourcesMonitoringJobType)
			metrics.IncBackgroundTaskError(leakedResourcesMonitoringJobType, "load_job_spec")
			return
		}

		logger.InfoContext(ctx, "Successfully acquired lock, executing leaked resources monitoring", "jobUUID", jobSpec.UUID)
		metrics.IncBackgroundTaskRun(leakedResourcesMonitoringJobType)
		defer releaseAdminJobSpecLock(ctx, se, leakedResourcesMonitoringJobType, leakedResourcesMonitoringLockTimeoutSeconds, logger)
		runCtx, cancel := context.WithTimeout(ctx, time.Duration(leakedResourcesMonitoringRunTimeoutSeconds)*time.Second)
		defer cancel()
		runCtx = injectLeakedResourcesRunID(runCtx, logger)
		if err := leakedresources.Run(runCtx, se, temporal); err != nil {
			logger.ErrorContext(runCtx, "Leaked resources monitoring failed", "error", err)
			metrics.IncBackgroundTaskError(leakedResourcesMonitoringJobType, "run")
		}
	} else {
		logger.InfoContext(ctx, "Could not acquire lock - another pod is currently executing the task or not enough time has passed since last execution")
	}
}

// injectLeakedResourcesRunID stamps a fresh run ID onto the context logger so
// every log line emitted during this pipeline run carries leaked_resources_run_id,
// making it easy to correlate logs from a single run and distinguish concurrent runs.
func injectLeakedResourcesRunID(ctx context.Context, base log.Logger) context.Context {
	runID := utils.RandomUUID()
	runLogger := base.With(log.Fields{"leaked_resources_run_id": runID})
	return context.WithValue(ctx, utilsmiddleware.ContextSLoggerKey, runLogger)
}

func handleGracefulShutdown(eg *errgroup.Group, ctx context.Context, httpServer *http.Server, logger log.Logger) {
	eg.Go(func() error {
		<-ctx.Done()
		logger.Info("Shutting down server")

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shut down server gracefully", "error", err.Error())
			return err
		}
		return nil
	})
}
