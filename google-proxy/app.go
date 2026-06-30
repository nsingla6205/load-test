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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/backgroundworkflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler/adminbackgroundjobs"
	_ "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/drivers/postgres"
	dbtuils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	api "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/endpoints"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := context.WithValue(context.Background(), utilsmiddleware.CorrelationContextKey, uuid.NewString())

	// Use signal.NotifyContext to handle termination signals
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := log.NewLogger()
	logger.Info("Starting gcp proxy API")

	vlm.SetActiveProvider(vlm.GCPCloud)

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

	cfg := common.LoadConfig()

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

	err = refreshAdminJobSpecs(ctx, cfg, dbCon, workflowClient.GetTemporalClient(), logger)
	if err != nil {
		logger.Errorf("Failed to refresh admin job specs: %v", err)
		os.Exit(1)
	}

	err = launchUpdateBackupScheduleWorkflow(ctx, cfg, workflowClient.GetTemporalClient(), logger)
	if err != nil {
		logger.Errorf("Failed to launch update backup schedule workflow: %v", err)
		os.Exit(1)
	}

	// Create GCP proxy server and inject required dependencies
	orch := factory.GetOrchestratorForProvider(dbCon, workflowClient.GetTemporalClient())
	serverState := api.NewServerState()
	newHandler := &api.Handler{Orchestrator: orch, ServerState: serverState} // inject the orchestrator and server state into the handler
	gcpServer, err := gcpgenserver.NewServer(newHandler)
	if err != nil {
		logger.Error("Failed to create server", "error", err.Error())
		os.Exit(1)
	}

	// TODO: add a flag to enable/disable the error handler
	// TODO: add middleware to handle error codes
	// Keeping errors.json in core for now, if needed we can merge two jsons together one in core and one in proxy layer later.
	_, err = vsaerrors.NewErrorHandler()
	if err != nil {
		logger.Error("Failed to create error handler", "error", err.Error())
		os.Exit(1)
	}

	httpServer := setupHTTPServer(cfg, gcpServer)

	// Use errgroup to manage goroutines and context
	eg, ctx := errgroup.WithContext(ctx)
	// Start HTTP server
	eg.Go(func() error {
		logger.Info("Starting HTTP server on " + cfg.GCPHost + ":" + cfg.GCPPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Failed to start HTTP server", "error", err.Error())
			return err
		}
		return nil
	})

	handleGracefulShutdown(eg, ctx, httpServer, serverState, logger)
	// Wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		logger.Error("Server error", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("Server stopped gracefully")
}

func GetDBConfig(cfg *common.Config) dbtuils.DbConfig {
	dbConfig := dbtuils.DbConfig{
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

func refreshAdminJobSpecs(ctx context.Context, cfg *common.Config, db database.Storage, temporal client.Client, logger log.Logger) error {
	// Always reload specs to ensure latest changes are picked up
	if err := adminbackgroundjobs.LoadJobSpecs(); err != nil {
		logger.Errorf("Failed to load admin job specs: %v", err)
		return err
	}

	if cfg.RefreshAdminJobSpecs {
		// Delete any existing schedules and reset DB specs to CREATING for re-creation
		if err := adminbackgroundjobs.RecreateAdminSchedules(ctx, db, temporal, logger); err != nil {
			logger.Errorf("Failed to prepare admin job specs for re-creation: %v", err)
			return err
		}
		// Re-create schedules by launching the job manager workflow
		if err := adminbackgroundjobs.LaunchJobManagerWorkflow(ctx, temporal, logger); err != nil {
			logger.Errorf("Failed to launch job manager workflow: %v", err)
			return err
		}
		logger.Info("Admin job schedules deleted if present and re-creation triggered")
	} else {
		// Delete all existing schedules and mark specs as DELETED
		if err := adminbackgroundjobs.DeleteAllAdminSchedules(ctx, db, temporal, logger); err != nil {
			logger.Errorf("Failed to delete existing admin schedules: %v", err)
			return err
		}
		logger.Info("Admin job schedules deleted because RefreshAdminJobSpecs=false")
	}

	return nil
}

func setupHTTPServer(cfg *common.Config, handler http.Handler) *http.Server {
	mux := chi.NewRouter()
	mux.Use(httphelpers.LoggingHttpHandler)
	mux.Use(log.LoggingMiddleware)
	mux.Use(auth.AuthMiddleware(false)) // false = do not skip project number validation. Must always be false for google proxy
	mux.Use(log.RecoverMiddleware)
	mux.Mount("/", handler)
	mux.Handle("/metrics", promhttp.Handler())

	return &http.Server{
		Addr:              cfg.GCPHost + ":" + cfg.GCPPort,
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
}

func launchUpdateBackupScheduleWorkflow(ctx context.Context, cfg *common.Config, temporalClient client.Client, logger log.Logger) error {
	if !cfg.UpdateBackupSchedules {
		logger.Info("Update backup schedule workflow skipped because UpdateBackupSchedules=false")
		return nil
	}

	_, err := temporalClient.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{
			TaskQueue:                workflowengine.BackgroundTaskQueue,
			ID:                       scheduler.UpdateBackupScheduleWorkflowID,
			WorkflowIDConflictPolicy: enums.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
			WorkflowRunTimeout:       *workflowengine.GetUpdateBackupScheduleWorkflowTimeout(),
		},
		backgroundworkflows.UpdateBackupScheduleWorkflow,
	)
	if err != nil {
		logger.Errorf("Failed to launch update backup schedule workflow: %v", err)
		return err
	}
	logger.Info("Successfully launched update backup schedule workflow")
	return nil
}

func handleGracefulShutdown(eg *errgroup.Group, ctx context.Context, httpServer *http.Server, serverState *api.ServerState, logger log.Logger) {
	eg.Go(func() error {
		<-ctx.Done()
		logger.Info("Received shutdown signal, marking server as not ready")

		// Mark server as shutting down immediately.
		// This causes the health endpoint to return 500, failing readiness probes.
		// The load balancer will stop routing new traffic to this pod.
		serverState.SetShuttingDown()

		// Wait for the load balancer to detect the failed health check and remove
		// this pod from the backend pool. The sleep duration is calculated based on
		// readiness probe configuration: failureThreshold × periodSeconds.
		// This ensures we wait long enough for the probe to fail and ILB to deregister.
		failureThreshold := env.GetInt("READINESS_PROBE_FAILURE_THRESHOLD", 1)
		periodSeconds := env.GetInt("READINESS_PROBE_PERIOD_SECONDS", 5)
		shutdownWaitSeconds := failureThreshold * periodSeconds
		logger.Info("Waiting for load balancer to deregister pod and drain connections",
			"failureThreshold", failureThreshold,
			"periodSeconds", periodSeconds,
			"waitDuration", shutdownWaitSeconds)
		time.Sleep(time.Duration(shutdownWaitSeconds) * time.Second)

		logger.Info("Shutting down HTTP server")
		// Allow up to 30 seconds for graceful shutdown to complete.
		// This gives time for in-flight requests to finish.
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shut down server gracefully", "error", err.Error())
			return err
		}
		logger.Info("HTTP server shut down successfully")
		return nil
	})
}
