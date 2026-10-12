package workflows

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	coreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type mockEncodedValue struct {
	err bool
}

type mockWorkflowContext struct {
	workflow.Context
	base context.Context
}

func (m *mockWorkflowContext) Value(key interface{}) interface{} {
	return m.base.Value(key)
}

type mockFuture struct{ mock.Mock }

func (m *mockFuture) Get(ctx workflow.Context, valuePtr interface{}) error {
	args := m.Called(ctx, valuePtr)
	return args.Error(0)
}
func (m *mockFuture) IsReady() bool { return true }

func (m mockEncodedValue) Get(valuePtr interface{}) error {
	if m.err {
		return fmt.Errorf("encoding error for value: %+v", valuePtr)
	}
	return nil
}

func (m mockEncodedValue) HasValue() bool {
	return true
}

func TestQueryWorkflowStatus_Success(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	expectedStatus := &WorkflowStatus{}

	mockClient.On("QueryWorkflow", mock.Anything, "wf-1", "run-1", StatusQueryName).
		Return(mockEncodedValue{err: false}, nil)

	status, err := QueryWorkflowStatus(context.Background(), mockClient, "wf-1", "run-1")
	assert.NoError(t, err)
	assert.Equal(t, expectedStatus, status)
}

func TestQueryWorkflowStatus_EncodeError(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)

	mockClient.On("QueryWorkflow", mock.Anything, "wf-1", "run-1", StatusQueryName).
		Return(mockEncodedValue{err: true}, nil)

	status, err := QueryWorkflowStatus(context.Background(), mockClient, "wf-1", "run-1")
	assert.Error(t, err)
	assert.Nil(t, status)
}

func TestQueryWorkflowStatus_QueryError(t *testing.T) {
	mockClient := workflow_engine.NewMockTemporalTestClient(t)
	mockClient.On("QueryWorkflow", mock.Anything, "wf-2", "run-2", StatusQueryName).
		Return(nil, errors.New("query error"))

	status, err := QueryWorkflowStatus(context.Background(), mockClient, "wf-2", "run-2")
	assert.Error(t, err)
	assert.Nil(t, status)
}

func TestPopulateRetryPolicyParams(t *testing.T) {
	origStartToCloseTimeout := StartToCloseTimeout
	origStartToCloseTimeoutLV := StartToCloseTimeoutLV
	origRetryInterval := RetryInterval
	origRetryMaxAttempts := RetryMaxAttempts
	origRetryMaxInterval := RetryMaxInterval
	origRetryBackoff := RetryBackoff

	defer func() {
		StartToCloseTimeout = origStartToCloseTimeout
		StartToCloseTimeoutLV = origStartToCloseTimeoutLV
		RetryInterval = origRetryInterval
		RetryMaxAttempts = origRetryMaxAttempts
		RetryMaxInterval = origRetryMaxInterval
		RetryBackoff = origRetryBackoff
	}()

	t.Run("success_standard_pool", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "1s"
		RetryMaxAttempts = 2
		RetryMaxInterval = "2m"
		RetryBackoff = "1.5"

		// Test standard pool (no parameter)
		policy, err := PopulateRetryPolicyParams()
		assert.NoError(t, err)
		assert.NotNil(t, policy)
		assert.Equal(t, 25*time.Minute, policy.StartToCloseTimeout)
	})

	t.Run("success_standard_pool_explicit_false", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "1s"
		RetryMaxAttempts = 2
		RetryMaxInterval = "2m"
		RetryBackoff = "1.5"

		// Test standard pool (explicitly false)
		policy, err := PopulateRetryPolicyParams(false)
		assert.NoError(t, err)
		assert.NotNil(t, policy)
		assert.Equal(t, 25*time.Minute, policy.StartToCloseTimeout)
	})

	t.Run("success_large_capacity_pool", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "1s"
		RetryMaxAttempts = 2
		RetryMaxInterval = "2m"
		RetryBackoff = "1.5"

		// Test large capacity pool
		policy, err := PopulateRetryPolicyParams(true)
		assert.NoError(t, err)
		assert.NotNil(t, policy)
		assert.Equal(t, 35*time.Minute, policy.StartToCloseTimeout)
	})

	t.Run("timeout_values_are_different", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "1s"
		RetryMaxAttempts = 2
		RetryMaxInterval = "2m"
		RetryBackoff = "1.5"

		// Test that standard and large capacity timeouts are different
		standardPolicy, err1 := PopulateRetryPolicyParams(false)
		largePolicy, err2 := PopulateRetryPolicyParams(true)

		assert.NoError(t, err1)
		assert.NoError(t, err2)
		assert.NotEqual(t, standardPolicy.StartToCloseTimeout, largePolicy.StartToCloseTimeout)
		assert.Equal(t, 25*time.Minute, standardPolicy.StartToCloseTimeout)
		assert.Equal(t, 35*time.Minute, largePolicy.StartToCloseTimeout)
	})

	t.Run("invalid_StartToCloseTimeout_standard", func(t *testing.T) {
		StartToCloseTimeout = "invalid"
		StartToCloseTimeoutLV = "35m"
		_, err := PopulateRetryPolicyParams(false)
		assert.Error(t, err)
	})

	t.Run("invalid_StartToCloseTimeoutLV_large_capacity", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "invalid"
		_, err := PopulateRetryPolicyParams(true)
		assert.Error(t, err)
	})

	t.Run("invalid RetryInterval", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "invalid"
		_, err := PopulateRetryPolicyParams()
		assert.Error(t, err)
	})

	t.Run("invalid RetryMaxInterval", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "1s"
		RetryMaxInterval = "invalid"
		_, err := PopulateRetryPolicyParams()
		assert.Error(t, err)
	})

	t.Run("invalid RetryBackoff", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "1s"
		RetryMaxInterval = "2m"
		RetryBackoff = "invalid"
		_, err := PopulateRetryPolicyParams()
		assert.Error(t, err)
	})

	t.Run("verify_all_fields_populated_correctly", func(t *testing.T) {
		StartToCloseTimeout = "25m"
		StartToCloseTimeoutLV = "35m"
		RetryInterval = "5s"
		RetryMaxAttempts = 3
		RetryMaxInterval = "5m"
		RetryBackoff = "2.0"

		policy, err := PopulateRetryPolicyParams(true)
		assert.NoError(t, err)
		assert.NotNil(t, policy)

		// Verify all fields are populated correctly
		assert.Equal(t, 35*time.Minute, policy.StartToCloseTimeout)
		assert.Equal(t, 5*time.Second, policy.InitialInterval)
		assert.Equal(t, 3, policy.MaximumAttempts)
		assert.Equal(t, 5*time.Minute, policy.MaximumInterval)
		assert.Equal(t, 2.0, policy.BackoffCoefficient)
	})
}

func TestPopulateADSyncRetryPolicyParams(t *testing.T) {
	origStartToClose := adSyncRetryStartToCloseTimeout
	origInitialInterval := adSyncRetryInitialInterval
	origMaxInterval := adSyncRetryMaxInterval
	origBackoff := adSyncRetryBackoffCoefficient
	origHeartbeat := adSyncHeartBeatTimeout
	origMaxAttempts := adSyncRetryMaxAttempts

	defer func() {
		adSyncRetryStartToCloseTimeout = origStartToClose
		adSyncRetryInitialInterval = origInitialInterval
		adSyncRetryMaxInterval = origMaxInterval
		adSyncRetryBackoffCoefficient = origBackoff
		adSyncHeartBeatTimeout = origHeartbeat
		adSyncRetryMaxAttempts = origMaxAttempts
	}()

	t.Run("valid_values", func(t *testing.T) {
		adSyncRetryStartToCloseTimeout = "15m"
		adSyncRetryInitialInterval = "5s"
		adSyncRetryMaxInterval = "30s"
		adSyncRetryBackoffCoefficient = "1.5"
		adSyncHeartBeatTimeout = "3m"
		adSyncRetryMaxAttempts = 5

		policy := PopulateADSyncRetryPolicyParams()
		assert.NotNil(t, policy)
		assert.Equal(t, 15*time.Minute, policy.StartToCloseTimeout)
		assert.Equal(t, 5*time.Second, policy.InitialInterval)
		assert.Equal(t, 30*time.Second, policy.MaximumInterval)
		assert.Equal(t, 1.5, policy.BackoffCoefficient)
		assert.Equal(t, 3*time.Minute, policy.HeartBeatTimeout)
		assert.Equal(t, 5, policy.MaximumAttempts)
	})

	t.Run("invalid_values_use_defaults", func(t *testing.T) {
		adSyncRetryStartToCloseTimeout = "invalid"
		adSyncRetryInitialInterval = "invalid"
		adSyncRetryMaxInterval = "invalid"
		adSyncRetryBackoffCoefficient = "invalid"
		adSyncHeartBeatTimeout = "invalid"
		adSyncRetryMaxAttempts = 10

		policy := PopulateADSyncRetryPolicyParams()
		assert.NotNil(t, policy)
		assert.Equal(t, 10*time.Minute, policy.StartToCloseTimeout)
		assert.Equal(t, 10*time.Second, policy.InitialInterval)
		assert.Equal(t, 60*time.Second, policy.MaximumInterval)
		assert.Equal(t, 2.0, policy.BackoffCoefficient)
		assert.Equal(t, 2*time.Minute, policy.HeartBeatTimeout)
		assert.Equal(t, 10, policy.MaximumAttempts)
	})
}

func TestPopulateServiceAccountRetryPolicyParams(t *testing.T) {
	// Save original values
	origSARetryStartToCloseTimeout := SARetryStartToCloseTimeout
	origSARetryInitialInterval := SARetryInitialInterval
	origSARetryBackoffCoefficient := SARetryBackoffCoefficient
	origSARetryMaximumInterval := SARetryMaximumInterval
	origSARetryMaximumAttempts := SARetryMaximumAttempts

	defer func() {
		SARetryStartToCloseTimeout = origSARetryStartToCloseTimeout
		SARetryInitialInterval = origSARetryInitialInterval
		SARetryBackoffCoefficient = origSARetryBackoffCoefficient
		SARetryMaximumInterval = origSARetryMaximumInterval
		SARetryMaximumAttempts = origSARetryMaximumAttempts
	}()

	t.Run("success with updated default values", func(t *testing.T) {
		SARetryStartToCloseTimeout = "25m"
		SARetryInitialInterval = "10s"
		SARetryBackoffCoefficient = "2.0"
		SARetryMaximumInterval = "60s"
		SARetryMaximumAttempts = 12

		policy, err := populateServiceAccountRetryPolicyParams()
		assert.NoError(t, err)
		assert.NotNil(t, policy)
		assert.Equal(t, 25*time.Minute, policy.StartToCloseTimeout)
		assert.Equal(t, 10*time.Second, policy.InitialInterval)
		assert.Equal(t, 2.0, policy.BackoffCoefficient)
		assert.Equal(t, 60*time.Second, policy.MaximumInterval)
		assert.Equal(t, 12, policy.MaximumAttempts)
	})

	t.Run("success with custom values", func(t *testing.T) {
		SARetryStartToCloseTimeout = "15m"
		SARetryInitialInterval = "2s"
		SARetryBackoffCoefficient = "1.5"
		SARetryMaximumInterval = "5m"
		SARetryMaximumAttempts = 8

		policy, err := populateServiceAccountRetryPolicyParams()
		assert.NoError(t, err)
		assert.NotNil(t, policy)
		assert.Equal(t, 15*time.Minute, policy.StartToCloseTimeout)
		assert.Equal(t, 2*time.Second, policy.InitialInterval)
		assert.Equal(t, 1.5, policy.BackoffCoefficient)
		assert.Equal(t, 5*time.Minute, policy.MaximumInterval)
		assert.Equal(t, 8, policy.MaximumAttempts)
	})

	t.Run("invalid StartToCloseTimeout", func(t *testing.T) {
		SARetryStartToCloseTimeout = "invalid-timeout"
		SARetryInitialInterval = "10s"
		SARetryBackoffCoefficient = "2.0"
		SARetryMaximumInterval = "60s"
		SARetryMaximumAttempts = 12

		policy, err := populateServiceAccountRetryPolicyParams()
		assert.Error(t, err)
		assert.Nil(t, policy)
		assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "invalid-timeout")
	})

	t.Run("invalid InitialInterval", func(t *testing.T) {
		SARetryStartToCloseTimeout = "25m"
		SARetryInitialInterval = "invalid-interval"
		SARetryBackoffCoefficient = "2.0"
		SARetryMaximumInterval = "60s"
		SARetryMaximumAttempts = 12

		policy, err := populateServiceAccountRetryPolicyParams()
		assert.Error(t, err)
		assert.Nil(t, policy)
		assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "invalid-interval")
	})

	t.Run("invalid BackoffCoefficient", func(t *testing.T) {
		SARetryStartToCloseTimeout = "25m"
		SARetryInitialInterval = "10s"
		SARetryBackoffCoefficient = "invalid-backoff"
		SARetryMaximumInterval = "60s"
		SARetryMaximumAttempts = 12

		policy, err := populateServiceAccountRetryPolicyParams()
		assert.Error(t, err)
		assert.Nil(t, policy)
		assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "invalid-backoff")
	})

	t.Run("invalid MaximumInterval", func(t *testing.T) {
		SARetryStartToCloseTimeout = "25m"
		SARetryInitialInterval = "10s"
		SARetryBackoffCoefficient = "2.0"
		SARetryMaximumInterval = "invalid-max-interval"
		SARetryMaximumAttempts = 12

		policy, err := populateServiceAccountRetryPolicyParams()
		assert.Error(t, err)
		assert.Nil(t, policy)
		assert.Contains(t, err.(*vsaerrors.CustomError).OriginalErr.Error(), "invalid-max-interval")
	})
}

func TestUpdateJobStatusWithCustomError(t *testing.T) {
	ctx := context.TODO()
	wfCtx := &mockWorkflowContext{base: ctx}
	origExecuteActivity := executeActivity

	defer func() { executeActivity = origExecuteActivity }()
	executeActivity = func(ctx workflow.Context, activity interface{}, args ...interface{}) workflow.Future {
		f := new(mockFuture)
		f.On("Get", ctx, mock.Anything).Return(nil)
		return f
	}

	bw := &BaseWorkflow{ID: "test-id"}
	jobError := temporal.NewApplicationError("test error", "CustomError", 123, "original error details")
	err := bw.UpdateJobStatus(wfCtx, models.JobStateFailure, jobError)

	assert.NoError(t, err)
}

func TestUpdateJobStatusWithCustomErrorMissingDetails(t *testing.T) {
	ctx := context.TODO()
	wfCtx := &mockWorkflowContext{base: ctx}
	origExecuteActivity := executeActivity

	defer func() { executeActivity = origExecuteActivity }()
	executeActivity = func(ctx workflow.Context, activity interface{}, args ...interface{}) workflow.Future {
		f := new(mockFuture)
		f.On("Get", ctx, mock.Anything).Return(nil)
		return f
	}

	bw := &BaseWorkflow{Logger: util.GetLogger(ctx), ID: "test-id"}
	jobError := temporal.NewApplicationError("test error", "CustomError", nil)
	err := bw.UpdateJobStatus(wfCtx, models.JobStateFailure, jobError)

	assert.NoError(t, err)
}

func TestUpdateJobStatusWithGenericError(t *testing.T) {
	ctx := context.TODO()
	wfCtx := &mockWorkflowContext{base: ctx}
	origExecuteActivity := executeActivity

	defer func() { executeActivity = origExecuteActivity }()
	executeActivity = func(ctx workflow.Context, activity interface{}, args ...interface{}) workflow.Future {
		f := new(mockFuture)
		f.On("Get", ctx, mock.Anything).Return(nil)
		return f
	}

	bw := &BaseWorkflow{Logger: util.GetLogger(ctx), ID: "test-id"}
	jobError := errors.New("test error")
	err := bw.UpdateJobStatus(wfCtx, models.JobStateFailure, jobError)

	assert.NoError(t, err)
}

func TestUpdateJobStatusWithEmptyID(t *testing.T) {
	ctx := context.TODO()
	wfCtx := &mockWorkflowContext{base: ctx}
	origExecuteActivity := executeActivity

	defer func() { executeActivity = origExecuteActivity }()
	executeActivity = func(ctx workflow.Context, activity interface{}, args ...interface{}) workflow.Future {
		f := new(mockFuture)
		f.On("Get", ctx, mock.Anything).Return(nil)
		return f
	}

	bw := &BaseWorkflow{ID: ""}
	err := bw.UpdateJobStatus(wfCtx, models.JobStateFailure, errors.New("test error"))

	assert.Error(t, err)
	assert.ErrorContains(t, err.(*vsaerrors.CustomError).OriginalErr, "job uuid cannot be empty")
}

func TestEnsureJobStateSuccess(t *testing.T) {
	ctx := context.TODO()
	wfCtx := &mockWorkflowContext{base: ctx}
	origExecuteActivity := executeActivity

	defer func() { executeActivity = origExecuteActivity }()
	executeActivity = func(ctx workflow.Context, activity interface{}, args ...interface{}) workflow.Future {
		f := new(mockFuture)
		f.On("Get", ctx, mock.Anything).
			Run(func(args mock.Arguments) {
				jobPtr := args[1].(**datamodel.Job)
				*jobPtr = &datamodel.Job{State: string(datamodel.JobsStateNEW)}
			}).
			Return(nil)
		return f
	}

	bw := &BaseWorkflow{ID: "job-id"}
	err := bw.EnsureJobState(wfCtx, datamodel.JobsStateNEW)

	assert.NoError(t, err)
}

func TestEnsureJobStateMismatch(t *testing.T) {
	ctx := context.TODO()
	wfCtx := &mockWorkflowContext{base: ctx}
	origExecuteActivity := executeActivity

	defer func() { executeActivity = origExecuteActivity }()
	executeActivity = func(ctx workflow.Context, activity interface{}, args ...interface{}) workflow.Future {
		f := new(mockFuture)
		f.On("Get", ctx, mock.Anything).
			Run(func(args mock.Arguments) {
				jobPtr := args[1].(**datamodel.Job)
				*jobPtr = &datamodel.Job{State: string(datamodel.JobsStatePROCESSING)}
			}).
			Return(nil)
		return f
	}

	bw := &BaseWorkflow{ID: "job-id"}
	err := bw.EnsureJobState(wfCtx, datamodel.JobsStateNEW)

	assert.Error(t, err)
	customErr, ok := err.(*vsaerrors.CustomError)
	assert.True(t, ok)
	assert.Equal(t, vsaerrors.ErrResourceStateConflictError, customErr.TrackingID)
	assert.NotNil(t, customErr.OriginalErr)
	assert.Contains(t, customErr.OriginalErr.Error(), "expected NEW")
}

func TestEnsureJobStateEmptyID(t *testing.T) {
	ctx := context.TODO()
	wfCtx := &mockWorkflowContext{base: ctx}

	bw := &BaseWorkflow{ID: ""}
	err := bw.EnsureJobState(wfCtx, datamodel.JobsStateNEW)

	assert.Error(t, err)
	customErr, ok := err.(*vsaerrors.CustomError)
	assert.True(t, ok)
	assert.Equal(t, vsaerrors.ErrWorkflowConfigurationError, customErr.TrackingID)
	assert.NotNil(t, customErr.OriginalErr)
	assert.Contains(t, customErr.OriginalErr.Error(), "job uuid cannot be empty")
}

func TestGetSnapshotPolicyName(t *testing.T) {
	t.Run("ReturnsPolicyName", func(t *testing.T) {
		volume := &datamodel.Volume{
			SnapshotPolicy: &datamodel.SnapshotPolicy{
				Name: "policy1",
			},
		}
		result := getSnapshotPolicyName(volume)
		assert.Equal(t, "policy1", result)
	})

	t.Run("ReturnsEmptyString_WhenVolumeIsNil", func(t *testing.T) {
		var volume *datamodel.Volume
		result := getSnapshotPolicyName(volume)
		assert.Equal(t, "", result)
	})

	t.Run("ReturnsEmptyString_WhenSnapshotPolicyIsNil", func(t *testing.T) {
		volume := &datamodel.Volume{}
		result := getSnapshotPolicyName(volume)
		assert.Equal(t, "", result)
	})

	t.Run("ReturnsEmptyString_WhenPolicyNameIsEmpty", func(t *testing.T) {
		volume := &datamodel.Volume{
			SnapshotPolicy: &datamodel.SnapshotPolicy{
				Name: "",
			},
		}
		result := getSnapshotPolicyName(volume)
		assert.Equal(t, "", result)
	})
}

func WfTest(ctx workflow.Context, jobUUID string, timeout time.Duration) error {
	activityTimeout := timeout
	if timeout < 5*time.Second {
		activityTimeout = 5 * time.Second
	}
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: activityTimeout,
	})
	err := PollOnDBJob(ctx, jobUUID, timeout)
	if err != nil {
		return fmt.Errorf("workflow test failed: %w", err)
	}
	return nil
}
func TestWaitForDBJob_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	commonActivity := activities.CommonActivities{}
	jobUUID := "job-uuid"
	job := &datamodel.Job{
		State: "DONE",
	}

	env.OnActivity(commonActivity.GetJob, mock.Anything, jobUUID).Return(job, nil)

	env.RegisterActivity(commonActivity.GetJob)
	env.ExecuteWorkflow(WfTest, jobUUID, 1*time.Minute)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func TestWaitForDBJob_JobWithErrorDetails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	commonActivity := activities.CommonActivities{}

	jobUUID := "job-uuid"
	job := &datamodel.Job{
		State:        "DONE",
		ErrorDetails: "some error",
	}

	env.OnActivity(commonActivity.GetJob, mock.Anything, jobUUID).Return(job, nil)

	env.RegisterActivity(commonActivity.GetJob)
	env.ExecuteWorkflow(WfTest, jobUUID, 1*time.Minute)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "job completed with error")
}

func TestWaitForDBJob_JobErrorWithErrorDetails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	commonActivity := activities.CommonActivities{}

	jobUUID := "job-uuid"
	job := &datamodel.Job{
		State:        "ERROR",
		ErrorDetails: "some error",
	}

	env.OnActivity(commonActivity.GetJob, mock.Anything, jobUUID).Return(job, nil)

	env.RegisterActivity(commonActivity.GetJob)
	env.ExecuteWorkflow(WfTest, jobUUID, 1*time.Minute)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "some error")
}

func TestWaitForDBJob_Timeout(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	commonActivity := activities.CommonActivities{}

	jobUUID := "job-uuid"
	job := &datamodel.Job{
		State: "PENDING",
	}

	env.OnActivity(commonActivity.GetJob, mock.Anything, jobUUID).Return(job, nil)

	env.RegisterActivity(commonActivity.GetJob)
	env.ExecuteWorkflow(WfTest, jobUUID, 1*time.Millisecond)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestWaitForDBJob_GetJobFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	commonActivity := activities.CommonActivities{}

	jobUUID := "job-uuid"

	env.OnActivity(commonActivity.GetJob, mock.Anything, jobUUID).Return(nil, assert.AnError)

	env.RegisterActivity(commonActivity.GetJob)
	env.ExecuteWorkflow(WfTest, jobUUID, 1*time.Minute)

	assert.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	assert.Error(t, err)
	assert.Contains(t, vsaerrors.ExtractCustomError(err).OriginalErr.Error(), "failed to get db job status")
}

func TestCreateNodeForProviderWithPool_CERT(t *testing.T) {
	dbNodes := []*datamodel.Node{
		{EndpointAddress: "1.1.1.1", HostDNSName: "host1"},
		{EndpointAddress: "2.2.2.2", HostDNSName: "host2"},
	}
	pool := &datamodel.Pool{
		DeploymentName: "cluster1",
		PoolCredentials: &datamodel.PoolCredentials{
			CertificateID: "cert-123",
			AuthType:      env.USER_CERTIFICATE,
		},
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})
	assert.Equal(t, map[string]string{"1.1.1.1": "host1", "2.2.2.2": "host2"}, node.EndpointAddressesToHostNameMap)
	assert.Equal(t, "cluster1", node.DeploymentName)
	assert.Equal(t, "cert-123", node.CertificateID)
}

func TestCreateNodeForProviderWithPool_NonCERT(t *testing.T) {
	dbNodes := []*datamodel.Node{
		{EndpointAddress: "1.1.1.1"},
		{EndpointAddress: "2.2.2.2"},
	}
	pool := &datamodel.Pool{
		DeploymentName: "cluster2",
		PoolCredentials: &datamodel.PoolCredentials{
			Password: "secret",
			AuthType: env.USERNAME_PWD,
		},
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})
	assert.Equal(t, map[string]string{"1.1.1.1": "1.1.1.1", "2.2.2.2": "2.2.2.2"}, node.EndpointAddressesToHostNameMap)
	assert.Equal(t, "secret", node.Password)
	assert.Equal(t, "cluster2", node.DeploymentName)
}

func TestPopulateRetryPolicyParamsTimeoutSelection(t *testing.T) {
	origStartToCloseTimeout := StartToCloseTimeout
	origStartToCloseTimeoutLV := StartToCloseTimeoutLV
	origRetryInterval := RetryInterval
	origRetryMaxAttempts := RetryMaxAttempts
	origRetryMaxInterval := RetryMaxInterval
	origRetryBackoff := RetryBackoff

	defer func() {
		StartToCloseTimeout = origStartToCloseTimeout
		StartToCloseTimeoutLV = origStartToCloseTimeoutLV
		RetryInterval = origRetryInterval
		RetryMaxAttempts = origRetryMaxAttempts
		RetryMaxInterval = origRetryMaxInterval
		RetryBackoff = origRetryBackoff
	}()

	// Setup valid values for all other fields
	RetryInterval = "5s"
	RetryMaxAttempts = 3
	RetryMaxInterval = "5m"
	RetryBackoff = "2.0"

	tests := []struct {
		name                   string
		standardTimeout        string
		largeCapacityTimeout   string
		largeCapacity          *bool
		expectedTimeoutMinutes int
	}{
		{
			name:                   "no_parameter_uses_standard_timeout",
			standardTimeout:        "20m",
			largeCapacityTimeout:   "40m",
			largeCapacity:          nil,
			expectedTimeoutMinutes: 20,
		},
		{
			name:                   "false_parameter_uses_standard_timeout",
			standardTimeout:        "30m",
			largeCapacityTimeout:   "50m",
			largeCapacity:          boolPtr(false),
			expectedTimeoutMinutes: 30,
		},
		{
			name:                   "true_parameter_uses_large_capacity_timeout",
			standardTimeout:        "15m",
			largeCapacityTimeout:   "45m",
			largeCapacity:          boolPtr(true),
			expectedTimeoutMinutes: 45,
		},
		{
			name:                   "default_production_values_standard",
			standardTimeout:        "25m",
			largeCapacityTimeout:   "35m",
			largeCapacity:          boolPtr(false),
			expectedTimeoutMinutes: 25,
		},
		{
			name:                   "default_production_values_large_capacity",
			standardTimeout:        "25m",
			largeCapacityTimeout:   "35m",
			largeCapacity:          boolPtr(true),
			expectedTimeoutMinutes: 35,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			StartToCloseTimeout = tt.standardTimeout
			StartToCloseTimeoutLV = tt.largeCapacityTimeout

			var policy *WorkflowRetryPolicy
			var err error

			if tt.largeCapacity == nil {
				policy, err = PopulateRetryPolicyParams()
			} else {
				policy, err = PopulateRetryPolicyParams(*tt.largeCapacity)
			}

			assert.NoError(t, err)
			assert.NotNil(t, policy)
			expectedTimeout := time.Duration(tt.expectedTimeoutMinutes) * time.Minute
			assert.Equal(t, expectedTimeout, policy.StartToCloseTimeout)
		})
	}
}

// Helper function to create a pointer to bool
func boolPtr(b bool) *bool {
	return &b
}

func TestWithKmsRotationLock_AcquireFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	env.RegisterActivity(activity.AcquireKmsRotationLockActivity)
	env.OnActivity(activity.AcquireKmsRotationLockActivity, mock.Anything, "kms-uuid").Return("", errors.New("acquire failed"))

	testWf := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		_, _, err := WithKmsRotationLock(ctx, activity, "kms-uuid")
		return err
	}
	env.ExecuteWorkflow(testWf)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "acquire failed")
}

// TestWithKmsRotationLockForKmsConfig_LocksAtServiceAccountScope verifies that a caller holding only
// a KMS config UUID resolves the owning service account and acquires the lock keyed by that
// service-account UUID (not the KMS config UUID). This is what keeps configure/EKM operations
// mutually exclusive with the scheduled rotation workflow for configs that share a service account.
func TestWithKmsRotationLockForKmsConfig_LocksAtServiceAccountScope(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	env.RegisterActivity(activity.GetServiceAccountUUIDForKmsConfigActivity)
	env.RegisterActivity(activity.AcquireKmsRotationLockActivity)
	env.RegisterActivity(activity.RenewKmsRotationLockActivity)
	env.RegisterActivity(activity.ReleaseKmsRotationLockActivity)

	env.OnActivity(activity.GetServiceAccountUUIDForKmsConfigActivity, mock.Anything, "kms-uuid").Return("sa-uuid", nil)
	// The lock MUST be acquired with the resolved service-account UUID, not the KMS config UUID.
	env.OnActivity(activity.AcquireKmsRotationLockActivity, mock.Anything, "sa-uuid").Return("lock-client-id", nil)
	env.OnActivity(activity.RenewKmsRotationLockActivity, mock.Anything, "sa-uuid", "lock-client-id").Return(nil).Maybe()
	env.OnActivity(activity.ReleaseKmsRotationLockActivity, mock.Anything, "sa-uuid", "lock-client-id").Return(nil).Maybe()

	testWf := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		_, release, err := WithKmsRotationLockForKmsConfig(ctx, activity, "kms-uuid")
		if err != nil {
			return err
		}
		release()
		return nil
	}
	env.ExecuteWorkflow(testWf)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertCalled(t, "AcquireKmsRotationLockActivity", mock.Anything, "sa-uuid")
}

// TestWithKmsRotationLockForKmsConfig_ResolveFails verifies that if the service account cannot be
// resolved from the KMS config, no lock is acquired and the error propagates.
func TestWithKmsRotationLockForKmsConfig_ResolveFails(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	activity := &backgroundactivities.RotateKmsSAKeyActivity{}
	env.RegisterActivity(activity.GetServiceAccountUUIDForKmsConfigActivity)
	env.RegisterActivity(activity.AcquireKmsRotationLockActivity)

	env.OnActivity(activity.GetServiceAccountUUIDForKmsConfigActivity, mock.Anything, "kms-uuid").Return("", errors.New("no service account associated with KMS config"))

	testWf := func(ctx workflow.Context) error {
		ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
			StartToCloseTimeout: time.Minute,
		})
		_, _, err := WithKmsRotationLockForKmsConfig(ctx, activity, "kms-uuid")
		return err
	}
	env.ExecuteWorkflow(testWf)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "no service account associated with KMS config")
	env.AssertNotCalled(t, "AcquireKmsRotationLockActivity", mock.Anything, mock.Anything)
}

// TestPollTransferStatusWithContinueAsNewCommon tests the PollTransferStatusWithContinueAsNewCommon function
func TestPollTransferStatusWithContinueAsNewCommon(t *testing.T) {
	// Save original environment variables
	origStartToCloseTimeout := StartToCloseTimeout
	origRetryInterval := RetryInterval
	origRetryMaxAttempts := RetryMaxAttempts
	origRetryMaxInterval := RetryMaxInterval
	origRetryBackoff := RetryBackoff

	defer func() {
		StartToCloseTimeout = origStartToCloseTimeout
		RetryInterval = origRetryInterval
		RetryMaxAttempts = origRetryMaxAttempts
		RetryMaxInterval = origRetryMaxInterval
		RetryBackoff = origRetryBackoff
	}()

	// Set test environment variables
	StartToCloseTimeout = "25m"
	RetryInterval = "5s"
	RetryMaxAttempts = 3
	RetryMaxInterval = "5m"
	RetryBackoff = "2.0"

	t.Run("WhenSuccessTransferCompletesImmediately", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Mock the polling activity to return transfer complete immediately
		// Use mock.Anything for context since it's a complex type
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(&activities.PollTransferStatusOutput{
				BackupActivitiesContext: backupActivitiesContext,
				TransferComplete:        true,
				ShouldContinueAsNew:     false,
				ContinueAsNewReason:     "",
				NextWaitTime:            5 * time.Second,
				TransferStatus: &activities.SnapmirrorTransferStatus{
					Status:           activities.SmStatusSuccess,
					BytesTransferred: nil,
				},
			}, nil)

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify no error occurred
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenSuccessContinueAsNewTriggered", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Mock the polling activity to trigger ContinueAsNew
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(&activities.PollTransferStatusOutput{
				BackupActivitiesContext: backupActivitiesContext,
				TransferComplete:        false,
				ShouldContinueAsNew:     true,
				ContinueAsNewReason:     "Event history limit reached",
				NextWaitTime:            5 * time.Second,
			}, nil)

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify ContinueAsNewError was returned
		err := env.GetWorkflowError()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "continue as new")
		env.AssertExpectations(t)
	})

	t.Run("WhenErrorPollingActivityFails", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Mock the polling activity to return an error
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(nil, errors.New("polling activity failed"))

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify error was returned
		err := env.GetWorkflowError()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "polling activity failed")
		env.AssertExpectations(t)
	})

	t.Run("WhenErrorRetryPolicyConfigurationFails", func(t *testing.T) {
		// Set invalid retry policy configuration
		StartToCloseTimeout = "invalid-duration"

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify error was returned due to invalid retry policy
		err := env.GetWorkflowError()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid duration")
	})

	t.Run("WhenSuccessExponentialBackoffBehavior", func(t *testing.T) {
		// Reset environment variables for this test
		StartToCloseTimeout = "25m"
		RetryInterval = "5s"
		RetryMaxAttempts = 3
		RetryMaxInterval = "5m"
		RetryBackoff = "2.0"

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Track the wait times passed to the activity
		var waitTimes []time.Duration
		callCount := 0

		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Run(func(args mock.Arguments) {
				callCount++
				input := args.Get(1).(*activities.PollTransferStatusInput)
				waitTimes = append(waitTimes, input.NextWaitTime)
			}).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				if callCount < 3 {
					// First two calls: transfer in progress
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        false,
						ShouldContinueAsNew:     false,
						ContinueAsNewReason:     "",
						NextWaitTime:            input.NextWaitTime,
					}, nil
				}
				// Third call: transfer complete
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        true,
					ShouldContinueAsNew:     false,
					ContinueAsNewReason:     "",
					NextWaitTime:            input.NextWaitTime,
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify no error occurred and exponential backoff was applied
		assert.NoError(t, env.GetWorkflowError())
		assert.Equal(t, 3, callCount)

		// Verify exponential backoff: 5s -> 10s -> 20s
		assert.Equal(t, 3, len(waitTimes))
		if len(waitTimes) >= 3 { // Ensure the slice has enough elements
			assert.Equal(t, 5*time.Second, waitTimes[0])
			assert.Equal(t, 10*time.Second, waitTimes[1])
			assert.Equal(t, 20*time.Second, waitTimes[2])
		}

		env.AssertExpectations(t)
	})

	t.Run("WhenSuccessContextUpdatedCorrectly", func(t *testing.T) {
		// Reset environment variables for this test
		StartToCloseTimeout = "25m"
		RetryInterval = "5s"
		RetryMaxAttempts = 3
		RetryMaxInterval = "5m"
		RetryBackoff = "2.0"

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		originalContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		updatedContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName:   "test-snapshot",
			TransferStatus: "success",
		}

		// Mock the polling activity to return updated context
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(&activities.PollTransferStatusOutput{
				BackupActivitiesContext: updatedContext,
				TransferComplete:        true,
				ShouldContinueAsNew:     false,
				ContinueAsNewReason:     "",
				NextWaitTime:            5 * time.Second,
			}, nil)

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, originalContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify no error occurred
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenSuccessActivityOptionsConfiguredCorrectly", func(t *testing.T) {
		// Reset environment variables for this test
		StartToCloseTimeout = "25m"
		RetryInterval = "5s"
		RetryMaxAttempts = 3
		RetryMaxInterval = "5m"
		RetryBackoff = "2.0"

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Mock the polling activity
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(&activities.PollTransferStatusOutput{
				BackupActivitiesContext: backupActivitiesContext,
				TransferComplete:        true,
				ShouldContinueAsNew:     false,
				ContinueAsNewReason:     "",
				NextWaitTime:            5 * time.Second,
			}, nil)

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify no error occurred
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenTransferStuck_ContinuesPollingUntilCompletion", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Fixed bytes (same every call = stuck transfer). The code only logs this, it does not return an error.
		fixedBytes := int64(1000)
		callCount := 0
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				callCount++
				if callCount >= 4 {
					// Eventually complete after several "stuck" iterations
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        true,
						ShouldContinueAsNew:     false,
					}, nil
				}
				// Transfer appears stuck (same bytes) - code logs and continues polling
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        false,
					ShouldContinueAsNew:     false,
					TransferStatus: &activities.SnapmirrorTransferStatus{
						Status:           activities.SmStatusTransferring,
						BytesTransferred: &fixedBytes, // Same bytes every time = stuck
					},
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Stuck transfer does not cause an error; the workflow logs and continues polling until completion
		assert.NoError(t, env.GetWorkflowError())
		assert.Equal(t, 4, callCount)
		env.AssertExpectations(t)
	})

	t.Run("WhenTransferMakesProgress", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Mock the polling activity to return transfer incomplete initially, then complete with increasing bytes
		callCount := 0
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				callCount++
				bytes := int64(1000 * callCount) // Increasing bytes to simulate progress
				if callCount >= 3 {
					// Transfer completes after a few iterations
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        true,
						ShouldContinueAsNew:     false,
						ContinueAsNewReason:     "",
						NextWaitTime:            5 * time.Second,
						TransferStatus: &activities.SnapmirrorTransferStatus{
							Status:           activities.SmStatusSuccess,
							BytesTransferred: &bytes,
						},
					}, nil
				}
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        false,
					ShouldContinueAsNew:     false,
					ContinueAsNewReason:     "",
					NextWaitTime:            5 * time.Second,
					TransferStatus: &activities.SnapmirrorTransferStatus{
						Status:           activities.SmStatusTransferring,
						BytesTransferred: &bytes, // Increasing bytes = progress
					},
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify no error occurred - transfer completed successfully
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenBytesNotAvailable", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Mock the polling activity to return transfer complete after a few iterations with nil bytes
		callCount := 0
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				callCount++
				if callCount >= 2 {
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        true,
						ShouldContinueAsNew:     false,
						ContinueAsNewReason:     "",
						NextWaitTime:            5 * time.Second,
						TransferStatus: &activities.SnapmirrorTransferStatus{
							Status:           activities.SmStatusSuccess,
							BytesTransferred: nil, // Bytes not available
						},
					}, nil
				}
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        false,
					ShouldContinueAsNew:     false,
					ContinueAsNewReason:     "",
					NextWaitTime:            5 * time.Second,
					TransferStatus: &activities.SnapmirrorTransferStatus{
						Status:           activities.SmStatusTransferring,
						BytesTransferred: nil, // Bytes not available
					},
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify no error occurred - workflow should continue with status check only and complete quickly
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenNonTransferringStatus_ContinuesPollingUntilCompletion", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Transfer in a non-transferring state ("queued") with nil bytes.
		// The code only logs this situation; it does not return an error.
		callCount := 0
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				callCount++
				if callCount >= 3 {
					// Eventually complete
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        true,
						ShouldContinueAsNew:     false,
					}, nil
				}
				// Non-transferring status - code logs and continues polling
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        false,
					ShouldContinueAsNew:     false,
					TransferStatus: &activities.SnapmirrorTransferStatus{
						Status:           "queued", // Not "transferring", so fine-grained check won't apply
						BytesTransferred: nil,
					},
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Non-transferring status does not cause an error; the workflow logs and continues polling until completion
		assert.NoError(t, env.GetWorkflowError())
		assert.Equal(t, 3, callCount)
		env.AssertExpectations(t)
	})

	t.Run("WhenGetBytesFails", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Mock the polling activity to return transfer complete after a few iterations with nil bytes (error case)
		callCount := 0
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				callCount++
				if callCount >= 2 {
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        true,
						ShouldContinueAsNew:     false,
						ContinueAsNewReason:     "",
						NextWaitTime:            5 * time.Second,
						TransferStatus: &activities.SnapmirrorTransferStatus{
							Status:           activities.SmStatusSuccess,
							BytesTransferred: nil, // Bytes not available (error case handled in activity)
						},
					}, nil
				}
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        false,
					ShouldContinueAsNew:     false,
					ContinueAsNewReason:     "",
					NextWaitTime:            5 * time.Second,
					TransferStatus: &activities.SnapmirrorTransferStatus{
						Status:           activities.SmStatusTransferring,
						BytesTransferred: nil, // Bytes not available (error case handled in activity)
					},
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Verify no error occurred - workflow should continue with status check only when bytes fail
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WhenBytesNilDuringTransferring_ContinuesPollingUntilCompletion", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// Status is "transferring" but BytesTransferred is nil.
		// The code only logs this situation; it does not return an error.
		callCount := 0
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				callCount++
				if callCount >= 3 {
					// Eventually complete
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        true,
						ShouldContinueAsNew:     false,
					}, nil
				}
				// Transferring status but bytes unavailable - code logs and continues polling
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        false,
					ShouldContinueAsNew:     false,
					TransferStatus: &activities.SnapmirrorTransferStatus{
						Status:           activities.SmStatusTransferring,
						BytesTransferred: nil, // Bytes not available
					},
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Nil bytes during transferring does not cause an error; the workflow logs and continues polling until completion
		assert.NoError(t, env.GetWorkflowError())
		assert.Equal(t, 3, callCount)
		env.AssertExpectations(t)
	})

	t.Run("WhenTransferStatusIsNil_NoPanic", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

		// Register the BackupActivity
		env.RegisterActivity(&activities.BackupActivity{})

		// Create test data
		backupActivitiesContext := &activities.BackupActivitiesContext{
			Node: &coreModels.Node{},
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "sm-uuid",
			},
			SnapshotName: "test-snapshot",
		}

		// TransferStatus is nil (no status info available at all) - must not cause a nil pointer panic.
		callCount := 0
		env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.AnythingOfType("*activities.PollTransferStatusInput"), mock.AnythingOfType("time.Time")).
			Return(func(ctx context.Context, input *activities.PollTransferStatusInput, currentTime time.Time) (*activities.PollTransferStatusOutput, error) {
				callCount++
				if callCount >= 2 {
					// Eventually complete
					return &activities.PollTransferStatusOutput{
						BackupActivitiesContext: backupActivitiesContext,
						TransferComplete:        true,
						ShouldContinueAsNew:     false,
					}, nil
				}
				// Nil TransferStatus - code must guard against nil dereference
				return &activities.PollTransferStatusOutput{
					BackupActivitiesContext: backupActivitiesContext,
					TransferComplete:        false,
					ShouldContinueAsNew:     false,
					TransferStatus:          nil,
				}, nil
			})

		// Execute the workflow
		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return PollTransferStatusWithContinueAsNewCommon(ctx, backupActivitiesContext, "TestWorkflow", "arg1", "arg2")
		})

		// Nil TransferStatus must not cause a panic; the workflow should complete successfully
		assert.NoError(t, env.GetWorkflowError())
		assert.Equal(t, 2, callCount)
		env.AssertExpectations(t)
	})
}

func TestWrapErrorForChildWorkflow(t *testing.T) {
	t.Run("ReturnsNilWhenErrorIsNil", func(t *testing.T) {
		result := WrapErrorForChildWorkflow(nil)
		assert.Nil(t, result)
	})

	t.Run("WrapsRegularErrorAsTemporalApplicationError", func(t *testing.T) {
		originalErr := errors.New("test error")
		result := WrapErrorForChildWorkflow(originalErr)
		assert.NotNil(t, result)

		var appErr *temporal.ApplicationError
		assert.True(t, vsaerrors.As(result, &appErr))
		assert.Equal(t, vsaerrors.CustomErrorType, appErr.Type())
	})

	t.Run("WrapsCustomErrorPreservingTrackingID", func(t *testing.T) {
		customErr := vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, errors.New("bad request"))
		result := WrapErrorForChildWorkflow(customErr)
		assert.NotNil(t, result)

		var appErr *temporal.ApplicationError
		assert.True(t, vsaerrors.As(result, &appErr))
		assert.Equal(t, vsaerrors.CustomErrorType, appErr.Type())

		var trackingID int
		var errorDetails string
		err := appErr.Details(&trackingID, &errorDetails)
		assert.NoError(t, err)
		assert.Equal(t, vsaerrors.ErrBadRequest, trackingID)
	})

	t.Run("WrapsTemporalApplicationError", func(t *testing.T) {
		appErr := temporal.NewApplicationError("wrapped error", vsaerrors.CustomErrorType, vsaerrors.ErrResourceNotFound, "original details")
		result := WrapErrorForChildWorkflow(appErr)
		assert.NotNil(t, result)

		var extractedAppErr *temporal.ApplicationError
		assert.True(t, vsaerrors.As(result, &extractedAppErr))
		assert.Equal(t, vsaerrors.CustomErrorType, extractedAppErr.Type())
	})
}

func TestConvertToVSAError(t *testing.T) {
	t.Run("ReturnsNilWhenErrorIsNil", func(t *testing.T) {
		result := ConvertToVSAError(nil)
		assert.Nil(t, result)
	})

	t.Run("ExtractsCustomErrorFromRegularError", func(t *testing.T) {
		originalErr := errors.New("test error")
		result := ConvertToVSAError(originalErr)
		assert.NotNil(t, result)
		assert.Equal(t, vsaerrors.ErrInternalServerError, result.TrackingID)
	})

	t.Run("PreservesExistingCustomError", func(t *testing.T) {
		customErr := vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, errors.New("bad request"))
		result := ConvertToVSAError(customErr)
		assert.NotNil(t, result)
		assert.Equal(t, vsaerrors.ErrBadRequest, result.TrackingID)
	})
}

func TestExtractCustomError_AcrossTemporalBoundary(t *testing.T) {
	// Simulate what happens when an ApplicationError with CustomError details
	// goes through Temporal's failure serialization/deserialization cycle
	// (i.e. crossing the activity boundary).
	fc := temporal.GetDefaultFailureConverter()

	// Create the error exactly like WrapOntapError does in production
	classified := vsaerrors.ClassifyOntapError(errors.New("DNS server 10.0.0.1 cannot be reached"), vsaerrors.DomainDNS)
	require.Equal(t, vsaerrors.ErrDNSServerUnreachable, classified.TrackingID)

	wrapped := vsaerrors.WrapAsTemporalApplicationError(classified)

	// Step 1: Serialize to Failure proto (ErrorToFailure)
	failure := fc.ErrorToFailure(wrapped)
	require.NotNil(t, failure)
	require.NotNil(t, failure.GetApplicationFailureInfo())
	assert.Equal(t, vsaerrors.CustomErrorType, failure.GetApplicationFailureInfo().GetType())

	// Step 2: Deserialize from Failure proto (FailureToError) — this is what the workflow receives
	deserialized := fc.FailureToError(failure)
	require.NotNil(t, deserialized)

	var appErr *temporal.ApplicationError
	require.True(t, errors.As(deserialized, &appErr), "Expected ApplicationError after deserialization")
	assert.Equal(t, vsaerrors.CustomErrorType, appErr.Type())

	// Step 3: Try extracting details (this is the critical assertion)
	var trackingID int
	var errorDetails string
	err := appErr.Details(&trackingID, &errorDetails)
	assert.NoError(t, err, "Details() should decode successfully after Temporal serialization round-trip")
	assert.Equal(t, vsaerrors.ErrDNSServerUnreachable, trackingID, "TrackingID should be 5016")

	// Step 4: Verify ConvertToVSAError works on the deserialized error
	customErr := ConvertToVSAError(deserialized)
	assert.Equal(t, vsaerrors.ErrDNSServerUnreachable, customErr.TrackingID, "ConvertToVSAError should preserve tracking ID 5016 after Temporal round-trip")

	errMsg := vsaerrors.GetErrorMessageByTrackingID(customErr.TrackingID)
	assert.NotNil(t, errMsg.HttpCode)
	assert.Equal(t, 400, *errMsg.HttpCode)
}

func TestPopulateRotationRetryPolicyParams(t *testing.T) {
	// Save original values
	originalRetryInterval := RetryInterval
	originalRetryMaxInterval := RetryMaxInterval
	originalRetryBackoff := RetryBackoff

	// Restore original values after test
	defer func() {
		RetryInterval = originalRetryInterval
		RetryMaxInterval = originalRetryMaxInterval
		RetryBackoff = originalRetryBackoff
	}()

	t.Run("WhenAllParamsValid_ThenReturnRetryPolicy", func(tt *testing.T) {
		RetryInterval = "5s"
		RetryMaxInterval = "5m"
		RetryBackoff = "2.0"

		policy, err := PopulateRotationRetryPolicyParams()
		assert.NoError(tt, err)
		assert.NotNil(tt, policy)
		assert.Equal(tt, 5*time.Minute, policy.StartToCloseTimeout)
		assert.Equal(tt, 5*time.Second, policy.InitialInterval)
		assert.Equal(tt, 5*time.Minute, policy.MaximumInterval)
		assert.Equal(tt, 2.0, policy.BackoffCoefficient)
		assert.Equal(tt, 2, policy.MaximumAttempts)
	})

	t.Run("WhenRetryIntervalInvalid_ThenReturnError", func(tt *testing.T) {
		RetryInterval = "invalid"
		RetryMaxInterval = "5m"
		RetryBackoff = "2.0"

		policy, err := PopulateRotationRetryPolicyParams()
		assert.Error(tt, err)
		assert.Nil(tt, policy)
	})

	t.Run("WhenRetryMaxIntervalInvalid_ThenReturnError", func(tt *testing.T) {
		RetryInterval = "5s"
		RetryMaxInterval = "invalid"
		RetryBackoff = "2.0"

		policy, err := PopulateRotationRetryPolicyParams()
		assert.Error(tt, err)
		assert.Nil(tt, policy)
	})

	t.Run("WhenRetryBackoffInvalid_ThenReturnError", func(tt *testing.T) {
		RetryInterval = "5s"
		RetryMaxInterval = "5m"
		RetryBackoff = "invalid"

		policy, err := PopulateRotationRetryPolicyParams()
		assert.Error(tt, err)
		assert.Nil(tt, policy)
	})
}

func TestFetchAndSetAuthToken(t *testing.T) {
	t.Run("WhenLocalEnv_ThenReturnsOriginalContext", func(tt *testing.T) {
		// Save original IsLocalEnv and restore after test
		originalIsLocalEnv := env.IsLocalEnv
		defer func() { env.IsLocalEnv = originalIsLocalEnv }()

		// Set IsLocalEnv to return true
		env.IsLocalEnv = func() bool { return true }

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(testFetchAndSetAuthTokenWorkflow)

		env.ExecuteWorkflow(testFetchAndSetAuthTokenWorkflow, "test-account")

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("WhenNotLocalEnv_ThenFetchesToken", func(tt *testing.T) {
		// Save original IsLocalEnv and restore after test
		originalIsLocalEnv := env.IsLocalEnv
		defer func() { env.IsLocalEnv = originalIsLocalEnv }()

		// Set IsLocalEnv to return false
		env.IsLocalEnv = func() bool { return false }

		var ts testsuite.WorkflowTestSuite
		testEnv := ts.NewTestWorkflowEnvironment()
		testEnv.RegisterWorkflow(testFetchAndSetAuthTokenWorkflow)
		testEnv.RegisterActivity(&activities.CommonActivities{})

		testEnv.OnActivity("GetAuthJWTToken", mock.Anything, "test-account").Return("test-jwt-token", nil)

		testEnv.ExecuteWorkflow(testFetchAndSetAuthTokenWorkflow, "test-account")

		assert.True(tt, testEnv.IsWorkflowCompleted())
		assert.NoError(tt, testEnv.GetWorkflowError())
	})

	t.Run("WhenTokenFetchFails_ThenReturnsError", func(tt *testing.T) {
		// Save original IsLocalEnv and restore after test
		originalIsLocalEnv := env.IsLocalEnv
		defer func() { env.IsLocalEnv = originalIsLocalEnv }()

		// Set IsLocalEnv to return false
		env.IsLocalEnv = func() bool { return false }

		var ts testsuite.WorkflowTestSuite
		testEnv := ts.NewTestWorkflowEnvironment()
		testEnv.RegisterWorkflow(testFetchAndSetAuthTokenWorkflow)
		testEnv.RegisterActivity(&activities.CommonActivities{})

		testEnv.OnActivity("GetAuthJWTToken", mock.Anything, "test-account").Return("", errors.New("token fetch failed"))

		testEnv.ExecuteWorkflow(testFetchAndSetAuthTokenWorkflow, "test-account")

		assert.True(tt, testEnv.IsWorkflowCompleted())
		assert.Error(tt, testEnv.GetWorkflowError())
	})

	t.Run("WhenTokenFetchFailsAndLoggerNil_ThenReturnsError", func(tt *testing.T) {
		originalIsLocalEnv := env.IsLocalEnv
		defer func() { env.IsLocalEnv = originalIsLocalEnv }()
		env.IsLocalEnv = func() bool { return false }

		var ts testsuite.WorkflowTestSuite
		testEnv := ts.NewTestWorkflowEnvironment()
		testEnv.RegisterWorkflow(testFetchAndSetAuthTokenWorkflowNilLogger)
		testEnv.RegisterActivity(&activities.CommonActivities{})
		testEnv.OnActivity("GetAuthJWTToken", mock.Anything, "test-account").Return("", errors.New("token fetch failed"))

		testEnv.ExecuteWorkflow(testFetchAndSetAuthTokenWorkflowNilLogger, "test-account")

		assert.True(tt, testEnv.IsWorkflowCompleted())
		assert.Error(tt, testEnv.GetWorkflowError())
	})
}

// testFetchAndSetAuthTokenWorkflow is a test workflow that calls FetchAndSetAuthToken
func testFetchAndSetAuthTokenWorkflow(ctx workflow.Context, accountId string) error {
	logger := util.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	_, err := FetchAndSetAuthToken(ctx, accountId, logger)
	return err
}

func testFetchAndSetAuthTokenWorkflowNilLogger(ctx workflow.Context, accountId string) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	_, err := FetchAndSetAuthToken(ctx, accountId, nil)
	return err
}

func TestWaitForONTAPJob(t *testing.T) {
	t.Run("WhenJobResponseIsNil_ReturnsNil", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			return WaitForONTAPJob(ctx, nil, &coreModels.Node{EndpointAddress: "127.0.0.1"}, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.NoError(tt, env.GetWorkflowError())
	})

	t.Run("WhenJobResponseHasEmptyJobUUID_ReturnsNil", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			jobResponse := &vsa.OntapAsyncResponse{JobUUID: ""}
			return WaitForONTAPJob(ctx, jobResponse, &coreModels.Node{EndpointAddress: "127.0.0.1"}, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.NoError(tt, env.GetWorkflowError())
	})

	t.Run("WhenJobCompletesSuccessfully_ReturnsNil", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		env.RegisterActivity(commonActivity.GetOntapJob)

		jobResponse := &vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}
		node := &coreModels.Node{EndpointAddress: "127.0.0.1"}

		// Mock the activity to return success
		env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", node).Return(&vsa.OntapJob{
			UUID:  "test-job-uuid",
			State: "success",
		}, nil)

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			ao := workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			}
			ctx = workflow.WithActivityOptions(ctx, ao)
			return WaitForONTAPJob(ctx, jobResponse, node, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.NoError(tt, env.GetWorkflowError())
	})

	t.Run("WhenJobFailsWithError_ReturnsError", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		env.RegisterActivity(commonActivity.GetOntapJob)

		jobResponse := &vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}
		node := &coreModels.Node{EndpointAddress: "127.0.0.1"}

		errorMessage := "Job execution failed"
		errorCode := "500"
		// Mock the activity to return failure with error
		env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", node).Return(&vsa.OntapJob{
			UUID:  "test-job-uuid",
			State: "failure",
			Error: &vsa.OntapError{
				Message: errorMessage,
				Code:    errorCode,
			},
		}, nil)

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			ao := workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			}
			ctx = workflow.WithActivityOptions(ctx, ao)
			return WaitForONTAPJob(ctx, jobResponse, node, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.Error(tt, env.GetWorkflowError())
		assert.Contains(tt, env.GetWorkflowError().Error(), "Job execution failed")
	})

	t.Run("WhenJobFailsWithoutErrorMessage_ReturnsError", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		env.RegisterActivity(commonActivity.GetOntapJob)

		jobResponse := &vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}
		node := &coreModels.Node{EndpointAddress: "127.0.0.1"}

		// Mock the activity to return failure without error message
		env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", node).Return(&vsa.OntapJob{
			UUID:  "test-job-uuid",
			State: "failure",
			Error: nil,
		}, nil)

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			ao := workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			}
			ctx = workflow.WithActivityOptions(ctx, ao)
			return WaitForONTAPJob(ctx, jobResponse, node, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.Error(tt, env.GetWorkflowError())
		assert.Contains(tt, env.GetWorkflowError().Error(), "ontap job test-job-uuid failed with no error message")
	})

	t.Run("WhenGetOntapJobActivityFails_ReturnsError", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		env.RegisterActivity(commonActivity.GetOntapJob)

		jobResponse := &vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}
		node := &coreModels.Node{EndpointAddress: "127.0.0.1"}

		// Mock the activity to return an error
		env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", node).Return(nil, errors.New("failed to get ONTAP job"))

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			ao := workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			}
			ctx = workflow.WithActivityOptions(ctx, ao)
			return WaitForONTAPJob(ctx, jobResponse, node, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.Error(tt, env.GetWorkflowError())
		assert.Contains(tt, env.GetWorkflowError().Error(), "failed to get ONTAP job")
	})

	t.Run("WhenJobIsStillRunning_ThenSucceeds_ReturnsNil", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		env.RegisterActivity(commonActivity.GetOntapJob)

		jobResponse := &vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}
		node := &coreModels.Node{EndpointAddress: "127.0.0.1"}

		// First call returns running state, second call returns success
		env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", node).Return(&vsa.OntapJob{
			UUID:  "test-job-uuid",
			State: "running",
		}, nil).Once()

		env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", node).Return(&vsa.OntapJob{
			UUID:  "test-job-uuid",
			State: "success",
		}, nil).Once()

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			ao := workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			}
			ctx = workflow.WithActivityOptions(ctx, ao)
			return WaitForONTAPJob(ctx, jobResponse, node, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.NoError(tt, env.GetWorkflowError())
	})

	t.Run("WhenSleepFails_ReturnsError", func(tt *testing.T) {
		var suite testsuite.WorkflowTestSuite
		env := suite.NewTestWorkflowEnvironment()
		commonActivity := &activities.CommonActivities{}
		env.RegisterActivity(commonActivity.GetOntapJob)

		jobResponse := &vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}
		node := &coreModels.Node{EndpointAddress: "127.0.0.1"}

		// Mock the activity to return running state to trigger sleep
		env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "test-job-uuid", node).Return(&vsa.OntapJob{
			UUID:  "test-job-uuid",
			State: "running",
		}, nil)

		// Set up workflow to fail on sleep by cancelling the context
		env.RegisterDelayedCallback(func() {
			env.CancelWorkflow()
		}, 100*time.Millisecond)

		env.ExecuteWorkflow(func(ctx workflow.Context) error {
			ao := workflow.ActivityOptions{
				StartToCloseTimeout: 30 * time.Second,
				RetryPolicy: &temporal.RetryPolicy{
					NonRetryableErrorTypes: []string{"PanicError"},
				},
			}
			ctx = workflow.WithActivityOptions(ctx, ao)
			return WaitForONTAPJob(ctx, jobResponse, node, 5*time.Minute)
		})

		require.True(tt, env.IsWorkflowCompleted())
		require.Error(tt, env.GetWorkflowError())
	})
}
