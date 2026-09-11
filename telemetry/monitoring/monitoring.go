package monitoring

import (
	"time"
)

const (
	// ReportBizOps identifies the BizOps report freshness series.
	ReportBizOps = "bizops"
	// ReportUsageAggregation identifies the usage aggregation freshness series.
	ReportUsageAggregation = "usage_aggregation"
	// SinkTypeGCS identifies the durable GCS report sink.
	SinkTypeGCS = "gcs"
	// SinkTypeDatabase identifies the usage aggregation database sink.
	SinkTypeDatabase = "database"
)

type MetricRecorderParams struct {
	EndPoint           string
	Method             string
	MeasuredType       string
	StatusCode         string
	LatencyDuration    float64
	QueueName          string
	JobType            string
	JobStatus          string
	JobQuantity        int
	SinkType           string
	SinkStatus         string
	ResourceType       string
	SubmittedQuantity  float64
	FailedQuantity     float64
	SubmittedTimeStamp time.Time
}

// MetricsRecorder defines the interface for recording metrics
type MetricsRecorder interface {
	// API operations
	RecordAPIRequest(params *MetricRecorderParams)
	RecordAPILatency(params *MetricRecorderParams)

	// Job operations
	RecordJobEnqueued(params *MetricRecorderParams)
	RecordJobBatchEnqueued(params *MetricRecorderParams)
	RecordJobDequeued(params *MetricRecorderParams)
	RecordJobProcessed(params *MetricRecorderParams)
	RecordJobReclaimed(params *MetricRecorderParams)
	RecordJobRetriesExhausted(params *MetricRecorderParams)

	// Sink operations
	RecordSinkDelivered(params *MetricRecorderParams)
	RecordBillingMetricsSubmission(params *MetricRecorderParams)
	RecordReportSuccess(report, sinkType string, completedAt time.Time)
}
