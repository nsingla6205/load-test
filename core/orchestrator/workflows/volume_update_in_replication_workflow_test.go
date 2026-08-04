package workflows

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type UpdateVolumeInReplicationTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *UpdateVolumeInReplicationTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)
}

func TestUpdateVolumeInReplicationSuite(t *testing.T) {
	suite.Run(t, new(UpdateVolumeInReplicationTestSuite))
}

func (s *UpdateVolumeInReplicationTestSuite) Test_Success() {
	mockStorage := database.NewMockStorage(s.T())
	updateActivity := &activities.UpdateVolumeInReplicationActivity{}
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
	heartbeatTimeout := captureActivityHeartbeatTimeout(s.env, "GetReplicationFromDBVolume")
	expectedPolicy, policyErr := PopulateRetryPolicyParams()
	s.Require().NoError(policyErr)

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(updateActivity.GetReplicationFromDBVolume)
	s.env.RegisterActivity(updateActivity.GetLocalBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetRemoteBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetSignedLocalTokenVolume)
	s.env.RegisterActivity(updateActivity.GetSignedRemoteTokenVolume)
	s.env.RegisterActivity(updateActivity.GetReplicationMirrorState)
	s.env.RegisterActivity(updateActivity.GetRemotePoolDetailsVolume)
	s.env.RegisterActivity(updateActivity.ValidateRemoteVolumeUpdate)
	s.env.RegisterActivity(updateActivity.UpdateRemoteVolume)
	s.env.RegisterActivity(updateActivity.DescribeRemoteJobVolumeUpdate)
	s.env.RegisterActivity(updateActivity.CreateJobForChildWorkflow)
	s.env.RegisterWorkflow(UpdateVolumeWorkflow)

	params := &common.UpdateVolumeParams{}
	volume := &datamodel.Volume{}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("GetReplicationFromDBVolume", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetLocalBasePathVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetRemoteBasePathVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetSignedLocalTokenVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetSignedRemoteTokenVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetReplicationMirrorState", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetRemotePoolDetailsVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("ValidateRemoteVolumeUpdate", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("UpdateRemoteVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("DescribeRemoteJobVolumeUpdate", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("CreateJobForChildWorkflow", mock.Anything, mock.Anything).Return(&datamodel.Job{WorkflowID: "child-workflow-id"}, nil)
	s.env.OnWorkflow("UpdateVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateVolumeInReplicationWorkflow, params, volume)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	assert.Equal(s.T(), expectedPolicy.HeartBeatTimeout, *heartbeatTimeout)
	mockStorage.AssertExpectations(s.T())
}

func (s *UpdateVolumeInReplicationTestSuite) Test_Failure_SetsVolumeStateToReady() {
	mockStorage := database.NewMockStorage(s.T())
	updateActivity := &activities.UpdateVolumeInReplicationActivity{}
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(updateActivity.GetReplicationFromDBVolume)
	s.env.RegisterActivity(updateActivity.GetLocalBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetRemoteBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetSignedLocalTokenVolume)
	s.env.RegisterActivity(updateActivity.GetSignedRemoteTokenVolume)
	s.env.RegisterActivity(updateActivity.GetReplicationMirrorState)
	s.env.RegisterActivity(updateActivity.GetRemotePoolDetailsVolume)
	s.env.RegisterActivity(updateActivity.ValidateRemoteVolumeUpdate)
	s.env.RegisterActivity(updateActivity.UpdateRemoteVolume)
	s.env.RegisterActivity(updateActivity.DescribeRemoteJobVolumeUpdate)
	s.env.RegisterActivity(updateActivity.CreateJobForChildWorkflow)
	s.env.RegisterWorkflow(UpdateVolumeWorkflow)

	params := &common.UpdateVolumeParams{}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
	}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// First activity fails, triggering the deferred error handler
	s.env.OnActivity("GetReplicationFromDBVolume", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("replication lookup failed"))

	// Verify that the deferred handler sets state to READY (not Error)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "test-volume-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

	s.env.ExecuteWorkflow(UpdateVolumeInReplicationWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

func (s *UpdateVolumeInReplicationTestSuite) Test_Failure_ChildWorkflow_SetsVolumeStateToReady() {
	mockStorage := database.NewMockStorage(s.T())
	updateActivity := &activities.UpdateVolumeInReplicationActivity{}
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(updateActivity.GetReplicationFromDBVolume)
	s.env.RegisterActivity(updateActivity.GetLocalBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetRemoteBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetSignedLocalTokenVolume)
	s.env.RegisterActivity(updateActivity.GetSignedRemoteTokenVolume)
	s.env.RegisterActivity(updateActivity.GetReplicationMirrorState)
	s.env.RegisterActivity(updateActivity.GetRemotePoolDetailsVolume)
	s.env.RegisterActivity(updateActivity.ValidateRemoteVolumeUpdate)
	s.env.RegisterActivity(updateActivity.UpdateRemoteVolume)
	s.env.RegisterActivity(updateActivity.DescribeRemoteJobVolumeUpdate)
	s.env.RegisterActivity(updateActivity.CreateJobForChildWorkflow)
	s.env.RegisterWorkflow(UpdateVolumeWorkflow)

	params := &common.UpdateVolumeParams{}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
	}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity("GetReplicationFromDBVolume", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetLocalBasePathVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetRemoteBasePathVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetSignedLocalTokenVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetSignedRemoteTokenVolume", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("GetReplicationMirrorState", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity("CreateJobForChildWorkflow", mock.Anything, mock.Anything).Return(&datamodel.Job{WorkflowID: "child-workflow-id"}, nil)

	// Child workflow fails
	s.env.OnWorkflow("UpdateVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("child workflow failed"))

	// Verify that the deferred handler sets state to READY
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "test-volume-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(nil)

	s.env.ExecuteWorkflow(UpdateVolumeInReplicationWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

func (s *UpdateVolumeInReplicationTestSuite) Test_Failure_UpdateVolumeStateInDB_Fails() {
	mockStorage := database.NewMockStorage(s.T())
	updateActivity := &activities.UpdateVolumeInReplicationActivity{}
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(updateActivity.GetReplicationFromDBVolume)
	s.env.RegisterActivity(updateActivity.GetLocalBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetRemoteBasePathVolume)
	s.env.RegisterActivity(updateActivity.GetSignedLocalTokenVolume)
	s.env.RegisterActivity(updateActivity.GetSignedRemoteTokenVolume)
	s.env.RegisterActivity(updateActivity.GetReplicationMirrorState)
	s.env.RegisterActivity(updateActivity.GetRemotePoolDetailsVolume)
	s.env.RegisterActivity(updateActivity.ValidateRemoteVolumeUpdate)
	s.env.RegisterActivity(updateActivity.UpdateRemoteVolume)
	s.env.RegisterActivity(updateActivity.DescribeRemoteJobVolumeUpdate)
	s.env.RegisterActivity(updateActivity.CreateJobForChildWorkflow)
	s.env.RegisterWorkflow(UpdateVolumeWorkflow)

	params := &common.UpdateVolumeParams{}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
	}

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Activity fails
	s.env.OnActivity("GetReplicationFromDBVolume", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("replication lookup failed"))

	// UpdateVolumeStateInDB also fails — workflow should still complete with the original error
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, "test-volume-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Return(errors.New("db update failed"))

	s.env.ExecuteWorkflow(UpdateVolumeInReplicationWorkflow, params, volume)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	mockStorage.AssertExpectations(s.T())
}

func captureActivityHeartbeatTimeout(env *testsuite.TestWorkflowEnvironment, activityName string) *time.Duration {
	var heartbeatTimeout time.Duration
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		if info.ActivityType.Name == activityName {
			heartbeatTimeout = info.HeartbeatTimeout
		}
	})
	return &heartbeatTimeout
}
