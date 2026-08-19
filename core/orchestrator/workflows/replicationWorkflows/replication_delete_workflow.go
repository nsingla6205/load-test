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
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type replicationDeleteWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &replicationDeleteWorkflow{}

func ReplicationDeleteWorkflow(ctx workflow.Context, params *commonparams.DeleteReplicationParams, event *replication.DeleteReplicationEvent) (*vsa.VolumeReplication, error) {
	repWf := new(replicationDeleteWorkflow)
	err := repWf.Setup(ctx, params)
	if err != nil {
		return nil, err
	}

	if err = repWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
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

func (wf *replicationDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteReplicationParams := input.(*commonparams.DeleteReplicationParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *replicationDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	event := args[0].(*replication.DeleteReplicationEvent)
	replicationActivity := &replicationActivities.DeleteVolumeReplicationActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
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
		log.Warnf("Error handling cancellation: %v, proceeding with normal delete", cancelErr)
	}

	defer func() {
		if err != nil {
			if replicationResult.IsSrcForHybridReplication || replicationResult.IsHybridReplicationVolume {
				err2 := workflow.ExecuteActivity(ctx1, replicationActivity.UpdateReplicationInDBToErrorState, &replicationResult).Get(ctx, nil)
				if err2 != nil {
					log.Errorf("Failed to update volume replication state in DB to error: %v", err2)
				}
			} else {
				err2 := workflow.ExecuteActivity(ctx1, replicationActivity.UpdateReplicationOnDestinationToErrorState, &replicationResult).Get(ctx, &replicationResult)
				if err2 != nil {
					log.Errorf("Failed to update volume replication state in DB to error: %v", err2)
				}
				err3 := workflow.ExecuteActivity(ctx1, replicationActivity.UpdateReplicationOnSourceToErrorState, &replicationResult).Get(ctx, &replicationResult)
				if err3 != nil {
					log.Errorf("Failed to update volume replication state in DB to error: %v", err3)
				}
			}
		}
	}()

	err = workflow.ExecuteActivity(ctx, replicationActivity.SetHybridReplicationVariablesDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePathDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcTokenDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstTokenDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Delete Replication when Gcnv is source for Bidirectional/Hybrid Replication
	if replicationResult.IsSrcForHybridReplication {
		var dbNodes []*datamodel.Node
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &replicationResult.Event.ReplicationModel.Volume.Pool.ID).Get(ctx, &dbNodes)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            dbNodes,
			DeploymentName:   replicationResult.Event.ReplicationModel.Volume.Pool.DeploymentName,
			OntapCredentials: replicationResult.Event.ReplicationModel.Volume.Pool.PoolCredentials,
		})

		err = workflow.ExecuteActivity(ctx, replicationActivity.ReleaseReplicationOnSrc, &replicationResult, node).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteSnapmirrorSnapshotsOnSource, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeSourceJobForDelete, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteReplicationRecordOnSource, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateRbacRole, &replicationResult, node).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		if replicationResult.CleanupClusterPeering {
			err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteClusterPeeringInOntap, &replicationResult, node).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteClusterPeeringDB, &replicationResult).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}

			err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteRoleInOntap, node).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
		}

		return nil, nil
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetReplicationOnDestinationForDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Skip deletion on destination if hybrid replication is in PENDING_CLUSTER_PEER or PENDING_SVM_PEER
	isHybridReplicationPendingPeering := replicationResult.IsHybridReplicationVolume &&
		replicationResult.Event != nil &&
		replicationResult.Event.ReplicationModel != nil &&
		replicationResult.Event.ReplicationModel.HybridReplicationAttributes != nil &&
		(replicationResult.Event.ReplicationModel.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingClusterPeer ||
			replicationResult.Event.ReplicationModel.HybridReplicationAttributes.Status == datamodel.HybridReplicationStatusPendingSVMPeer)

	if !isHybridReplicationPendingPeering {
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteReplicationOnDestination, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForDelete, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteSnapmirrorSnapshotsOnDestination, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForDelete, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if !replicationResult.IsHybridReplicationVolume {
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteSnapmirrorSnapshotsOnSource, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeSourceJobForDelete, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeHydrateDestinationVolumeReplication, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationRecordOnSource, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeSourceJobForDelete, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationRecordOnDestination, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForDelete, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if isHybridReplicationPendingPeering || (replicationResult.DstReplication.MirrorState.IsSet() && replicationResult.DstReplication.MirrorState.Value == googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED) {
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteVolumeOnDestination, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForDelete, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeHydrateDestinationVolume, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	if replicationResult.CleanupClusterPeering {
		var dbNodes []*datamodel.Node
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, &replicationResult.Event.ReplicationModel.Volume.Pool.ID).Get(ctx, &dbNodes)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            dbNodes,
			DeploymentName:   replicationResult.Event.ReplicationModel.Volume.Pool.DeploymentName,
			OntapCredentials: replicationResult.Event.ReplicationModel.Volume.Pool.PoolCredentials,
		})

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteClusterPeeringInOntap, &replicationResult, node).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteClusterPeeringDB, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteRoleInOntap, node).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	return nil, workflows.ConvertToVSAError(err)
}
