package vcm_workflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// VcmUpdateHostGroupWorkflow updates an ONTAP igroup initiator list.
func VcmUpdateHostGroupWorkflow(ctx *orchestrator.Context, inputs common.WorkflowInputs, igroupName string, hosts []string) error {
	activityCtx := ctx.WithActivityOptions(vcmHostGroupActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error, responsePayload *string) error {
		params := activities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, responsePayload)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return err
		}
		return workflowErr
	}

	if err := checkDeadline(ctx, inputs.Deadline, "mark-processing"); err != nil {
		return finishJob(err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, activities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := checkDeadline(ctx, inputs.Deadline, "get-svm"); err != nil {
		return finishJob(err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, inputs.Node, inputs.ExpectedSvmName).Get(activityCtx, nil); err != nil {
		return finishJob(err, nil)
	}

	if err := checkDeadline(ctx, inputs.Deadline, "update-igroup"); err != nil {
		return finishJob(err, nil)
	}
	var osType string
	if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.UpdateIgroup, igroupName, hosts, inputs.ExpectedSvmName, inputs.Node).Get(activityCtx, &osType); err != nil {
		return finishJob(err, nil)
	}

	donePayload, err := igroupOperationResponsePayload(igroupName, hosts, osType)
	if err != nil {
		return finishJob(err, nil)
	}
	return finishJob(nil, donePayload)
}
