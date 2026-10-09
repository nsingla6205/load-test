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
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type hybridReplicationDeleteWorkflow struct {
	workflows.BaseWorkflow
}

type hybridDeleteDestinationVolumeWorkflow struct {
	workflows.BaseWorkflow
}

var (
	_ workflows.WorkflowInterface = &hybridReplicationDeleteWorkflow{}
	_ workflows.WorkflowInterface = &hybridDeleteDestinationVolumeWorkflow{}
)

func HybridReplicationDeleteWorkflow(ctx workflow.Context, params *commonparams.DeleteReplicationParams, event *replication.DeleteReplicationEvent) (*vsa.VolumeReplication, error) {
	repWf := new(hybridReplicationDeleteWorkflow)
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

func (wf *hybridReplicationDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteReplicationParams := input.(*commonparams.DeleteReplicationParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *hybridReplicationDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	event := args[0].(*replication.DeleteReplicationEvent)

	replicationActivity := &replicationActivities.DeleteVolumeReplicationActivity{}
	hybridActivity := &replicationActivities.HybridDeleteVolumeReplicationActivity{}

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
	releaseAo := ao
	// Keep activity timeout above SVM cleanup timeout to avoid losing mapped business error on deadline boundary.
	releaseAo.StartToCloseTimeout = time.Duration(workflows.StartToCloseTimeoutForReplicationActivities+120) * time.Second

	releaseCtx := workflow.WithActivityOptions(ctx, releaseAo)
	ao1 := ao
	ao1.RetryPolicy.MaximumAttempts = int32(ReplicationJobsRetryMaxAttempts)
	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	replicationResult := replication.DeleteReplicationResult{
		SrcProjectNumber: &event.SourceProjectNumber,
		DstProjectNumber: &event.DestinationProjectNumber,
		Event:            event,
		CorrelationID:    event.CommonReplicationEventParams.XCorrelationID,
	}

	// Error handling - update state to error on failure
	defer func() {
		if err != nil {
			err2 := workflow.ExecuteActivity(ctx1, replicationActivity.UpdateReplicationInDBToErrorState, &replicationResult).Get(ctx, nil)
			if err2 != nil {
				log.Error("Failed to update volume replication state in DB to error", "error", err2)
			}
		}
	}()

	// Set hybrid replication variables
	err = workflow.ExecuteActivity(ctx, replicationActivity.SetHybridReplicationVariablesDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Get base paths
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSrcBasePathDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetDstBasePathDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Get signed tokens
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedSrcTokenDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.GetSignedDstTokenDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Delete Replication when GCNV is source for Hybrid Replication
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

		// Release replication on source
		err = workflow.ExecuteActivity(releaseCtx, replicationActivity.ReleaseReplicationOnSrc, &replicationResult, node).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Delete snapmirror snapshots on source
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteSnapmirrorSnapshotsOnSource, &replicationResult).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeSourceJobForDelete, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Delete replication record from database
		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteReplicationRecordOnSource, &replicationResult).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Update RBAC role
		err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateRbacRole, &replicationResult, node).Get(ctx, &replicationResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		// Cleanup cluster peering if this is the last replication
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

	// GCNV is destination for Hybrid Replication
	err = workflow.ExecuteActivity(ctx, replicationActivity.GetReplicationOnDestinationForDelete, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Check if hybrid replication is in PENDING_CLUSTER_PEER or PENDING_SVM_PEER
	isHybridReplicationPendingPeering := replicationResult.Event != nil &&
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

	// Update replication record on destination
	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationRecordOnDestination, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForDelete, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Cleanup cluster peering if this is the last replication
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

	// Delete volume on destination if pending peering or UNINITIALIZED - execute as child workflow
	shouldDeleteDestinationVolume := isHybridReplicationPendingPeering ||
		(replicationResult.DstReplication.MirrorState.IsSet() &&
			replicationResult.DstReplication.MirrorState.Value == googleproxyclient.VolumeReplicationInternalV1betaMirrorStateUNINITIALIZED)

	if shouldDeleteDestinationVolume {
		var deleteVolumeJob datamodel.Job
		err = workflow.ExecuteActivity(ctx, hybridActivity.CreateJobForHybridDeleteVolume, &replicationResult, string(datamodel.JobTypeHybridReplicationDeleteVolume)).Get(ctx, &deleteVolumeJob)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			TaskQueue:             workflowengine.CustomerTaskQueue,
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
			ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_ABANDON,
			WorkflowID:            deleteVolumeJob.WorkflowID,
		})

		childWorkflowFuture := workflow.ExecuteChildWorkflow(
			childCtx,
			HybridDeleteDestinationVolumeWorkflow,
			replicationResult,
		)

		// Just get the child workflow execution, don't wait for result
		var childWE workflow.Execution
		err = childWorkflowFuture.GetChildWorkflowExecution().Get(ctx, &childWE)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	return nil, workflows.ConvertToVSAError(err)
}

// HybridDeleteDestinationVolumeWorkflow is a child workflow that deletes the destination volume
// It runs with PARENT_CLOSE_POLICY_ABANDON so it continues after the parent workflow ends
func HybridDeleteDestinationVolumeWorkflow(ctx workflow.Context, replicationResult replication.DeleteReplicationResult) error {
	deleteVolWf := new(hybridDeleteDestinationVolumeWorkflow)
	err := deleteVolWf.Setup(ctx, nil)
	if err != nil {
		return err
	}

	deleteVolWf.Status = workflows.WorkflowStatusRunning
	err = deleteVolWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		deleteVolWf.Status = workflows.WorkflowStatusFailed
		err = deleteVolWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}

	_, workflowErr := deleteVolWf.Run(ctx, replicationResult)
	if workflowErr != nil {
		deleteVolWf.Status = workflows.WorkflowStatusFailed
		err2 := deleteVolWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), workflowErr)
		if err2 != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		}
		return workflowErr
	}

	deleteVolWf.Status = workflows.WorkflowStatusCompleted
	err2 := deleteVolWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err2 != nil {
		workflow.GetLogger(ctx).Error("Failed to update job status", "error", err2)
		return err2
	}
	return nil
}

func (wf *hybridDeleteDestinationVolumeWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

func (wf *hybridDeleteDestinationVolumeWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	replicationResult := args[0].(replication.DeleteReplicationResult)
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

	// Delete volume on destination
	err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteVolumeOnDestination, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DescribeRemoteJobForDelete, &replicationResult).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx1, replicationActivity.DeHydrateDestinationVolume, &replicationResult).Get(ctx, &replicationResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}
