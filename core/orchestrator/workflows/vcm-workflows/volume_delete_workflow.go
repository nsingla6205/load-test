package vcm_workflows

import (
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

var (
	volumeDeleteActivity activities.VolumeDeleteActivity
	volumeUpdateActivity activities.VolumeUpdateActivity
)

// VcmDeleteVolumeWorkflow deletes a volume from ONTAP for VCM.
func VcmDeleteVolumeWorkflow(ctx *orchestrator.Context, volume *datamodel.Volume, inputs common.WorkflowInputs) error {
	activityCtx := ctx.WithActivityOptions(vcmVolumeDeleteActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	finishJob := func(workflowErr error) error {
		params := activities.JobProgressParams(operationID, vcmdb.JobState(""), workflowErr, nil)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, params).Get(activityCtx, nil); err != nil {
			return err
		}
		return workflowErr
	}

	if volume == nil || inputs.ExpectedSvmName == "" ||
		volume.VolumeAttributes == nil || volume.VolumeAttributes.FileProperties == nil ||
		volume.VolumeAttributes.FileProperties.ExportPolicy == nil ||
		volume.VolumeAttributes.ExternalUUID == "" {
		return finishJob(utilErrors.New("invalid workflow input: missing volume/SVM attributes"))
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

	if err := checkDeadline(ctx, inputs.Deadline, "get-volume"); err != nil {
		return finishJob(err)
	}
	var volResponse vsa.VolumeResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetVolumeFromONTAP, volume, inputs.Node).Get(activityCtx, &volResponse); err != nil {
		if !isVolumeNotFoundOnONTAP(err) {
			return finishJob(err)
		}
		if err := checkDeadline(ctx, inputs.Deadline, "load-recovery-payload"); err != nil {
			return finishJob(err)
		}
		var job *vcmdb.Job
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.GetJob, operationID).Get(activityCtx, &job); err != nil {
			return finishJob(err)
		}
		recovery, err := vcmdb.UnmarshalPayload[vcmdb.RecoveryPayload](job.RecoveryPayload)
		if err != nil {
			return finishJob(err)
		}
		volume.Name = recovery.VolumeName
		volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName = recovery.VolumeName
	} else {
		volume.Name = volResponse.Name
		volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName = volResponse.ExportPolicyName
		recoveryPayload, err := vcmdb.MarshalPayload(vcmdb.RecoveryPayload{
			VolumeName: volResponse.Name,
		})
		if err != nil {
			return finishJob(err)
		}
		if err := checkDeadline(ctx, inputs.Deadline, "persist-recovery-payload"); err != nil {
			return finishJob(err)
		}
		if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, vcmdb.UpdateJobProgressParams{
			JobID:           operationID,
			State:           vcmdb.JobStateProcessing,
			RecoveryPayload: recoveryPayload,
		}).Get(activityCtx, nil); err != nil {
			return finishJob(err)
		}
	}

	if volume.Name == "" {
		return finishJob(utilErrors.New("cannot delete volume: ONTAP volume name could not be resolved"))
	}

	if err := checkDeadline(ctx, inputs.Deadline, "delete-volume"); err != nil {
		return finishJob(err)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, volumeDeleteActivity.DeleteVolumeInONTAP, volume.VolumeAttributes.ExternalUUID, volume.Name, inputs.Node).Get(activityCtx, nil); err != nil {
		return finishJob(err)
	}

	if err := checkDeadline(ctx, inputs.Deadline, "delete-export-policy"); err != nil {
		return finishJob(err)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, volumeDeleteActivity.DeleteExportPolicy, volume, inputs.Node).Get(activityCtx, nil); err != nil {
		return finishJob(err)
	}

	if err := checkDeadline(ctx, inputs.Deadline, "delete-snapshot-policy"); err != nil {
		return finishJob(err)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, volumeDeleteActivity.DeleteSnapshotPolicyInONTAP, volume.Name, inputs.Node).Get(activityCtx, nil); err != nil {
		return finishJob(err)
	}

	// Intentionally omitted vs the GCP DeleteVolumeWorkflow (VCM has no VCP volume DB and a narrower API surface):
	// - EnforceDeleteProtection: VCM does not expose delete-protection / restricted-actions on volumes.
	// - Per-volume QoS policy delete: VCM volumes have no throughputMibps/iops; pool/SVM QoS is managed at pool create.
	// - DeleteIgroups / DeleteIgroupsFromBlockProperties: host groups are separate VCM resources (see hostgroup delete workflow).
	// - DeleteVolume (VCP DB), DeleteVolumeAssociatedSnapshots, DeleteAssociatedQuotaRules, CleanupAutoGeneratedVPG: no VCP persistence for VCM volumes.
	//
	// Not yet implemented (add when the corresponding VCM APIs/features land):
	// - DeleteSnapmirrorInONTAP: snapmirror / backup vault.
	// - DeleteCifsShareIfSMB and SmbTeardownWorkflow: SMB protocol volumes.
	// - DetermineIfVolumeIsLastFilesVolume / DeleteLDAPConfiguration: LDAP-enabled pools.

	return finishJob(nil)
}

// isVolumeNotFoundOnONTAP reports whether ONTAP no longer has the target volume.
func isVolumeNotFoundOnONTAP(err error) bool {
	if err == nil {
		return false
	}
	if utilErrors.IsNotFoundErrForObjectTypeInChain(err, "volume") {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "entry doesn't exist") || strings.Contains(msg, "entry not found") ||
		(strings.Contains(msg, "volume") && (strings.Contains(msg, "not found") || strings.Contains(msg, "does not exist")))
}

func vcmVolumeDeleteActivityOptions() orchestrator.ActivityOptions {
	return orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: volumeActivityTimeout,
		RetryPolicy: &orchestrator.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
}
