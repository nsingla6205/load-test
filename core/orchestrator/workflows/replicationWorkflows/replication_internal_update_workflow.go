package replicationWorkflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type internalVolumeReplicationUpdateWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &internalVolumeReplicationUpdateWorkflow{}

func UpdateInternalVolumeReplicationWorkflow(ctx workflow.Context, params *common.UpdateVolumeReplicationInternalParams, replication *datamodel.VolumeReplication) (*vsa.VolumeReplication, error) {
	logger := workflow.GetLogger(ctx)
	repWf := new(internalVolumeReplicationUpdateWorkflow)
	err := repWf.Setup(ctx, params)
	if err != nil {
		logger.Error("UpdateInternalVolumeReplicationWorkflow setup failed", "error", err)
		return nil, err
	}
	repWf.Status = workflows.WorkflowStatusRunning
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		logger.Error("Failed to update job status to Processing for UpdateInternalVolumeReplicationWorkflow", "error", err)
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if err != nil {
			logger.Error("Failed to update job status to Error for UpdateInternalVolumeReplicationWorkflow", "error", err)
		}
		return nil, err
	}
	_, customErr := repWf.Run(ctx, params, replication)
	if customErr != nil {
		logger.Error("UpdateInternalVolumeReplicationWorkflow run failed", "error", customErr)
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err != nil {
			logger.Error("Failed to update job status to Error for UpdateInternalVolumeReplicationWorkflow", "error", err)
		}
		return nil, err
	}
	repWf.Status = workflows.WorkflowStatusCompleted
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		logger.Error("Failed to update job status to Done for UpdateInternalVolumeReplicationWorkflow", "error", err)
	}
	return nil, err
}

func (wf *internalVolumeReplicationUpdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	updateReplicationParams := input.(*common.UpdateVolumeReplicationInternalParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = updateReplicationParams.AccountName
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *internalVolumeReplicationUpdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.UpdateVolumeReplicationInternalParams)
	replication := args[1].(*datamodel.VolumeReplication)
	replicationUpdateActivity := &replicationActivities.InternalVolumeReplicationUpdateActivity{}
	replicationActivity := &replicationActivities.InternalVolumeReplicationActivity{}
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

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, replication.Volume.PoolID).Get(ctx, &dbNodes)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if replication.Volume.Pool.PoolCredentials == nil {
		return nil, workflows.ConvertToVSAError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool credentials not found for pool %d", replication.Volume.PoolID)))
	}
	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   replication.Volume.Pool.DeploymentName,
		OntapCredentials: replication.Volume.Pool.PoolCredentials,
	})

	var replicationUpdateResponse *vsa.VolumeReplication
	err = workflow.ExecuteActivity(ctx, replicationUpdateActivity.UpdateVolumeReplicationOntap, params, node, replication.ReplicationAttributes.ExternalUUID).Get(ctx, &replicationUpdateResponse)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, replicationActivity.UpdateVolumeReplicationDetails, replication, replicationUpdateResponse, params).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Update cluster peering cluster location if provided
	err = workflow.ExecuteActivity(ctx, replicationUpdateActivity.UpdateClusterPeeringClusterLocation, params, replication).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, workflows.ConvertToVSAError(err)
}
