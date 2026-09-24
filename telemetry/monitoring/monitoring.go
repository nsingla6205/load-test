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

// Job queue LISTEN session lifecycle events, reported as the "event" label on
// the listener event counter. A session that never reaches Connected delivers no
// notifications at all, which is otherwise invisible: the queue keeps draining on
// its fallback poll and every other metric looks healthy.
const (
	// ListenerEventConnected means the session attached and notifications are flowing.
	ListenerEventConnected = "connected"
	// ListenerEventReconnected means the session recovered after losing its connection.
	ListenerEventReconnected = "reconnected"
	// ListenerEventDisconnected means an attached session lost its connection.
	ListenerEventDisconnected = "disconnected"
	// ListenerEventConnectFailed means a connection attempt failed; the session is
	// not attached and will be retried.
	ListenerEventConnectFailed = "connect_failed"
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
	// ListenerEvent is one of the ListenerEvent* constants, naming a job queue
	// LISTEN session transition.
	ListenerEvent string
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

	// Job queue notification operations
	RecordJobNotifyFailed(params *MetricRecorderParams)
	RecordJobListenerEvent(params *MetricRecorderParams)

	// Sink operations
	RecordSinkDelivered(params *MetricRecorderParams)
	RecordBillingMetricsSubmission(params *MetricRecorderParams)
	RecordReportSuccess(report, sinkType string, completedAt time.Time)

	// Performance processing operations
	RecordPerformanceRunDuration(params *MetricRecorderParams)
}
