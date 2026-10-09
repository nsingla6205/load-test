package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type internalVolumeReplicationDeleteWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &internalVolumeReplicationDeleteWorkflow{}

func DeleteInternalVolumeReplicationWorkflow(ctx workflow.Context, replication *datamodel.VolumeReplication, cleanupAfterReverse, isCleanup bool) (*vsa.VolumeReplication, error) {
	repWf := new(internalVolumeReplicationDeleteWorkflow)
	err := repWf.Setup(ctx, replication)
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
	_, customErr := repWf.Run(ctx, replication, cleanupAfterReverse, isCleanup)
	if customErr != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return nil, err
	}
	repWf.Status = workflows.WorkflowStatusCompleted
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return nil, err
}

func (wf *internalVolumeReplicationDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	deleteReplicationParams := input.(*datamodel.VolumeReplication)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = deleteReplicationParams.Account.Name
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *internalVolumeReplicationDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	replication := args[0].(*datamodel.VolumeReplication)
	cleanupAfterReverse := args[1].(bool)
	isCleanup := args[2].(bool)
	replicationActivity := &replicationActivities.InternalVolumeReplicationDeleteActivity{}
	stopActivity := &replicationActivities.InternalStopVolumeReplicationActivity{}
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
			NonRetryableErrorTypes: []string{"NonRetryableErr", "PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	defer func() {
		if err != nil {
			err2 := workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationStateInDBForDelete, replication).Get(ctx, nil)
			if err2 != nil {
				log.Error("Failed to update volume state in DB to error", "error", err2)
			}
		}
	}()

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, replication.Volume.PoolID).Get(ctx, &dbNodes)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   replication.Volume.Pool.DeploymentName,
		OntapCredentials: replication.Volume.Pool.PoolCredentials,
	})

	var replicationDeleteResponse *vsa.VolumeReplication
	if !cleanupAfterReverse {
		var ontapReplication *vsa.VolumeReplication
		err = workflow.ExecuteActivity(ctx, replicationActivity.GetVolumeReplicationFromOntap, replication, node).Get(ctx, &ontapReplication)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		if replicationActivities.ShouldBreakBeforeDelete(ontapReplication) {
			log.Info("Stopping replication on ONTAP before delete", "mirror_state", ontapReplication.MirrorState)
			forceStop := true
			err = workflow.ExecuteActivity(ctx, stopActivity.BreakVolumeReplication, replication, node, forceStop).Get(ctx, nil)
			if err != nil {
				return nil, workflows.ConvertToVSAError(err)
			}
		}

		err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteVolumeReplication, replication, node, isCleanup).Get(ctx, &replicationDeleteResponse)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	} else {
		err = workflow.ExecuteActivity(ctx, replicationActivity.CleanupReplicationAfterReverse, replication, node).Get(ctx, &replicationDeleteResponse)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}
	return nil, workflows.ConvertToVSAError(err)
}
