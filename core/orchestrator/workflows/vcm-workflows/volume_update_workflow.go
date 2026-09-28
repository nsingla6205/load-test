package vcm_workflows

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

var vcmVolumeActivity activities.VolumeActivity

// VcmUpdateVolumeWorkflow updates an ONTAP volume for VCM.
func VcmUpdateVolumeWorkflow(ctx *orchestrator.Context, volume *datamodel.Volume, node *models.Node, params *common.UpdateVolumeParams) error {
	activityCtx := ctx.WithActivityOptions(vcmVolumeActivityOptions())
	operationID := orchestrator.GetInfo(ctx).WorkflowExecution.ID

	if volume == nil || volume.Svm == nil || volume.Svm.Name == "" || volume.VolumeAttributes == nil || params == nil {
		err := utilErrors.New("invalid workflow input: missing volume, node, or update parameters")
		wfLogger(ctx).Error("VCM volume update workflow rejected invalid input", "jobId", operationID, "error", err)
		return finishJob(activityCtx, operationID, err, nil)
	}

	deadline := params.Deadline

	if err := checkDeadline(ctx, deadline, "mark-processing"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, vcmJobActivity.UpdateJobProgress, activities.JobProgressParams(operationID, vcmdb.JobStateProcessing, nil, nil)).Get(activityCtx, nil); err != nil {
		return err
	}

	if err := checkDeadline(ctx, deadline, "get-svm"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, svmActivity.GetSvmByName, node, volume.Svm.Name).Get(activityCtx, nil); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}

	if err := checkDeadline(ctx, deadline, "get-volume"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	var volResponse vsa.VolumeResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetVolumeFromONTAP, volume, node).Get(activityCtx, &volResponse); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	if err := checkDeadline(ctx, deadline, "populate-volume"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	if err := orchestrator.ExecuteActivity(activityCtx, vcmVolumeActivity.PopulateVolumeFromONTAPResponse, volume, &volResponse).Get(activityCtx, volume); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}

	var blockLun vsa.LunResponse
	blockLunLookupNeeded := (params.QuotaInBytes > 0 && params.QuotaInBytes < volResponse.Size) ||
		(params.SnapReserve != nil && volume.VolumeAttributes != nil && *params.SnapReserve != volume.VolumeAttributes.SnapReserve) ||
		isLunResizeRequired(params, volume) || (len(params.BlockDevices) > 0 && params.BlockDevices[0] != nil && len(params.BlockDevices[0].HostGroupNames) > 0)
	if blockLunLookupNeeded {
		if err := checkDeadline(ctx, deadline, "get-lun"); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
		lunName := utils.GetLunName(volume.Name)
		if err := orchestrator.ExecuteActivity(activityCtx, vcmVolumeActivity.GetLunInONTAP, lunName, volume.Name, volume.Svm.Name, node).Get(activityCtx, &blockLun); err != nil {
			if !activities.IsLunNotFoundError(err) {
				return finishJob(activityCtx, operationID, err, nil)
			}
		}
	}
	isSANVolume := blockLun.ExternalUUID != ""

	// TODO(VCM): Remove this workflow-level size validation once CLH adds equivalent
	// request validation on the volume update PATCH path.
	if params.QuotaInBytes > 0 {
		minQuotaInBytes := utils.MinQuotaInBytesVolumeForVolume
		maxQuotaProtocols := []string{utils.ProtocolNFSv3}
		if isSANVolume {
			maxQuotaProtocols = []string{utils.ProtocolISCSI}
		}
		maxQuotaInBytes := factorycommon.GetMaxQuotaForVolume(maxQuotaProtocols)
		if uint64(params.QuotaInBytes) < minQuotaInBytes || uint64(params.QuotaInBytes) > maxQuotaInBytes {
			return finishJob(activityCtx, operationID, utilErrors.NewUserInputValidationErr(fmt.Sprintf("Invalid volume capacity %s. Must be between %s and %s.",
				utils.FmtUint64Bytes(uint64(params.QuotaInBytes)), utils.FmtUint64Bytes(minQuotaInBytes),
				utils.FmtUint64Bytes(maxQuotaInBytes))), nil)
		}

		if params.QuotaInBytes < volResponse.Size {
			if isSANVolume {
				return finishJob(activityCtx, operationID, utilErrors.NewUserInputValidationErr("volume size cannot be reduced"), nil)
			}
			if volResponse.UsedBytes > params.QuotaInBytes {
				err := fmt.Errorf("cannot reduce volume size to %s as it is currently using %s. Free up space or choose a larger size",
					utils.FmtUint64Bytes(uint64(params.QuotaInBytes)),
					utils.FmtUint64Bytes(uint64(volResponse.UsedBytes)))
				return finishJob(activityCtx, operationID, vsaerrors.NewVCPError(vsaerrors.ErrVolumeSizeTooSmall, err), nil)
			}
		}
	}

	if params.SnapshotPolicy != nil {
		params.SnapshotPolicy.Name = volume.Name

		attachedPolicyName := volResponse.SnapshotPolicyName
		updatingPolicy := factorycommon.PopulateSnapshotPolicyFromParams(params.SnapshotPolicy)
		updatingPolicy.Name = volume.Name

		if attachedPolicyName == "" || attachedPolicyName == models.DefaultONTAPSnapshotPolicyName || attachedPolicyName != volume.Name {
			if len(params.SnapshotPolicy.Schedules) == 0 {
				return finishJob(activityCtx, operationID, utilErrors.NewUserInputValidationErr("no existing snapshot policy found for the volume and no schedules provided in the update request. Cannot create a new snapshot policy without schedules"), nil)
			}
			volume.SnapshotPolicy = updatingPolicy
			if err := checkDeadline(ctx, deadline, "create-snapshot-policy"); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			if err := orchestrator.ExecuteActivity(activityCtx, volumeCreateActivity.CreateSnapshotPolicyInONTAP, volume, node).Get(activityCtx, nil); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
		} else {
			if err := checkDeadline(ctx, deadline, "get-snapshot-policy"); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			var currentPolicy datamodel.SnapshotPolicy
			if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetSnapshotPolicyInONTAP, node, attachedPolicyName).Get(activityCtx, &currentPolicy); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			if len(updatingPolicy.Schedules) == 0 {
				updatingPolicy.Schedules = currentPolicy.Schedules
			}
			if !params.SnapshotPolicyEnabledSet {
				updatingPolicy.IsEnabled = currentPolicy.IsEnabled
			}
			if err := checkDeadline(ctx, deadline, "update-snapshot-policy"); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.UpdateSnapshotPolicyInOntap, node, &currentPolicy, updatingPolicy).Get(activityCtx, nil); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			volume.SnapshotPolicy = updatingPolicy
		}
	}

	if params.SnapReserve != nil && volume.VolumeAttributes != nil && isSANVolume &&
		*params.SnapReserve != volume.VolumeAttributes.SnapReserve {
		if err := factorycommon.ValidateSANSnapReserveIncrease(params.SnapReserve, volume.VolumeAttributes.SnapReserve, volume.SizeInBytes, params.QuotaInBytes); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
	}

	// TODO(VCM): UpdateVolumeInONTAP still uses VCP storage (SE) in auto-tiering paths; VCM has no SE.
	// Refactor once auto-tiering is supported for VCM (e.g. dedicated activity or VCM-safe tiering path).
	if factorycommon.IsVolumeUpdateRequired(&volResponse, params, volume) {
		if err := checkDeadline(ctx, deadline, "update-volume"); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
		if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.UpdateVolumeInONTAP, volume, params, node).Get(activityCtx, nil); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
	}

	if isLunResizeRequired(params, volume) {
		if blockLun.ExternalUUID != "" {
			volume.VolumeAttributes.BlockDevices = &[]datamodel.BlockDevice{{
				Name:       utils.ExtractLunNameFromPath(blockLun.Name),
				Identifier: blockLun.SerialNumber,
				Size:       blockLun.Size,
				LunUUID:    blockLun.ExternalUUID,
			}}
			if err := checkDeadline(ctx, deadline, "refresh-volume-for-lun"); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetVolumeFromONTAP, volume, node).Get(activityCtx, &volResponse); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			if err := checkDeadline(ctx, deadline, "update-lun"); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.UpdateLun, volume, &volResponse, node).Get(activityCtx, nil); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
		}
	}

	if len(params.BlockDevices) > 0 && params.BlockDevices[0] != nil && len(params.BlockDevices[0].HostGroupNames) > 0 {
		hostGroupNames := params.BlockDevices[0].HostGroupNames
		if err := checkDeadline(ctx, deadline, "validate-host-groups"); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
		if err := orchestrator.ExecuteActivity(activityCtx, vcmHostGroupActivity.ValidateHostGroupsExistInONTAP, hostGroupNames, volume.Svm.Name, node).Get(activityCtx, nil); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
		if blockLun.ExternalUUID == "" {
			return finishJob(activityCtx, operationID, utilErrors.NewUserInputValidationErr("block device host group update is only supported for SAN volumes"), nil)
		}
		if err := checkDeadline(ctx, deadline, "update-block-device-host-groups"); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
		if err := orchestrator.ExecuteActivity(activityCtx, vcmVolumeActivity.UpdateBlockDeviceHostGroups, blockLun, volume.Svm.Name, hostGroupNames, node).Get(activityCtx, nil); err != nil {
			return finishJob(activityCtx, operationID, err, nil)
		}
	}

	if volume.VolumeAttributes.FileProperties != nil && params.FileProperties != nil {
		if params.FileProperties.JunctionPath != "" &&
			params.FileProperties.JunctionPath != volume.VolumeAttributes.FileProperties.JunctionPath {
			if err := checkDeadline(ctx, deadline, "update-junction-path"); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.UpdateJunctionPathInONTAP, volume, params.FileProperties.JunctionPath, node).Get(activityCtx, nil); err != nil {
				return finishJob(activityCtx, operationID, err, nil)
			}
			volume.VolumeAttributes.FileProperties.JunctionPath = params.FileProperties.JunctionPath
		}

		if params.FileProperties.ExportPolicy != nil {
			policyName := volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName
			if policyName != "" && policyName != models.DefaultExportPolicyName {
				if err := checkDeadline(ctx, deadline, "get-export-policy"); err != nil {
					return finishJob(activityCtx, operationID, err, nil)
				}
				var currentExportPolicy datamodel.ExportPolicy
				if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetExportPolicyInONTAP, node, policyName, volume.Svm.Name).Get(activityCtx, &currentExportPolicy); err != nil {
					return finishJob(activityCtx, operationID, err, nil)
				}
				volume.VolumeAttributes.FileProperties.ExportPolicy = &currentExportPolicy

				if factorycommon.IsExportPolicyRulesUpdateRequired(volume.VolumeAttributes.FileProperties.ExportPolicy, params.FileProperties.ExportPolicy) {
					updatePolicy := params.FileProperties.ExportPolicy
					updatePolicy.ExportPolicyName = volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName
					if err := checkDeadline(ctx, deadline, "update-export-policy"); err != nil {
						return finishJob(activityCtx, operationID, err, nil)
					}
					if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.UpdateExportPolicyRulesInONTAP, volume, updatePolicy, node).Get(activityCtx, nil); err != nil {
						return finishJob(activityCtx, operationID, err, nil)
					}
				}
			}
		}
	}

	if err := checkDeadline(ctx, deadline, "refresh-volume"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	var updatedVolResponse vsa.VolumeResponse
	if err := orchestrator.ExecuteActivity(activityCtx, volumeUpdateActivity.GetVolumeFromONTAP, volume, node).Get(activityCtx, &updatedVolResponse); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}

	if err := checkDeadline(ctx, deadline, "build-response"); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	var enrichment *activities.VolumeOperationEnrichment
	if err := orchestrator.ExecuteActivity(activityCtx, vcmVolumeActivity.BuildVolumeOperationResponseEnrichment, &updatedVolResponse, volume.Svm.Name, node).Get(activityCtx, &enrichment); err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}

	donePayload, err := volumeOperationResponsePayload(&updatedVolResponse, enrichment)
	if err != nil {
		return finishJob(activityCtx, operationID, err, nil)
	}
	return finishJob(activityCtx, operationID, nil, donePayload)
}
func isLunResizeRequired(params *common.UpdateVolumeParams, volume *datamodel.Volume) bool {
	if volume == nil || volume.VolumeAttributes == nil || params == nil {
		return false
	}
	return params.QuotaInBytes > volume.SizeInBytes ||
		(params.SnapReserve != nil && *params.SnapReserve != volume.VolumeAttributes.SnapReserve)
}
