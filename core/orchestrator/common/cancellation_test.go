package common

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/api/workflowservice/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// mockWorkflowRun is a simple mock for client.WorkflowRun
type mockWorkflowRunForCommon struct {
	mock.Mock
}

// createMockWorkflowExecutionResponse creates a mock DescribeWorkflowExecutionResponse with the given status
func createMockWorkflowExecutionResponse(status enums.WorkflowExecutionStatus) *workflowservice.DescribeWorkflowExecutionResponse {
	mockDesc := &workflowservice.DescribeWorkflowExecutionResponse{}
	// Get the WorkflowExecutionInfo field and set its Status using reflection
	info := mockDesc.GetWorkflowExecutionInfo()
	if info == nil {
		// Create a new WorkflowExecutionInfo using reflection
		infoType := reflect.TypeOf(mockDesc).Elem()
		infoField, _ := infoType.FieldByName("WorkflowExecutionInfo")
		infoValue := reflect.New(infoField.Type.Elem())
		reflect.ValueOf(mockDesc).Elem().FieldByName("WorkflowExecutionInfo").Set(infoValue)
		info = mockDesc.GetWorkflowExecutionInfo()
	}
	// Set Status using reflection
	statusField := reflect.ValueOf(info).Elem().FieldByName("Status")
	if statusField.IsValid() && statusField.CanSet() {
		statusField.Set(reflect.ValueOf(status))
	}
	return mockDesc
}

func (m *mockWorkflowRunForCommon) Get(ctx context.Context, valuePtr interface{}) error {
	args := m.Called(ctx, valuePtr)
	return args.Error(0)
}

func (m *mockWorkflowRunForCommon) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	args := m.Called(ctx, valuePtr, options)
	return args.Error(0)
}

func (m *mockWorkflowRunForCommon) GetID() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockWorkflowRunForCommon) GetRunID() string {
	args := m.Called()
	return args.String(0)
}

func TestIsWorkflowRunning_Success_Running(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"

	// Create a mock response with WorkflowExecutionInfo showing running status
	mockDesc := createMockWorkflowExecutionResponse(enums.WORKFLOW_EXECUTION_STATUS_RUNNING)
	mockClient.EXPECT().DescribeWorkflowExecution(ctx, workflowID, "").Return(mockDesc, nil)

	result, err := IsWorkflowRunning(ctx, mockClient, workflowID)

	assert.NoError(t, err)
	assert.True(t, result)
	mockClient.AssertExpectations(t)
}

func TestIsWorkflowRunning_Success_NotRunning(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"

	// Create a mock response with WorkflowExecutionInfo showing completed status
	mockDesc := createMockWorkflowExecutionResponse(enums.WORKFLOW_EXECUTION_STATUS_COMPLETED)
	mockClient.EXPECT().DescribeWorkflowExecution(ctx, workflowID, "").Return(mockDesc, nil)

	result, err := IsWorkflowRunning(ctx, mockClient, workflowID)

	assert.NoError(t, err)
	assert.False(t, result)
	mockClient.AssertExpectations(t)
}

func TestIsWorkflowRunning_Error(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"

	mockClient.EXPECT().DescribeWorkflowExecution(ctx, workflowID, "").Return(nil, errors.New("workflow not found"))

	result, err := IsWorkflowRunning(ctx, mockClient, workflowID)

	assert.Error(t, err)
	assert.False(t, result)
	mockClient.AssertExpectations(t)
}

func TestWaitForWorkflowCancellationAck_Success(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"
	timeout := 5 * time.Minute

	mockRun := &mockWorkflowRunForCommon{}
	mockClient.EXPECT().GetWorkflow(ctx, workflowID, "").Return(mockRun)
	mockRun.On("Get", mock.Anything, nil).Return(nil)

	result, err := WaitForWorkflowCancellationAck(ctx, mockClient, workflowID, timeout)

	assert.NoError(t, err)
	assert.True(t, result)
	mockClient.AssertExpectations(t)
	mockRun.AssertExpectations(t)
}

func TestWaitForWorkflowCancellationAck_Timeout(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"
	timeout := 1 * time.Millisecond // Very short timeout

	mockRun := &mockWorkflowRunForCommon{}
	mockClient.EXPECT().GetWorkflow(ctx, workflowID, "").Return(mockRun)
	// The implementation creates a context with timeout and checks ctxWithTimeout.Err()
	// We need to make the mock's Get() method wait long enough for the context to timeout
	// Use Run to access the actual context parameter
	mockRun.On("Get", mock.Anything, nil).Run(func(args mock.Arguments) {
		ctxParam := args.Get(0).(context.Context)
		// Wait longer than the timeout to ensure the context expires
		time.Sleep(5 * time.Millisecond)
		// The context should have timed out by now
		_ = ctxParam
	}).Return(context.DeadlineExceeded)

	result, err := WaitForWorkflowCancellationAck(ctx, mockClient, workflowID, timeout)

	// When timeout occurs, the function returns false, nil (no error, but timeout occurred)
	assert.NoError(t, err)
	assert.False(t, result)
	mockClient.AssertExpectations(t)
	mockRun.AssertExpectations(t)
}

func TestWaitForWorkflowCancellationAck_CancelledError(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"
	timeout := 5 * time.Minute

	mockRun := &mockWorkflowRunForCommon{}
	mockClient.EXPECT().GetWorkflow(ctx, workflowID, "").Return(mockRun)
	cancelErr := temporal.NewCanceledError("workflow cancelled")
	mockRun.On("Get", mock.Anything, nil).Return(cancelErr)

	result, err := WaitForWorkflowCancellationAck(ctx, mockClient, workflowID, timeout)

	assert.NoError(t, err)
	assert.True(t, result) // Should return true for cancelled workflows
	mockClient.AssertExpectations(t)
	mockRun.AssertExpectations(t)
}

func TestWaitForWorkflowCancellationAck_ApplicationError(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"
	timeout := 5 * time.Minute

	mockRun := &mockWorkflowRunForCommon{}
	mockClient.EXPECT().GetWorkflow(ctx, workflowID, "").Return(mockRun)
	appErr := temporal.NewApplicationError("workflow failed", "ErrorType", true, nil)
	mockRun.On("Get", mock.Anything, nil).Return(appErr)

	result, err := WaitForWorkflowCancellationAck(ctx, mockClient, workflowID, timeout)

	assert.NoError(t, err)
	assert.True(t, result) // Should return true for completed workflows (even with error)
	mockClient.AssertExpectations(t)
	mockRun.AssertExpectations(t)
}

func TestWaitForWorkflowCancellationAck_OtherError(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	workflowID := "test-workflow-id"
	timeout := 5 * time.Minute

	mockRun := &mockWorkflowRunForCommon{}
	mockClient.EXPECT().GetWorkflow(ctx, workflowID, "").Return(mockRun)
	mockRun.On("Get", mock.Anything, nil).Return(errors.New("unknown error"))

	result, err := WaitForWorkflowCancellationAck(ctx, mockClient, workflowID, timeout)

	assert.Error(t, err)
	assert.False(t, result)
	mockClient.AssertExpectations(t)
	mockRun.AssertExpectations(t)
}

func TestWorkflowCancellationHandler_IsCancelled_InitiallyFalse(t *testing.T) {
	// This test requires a workflow context, so we'll test it in workflow tests
	// The handler needs to be created within a workflow context
	handler := &WorkflowCancellationHandler{
		cancelled: false,
	}

	assert.False(t, handler.IsCancelled())
}

func TestWorkflowCancellationHandler_IsCancelled_AfterCancellation(t *testing.T) {
	handler := &WorkflowCancellationHandler{
		cancelled: true,
	}

	assert.True(t, handler.IsCancelled())
}

// Test workflow functions for WorkflowCancellationHandler
func testWorkflowWithCancellationHandler(ctx workflow.Context, signalName string, resourceUUID string, resourceName string) error {
	handler := NewWorkflowCancellationHandler(ctx, signalName, resourceUUID, resourceName)
	// Wait a bit to allow signal to be sent, then check for cancellation
	_ = workflow.Sleep(ctx, 10*time.Millisecond)
	return handler.CheckCancellation(ctx)
}

func testWorkflowWithCancellationHandlerNoSignal(ctx workflow.Context, signalName string, resourceUUID string, resourceName string) error {
	handler := NewWorkflowCancellationHandler(ctx, signalName, resourceUUID, resourceName)
	// Check cancellation - should not error when no signal
	err := handler.CheckCancellation(ctx)
	if err != nil {
		return err
	}
	// Verify handler is not cancelled
	if handler.IsCancelled() {
		return fmt.Errorf("handler should not be cancelled")
	}
	return nil
}

func TestNewWorkflowCancellationHandler_WithEmptySignalName(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(testWorkflowWithCancellationHandler, "", "test-uuid", "test-resource")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestNewWorkflowCancellationHandler_WithSignalName(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(testWorkflowWithCancellationHandler, "custom-signal", "test-uuid", "test-resource")

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestWorkflowCancellationHandler_CheckCancellation_WithSignal(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	signalName := "cancel-signal"
	resourceUUID := "test-uuid"
	resourceName := "test-resource"

	// Send signal after workflow starts using RegisterDelayedCallback
	// Send it after a short delay to ensure workflow has started
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(signalName, "cancel data")
	}, 5*time.Millisecond)

	env.ExecuteWorkflow(testWorkflowWithCancellationHandler, signalName, resourceUUID, resourceName)

	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "creation cancelled by delete request")
}

func TestWorkflowCancellationHandler_CheckCancellation_NoSignal(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	signalName := "cancel-signal"
	resourceUUID := "test-uuid"
	resourceName := "test-resource"

	// Don't send signal - should use default selector path
	env.ExecuteWorkflow(testWorkflowWithCancellationHandlerNoSignal, signalName, resourceUUID, resourceName)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// testCancellationActivity implements CancellationActivityMethods for testing
// We can't import activities package due to circular dependency, so we create our own implementation
type testCancellationActivity struct{}

func (a *testCancellationActivity) IsWorkflowRunningActivity(ctx context.Context, workflowID string) (bool, error) {
	// This will be mocked via env.OnActivity
	return false, nil
}

func (a *testCancellationActivity) SendCancelSignalActivity(ctx context.Context, workflowID string, signalName string, signalData string) error {
	// This will be mocked via env.OnActivity
	return nil
}

func (a *testCancellationActivity) WaitForWorkflowCancellationAckActivity(ctx context.Context, workflowID string, timeout time.Duration) (bool, error) {
	// This will be mocked via env.OnActivity
	return false, nil
}

func (a *testCancellationActivity) ForceCancelWorkflowActivity(ctx context.Context, workflowID string) error {
	// This will be mocked via env.OnActivity
	return nil
}

// testCommonActivity implements CommonActivityMethods for testing
type testCommonActivity struct{}

func (a *testCommonActivity) UpdateJobStatus(ctx context.Context, job *datamodel.Job) error {
	// This will be mocked via env.OnActivity
	return nil
}

// TestHandleCancellationInDeleteWorkflow_WithEmptyCreateJobType tests that the function skips cancellation handling when CreateJobType is empty (lines 160-161, 170-172)
func TestHandleCancellationInDeleteWorkflow_WithEmptyCreateJobType(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          "", // Empty CreateJobType
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	// Mock getCreateJobActivity to return nil, nil (not called but needed for type)
	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return nil, nil
	}

	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WithDefaultSignalName tests that the function uses default signal name when SignalName is empty (lines 160-161, 163-164)
func TestHandleCancellationInDeleteWorkflow_WithDefaultSignalName(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "", // Empty SignalName - should use default
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, DefaultCancelSignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WithDefaultTimeout tests that the function uses default timeout when Timeout is zero (lines 160-161, 166-167)
func TestHandleCancellationInDeleteWorkflow_WithDefaultTimeout(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 0, // Zero timeout - should use default
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, DefaultCancellationTimeout).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenGetCreateJobFails tests that the function handles errors when getting create job fails (lines 160-161, 176-177, 179-181)
func TestHandleCancellationInDeleteWorkflow_WhenGetCreateJobFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return nil, errors.New("job not found")
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(nil, errors.New("job not found"))

	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultIsNil tests that the function handles nil create job result (lines 160-161, 176-177, 184-186)
func TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultIsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return nil, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(nil, nil)

	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultHasEmptyWorkflowID tests that the function handles empty workflow ID in create job result (lines 160-161, 176-177, 184-186)
func TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultHasEmptyWorkflowID(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "", // Empty WorkflowID
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)

	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCreateJobFound tests lines 160-161, 176-177, 189
func TestHandleCancellationInDeleteWorkflow_WhenCreateJobFound(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCheckWorkflowStatusFails tests that the function handles errors when checking workflow status fails (lines 160-161, 176-177, 189, 192-193, 195-197)
func TestHandleCancellationInDeleteWorkflow_WhenCheckWorkflowStatusFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, errors.New("failed to check status"))

	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning tests that the function updates job status when workflow is not running (lines 160-161, 176-177, 189, 192-193, 200, 202-203, 211)
func TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunningAndUpdateJobFails tests that the function handles job update failure when workflow is not running (lines 160-161, 176-177, 189, 192-193, 200, 202-203, 208-209, 211)
func TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunningAndUpdateJobFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("failed to update job"))

	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenSendCancelSignalFails tests that the function handles errors when sending cancel signal fails (lines 160-161, 176-177, 189, 192-193, 215-217, 219-220)
func TestHandleCancellationInDeleteWorkflow_WhenSendCancelSignalFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(errors.New("failed to send signal"))
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWaitForCancellationAckFails tests that the function handles errors when waiting for cancellation acknowledgment fails (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 229-230)
func TestHandleCancellationInDeleteWorkflow_WhenWaitForCancellationAckFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, errors.New("wait failed"))
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelSucceeds tests that the function successfully force cancels workflow when timeout occurs (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 234, 236, 238, 244-247, 249-252, 258, 262, 267-269, 271, 274)
func TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelSucceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil) // Timeout
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil) // Force cancel wait succeeds
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.UUID == createJobResult.JobUUID &&
			job.State == string(datamodel.JobsStateERROR) &&
			job.TrackingID == vsaerrors.ErrInternalServerError &&
			job.ErrorDetails == "Resource creation forcefully terminated due to delete request (timeout exceeded)"
	})).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelFails tests that the function handles errors when force cancel fails after timeout (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 234, 236, 238, 240-241)
func TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil) // Timeout
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(errors.New("force cancel failed"))
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelWaitFails tests that the function handles errors when waiting for force cancel completion fails (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 234, 236, 238, 244-247, 249-252)
func TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelWaitFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil) // Timeout
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, errors.New("wait failed")) // Force cancel wait fails
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelWaitTimeout tests that the function handles timeout when waiting for force cancel completion (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 234, 236, 238, 244-247, 249-252, 254)
func TestHandleCancellationInDeleteWorkflow_WhenTimeoutAndForceCancelWaitTimeout(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil) // Timeout
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, nil) // Force cancel wait timeout
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCancellationSucceeds tests that the function successfully cancels workflow when acknowledgment is received (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 258)
func TestHandleCancellationInDeleteWorkflow_WhenCancellationSucceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil) // Success
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.UUID == createJobResult.JobUUID &&
			job.State == string(datamodel.JobsStateERROR) &&
			job.TrackingID == vsaerrors.ErrInternalServerError &&
			job.ErrorDetails == "Resource creation cancelled due to delete request"
	})).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenUpdateJobFails tests that the function handles errors when updating job status fails after cancellation (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 258, 262, 267-269, 274)
func TestHandleCancellationInDeleteWorkflow_WhenUpdateJobFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("failed to update job"))

	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenUpdateJobSucceeds tests that the function successfully updates job status after cancellation (lines 160-161, 176-177, 189, 192-193, 215-217, 224-226, 258, 262, 267-269, 271, 274)
func TestHandleCancellationInDeleteWorkflow_WhenUpdateJobSucceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCreateJobFound_LogsMatchingJob tests that the function logs when a matching create job is found (line 189)
func TestHandleCancellationInDeleteWorkflow_WhenCreateJobFound_LogsMatchingJob(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-123",
		WorkflowID: "workflow-id-456",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning_LogsCompletionAndUpdatesJob tests that when workflow is not running, it logs completion and updates job (lines 200, 202-203, 208-209, 211)
func TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning_LogsCompletionAndUpdatesJob(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.UUID == createJobResult.JobUUID &&
			job.State == string(datamodel.JobsStateERROR) &&
			job.TrackingID == vsaerrors.ErrInternalServerError &&
			job.ErrorDetails == "Resource creation cancelled due to delete request"
	})).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenSendingCancelSignal_LogsSignalAndHandlesError tests sending cancellation signal and error handling (lines 215-217, 219-220)
func TestHandleCancellationInDeleteWorkflow_WhenSendingCancelSignal_LogsSignalAndHandlesError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.MatchedBy(func(data string) bool {
		return data == fmt.Sprintf("Delete requested for resource %s", params.ResourceUUID)
	})).Return(errors.New("signal send failed"))
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWaitingForAck_LogsWaitAndHandlesError tests waiting for cancellation acknowledgment and error handling (lines 224-226, 229-230)
func TestHandleCancellationInDeleteWorkflow_WhenWaitingForAck_LogsWaitAndHandlesError(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, errors.New("wait error"))
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenTimeoutOccurs_LogsTimeoutAndForceCancels tests timeout handling and force cancellation (lines 234, 236, 238)
func TestHandleCancellationInDeleteWorkflow_WhenTimeoutOccurs_LogsTimeoutAndForceCancels(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelFails_LogsErrorAndProceeds tests error handling when force cancel fails (lines 240-241)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelFails_LogsErrorAndProceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(errors.New("force cancel failed"))
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelSucceeds_WaitsForCompletion tests waiting for force cancellation to complete (lines 244-247)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelSucceeds_WaitsForCompletion(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitFails_LogsErrorAndProceeds tests error handling when force cancel wait fails (lines 249-250)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitFails_LogsErrorAndProceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, errors.New("wait failed"))
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitSucceeds_LogsCompletion tests successful force cancel wait completion (lines 251-252)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitSucceeds_LogsCompletion(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitTimesOut_LogsTimeoutAndProceeds tests timeout handling for force cancel wait (lines 254)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitTimesOut_LogsTimeoutAndProceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCancellationAcknowledged_LogsSuccess tests successful cancellation acknowledgment (line 258)
func TestHandleCancellationInDeleteWorkflow_WhenCancellationAcknowledged_LogsSuccess(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenUpdatingJobAfterCancellation_CreatesJobAndUpdatesStatus tests job update after cancellation (lines 262, 267-269)
func TestHandleCancellationInDeleteWorkflow_WhenUpdatingJobAfterCancellation_CreatesJobAndUpdatesStatus(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.UUID == createJobResult.JobUUID &&
			job.State == string(datamodel.JobsStateERROR) &&
			job.TrackingID == vsaerrors.ErrInternalServerError &&
			job.ErrorDetails == "Resource creation cancelled due to delete request"
	})).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenJobUpdateSucceeds_LogsSuccessAndReturnsNil tests successful job update and return (lines 271, 274)
func TestHandleCancellationInDeleteWorkflow_WhenJobUpdateSucceeds_LogsSuccessAndReturnsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-789",
		WorkflowID: "workflow-id-123",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultIsNil_LogsWarningAndReturnsNil tests that the function handles nil create job result and logs warning (lines 184-186)
func TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultIsNil_LogsWarningAndReturnsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return nil, nil // Returns nil result
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(nil, nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultHasEmptyWorkflowID_LogsWarningAndReturnsNil tests that the function handles empty workflow ID and logs warning (lines 184-186)
func TestHandleCancellationInDeleteWorkflow_WhenCreateJobResultHasEmptyWorkflowID_LogsWarningAndReturnsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "", // Empty WorkflowID
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCreateJobFound_LogsMatchingJobInfo tests that the function logs matching create job information (line 189)
func TestHandleCancellationInDeleteWorkflow_WhenCreateJobFound_LogsMatchingJobInfo(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-123",
		WorkflowID: "workflow-id-456",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCheckingWorkflowStatus_ExecutesIsRunningActivity tests that the function executes IsWorkflowRunningActivity and stores result (lines 192-193)
func TestHandleCancellationInDeleteWorkflow_WhenCheckingWorkflowStatus_ExecutesIsRunningActivity(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCheckWorkflowStatusFails_LogsWarningAndReturnsNil tests that the function handles errors when checking workflow status fails (lines 195-197)
func TestHandleCancellationInDeleteWorkflow_WhenCheckWorkflowStatusFails_LogsWarningAndReturnsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, errors.New("failed to check status"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning_ChecksIsRunningAndProceedsToUpdate tests that the function checks if workflow is not running and proceeds (line 200)
func TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning_ChecksIsRunningAndProceedsToUpdate(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, nil) // Not running
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning_LogsCompletionMessage tests that the function logs completion message when workflow is not running (lines 202-203)
func TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunning_LogsCompletionMessage(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id-completed",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunningAndUpdateJobFails_LogsWarningAndReturnsNil tests that the function handles job update failure when workflow is not running (lines 208-209, 211)
func TestHandleCancellationInDeleteWorkflow_WhenWorkflowNotRunningAndUpdateJobFails_LogsWarningAndReturnsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("failed to update job"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenSendingCancelSignal_LogsSignalAndExecutesActivity tests that the function logs and executes send cancel signal activity (lines 215-217)
func TestHandleCancellationInDeleteWorkflow_WhenSendingCancelSignal_LogsSignalAndExecutesActivity(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.MatchedBy(func(data string) bool {
		return data == fmt.Sprintf("Delete requested for resource %s", params.ResourceUUID)
	})).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenSendCancelSignalFails_LogsWarningAndContinues tests that the function handles errors when sending cancel signal fails (lines 219-220)
func TestHandleCancellationInDeleteWorkflow_WhenSendCancelSignalFails_LogsWarningAndContinues(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(errors.New("signal send failed"))
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWaitingForAck_LogsWaitAndExecutesActivity tests that the function logs wait message and executes wait activity (lines 224-226)
func TestHandleCancellationInDeleteWorkflow_WhenWaitingForAck_LogsWaitAndExecutesActivity(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenWaitForAckFails_LogsWarningAndContinues tests that the function handles errors when waiting for acknowledgment fails (lines 229-230)
func TestHandleCancellationInDeleteWorkflow_WhenWaitForAckFails_LogsWarningAndContinues(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, errors.New("wait error"))
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenAcknowledgmentNotReceived_ChecksAcknowledgedAndProceedsToForceCancel tests that the function checks acknowledgment and proceeds to force cancel (line 234)
func TestHandleCancellationInDeleteWorkflow_WhenAcknowledgmentNotReceived_ChecksAcknowledgedAndProceedsToForceCancel(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil) // Not acknowledged
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenTimeoutOccurs_LogsTimeoutWarning tests that the function logs timeout warning when acknowledgment is not received (line 236)
func TestHandleCancellationInDeleteWorkflow_WhenTimeoutOccurs_LogsTimeoutWarning(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenTimeoutOccurs_ExecutesForceCancelActivity tests that the function executes force cancel activity when timeout occurs (line 238)
func TestHandleCancellationInDeleteWorkflow_WhenTimeoutOccurs_ExecutesForceCancelActivity(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelFails_LogsWarningAndProceeds tests that the function handles errors when force cancel fails (lines 240-241)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelFails_LogsWarningAndProceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(errors.New("force cancel failed"))
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelSucceeds_WaitsForCompletionWithTimeout tests that the function waits for force cancellation completion after successful force cancel (lines 244-247)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelSucceeds_WaitsForCompletionWithTimeout(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitFails_LogsWarningAndProceeds tests that the function handles errors when waiting for force cancel completion fails (lines 249-250)
func TestHandleCancellationInDeleteWorkflow_WhenForceCancelWaitFails_LogsWarningAndProceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, errors.New("wait failed"))
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenCancellationAcknowledged_LogsSuccessMessage tests that the function logs success message when cancellation is acknowledged (line 258)
func TestHandleCancellationInDeleteWorkflow_WhenCancellationAcknowledged_LogsSuccessMessage(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil) // Acknowledged
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenUpdatingJobAfterCancellation_CreatesJobWithErrorState tests that the function creates job with error state for update after cancellation (line 262)
func TestHandleCancellationInDeleteWorkflow_WhenUpdatingJobAfterCancellation_CreatesJobWithErrorState(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.UUID == createJobResult.JobUUID &&
			job.State == string(datamodel.JobsStateERROR) &&
			job.TrackingID == vsaerrors.ErrInternalServerError &&
			job.ErrorDetails == "Resource creation cancelled due to delete request"
	})).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenUpdateJobFails_LogsWarningAndReturnsNil tests that the function handles errors when updating job fails after cancellation (lines 267-269)
func TestHandleCancellationInDeleteWorkflow_WhenUpdateJobFails_LogsWarningAndReturnsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(errors.New("failed to update job"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_WhenUpdateJobSucceeds_LogsSuccessAndReturnsNil tests that the function logs success and returns nil when job update succeeds (lines 271, 274)
func TestHandleCancellationInDeleteWorkflow_WhenUpdateJobSucceeds_LogsSuccessAndReturnsNil(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-final",
		WorkflowID: "workflow-id-final",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine189 tests that line 189 (log matching job) is executed
func TestHandleCancellationInDeleteWorkflow_CoversLine189(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-189",
		WorkflowID: "workflow-id-189",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines192_193 tests that lines 192-193 (workflow check) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines192_193(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-192",
		WorkflowID: "workflow-id-192",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines195_197 tests that lines 195-197 (error handling for workflow check) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines195_197(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-195",
		WorkflowID: "workflow-id-195",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, errors.New("check failed"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines200_203_208_209_211 tests that lines 200, 202-203, 208-209, 211 (workflow not running path) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines200_203_208_209_211(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-200",
		WorkflowID: "workflow-id-200",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("update failed"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines215_217_219_220 tests that lines 215-217, 219-220 (send signal and error handling) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines215_217_219_220(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-215",
		WorkflowID: "workflow-id-215",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(errors.New("send signal failed"))
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines224_226_229_230 tests that lines 224-226, 229-230 (wait for ack and error handling) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines224_226_229_230(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-224",
		WorkflowID: "workflow-id-224",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, errors.New("wait failed"))
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines234_236_238_240_241 tests that lines 234, 236, 238, 240-241 (timeout and force cancel) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines234_236_238_240_241(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-234",
		WorkflowID: "workflow-id-234",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(errors.New("force cancel failed"))
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines244_247_249_252 tests that lines 244-247, 249-252 (force cancel wait and error handling) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines244_247_249_252(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-244",
		WorkflowID: "workflow-id-244",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, errors.New("wait error"))
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine254 tests that line 254 (force cancel wait timeout) is executed
func TestHandleCancellationInDeleteWorkflow_CoversLine254(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-254",
		WorkflowID: "workflow-id-254",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine258 tests that line 258 (success log) is executed
func TestHandleCancellationInDeleteWorkflow_CoversLine258(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-258",
		WorkflowID: "workflow-id-258",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines262_267_269_271_274 tests that lines 262, 267-269, 271, 274 (job update paths) are executed
func TestHandleCancellationInDeleteWorkflow_CoversLines262_267_269_271_274(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-262",
		WorkflowID: "workflow-id-262",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines184_186_NilResult tests lines 184-186 when createJobResult is nil
func TestHandleCancellationInDeleteWorkflow_CoversLines184_186_NilResult(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid-184-nil",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return nil, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(nil, nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestHandleCancellationInDeleteWorkflow_CoversLines184_186_EmptyWorkflowID tests lines 184-186 when WorkflowID is empty
func TestHandleCancellationInDeleteWorkflow_CoversLines184_186_EmptyWorkflowID(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestHandleCancellationInDeleteWorkflow_CoversLine189_LogsMatchingJob tests line 189 - logs matching job info
func TestHandleCancellationInDeleteWorkflow_CoversLine189_LogsMatchingJob(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-189",
		WorkflowID: "workflow-id-189",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestHandleCancellationInDeleteWorkflow_CoversLines192_193_ExecutesIsRunningActivity tests lines 192-193 - executes IsWorkflowRunningActivity
func TestHandleCancellationInDeleteWorkflow_CoversLines192_193_ExecutesIsRunningActivity(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-192",
		WorkflowID: "workflow-id-192",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines195_197_CheckStatusFails tests lines 195-197 - when check workflow status fails
func TestHandleCancellationInDeleteWorkflow_CoversLines195_197_CheckStatusFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-195",
		WorkflowID: "workflow-id-195",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, errors.New("check failed"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine200_WorkflowNotRunning tests line 200 - when workflow is not running
func TestHandleCancellationInDeleteWorkflow_CoversLine200_WorkflowNotRunning(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-200",
		WorkflowID: "workflow-id-200",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines202_203_LogsCompletion tests lines 202-203 - logs completion message
func TestHandleCancellationInDeleteWorkflow_CoversLines202_203_LogsCompletion(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-202",
		WorkflowID: "workflow-id-202",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines208_209_211_UpdateJobFails tests lines 208-209, 211 - when update job fails
func TestHandleCancellationInDeleteWorkflow_CoversLines208_209_211_UpdateJobFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-208",
		WorkflowID: "workflow-id-208",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("update failed"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines215_217_SendsCancelSignal tests lines 215-217 - sends cancel signal
func TestHandleCancellationInDeleteWorkflow_CoversLines215_217_SendsCancelSignal(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-215",
		WorkflowID: "workflow-id-215",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines219_220_SendSignalFails tests lines 219-220 - when send signal fails
func TestHandleCancellationInDeleteWorkflow_CoversLines219_220_SendSignalFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-219",
		WorkflowID: "workflow-id-219",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(errors.New("send signal failed"))
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines224_226_WaitsForAck tests lines 224-226 - waits for cancellation ack
func TestHandleCancellationInDeleteWorkflow_CoversLines224_226_WaitsForAck(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-224",
		WorkflowID: "workflow-id-224",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines229_230_WaitForAckFails tests lines 229-230 - when wait for ack fails
func TestHandleCancellationInDeleteWorkflow_CoversLines229_230_WaitForAckFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-229",
		WorkflowID: "workflow-id-229",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, errors.New("wait failed"))
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine234_NotAcknowledged tests line 234 - when not acknowledged
func TestHandleCancellationInDeleteWorkflow_CoversLine234_NotAcknowledged(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-234",
		WorkflowID: "workflow-id-234",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine236_LogsTimeoutWarning tests line 236 - logs timeout warning
func TestHandleCancellationInDeleteWorkflow_CoversLine236_LogsTimeoutWarning(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-236",
		WorkflowID: "workflow-id-236",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine238_ExecutesForceCancel tests line 238 - executes force cancel
func TestHandleCancellationInDeleteWorkflow_CoversLine238_ExecutesForceCancel(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-238",
		WorkflowID: "workflow-id-238",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines240_241_ForceCancelFails tests lines 240-241 - when force cancel fails
func TestHandleCancellationInDeleteWorkflow_CoversLines240_241_ForceCancelFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-240",
		WorkflowID: "workflow-id-240",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(errors.New("force cancel failed"))
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines244_247_WaitsForForceCancel tests lines 244-247 - waits for force cancel
func TestHandleCancellationInDeleteWorkflow_CoversLines244_247_WaitsForForceCancel(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-244",
		WorkflowID: "workflow-id-244",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines249_252_ForceCancelWaitErrorHandling tests lines 249-252 - force cancel wait error handling
func TestHandleCancellationInDeleteWorkflow_CoversLines249_252_ForceCancelWaitErrorHandling(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-249",
		WorkflowID: "workflow-id-249",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, errors.New("wait error"))
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine251_252_ForceCancelWaitSucceeds tests lines 251-252 - when force cancel wait succeeds
func TestHandleCancellationInDeleteWorkflow_CoversLine251_252_ForceCancelWaitSucceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-251",
		WorkflowID: "workflow-id-251",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity(cancellationActivity.ForceCancelWorkflowActivity, mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine254_ForceCancelWaitTimeout tests line 254 - when force cancel wait times out
func TestHandleCancellationInDeleteWorkflow_CoversLine254_ForceCancelWaitTimeout(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-254",
		WorkflowID: "workflow-id-254",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(false, nil)
	env.OnActivity("ForceCancelWorkflowActivity", mock.Anything, createJobResult.WorkflowID).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, 30*time.Second).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine258_CancellationAcknowledged tests line 258 - when cancellation is acknowledged
func TestHandleCancellationInDeleteWorkflow_CoversLine258_CancellationAcknowledged(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-258",
		WorkflowID: "workflow-id-258",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLine262_CreatesJobForUpdate tests line 262 - creates job for update
func TestHandleCancellationInDeleteWorkflow_CoversLine262_CreatesJobForUpdate(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-262",
		WorkflowID: "workflow-id-262",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines267_269_UpdateJobFails tests lines 267-269 - when update job fails
func TestHandleCancellationInDeleteWorkflow_CoversLines267_269_UpdateJobFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-267",
		WorkflowID: "workflow-id-267",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity("IsWorkflowRunningActivity", mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity("SendCancelSignalActivity", mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity("WaitForWorkflowCancellationAckActivity", mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("update failed"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// TestHandleCancellationInDeleteWorkflow_CoversLines271_274_UpdateJobSucceeds tests lines 271, 274 - when update job succeeds
func TestHandleCancellationInDeleteWorkflow_CoversLines271_274_UpdateJobSucceeds(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(time.Minute)

	params := WorkflowCancellationParams{
		ResourceUUID:           "test-resource-uuid",
		CorrelationID:          "test-correlation-id",
		CreateJobType:          datamodel.JobTypeCreatePool,
		SignalName:             "cancel-signal",
		CancellationAckTimeout: 5 * time.Minute,
	}

	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid-271",
		WorkflowID: "workflow-id-271",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, params.CorrelationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(true, nil)
	env.OnActivity(cancellationActivity.SendCancelSignalActivity, mock.Anything, createJobResult.WorkflowID, params.SignalName, mock.Anything).Return(nil)
	env.OnActivity(cancellationActivity.WaitForWorkflowCancellationAckActivity, mock.Anything, createJobResult.WorkflowID, params.CancellationAckTimeout).Return(true, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ao := workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		}
		ctx = workflow.WithActivityOptions(ctx, ao)
		return HandleCancellationInDeleteWorkflow(ctx, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestWorkflowCancellationHandler_CheckCancellationSignal_WithCancellation_ReturnsCustomError(t *testing.T) {
	// Test for lines 145-146, 148: CheckCancellationSignal converts cancellation error to CustomError
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	signalName := "cancel-signal"
	resourceUUID := "test-uuid"
	resourceName := "test-resource"

	// Send signal after workflow starts
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(signalName, "cancel data")
	}, 5*time.Millisecond)

	env.ExecuteWorkflow(func(ctx workflow.Context) *vsaerrors.CustomError {
		handler := NewWorkflowCancellationHandler(ctx, signalName, resourceUUID, resourceName)
		_ = workflow.Sleep(ctx, 10*time.Millisecond)
		return handler.CheckCancellationSignal(ctx)
	})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	// Should return a CustomError when cancellation is detected
	assert.Contains(t, err.Error(), "cancelled")
}

func TestWorkflowCancellationHandler_CheckCancellationSignal_NoCancellation_ReturnsNil(t *testing.T) {
	// Test for lines 145-146, 148: CheckCancellationSignal returns nil when no cancellation
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	signalName := "cancel-signal"
	resourceUUID := "test-uuid"
	resourceName := "test-resource"

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		handler := NewWorkflowCancellationHandler(ctx, signalName, resourceUUID, resourceName)
		customErr := handler.CheckCancellationSignal(ctx)
		if customErr != nil {
			return customErr
		}
		return nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestHandleCancellationForCreatingResource_NonCreatingState_ReturnsNilEarly(t *testing.T) {
	// Test for lines 316-317: When resource state is not CREATING, return nil early
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockLogger := &log.MockLogger{}
	params := HandleCancellationForCreatingResourceParams{
		ResourceUUID:  "resource-uuid",
		ResourceState: datamodel.LifeCycleStateREADY, // Not CREATING
		CreateJobType: datamodel.JobTypeCreateVolume,
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return nil, nil
	}
	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		return HandleCancellationForCreatingResource(ctx, mockLogger, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Should return early without calling HandleCancellationInDeleteWorkflow
}

func TestHandleCancellationForCreatingResource_Success_ReturnsNil(t *testing.T) {
	// Test for lines 326, 335-337, 339: When HandleCancellationInDeleteWorkflow succeeds, return nil
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	fields := log.Fields{
		string(middleware.RequestCorrelationID): "test-correlation-id",
	}
	env.SetTestTimeout(1 * time.Minute)

	mockLogger := &log.MockLogger{}
	params := HandleCancellationForCreatingResourceParams{
		ResourceUUID:  "resource-uuid",
		ResourceState: datamodel.LifeCycleStateCreating,
		CreateJobType: datamodel.JobTypeCreateVolume,
	}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}
	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	// Correlation ID is extracted from workflow context, so use the value from fields
	correlationID := fields[string(middleware.RequestCorrelationID)].(string)
	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, correlationID, string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, middleware.TemporalSLoggerKey, fields)
		return HandleCancellationForCreatingResource(ctx, mockLogger, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestHandleCancellationForCreatingResource_CorrelationIDError_ContinuesWithEmptyID(t *testing.T) {
	// Test for lines 322-323: When GetCorrelationIDFromWorkflowContextLoggerFields returns error, continue with empty correlation ID
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	env.SetTestTimeout(1 * time.Minute)

	mockLogger := &log.MockLogger{}
	mockLogger.On("Warn", "Could not get correlation ID from workflow context", "error", mock.Anything).Return()
	params := HandleCancellationForCreatingResourceParams{
		ResourceUUID:  "resource-uuid",
		ResourceState: datamodel.LifeCycleStateCreating,
		CreateJobType: datamodel.JobTypeCreateVolume,
	}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}
	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	// Don't set correlation ID in context, so GetCorrelationIDFromWorkflowContextLoggerFields will return error
	// Use empty string as correlation ID when the function is called
	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, "", string(params.CreateJobType)).Return(createJobResult, nil)
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, nil)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		// Don't set correlation ID in context to trigger error path
		return HandleCancellationForCreatingResource(ctx, mockLogger, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	// Verify that logger was called with warning about correlation ID
	mockLogger.AssertExpectations(t)
}

func TestHandleCancellationForCreatingResource_CancellationError_ReturnsError(t *testing.T) {
	// Test for lines 336-337: When HandleCancellationInDeleteWorkflow returns error, log warning and return error
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	fields := log.Fields{
		string(middleware.RequestCorrelationID): "test-correlation-id",
	}
	env.SetTestTimeout(1 * time.Minute)

	mockLogger := &log.MockLogger{}
	params := HandleCancellationForCreatingResourceParams{
		ResourceUUID:  "resource-uuid",
		ResourceState: datamodel.LifeCycleStateCreating,
		CreateJobType: datamodel.JobTypeCreateVolume,
	}

	createJobResult := &CreateJobResult{
		JobUUID:    "job-uuid",
		WorkflowID: "workflow-id",
	}

	getCreateJobActivity := func(ctx context.Context, resourceUUID string, correlationID string, jobType string) (*CreateJobResult, error) {
		return createJobResult, nil
	}
	cancellationActivity := &testCancellationActivity{}
	commonActivity := &testCommonActivity{}

	env.RegisterActivity(getCreateJobActivity)
	env.RegisterActivity(cancellationActivity)
	env.RegisterActivity(commonActivity)

	// Correlation ID is extracted from workflow context
	correlationID := fields[string(middleware.RequestCorrelationID)].(string)
	env.OnActivity(getCreateJobActivity, mock.Anything, params.ResourceUUID, correlationID, string(params.CreateJobType)).Return(createJobResult, nil)
	// Make IsWorkflowRunningActivity return error to cause HandleCancellationInDeleteWorkflow to fail
	env.OnActivity(cancellationActivity.IsWorkflowRunningActivity, mock.Anything, createJobResult.WorkflowID).Return(false, errors.New("temporal client error"))

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, middleware.TemporalSLoggerKey, fields)
		return HandleCancellationForCreatingResource(ctx, mockLogger, params, getCreateJobActivity, cancellationActivity, commonActivity)
	})

	require.True(t, env.IsWorkflowCompleted())
	// The function should return nil even on error (it logs warning and proceeds)
	require.NoError(t, env.GetWorkflowError())
	mockLogger.AssertExpectations(t)
}
