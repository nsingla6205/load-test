package vcm_workflows

import (
	"encoding/json"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// VcmUpdateHostGroupWorkflow updates an ONTAP igroup initiator list.
func VcmUpdateHostGroupWorkflow(ctx *orchestrator.Context, node *models.Node, igroupName string, hosts []string, expectedSvmName string) error {
	activityCtx := ctx.WithActivityOptions(vcmHostGroupActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error, responsePayload *string) error {
		params := activities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, responsePayload)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return err
		}
		return workflowErr
	}

	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, activities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, node, expectedSvmName).Get(activityCtx, nil); err != nil {
		return finishJob(err, nil)
	}

	var osType string
	if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.UpdateIgroup, igroupName, hosts, expectedSvmName, node).Get(activityCtx, &osType); err != nil {
		return finishJob(err, nil)
	}

	donePayload, err := igroupUpdateOperationResponsePayload(igroupName, hosts, osType)
	if err != nil {
		return finishJob(err, nil)
	}
	return finishJob(nil, donePayload)
}

// igroupUpdateOperationResponsePayload marshals the updated igroup into a JSON response payload.
func igroupUpdateOperationResponsePayload(name string, hosts []string, osType string) (*string, error) {
	payload, err := json.Marshal(map[string]any{
		"igroups": map[string]any{
			"name":   name,
			"hosts":  hosts,
			"osType": osType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal igroup operation response: %w", err)
	}
	result := string(payload)
	return &result, nil
}
