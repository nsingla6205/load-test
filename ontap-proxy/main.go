package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/endpoints"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/api/ontap-proxy-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/reverseproxy"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/ontap-proxy/ruleengine/dsl"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func main() {
	// Use signal.NotifyContext to handle termination signals
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := log.NewLogger()
	logger.Info("Starting ONTAP Proxy Service")

	// load config
	cfg := LoadConfig()

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

	// Initialize ontap-proxy metrics (must be called after SetupOpenTelemetry)
	if err := middleware.InitMetrics(); err != nil {
		logger.Error("Failed to initialize metrics", "error", err.Error())
		os.Exit(1)
	}

	// Create OpenAPI server with health endpoint handler
	handler := endpoints.Handler{}
	openAPIServer, err := oasgenserver.NewServer(handler)
	if err != nil {
		logger.Error("Failed to create OpenAPI server", "error", err.Error())
		os.Exit(1)
	}

	httpServer := setupHTTPServer(openAPIServer)
	httpServer.Addr = ":" + cfg.AppPort

	// Start HTTP server in a goroutine for graceful shutdown
	go func() {
		logger.Info("Starting HTTP server on " + cfg.AppPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Failed to start HTTP server", "error", err.Error())
		}
	}()

	// Wait for shutdown signal
	<-ctx.Done()
	logger.Info("Shutting down server...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", "error", err.Error())
	}

	logger.Info("Server stopped gracefully")
}

func setupHTTPServer(handler http.Handler) *http.Server {
	mux := chi.NewRouter()

	mux.Use(httphelpers.LoggingHttpHandler)
	mux.Use(log.LoggingMiddleware)
	mux.Use(log.RecoverMiddleware)
	// Metrics middleware BEFORE auth to capture auth failures
	// Only track passthrough routes (will be filtered in the middleware)
	mux.Use(middleware.MetricsMiddleware())
	mux.Use(auth.AuthMiddleware(false)) // false = enable project number validation

	ontapProxy := reverseproxy.BuildOntapRESTProxy()

	// ONTAP API routes
	mux.Route("/v1beta/projects/{projectId}/locations/{locationId}/pools/{poolId}/ontap", func(r chi.Router) {
		// Apply query transform to all ONTAP API routes (ogen-handled and passthrough)
		r.Use(middleware.QueryTransformMiddleware())

		// Ogen-handled routes (no chi middleware - ogen handler calls auth functions directly)
		// These handlers setup credentials internally via SetupCredentialsForHandler()

		// Snaplock file delete - delegated to ogen server which handles auth internally
		r.Delete("/api/storage/snaplock/file/{volumeUuid}/*", handler.ServeHTTP)

		// Snaplock litigation (legal hold) REST APIs - delegated to ogen server
		r.Get("/api/storage/snaplock/litigations", handler.ServeHTTP)
		r.Post("/api/storage/snaplock/litigations", handler.ServeHTTP)
		r.Get("/api/storage/snaplock/litigations/{litigationId}", handler.ServeHTTP)
		r.Delete("/api/storage/snaplock/litigations/{litigationId}", handler.ServeHTTP)

		r.Post("/api/storage/snaplock/litigations/{litigationId}/operations", handler.ServeHTTP)
		r.Get("/api/storage/snaplock/litigations/{litigationId}/operations/{operationId}", handler.ServeHTTP)
		r.Delete("/api/storage/snaplock/litigations/{litigationId}/operations/{operationId}", handler.ServeHTTP)

		// CLI execute - delegated to ogen server which handles auth and CLI rules internally
		r.Post("/api/private/cli", handler.ServeHTTP)

		// Event retention (EBR) policies - CLI-based, handled by ogen server (bulk DELETE/PATCH denied via rule engine)
		r.Get("/api/storage/snaplock/event-retention/policies", handler.ServeHTTP)
		r.Post("/api/storage/snaplock/event-retention/policies", handler.ServeHTTP)
		// DELETE and PATCH on collection not registered here so they fall through to passthrough and are denied by rule engine
		r.Get("/api/storage/snaplock/event-retention/policies/*", handler.ServeHTTP)
		r.Patch("/api/storage/snaplock/event-retention/policies/*", handler.ServeHTTP)
		r.Delete("/api/storage/snaplock/event-retention/policies/*", handler.ServeHTTP)

		// Event retention (EBR) operations - CLI-based, handled by ogen server
		r.Get("/api/storage/snaplock/event-retention/operations", handler.ServeHTTP)
		r.Post("/api/storage/snaplock/event-retention/operations", handler.ServeHTTP)
		r.Get("/api/storage/snaplock/event-retention/operations/*", handler.ServeHTTP)
		r.Delete("/api/storage/snaplock/event-retention/operations/*", handler.ServeHTTP)

		// QoS policy group instance operations - handled by ogen server.
		r.Get("/api/storage/qos/policies/{uuid}", handler.ServeHTTP)
		r.Patch("/api/storage/qos/policies/{uuid}", handler.ServeHTTP)
		r.Delete("/api/storage/qos/policies/{uuid}", handler.ServeHTTP)

		// Passthrough routes (chi middleware for reverse proxy)
		r.Group(func(r chi.Router) {
			r.Use(middleware.URLValidationMiddleware())
			r.Use(bodyLimitMiddleware(dsl.MaxRequestBodySize))
			r.Use(middleware.CredentialMiddleware()) // Routes admin vs gcnvadmin credentials
			r.Use(middleware.CertificateMiddleware())
			r.Use(middleware.RuleEngineMiddleware())
			r.Handle("/*", ontapProxy)
		})
	})

	// Mount OpenAPI server for /health endpoint (less specific, handles remaining routes)
	mux.Mount("/", handler)

	// Expose Prometheus metrics endpoint (AuthMiddleware automatically skips /metrics)
	mux.Handle("/metrics", promhttp.Handler())

	return &http.Server{
		Handler:           mux,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 2 * time.Second,
	}
}

// bodyLimitMiddleware limits request body size using http.MaxBytesReader
func bodyLimitMiddleware(maxBytes int) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, int64(maxBytes))
			next.ServeHTTP(w, r)
		})
	}
}
