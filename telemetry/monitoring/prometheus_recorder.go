package monitoring

import (
	"time"
)

const (
	sinkDeliveryStatusSuccess = "success"
	sinkDeliveryStatusFailure = "failure"
)

// PrometheusRecorder implements MetricsRecorder using Prometheus metrics
type PrometheusRecorder struct{}

func NewPrometheusRecorder() *PrometheusRecorder {
	return &PrometheusRecorder{}
}

func (p *PrometheusRecorder) RecordAPIRequest(params *MetricRecorderParams) {
	apiRequestsTotal.WithLabelValues(params.EndPoint, params.Method, params.StatusCode).Inc()
}

func (p *PrometheusRecorder) RecordAPILatency(params *MetricRecorderParams) {
	// If you have a latency histogram/summary metric
	apiLatency.WithLabelValues(params.EndPoint, params.Method).Observe(params.LatencyDuration)
}

func (p *PrometheusRecorder) RecordJobEnqueued(params *MetricRecorderParams) {
	jobsEnqueuedTotal.WithLabelValues(params.QueueName, params.JobType, params.JobStatus).Inc()
}

func (p *PrometheusRecorder) RecordJobBatchEnqueued(params *MetricRecorderParams) {
	jobsBatchEnqueuedTotal.WithLabelValues(params.QueueName, params.JobStatus).Inc()
}

func (p *PrometheusRecorder) RecordJobDequeued(params *MetricRecorderParams) {
	jobsDequeuedTotal.WithLabelValues(params.QueueName, params.JobType).Inc()
}

func (p *PrometheusRecorder) RecordJobProcessed(params *MetricRecorderParams) {
	if params.JobQuantity > 0 {
		jobsProcessedTotal.WithLabelValues(params.JobType, params.QueueName, params.JobStatus).Add(float64(params.JobQuantity))
	} else {
		jobsProcessedTotal.WithLabelValues(params.JobType, params.QueueName, params.JobStatus).Inc()
	}
}

func (p *PrometheusRecorder) RecordJobReclaimed(params *MetricRecorderParams) {
	if params.JobQuantity > 0 {
		jobsReclaimedTotal.WithLabelValues(params.QueueName, params.JobType).Add(float64(params.JobQuantity))
	} else {
		jobsReclaimedTotal.WithLabelValues(params.QueueName, params.JobType).Inc()
	}
}

func (p *PrometheusRecorder) RecordJobRetriesExhausted(params *MetricRecorderParams) {
	jobsRetriesExhaustedTotal.WithLabelValues(params.QueueName, params.JobType).Inc()
}

func (p *PrometheusRecorder) RecordJobNotifyFailed(params *MetricRecorderParams) {
	jobsNotifyFailedTotal.WithLabelValues(params.QueueName).Inc()
}

// RecordJobListenerEvent counts the transition and keeps the connected gauge in
// step, so a session that never attaches is visible as a level rather than only
// as a rate of failures.
func (p *PrometheusRecorder) RecordJobListenerEvent(params *MetricRecorderParams) {
	jobListenerEventsTotal.WithLabelValues(params.QueueName, params.ListenerEvent).Inc()

	switch params.ListenerEvent {
	case ListenerEventConnected, ListenerEventReconnected:
		jobListenerConnected.WithLabelValues(params.QueueName).Set(1)
	case ListenerEventDisconnected, ListenerEventConnectFailed:
		jobListenerConnected.WithLabelValues(params.QueueName).Set(0)
	}
}

func (p *PrometheusRecorder) RecordSinkDelivered(params *MetricRecorderParams) {
	if params.FailedQuantity > 0 {
		sinkDeliveredTotal.WithLabelValues(params.SinkType, sinkDeliveryStatusFailure).Add(params.FailedQuantity)
	}
	if params.SubmittedQuantity > 0 {
		sinkDeliveredTotal.WithLabelValues(params.SinkType, sinkDeliveryStatusSuccess).Add(params.SubmittedQuantity)
	}
}

func (p *PrometheusRecorder) RecordBillingMetricsSubmission(params *MetricRecorderParams) {
	submittedTimeStamp := params.SubmittedTimeStamp.Format(time.RFC3339)
	billingMetricsSubmissionTotal.WithLabelValues(params.SinkType, params.MeasuredType, submittedTimeStamp).Set(params.SubmittedQuantity)
}

func (p *PrometheusRecorder) RecordReportSuccess(report, sinkType string, completedAt time.Time) {
	if completedAt.IsZero() {
		return
	}
	if !isSupportedReportSink(report, sinkType) {
		return
	}
	reportLastSuccess.WithLabelValues(report, sinkType).Set(float64(completedAt.UTC().Unix()))
}

func isSupportedReportSink(report, sinkType string) bool {
	return (report == ReportBizOps && sinkType == SinkTypeGCS) ||
		(report == ReportUsageAggregation && sinkType == SinkTypeDatabase)
}
