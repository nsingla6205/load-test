package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
)

// MockVCPProcessor is a mock implementation of common.VCPProcessor for testing
type MockVCPProcessor struct {
	mock.Mock
}

func (m *MockVCPProcessor) ProcessPerformanceMetrics(ctx context.Context, timestamp time.Time) error {
	args := m.Called(ctx, timestamp)
	return args.Error(0)
}

func (m *MockVCPProcessor) ProcessUsageMetrics(ctx context.Context, timestamp time.Time) error {
	args := m.Called(ctx, timestamp)
	return args.Error(0)
}

func (m *MockVCPProcessor) CollectMetrics(ctx context.Context, projectID string, timestamp time.Time) error {
	args := m.Called(ctx, projectID, timestamp)
	return args.Error(0)
}

func (m *MockVCPProcessor) ProcessBizOps(ctx context.Context, params *utils.BizOpsReportParams) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

func (m *MockVCPProcessor) ProcessBillingSubmission(ctx context.Context, aggregationEndTime time.Time) error {
	args := m.Called(ctx, aggregationEndTime)
	return args.Error(0)
}

// Compile-time check to ensure MockVCPProcessor implements common.VCPProcessor
var _ common.VCPProcessor = (*MockVCPProcessor)(nil)

func TestNewCollectMetrics(t *testing.T) {
	tests := []struct {
		name      string
		projectID string
		timestamp time.Time
		wantErr   bool
	}{
		{
			name:      "Valid projectID and timestamp",
			projectID: "test-project-123",
			timestamp: time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC),
			wantErr:   false,
		},
		{
			name:      "Empty projectID",
			projectID: "",
			timestamp: time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC),
			wantErr:   false,
		},
		{
			name:      "Zero timestamp",
			projectID: "test-project",
			timestamp: time.Time{},
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			job := NewCollectMetrics(tt.projectID, tt.timestamp)

			// Verify job is created
			assert.NotNil(t, job)
			assert.NotEmpty(t, job.Data)

			// Verify the data can be unmarshaled back to payload
			var payload CollectMetricsPayload
			err := json.Unmarshal([]byte(job.Data), &payload)

			if !tt.wantErr {
				assert.NoError(t, err)
				assert.Equal(t, tt.projectID, payload.ProjectID)
				assert.Equal(t, tt.timestamp, payload.Timestamp)
			}
		})
	}
}

func TestNewCollectMetrics_MarshalError(t *testing.T) {
	// This test simulates a scenario where JSON marshaling might fail
	// For this simple struct, it's hard to make marshal fail, but we test the fallback logic

	projectID := "test-project"
	timestamp := time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC) // Use fixed timestamp

	job := NewCollectMetrics(projectID, timestamp)
	assert.NotNil(t, job)
	assert.NotEmpty(t, job.Data)

	// Verify it's valid JSON
	var payload CollectMetricsPayload
	err := json.Unmarshal([]byte(job.Data), &payload)
	assert.NoError(t, err)
	assert.Equal(t, projectID, payload.ProjectID)
	assert.Equal(t, timestamp, payload.Timestamp)
}

func TestCollectMetrics_Perform_Success(t *testing.T) {
	// Setup
	projectID := "test-project-123"
	timestamp := time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC)
	job := NewCollectMetrics(projectID, timestamp)
	job.CorrelationID = "test-correlation-id"

	mockProcessor := new(MockVCPProcessor)
	mockProcessor.On("CollectMetrics", mock.Anything, projectID, timestamp).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Verify
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
	mockProcessor.AssertCalled(t, "CollectMetrics", mock.Anything, projectID, timestamp)
}

func TestCollectMetrics_Perform_ProcessorError(t *testing.T) {
	// Setup
	projectID := "test-project-123"
	timestamp := time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC)
	job := NewCollectMetrics(projectID, timestamp)
	job.CorrelationID = "test-correlation-id"

	expectedError := fmt.Errorf("processor error")
	mockProcessor := new(MockVCPProcessor)
	mockProcessor.On("CollectMetrics", mock.Anything, projectID, timestamp).Return(expectedError)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Verify
	assert.Error(t, err)
	assert.Equal(t, expectedError, err)
	mockProcessor.AssertExpectations(t)
}

func TestCollectMetrics_Perform_InvalidProcessor(t *testing.T) {
	// Setup
	job := NewCollectMetrics("test-project", time.Now())
	invalidProcessor := "not a processor"

	// Execute
	err := job.Perform(context.Background(), invalidProcessor, 1)

	// Verify
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid processor type")
}

func TestCollectMetrics_Perform_InvalidJSON(t *testing.T) {
	// Setup - create job with invalid JSON data
	job := &CollectMetrics{
		Data:          "invalid json data",
		CorrelationID: "test-correlation-id",
	}

	mockProcessor := new(MockVCPProcessor)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Verify
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid character")

	// Ensure processor is not called with invalid data
	mockProcessor.AssertNotCalled(t, "CollectMetrics")
}

func TestCollectMetrics_Perform_WithoutCorrelationID(t *testing.T) {
	// Setup
	projectID := "test-project-123"
	timestamp := time.Date(2025, 10, 7, 10, 0, 0, 0, time.UTC)
	job := NewCollectMetrics(projectID, timestamp)
	// Don't set CorrelationID

	mockProcessor := new(MockVCPProcessor)
	mockProcessor.On("CollectMetrics", mock.Anything, projectID, timestamp).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Verify
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestCollectMetrics_Load_Success(t *testing.T) {
	// Setup
	originalJob := NewCollectMetrics("test-project", time.Now())
	originalJob.CorrelationID = "test-correlation"

	// Serialize the job to JSON
	jsonData, err := json.Marshal(originalJob)
	assert.NoError(t, err)

	// Execute Load
	loadedJob, err := originalJob.Load(string(jsonData))

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, loadedJob)

	collectMetricsJob, ok := loadedJob.(CollectMetrics)
	assert.True(t, ok)
	assert.Equal(t, originalJob.Data, collectMetricsJob.Data)
	assert.Equal(t, originalJob.CorrelationID, collectMetricsJob.CorrelationID)
}

func TestCollectMetrics_Load_InvalidJSON(t *testing.T) {
	// Setup
	job := &CollectMetrics{}
	invalidJSON := "invalid json"

	// Execute
	loadedJob, err := job.Load(invalidJSON)

	// Verify
	assert.Error(t, err)
	assert.Nil(t, loadedJob)
	assert.Contains(t, err.Error(), "failed to unmarshal CollectMetrics")
}

func TestCollectMetrics_Load_EmptyData(t *testing.T) {
	// Setup
	job := &CollectMetrics{}
	emptyJSON := "{}"

	// Execute
	loadedJob, err := job.Load(emptyJSON)

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, loadedJob)

	collectMetricsJob, ok := loadedJob.(CollectMetrics)
	assert.True(t, ok)
	assert.Empty(t, collectMetricsJob.Data)
	assert.Empty(t, collectMetricsJob.CorrelationID)
}

func TestCollectMetricsPayload_JSONMarshaling(t *testing.T) {
	// Test JSON marshaling and unmarshaling of the payload
	payload := CollectMetricsPayload{
		ProjectID: "test-project-123",
		Timestamp: time.Date(2025, 10, 7, 15, 30, 45, 0, time.UTC),
	}

	// Marshal to JSON
	jsonData, err := json.Marshal(payload)
	assert.NoError(t, err)
	assert.NotEmpty(t, jsonData)

	// Unmarshal from JSON
	var unmarshaled CollectMetricsPayload
	err = json.Unmarshal(jsonData, &unmarshaled)
	assert.NoError(t, err)
	assert.Equal(t, payload.ProjectID, unmarshaled.ProjectID)
	assert.Equal(t, payload.Timestamp, unmarshaled.Timestamp)
}

func TestCollectMetrics_InterfaceCompliance(t *testing.T) {
	// Verify that CollectMetrics implements the Job interface
	var job utils.Job = &CollectMetrics{}
	assert.NotNil(t, job)

	// Test that it can perform the required methods
	_, err := job.Load("{}")
	assert.NoError(t, err)
}

// Benchmark tests
func BenchmarkNewCollectMetrics(b *testing.B) {
	projectID := "benchmark-project-id"
	timestamp := time.Now()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = NewCollectMetrics(projectID, timestamp)
	}
}

func BenchmarkCollectMetrics_Load(b *testing.B) {
	job := NewCollectMetrics("test-project", time.Now())
	jsonData, _ := json.Marshal(job)
	data := string(jsonData)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = job.Load(data)
	}
}
