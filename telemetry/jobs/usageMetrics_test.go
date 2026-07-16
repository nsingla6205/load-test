package jobs

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestProcessUsageMetrics_Perform_Success(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Create valid job with correlation ID
	job := ProcessUsageMetrics{
		Data:          `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
		CorrelationID: "test-correlation-123",
	}

	// Setup mock expectation
	mockProcessor.On("ProcessUsageMetrics", mock.Anything, endTime).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestProcessUsageMetrics_Perform_SuccessWithoutCorrelationID(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Create valid job without correlation ID
	job := ProcessUsageMetrics{
		Data: `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
	}

	// Setup mock expectation
	mockProcessor.On("ProcessUsageMetrics", mock.Anything, endTime).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestProcessUsageMetrics_Perform_InvalidProcessorType(t *testing.T) {
	// Setup
	job := ProcessUsageMetrics{
		Data:          `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
		CorrelationID: "test-correlation-123",
	}

	// Execute with invalid processor type
	err := job.Perform(context.Background(), "invalid-processor", 1)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid processor type")
	assert.Contains(t, err.Error(), "string")
}

func TestProcessUsageMetrics_Perform_JSONUnmarshalError(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)

	// Create job with invalid JSON
	job := ProcessUsageMetrics{
		Data:          `{"invalid":"json"`,
		CorrelationID: "test-correlation-123",
	}

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected end of JSON input")
}

func TestProcessUsageMetrics_Perform_ProcessorError(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	processorError := errors.New("processor failed to process metrics")

	// Create valid job with correlation ID
	job := ProcessUsageMetrics{
		Data:          `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
		CorrelationID: "test-correlation-123",
	}

	// Setup mock to return error
	mockProcessor.On("ProcessUsageMetrics", mock.Anything, endTime).Return(processorError)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, processorError, err)
	mockProcessor.AssertExpectations(t)
}

func TestProcessUsageMetrics_Perform_ProcessorErrorWithoutCorrelationID(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	processorError := errors.New("processor failed to process metrics")

	// Create valid job without correlation ID
	job := ProcessUsageMetrics{
		Data: `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
	}

	// Setup mock to return error
	mockProcessor.On("ProcessUsageMetrics", mock.Anything, endTime).Return(processorError)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, processorError, err)
	mockProcessor.AssertExpectations(t)
}

func TestProcessUsageMetrics_Perform_ContextSetupWithCorrelationID(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Create job with correlation ID
	job := ProcessUsageMetrics{
		Data:          `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
		CorrelationID: "test-correlation-456",
	}

	// Custom matcher to verify context has correlation ID
	contextMatcher := mock.MatchedBy(func(ctx context.Context) bool {
		// Verify context is not the background context (has values set)
		return ctx != context.Background()
	})

	mockProcessor.On("ProcessUsageMetrics", contextMatcher, endTime).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestProcessUsageMetrics_Perform_EmptyCorrelationID(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Create job with empty correlation ID (should be treated as no correlation ID)
	job := ProcessUsageMetrics{
		Data:          `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
		CorrelationID: "",
	}

	// Should use background context when correlation ID is empty
	mockProcessor.On("ProcessUsageMetrics", mock.Anything, endTime).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestProcessUsageMetrics_Perform_MultipleAttempts(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Create valid job
	job := ProcessUsageMetrics{
		Data:          `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`,
		CorrelationID: "test-correlation-789",
	}

	// Test multiple attempts (should work the same way)
	for attempt := int32(1); attempt <= 3; attempt++ {
		mockProcessor.On("ProcessUsageMetrics", mock.Anything, endTime).Return(nil).Once()

		err := job.Perform(context.Background(), mockProcessor, attempt)
		assert.NoError(t, err)
	}

	mockProcessor.AssertExpectations(t)
}

func TestProcessUsageMetrics_Perform_JSONUnmarshalPartialError(t *testing.T) {
	// Setup
	mockProcessor := new(MockVCPProcessor)

	// Create job with JSON that has missing required fields
	job := ProcessUsageMetrics{
		Data:          `{"timestamp":"2024-01-15T12:00:00Z"}`,
		CorrelationID: "test-correlation-123",
	}

	// The unmarshal should succeed but with zero-value times
	zeroTime := time.Time{}
	mockProcessor.On("ProcessUsageMetrics", mock.Anything, zeroTime).Return(nil)

	// Execute
	err := job.Perform(context.Background(), mockProcessor, 1)

	// Assert - should succeed even with partial JSON
	assert.NoError(t, err)
	mockProcessor.AssertExpectations(t)
}

func TestNewProcessUsageMetrics(t *testing.T) {
	// Setup
	timestamp := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	startTime := time.Date(2024, 1, 15, 11, 0, 0, 0, time.UTC)
	endTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Execute
	job := NewProcessUsageMetrics(timestamp, startTime, endTime)

	// Assert
	assert.NotNil(t, job)
	assert.NotEmpty(t, job.Data)
	assert.Empty(t, job.CorrelationID) // Should be empty by default

	// Verify the JSON content
	expected := `{"timestamp":"2024-01-15T12:00:00Z","aggregation_start_time":"2024-01-15T11:00:00Z","aggregation_end_time":"2024-01-15T12:00:00Z"}`
	assert.JSONEq(t, expected, job.Data)
}

func TestProcessUsageMetrics_Load_Success(t *testing.T) {
	// Setup
	job := ProcessUsageMetrics{}
	data := `{"data":"test-data","correlation_id":"test-correlation"}`

	// Execute
	loadedJob, err := job.Load(data)

	// Assert
	assert.NoError(t, err)
	assert.IsType(t, ProcessUsageMetrics{}, loadedJob)

	loaded := loadedJob.(ProcessUsageMetrics)
	assert.Equal(t, "test-data", loaded.Data)
	assert.Equal(t, "test-correlation", loaded.CorrelationID)
}

func TestProcessUsageMetrics_Load_Error(t *testing.T) {
	// Setup
	job := ProcessUsageMetrics{}
	invalidData := `{"invalid":"json"`

	// Execute
	loadedJob, err := job.Load(invalidData)

	// Assert
	assert.Error(t, err)
	assert.Equal(t, ProcessUsageMetrics{}, loadedJob)
	assert.Contains(t, err.Error(), "unexpected end of JSON input")
}
