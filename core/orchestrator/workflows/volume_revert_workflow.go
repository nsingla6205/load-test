package workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	volumeRevertStartToCloseTimeoutSec = env.GetUint64("VOLUME_REVERT_START_TO_CLOSE_TIMEOUT_SEC", 600)
	volumeRevertHeartbeatTimeoutSec    = env.GetUint64("VOLUME_REVERT_HEARTBEAT_TIMEOUT_SEC", 300)
)

type volumeRevertWorkflow struct {
	// add fields needed for revert volume workflow
	BaseWorkflow
}

// Enforcing the WorkflowInterface on volumeRevertWorkflow
var _ WorkflowInterface = &volumeRevertWorkflow{}

// RevertVolumeWorkflow Update Volume Workflow process volume related requests from a customer.
func RevertVolumeWorkflow(ctx workflow.Context, params *common.RevertVolumeParams, volume *datamodel.Volume, snapshot *datamodel.Snapshot) error {
	log := util.GetLogger(ctx)
	volumeWf := new(volumeRevertWorkflow)
	err := volumeWf.Setup(ctx, volume)
	if err != nil {
		log.Errorf("Volume update workflow setup executed with error: %v", err)
		return err
	}
	if err = volumeWf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return err
	}
	volumeWf.Status = WorkflowStatusRunning
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Processing for RevertVolumeWorkflow: %v", err)
		return err
	}

	_, errRun := volumeWf.Run(ctx, params, volume, snapshot)
	if errRun != nil {
		log.Errorf("RevertVolumeWorkflow completed with error: %v", errRun)
		volumeWf.Status = WorkflowStatusFailed
		err2 := volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), errRun)
		if err2 != nil {
			log.Errorf("Failed to update job status to Done with err for RevertVolumeWorkflow: %v", err)
			return err2
		}
		return errRun
	}

	volumeWf.Status = WorkflowStatusCompleted
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		log.Errorf("Failed to update job status to Done for RevertVolumeWorkflow: %v", err)
	}
	return nil
}

func (wf *volumeRevertWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.Volume)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "customerID": wf.CustomerID})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *volumeRevertWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*common.RevertVolumeParams)
	volume := args[1].(*datamodel.Volume)
	snapshot := args[2].(*datamodel.Snapshot)

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(volumeRevertStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(volumeRevertHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	rollbackManager := common.NewRollbackManager()
	rollbackManager.AddActivity(activities.VolumeCreateActivity.UpdateVolumeStateInDB, volume.UUID, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)

	defer func() {
		if err != nil {
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, volume.Pool.ID).Get(ctx, &dbNodes)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   volume.Pool.DeploymentName,
		OntapCredentials: volume.Pool.PoolCredentials,
	})
	err = workflow.ExecuteActivity(ctx, activities.VolumeRevertActivity.RevertVolume, &volume, &snapshot, &node, params).Get(ctx, nil)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, ConvertToVSAError(err)
}
