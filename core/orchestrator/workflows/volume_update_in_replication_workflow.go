package workflows

import (
	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	UpdateRemoteVolumeJobsRetryMaxAttempts = env.GetInt("UPDATE_REMOTE_VOLUME_JOBS_RETRY_MAX_ATTEMPTS", 10)
)

type updateVolumeInReplication struct {
	BaseWorkflow
}

// Enforcing the WorkflowInterface on updateVolumeInReplication
var _ WorkflowInterface = &updateVolumeInReplication{}

// UpdateVolumeInReplicationWorkflow handles volume updates when the volume has Cross-Region Replication (CRR)
func UpdateVolumeInReplicationWorkflow(ctx workflow.Context, params *common.UpdateVolumeParams, volume *datamodel.Volume) error {
	volumeWf := new(updateVolumeInReplication)
	err := volumeWf.Setup(ctx, params)
	if err != nil {
		return err
	}
	volumeWf.Status = WorkflowStatusRunning
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		return err
	}
	_, customErr := volumeWf.Run(ctx, volume, params)
	if customErr != nil {
		volumeWf.Status = WorkflowStatusFailed
		err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if err != nil {
			return err
		}
	}
	volumeWf.Status = WorkflowStatusCompleted
	err = volumeWf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	return err
}

func (wf *updateVolumeInReplication) Setup(ctx workflow.Context, input interface{}) error {
	params := input.(*common.UpdateVolumeParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = params.AccountName
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

func (wf *updateVolumeInReplication) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	volume := args[0].(*datamodel.Volume)
	params := args[1].(*common.UpdateVolumeParams)
	updateActivity := &activities.UpdateVolumeInReplicationActivity{}
	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	options := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	ao1 := options
	ao1.RetryPolicy.MaximumAttempts = int32(UpdateRemoteVolumeJobsRetryMaxAttempts)
	ctx1 := workflow.WithActivityOptions(ctx, ao1)

	rollbackManager := common.NewRollbackManager()
	defer func() {
		if err != nil {
			err2 := workflow.ExecuteActivity(ctx, activities.VolumeCreateActivity.UpdateVolumeStateInDB, volume.UUID, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil)
			if err2 != nil {
				log.Errorf("Failed to update volume state in DB to READY: %v", err2)
			}
			disconnectedCtx, _ := workflow.NewDisconnectedContext(ctx)
			rollbackManager.ExecuteRollback(disconnectedCtx, err)
		}
	}()
	event := &common.VolumeUpdateEventParams{}
	event.CorrelationID = params.CorrelationID

	// Get replication details from the database
	err = workflow.ExecuteActivity(ctx, updateActivity.GetReplicationFromDBVolume, volume, event, params).Get(ctx, &event)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, updateActivity.GetLocalBasePathVolume, event).Get(ctx, &event)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, updateActivity.GetRemoteBasePathVolume, event).Get(ctx, &event)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, updateActivity.GetSignedLocalTokenVolume, event).Get(ctx, &event)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, updateActivity.GetSignedRemoteTokenVolume, event).Get(ctx, &event)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	var mirrorState string
	err = workflow.ExecuteActivity(ctx, updateActivity.GetReplicationMirrorState, event, volume).Get(ctx, &mirrorState)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	if mirrorState == string(gcpgenserver.ReplicationV1betaMirrorStateMIRRORED) {
		var pool googleproxyclient.PoolV1beta
		err = workflow.ExecuteActivity(ctx, updateActivity.GetRemotePoolDetailsVolume, event).Get(ctx, &pool)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		updateRemoteVolume := false
		err = workflow.ExecuteActivity(ctx, updateActivity.ValidateRemoteVolumeUpdate, &pool, params, volume).Get(ctx, &updateRemoteVolume)
		if err != nil {
			return nil, ConvertToVSAError(err)
		}
		if updateRemoteVolume {
			var jobId string
			err = workflow.ExecuteActivity(ctx, updateActivity.UpdateRemoteVolume, params, event).Get(ctx, &jobId)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}

			err = workflow.ExecuteActivity(ctx1, updateActivity.DescribeRemoteJobVolumeUpdate, event, jobId).Get(ctx, nil)
			if err != nil {
				return nil, ConvertToVSAError(err)
			}
		}
	}

	var createdJob datamodel.Job
	err = workflow.ExecuteActivity(ctx, updateActivity.CreateJobForChildWorkflow, volume).Get(ctx, &createdJob)
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		TaskQueue:             workflowengine.CustomerTaskQueue,
		WorkflowID:            createdJob.WorkflowID,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
		WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		ParentClosePolicy:     enums.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    int32(retryPolicy.MaximumAttempts),
		},
	})

	var childWorkflowResult gcpgenserver.V1betaDescribeVolumeRes
	err = workflow.ExecuteChildWorkflow(childCtx, UpdateVolumeWorkflow, params, volume).Get(ctx, &childWorkflowResult)
	if err != nil {
		return nil, ConvertToVSAError(err)
	}

	return nil, nil
}
