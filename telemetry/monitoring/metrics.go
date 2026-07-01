package monitoring

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	vcpTelemetryAPIRequestsTotal             = "vcp_telemetry_api_requests_total"
	vcpTelemetryAPIRequestDurationSeconds    = "vcp_telemetry_api_request_duration_seconds"
	vcpTelemetryJobsEnqueuedTotal            = "vcp_telemetry_jobs_enqueued_total"
	vcpTelemetryJobsEnqueuedBatchTotal       = "vcp_telemetry_jobs_batch_enqueued_total"
	vcpTelemetryJobsDeQueuedTotal            = "vcp_telemetry_jobs_dequeued_total"
	vcpTelemetryJobsProcessedTotal           = "vcp_telemetry_jobs_processed_total"
	vcpTelemetryJobsReclaimedTotal           = "vcp_telemetry_jobs_reclaimed_total"
	vcpTelemetrySinkDeliveredTotal           = "vcp_telemetry_metrics_delivered_total"
	vcpTelemetryBillingMetricsSubmittedTotal = "vcp_telemetry_billing_metrics_submission_total"
)

var (
	// Counter for api requests
	apiRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryAPIRequestsTotal,
			Help: "Total number of telemetry API requests",
		},
		[]string{"endpoint", "method", "status_code"},
	)

	// Counter for api requests latency
	apiLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    vcpTelemetryAPIRequestDurationSeconds,
			Help:    "API request latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"endpoint", "method"},
	)

	// Counter for jobs enqueued
	jobsEnqueuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobsEnqueuedTotal,
			Help: "Total number of jobs enqueued to the job queue",
		},
		[]string{"queue", "job_type", "status"},
	)

	// Counter for batch enqueue operations
	jobsBatchEnqueuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobsEnqueuedBatchTotal,
			Help: "Total number of jobs enqueued via batch operations",
		},
		[]string{"queue", "status"},
	)

	// Counter for jobs dequeued
	jobsDequeuedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobsDeQueuedTotal,
			Help: "Total number of jobs dequeued from the job queue",
		},
		[]string{"queue", "job_type"},
	)

	// Counter for job processed
	jobsProcessedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobsProcessedTotal,
			Help: "Total number of jobs processed",
		},
		[]string{"job_type", "queue", "status"},
	)

	// Counter for jobs reclaimed by the lease sweeper (crash/stuck detection)
	jobsReclaimedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobsReclaimedTotal,
			Help: "Total number of in-progress jobs reclaimed after their lease expired",
		},
		[]string{"queue", "job_type"},
	)

	// Counter for metrics delivered to external sinks
	sinkDeliveredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetrySinkDeliveredTotal,
			Help: "Total number of metrics delivered to external sinks",
		},
		[]string{"sink_type", "status"},
	)

	// Gauge for billing metrics submission
	billingMetricsSubmissionTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: vcpTelemetryBillingMetricsSubmittedTotal,
			Help: "Submitted quantity and timestamp for billing metrics.",
		},
		[]string{"sink_type", "measured_type", "timestamp"},
	)
)

func init() {
	RegisterCollector(
		apiRequestsTotal,
		apiLatency,
		jobsEnqueuedTotal,
		jobsBatchEnqueuedTotal,
		jobsDequeuedTotal,
		jobsProcessedTotal,
		jobsReclaimedTotal,
		sinkDeliveredTotal,
		billingMetricsSubmissionTotal,
	)
}
