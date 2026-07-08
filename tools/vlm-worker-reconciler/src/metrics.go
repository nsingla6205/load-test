package main

import (
	"context"
	"log/slog"
	"time"

	cloudmetric "github.com/GoogleCloudPlatform/opentelemetry-operations-go/exporter/metric"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

const (
	outcomeSuccess        = "success"
	outcomePartialFailure = "partial_failure"
	outcomeSkipped        = "skipped"
)

type jobOutcome = string

type reconcileMetrics struct {
	provider *sdkmetric.MeterProvider
	runs     metric.Int64Counter
	duration metric.Float64Gauge
}

// setupMetrics creates an OTel MeterProvider backed by the GCP Cloud Monitoring
// exporter. Returns nil (no-op) when gcpProjectID is empty, so the job still
// exits cleanly in environments where the variable is not set.
func setupMetrics(ctx context.Context, gcpProjectID string, log *slog.Logger) *reconcileMetrics {
	if gcpProjectID == "" {
		log.Warn("OTEL_GOOGLE_PROJECT_ID not set — metrics emission disabled")
		return nil
	}

	exporter, err := cloudmetric.New(cloudmetric.WithProjectID(gcpProjectID))
	if err != nil {
		log.Error("failed to create GCP metric exporter — metrics disabled", "error", err)
		return nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String("vlm-worker-reconciler"),
		),
	)
	if err != nil {
		// Non-fatal: fall back to an empty resource.
		log.Warn("failed to build OTel resource, using default", "error", err)
		res = resource.Default()
	}

	// Use a long periodic interval — we always flush manually via ForceFlush
	// before the job exits, so the periodic tick is never needed.
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(time.Hour))),
		sdkmetric.WithResource(res),
	)

	meter := mp.Meter("vlm-worker-reconciler")

	runs, err := meter.Int64Counter(
		"vlm_worker_reconciler.runs_total",
		metric.WithDescription("Total number of vlm-worker-reconciler Job runs by outcome."),
	)
	if err != nil {
		log.Error("failed to create runs counter — metrics disabled", "error", err)
		_ = mp.Shutdown(ctx)
		return nil
	}

	dur, err := meter.Float64Gauge(
		"vlm_worker_reconciler.duration_seconds",
		metric.WithDescription("Duration of the last vlm-worker-reconciler Job run in seconds."),
	)
	if err != nil {
		log.Error("failed to create duration gauge — metrics disabled", "error", err)
		_ = mp.Shutdown(ctx)
		return nil
	}

	return &reconcileMetrics{provider: mp, runs: runs, duration: dur}
}

// emitAndFlush records outcome and duration metrics, then force-flushes all
// pending data to Google Cloud Monitoring. Safe to call on a nil receiver.
func (m *reconcileMetrics) emitAndFlush(ctx context.Context, outcome jobOutcome, dur time.Duration, log *slog.Logger) {
	if m == nil {
		return
	}

	attrs := metric.WithAttributes(attribute.String("outcome", outcome))
	m.runs.Add(ctx, 1, attrs)
	m.duration.Record(ctx, dur.Seconds(), attrs)

	fCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	if err := m.provider.ForceFlush(fCtx); err != nil {
		log.Error("failed to flush metrics to Cloud Monitoring", "error", err)
		return
	}
	log.Info("metrics pushed to Cloud Monitoring", "outcome", outcome, "duration_s", dur.Seconds())
}
