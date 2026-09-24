package monitoring

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPrometheusRecorder(t *testing.T) {
	recorder := NewPrometheusRecorder()
	assert.NotNil(t, recorder)
}

func TestPrometheusRecorder_RecordAPIRequest(t *testing.T) {
	// Reset metrics before test
	apiRequestsTotal.Reset()

	recorder := NewPrometheusRecorder()
	params := &MetricRecorderParams{
		EndPoint:   "/api/v1/volumes",
		Method:     "POST",
		StatusCode: "200",
	}

	recorder.RecordAPIRequest(params)

	// Verify counter was incremented
	counter := apiRequestsTotal.WithLabelValues(params.EndPoint, params.Method, params.StatusCode)
	assert.Equal(t, float64(1), testutil.ToFloat64(counter))
}

func TestPrometheusRecorder_RecordAPILatency(t *testing.T) {
	// Reset metrics before test
	apiLatency.Reset()

	recorder := NewPrometheusRecorder()
	params := &MetricRecorderParams{
		EndPoint:        "/api/v1/volumes",
		Method:          "GET",
		LatencyDuration: 0.125,
	}

	recorder.RecordAPILatency(params)

	// Verify observation was recorded
	// For histograms, we need to check the count of observations
	assert.Equal(t, 1, testutil.CollectAndCount(apiLatency))
}

func TestPrometheusRecorder_RecordJobEnqueued(t *testing.T) {
	// Reset metrics before test
	jobsEnqueuedTotal.Reset()

	recorder := NewPrometheusRecorder()
	params := &MetricRecorderParams{
		QueueName: "backup-queue",
		JobType:   "backup",
		JobStatus: "pending",
	}

	recorder.RecordJobEnqueued(params)

	counter := jobsEnqueuedTotal.WithLabelValues(params.QueueName, params.JobType, params.JobStatus)
	assert.Equal(t, float64(1), testutil.ToFloat64(counter))
}

func TestPrometheusRecorder_RecordJobBatchEnqueued(t *testing.T) {
	// Reset metrics before test
	jobsBatchEnqueuedTotal.Reset()

	recorder := NewPrometheusRecorder()
	params := &MetricRecorderParams{
		QueueName: "volume-queue",
		JobStatus: "queued",
	}

	recorder.RecordJobBatchEnqueued(params)

	counter := jobsBatchEnqueuedTotal.WithLabelValues(params.QueueName, params.JobStatus)
	assert.Equal(t, float64(1), testutil.ToFloat64(counter))
}

func TestPrometheusRecorder_RecordJobDequeued(t *testing.T) {
	// Reset metrics before test
	jobsDequeuedTotal.Reset()

	recorder := NewPrometheusRecorder()
	params := &MetricRecorderParams{
		QueueName: "snapshot-queue",
		JobType:   "snapshot",
	}

	recorder.RecordJobDequeued(params)

	counter := jobsDequeuedTotal.WithLabelValues(params.QueueName, params.JobType)
	assert.Equal(t, float64(1), testutil.ToFloat64(counter))
}

func TestPrometheusRecorder_RecordJobProcessed(t *testing.T) {
	tests := []struct {
		name          string
		params        *MetricRecorderParams
		expectedValue float64
	}{
		{
			name: "single job processed",
			params: &MetricRecorderParams{
				JobType:     "backup",
				QueueName:   "backup-queue",
				JobStatus:   "success",
				JobQuantity: 0,
			},
			expectedValue: 1,
		},
		{
			name: "batch jobs processed",
			params: &MetricRecorderParams{
				JobType:     "replication",
				QueueName:   "replication-queue",
				JobStatus:   "success",
				JobQuantity: 5,
			},
			expectedValue: 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset metrics before each test
			jobsProcessedTotal.Reset()

			recorder := NewPrometheusRecorder()
			recorder.RecordJobProcessed(tt.params)

			counter := jobsProcessedTotal.WithLabelValues(tt.params.JobType, tt.params.QueueName, tt.params.JobStatus)
			assert.Equal(t, tt.expectedValue, testutil.ToFloat64(counter))
		})
	}
}

func TestPrometheusRecorder_RecordJobReclaimed(t *testing.T) {
	tests := []struct {
		name          string
		params        *MetricRecorderParams
		expectedValue float64
	}{
		{
			name: "single job reclaimed",
			params: &MetricRecorderParams{
				QueueName:   "all",
				JobType:     "all",
				JobQuantity: 0,
			},
			expectedValue: 1,
		},
		{
			name: "batch jobs reclaimed",
			params: &MetricRecorderParams{
				QueueName:   "all",
				JobType:     "all",
				JobQuantity: 3,
			},
			expectedValue: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobsReclaimedTotal.Reset()

			recorder := NewPrometheusRecorder()
			recorder.RecordJobReclaimed(tt.params)

			counter := jobsReclaimedTotal.WithLabelValues(tt.params.QueueName, tt.params.JobType)
			assert.Equal(t, tt.expectedValue, testutil.ToFloat64(counter))
		})
	}
}

func TestPrometheusRecorder_RecordJobRetriesExhausted(t *testing.T) {
	jobsRetriesExhaustedTotal.Reset()

	recorder := NewPrometheusRecorder()
	params := &MetricRecorderParams{
		QueueName: "usage",
		JobType:   "jobs.ProcessUsageMetrics",
	}

	recorder.RecordJobRetriesExhausted(params)

	counter := jobsRetriesExhaustedTotal.WithLabelValues(params.QueueName, params.JobType)
	assert.Equal(t, float64(1), testutil.ToFloat64(counter))
}

func TestPrometheusRecorder_RecordJobNotifyFailed(t *testing.T) {
	jobsNotifyFailedTotal.Reset()

	recorder := NewPrometheusRecorder()
	params := &MetricRecorderParams{QueueName: "performance"}

	recorder.RecordJobNotifyFailed(params)
	recorder.RecordJobNotifyFailed(params)

	counter := jobsNotifyFailedTotal.WithLabelValues(params.QueueName)
	assert.Equal(t, float64(2), testutil.ToFloat64(counter))
}

// TestPrometheusRecorder_RecordJobListenerEvent pins the gauge transitions as well
// as the counter, because the gauge is what makes "notifications are silently not
// being delivered" alertable: a queue pinned at 0 looks healthy in every other
// series.
func TestPrometheusRecorder_RecordJobListenerEvent(t *testing.T) {
	tests := []struct {
		name          string
		event         string
		wantConnected float64
	}{
		{name: "connected marks the queue attached", event: ListenerEventConnected, wantConnected: 1},
		{name: "reconnected marks the queue attached", event: ListenerEventReconnected, wantConnected: 1},
		{name: "disconnected marks the queue detached", event: ListenerEventDisconnected, wantConnected: 0},
		{name: "connect failure marks the queue detached", event: ListenerEventConnectFailed, wantConnected: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobListenerEventsTotal.Reset()
			jobListenerConnected.Reset()

			recorder := NewPrometheusRecorder()
			params := &MetricRecorderParams{QueueName: "performance", ListenerEvent: tt.event}

			recorder.RecordJobListenerEvent(params)

			counter := jobListenerEventsTotal.WithLabelValues(params.QueueName, tt.event)
			assert.Equal(t, float64(1), testutil.ToFloat64(counter), "the event must be counted")
			gauge := jobListenerConnected.WithLabelValues(params.QueueName)
			assert.Equal(t, tt.wantConnected, testutil.ToFloat64(gauge), "the connected gauge must track the event")
		})
	}
}

// TestPrometheusRecorder_RecordJobListenerEvent_RecoveryClearsGauge covers the
// transition that matters most for alerting: an outage followed by a reconnect has
// to clear the gauge, otherwise every recovered queue stays alarming.
func TestPrometheusRecorder_RecordJobListenerEvent_RecoveryClearsGauge(t *testing.T) {
	jobListenerEventsTotal.Reset()
	jobListenerConnected.Reset()

	recorder := NewPrometheusRecorder()
	gauge := jobListenerConnected.WithLabelValues("performance")

	recorder.RecordJobListenerEvent(&MetricRecorderParams{QueueName: "performance", ListenerEvent: ListenerEventConnectFailed})
	require.Equal(t, float64(0), testutil.ToFloat64(gauge))

	recorder.RecordJobListenerEvent(&MetricRecorderParams{QueueName: "performance", ListenerEvent: ListenerEventReconnected})
	assert.Equal(t, float64(1), testutil.ToFloat64(gauge))
}

// TestPrometheusRecorder_RecordJobListenerEvent_UnknownEventLeavesGauge keeps an
// unrecognised event from silently flipping the connection state.
func TestPrometheusRecorder_RecordJobListenerEvent_UnknownEventLeavesGauge(t *testing.T) {
	jobListenerEventsTotal.Reset()
	jobListenerConnected.Reset()

	recorder := NewPrometheusRecorder()
	gauge := jobListenerConnected.WithLabelValues("performance")

	recorder.RecordJobListenerEvent(&MetricRecorderParams{QueueName: "performance", ListenerEvent: ListenerEventConnected})
	require.Equal(t, float64(1), testutil.ToFloat64(gauge))

	recorder.RecordJobListenerEvent(&MetricRecorderParams{QueueName: "performance", ListenerEvent: "something_new"})
	assert.Equal(t, float64(1), testutil.ToFloat64(gauge), "an unknown event must not change the connection state")
}

func TestPrometheusRecorder_RecordSinkDelivered(t *testing.T) {
	tests := []struct {
		name                 string
		params               *MetricRecorderParams
		expectedSuccessValue float64
		expectedFailureValue float64
	}{
		{
			name: "only successful deliveries",
			params: &MetricRecorderParams{
				SinkType:          "gcs",
				SubmittedQuantity: 10,
				FailedQuantity:    0,
			},
			expectedSuccessValue: 10,
			expectedFailureValue: 0,
		},
		{
			name: "only failed deliveries",
			params: &MetricRecorderParams{
				SinkType:          "gcs",
				SubmittedQuantity: 0,
				FailedQuantity:    3,
			},
			expectedSuccessValue: 0,
			expectedFailureValue: 3,
		},
		{
			name: "mixed deliveries",
			params: &MetricRecorderParams{
				SinkType:          "s3",
				SubmittedQuantity: 7,
				FailedQuantity:    2,
			},
			expectedSuccessValue: 7,
			expectedFailureValue: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Reset metrics before each test
			sinkDeliveredTotal.Reset()

			recorder := NewPrometheusRecorder()
			recorder.RecordSinkDelivered(tt.params)

			successCounter := sinkDeliveredTotal.WithLabelValues(tt.params.SinkType, sinkDeliveryStatusSuccess)
			failureCounter := sinkDeliveredTotal.WithLabelValues(tt.params.SinkType, sinkDeliveryStatusFailure)

			assert.Equal(t, tt.expectedSuccessValue, testutil.ToFloat64(successCounter))
			assert.Equal(t, tt.expectedFailureValue, testutil.ToFloat64(failureCounter))
		})
	}
}

func TestPrometheusRecorder_RecordBillingMetricsSubmission(t *testing.T) {
	// Reset metrics before test
	billingMetricsSubmissionTotal.Reset()

	recorder := NewPrometheusRecorder()
	timestamp := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	params := &MetricRecorderParams{
		SinkType:           "billing-service",
		MeasuredType:       "BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE",
		SubmittedTimeStamp: timestamp,
		SubmittedQuantity:  100,
	}

	recorder.RecordBillingMetricsSubmission(params)

	expectedTimestamp := timestamp.Format(time.RFC3339)
	gauge := billingMetricsSubmissionTotal.WithLabelValues(params.SinkType, params.MeasuredType, expectedTimestamp)
	assert.Equal(t, float64(100), testutil.ToFloat64(gauge))
}

func TestPrometheusRecorder_RecordBillingMetricsSubmission_UpdatesExistingValue(t *testing.T) {
	// Reset metrics before test
	billingMetricsSubmissionTotal.Reset()

	recorder := NewPrometheusRecorder()
	timestamp := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)

	// First submission
	params1 := &MetricRecorderParams{
		SinkType:           "billing-service",
		SubmittedTimeStamp: timestamp,
		SubmittedQuantity:  100,
	}
	recorder.RecordBillingMetricsSubmission(params1)

	// Second submission with same timestamp should update the gauge
	params2 := &MetricRecorderParams{
		SinkType:           "billing-service",
		MeasuredType:       "BACKUP_ENABLED_VOLUME_ALLOCATED_SIZE",
		SubmittedTimeStamp: timestamp,
		SubmittedQuantity:  150,
	}
	recorder.RecordBillingMetricsSubmission(params2)

	expectedTimestamp := timestamp.Format(time.RFC3339)
	gauge := billingMetricsSubmissionTotal.WithLabelValues(params2.SinkType, params2.MeasuredType, expectedTimestamp)
	assert.Equal(t, float64(150), testutil.ToFloat64(gauge))
}

func TestPrometheusRecorder_RecordReportSuccess(t *testing.T) {
	completedAt := time.Date(2026, 8, 19, 6, 0, 0, 0, time.FixedZone("IST", 19800))
	tests := []struct {
		name        string
		report      string
		sinkType    string
		completedAt time.Time
		wantCount   int
	}{
		{
			name:        "records supported report",
			report:      ReportBizOps,
			sinkType:    SinkTypeGCS,
			completedAt: completedAt,
			wantCount:   1,
		},
		{
			name:      "ignores zero timestamp",
			report:    ReportBizOps,
			sinkType:  SinkTypeGCS,
			wantCount: 0,
		},
		{
			name:        "ignores unknown labels",
			report:      "unknown",
			sinkType:    "unknown",
			completedAt: completedAt,
			wantCount:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reportLastSuccess.Reset()
			NewPrometheusRecorder().RecordReportSuccess(tt.report, tt.sinkType, tt.completedAt)

			assert.Equal(t, tt.wantCount, testutil.CollectAndCount(reportLastSuccess))
			if tt.wantCount > 0 {
				assert.Equal(t, float64(tt.completedAt.UTC().Unix()), testutil.ToFloat64(
					reportLastSuccess.WithLabelValues(tt.report, tt.sinkType),
				))
			}
		})
	}
}

func TestPrometheusRecorder_MultipleOperations(t *testing.T) {
	// Test that multiple operations work together correctly
	recorder := NewPrometheusRecorder()

	// Reset all metrics
	apiRequestsTotal.Reset()
	apiLatency.Reset()
	jobsProcessedTotal.Reset()

	// Record multiple API requests
	for i := 0; i < 3; i++ {
		recorder.RecordAPIRequest(&MetricRecorderParams{
			EndPoint:   "/api/v1/volumes",
			Method:     "GET",
			StatusCode: "200",
		})
	}

	// Record latency
	recorder.RecordAPILatency(&MetricRecorderParams{
		EndPoint:        "/api/v1/volumes",
		Method:          "GET",
		LatencyDuration: 0.05,
	})

	// Record job processed
	recorder.RecordJobProcessed(&MetricRecorderParams{
		JobType:     "backup",
		QueueName:   "backup-queue",
		JobStatus:   "success",
		JobQuantity: 2,
	})

	// Verify all metrics
	apiCounter := apiRequestsTotal.WithLabelValues("/api/v1/volumes", "GET", "200")
	assert.Equal(t, float64(3), testutil.ToFloat64(apiCounter))

	jobCounter := jobsProcessedTotal.WithLabelValues("backup", "backup-queue", "success")
	assert.Equal(t, float64(2), testutil.ToFloat64(jobCounter))
}
