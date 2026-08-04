package replicationWorkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/replicationActivities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type VolumeReplicationAttributesupdateWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

var _ workflows.WorkflowInterface = &VolumeReplicationAttributesupdateWorkflow{}

func UpdateVolumeReplicationAttributesWorkflow(ctx workflow.Context, params *commonparams.UpdateVolumeReplicationAttributesParams, event *replication.UpdateVolumeReplicationAttributesEvent) error {
	updateAttrWf := new(VolumeReplicationAttributesupdateWorkflow)
	err := updateAttrWf.Setup(ctx, params)
	if err != nil {
		return err
	}
	updateAttrWf.Status = workflows.WorkflowStatusRunning
	err = updateAttrWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		updateAttrWf.Status = workflows.WorkflowStatusFailed
		err = updateAttrWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		return err
	}
	_, customErr := updateAttrWf.Run(ctx, event)
	if customErr != nil {
		updateAttrWf.Status = workflows.WorkflowStatusFailed
		err = updateAttrWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		return err
	}
	updateAttrWf.Status = workflows.WorkflowStatusCompleted
	err = updateAttrWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return err
}

func (wf *VolumeReplicationAttributesupdateWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	updateAttributesParams := input.(*commonparams.UpdateVolumeReplicationAttributesParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = updateAttributesParams.AccountName
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

func (wf *VolumeReplicationAttributesupdateWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	event := args[0].(*replication.UpdateVolumeReplicationAttributesEvent)
	updateAttrActivity := &replicationActivities.UpdateVolumeReplicationAttributesActivity{}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(workflows.StartToCloseTimeoutForReplicationActivities) * time.Second,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     workflows.BackoffCoefficientForReplicationActivities,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"NonRetryableErr", "PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	updateResult := replication.UpdateVolumeReplicationAttributesResult{
		Event: event,
	}

	if updateResult.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationInternal.EndpointType == "src" {
		err = workflow.ExecuteActivity(ctx, updateAttrActivity.UpdateSrcVolumeReplication, &updateResult).Get(ctx, &updateResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	} else {
		err = workflow.ExecuteActivity(ctx, updateAttrActivity.GetSnapmirrorDetailsFromOntap, &updateResult).Get(ctx, &updateResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}

		err = workflow.ExecuteActivity(ctx, updateAttrActivity.UpdateDstVolumeReplication, &updateResult).Get(ctx, &updateResult)
		if err != nil {
			return nil, workflows.ConvertToVSAError(err)
		}
	}

	err = workflow.ExecuteActivity(ctx, updateAttrActivity.UpdateVolumeTypeActivity, &updateResult).Get(ctx, &updateResult)
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	return nil, nil
}
