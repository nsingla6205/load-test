package replicationWorkflows

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestUpdateVolumeReplicationAttributesWorkflow(t *testing.T) {
	t.Run("TestUpdateVolumeReplicationAttributesWorkflow", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(updateAttrActivity.GetSnapmirrorDetailsFromOntap)
		env.RegisterActivity(updateAttrActivity.UpdateDstVolumeReplication)
		env.RegisterActivity(updateAttrActivity.UpdateVolumeTypeActivity)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.UpdateVolumeReplicationAttributesParams{
			AccountName: "test-account",
		}

		event := &replication.UpdateVolumeReplicationAttributesEvent{
			UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
				VolumeReplicationInternal: &gcpgenserver.VolumeReplicationInternalV1beta{
					EndpointType: "dst",
				},
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorDetailsFromOntap", mock.Anything, mock.Anything).Return(&replication.UpdateVolumeReplicationAttributesResult{Event: event}, nil)
		env.OnActivity("UpdateDstVolumeReplication", mock.Anything, mock.Anything).Return(&replication.UpdateVolumeReplicationAttributesResult{Event: event}, nil)
		env.OnActivity("UpdateVolumeTypeActivity", mock.Anything, mock.Anything).Return(nil)

		heartbeatTimeout := captureActivityHeartbeatTimeout(env, "GetSnapmirrorDetailsFromOntap")
		expectedPolicy, policyErr := workflows.PopulateRetryPolicyParams()
		assert.NoError(tt, policyErr)

		env.ExecuteWorkflow(UpdateVolumeReplicationAttributesWorkflow, params, event)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		assert.Equal(tt, expectedPolicy.HeartBeatTimeout, *heartbeatTimeout)
	})
	t.Run("TestUpdateVolumeReplicationAttributesWorkflowWhenSrcEndpoint", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(updateAttrActivity.UpdateSrcVolumeReplication)
		env.RegisterActivity(updateAttrActivity.UpdateVolumeTypeActivity)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.UpdateVolumeReplicationAttributesParams{
			AccountName: "test-account",
		}

		event := &replication.UpdateVolumeReplicationAttributesEvent{
			UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
				VolumeReplicationInternal: &gcpgenserver.VolumeReplicationInternalV1beta{
					EndpointType: "src",
				},
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateSrcVolumeReplication", mock.Anything, mock.Anything).Return(&replication.UpdateVolumeReplicationAttributesResult{Event: event}, nil)
		env.OnActivity("UpdateVolumeTypeActivity", mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(UpdateVolumeReplicationAttributesWorkflow, params, event)

		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("TestUpdateVolumeReplicationAttributesWorkflow_GetSnapmirrorDetailsError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(updateAttrActivity.GetSnapmirrorDetailsFromOntap)
		env.RegisterActivity(updateAttrActivity.UpdateDstVolumeReplication)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.UpdateVolumeReplicationAttributesParams{
			AccountName: "test-account",
		}

		event := &replication.UpdateVolumeReplicationAttributesEvent{
			UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
				VolumeReplicationInternal: &gcpgenserver.VolumeReplicationInternalV1beta{
					EndpointType: "dst",
				},
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorDetailsFromOntap", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		env.ExecuteWorkflow(UpdateVolumeReplicationAttributesWorkflow, params, event)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("TestUpdateVolumeReplicationAttributesWorkflow_UpdateReplicationTableEntriesError", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		mockStorage := database.NewMockStorage(tt)
		updateAttrActivity := replicationActivities.UpdateVolumeReplicationAttributesActivity{SE: mockStorage}
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(updateAttrActivity.GetSnapmirrorDetailsFromOntap)
		env.RegisterActivity(updateAttrActivity.UpdateDstVolumeReplication)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.UpdateVolumeReplicationAttributesParams{
			AccountName: "test-account",
		}

		event := &replication.UpdateVolumeReplicationAttributesEvent{
			UpdateVolumeReplicationAttributesParams: &models.UpdateVolumeReplicationAttributesParams{
				VolumeReplicationInternal: &gcpgenserver.VolumeReplicationInternalV1beta{
					EndpointType: "dst",
				},
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorDetailsFromOntap", mock.Anything, mock.Anything).Return(&replication.UpdateVolumeReplicationAttributesResult{Event: event}, nil)
		env.OnActivity("UpdateDstVolumeReplication", mock.Anything, mock.Anything).Return(nil, assert.AnError)

		env.ExecuteWorkflow(UpdateVolumeReplicationAttributesWorkflow, params, event)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})
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
