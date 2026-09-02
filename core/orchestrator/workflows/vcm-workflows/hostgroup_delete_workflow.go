package vcm_workflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	vcmactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/vcm"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// VcmDeleteHostGroupWorkflow deletes an ONTAP igroup for the requested HostGroup.
func VcmDeleteHostGroupWorkflow(ctx *orchestrator.Context, node *models.Node, igroupName string, expectedSvmName string) error {
	activityCtx := ctx.WithActivityOptions(vcmHostGroupActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error) error {
		params := vcmactivities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, nil)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return err
		}
		return workflowErr
	}

	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, vcmactivities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, node, expectedSvmName).Get(activityCtx, nil); err != nil {
		return finishJob(err)
	}

	if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.DeleteIgroup, igroupName, expectedSvmName, node).Get(activityCtx, nil); err != nil {
		return finishJob(err)
	}

	return finishJob(nil)
}
