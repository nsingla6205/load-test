package replicationWorkflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestReplicationCleanupWorkflow(t *testing.T) {
	t.Run("TestReplicationCleanupWorkflow", func(tt *testing.T) {
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
		commonActivity := activities.CommonActivities{SE: mockStorage}
		resumeReplicationActivity := replicationActivities.CleanupVolumeReplicationActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(resumeReplicationActivity.GetSrcBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDstBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedSrcTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedDstTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.StopReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeRemoteJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnSourceForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeSourceJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeReplicationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteVolumeOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationOnDestinationToErrorStateForCleanup)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.DeleteReplicationParams{}

		event := &replication.DeleteReplicationEvent{
			CommonReplicationEventParams: replication.CommonReplicationEventParams{
				ReplicationModel: &datamodel.VolumeReplication{},
			},
		}

		replicationResult := &replication.DeleteReplicationResult{
			SrcProjectNumber: &event.SourceProjectNumber,
			DstProjectNumber: &event.DestinationProjectNumber,
			Event:            event,
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				Name:             googleproxyclient.NewOptString("repl-123"),
				LastTransferSize: googleproxyclient.NewOptInt64(100),
				MirrorState:      googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING),
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeRemoteJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DeleteSnapmirrorSnapshotsOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnSourceForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeSourceJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeHydrateDestinationVolumeReplicationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDestinationVolumeForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DeleteVolumeOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DeHydrateDestinationVolumeForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(ReplicationCleanupWorkflow, params, event)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})
}

func TestReplicationCleanupWorkflowWhenError(t *testing.T) {
	t.Run("TestReplicationCleanupWorkflow", func(tt *testing.T) {
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
		commonActivity := activities.CommonActivities{SE: mockStorage}
		resumeReplicationActivity := replicationActivities.CleanupVolumeReplicationActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(resumeReplicationActivity.GetSrcBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDstBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedSrcTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedDstTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.StopReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeRemoteJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnSourceForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeSourceJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeReplicationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteVolumeOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationOnDestinationToErrorStateForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationOnSourceToErrorStateForCleanup)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.DeleteReplicationParams{}

		srcBasePath := "https://src-base-path"
		dstBasePath := "https://dst-base-path"
		srcJwtToken := "src-jwt-token"
		dstJwtToken := "dst-jwt-token"
		srcProjectNumber := "123456789"
		dstProjectNumber := "987654321"

		event := &replication.DeleteReplicationEvent{
			CommonReplicationEventParams: replication.CommonReplicationEventParams{
				ReplicationModel: &datamodel.VolumeReplication{
					Name: "test-replication",
					ReplicationAttributes: &datamodel.ReplicationDetails{
						SourceLocation:             "us-central1",
						DestinationLocation:        "us-east1",
						SourceReplicationUUID:      "src-replication-uuid",
						DestinationReplicationUUID: "dst-replication-uuid",
					},
				},
				SourceProjectNumber:      srcProjectNumber,
				DestinationProjectNumber: dstProjectNumber,
			},
		}

		replicationResult := &replication.DeleteReplicationResult{
			SrcProjectNumber: &srcProjectNumber,
			DstProjectNumber: &dstProjectNumber,
			SrcBasePath:      &srcBasePath,
			DstBasePath:      &dstBasePath,
			SrcJwtToken:      &srcJwtToken,
			DstJwtToken:      &dstJwtToken,
			Event:            event,
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				Name:             googleproxyclient.NewOptString("repl-123"),
				LastTransferSize: googleproxyclient.NewOptInt64(100),
				MirrorState:      googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED),
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity(resumeReplicationActivity.StopReplicationOnDestinationForCleanup, mock.Anything, mock.Anything).Return(nil, errors.New("failed to update volume details"))
		env.OnActivity("UpdateReplicationOnDestinationToErrorStateForCleanup", mock.Anything, mock.Anything).Return(nil, assert.AnError)
		env.OnActivity("UpdateReplicationOnSourceToErrorStateForCleanup", mock.Anything, mock.Anything).Return(nil, assert.AnError)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(ReplicationCleanupWorkflow, params, event)
		// Assert that the workflow was executed and handled the error
		assert.True(t, env.IsWorkflowCompleted())
		env.AssertExpectations(t)
	})
}

func TestReplicationCleanupWorkflowWithMirrorStateUnspecified(t *testing.T) {
	t.Run("TestReplicationCleanupWorkflowWithMirrorStateUnspecified", func(tt *testing.T) {
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
		commonActivity := activities.CommonActivities{SE: mockStorage}
		resumeReplicationActivity := replicationActivities.CleanupVolumeReplicationActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(resumeReplicationActivity.GetSrcBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDstBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedSrcTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedDstTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.StopReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeRemoteJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnSourceForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeSourceJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeReplicationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteVolumeOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationOnDestinationToErrorStateForCleanup)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.DeleteReplicationParams{}

		event := &replication.DeleteReplicationEvent{
			CommonReplicationEventParams: replication.CommonReplicationEventParams{
				ReplicationModel: &datamodel.VolumeReplication{},
			},
		}

		replicationResult := &replication.DeleteReplicationResult{
			SrcProjectNumber: &event.SourceProjectNumber,
			DstProjectNumber: &event.DestinationProjectNumber,
			Event:            event,
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				Name:             googleproxyclient.NewOptString("repl-123"),
				LastTransferSize: googleproxyclient.NewOptInt64(100),
				MirrorState:      googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORSTATEUNSPECIFIED),
			},
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		// StopReplicationOnDestinationForCleanup should not be called when mirror state is MIRROR_STATE_UNSPECIFIED.
		env.OnActivity("DescribeRemoteJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DeleteSnapmirrorSnapshotsOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnSourceForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeSourceJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeHydrateDestinationVolumeReplicationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		// GetDestinationVolumeForCleanup returns result with DstVolume = nil, so DeleteVolumeOnDestinationForCleanup
		// and DeHydrateDestinationVolumeForCleanup won't be called
		env.OnActivity("GetDestinationVolumeForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(ReplicationCleanupWorkflow, params, event)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})
}

func TestReplicationCleanupWorkflow_WhenDestinationVolumeIsDeleted(t *testing.T) {
	t.Run("TestReplicationCleanupWorkflow_WhenDestinationVolumeIsDeleted", func(tt *testing.T) {
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
		commonActivity := activities.CommonActivities{SE: mockStorage}
		resumeReplicationActivity := replicationActivities.CleanupVolumeReplicationActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(resumeReplicationActivity.GetSrcBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDstBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedSrcTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedDstTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.StopReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeRemoteJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnSourceForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeSourceJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeReplicationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteVolumeOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeForCleanup)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.DeleteReplicationParams{}

		event := &replication.DeleteReplicationEvent{
			CommonReplicationEventParams: replication.CommonReplicationEventParams{
				ReplicationModel: &datamodel.VolumeReplication{},
			},
		}

		// Create a volume with Deleted field set (volume is deleted)
		deletedVolume := &googleproxyclient.VolumeV1beta{
			ResourceId: "test-volume-resource-id",
			VolumeId:   googleproxyclient.NewOptString("test-volume-id"),
			Deleted:    googleproxyclient.NewOptNilDateTime(time.Now()),
		}

		replicationResult := &replication.DeleteReplicationResult{
			SrcProjectNumber: &event.SourceProjectNumber,
			DstProjectNumber: &event.DestinationProjectNumber,
			Event:            event,
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				Name:             googleproxyclient.NewOptString("repl-123"),
				LastTransferSize: googleproxyclient.NewOptInt64(100),
				MirrorState:      googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING),
			},
			DstVolume: deletedVolume,
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeRemoteJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DeleteSnapmirrorSnapshotsOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnSourceForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeSourceJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeHydrateDestinationVolumeReplicationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDestinationVolumeForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		// These activities should NOT be called when volume is deleted
		// env.OnActivity("DeleteVolumeOnDestinationForCleanup", ...) - should not be called
		// env.OnActivity("DeHydrateDestinationVolumeForCleanup", ...) - should not be called
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(ReplicationCleanupWorkflow, params, event)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		// Verify that DeleteVolumeOnDestinationForCleanup and DeHydrateDestinationVolumeForCleanup were NOT called
		env.AssertExpectations(tt)
	})
}

func TestReplicationCleanupWorkflow_WhenDestinationVolumeIsNotDeleted(t *testing.T) {
	t.Run("TestReplicationCleanupWorkflow_WhenDestinationVolumeIsNotDeleted", func(tt *testing.T) {
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
		commonActivity := activities.CommonActivities{SE: mockStorage}
		resumeReplicationActivity := replicationActivities.CleanupVolumeReplicationActivity{SE: mockStorage}
		env.SetHeader(mockHeader)
		env.RegisterActivity(resumeReplicationActivity.GetSrcBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDstBasePathCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedSrcTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetSignedDstTokenCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.StopReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeRemoteJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteReplicationOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnSourceForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DescribeSourceJobForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeReplicationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.UpdateReplicationRecordOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.GetDestinationVolumeForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeleteVolumeOnDestinationForCleanup)
		env.RegisterActivity(resumeReplicationActivity.DeHydrateDestinationVolumeForCleanup)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		params := &commonparams.DeleteReplicationParams{}

		event := &replication.DeleteReplicationEvent{
			CommonReplicationEventParams: replication.CommonReplicationEventParams{
				ReplicationModel: &datamodel.VolumeReplication{},
			},
		}

		// Create a volume with Deleted field NOT set (volume is not deleted)
		// Deleted field will be unset (default OptNilDateTime{} which has Set=false)
		notDeletedVolume := &googleproxyclient.VolumeV1beta{
			ResourceId: "test-volume-resource-id",
			VolumeId:   googleproxyclient.NewOptString("test-volume-id"),
			// Deleted field is not set, so IsSet() will return false
		}

		replicationResult := &replication.DeleteReplicationResult{
			SrcProjectNumber: &event.SourceProjectNumber,
			DstProjectNumber: &event.DestinationProjectNumber,
			Event:            event,
			DstReplication: &googleproxyclient.VolumeReplicationInternalV1beta{
				Name:             googleproxyclient.NewOptString("repl-123"),
				LastTransferSize: googleproxyclient.NewOptInt64(100),
				MirrorState:      googleproxyclient.NewOptVolumeReplicationInternalV1betaMirrorState(googleproxyclient.VolumeReplicationInternalV1betaMirrorStatePREPARING),
			},
			DstVolume: notDeletedVolume,
		}

		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSrcBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDstBasePathCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedSrcTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetSignedDstTokenCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeRemoteJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteReplicationOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DeleteSnapmirrorSnapshotsOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnSourceForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DescribeSourceJobForCleanup", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeHydrateDestinationVolumeReplicationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("UpdateReplicationRecordOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("GetDestinationVolumeForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		// These activities SHOULD be called when volume is not deleted
		env.OnActivity("DeleteVolumeOnDestinationForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		env.OnActivity("DeHydrateDestinationVolumeForCleanup", mock.Anything, mock.Anything).Return(replicationResult, nil)
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.ExecuteWorkflow(ReplicationCleanupWorkflow, params, event)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.Nil(tt, err)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		// Verify that DeleteVolumeOnDestinationForCleanup and DeHydrateDestinationVolumeForCleanup were called
		env.AssertExpectations(tt)
	})
}

func TestShouldSkipDehydration(t *testing.T) {
	tests := []struct {
		name         string
		replication  *googleproxyclient.VolumeReplicationInternalV1beta
		expectedSkip bool
	}{
		{
			name:         "nil replication should skip",
			replication:  nil,
			expectedSkip: true,
		},
		{
			name: "lifecycle state creating should skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
					googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating,
				),
			},
			expectedSkip: true,
		},
		{
			name: "lifecycle state error should skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
					googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError,
				),
			},
			expectedSkip: true,
		},
		{
			name: "lifecycle state available should not skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
					googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateAvailable,
				),
			},
			expectedSkip: false,
		},
		{
			name: "lifecycle state updating should not skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
					googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateUpdating,
				),
			},
			expectedSkip: false,
		},
		{
			name: "lifecycle state disabled should not skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
					googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDisabled,
				),
			},
			expectedSkip: false,
		},
		{
			name: "lifecycle state deleting should not skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
					googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleting,
				),
			},
			expectedSkip: false,
		},
		{
			name: "lifecycle state deleted should not skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				LifeCycleState: googleproxyclient.NewOptVolumeReplicationInternalV1betaLifeCycleState(
					googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateDeleted,
				),
			},
			expectedSkip: false,
		},
		{
			name:        "lifecycle state not set should not skip",
			replication: &googleproxyclient.VolumeReplicationInternalV1beta{
				// LifeCycleState is not set (default OptVolumeReplicationInternalV1betaLifeCycleState with Set=false)
			},
			expectedSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldSkipDehydration(tt.replication)
			assert.Equal(t, tt.expectedSkip, result, "shouldSkipDehydration() = %v, want %v", result, tt.expectedSkip)
		})
	}
}
