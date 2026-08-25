package expertMode

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func testFlexCloneSplitVolume() *datamodel.ExpertModeVolumes {
	return &datamodel.ExpertModeVolumes{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 1},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
		},
	}
}

func newFlexCloneSplitTestEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, *database.MockStorage) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	env.SetHeader(mockHeader)

	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}

	env.RegisterActivity(commonActivity.GetNode)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(expertModeActivity.WaitForExpertModeFlexCloneSplitComplete)
	env.RegisterActivity(expertModeActivity.RecoverExpertModeVolumeAfterFlexCloneSplitFailure)
	env.RegisterActivity(expertModeActivity.CompleteExpertModeFlexCloneSplitInDB)

	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	env.OnActivity("GetNode", mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	return env, mockStorage
}

func TestExpertModeFlexCloneSplitWorkflow(t *testing.T) {
	t.Run("SplitCompletes_JobDone", func(tt *testing.T) {
		env, mockStorage := newFlexCloneSplitTestEnv(tt)
		volume := testFlexCloneSplitVolume()

		var statuses []string
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				statuses = append(statuses, args.String(2))
			}).
			Return(nil).Twice()

		env.OnActivity("WaitForExpertModeFlexCloneSplitComplete", mock.Anything, mock.Anything, mock.Anything).Return(int64(2048), nil).Once()
		env.OnActivity("CompleteExpertModeFlexCloneSplitInDB", mock.Anything, "test-volume-uuid", int64(2048)).Return(nil).Once()
		heartbeatTimeouts := captureActivityHeartbeatTimeouts(env,
			"WaitForExpertModeFlexCloneSplitComplete",
			"CompleteExpertModeFlexCloneSplitInDB",
		)

		env.ExecuteWorkflow(ExpertModeFlexCloneSplitWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		assert.Equal(tt, []string{string(datamodel.JobsStatePROCESSING), string(datamodel.JobsStateDONE)}, statuses)
		expectedHeartbeat := time.Duration(VolumeReconciliationExpertModeStartToCloseTimeoutSec) * time.Second / 2
		assert.Equal(tt, expectedHeartbeat, heartbeatTimeouts["WaitForExpertModeFlexCloneSplitComplete"])
		assert.Equal(tt, expectedHeartbeat, heartbeatTimeouts["CompleteExpertModeFlexCloneSplitInDB"])
		env.AssertNotCalled(tt, "RecoverExpertModeVolumeAfterFlexCloneSplitFailure", mock.Anything, mock.Anything, mock.Anything)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SplitAborted_NonRetryable_RecoversAndJobError", func(tt *testing.T) {
		env, mockStorage := newFlexCloneSplitTestEnv(tt)
		volume := testFlexCloneSplitVolume()

		var statuses []string
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				statuses = append(statuses, args.String(2))
			}).
			Return(nil).Twice()

		abortErr := temporal.NewNonRetryableApplicationError("split aborted", "FlexCloneSplitAborted", nil)
		env.OnActivity("WaitForExpertModeFlexCloneSplitComplete", mock.Anything, mock.Anything, mock.Anything).Return(int64(0), abortErr).Once()
		env.OnActivity("RecoverExpertModeVolumeAfterFlexCloneSplitFailure", mock.Anything, volume, mock.Anything).Return(nil).Once()
		recoveryHeartbeatTimeout := captureActivityHeartbeatTimeout(env, "RecoverExpertModeVolumeAfterFlexCloneSplitFailure")

		env.ExecuteWorkflow(ExpertModeFlexCloneSplitWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		assert.Equal(tt, []string{string(datamodel.JobsStatePROCESSING), string(datamodel.JobsStateERROR)}, statuses)
		assert.Equal(tt, 2*time.Minute, *recoveryHeartbeatTimeout)
		env.AssertNotCalled(tt, "CompleteExpertModeFlexCloneSplitInDB", mock.Anything, mock.Anything, mock.Anything)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PollRetriesUntilCompletion", func(tt *testing.T) {
		env, mockStorage := newFlexCloneSplitTestEnv(tt)
		volume := testFlexCloneSplitVolume()

		var statuses []string
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				statuses = append(statuses, args.String(2))
			}).
			Return(nil).Twice()

		pendingErr := temporal.NewApplicationError("split pending", "FlexCloneSplitPending")
		env.OnActivity("WaitForExpertModeFlexCloneSplitComplete", mock.Anything, mock.Anything, mock.Anything).Return(int64(0), pendingErr).Twice()
		env.OnActivity("WaitForExpertModeFlexCloneSplitComplete", mock.Anything, mock.Anything, mock.Anything).Return(int64(3072), nil).Once()
		env.OnActivity("CompleteExpertModeFlexCloneSplitInDB", mock.Anything, "test-volume-uuid", int64(3072)).Return(nil).Once()

		env.ExecuteWorkflow(ExpertModeFlexCloneSplitWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		assert.Equal(tt, []string{string(datamodel.JobsStatePROCESSING), string(datamodel.JobsStateDONE)}, statuses)
		env.AssertNumberOfCalls(tt, "WaitForExpertModeFlexCloneSplitComplete", 3)
		env.AssertNotCalled(tt, "RecoverExpertModeVolumeAfterFlexCloneSplitFailure", mock.Anything, mock.Anything, mock.Anything)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CompleteSplitInDBFails_ReturnsErrorAndMarksJobError", func(tt *testing.T) {
		env, mockStorage := newFlexCloneSplitTestEnv(tt)
		volume := testFlexCloneSplitVolume()

		var statuses []string
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				statuses = append(statuses, args.String(2))
			}).
			Return(nil).Twice()

		env.OnActivity("WaitForExpertModeFlexCloneSplitComplete", mock.Anything, mock.Anything, mock.Anything).Return(int64(2048), nil).Once()
		env.OnActivity("CompleteExpertModeFlexCloneSplitInDB", mock.Anything, "test-volume-uuid", int64(2048)).Return(errors.New("persist failed")).Once()

		env.ExecuteWorkflow(ExpertModeFlexCloneSplitWorkflow, volume)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		assert.Equal(tt, []string{string(datamodel.JobsStatePROCESSING), string(datamodel.JobsStateERROR)}, statuses)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

// newFlexCloneSplitBareEnv builds a flexclone-split environment without the auto-success GetNode /
// UpdateJob mocks so the job-status and GetNode failure branches can be exercised directly.
func newFlexCloneSplitBareEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, activities.CommonActivities) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})

	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
	env.RegisterActivity(commonActivity.GetNode)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(expertModeActivity.WaitForExpertModeFlexCloneSplitComplete)
	env.RegisterActivity(expertModeActivity.RecoverExpertModeVolumeAfterFlexCloneSplitFailure)
	env.RegisterActivity(expertModeActivity.CompleteExpertModeFlexCloneSplitInDB)

	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	return env, commonActivity
}

func TestExpertModeFlexCloneSplitWorkflow_ProcessingUpdateFailsAndErrorUpdateFails(t *testing.T) {
	env, commonActivity := newFlexCloneSplitBareEnv(t)
	// Both PROCESSING and the best-effort ERROR transition fail.
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).
		Return(temporal.NewNonRetryableApplicationError("update job status failed", "UpdateFailed", nil)).Maybe()

	env.ExecuteWorkflow(ExpertModeFlexCloneSplitWorkflow, testFlexCloneSplitVolume())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestExpertModeFlexCloneSplitWorkflow_GetNodeFails(t *testing.T) {
	env, commonActivity := newFlexCloneSplitBareEnv(t)
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil).Maybe()
	env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return(([]*datamodel.Node)(nil), temporal.NewNonRetryableApplicationError("get node failed", "GetNodeFailed", nil)).Maybe()

	env.ExecuteWorkflow(ExpertModeFlexCloneSplitWorkflow, testFlexCloneSplitVolume())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestExpertModeFlexCloneSplitWorkflow_RunFailsAndErrorUpdateFails(t *testing.T) {
	env, commonActivity := newFlexCloneSplitBareEnv(t)
	// PROCESSING succeeds; the best-effort ERROR transition after Run fails.
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State == string(datamodel.JobsStateERROR)
	})).Return(temporal.NewNonRetryableApplicationError("update job status failed", "UpdateFailed", nil)).Maybe()
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
		return job.State != string(datamodel.JobsStateERROR)
	})).Return(nil).Maybe()
	// Run fails at GetNode.
	env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return(([]*datamodel.Node)(nil), temporal.NewNonRetryableApplicationError("get node failed", "GetNodeFailed", nil)).Maybe()

	env.ExecuteWorkflow(ExpertModeFlexCloneSplitWorkflow, testFlexCloneSplitVolume())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}
