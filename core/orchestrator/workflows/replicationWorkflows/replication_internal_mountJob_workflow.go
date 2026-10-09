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

type mountCheckWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &mountCheckWorkflow{}

func PerformMountCheckWorkflow(ctx workflow.Context, replicationUUID string, accountName string) error {
	logger := workflow.GetLogger(ctx)
	mountCheckWF := new(mountCheckWorkflow)
	err := mountCheckWF.Setup(ctx, accountName)
	if err != nil {
		logger.Error("PerformMountCheckWorkflow setup failed", "error", err)
		return err
	}
	mountCheckWF.Status = workflows.WorkflowStatusRunning
	err = mountCheckWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		logger.Error("Failed to update job status to Processing for PerformMountCheckWorkflow", "error", err)
		mountCheckWF.Status = workflows.WorkflowStatusFailed
		err = mountCheckWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if err != nil {
			logger.Error("Failed to update job status to Error for PerformMountCheckWorkflow", "error", err)
		}
		return err
	}
	_, customErr := mountCheckWF.Run(ctx, replicationUUID, accountName)
	if customErr != nil {
		logger.Error("PerformMountCheckWorkflow run failed", "error", customErr)
		mountCheckWF.Status = workflows.WorkflowStatusFailed
		err = mountCheckWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err != nil {
			logger.Error("Failed to update job status to Error for PerformMountCheckWorkflow", "error", err)
		}
		return err
	}
	mountCheckWF.Status = workflows.WorkflowStatusCompleted
	err = mountCheckWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		logger.Error("Failed to update job status to Done for PerformMountCheckWorkflow", "error", err)
	}
	return err
}

func (wf *mountCheckWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	AccountName := input.(string)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = AccountName
	wf.Status = workflows.WorkflowStatusCreated

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *mountCheckWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	replicationUUID := args[0].(string)
	accountName := args[1].(string)
	mountJobActivity := &replicationActivities.MountJobActivity{}
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

	var replication *datamodel.VolumeReplication
	err = workflow.ExecuteActivity(ctx, mountJobActivity.GetReplication, replicationUUID).Get(ctx, &replication)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

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

	ao1 := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:        retryPolicy.MaximumInterval,
			NonRetryableErrorTypes: []string{"NonRetryableErr", "PanicError"},
		},
	}
	ctx1 := workflow.WithActivityOptions(ctx, ao1)
	checkMountStart := workflow.Now(ctx)
	err = workflow.ExecuteActivity(ctx1, mountJobActivity.CheckMountJob, replication, node, accountName, checkMountStart).Get(ctx1, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	var lunDetails []*vsa.LunResponse
	if replication.Volume.VolumeAttributes.FileProperties != nil {
		err = workflow.ExecuteActivity(ctx, mountJobActivity.MountVolume, replication, node).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	} else {
		err = workflow.ExecuteActivity(ctx, mountJobActivity.GetLunDetailsFromOntap, replication, node).Get(ctx, &lunDetails)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}
	if (replication.Volume.VolumeAttributes.Protocols != nil && replication.Volume.VolumeAttributes.Protocols[0] == "ISCSI") && (lunDetails == nil || len(lunDetails) > 1) {
		err = workflow.ExecuteActivity(ctx, mountJobActivity.AbortVolumeReplicationForMount, replication, node).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
		err = workflow.ExecuteActivity(ctx, mountJobActivity.BreakVolumeReplicationForMount, replication, node).Get(ctx, nil)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, mountJobActivity.GetReplicationFromOntap, replication, node, accountName).Get(ctx, &replication)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, mountJobActivity.UpdateReplicationInDB, replication, lunDetails).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, mountJobActivity.UpdateVolumeDetailsInDB, replication, lunDetails).Get(ctx, nil)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}
