package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof" // Enable pprof endpoints for performance profiling
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
	monitoring "cloud.google.com/go/monitoring/apiv3/v2"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/go-faster/errors"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/sync/errgroup"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/connection"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/aggregator"
	api "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/api/endpoints"
	coreapiserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/api/telemetry-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/bizops"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/collector"
	metricscommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/jobs"
	telementrymonitoring "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/performance"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/processor"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/usage"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/httphelpers"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	retryutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

const (
	workerPanicRestartBase        = time.Second
	workerPanicRestartMax         = 30 * time.Second
	workerPanicRestartMaxJitterMs = 300
)

var (
	migrateEnabled = env.GetBool("RUN_MIGRATION_ON_START", false)
)

func main() {
	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, uuid.NewString())
	ctx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger := util.GetLogger(ctx)
	logger.Info("Starting Telemetry Server")

	// TODO SHIVVAT defer DB connection close
	// defer cleanup(ctx)

	logger.Info("Initializing database connections...")
	// Initialize the VCP database connection
	VCPDbConn, err := connection.GetVcpDbConnection(ctx, logger)
	if err != nil {
		logger.Error("Failed to initialize VCP database connection", "error", err.Error())
		return
	}

	logger.Info("Successfully connected to VCP database...")

	logger.Info("Initializing metrics database...")
	// Initialize the telemetry database connection
	telemetryDbConn, err := connection.GetTelemetryDbConnection(ctx, logger)
	if err != nil {
		logger.Error("Failed to initialize Telemetry database connection", "error", err.Error())
		return
	}

	if migrateEnabled {
		err := telemetryDbConn.Migrate(ctx)
		if err != nil {
			logger.Error("Failed to run migrations on Telemetry database", "error", err.Error())
			return
		}
	}

	logger.Info("Successfully connected to Telemetry database...")

	tdb := telemetryDbConn.SQLDB()

	// Initialize metrics recorder
	metricsRecorder := telementrymonitoring.NewPrometheusRecorder()

	googleSink := performance.NewSink(ctx, metricscommon.LoadConfig(), metricsRecorder)
	billingSink := usage.NewSink(ctx, metricscommon.LoadConfig(), telemetryDbConn, metricsRecorder)
	bizopsSink, err := bizops.NewSink(bizops.GCP)
	if err != nil {
		logger.Warnf("Failed to initialize Bizops Sink:%v", err)
	}
	tenantProvider := collector.NewGoogleTenantProjectProvider(VCPDbConn)
	client, err := monitoring.NewMetricClient(ctx)
	if err != nil {
		logger.Warnf("Failed to create MetricClient: %v", err)
	}
	wrapper := collector.NewMetricClientWrapper(client)
	config := metricscommon.LoadMetricsConfigFromBytes()
	provider := collector.NewGoogleProvider(tenantProvider, wrapper, config.VolumeMetrics, googleSink)
	billingProvider := aggregator.NewBillingProvider(telemetryDbConn, VCPDbConn, metricscommon.LoadConfig(), billingSink)
	bizopsProvider := bizops.NewBizOpsProvider(telemetryDbConn, VCPDbConn, bizopsSink)
	metricsProcessor := processor.NewMetricsProcessor(VCPDbConn, telemetryDbConn, googleSink, provider, billingProvider, bizopsProvider)

	queue := utils.NewQueue(tdb, &metricsProcessor, metricsRecorder)
	provider.SetJobQueue(queue)
	billingProvider.SetJobQueue(queue)
	// provider.SetJobQueue(queue)
	// Create a new server instance with the API handler
	var gcpServer *coreapiserver.Server
	if gcpServer, err = coreapiserver.NewServer(api.NewHandler(VCPDbConn, telemetryDbConn, metricsProcessor, queue)); err != nil {
		logger.Error("Fatal error occurred", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("Successfully initialized Telemetry server...")

	// prometheus metrics endpoint
	mux := chi.NewRouter()
	mux.Use(chimiddleware.Recoverer)
	mux.Use(httphelpers.LoggingHttpHandler)
	mux.Use(log.LoggingMiddleware)
	mux.Use(telementrymonitoring.MetricsMiddlewareWithRecorder(metricsRecorder))
	mux.Mount("/", http.Handler(gcpServer))
	mux.Handle("/metrics", promhttp.Handler())

	// Enable pprof endpoints if ENABLE_PPROF is set
	pprofEnabled := env.GetBool("ENABLE_PPROF", false)
	if pprofEnabled {
		logger.Info("Enabling pprof endpoints at /debug/pprof/")
		mux.Route("/debug/pprof", func(r chi.Router) {
			r.HandleFunc("/", pprof.Index)
			r.HandleFunc("/cmdline", pprof.Cmdline)
			r.HandleFunc("/profile", pprof.Profile)
			r.HandleFunc("/symbol", pprof.Symbol)
			r.HandleFunc("/trace", pprof.Trace)
			r.HandleFunc("/goroutine", pprof.Handler("goroutine").ServeHTTP)
			r.HandleFunc("/heap", pprof.Handler("heap").ServeHTTP)
			r.HandleFunc("/allocs", pprof.Handler("allocs").ServeHTTP)
			r.HandleFunc("/block", pprof.Handler("block").ServeHTTP)
			r.HandleFunc("/mutex", pprof.Handler("mutex").ServeHTTP)
			r.HandleFunc("/threadcreate", pprof.Handler("threadcreate").ServeHTTP)
		})
	}

	cfg := common.LoadConfig()
	telemetryConfig := metricscommon.LoadConfig()
	// Setup HTTP server with proper timeouts
	httpServer := &http.Server{
		Addr:              ":" + cfg.MetricsServerPort,
		Handler:           mux,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
	}

	eg, _ := errgroup.WithContext(ctx)

	logger.Infof("Starting HTTP server on port %s", cfg.MetricsServerPort)
	eg.Go(func() error {
		logger.Info("Starting Telemetry server", "port", cfg.MetricsServerPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	logger.Infof("Telemetry server started with Performance Job workers %d", telemetryConfig.NumWorkersPerformance)
	logger.Infof("Telemetry server started with Usage Job workers %d", telemetryConfig.NumWorkersUsage)
	logger.Infof("Telemetry server started with Collection Job workers %d", telemetryConfig.NumWorkersCollection)
	logger.Infof("Telemetry server started with BizOps Job workers %d", telemetryConfig.NumWorkersBizOps)

	for i := 0; i < telemetryConfig.NumWorkersPerformance; i++ {
		goJobWorker(ctx, logger, fmt.Sprintf("performance-%d", i), func(workerCtx context.Context) error {
			return queue.Worker(workerCtx, []string{utils.PerformanceQueue}, &jobs.ProcessPerformanceMetrics{})
		})
	}

	for i := 0; i < telemetryConfig.NumWorkersUsage; i++ {
		goJobWorker(ctx, logger, fmt.Sprintf("usage-%d", i), func(workerCtx context.Context) error {
			return queue.Worker(workerCtx, []string{utils.UsageQueue}, &jobs.ProcessUsageMetrics{})
		})
	}

	for i := 0; i < telemetryConfig.NumWorkersCollection; i++ {
		goJobWorker(ctx, logger, fmt.Sprintf("collection-%d", i), func(workerCtx context.Context) error {
			return queue.Worker(workerCtx, []string{utils.CollectionQueue}, &jobs.CollectMetrics{})
		})
	}

	for i := 0; i < telemetryConfig.NumWorkersBizOps; i++ {
		goJobWorker(ctx, logger, fmt.Sprintf("bizops-%d", i), func(workerCtx context.Context) error {
			return queue.Worker(workerCtx, []string{utils.BizOpsReportQueue}, &jobs.BizOpsReport{})
		})
	}

	for i := 0; i < telemetryConfig.NumWorkersBillingRetry; i++ {
		goJobWorker(ctx, logger, fmt.Sprintf("billing-retry-%d", i), func(workerCtx context.Context) error {
			return queue.Worker(workerCtx, []string{utils.BillingRetryQueue}, &jobs.ProcessBillingSubmission{})
		})
	}

	// Lease sweeper: reclaims in-progress jobs whose lease expired (worker crash
	// or stuck job) so they can be retried. No-op when ENABLE_JOB_SWEEPER=false.
	go func() {
		if err := queue.RunSweeper(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Errorf(err.Error())
		}
	}()

	handleGracefulShutdown(eg, ctx, httpServer, logger)
	// Wait for all goroutines to finish
	if err := eg.Wait(); err != nil {
		logger.Error("Server error", "error", err.Error())
		os.Exit(1)
	}
	logger.Info("Server stopped gracefully")
	// Wait for the context to be done, it's an infinite loop.
}

// runWorkerOnce invokes fn once, recovering panics that escape queue.Worker.
// It returns true when fn panicked.
func runWorkerOnce(ctx context.Context, logger log.Logger, name string, fn func(context.Context) error) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			logger.ErrorContext(ctx, "telemetry worker panic",
				"worker", name,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	err := fn(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.ErrorContext(ctx, "telemetry worker exited",
			"worker", name,
			"error", err.Error(),
		)
	}
	return panicked
}

// goJobWorker restarts fn after panics that escape queue.Worker (outside runJob's recover).
func goJobWorker(ctx context.Context, logger log.Logger, name string, fn func(context.Context) error) {
	go func() {
		restartAttempt := 0

		for ctx.Err() == nil {
			panicked := runWorkerOnce(ctx, logger, name, fn)
			if ctx.Err() != nil || !panicked {
				return
			}

			delay := retryutils.ExponentialBackoffWithJitter(
				restartAttempt, workerPanicRestartBase, workerPanicRestartMax, workerPanicRestartMaxJitterMs,
			)
			restartAttempt++

			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
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
