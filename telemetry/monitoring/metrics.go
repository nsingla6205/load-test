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
	vcpTelemetryJobsRetriesExhaustedTotal    = "vcp_telemetry_jobs_retries_exhausted_total"
	vcpTelemetryJobsNotifyFailedTotal        = "vcp_telemetry_jobs_notify_failed_total"
	vcpTelemetryJobListenerEventsTotal       = "vcp_telemetry_job_listener_events_total"
	vcpTelemetryJobListenerConnected         = "vcp_telemetry_job_listener_connected"
	vcpTelemetrySinkDeliveredTotal           = "vcp_telemetry_metrics_delivered_total"
	vcpTelemetryBillingMetricsSubmittedTotal = "vcp_telemetry_billing_metrics_submission_total"
	vcpTelemetryReportLastSuccess            = "vcp_telemetry_report_last_success_timestamp_seconds"
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

	// Counter for jobs with retries exhausted.
	jobsRetriesExhaustedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobsRetriesExhaustedTotal,
			Help: "Total number of jobs that exhausted their queue retry limit",
		},
		[]string{"queue", "job_type"},
	)

	// Counter for failed pg_notify attempts. The job row is already committed when
	// the notify runs, so a failure delays pickup to the next fallback poll rather
	// than losing work.
	jobsNotifyFailedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobsNotifyFailedTotal,
			Help: "Total number of failed pg_notify attempts when announcing enqueued jobs",
		},
		[]string{"queue"},
	)

	// Counter for LISTEN session lifecycle events, by ListenerEvent* name.
	jobListenerEventsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: vcpTelemetryJobListenerEventsTotal,
			Help: "Total number of job queue LISTEN session connection events by type",
		},
		[]string{"queue", "event"},
	)

	// Gauge tracking whether each queue has an attached LISTEN session. This is the
	// alertable signal for "notifications are silently not being delivered": a
	// queue pinned at 0 is running on its fallback poll alone.
	jobListenerConnected = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: vcpTelemetryJobListenerConnected,
			Help: "1 when the queue has an attached LISTEN session, 0 when notifications are not being delivered",
		},
		[]string{"queue"},
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
	reportLastSuccess = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: vcpTelemetryReportLastSuccess,
			Help: "Unix timestamp of the latest successfully completed telemetry report by report and sink.",
		},
		[]string{"report", "sink_type"},
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
		jobsRetriesExhaustedTotal,
		jobsNotifyFailedTotal,
		jobListenerEventsTotal,
		jobListenerConnected,
		sinkDeliveredTotal,
		billingMetricsSubmissionTotal,
		reportLastSuccess,
	)
}
