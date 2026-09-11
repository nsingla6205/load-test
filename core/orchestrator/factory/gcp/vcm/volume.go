package vcm

import (
	"context"
	"errors"
	"fmt"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	vcmworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

// GetVolume returns a VCM volume from a single ONTAP volume GET by volume UUID.
func (o *VCMOrchestrator) GetVolume(ctx context.Context, volumeUUID string, _ bool) (*models.Volume, error) {
	svmName, provider, err := ontapProviderFromStore(ctx, o.store)
	if err != nil {
		return nil, fmt.Errorf("GetVolume: %w", err)
	}

	ontapVolume, err := provider.GetVolume(vsa.GetVolumeParams{
		UUID:    volumeUUID,
		SvmName: svmName,
	})
	if err != nil {
		return nil, fmt.Errorf("GetVolume: read ONTAP volume: %w", err)
	}

	return volumeModelFromONTAP(ontapVolume), nil
}

func volumeModelFromONTAP(ontapVolume *vsa.VolumeResponse) *models.Volume {
	volume := &models.Volume{
		BaseModel: models.BaseModel{
			UUID: ontapVolume.ExternalUUID,
		},
		DisplayName:       ontapVolume.Name,
		QuotaInBytes:      uint64(ontapVolume.Size),
		UsedBytes:         uint64(ontapVolume.UsedBytes),
		SnapReserve:       ontapVolume.SnapReserve,
		SnapshotDirectory: ontapVolume.SnapshotDirectoryAccessEnabled,
	}
	if ontapVolume.JunctionPath != "" {
		volume.FileProperties = &models.FileProperties{
			JunctionPath:    ontapVolume.JunctionPath,
			SecurityStyle:   ontapVolume.SecurityStyle,
			UnixPermissions: ontapVolume.UnixPermissions,
		}
	}
	return volume
}

// CreateVolume persists a durable job, and starts the VCM volume create workflow.
func (o *VCMOrchestrator) CreateVolume(ctx context.Context, params *commonparams.CreateVolumeParams) (*models.Volume, string, error) {
	node, expectedSvmName, err := nodeAndSvmFromStore(o.store)
	if err != nil {
		return nil, "", fmt.Errorf("CreateVolume: %w", err)
	}

	volume := &datamodel.Volume{
		Name:        params.Name,
		SizeInBytes: int64(params.QuotaInBytes),
		Svm:         &datamodel.Svm{Name: expectedSvmName},
		VolumeAttributes: &datamodel.VolumeAttributes{
			CreationToken:     params.CreationToken,
			Protocols:         params.Protocols,
			SnapReserve:       params.SnapReserve,
			SnapshotDirectory: params.SnapshotDirectory,
			DeploymentName:    node.DeploymentName,
		},
	}

	if utils.IsSanProtocols(params.Protocols) {
		volume.VolumeAttributes.SnapshotDirectory = false
	}

	if params.BlockDevices != nil && len(*params.BlockDevices) > 0 {
		blockDevices := make([]datamodel.BlockDevice, 0, len(*params.BlockDevices))
		for _, blockDeviceReq := range *params.BlockDevices {
			blockDevice := datamodel.BlockDevice{
				Name:   blockDeviceReq.Name,
				OSType: blockDeviceReq.OSType,
			}
			for _, hostGroupName := range blockDeviceReq.HostGroupNames {
				blockDevice.HostGroupDetails = append(blockDevice.HostGroupDetails, datamodel.HostGroupDetail{
					HostGroupName: hostGroupName,
				})
			}
			blockDevices = append(blockDevices, blockDevice)
		}
		volume.VolumeAttributes.BlockDevices = &blockDevices
	}

	if params.FileProperties != nil {
		volume.VolumeAttributes.FileProperties = factorycommon.BuildFilePropertiesFromParams(params.FileProperties, params.CreationToken)
	}

	if params.SnapshotPolicy != nil {
		volume.SnapshotPolicy = &datamodel.SnapshotPolicy{
			Name:      volume.Name,
			IsEnabled: params.SnapshotPolicy.IsEnabled,
			Schedules: factorycommon.ConvertToDBSnapshotPolicySchedule(params.SnapshotPolicy.Schedules),
		}
	}

	operationID := params.OperationID
	job := &vcmdb.Job{
		ID:         operationID,
		ResourceID: params.Name,
		Type:       string(datamodel.JobTypeCreateVolume),
		State:      vcmdb.JobStateNew,
		DeadlineAt: params.Deadline.UTC(),
	}
	alreadyExists, err := createJobOrGet(ctx, o.store, job)
	if err != nil {
		return nil, "", fmt.Errorf("CreateVolume: %w", err)
	}
	if alreadyExists {
		return nil, operationID, nil
	}

	_, err = o.wfStarter.Start(ctx, operationID, vcmworkflows.VcmCreateVolumeWorkflow, volume, node, params)
	if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
		return nil, operationID, nil
	}
	if err != nil {
		if updateErr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); updateErr != nil {
			o.logger.ErrorContext(ctx, "Volume create failed to mark job ERROR after workflow start failure", "requestId", operationID, "volumeName", params.Name, "workflowError", err, "updateError", updateErr)
		}
		return nil, "", fmt.Errorf("CreateVolume: start workflow: %w", err)
	}

	return nil, operationID, nil
}

// DeleteVolume starts the VCM volume delete go-workflow after persisting a durable job.
func (o *VCMOrchestrator) DeleteVolume(ctx context.Context, params *commonparams.DeleteVolumeParams) (*models.Volume, string, error) {
	node, expectedSvmName, err := nodeAndSvmFromStore(o.store)
	if err != nil {
		return nil, "", fmt.Errorf("DeleteVolume: %w", err)
	}

	volume := &datamodel.Volume{
		Svm: &datamodel.Svm{
			Name: expectedSvmName,
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: params.VolumeUUID,
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{},
			},
		},
	}

	operationID := params.OperationID
	job := &vcmdb.Job{
		ID:         operationID,
		ResourceID: params.VolumeUUID,
		Type:       string(datamodel.JobTypeDeleteVolume),
		State:      vcmdb.JobStateNew,
		DeadlineAt: params.Deadline.UTC(),
	}
	alreadyExists, err := createJobOrGet(ctx, o.store, job)
	if err != nil {
		return nil, "", fmt.Errorf("DeleteVolume: %w", err)
	}
	if alreadyExists {
		return nil, operationID, nil
	}

	_, err = o.wfStarter.Start(ctx, operationID, vcmworkflows.VcmDeleteVolumeWorkflow, volume, node)
	if errors.Is(err, cbackend.ErrInstanceAlreadyExists) {
		return nil, operationID, nil
	}
	if err != nil {
		if updateErr := o.store.UpdateJobProgress(ctx, vcmdb.UpdateJobProgressParams{
			JobID:        operationID,
			State:        vcmdb.JobStateError,
			Step:         jobStepFailedToStart,
			ErrorMessage: err.Error(),
			Retriable:    true,
		}); updateErr != nil {
			o.logger.ErrorContext(ctx, "Volume delete failed to mark job ERROR after workflow start failure", "requestId", operationID, "volumeUUID", params.VolumeUUID, "workflowError", err, "updateError", updateErr)
		}
		return nil, "", fmt.Errorf("DeleteVolume: start workflow: %w", err)
	}

	return nil, operationID, nil
}
