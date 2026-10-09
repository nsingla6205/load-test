package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type internalVolumeReplicationRowDeleteWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &internalVolumeReplicationRowDeleteWorkflow{}

func ReleaseVolumeReplicationInternalWorkflow(ctx workflow.Context, replication *datamodel.VolumeReplication) error {
	repWf := new(internalVolumeReplicationRowDeleteWorkflow)
	err := repWf.Setup(ctx, replication)
	if err != nil {
		return err
	}
	repWf.Status = workflows.WorkflowStatusRunning
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}
	_, customErr := repWf.Run(ctx, replication)
	if customErr != nil {
		repWf.Status = workflows.WorkflowStatusFailed
		err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return err
	}
	repWf.Status = workflows.WorkflowStatusCompleted
	err = repWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return err
}

func (wf *internalVolumeReplicationRowDeleteWorkflow) Setup(ctx workflow.Context, input interface{}) error { // v is of type *datamodel.VolumeReplication
	createReplicationParams := input.(*datamodel.VolumeReplication)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = createReplicationParams.Account.Name
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *internalVolumeReplicationRowDeleteWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := workflow.GetLogger(ctx)
	replication := args[0].(*datamodel.VolumeReplication)
	replicationActivity := &replicationActivities.InternalVolumeReplicationRowDeleteActivity{}
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

	defer func() {
		if err != nil {
			err2 := workflow.ExecuteActivity(ctx, replicationActivity.UpdateReplicationStateInDBForRelease, replication).Get(ctx, nil)
			if err2 != nil {
				log.Error("Failed to update volume state in DB to error", "error", err2)
			}
		}
	}()

	err = workflow.ExecuteActivity(ctx, replicationActivity.DeleteVolumeReplicationRow, replication).Get(ctx, nil)

	return nil, workflows.ConvertToVSAError(err)
}
