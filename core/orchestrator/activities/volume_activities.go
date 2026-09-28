package activities

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/apperror"
)

// VolumeActivity performs ONTAP volume update operations for VCM workflows.
type VolumeActivity struct{}

// UpdateBlockDeviceHostGroups syncs LUN host-group mappings to the desired igroup names.
func (VolumeActivity) UpdateBlockDeviceHostGroups(ctx context.Context, lun vsa.LunResponse, svmName string, desiredHostGroupNames []string, node *models.Node) error {
	logger := util.GetLogger(ctx)
	orchestrator.RecordHeartbeat(ctx, "Starting UpdateBlockDeviceHostGroups activity")
	if lun.ExternalUUID == "" || lun.Name == "" {
		return apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			utilErrors.New("invalid LUN input for block device update")), true)
	}
	if svmName == "" {
		return apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			utilErrors.New("invalid SVM input for block device update")), true)
	}
	if len(desiredHostGroupNames) == 0 {
		return apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			utilErrors.NewUserInputValidationErr("igroups must contain at least one igroup name")), true)
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return apperror.WrapAsAppError(err, false)
	}

	currentHostGroupNames := lun.IgroupNames
	toCreate, toDelete := HostGroupsUpdateDiffForVolume(currentHostGroupNames, desiredHostGroupNames)

	for _, hostGroupName := range toDelete {
		orchestrator.RecordHeartbeat(ctx, "Unmapping host group from LUN", "hostGroup", hostGroupName)
		exists, igroup, err := provider.IgroupExists(hostGroupName, &svmName)
		if err != nil {
			return apperror.WrapAsAppError(err, false)
		}
		if !exists || igroup == nil || igroup.UUID == nil {
			continue
		}
		if err := provider.LunMapDelete(vsa.LunMapDeleteParams{
			LunUUID:    lun.ExternalUUID,
			IGroupUUID: *igroup.UUID,
		}); err != nil {
			logger.ErrorContext(ctx, "Failed to unmap host group from LUN", "hostGroup", hostGroupName, "error", err)
			return apperror.WrapAsAppError(err, false)
		}
	}

	if len(toCreate) > 0 {
		orchestrator.RecordHeartbeat(ctx, "Mapping host groups to LUN")
		err = provider.LunMapCreate(vsa.LunMapCreateParams{
			LunName:    lun.Name,
			SvmName:    svmName,
			IGroupName: toCreate,
		})
		if err != nil && !utilErrors.IsConflictErr(err) {
			return apperror.WrapAsAppError(err, false)
		}
	}

	orchestrator.RecordHeartbeat(ctx, "Finished UpdateBlockDeviceHostGroups activity")
	return nil
}

// GetLunInONTAP retrieves a LUN from ONTAP by SVM, volume, and LUN name.
func (VolumeActivity) GetLunInONTAP(ctx context.Context, lunName, volumeName, svmName string, node *models.Node) (vsa.LunResponse, error) {
	orchestrator.RecordHeartbeat(ctx, "Starting GetLunInONTAP activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsa.LunResponse{}, apperror.WrapAsAppError(err, false)
	}

	lun, err := LunGet(ctx, lunName, volumeName, svmName, provider)
	if err != nil {
		return vsa.LunResponse{}, apperror.WrapAsAppError(err, false)
	}

	orchestrator.RecordHeartbeat(ctx, "Finished GetLunInONTAP activity")
	return *lun, nil
}

// IsLunNotFoundError reports whether an ONTAP LUN lookup failed because the LUN does not exist.
func IsLunNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	if utilErrors.IsNotFoundErrForObjectTypeInChain(err, "lun") {
		return true
	}
	// go-workflows serializes activity errors into a message-only unwrap chain, so
	// inspect each wrapped level rather than only the outermost message.
	for current := err; current != nil; current = errors.Unwrap(current) {
		if strings.Contains(strings.ToLower(current.Error()), "lun not found") {
			return true
		}
	}
	return false
}

// PopulateVolumeFromONTAPResponse hydrates a datamodel volume from an ONTAP volume response.
func (VolumeActivity) PopulateVolumeFromONTAPResponse(ctx context.Context, volume *datamodel.Volume, volResponse *vsa.VolumeResponse) (*datamodel.Volume, error) {
	orchestrator.RecordHeartbeat(ctx, "Starting PopulateVolumeFromONTAPResponse activity")
	if volume == nil || volResponse == nil {
		return nil, apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("volume and ONTAP response are required")), true)
	}
	if volume.Svm == nil || volume.Svm.Name == "" {
		return nil, apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("volume SVM is required")), true)
	}

	volume.Name = volResponse.Name
	volume.SizeInBytes = volResponse.Size
	if volume.VolumeAttributes == nil {
		volume.VolumeAttributes = &datamodel.VolumeAttributes{}
	}
	volume.VolumeAttributes.ExternalUUID = volResponse.ExternalUUID
	volume.VolumeAttributes.SnapReserve = volResponse.SnapReserve
	volume.VolumeAttributes.SnapshotDirectory = volResponse.SnapshotDirectoryAccessEnabled
	if volume.VolumeAttributes.FileProperties == nil {
		volume.VolumeAttributes.FileProperties = &datamodel.FileProperties{
			ExportPolicy: &datamodel.ExportPolicy{},
		}
	}
	if volResponse.ExportPolicyName != "" {
		volume.VolumeAttributes.FileProperties.ExportPolicy.ExportPolicyName = volResponse.ExportPolicyName
	}
	if volResponse.JunctionPath != "" {
		volume.VolumeAttributes.FileProperties.JunctionPath = volResponse.JunctionPath
	}
	if volResponse.UnixPermissions != "" {
		volume.VolumeAttributes.FileProperties.UnixPermissions = volResponse.UnixPermissions
	}
	if volResponse.SecurityStyle != "" {
		volume.VolumeAttributes.FileProperties.SecurityStyle = volResponse.SecurityStyle
	}
	if volResponse.SnapshotPolicyName != "" {
		volume.SnapshotPolicy = &datamodel.SnapshotPolicy{
			Name: volResponse.SnapshotPolicyName,
		}
	}

	orchestrator.RecordHeartbeat(ctx, "Finished PopulateVolumeFromONTAPResponse activity")
	return volume, nil
}

// BuildVolumeOperationResponseEnrichment loads block-device and export-policy payloads for LRO responses.
func (VolumeActivity) BuildVolumeOperationResponseEnrichment(ctx context.Context, ontapVol *vsa.VolumeResponse, svmName string, node *models.Node) (*VolumeOperationEnrichment, error) {
	orchestrator.RecordHeartbeat(ctx, "Starting BuildVolumeOperationResponseEnrichment activity")
	if ontapVol == nil {
		return nil, apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			utilErrors.New("ONTAP volume response is required")), true)
	}
	if strings.TrimSpace(svmName) == "" {
		return nil, apperror.WrapAsAppError(vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			utilErrors.New("SVM name is required")), true)
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, apperror.WrapAsAppError(err, false)
	}

	enrichment, err := BuildVolumeOperationEnrichment(provider, svmName, ontapVol)
	if err != nil {
		return nil, apperror.WrapAsAppError(err, false)
	}

	orchestrator.RecordHeartbeat(ctx, "Finished BuildVolumeOperationResponseEnrichment activity")
	return enrichment, nil
}
