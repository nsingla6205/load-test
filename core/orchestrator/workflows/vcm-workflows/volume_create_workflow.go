package vcm_workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	vcmactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

var volumeCreateActivity activities.VolumeCreateActivity

const volumeActivityTimeout = 600 * time.Second

// VcmCreateVolumeWorkflow orchestrates VCM volume creation against ONTAP.
func VcmCreateVolumeWorkflow(ctx *orchestrator.Context, volume *datamodel.Volume, node *models.Node, createVolumeParams *common.CreateVolumeParams) error {
	activityCtx := ctx.WithActivityOptions(vcmVolumeActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error, responsePayload *string) error {
		params := vcmactivities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, responsePayload)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return returnFinishJobError(workflowErr, err)
		}
		return workflowErr
	}

	if volume == nil || volume.Svm == nil || volume.Svm.Name == "" ||
		volume.VolumeAttributes == nil || node == nil || createVolumeParams == nil {
		err := utilErrors.New("invalid workflow input: missing volume, node, or create parameters")
		wfLogger(ctx).Error("VCM volume create workflow rejected invalid input", "jobId", operationID, "error", err)
		return finishJob(err, nil)
	}

	expectedSvmName := volume.Svm.Name
	hostGroupNames := hostGroupNamesFromCreateParams(createVolumeParams)

	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, vcmactivities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, node, expectedSvmName).Get(activityCtx, nil); err != nil {
		return finishJob(err, nil)
	}

	// Pre-provisioning child workflow.
	preWorkflowFunc, preErr := selectVcmVolumeChildWorkflow(volume.VolumeAttributes.Protocols, PhasePre)
	if preErr != nil {
		return finishJob(preErr, nil)
	}
	childWorkflowParams := VcmVolumeChildWorkflowParams{
		Volume:         volume,
		Node:           node,
		HostGroupNames: hostGroupNames,
	}
	if err := orchestrator.ExecuteChildWorkflow(ctx, preWorkflowFunc, childWorkflowParams).Get(ctx, nil); err != nil {
		return finishJob(err, nil)
	}

	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateSnapshotPolicyInONTAP, volume, node).Get(activityCtx, nil); err != nil {
		return finishJob(err, nil)
	}

	// TODO(VCM): CreateVolumeInONTAP still uses VCP storage in auto-tiering paths; VCM passes nil snapshot/backup/aggrs.
	var volCreateResponse *vsa.VolumeResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateVolumeInONTAP, volume, node, nil, nil, nil).Get(activityCtx, &volCreateResponse); err != nil {
		return finishJob(err, nil)
	}
	volume.VolumeAttributes.ExternalUUID = volCreateResponse.ExternalUUID
	volume.UsedBytes = uint64(volCreateResponse.UsedBytes)

	postWorkflowFunc, postErr := selectVcmVolumeChildWorkflow(volume.VolumeAttributes.Protocols, PhasePost)
	if postErr != nil {
		return finishJob(postErr, nil)
	}
	childWorkflowParams.VolCreateResponse = volCreateResponse
	if workflowSlice, ok := postWorkflowFunc.([]interface{}); ok {
		for _, workflowFunc := range workflowSlice {
			if err := orchestrator.ExecuteChildWorkflow(ctx, workflowFunc, childWorkflowParams).Get(ctx, nil); err != nil {
				return finishJob(err, nil)
			}
		}
	} else {
		if err := orchestrator.ExecuteChildWorkflow(ctx, postWorkflowFunc, childWorkflowParams).Get(ctx, nil); err != nil {
			return finishJob(err, nil)
		}
	}

	var volFromONTAP *vsa.VolumeResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetVolumeFromONTAP, volume, node).Get(activityCtx, &volFromONTAP); err != nil {
		return finishJob(err, nil)
	}

	donePayload, err := volumeCreateOperationResponsePayload(volume, volFromONTAP)
	if err != nil {
		return finishJob(err, nil)
	}
	return finishJob(nil, donePayload)
}

// hostGroupNamesFromCreateParams returns igroup names from workflow input params (already []string).
func hostGroupNamesFromCreateParams(params *common.CreateVolumeParams) []string {
	if params == nil || params.BlockDevices == nil {
		return nil
	}
	hostGroupNames := make([]string, 0)
	for _, blockDevice := range *params.BlockDevices {
		hostGroupNames = append(hostGroupNames, blockDevice.HostGroupNames...)
	}
	return hostGroupNames
}

func vcmVolumeActivityOptions() orchestrator.ActivityOptions {
	return orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: volumeActivityTimeout,
		RetryPolicy: &orchestrator.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
}
