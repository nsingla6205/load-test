package expertMode

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

type expertModeFlexCloneSplitWorkflow struct {
	workflows.BaseWorkflow
}

var _ workflows.WorkflowInterface = &expertModeFlexCloneSplitWorkflow{}

// ExpertModeFlexCloneSplitWorkflow retries WaitForExpertModeFlexCloneSplitComplete until ONTAP reports split complete
// (split is initiated by the client PATCH), then clears clone flags in DB.
func ExpertModeFlexCloneSplitWorkflow(ctx workflow.Context, volume *datamodel.ExpertModeVolumes) (interface{}, error) {
	wf := new(expertModeFlexCloneSplitWorkflow)
	if err := wf.Setup(ctx, volume); err != nil {
		return nil, err
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}
	wf.Status = workflows.WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := util.GetLogger(ctx)
		log.Errorf("Failed to update job status to PROCESSING: %v", err)
		jobErr := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), err)
		if jobErr != nil {
			log.Errorf("Failed to update job status to ERROR: %v", jobErr)
		}
		return nil, err
	}
	_, cerr := wf.Run(ctx, volume)
	if cerr != nil {
		wf.Status = workflows.WorkflowStatusFailed
		log := util.GetLogger(ctx)
		err2 := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), cerr)
		if err2 != nil {
			log.Errorf("Failed to update job status to ERROR: %v", err2)
		}
		return nil, cerr
	}
	wf.Status = workflows.WorkflowStatusCompleted
	return nil, wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
}

func (wf *expertModeFlexCloneSplitWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	volume := input.(*datamodel.ExpertModeVolumes)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = volume.Account.Name

	wf.Status = workflows.WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{"workflowID": wf.ID, "volumeName": volume.Name})
	wf.Logger = util.GetLogger(ctx)
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{ID: wf.ID, Status: wf.Status, CustomerID: wf.CustomerID}, nil
	})
}

func (wf *expertModeFlexCloneSplitWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	log := util.GetLogger(ctx)
	volume := args[0].(*datamodel.ExpertModeVolumes)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	expertModeStartToCloseTimeout := time.Duration(VolumeReconciliationExpertModeStartToCloseTimeoutSec) * time.Second

	// `ctx`: GetNode, CompleteExpertModeFlexCloneSplitInDB. Per-try: StartToCloseTimeout; retries: expert-mode backoff.
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: expertModeStartToCloseTimeout,
		HeartbeatTimeout:    expertModeStartToCloseTimeout / 2,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     VolumeReconciliationExpertModeBackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// `waitCtx`: poll activity only. ScheduleToClose = total budget for all polls (same as workflow run timeout, env EXPERT_MODE_FLEXCLONE_SPLIT_WORKFLOW_TIMEOUT_MINUTES).
	// StartToClose = max one attempt; HeartbeatTimeout = worker must heartbeat; retries every pollInterval (env EXPERT_MODE_FLEXCLONE_SPLIT_POLL_INTERVAL_SEC), unlimited until ScheduleToClose.
	pollInterval := workflowengine.GetExpertModeFlexCloneSplitPollInterval()
	waitCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ScheduleToCloseTimeout: workflowengine.GetExpertModeFlexCloneSplitWorkflowTimeout(),
		StartToCloseTimeout:    expertModeStartToCloseTimeout,
		HeartbeatTimeout:       expertModeStartToCloseTimeout / 2,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        pollInterval,
			BackoffCoefficient:     1,
			MaximumInterval:        pollInterval,
			MaximumAttempts:        0,
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	})

	// `recoverCtx`: recovery activity after poll failure. 15m per try, max 2 tries, standard backoff.
	recoverCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Minute,
		HeartbeatTimeout:    2 * time.Minute,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    retryPolicy.InitialInterval,
			BackoffCoefficient: retryPolicy.BackoffCoefficient,
			MaximumInterval:    retryPolicy.MaximumInterval,
			MaximumAttempts:    2,
		},
	})

	pool := volume.Pool

	var dbNodes []*datamodel.Node
	err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetNode, pool.ID).Get(ctx, &dbNodes)
	if err != nil {
		log.Errorf("Failed to get nodes for pool %d: %v", pool.ID, err)
		return nil, workflows.ConvertToVSAError(err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	})

	activity := &expertmodeactivities.ExpertModeVolumeActivity{}
	var splitVolumeSize int64

	err = workflow.ExecuteActivity(waitCtx, activity.WaitForExpertModeFlexCloneSplitComplete, volume, node).Get(waitCtx, &splitVolumeSize)
	if err != nil {
		log.Errorf("WaitForExpertModeFlexCloneSplitComplete failed: %v", err)
		_ = workflow.ExecuteActivity(recoverCtx, activity.RecoverExpertModeVolumeAfterFlexCloneSplitFailure, volume, node).Get(recoverCtx, nil)
		return nil, workflows.ConvertToVSAError(err)
	}

	err = workflow.ExecuteActivity(ctx, activity.CompleteExpertModeFlexCloneSplitInDB, volume.UUID, splitVolumeSize).Get(ctx, nil)
	if err != nil {
		log.Errorf("CompleteExpertModeFlexCloneSplitInDB failed: %v", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	log.Infof("Expert mode flexclone split workflow completed for volume %s", volume.Name)
	return nil, nil
}
