package vcm_workflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
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
func VcmCreateVolumeWorkflow(ctx *orchestrator.Context, volume *datamodel.Volume, inputs common.WorkflowInputs, createVolumeParams *common.CreateVolumeParams) error {
	activityCtx := ctx.WithActivityOptions(vcmVolumeActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error, responsePayload *string) error {
		params := activities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, responsePayload)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return err
		}
		return workflowErr
	}

	if volume == nil || volume.VolumeAttributes == nil || inputs.Node == nil ||
		inputs.ExpectedSvmName == "" || createVolumeParams == nil {
		err := utilErrors.New("invalid workflow input: missing volume, node, or create parameters")
		wfLogger(ctx).Error("VCM volume create workflow rejected invalid input", "jobId", operationID, "error", err)
		return finishJob(err, nil)
	}

	hostGroupNames := hostGroupNamesFromCreateParams(createVolumeParams)

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

	// Pre-provisioning child workflow.
	preWorkflowFunc, preErr := selectVcmVolumeChildWorkflow(volume.VolumeAttributes.Protocols, PhasePre)
	if preErr != nil {
		return finishJob(preErr, nil)
	}
	childWorkflowParams := VcmVolumeChildWorkflowParams{
		Volume:         volume,
		Node:           inputs.Node,
		HostGroupNames: hostGroupNames,
		Deadline:       inputs.Deadline,
	}
	if err := checkDeadline(ctx, inputs.Deadline, "pre-provision"); err != nil {
		return finishJob(err, nil)
	}
	if err := orchestrator.ExecuteChildWorkflow(ctx, preWorkflowFunc, childWorkflowParams).Get(ctx, nil); err != nil {
		return finishJob(err, nil)
	}

	if err := checkDeadline(ctx, inputs.Deadline, "create-snapshot-policy"); err != nil {
		return finishJob(err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateSnapshotPolicyInONTAP, volume, inputs.Node).Get(activityCtx, nil); err != nil {
		return finishJob(err, nil)
	}

	// TODO(VCM): CreateVolumeInONTAP still uses VCP storage in auto-tiering paths; VCM passes nil snapshot/backup/aggrs.
	if err := checkDeadline(ctx, inputs.Deadline, "create-volume"); err != nil {
		return finishJob(err, nil)
	}
	var volCreateResponse *vsa.VolumeResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateVolumeInONTAP, volume, inputs.Node, nil, nil, nil).Get(activityCtx, &volCreateResponse); err != nil {
		return finishJob(err, nil)
	}
	volume.VolumeAttributes.ExternalUUID = volCreateResponse.ExternalUUID
	volume.UsedBytes = uint64(volCreateResponse.UsedBytes)

	postWorkflowFunc, postErr := selectVcmVolumeChildWorkflow(volume.VolumeAttributes.Protocols, PhasePost)
	if postErr != nil {
		return finishJob(postErr, nil)
	}
	childWorkflowParams.VolCreateResponse = volCreateResponse
	if err := checkDeadline(ctx, inputs.Deadline, "post-provision"); err != nil {
		return finishJob(err, nil)
	}
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

	if err := checkDeadline(ctx, inputs.Deadline, "get-volume"); err != nil {
		return finishJob(err, nil)
	}
	var volFromONTAP *vsa.VolumeResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetVolumeFromONTAP, volume, inputs.Node).Get(activityCtx, &volFromONTAP); err != nil {
		return finishJob(err, nil)
	}

	if err := checkDeadline(ctx, inputs.Deadline, "build-response"); err != nil {
		return finishJob(err, nil)
	}
	var enrichment *vsa.VolumeOperationEnrichment
	if err := orchestrator.ExecuteActivity(activityCtx, vcmVolumeActivity.BuildVolumeOperationResponseEnrichment, volFromONTAP, inputs.ExpectedSvmName, inputs.Node).Get(activityCtx, &enrichment); err != nil {
		return finishJob(err, nil)
	}

	donePayload, err := volumeOperationResponsePayload(volFromONTAP, enrichment)
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
