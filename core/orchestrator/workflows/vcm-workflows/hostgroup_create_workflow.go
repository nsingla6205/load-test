package vcm_workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

const hostGroupActivityTimeout = 600 * time.Second

var (
	vcmJobActivity       activities.JobActivity
	vcmHostGroupActivity activities.HostGroupActivity
	svmActivity          activities.SvmActivity
)

// VcmCreateHostGroupWorkflow resolves the target SVM and creates the requested igroup.
func VcmCreateHostGroupWorkflow(ctx *orchestrator.Context, inputs common.WorkflowInputs, host *common.HostParams) error {
	activityCtx := ctx.WithActivityOptions(vcmHostGroupActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	if err := checkDeadline(ctx, inputs.Deadline, "mark-processing"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, activities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := checkDeadline(ctx, inputs.Deadline, "get-svm"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, inputs.Node, inputs.ExpectedSvmName).Get(activityCtx, nil); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}

	if err := checkDeadline(ctx, inputs.Deadline, "create-igroup"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	var igroupUUID string
	if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.CreateIgroup, inputs.ExpectedSvmName, host, inputs.Node).Get(activityCtx, &igroupUUID); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}

	donePayload, err := igroupOperationResponsePayload(host.HostName, igroupUUID, host.HostIQNs, host.OsType)
	if err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	return finishJob(activityCtx, operationID, nil, donePayload)
}

func vcmHostGroupActivityOptions() orchestrator.ActivityOptions {
	return orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: hostGroupActivityTimeout,
		RetryPolicy: &orchestrator.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
}
