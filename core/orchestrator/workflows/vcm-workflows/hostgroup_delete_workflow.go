package vcm_workflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// VcmDeleteHostGroupWorkflow deletes an ONTAP igroup for the requested HostGroup.
func VcmDeleteHostGroupWorkflow(ctx *orchestrator.Context, inputs common.WorkflowInputs, igroupName string) error {
	activityCtx := ctx.WithActivityOptions(vcmHostGroupActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error) error {
		params := activities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, nil)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return err
		}
		return workflowErr
	}

	if err := checkDeadline(ctx, inputs.Deadline, "mark-processing"); err != nil {
		return finishJob(err)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, activities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := checkDeadline(ctx, inputs.Deadline, "get-svm"); err != nil {
		return finishJob(err)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, inputs.Node, inputs.ExpectedSvmName).Get(activityCtx, nil); err != nil {
		return finishJob(err)
	}

	if err := checkDeadline(ctx, inputs.Deadline, "delete-igroup"); err != nil {
		return finishJob(err)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.DeleteIgroup, igroupName, inputs.ExpectedSvmName, inputs.Node).Get(activityCtx, nil); err != nil {
		return finishJob(err)
	}

	return finishJob(nil)
}
