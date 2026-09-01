package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"runtime/debug"
	"sync"
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
	baseCtx := context.WithValue(context.Background(), middleware.CorrelationContextKey, uuid.NewString())
	signalCtx, stopSignal := signal.NotifyContext(baseCtx, syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()

	jobCtx, cancelJobs := context.WithCancel(context.WithoutCancel(baseCtx))
	defer cancelJobs()

	logger := util.GetLogger(signalCtx)
	logger.Info("Starting Telemetry Server")

	// TODO SHIVVAT defer DB connection close
	// defer cleanup(ctx)

	logger.Info("Initializing database connections...")
	// Initialize the VCP database connection
	VCPDbConn, err := connection.GetVcpDbConnection(signalCtx, logger)
	if err != nil {
		logger.Error("Failed to initialize VCP database connection", "error", err.Error())
		return
	}

	logger.Info("Successfully connected to VCP database...")

	logger.Info("Initializing metrics database...")
	// Initialize the telemetry database connection
	telemetryDbConn, err := connection.GetTelemetryDbConnection(signalCtx, logger)
	if err != nil {
		logger.Error("Failed to initialize Telemetry database connection", "error", err.Error())
		return
	}

	if migrateEnabled {
		err := telemetryDbConn.Migrate(signalCtx)
		if err != nil {
			logger.Error("Failed to run migrations on Telemetry database", "error", err.Error())
			return
		}
	}

	logger.Info("Successfully connected to Telemetry database...")

	tdb := telemetryDbConn.SQLDB()

	// Initialize metrics recorder
	metricsRecorder := telementrymonitoring.NewPrometheusRecorder()

	googleSink := performance.NewSink(signalCtx, metricscommon.LoadConfig(), metricsRecorder)
	billingSink := usage.NewSink(signalCtx, metricscommon.LoadConfig(), telemetryDbConn, metricsRecorder)
	bizopsSink, err := bizops.NewSink(bizops.GCP)
	if err != nil {
		logger.Warnf("Failed to initialize Bizops Sink:%v", err)
	}
	tenantProvider := collector.NewGoogleTenantProjectProvider(VCPDbConn)
	client, err := monitoring.NewMetricClient(signalCtx)
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

	eg, pollCtx := errgroup.WithContext(signalCtx)

	logger.Infof("Starting HTTP server on port %s", cfg.MetricsServerPort)
	eg.Go(func() error {
		defer stopSignal()
		logger.Info("Starting Telemetry server", "port", cfg.MetricsServerPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	})

	var workerWG sync.WaitGroup
	totalWorkers := startJobWorkers(pollCtx, jobCtx, &workerWG, logger, queue, telemetryConfig, goJobWorker)

	shutdownDone := runShutdownCoordinatorAsync(shutdownCoordinatorConfig{
		logger:          logger,
		httpServer:      httpServer,
		budget:          telemetryConfig.ShutdownBudget,
		httpTimeout:     telemetryConfig.HTTPShutdownTimeout,
		finalizeReserve: utils.ShutdownFinalizeReserve,
		cancelJobs:      cancelJobs,
		workerWG:        &workerWG,
		pollCtx:         pollCtx,
		workerCount:     totalWorkers,
	})

	logTelemetryWorkerStartup(logger, telemetryConfig)

	drainResult := <-shutdownDone
	if err := eg.Wait(); err != nil {
		logger.Error("Server error", "error", err.Error())
		os.Exit(1)
	}
	logShutdownDrainResult(logger, drainResult)
}

func runWorkerOnce(pollCtx, jobCtx context.Context, logger log.Logger, name string, fn func(context.Context, context.Context) error) (panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			logger.ErrorContext(pollCtx, "telemetry worker panic",
				"worker", name,
				"panic", r,
				"stack", string(debug.Stack()),
			)
		}
	}()

	err := fn(pollCtx, jobCtx)
	if err != nil && !errors.Is(err, context.Canceled) {
		logger.ErrorContext(pollCtx, "telemetry worker exited",
			"worker", name,
			"error", err.Error(),
		)
	}
	return panicked
}

func goJobWorker(pollCtx, jobCtx context.Context, wg *sync.WaitGroup, logger log.Logger, name string, fn func(context.Context, context.Context) error) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		restartAttempt := 0

		for pollCtx.Err() == nil {
			panicked := runWorkerOnce(pollCtx, jobCtx, logger, name, fn)
			if pollCtx.Err() != nil || !panicked {
				return
			}

			delay := retryutils.ExponentialBackoffWithJitter(
				restartAttempt, workerPanicRestartBase, workerPanicRestartMax, workerPanicRestartMaxJitterMs,
			)
			restartAttempt++

			select {
			case <-pollCtx.Done():
				return
			case <-time.After(delay):
			}
		}
	}()
}

type jobWorkerStarter func(pollCtx, jobCtx context.Context, wg *sync.WaitGroup, logger log.Logger, name string, fn func(context.Context, context.Context) error)

func startJobWorkers(
	pollCtx, jobCtx context.Context,
	wg *sync.WaitGroup,
	logger log.Logger,
	queue *utils.JobQueue,
	cfg *metricscommon.TelemetryConfig,
	startWorker jobWorkerStarter,
) int {
	totalWorkers := cfg.NumWorkersPerformance +
		cfg.NumWorkersUsage +
		cfg.NumWorkersCollection +
		cfg.NumWorkersBizOps +
		cfg.NumWorkersBillingRetry

	for i := 0; i < cfg.NumWorkersPerformance; i++ {
		startWorker(pollCtx, jobCtx, wg, logger, fmt.Sprintf("performance-%d", i), func(poll, job context.Context) error {
			return queue.Worker(poll, job, []string{utils.PerformanceQueue}, &jobs.ProcessPerformanceMetrics{})
		})
	}

	for i := 0; i < cfg.NumWorkersUsage; i++ {
		startWorker(pollCtx, jobCtx, wg, logger, fmt.Sprintf("usage-%d", i), func(poll, job context.Context) error {
			return queue.Worker(poll, job, []string{utils.UsageQueue}, &jobs.ProcessUsageMetrics{})
		})
	}

	for i := 0; i < cfg.NumWorkersCollection; i++ {
		startWorker(pollCtx, jobCtx, wg, logger, fmt.Sprintf("collection-%d", i), func(poll, job context.Context) error {
			return queue.Worker(poll, job, []string{utils.CollectionQueue}, &jobs.CollectMetrics{})
		})
	}

	for i := 0; i < cfg.NumWorkersBizOps; i++ {
		startWorker(pollCtx, jobCtx, wg, logger, fmt.Sprintf("bizops-%d", i), func(poll, job context.Context) error {
			return queue.Worker(poll, job, []string{utils.BizOpsReportQueue}, &jobs.BizOpsReport{})
		})
	}

	for i := 0; i < cfg.NumWorkersBillingRetry; i++ {
		startWorker(pollCtx, jobCtx, wg, logger, fmt.Sprintf("billing-retry-%d", i), func(poll, job context.Context) error {
			return queue.Worker(poll, job, []string{utils.BillingRetryQueue}, &jobs.ProcessBillingSubmission{})
		})
	}

	if queue.SweeperEnabled() {
		totalWorkers++
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := queue.RunSweeper(pollCtx); err != nil && !errors.Is(err, context.Canceled) {
				logger.Error(err.Error())
			}
		}()
	}

	return totalWorkers
}

func logTelemetryWorkerStartup(logger log.Logger, cfg *metricscommon.TelemetryConfig) {
	logger.Infof("Telemetry server started with Performance Job workers %d", cfg.NumWorkersPerformance)
	logger.Infof("Telemetry server started with Usage Job workers %d", cfg.NumWorkersUsage)
	logger.Infof("Telemetry server started with Collection Job workers %d", cfg.NumWorkersCollection)
	logger.Infof("Telemetry server started with BizOps Job workers %d", cfg.NumWorkersBizOps)
	logger.Infof("Telemetry server started with Billing Retry Job workers %d", cfg.NumWorkersBillingRetry)
}

func logShutdownDrainResult(logger log.Logger, result shutdownResult) {
	if result.graceful {
		logger.Info("Server stopped gracefully", "elapsed", result.elapsed.String())
	} else {
		logger.Warn("Server shutdown completed after forced worker cancel", "elapsed", result.elapsed.String())
	}
}

func runShutdownCoordinatorAsync(cfg shutdownCoordinatorConfig) <-chan shutdownResult {
	done := make(chan shutdownResult, 1)
	go func() {
		done <- runShutdownCoordinator(cfg)
	}()
	return done
}
