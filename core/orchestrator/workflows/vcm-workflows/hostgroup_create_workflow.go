package vcm_workflows

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	vcmactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

const hostGroupActivityTimeout = 600 * time.Second

var (
	vcmJobActivity       vcmactivities.JobActivity
	vcmHostGroupActivity vcmactivities.HostGroupActivity
	svmActivity          activities.SvmActivity
)

// VcmCreateHostGroupWorkflow resolves the target SVM and creates the requested igroup.
func VcmCreateHostGroupWorkflow(ctx *orchestrator.Context, node *models.Node, host *common.HostParams, expectedSvmName string) error {
	activityCtx := ctx.WithActivityOptions(vcmHostGroupActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error, responsePayload *string) error {
		params := vcmactivities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, responsePayload)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return returnFinishJobError(workflowErr, err)
		}
		return workflowErr
	}

	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, vcmactivities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, node, expectedSvmName).Get(activityCtx, nil); err != nil {
		return finishJob(err, nil)
	}

	if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.CreateIgroup, expectedSvmName, host, node).Get(activityCtx, nil); err != nil {
		return finishJob(err, nil)
	}

	donePayload, err := igroupCreateOperationResponsePayload(host)
	if err != nil {
		return finishJob(err, nil)
	}
	return finishJob(nil, donePayload)
}

// igroupCreateOperationResponsePayload marshals the created igroup into a JSON response payload.
func igroupCreateOperationResponsePayload(host *common.HostParams) (*string, error) {
	payload, err := json.Marshal(map[string]any{
		"igroups": map[string]any{
			"name":   host.HostName,
			"hosts":  host.HostIQNs,
			"osType": host.OsType,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal igroup operation response: %w", err)
	}
	result := string(payload)
	return &result, nil
}

func vcmHostGroupActivityOptions() orchestrator.ActivityOptions {
	return orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: hostGroupActivityTimeout,
		RetryPolicy: &orchestrator.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
}

// returnFinishJobError preserves the workflow failure when terminal job persistence also fails.
func returnFinishJobError(workflowErr, updateErr error) error {
	if workflowErr != nil {
		return workflowErr
	}
	return updateErr
}
