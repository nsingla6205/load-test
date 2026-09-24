package vcm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	cbackend "github.com/cschleiden/go-workflows/backend"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	vcmworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/vcm-workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vcmdb "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
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

	volume := volumeModelFromONTAP(ontapVolume)
	if volume == nil {
		return nil, nil
	}
	if err := enrichVolumeFromONTAP(provider, svmName, volume, ontapVolume); err != nil {
		return nil, fmt.Errorf("GetVolume: enrich ONTAP volume: %w", err)
	}
	return volume, nil
}

// ListVolumes returns all VCM volumes from ONTAP for the deployment SVM.
func (o *VCMOrchestrator) ListVolumes(ctx context.Context, _ string) ([]*models.Volume, error) {
	svmName, provider, err := ontapProviderFromStore(ctx, o.store)
	if err != nil {
		return nil, fmt.Errorf("ListVolumes: %w", err)
	}

	ontapVolumes, err := provider.ListVolumesForSVM(svmName)
	if err != nil {
		return nil, fmt.Errorf("ListVolumes: read ONTAP volumes: %w", err)
	}

	volumes := make([]*models.Volume, 0, len(ontapVolumes))
	for _, ontapVolume := range ontapVolumes {
		if volume := volumeModelFromONTAP(ontapVolume); volume != nil {
			volumes = append(volumes, volume)
		}
	}
	return volumes, nil
}

func volumeModelFromONTAP(ontapVolume *vsa.VolumeResponse) *models.Volume {
	if ontapVolume == nil {
		return nil
	}
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
	if !ontapVolume.Created.IsZero() {
		volume.CreatedAt = ontapVolume.Created.UTC()
	}
	if strings.TrimSpace(ontapVolume.JunctionPath) != "" {
		exportPolicyName := ontapVolume.ExportPolicyName
		if exportPolicyName == "" {
			exportPolicyName = models.DefaultExportPolicyName
		}
		volume.FileProperties = &models.FileProperties{
			JunctionPath:    ontapVolume.JunctionPath,
			SecurityStyle:   ontapVolume.SecurityStyle,
			UnixPermissions: ontapVolume.UnixPermissions,
			ExportPolicy: &models.ExportPolicy{
				ExportPolicyName: exportPolicyName,
			},
		}
	}
	if ontapVolume.SnapshotPolicyName != "" {
		volume.SnapshotPolicy = &models.SnapshotPolicy{
			Name: ontapVolume.SnapshotPolicyName,
		}
	}
	return volume
}

func enrichVolumeFromONTAP(provider vsa.Provider, svmName string, volume *models.Volume, ontapVolume *vsa.VolumeResponse) error {
	if volume == nil || ontapVolume == nil {
		return nil
	}

	if ontapVolume.ExportPolicyName != "" && ontapVolume.ExportPolicyName != models.DefaultExportPolicyName {
		exportPolicy, err := provider.GetExportPolicy(ontapVolume.ExportPolicyName, svmName)
		if err != nil && !utilErrors.IsNotFoundErr(err) {
			return err
		}
		if exportPolicy != nil {
			if volume.FileProperties == nil {
				volume.FileProperties = &models.FileProperties{}
			}
			volume.FileProperties.ExportPolicy = convertVSAExportPolicyToModel(exportPolicy)
		}
	}

	if ontapVolume.SnapshotPolicyName != "" && ontapVolume.SnapshotPolicyName != models.DefaultONTAPSnapshotPolicyName {
		snapshotPolicy, err := provider.GetSnapshotPolicy(ontapVolume.SnapshotPolicyName)
		if err != nil && !utilErrors.IsNotFoundErr(err) {
			return err
		}
		if snapshotPolicy != nil {
			volume.SnapshotPolicy = convertVSASnapshotPolicyToModel(snapshotPolicy)
		}
	}

	luns, err := provider.LunList(vsa.LunGetParams{
		SvmName:    svmName,
		VolumeName: ontapVolume.Name,
	})
	if err != nil {
		// NFS/file volumes have no LUNs; LunList returns NotFoundErr("lun") wrapped in VCPError.
		if utilErrors.IsNotFoundErrForObjectTypeInChain(err, "lun") {
			return nil
		}
		return err
	}
	if len(luns) == 0 {
		return nil
	}

	blockDevices := make([]models.BlockDevice, 0, len(luns))
	for _, lun := range luns {
		if lun == nil {
			continue
		}
		igroupNames := lun.IgroupNames
		if lun.ExternalUUID != "" {
			mappedNames, mapErr := provider.IgroupNamesForLunUUID(svmName, lun.ExternalUUID)
			if mapErr != nil {
				return mapErr
			}
			if len(mappedNames) > 0 {
				igroupNames = mappedNames
			}
		}
		hostGroups := make([]models.HostGroupDetails, 0, len(igroupNames))
		for _, igroupName := range igroupNames {
			hostGroups = append(hostGroups, models.HostGroupDetails{HostGroupID: igroupName})
		}
		blockDevices = append(blockDevices, models.BlockDevice{
			Name:            lun.Name,
			Identifier:      lun.SerialNumber,
			Size:            uint64(lun.Size),
			OSType:          strings.ToUpper(lun.OSType),
			HostGroupDetail: hostGroups,
		})
	}
	if len(blockDevices) > 0 {
		volume.BlockDevices = &blockDevices
	}
	return nil
}

func convertVSAExportPolicyToModel(exportPolicy *vsa.ExportPolicy) *models.ExportPolicy {
	if exportPolicy == nil {
		return nil
	}
	result := &models.ExportPolicy{
		ExportPolicyName: exportPolicy.ExportPolicyName,
	}
	for _, rule := range exportPolicy.ExportRules {
		if rule == nil {
			continue
		}
		result.ExportRules = append(result.ExportRules, &models.ExportRule{
			AllowedClients:      rule.AllowedClients,
			AnonymousUser:       rule.AnonymousUser,
			Index:               rule.Index,
			ChownMode:           rule.ChownMode,
			AccessType:          rule.AccessType,
			CIFS:                rule.CIFS,
			NFSv3:               rule.NFSv3,
			NFSv4:               rule.NFSv4,
			S3:                  rule.S3,
			UnixReadOnly:        rule.UnixReadOnly,
			UnixReadWrite:       rule.UnixReadWrite,
			Kerberos5ReadOnly:   rule.Kerberos5ReadOnly,
			Kerberos5ReadWrite:  rule.Kerberos5ReadWrite,
			Kerberos5iReadOnly:  rule.Kerberos5iReadOnly,
			Kerberos5iReadWrite: rule.Kerberos5iReadWrite,
			Kerberos5pReadOnly:  rule.Kerberos5pReadOnly,
			Kerberos5pReadWrite: rule.Kerberos5pReadWrite,
			Superuser:           rule.Superuser,
			AllSquash:           rule.AllSquash,
			AnonUid:             rule.AnonUid,
		})
	}
	return result
}

func convertVSASnapshotPolicyToModel(snapshotPolicy *vsa.SnapshotPolicy) *models.SnapshotPolicy {
	if snapshotPolicy == nil {
		return nil
	}
	result := &models.SnapshotPolicy{
		Name:      snapshotPolicy.Name,
		IsEnabled: snapshotPolicy.IsEnabled,
	}
	for _, schedule := range snapshotPolicy.Schedules {
		if schedule == nil {
			continue
		}
		modelSchedule := &models.SnapshotPolicySchedule{
			Count:           schedule.Count,
			SnapmirrorLabel: schedule.SnapmirrorLabel,
		}
		if schedule.Schedule != nil {
			modelSchedule.Schedule = &models.Schedule{
				Name:        schedule.Schedule.Name,
				Description: schedule.Schedule.Description,
				Type:        schedule.Schedule.Type,
				Months:      append([]int(nil), schedule.Schedule.Months...),
				DaysOfMonth: append([]int(nil), schedule.Schedule.DaysOfMonth...),
				DaysOfWeek:  append([]int(nil), schedule.Schedule.DaysOfWeek...),
				Hours:       append([]int(nil), schedule.Schedule.Hours...),
				Minutes:     append([]int(nil), schedule.Schedule.Minutes...),
			}
		}
		result.Schedules = append(result.Schedules, modelSchedule)
	}
	return result
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

	_, err = o.wfStarter.Start(ctx, operationID, vcmworkflows.VcmCreateVolumeWorkflow, volume, commonparams.WorkflowInputs{
		Node:            node,
		ExpectedSvmName: expectedSvmName,
		Deadline:        params.Deadline.UTC(),
	}, params)
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

	_, err = o.wfStarter.Start(ctx, operationID, vcmworkflows.VcmDeleteVolumeWorkflow, volume, commonparams.WorkflowInputs{
		Node:            node,
		ExpectedSvmName: expectedSvmName,
		Deadline:        params.Deadline.UTC(),
	})
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

// UpdateVolumeV2 persists a durable job and starts the VCM volume update workflow.
func (o *VCMOrchestrator) UpdateVolumeV2(ctx context.Context, params *commonparams.UpdateVolumeParams) (*models.Volume, string, error) {
	node, expectedSvmName, err := nodeAndSvmFromStore(o.store)
	if err != nil {
		return nil, "", fmt.Errorf("UpdateVolumeV2: %w", err)
	}

	volume := &datamodel.Volume{
		Svm: &datamodel.Svm{
			Name: expectedSvmName,
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID: params.VolumeId,
			FileProperties: &datamodel.FileProperties{
				ExportPolicy: &datamodel.ExportPolicy{},
			},
		},
	}

	operationID := params.OperationID
	job := &vcmdb.Job{
		ID:         operationID,
		ResourceID: params.VolumeId,
		Type:       string(datamodel.JobTypeUpdateVolume),
		State:      vcmdb.JobStateNew,
		DeadlineAt: params.Deadline.UTC(),
	}
	alreadyExists, err := createJobOrGet(ctx, o.store, job)
	if err != nil {
		return nil, "", fmt.Errorf("UpdateVolumeV2: %w", err)
	}
	if alreadyExists {
		return nil, operationID, nil
	}

	_, err = o.wfStarter.Start(ctx, operationID, vcmworkflows.VcmUpdateVolumeWorkflow, volume, node, params)
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
			o.logger.ErrorContext(ctx, "Volume update failed to mark job ERROR after workflow start failure", "requestId", operationID, "volumeUUID", params.VolumeId, "workflowError", err, "updateError", updateErr)
		}
		return nil, "", fmt.Errorf("UpdateVolumeV2: start workflow: %w", err)
	}

	return nil, operationID, nil
}
