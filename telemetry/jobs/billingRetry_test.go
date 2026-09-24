package jobs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
)

// MockVCPProcessor is a mock implementation of common.VCPProcessor for testing
type MockVCPProcessorForRetry struct {
	mock.Mock
}

func (m *MockVCPProcessorForRetry) ProcessPerformanceMetrics(ctx context.Context, timestamp time.Time) error {
	args := m.Called(ctx, timestamp)
	return args.Error(0)
}

func (m *MockVCPProcessorForRetry) ProcessUsageMetrics(ctx context.Context, timestamp time.Time) error {
	args := m.Called(ctx, timestamp)
	return args.Error(0)
}

func (m *MockVCPProcessorForRetry) CollectMetrics(ctx context.Context, projectId string, timestamp time.Time) error {
	args := m.Called(ctx, projectId, timestamp)
	return args.Error(0)
}

func (m *MockVCPProcessorForRetry) ProcessBizOps(ctx context.Context, params *utils.BizOpsReportParams) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

func (m *MockVCPProcessorForRetry) ProcessBillingRetry(ctx context.Context, aggregationEndTime time.Time) error {
	args := m.Called(ctx, aggregationEndTime)
	return args.Error(0)
}

func (m *MockVCPProcessorForRetry) ProcessBillingSubmission(ctx context.Context, aggregationEndTime time.Time) error {
	args := m.Called(ctx, aggregationEndTime)
	return args.Error(0)
}

// Compile-time check to ensure MockVCPProcessorForRetry implements common.VCPProcessor
var _ common.VCPProcessor = (*MockVCPProcessorForRetry)(nil)

func TestNewDeliverBillingMetrics(t *testing.T) {
	tests := []struct {
		name               string
		aggregationEndTime time.Time
		wantErr            bool
	}{
		{
			name:               "Valid parameters",
			aggregationEndTime: time.Now(),
			wantErr:            false,
		},
		{
			name:               "Past time",
			aggregationEndTime: time.Now().Add(-1 * time.Hour),
			wantErr:            false,
		},
		{
			name:               "Future time",
			aggregationEndTime: time.Now().Add(1 * time.Hour),
			wantErr:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := NewDeliverBillingMetrics(tt.aggregationEndTime)

			assert.NotNil(t, job)
			assert.NotEmpty(t, job.Data)

			// Verify it's valid JSON
			var payload ProcessBillingSubmissionPayload
			err := json.Unmarshal([]byte(job.Data), &payload)
			assert.NoError(t, err)
			assert.Equal(t, tt.aggregationEndTime.Unix(), payload.AggregationEndTime.Unix())
		})
	}
}

func TestNewDeliverBillingMetrics_WithScheduledTime(t *testing.T) {
	aggregationEndTime := time.Now().Add(15 * time.Minute)

	job := NewDeliverBillingMetrics(aggregationEndTime)

	assert.NotNil(t, job)
	assert.NotEmpty(t, job.Data)

	// Verify it's valid JSON with aggregation end time
	var payload ProcessBillingSubmissionPayload
	err := json.Unmarshal([]byte(job.Data), &payload)
	assert.NoError(t, err)
	assert.WithinDuration(t, aggregationEndTime, payload.AggregationEndTime, time.Second)
}

func TestProcessBillingSubmission_Perform_InvalidProcessor(t *testing.T) {
	// Setup
	aggregationEndTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	job := NewDeliverBillingMetrics(aggregationEndTime)

	mockProcessor := new(MockVCPProcessorForRetry)
	mockProcessor.On("ProcessBillingSubmission", mock.Anything, aggregationEndTime).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Verify
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
	mockProcessor.AssertCalled(t, "ProcessBillingSubmission", mock.Anything, aggregationEndTime)
}

func TestProcessBillingSubmission_Perform_ProcessorError(t *testing.T) {
	// Setup
	aggregationEndTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	job := NewDeliverBillingMetrics(aggregationEndTime)

	mockProcessor := new(MockVCPProcessorForRetry)
	expectedError := assert.AnError
	mockProcessor.On("ProcessBillingSubmission", mock.Anything, aggregationEndTime).Return(expectedError)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Verify
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockProcessor.AssertExpectations(t)
}

func TestProcessBillingSubmission_Perform_InvalidProcessorType(t *testing.T) {
	// Setup
	job := NewDeliverBillingMetrics(time.Now())
	invalidProcessor := "not a processor"

	// Execute
	err := job.Perform(context.Background(), invalidProcessor, 1)

	// Verify
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid processor type")
}

func TestProcessBillingSubmission_Perform_InvalidJSON(t *testing.T) {
	// Setup - create job with invalid JSON data
	job := &ProcessBillingSubmission{
		Data:          "invalid json data",
		CorrelationID: "test-correlation-id",
	}

	mockProcessor := new(MockVCPProcessorForRetry)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Verify
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid character")

	// Ensure processor is not called with invalid data
	mockProcessor.AssertNotCalled(t, "ProcessBillingSubmission")
}

func TestProcessBillingSubmission_Load_Success(t *testing.T) {
	// Setup
	originalJob := NewDeliverBillingMetrics(time.Now())
	originalJob.CorrelationID = "test-correlation"

	// Serialize the job to JSON
	jsonData, err := json.Marshal(originalJob)
	assert.NoError(t, err)

	// Execute Load
	loadedJob, err := originalJob.Load(string(jsonData))

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, loadedJob)

	retryJob, ok := loadedJob.(ProcessBillingSubmission)
	assert.True(t, ok)
	assert.Equal(t, originalJob.Data, retryJob.Data)
	assert.Equal(t, originalJob.CorrelationID, retryJob.CorrelationID)
}

func TestProcessBillingSubmission_Load_InvalidJSON(t *testing.T) {
	// Setup
	job := NewDeliverBillingMetrics(time.Now())

	// Execute Load with invalid JSON
	loadedJob, err := job.Load("invalid json")

	// Verify
	assert.Error(t, err)
	assert.Nil(t, loadedJob)
	assert.Contains(t, err.Error(), "failed to unmarshal ProcessBillingSubmission")
}

func TestProcessBillingSubmission_Load_EmptyData(t *testing.T) {
	// Setup
	job := NewDeliverBillingMetrics(time.Now())

	// Execute Load with empty data
	loadedJob, err := job.Load("")

	// Verify
	assert.Error(t, err)
	assert.Nil(t, loadedJob)
}

func TestProcessBillingSubmissionPayload_JSONMarshaling(t *testing.T) {
	// Test JSON marshaling and unmarshaling of the payload
	payload := ProcessBillingSubmissionPayload{
		AggregationEndTime: time.Date(2025, 11, 25, 15, 30, 45, 0, time.UTC),
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(payload)
	assert.NoError(t, err)
	assert.NotEmpty(t, jsonData)

	// Unmarshal from JSON
	var unmarshaled ProcessBillingSubmissionPayload
	err = json.Unmarshal(jsonData, &unmarshaled)
	assert.NoError(t, err)
	assert.Equal(t, payload.AggregationEndTime, unmarshaled.AggregationEndTime)
}

func TestProcessBillingSubmission_InterfaceCompliance(t *testing.T) {
	// Verify that ProcessBillingSubmission implements the Job interface
	var job utils.Job = &ProcessBillingSubmission{}
	assert.NotNil(t, job)

	// Test that it can perform the required methods
	_, err := job.Load("{}")
	assert.NoError(t, err)
}
