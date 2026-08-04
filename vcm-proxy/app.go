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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	utilsmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	vcmapi "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/endpoints"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
	vcmconfig "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/config"
	vcmmiddleware "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/gowfbackend"
	"golang.org/x/sync/errgroup"
)

const workflowUIPath = "/workflow-ui"

func main() {
	ctx := context.WithValue(context.Background(), utilsmiddleware.CorrelationContextKey, uuid.NewString())
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := log.NewLogger()
	logger.Info("Starting VCM proxy API")
	cfg := vcmconfig.LoadServerConfig()

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

	orch := factory.GetOrchestratorForProvider(nil, nil)

	serverState := vcmapi.NewServerState()
	configStore := vcmconfig.NewStore(logger, cfg.DBValidateTimeout)
	handler := &vcmapi.Handler{
		Orchestrator: orch,
		ServerState:  serverState,
		ConfigStore:  configStore,
	}

	vcmServer, err := vcmserver.NewServer(handler)
	if err != nil {
		logger.Error("Failed to create VCM server", "error", err.Error())
		os.Exit(1)
	}

	if err := runServer(ctx, cfg, vcmServer, serverState, configStore, logger); err != nil {
		logger.Error("Server error", "error", err.Error())
		os.Exit(1)
	}
}

// runServer wires the go-workflows runner, HTTP server, and graceful shutdown.
// It blocks until ctx is cancelled or a goroutine returns an error.
func runServer(ctx context.Context, cfg *vcmconfig.ServerConfig, vcmServer http.Handler, serverState *vcmapi.ServerState, configStore *vcmconfig.Store, logger log.Logger) error {
	workflowRunner, err := gowfbackend.NewRunner(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to create go-workflows runner")
	}

	workflowUI := workflowUIHandler(workflowRunner, logger, cfg.WorkflowUIEnabled)
	httpServer := setupHTTPServer(cfg, vcmServer, workflowUI, configStore)

	// Start the worker with the errgroup context so an HTTP/server failure cancels polling before handleGracefulShutdown calls Stop().
	eg, ctx := errgroup.WithContext(ctx)
	if err := workflowRunner.Start(ctx); err != nil {
		_ = workflowRunner.Stop()
		return errors.Wrap(err, "failed to start go-workflows runner")
	}

	eg.Go(func() error {
		logger.Info("Starting VCM HTTP server on " + cfg.Host + ":" + cfg.Port)
		httpServer.Addr = cfg.Host + ":" + cfg.Port
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("VCM HTTP server error", "error", err.Error())
			return err
		}
		return nil
	})

	handleGracefulShutdown(ctx, eg, httpServer, serverState, workflowRunner, logger)

	if err := eg.Wait(); err != nil {
		return err
	}
	logger.Info("VCM server stopped gracefully")
	return nil
}

func setupHTTPServer(cfg *vcmconfig.ServerConfig, handler, workflowUI http.Handler, configStore *vcmconfig.Store) *http.Server {
	mux := chi.NewRouter()

	// Serve the diagnostic UI outside LoggingHttpHandler so static JS/CSS
	// and diag API responses are not dumped into request/response body logs.
	// LoggingMiddleware is still required so RecoverMiddleware has a logger in context.
	if workflowUI != nil {
		mux.Group(func(r chi.Router) {
			r.Use(log.LoggingMiddleware)
			r.Use(log.RecoverMiddleware)
			r.Mount(workflowUIPath, http.StripPrefix(workflowUIPath, workflowUI))
		})
	}

	api := chi.NewRouter()
	api.Use(httphelpers.LoggingHttpHandler)
	api.Use(log.LoggingMiddleware)
	api.Use(log.RecoverMiddleware)
	api.Use(auth.VCMAuthMiddleware(true))
	// Avoid passing a typed-nil *Store as ConfiguredChecker (nil interface check would fail).
	var configuredChecker vcmmiddleware.ConfiguredChecker
	if configStore != nil {
		configuredChecker = configStore
	}
	api.Use(vcmmiddleware.ConfiguredGate(configuredChecker, vcmmiddleware.DefaultAllowlist))
	api.Mount("/", handler)
	api.Handle("/metrics", promhttp.Handler())
	mux.Mount("/", api)

	return &http.Server{
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}
}

// workflowUIHandler builds the go-workflows diagnostic UI handler when enabled.
func workflowUIHandler(runner *gowfbackend.Runner, logger log.Logger, enabled bool) http.Handler {
	if !enabled {
		logger.Info("go-workflows UI disabled via VCM_WORKFLOW_UI_ENABLED")
		return nil
	}
	workflowUI, err := runner.DiagnosticsHandler()
	if err != nil {
		logger.Warn("go-workflows UI unavailable; continuing without diagnostic UI", "error", err.Error())
		return nil
	}
	logger.Info("Go workflows UI will be available at " + workflowUIPath)
	return workflowUI
}

func handleGracefulShutdown(ctx context.Context, eg *errgroup.Group, httpServer *http.Server, serverState *vcmapi.ServerState, workflowRunner *gowfbackend.Runner, logger log.Logger) {
	eg.Go(func() error {
		<-ctx.Done()
		logger.Info("Received shutdown signal, marking VCM server as not ready")
		serverState.SetShuttingDown()

		logger.Info("Shutting down VCM HTTP server")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer shutdownCancel()

		var shutdownErr error
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("Failed to shut down VCM server gracefully", "error", err.Error())
			shutdownErr = err
		} else {
			logger.Info("VCM HTTP server shut down successfully")
		}

		if err := workflowRunner.Stop(); err != nil {
			logger.Error("Failed to stop go-workflows runner", "error", err.Error())
			if shutdownErr == nil {
				shutdownErr = err
			}
		}
		return shutdownErr
	})
}
