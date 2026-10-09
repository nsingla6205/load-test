package replicationWorkflows

import (
	"time"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type replicationCleanupWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &replicationCleanupWorkflow{}

// shouldSkipDehydration checks if dehydration should be skipped.
// Returns true if replication is nil, in "creating" lifecycle state, or in "error" lifecycle state, false otherwise.
func shouldSkipDehydration(dstReplication *googleproxyclient.VolumeReplicationInternalV1beta) bool {
	if dstReplication == nil {
		return true
	}
	// Skip dehydration if lifecycle state is "creating" or "error"
	return dstReplication.LifeCycleState.Set &&
		(dstReplication.LifeCycleState.Value == googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating ||
			dstReplication.LifeCycleState.Value == googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateError)
}

func shouldStopReplicationBeforeDelete(dstReplication *googleproxyclient.VolumeReplicationInternalV1beta) bool {
	if dstReplication == nil {
		return false
	}
	// Don't stop if lifecycle state is "creating" or "error"
	if dstReplication.LifeCycleState.Set &&
		(dstReplication.LifeCycleState.Value == googleproxyclient.VolumeReplicationInternalV1betaLifeCycleStateCreating) {
		return false
	}
	mirrorState := dstReplication.MirrorState.Value

	if mirrorState == googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORSTATEUNSPECIFIED {
		return false
	}
	return mirrorState == googleproxyclient.VolumeReplicationInternalV1betaMirrorStateMIRRORED ||
		mirrorState == googleproxyclient.VolumeReplicationInternalV1betaMirrorStateBASELINETRANSFERRING
}

func ReplicationCleanupWorkflow(ctx workflow.Context, params *commonparams.DeleteReplicationParams, event *replication.DeleteReplicationEvent) (*vsa.VolumeReplication, error) {
	repWf := new(replicationCleanupWorkflow)
	err := repWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}
	repWf.Status = workflows.WorkflowStatusRunning
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return nil, err
	}
	_, customErr := repWf.Run(ctx, event)
	if customErr != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}

	repWf.Status = workflows.WorkflowStatusCompleted
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

func (wf *replicationCleanupWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	cleanupReplicationParams := input.(*commonparams.DeleteReplicationParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = cleanupReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *replicationCleanupWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	event := args[0].(*replication.DeleteReplicationEvent)
	replicationActivity := &replicationActivities.CleanupVolumeReplicationActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)

	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	replicationResult := replication.DeleteReplicationResult{
		SrcProjectNumber: &event.SourceProjectNumber,
		DstProjectNumber: &event.DestinationProjectNumber,
		Event:            event,
		CorrelationID:    event.CommonReplicationEventParams.XCorrelationID,
	}

	// Get UUID and state for cancellation handler
	replicationUUID := event.CommonReplicationEventParams.ReplicationResourceID
	replicationState := ""
	if event.CommonReplicationEventParams.ReplicationModel != nil {
		replicationUUID = event.CommonReplicationEventParams.ReplicationModel.UUID
		replicationState = event.CommonReplicationEventParams.ReplicationModel.State
	}

	cancellationActivity := &activities.CancellationActivity{}
	commonActivity := &activities.CommonActivities{}
	poolActivity := &activities.PoolActivity{}
	ackTimeout, forceTimeout := commonparams.GetCancellationTimeouts("REPLICATION")
	if cancelErr := commonparams.HandleCancellationForCreatingResource(ctx, log,
		commonparams.HandleCancellationForCreatingResourceParams{
			ResourceUUID:               replicationUUID,
			ResourceState:              replicationState,
			CreateJobType:              datamodel.JobTypeCreateVolumeReplication,
			SignalName:                 CancelReplicationSignalName,
			CancellationAckTimeout:     ackTimeout,
			ForceTerminationAckTimeout: forceTimeout,
		},
		poolActivity.GetCreateJobByResourceUUID,
		cancellationActivity,
		commonActivity,
	); cancelErr != nil {
		log.Warn("Error handling cancellation, proceeding with normal delete", "error", cancelErr)
	}

	defer func() {
		if err != nil {
			err2 := workflow.ExecuteActivity(ctx1, replicationActivity.UpdateReplicationOnDestinationToErrorStateForCleanup, &replicationResult).Get(ctx, &replicationResult)
			if err2 != nil {
				log.Error("Failed to update volume replication state in DB to error", "error", err2)
			}
			err3 := workflow.ExecuteActivity(ctx1, replicationActivity.UpdateReplicationOnSourceToErrorStateForCleanup, &replicationResult).Get(ctx, &replicationResult)
			if err3 != nil {
				log.Error("Failed to update volume replication state in DB to error", "error", err3)
			}
		}
	}()
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePathCleanup, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathCleanup, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcTokenCleanup, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstTokenCleanup, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetReplicationOnDestinationForCleanup, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if shouldStopReplicationBeforeDelete(replicationResult.DstReplication) {
		err = workflow.ExecuteActivity(ctx, replicationActivity.StopReplicationOnDestinationForCleanup, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForCleanup, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if replicationResult.DstReplication != nil {
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteReplicationOnDestinationForCleanup, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForCleanup, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteSnapmirrorSnapshotsOnDestinationForCleanup, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForCleanup, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if !shouldSkipDehydration(replicationResult.DstReplication) {
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeHydrateDestinationVolumeReplicationForCleanup, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationRecordOnSourceForCleanup, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeSourceJobForCleanup, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if replicationResult.DstReplication != nil {
		err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationRecordOnDestinationForCleanup, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForCleanup, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDestinationVolumeForCleanup, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if replicationResult.DstVolume != nil && !replicationResult.DstVolume.Deleted.IsSet() {
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteVolumeOnDestinationForCleanup, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForCleanup, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeHydrateDestinationVolumeForCleanup, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	return nil, workflows.ConvertToVSAError(err)
}
