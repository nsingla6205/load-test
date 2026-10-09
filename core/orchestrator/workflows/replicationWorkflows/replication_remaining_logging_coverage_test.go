package replicationWorkflows

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func newRemainingReplicationLoggingEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var testSuite testsuite.WorkflowTestSuite
	env := testSuite.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, err := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	assert.NoError(t, err)
	env.SetHeader(&commonpb.Header{
		Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
	})
	return env
}

func TestGetMultipleReplicationsInternalWorkflow_DoneStatusUpdateFails(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	activity := replicationActivities.ReplicationInternalGetMultipleActivity{SE: mockStorage}
	commonActivity := activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(activity.GetReplicationsFromDB)
	env.RegisterActivity(activity.GetNodesForPools)
	env.RegisterActivity(activity.GetReplicationsFromOntap)
	env.RegisterActivity(activity.UpdateReplicationsInDB)
	env.RegisterActivity(commonActivity.UpdateJobStatus)

	params := &common.ReplicationInternalGetMultipleParams{AccountName: "deathstar"}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity("GetReplicationsFromDB", mock.Anything, params).Return(params, nil)
	env.OnActivity("GetNodesForPools", mock.Anything, params).Return(params, nil)
	env.OnActivity("GetReplicationsFromOntap", mock.Anything, params).Return(params, nil)
	env.OnActivity("UpdateReplicationsInDB", mock.Anything, params).Return(nil)

	env.ExecuteWorkflow(GetMultipleReplicationsInternalWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestGetMultipleReplicationsInternalWorkflow_RunAndErrorStatusUpdateFail(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	activity := replicationActivities.ReplicationInternalGetMultipleActivity{SE: mockStorage}
	commonActivity := activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(activity.GetReplicationsFromDB)
	env.RegisterActivity(commonActivity.UpdateJobStatus)

	params := &common.ReplicationInternalGetMultipleParams{AccountName: "deathstar"}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity(activity.GetReplicationsFromDB, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	env.ExecuteWorkflow(GetMultipleReplicationsInternalWorkflow, params)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestPerformMountCheckWorkflow_ProcessingStatusUpdateFails(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)

	env.ExecuteWorkflow(PerformMountCheckWorkflow, "replication-uuid", "testAccount")
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestUpdateInternalVolumeReplicationWorkflow_ProcessingStatusUpdateFails(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)

	params := &common.UpdateVolumeReplicationInternalParams{AccountName: "test-account"}
	replication := &datamodel.VolumeReplication{
		Volume: &datamodel.Volume{
			Pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
		},
	}
	env.ExecuteWorkflow(UpdateInternalVolumeReplicationWorkflow, params, replication)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestUpdateInternalVolumeReplicationWorkflow_RunAndErrorStatusUpdateFail(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.GetNode)

	params := &common.UpdateVolumeReplicationInternalParams{AccountName: "test-account"}
	replication := &datamodel.VolumeReplication{
		Volume: &datamodel.Volume{
			Pool: &datamodel.Pool{PoolCredentials: &datamodel.PoolCredentials{Password: "password"}},
		},
	}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	env.ExecuteWorkflow(UpdateInternalVolumeReplicationWorkflow, params, replication)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestUpdateVolumeReplicationAttributesWorkflow_DoneStatusUpdateFails(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
	commonActivity := activities.CommonActivities{SE: mockStorage}
	env.RegisterActivity(updateAttrActivity.UpdateSrcVolumeReplication)
	env.RegisterActivity(updateAttrActivity.UpdateVolumeTypeActivity)
	env.RegisterActivity(commonActivity.UpdateJobStatus)

	params := &common.UpdateVolumeReplicationAttributesParams{AccountName: "test-account"}
	event := &replication.UpdateVolumeReplicationAttributesEvent{
		UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
			VolumeReplicationInternal: &gcpgenserver.VolumeReplicationInternalV1beta{EndpointType: "src"},
		},
	}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity("UpdateSrcVolumeReplication", mock.Anything, mock.Anything).Return(&replication.UpdateVolumeReplicationAttributesResult{Event: event}, nil)
	env.OnActivity("UpdateVolumeTypeActivity", mock.Anything, mock.Anything).Return(nil)

	env.ExecuteWorkflow(UpdateVolumeReplicationAttributesWorkflow, params, event)
	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
}

func TestHybridDeleteDestinationVolumeWorkflow_ErrorStatusUpdateFails(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := replicationActivities.DeleteVolumeReplicationActivity{SE: mockStorage}
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(deleteActivity.DeleteVolumeOnDestination)

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity(deleteActivity.DeleteVolumeOnDestination, mock.Anything, mock.Anything).Return(nil, assert.AnError)

	env.ExecuteWorkflow(HybridDeleteDestinationVolumeWorkflow, replication.DeleteReplicationResult{})

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestHybridDeleteDestinationVolumeWorkflow_DoneStatusUpdateFails(t *testing.T) {
	env := newRemainingReplicationLoggingEnv(t)
	mockStorage := database.NewMockStorage(t)
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := replicationActivities.DeleteVolumeReplicationActivity{SE: mockStorage}
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(deleteActivity.DeleteVolumeOnDestination)
	env.RegisterActivity(deleteActivity.DescribeRemoteJobForDelete)
	env.RegisterActivity(deleteActivity.DeHydrateDestinationVolume)

	result := replication.DeleteReplicationResult{}
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(assert.AnError)
	env.OnActivity(deleteActivity.DeleteVolumeOnDestination, mock.Anything, mock.Anything).Return(&result, nil)
	env.OnActivity(deleteActivity.DescribeRemoteJobForDelete, mock.Anything, mock.Anything).Return(nil)
	env.OnActivity(deleteActivity.DeHydrateDestinationVolume, mock.Anything, mock.Anything).Return(&result, nil)

	env.ExecuteWorkflow(HybridDeleteDestinationVolumeWorkflow, result)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
