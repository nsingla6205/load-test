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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler/ocischedules"
	dbtuils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	api "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/endpoints"
	ociserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/api/oci-servergen"
	ocimiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/oci-proxy/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"golang.org/x/sync/errgroup"
)

func main() {
	ctx := context.WithValue(context.Background(), utilsmiddleware.CorrelationContextKey, uuid.NewString())

	// Use signal.NotifyContext to handle termination signals
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := log.NewLogger()
	logger.Info("Starting OCI proxy API")

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

	// Register the VLM provider factory so wrapper types (provider_config,
	// net_config, provider_disk_config, provider_dev_flags) can be JSON-decoded
	// during synchronous request validation. oci-proxy only ever serves OCI.
	vlm.SetActiveProvider(vlm.OCICloud)
	logger.Info("VLM active provider set", "provider", vlm.OCICloud)

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

	// Create OCI proxy server and inject required dependencies (orchestrator uses same factory with DB + Temporal)
	temporalClient := workflowClient.GetTemporalClient()

	// Ensure the OCI certificate-rotation schedule on the customer task queue. OCI runs only a
	// customer worker, so this cannot use the background-bound admin-job framework. Non-fatal:
	// a scheduling hiccup must not block the API from serving.
	if err := ocischedules.EnsureOciCertificateRotationSchedule(ctx, temporalClient, logger); err != nil {
		logger.Error("Failed to ensure OCI certificate rotation schedule", "error", err.Error())
	}

	// Ensure the OCI temporary-password verification schedule on the customer task queue.
	// Non-fatal for the same reason: a scheduling hiccup must not block the API from serving.
	if err := ocischedules.EnsureOciTempPasswordVerificationSchedule(ctx, temporalClient, logger); err != nil {
		logger.Error("Failed to ensure OCI temp-password verification schedule", "error", err.Error())
	}

	orch := factory.GetOrchestratorForProvider(dbCon, temporalClient)
	serverState := api.NewServerState()
	newHandler := &api.Handler{Orchestrator: orch, ServerState: serverState, TemporalClient: temporalClient}
	ociServer, err := ociserver.NewServer(newHandler, ociserver.WithMiddleware(newHandler.IdempotencyMiddleware()))
	if err != nil {
		logger.Error("Failed to create server", "error", err.Error())
		os.Exit(1)
	}

	httpServer := setupHTTPServer(cfg, ociServer)

	// Use errgroup to manage goroutines and context
	eg, ctx := errgroup.WithContext(ctx)
	// Start HTTP server
	eg.Go(func() error {
		logger.Info("Starting HTTP server on " + httpServer.Addr)
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

func setupHTTPServer(cfg *common.Config, handler http.Handler) *http.Server {
	mux := chi.NewRouter()
	// Order (outer → inner): OPC headers / access log / context logger → auth → recover → API.
	h := handler
	h = log.RecoverMiddleware(h)
	h = ocimiddleware.WrapWithOCIAndLogging(h)
	mux.Mount("/", h)
	mux.Handle("/metrics", promhttp.Handler())

	addr := cfg.GCPHost + ":" + cfg.GCPPort
	if cfg.GCPHost == "" {
		addr = ":" + cfg.GCPPort
	}
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
}

func handleGracefulShutdown(eg *errgroup.Group, ctx context.Context, httpServer *http.Server, serverState *api.ServerState, logger log.Logger) {
	eg.Go(func() error {
		<-ctx.Done()
		logger.Info("Received shutdown signal, marking server as not ready")

		// Mark server as shutting down so health returns 500 and readiness fails.
		serverState.SetShuttingDown()

		// Wait for load balancer to deregister (readiness probe failure).
		failureThreshold := env.GetInt("READINESS_PROBE_FAILURE_THRESHOLD", 1)
		periodSeconds := env.GetInt("READINESS_PROBE_PERIOD_SECONDS", 5)
		shutdownWaitSeconds := failureThreshold * periodSeconds
		logger.Info("Waiting for load balancer to deregister pod",
			"failureThreshold", failureThreshold,
			"periodSeconds", periodSeconds,
			"waitDuration", shutdownWaitSeconds)
		time.Sleep(time.Duration(shutdownWaitSeconds) * time.Second)

		logger.Info("Shutting down HTTP server")
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
