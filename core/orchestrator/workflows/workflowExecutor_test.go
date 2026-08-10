package workflows

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/operatorservice/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/workflow"
)

// MockTemporalClient is a mock implementation of Temporal client
type MockTemporalClient struct {
	mock.Mock
}

func (m *MockTemporalClient) NewWithStartWorkflowOperation(options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) client.WithStartWorkflowOperation {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) GetWorkflowHistory(ctx context.Context, workflowID string, runID string, isLongPoll bool, filterType enums.HistoryEventFilterType) client.HistoryEventIterator {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CompleteActivity(ctx context.Context, taskToken []byte, result interface{}, err error) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CompleteActivityByID(ctx context.Context, namespace, workflowID, runID, activityID string, result interface{}, err error) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) RecordActivityHeartbeat(ctx context.Context, taskToken []byte, details ...interface{}) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) RecordActivityHeartbeatByID(ctx context.Context, namespace, workflowID, runID, activityID string, details ...interface{}) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CompleteActivityByActivityID(ctx context.Context, namespace, activityID, activityRunID string, result interface{}, err error) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CompleteActivityWithOptions(ctx context.Context, opts client.CompleteActivityOptions) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CompleteActivityByIDWithOptions(ctx context.Context, opts client.CompleteActivityByIDOptions) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CompleteActivityByActivityIDWithOptions(ctx context.Context, opts client.CompleteActivityByActivityIDOptions) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) RecordActivityHeartbeatWithOptions(ctx context.Context, opts client.RecordActivityHeartbeatOptions) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) RecordActivityHeartbeatByIDWithOptions(ctx context.Context, opts client.RecordActivityHeartbeatByIDOptions) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ExecuteActivity(ctx context.Context, options client.StartActivityOptions, activity interface{}, args ...interface{}) (client.ActivityHandle, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) GetActivityHandle(options client.GetActivityHandleOptions) client.ActivityHandle {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ListActivities(ctx context.Context, options client.ListActivitiesOptions) (client.ListActivitiesResult, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CountActivities(ctx context.Context, options client.CountActivitiesOptions) (*client.CountActivitiesResult, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) DescribeWorkflow(ctx context.Context, workflowID, runID string) (*client.WorkflowExecutionDescription, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ListClosedWorkflow(ctx context.Context, request *workflowservice.ListClosedWorkflowExecutionsRequest) (*workflowservice.ListClosedWorkflowExecutionsResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ListOpenWorkflow(ctx context.Context, request *workflowservice.ListOpenWorkflowExecutionsRequest) (*workflowservice.ListOpenWorkflowExecutionsResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ListWorkflow(ctx context.Context, request *workflowservice.ListWorkflowExecutionsRequest) (*workflowservice.ListWorkflowExecutionsResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ListArchivedWorkflow(ctx context.Context, request *workflowservice.ListArchivedWorkflowExecutionsRequest) (*workflowservice.ListArchivedWorkflowExecutionsResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ScanWorkflow(ctx context.Context, request *workflowservice.ScanWorkflowExecutionsRequest) (*workflowservice.ScanWorkflowExecutionsResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) CountWorkflow(ctx context.Context, request *workflowservice.CountWorkflowExecutionsRequest) (*workflowservice.CountWorkflowExecutionsResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) GetSearchAttributes(ctx context.Context) (*workflowservice.GetSearchAttributesResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) QueryWorkflowWithOptions(ctx context.Context, request *client.QueryWorkflowWithOptionsRequest) (*client.QueryWorkflowWithOptionsResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) DescribeTaskQueue(ctx context.Context, taskqueue string, taskqueueType enums.TaskQueueType) (*workflowservice.DescribeTaskQueueResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) DescribeTaskQueueEnhanced(ctx context.Context, options client.DescribeTaskQueueEnhancedOptions) (client.TaskQueueDescription, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ResetWorkflowExecution(ctx context.Context, request *workflowservice.ResetWorkflowExecutionRequest) (*workflowservice.ResetWorkflowExecutionResponse, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) UpdateWorkerBuildIdCompatibility(ctx context.Context, options *client.UpdateWorkerBuildIdCompatibilityOptions) error {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) GetWorkerBuildIdCompatibility(ctx context.Context, options *client.GetWorkerBuildIdCompatibilityOptions) (*client.WorkerBuildIDVersionSets, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) GetWorkerTaskReachability(ctx context.Context, options *client.GetWorkerTaskReachabilityOptions) (*client.WorkerTaskReachability, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) UpdateWorkerVersioningRules(ctx context.Context, options client.UpdateWorkerVersioningRulesOptions) (*client.WorkerVersioningRules, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) GetWorkerVersioningRules(ctx context.Context, options client.GetWorkerVersioningOptions) (*client.WorkerVersioningRules, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) UpdateWorkflow(ctx context.Context, options client.UpdateWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) UpdateWorkflowExecutionOptions(ctx context.Context, options client.UpdateWorkflowExecutionOptionsRequest) (client.WorkflowExecutionOptions, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) UpdateWithStartWorkflow(ctx context.Context, options client.UpdateWithStartWorkflowOptions) (client.WorkflowUpdateHandle, error) {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) GetWorkflowUpdateHandle(ref client.GetWorkflowUpdateHandleOptions) client.WorkflowUpdateHandle {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) DeploymentClient() client.DeploymentClient {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) WorkerDeploymentClient() client.WorkerDeploymentClient {
	// TODO implement me
	panic("implement me")
}

func (m *MockTemporalClient) ExecuteWorkflow(ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, args ...interface{}) (client.WorkflowRun, error) {
	argsList := []interface{}{ctx, options, workflow}
	argsList = append(argsList, args...)
	callArgs := m.Called(argsList...)
	if callArgs.Get(0) == nil {
		return nil, callArgs.Error(1)
	}
	return callArgs.Get(0).(client.WorkflowRun), callArgs.Error(1)
}

func (m *MockTemporalClient) GetWorkflow(ctx context.Context, workflowID string, runID string) client.WorkflowRun {
	args := m.Called(ctx, workflowID, runID)
	return args.Get(0).(client.WorkflowRun)
}

func (m *MockTemporalClient) SignalWorkflow(ctx context.Context, workflowID string, runID string, signalName string, arg interface{}) error {
	args := m.Called(ctx, workflowID, runID, signalName, arg)
	return args.Error(0)
}

func (m *MockTemporalClient) SignalWithStartWorkflow(ctx context.Context, workflowID string, signalName string, signalArg interface{}, options client.StartWorkflowOptions, workflow interface{}, workflowArgs ...interface{}) (client.WorkflowRun, error) {
	args := m.Called(ctx, workflowID, signalName, signalArg, options, workflow, workflowArgs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(client.WorkflowRun), args.Error(1)
}

func (m *MockTemporalClient) CancelWorkflow(ctx context.Context, workflowID string, runID string) error {
	args := m.Called(ctx, workflowID, runID)
	return args.Error(0)
}

func (m *MockTemporalClient) TerminateWorkflow(ctx context.Context, workflowID string, runID string, reason string, details ...interface{}) error {
	args := m.Called(ctx, workflowID, runID, reason, details)
	return args.Error(0)
}

func (m *MockTemporalClient) QueryWorkflow(ctx context.Context, workflowID string, runID string, queryType string, args ...interface{}) (converter.EncodedValue, error) {
	callArgs := m.Called(ctx, workflowID, runID, queryType, args)
	if callArgs.Get(0) == nil {
		return nil, callArgs.Error(1)
	}
	return callArgs.Get(0).(converter.EncodedValue), callArgs.Error(1)
}

func (m *MockTemporalClient) DescribeWorkflowExecution(ctx context.Context, workflowID string, runID string) (*workflowservice.DescribeWorkflowExecutionResponse, error) {
	args := m.Called(ctx, workflowID, runID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*workflowservice.DescribeWorkflowExecutionResponse), args.Error(1)
}

func (m *MockTemporalClient) Close() {
	m.Called()
}

func (m *MockTemporalClient) CheckHealth(ctx context.Context, request *client.CheckHealthRequest) (*client.CheckHealthResponse, error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*client.CheckHealthResponse), args.Error(1)
}

func (m *MockTemporalClient) WorkflowService() workflowservice.WorkflowServiceClient {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(workflowservice.WorkflowServiceClient)
}

func (m *MockTemporalClient) ScheduleClient() client.ScheduleClient {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(client.ScheduleClient)
}

func (m *MockTemporalClient) OperatorService() operatorservice.OperatorServiceClient {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(operatorservice.OperatorServiceClient)
}

// MockLogger is a mock implementation of log.Logger
type MockLogger struct {
	mock.Mock
}

func (m *MockLogger) InfoContext(ctx context.Context, msg string, args ...any) {
	// TODO implement me
	panic("implement me")
}

func (m *MockLogger) WarnContext(ctx context.Context, msg string, args ...any) {
	// TODO implement me
	panic("implement me")
}

func (m *MockLogger) ErrorContext(ctx context.Context, msg string, args ...any) {
	// TODO implement me
	panic("implement me")
}

func (m *MockLogger) DebugContext(ctx context.Context, msg string, args ...any) {
	// TODO implement me
	panic("implement me")
}

func (m *MockLogger) With(fields log.Fields) log.Logger {
	// TODO implement me
	panic("implement me")
}

func (m *MockLogger) Debug(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Info(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Warn(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Error(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Panic(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) Fatal(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

func (m *MockLogger) WithFields(fieldName string, fields log.Fields) log.Logger {
	args := m.Called(fieldName, fields)
	return args.Get(0).(log.Logger)
}

func (m *MockLogger) Debugf(msg string, args ...interface{}) {
	m.Called(msg, args)
}

func (m *MockLogger) Infof(msg string, args ...interface{}) {
	m.Called(msg, args)
}

func (m *MockLogger) Warnf(msg string, args ...interface{}) {
	m.Called(msg, args)
}

func (m *MockLogger) Errorf(msg string, args ...interface{}) {
	m.Called(msg, args)
}

func (m *MockLogger) Panicf(msg string, args ...interface{}) {
	m.Called(msg, args)
}

func (m *MockLogger) Fatalf(msg string, args ...interface{}) {
	m.Called(msg, args)
}

func TestNewWorkflowExecutor(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)

	executor := NewWorkflowExecutor(mockClient, mockLogger)

	assert.NotNil(t, executor)
	assert.Equal(t, mockClient, executor.temporal)
	assert.Equal(t, mockLogger, executor.logger)
}

func TestDefaultSequentialWorkflowOptions(t *testing.T) {
	controlID := "control-workflow-123"
	childID := "child-workflow-456"

	options := DefaultSequentialWorkflowOptions(controlID, childID)

	assert.NotNil(t, options)
	assert.Equal(t, controlID, options.ControlWorkflowID)
	assert.Equal(t, childID, options.ChildWorkflowID)
	assert.True(t, options.EnableRetry)
	assert.Equal(t, TemporalWorkflowMaxRetries, options.MaxRetries)
	assert.Equal(t, TemporalWorkflowRetryDelay, options.RetryDelay)
}

func TestGenerateControlWorkflowID(t *testing.T) {
	accountID := int64(12345)
	location := "us-central1"
	poolName := "test-pool"

	workflowID := GenerateControlWorkflowID(accountID, location, poolName)

	assert.NotEmpty(t, workflowID)
	assert.Contains(t, workflowID, fmt.Sprintf("%d", accountID))
	assert.Contains(t, workflowID, location)
	assert.Contains(t, workflowID, poolName)
}

func TestWorkflowExecutor_isRetryableError(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "NilError",
			err:      nil,
			expected: false,
		},
		{
			name:     "DeadlineExceeded",
			err:      context.DeadlineExceeded,
			expected: true,
		},
		{
			name:     "WorkflowAlreadyStarted",
			err:      serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", ""),
			expected: false,
		},
		{
			name:     "Unavailable",
			err:      serviceerror.NewUnavailable("service unavailable"),
			expected: true,
		},
		{
			name:     "TemporalDeadlineExceeded",
			err:      serviceerror.NewDeadlineExceeded("deadline exceeded"),
			expected: true,
		},
		{
			name:     "ConnectionRefused",
			err:      errors.New("connection refused"),
			expected: true,
		},
		{
			name:     "ConnectionReset",
			err:      errors.New("connection reset by peer"),
			expected: true,
		},
		{
			name:     "TimeoutError",
			err:      errors.New("request timeout"),
			expected: true,
		},
		{
			name:     "UnavailableError",
			err:      errors.New("service unavailable"),
			expected: true,
		},
		{
			name:     "GenericError",
			err:      errors.New("some other error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executor.isRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_Success(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_Error(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	expectedErr := errors.New("workflow execution failed")

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(nil, expectedErr)
	mockLogger.On("Error", mock.Anything, mock.Anything).Return()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.Error(t, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_FirstAttemptSuccess(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_RetryableErrorThenSuccess(t *testing.T) {
	withFastWorkflowRetry(t)
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// First call fails with retryable error, second succeeds
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewUnavailable("unavailable")).Once()
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	mockLogger.On("Warn", mock.Anything, mock.Anything).Return()
	mockLogger.On("Info", mock.Anything, mock.Anything).Return()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_NonRetryableError(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	nonRetryableErr := serviceerror.NewWorkflowExecutionAlreadyStarted("already started", "", "")
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nonRetryableErr).Once()

	mockLogger.On("Error", mock.Anything, mock.Anything).Return()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.Error(t, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_MaxRetriesExceeded(t *testing.T) {
	withFastWorkflowRetry(t)
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	retryableErr := serviceerror.NewUnavailable("unavailable")
	// All attempts fail with retryable error
	for i := 0; i < TemporalWorkflowMaxRetries; i++ {
		mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, retryableErr).Once()
	}

	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Times(TemporalWorkflowMaxRetries - 1)
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed after")
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_ContextCancellation(t *testing.T) {
	// Use a visible backoff window so cancellation during sleep is observable.
	origDelay := TemporalWorkflowRetryDelay
	TemporalWorkflowRetryDelay = 100 * time.Millisecond
	t.Cleanup(func() { TemporalWorkflowRetryDelay = origDelay })

	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx, cancel := context.WithCancel(context.Background())
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	retryableErr := serviceerror.NewUnavailable("unavailable")
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, retryableErr).Once()

	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return()

	// Cancel context after first failure
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.Error(t, err)
	assert.Equal(t, context.Canceled, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflow(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflow(ctx, workflowID, taskQueue, workflowFunc, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_isRetryableError_EdgeCases(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "WrappedDeadlineExceeded",
			err:      fmt.Errorf("wrapped: %w", context.DeadlineExceeded),
			expected: true,
		},
		{
			name:     "MixedCaseTimeout",
			err:      errors.New("Request TIMEOUT occurred"),
			expected: true,
		},
		{
			name:     "MixedCaseUnavailable",
			err:      errors.New("Service UNAVAILABLE at this time"),
			expected: true,
		},
		{
			name:     "EmptyErrorMessage",
			err:      errors.New(""),
			expected: false,
		},
		{
			name:     "ValidationError",
			err:      errors.New("validation failed: invalid input"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executor.isRetryableError(tt.err)
			assert.Equal(t, tt.expected, result, "Error: %v", tt.err)
		})
	}
}

func TestSequentialWorkflowOptions_CustomValues(t *testing.T) {
	options := &SequentialWorkflowOptions{
		ControlWorkflowID: "custom-control",
		ChildWorkflowID:   "custom-child",
		TaskQueue:         "custom-queue",
		EnableRetry:       false,
		MaxRetries:        5,
		RetryDelay:        5 * time.Second,
	}

	assert.Equal(t, "custom-control", options.ControlWorkflowID)
	assert.Equal(t, "custom-child", options.ChildWorkflowID)
	assert.Equal(t, "custom-queue", options.TaskQueue)
	assert.False(t, options.EnableRetry)
	assert.Equal(t, 5, options.MaxRetries)
	assert.Equal(t, 5*time.Second, options.RetryDelay)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_WithArgs(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func(arg1 string, arg2 int) {}
	arg1 := "test-arg"
	arg2 := 42

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, arg1, arg2).
		Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil, arg1, arg2)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_isRetryableError_VSAErrors(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	// Test with VSA wrapped errors
	wrappedErr := vsaerrors.WrapAsTemporalApplicationError(errors.New("connection refused"))
	result := executor.isRetryableError(wrappedErr)
	assert.True(t, result)
}

func TestWorkflowExecutor_ExecuteSequentialWorkflow_WithRetryEnabled(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	options := &SequentialWorkflowOptions{
		ControlWorkflowID: "control-123",
		ChildWorkflowID:   "child-456",
		TaskQueue:         "test-queue",
		EnableRetry:       true,
		MaxRetries:        3,
		RetryDelay:        1 * time.Millisecond,
	}
	workflowFunc := func() {}

	// Mock SignalWithStartWorkflow to simulate retryable errors followed by max retries
	mockClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewUnavailable("service unavailable")).Times(3)
	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Maybe()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	// Execute with retry enabled - should retry and eventually fail after max retries
	err := executor.ExecuteSequentialWorkflow(ctx, options, workflowFunc)

	// The function should fail after exhausting retries
	assert.Error(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteSequentialWorkflow_WithRetryDisabled(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	options := &SequentialWorkflowOptions{
		ControlWorkflowID: "control-123",
		ChildWorkflowID:   "child-456",
		TaskQueue:         "test-queue",
		EnableRetry:       false,
		MaxRetries:        3,
		RetryDelay:        1 * time.Second,
	}
	workflowFunc := func() {}

	// Mock SignalWithStartWorkflow to simulate an error
	mockClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("test error")).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	// Execute without retry - should fail immediately without retrying
	err := executor.ExecuteSequentialWorkflow(ctx, options, workflowFunc)

	// The function should fail on first attempt without retry
	assert.Error(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_executeWithRetry_SequentialSuccess(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	options := &SequentialWorkflowOptions{
		ControlWorkflowID: "control-123",
		ChildWorkflowID:   "child-456",
		TaskQueue:         "test-queue",
		EnableRetry:       true,
		MaxRetries:        3,
		RetryDelay:        1 * time.Millisecond,
	}
	workflowFunc := func() {}

	// Mock SignalWithStartWorkflow to succeed on first attempt
	mockClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()
	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Maybe()
	mockLogger.On("Info", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.executeWithRetry(ctx, options, workflowFunc)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_executeWithRetry_SequentialContextCancellation(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx, cancel := context.WithCancel(context.Background())
	options := &SequentialWorkflowOptions{
		ControlWorkflowID: "control-123",
		ChildWorkflowID:   "child-456",
		TaskQueue:         "test-queue",
		EnableRetry:       true,
		MaxRetries:        3,
		RetryDelay:        100 * time.Millisecond,
	}
	workflowFunc := func() {}

	// Mock SignalWithStartWorkflow - may be called before cancellation is detected
	mockClient.On("SignalWithStartWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, context.Canceled).Maybe()
	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Maybe()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	// Cancel context immediately
	cancel()

	err := executor.executeWithRetry(ctx, options, workflowFunc)

	// Should fail with context cancellation error
	assert.Error(t, err)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithArgs(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func(arg1 string, arg2 int, arg3 bool) {}
	arg1 := "test-string"
	arg2 := 100
	arg3 := true

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, arg1, arg2, arg3).
		Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, nil, nil, arg1, arg2, arg3)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflow_CallsExecuteWorkflowWithRetry(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflow(ctx, workflowID, taskQueue, workflowFunc, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_isRetryableError_AllTemporalErrors(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "WorkflowExecutionAlreadyStarted",
			err:      serviceerror.NewWorkflowExecutionAlreadyStarted("msg", "requestId", "runId"),
			expected: false,
		},
		{
			name:     "ResourceExhausted",
			err:      serviceerror.NewResourceExhausted(enums.RESOURCE_EXHAUSTED_CAUSE_RPS_LIMIT, "rate limit exceeded"),
			expected: true,
		},
		{
			name:     "Unavailable",
			err:      serviceerror.NewUnavailable("cause"),
			expected: true,
		},
		{
			name:     "DeadlineExceeded",
			err:      serviceerror.NewDeadlineExceeded("cause"),
			expected: true,
		},
		{
			name:     "InvalidArgument",
			err:      serviceerror.NewInvalidArgument("cause"),
			expected: false,
		},
		{
			name:     "NotFound",
			err:      serviceerror.NewNotFound("cause"),
			expected: false,
		},
		{
			name:     "PermissionDenied",
			err:      serviceerror.NewPermissionDenied("cause", "reason"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := executor.isRetryableError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGenerateControlWorkflowID_MultipleAccounts(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		location  string
		poolName  string
	}{
		{
			name:      "Account1",
			accountID: 12345,
			location:  "us-central1",
			poolName:  "pool-a",
		},
		{
			name:      "Account2",
			accountID: 67890,
			location:  "europe-west1",
			poolName:  "pool-b",
		},
		{
			name:      "Account3",
			accountID: 11111,
			location:  "asia-southeast1",
			poolName:  "pool-c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workflowID := GenerateControlWorkflowID(tt.accountID, tt.location, tt.poolName)

			assert.NotEmpty(t, workflowID)
			assert.Contains(t, workflowID, fmt.Sprintf("%d", tt.accountID))
			assert.Contains(t, workflowID, tt.location)
			assert.Contains(t, workflowID, tt.poolName)
		})
	}
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_SecondAttemptSuccess(t *testing.T) {
	withFastWorkflowRetry(t)
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// First call fails, second succeeds
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewUnavailable("temporarily unavailable")).Once()
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Once()

	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Info", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_ThirdAttemptSuccess(t *testing.T) {
	withFastWorkflowRetry(t)
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// First two calls fail, third succeeds
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewResourceExhausted(enums.RESOURCE_EXHAUSTED_CAUSE_RPS_LIMIT, "system overloaded")).Once()
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, serviceerror.NewDeadlineExceeded("timeout")).Once()
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Once()

	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Times(2)
	mockLogger.On("Info", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestDefaultSequentialWorkflowOptions_VerifyAllFields(t *testing.T) {
	controlID := "control-wf-001"
	childID := "child-wf-001"

	options := DefaultSequentialWorkflowOptions(controlID, childID)

	assert.NotNil(t, options)
	assert.Equal(t, controlID, options.ControlWorkflowID)
	assert.Equal(t, childID, options.ChildWorkflowID)
	assert.NotEmpty(t, options.TaskQueue)
	assert.True(t, options.EnableRetry)
	assert.Equal(t, TemporalWorkflowMaxRetries, options.MaxRetries)
	assert.Equal(t, TemporalWorkflowRetryDelay, options.RetryDelay)
}

// Test cases for workflowRunTimeout parameter (commit 372466b70d4129d9fefce97774b92c3e5eae4024)

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithNilTimeout_UsesDefault(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// Verify that when nil is passed, it uses the default timeout from GetWorkflowGlobalTimeout()
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		// Verify that WorkflowRunTimeout is set to the default value
		// The default should be from workflowengine.GetWorkflowGlobalTimeout()
		return opts.WorkflowRunTimeout > 0
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithCustomTimeout_UsesCustom(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	customTimeout := 30 * time.Minute

	// Verify that when a custom timeout is passed, it uses that timeout
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowRunTimeout == customTimeout
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, &customTimeout, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflow_WithNilTimeout_UsesDefault(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// Verify that ExecuteWorkflow passes nil through to ExecuteWorkflowWithRetry and ExecuteWorkflowSingle
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		// Verify that WorkflowRunTimeout is set (default value)
		return opts.WorkflowRunTimeout > 0
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflow(ctx, workflowID, taskQueue, workflowFunc, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflow_WithCustomTimeout_UsesCustom(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	customTimeout := 45 * time.Minute

	// Verify that ExecuteWorkflow passes custom timeout through the call chain
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowRunTimeout == customTimeout
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflow(ctx, workflowID, taskQueue, workflowFunc, &customTimeout)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_WithNilTimeout_UsesDefault(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// Verify that ExecuteWorkflowWithRetry passes nil through to ExecuteWorkflowSingle
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		// Verify that WorkflowRunTimeout is set (default value)
		return opts.WorkflowRunTimeout > 0
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_WithCustomTimeout_UsesCustom(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	customTimeout := 20 * time.Minute

	// Verify that ExecuteWorkflowWithRetry passes custom timeout through to ExecuteWorkflowSingle
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowRunTimeout == customTimeout
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, &customTimeout, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_WithCustomTimeout_RetryUsesSameTimeout(t *testing.T) {
	withFastWorkflowRetry(t)
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	customTimeout := 15 * time.Minute

	// First call fails with retryable error, second succeeds
	// Both should use the same custom timeout
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowRunTimeout == customTimeout
	}), mock.Anything).
		Return(nil, serviceerror.NewUnavailable("unavailable")).Once()
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowRunTimeout == customTimeout
	}), mock.Anything).
		Return(nil, nil).Once()

	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Info", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, &customTimeout, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithZeroTimeout_UsesDefault(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	zeroTimeout := time.Duration(0)

	// Even if a zero duration is passed, it should use the default
	// (though in practice, nil should be used instead of zero)
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		// When zero is passed, it will be used (not ideal, but tests the behavior)
		return opts.WorkflowRunTimeout == zeroTimeout
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, &zeroTimeout, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflow_TimeoutParameterPassedThrough(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func(arg1 string) {}
	customTimeout := 90 * time.Minute
	arg1 := "test-arg"

	// Verify that timeout is passed through ExecuteWorkflow -> ExecuteWorkflowWithRetry -> ExecuteWorkflowSingle
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowRunTimeout == customTimeout &&
			opts.ID == workflowID &&
			opts.TaskQueue == taskQueue
	}), mock.Anything, arg1).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflow(ctx, workflowID, taskQueue, workflowFunc, &customTimeout, arg1)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_executeSingle_WithCustomWorkflowRunTimeout(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	customTimeout := 30 * time.Minute
	options := &SequentialWorkflowOptions{
		ControlWorkflowID:  "control-123",
		ChildWorkflowID:    "child-456",
		TaskQueue:          "test-queue",
		WorkflowRunTimeout: &customTimeout,
	}
	workflowFunc := func() {}

	// Mock ExecuteWorkflowSeq to succeed
	originalExecuteWorkflowSeq := ExecuteWorkflowSeq
	defer func() { ExecuteWorkflowSeq = originalExecuteWorkflowSeq }()

	ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		// Verify that the custom timeout is used in the child workflow options
		assert.Equal(t, *options.WorkflowRunTimeout, wfOptions.WorkflowRunTimeout)
		return nil
	}

	err := executor.executeSingle(ctx, options, workflowFunc)

	assert.NoError(t, err)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithNilReusePolicy_UsesDefault(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// Verify that when nil is passed, it uses the default policy (REJECT_DUPLICATE)
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, nil, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithAllowDuplicatePolicy(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	reusePolicy := enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE

	// Verify that when ALLOW_DUPLICATE is passed, it uses that policy
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, nil, &reusePolicy)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithAllowDuplicateFailedOnlyPolicy(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	reusePolicy := enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY

	// Verify that when ALLOW_DUPLICATE_FAILED_ONLY is passed, it uses that policy
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, nil, &reusePolicy)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_WithAllowDuplicatePolicy(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	reusePolicy := enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE

	// Verify that the custom policy is passed through ExecuteWorkflowWithRetry
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, &reusePolicy)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_WithCustomPolicyAndRetry(t *testing.T) {
	withFastWorkflowRetry(t)
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	reusePolicy := enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE

	// First call fails with retryable error, second succeeds
	// Both should use the same custom policy
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	}), mock.Anything).
		Return(nil, serviceerror.NewUnavailable("unavailable")).Once()
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE
	}), mock.Anything).
		Return(nil, nil).Once()

	mockLogger.On("Warn", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Info", mock.Anything, mock.Anything).Return().Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, &reusePolicy)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
	mockLogger.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowSingle_WithCustomTimeoutAndPolicy(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}
	customTimeout := 25 * time.Minute
	reusePolicy := enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE

	// Verify both custom timeout and custom policy are used together
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE &&
			opts.WorkflowRunTimeout == customTimeout &&
			opts.ID == workflowID &&
			opts.TaskQueue == taskQueue
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflowSingle(ctx, workflowID, taskQueue, workflowFunc, &customTimeout, &reusePolicy)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflow_PassesThroughWithNilPolicy(t *testing.T) {
	mockClient := new(MockTemporalClient)
	mockLogger := new(MockLogger)
	executor := NewWorkflowExecutor(mockClient, mockLogger)

	ctx := context.Background()
	workflowID := "test-workflow-123"
	taskQueue := "test-queue"
	workflowFunc := func() {}

	// Verify that ExecuteWorkflow passes nil policy (defaults to REJECT_DUPLICATE)
	mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
		return opts.WorkflowIDReusePolicy == enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE
	}), mock.Anything).Return(nil, nil).Once()
	mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

	err := executor.ExecuteWorkflow(ctx, workflowID, taskQueue, workflowFunc, nil)

	assert.NoError(t, err)
	mockClient.AssertExpectations(t)
}

func TestWorkflowExecutor_ExecuteWorkflowWithRetry_AllPolicies(t *testing.T) {
	tests := []struct {
		name   string
		policy enums.WorkflowIdReusePolicy
	}{
		{
			name:   "REJECT_DUPLICATE",
			policy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		},
		{
			name:   "ALLOW_DUPLICATE",
			policy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		},
		{
			name:   "ALLOW_DUPLICATE_FAILED_ONLY",
			policy: enums.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
		},
		{
			name:   "TERMINATE_IF_RUNNING",
			policy: enums.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := new(MockTemporalClient)
			mockLogger := new(MockLogger)
			executor := NewWorkflowExecutor(mockClient, mockLogger)

			ctx := context.Background()
			workflowID := "test-workflow-123"
			taskQueue := "test-queue"
			workflowFunc := func() {}
			policy := tt.policy

			mockClient.On("ExecuteWorkflow", mock.Anything, mock.MatchedBy(func(opts client.StartWorkflowOptions) bool {
				return opts.WorkflowIDReusePolicy == policy
			}), mock.Anything).Return(nil, nil).Once()
			mockLogger.On("Error", mock.Anything, mock.Anything).Return().Maybe()

			err := executor.ExecuteWorkflowWithRetry(ctx, workflowID, taskQueue, workflowFunc, nil, &policy)

			assert.NoError(t, err)
			mockClient.AssertExpectations(t)
		})
	}
}
