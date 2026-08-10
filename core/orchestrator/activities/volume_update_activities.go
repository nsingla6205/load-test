package activities

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"
	ontapModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

const (
	VolumeAttributesProperty = "volume_attributes"
)

var (
	prepareFieldsForUpdate                 = getUpdatedFieldsFromParams
	HostGroupsUpdateDiffForVolume          = _hostGroupsUpdateDiffForVolume
	getHostGroup                           = _getHostGroup
	applyFlexCacheParameters               = _applyFlexCacheParameters
	isAllSquashExistInPreviousExportPolicy = _isAllSquashExistInPreviousExportPolicy
)

type VolumeUpdateActivity struct {
	SE        database.Storage
	Scheduler *scheduler.TemporalScheduler
}

// UpdateVolumeInONTAP updates the volume in ONTAP
func (a *VolumeUpdateActivity) UpdateVolumeInONTAP(ctx context.Context, volume *datamodel.Volume, params *common.UpdateVolumeParams, node *models.Node) error {
	logger := util.GetLogger(ctx)
	se := a.SE
	activity.RecordHeartbeat(ctx, "Starting UpdateVolumeInONTAP activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	updateVolumeParams := &vsa.UpdateVolumeParams{
		UUID:        volume.VolumeAttributes.ExternalUUID,
		Size:        params.QuotaInBytes,
		SnapReserve: params.SnapReserve,
	}

	// Set snapshot policy only if the volume is not a data protection volume.
	if !volume.VolumeAttributes.IsDataProtection {
		if params.SnapshotPolicy != nil && params.SnapshotPolicy.Name != "" {
			updateVolumeParams.SnapshotPolicyName = params.SnapshotPolicy.Name
		}
	}

	if params.SnapshotDirectoryAccess != nil {
		updateVolumeParams.SnapshotDirectoryAccess = params.SnapshotDirectoryAccess
	}

	if params.AutoTieringPolicy != nil {
		updateVolumeParams.TieringPolicy, err = updateAutoTieringParams(ctx, params, updateVolumeParams, volume, se)
		if err != nil {
			logger.Errorf("Failed to update auto-tiering params for volume %s: %v", volume.Name, err)
			return err
		}
	}
	if params.FileProperties != nil && params.FileProperties.UnixPermissions != "" {
		updateVolumeParams.UnixPermissions = &params.FileProperties.UnixPermissions
	}
	err = updateVolume(ctx, provider, *updateVolumeParams)
	if err != nil {
		logger.Errorf("Failed to update volume %s in ontap: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("Volume %s updated successfully in ontap", volume.Name)

	activity.RecordHeartbeat(ctx, "Finished UpdateVolumeInONTAP activity")
	return nil
}

func updateAutoTieringParams(ctx context.Context, params *common.UpdateVolumeParams, updateVolumeParams *vsa.UpdateVolumeParams, volume *datamodel.Volume, se database.Storage) (*vsa.TieringPolicy, error) {
	// By default, assume tiering is not paused. This is overridden only when "all" policy
	// is concerned. In that case, we fetch the pool from DB to check if tiering is paused.
	// If auto-tiering is paused for pool, we don't set the all auto-tiering policy during
	// volume creation in ontap. Since this supersedes the tiering fullness threshold and
	// doesn't stop tiering. We let the volume be created with default tiering policy 'none'
	// This will get later corrected when the pool will resume auto-tiering.
	pool := &datamodel.PoolView{
		Pool: datamodel.Pool{
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusResumed,
			},
		},
	}
	if params.AutoTieringPolicy.TieringPolicy == ontapModels.VolumeInlineTieringPolicyAll {
		var err error
		// Fetch pool from db to check if auto-tiering is currently paused
		recordHeartbeat(ctx, fmt.Sprintf("Fetching pool %s for volume %s auto-tiering update", volume.Pool.UUID, volume.UUID))
		pool, err = se.GetPool(ctx, volume.Pool.UUID, volume.AccountID)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	updateVolumeParams.TieringPolicy = &vsa.TieringPolicy{}
	updateVolumeParams.TieringPolicy.CoolnessPeriod = int64(params.AutoTieringPolicy.CoolingThresholdDays)
	isDataProtection := volume.VolumeAttributes != nil && volume.VolumeAttributes.IsDataProtection
	if params.AutoTieringPolicy.AutoTieringEnabled && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPaused && pool.AutoTieringConfig.TieringStatus != datamodel.TieringStatusPartiallyPaused {
		updateVolumeParams.TieringPolicy.CoolAccessTieringPolicy = nillable.GetString(&params.AutoTieringPolicy.TieringPolicy, utils.FetchTieringPolicyAsPerVolumeType(!utils.IsSanProtocols(volume.VolumeAttributes.Protocols)))
		updateVolumeParams.TieringPolicy.CoolAccessRetrievalPolicy = nillable.GetString(&params.AutoTieringPolicy.RetrievalPolicy, ontapModels.VolumeCloudRetrievalPolicyDefault)
		// ONTAP rejects is-cloud-write-enabled changes on SnapMirror DP read-only volumes
		// (error 917628). Tiering policy / cooling / retrieval can still be applied.
		if !isDataProtection {
			updateVolumeParams.TieringPolicy.CloudWriteModeEnabled = params.AutoTieringPolicy.CloudWriteModeEnabled
		}
	} else {
		updateVolumeParams.TieringPolicy.CoolAccessTieringPolicy = ontapModels.VolumeInlineTieringPolicyNone
		if !isDataProtection {
			updateVolumeParams.TieringPolicy.CloudWriteModeEnabled = nillable.GetBoolPtr(false)
		}
	}

	return updateVolumeParams.TieringPolicy, nil
}

func updateVolume(ctx context.Context, provider vsa.Provider, params vsa.UpdateVolumeParams) error {
	recordHeartbeat(ctx, fmt.Sprintf("Updating volume %s in ONTAP", params.UUID))
	err := provider.UpdateVolume(params)
	if err != nil {
		util.GetLogger(ctx).Errorf("Failed to update volume %s in ontap: %v", params.UUID, err)
		return err
	}
	util.GetLogger(ctx).Debugf("Volume %s updated successfully in ontap", params.UUID)
	return nil
}

func (a *VolumeUpdateActivity) UpdateVolumeJunctionpath(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	activity.RecordHeartbeat(ctx, "UpdateVolumeJunctionpath activity in progress")
	if utils.IsSanProtocols(volume.VolumeAttributes.Protocols) {
		return nil
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	err = updateVolume(ctx, provider, vsa.UpdateVolumeParams{
		UUID:         volume.VolumeAttributes.ExternalUUID,
		JunctionPath: &volume.VolumeAttributes.FileProperties.JunctionPath,
	})
	activity.RecordHeartbeat(ctx, "UpdateVolumeJunctionpath activity completed")
	return err
}

// GetVolumeFromONTAP retrieves the volume from ONTAP
func (a *VolumeUpdateActivity) GetVolumeFromONTAP(ctx context.Context, volume *datamodel.Volume, node *models.Node) (*vsa.VolumeResponse, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting GetVolumeFromONTAP activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	isRestore := false
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.RestoredBackupPath != "" {
		isRestore = true
	}
	recordHeartbeat(ctx, fmt.Sprintf("Getting volume %s from ONTAP", volume.UUID))
	volumeRes, err := provider.GetVolume(vsa.GetVolumeParams{
		UUID:              volume.VolumeAttributes.ExternalUUID,
		VolumeName:        volume.Name,
		SvmName:           volume.Svm.Name,
		IsRestore:         isRestore,
		SnapshotDirectory: volume.VolumeAttributes.SnapshotDirectory,
	})

	if err != nil {
		logger.Errorf("Failed to get volume %s from ONTAP: %v", volume.Name, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished GetVolumeFromONTAP activity")
	return volumeRes, nil
}

// UpdateLun updates the LUN associated with the volume in the VSA cluster
func (a *VolumeUpdateActivity) UpdateLun(ctx context.Context, volume *datamodel.Volume, volResponse *vsa.VolumeResponse, node *models.Node) (*vsa.LunResponse, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateLun activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var lunUUID string
	var lunName string
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
		lunUUID = (*volume.VolumeAttributes.BlockDevices)[0].LunUUID
		lunName = (*volume.VolumeAttributes.BlockDevices)[0].Name
	} else if volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockProperties != nil {
		lunUUID = volume.VolumeAttributes.BlockProperties.LunUUID
		lunName = volume.VolumeAttributes.BlockProperties.LunName
	}
	recordHeartbeat(ctx, fmt.Sprintf("Getting LUN %s from ONTAP", lunName))
	lun, err := LunGet(ctx, lunName, volume.Name, volume.Svm.Name, provider)
	if err != nil {
		logger.Debug("lun not found !")
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Determine the size to update based on snap reserve logic
	sizeToUpdate := volResponse.AFSSize - volResponse.MetadataSize
	lunUpdateParams := vsa.LunUpdateParams{
		UUID:       lunUUID,
		LunName:    lunName,
		VolumeName: volume.Name,
		SvmName:    volume.Svm.Name,
	}
	if sizeToUpdate < lun.Size {
		lunUpdateParams.Size = lun.Size
	} else {
		lunUpdateParams.Size = sizeToUpdate
	}
	// Update the LUN with the calculated size
	recordHeartbeat(ctx, fmt.Sprintf("Updating LUN %s in ONTAP", lunName))
	err = provider.LunUpdate(lunUpdateParams)
	if err != nil {
		if errors.IsConflictErr(err) {
			logger.Debugf("Lun %s size is same as existing size", volume.Name)
			return nil, nil
		}
		logger.Errorf("Failed to update lun %s in vsa cluster: %v", volume.Name, err)
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrLunUpdate, err))
	}
	logger.Debugf("Lun %s updated successfully in vsa cluster", volume.Name)

	recordHeartbeat(ctx, fmt.Sprintf("Getting updated LUN %s from ONTAP", lunName))
	return LunGet(ctx, lunName, volume.Name, volume.Svm.Name, provider)
}

func (a *VolumeUpdateActivity) EnsureHostGroupsExistsAndMapDisk(ctx context.Context, volume *datamodel.Volume, iGroups []string, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting EnsureHostGroupsExistsAndMapDisk activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	recordHeartbeat(ctx, fmt.Sprintf("Getting host groups for volume %s from database", volume.UUID))
	hgs, err := a.SE.GetMultipleHostGroups(ctx, iGroups, volume.AccountID)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(hgs) == 0 {
		logger.Debugf("No host groups to map for volume %s", volume.Name)
		return nil
	}

	hgNames := make([]string, 0)
	for _, hg := range hgs {
		hgNames = append(hgNames, hg.Name)
	}

	for _, hostGroup := range hgs {
		activity.RecordHeartbeat(ctx, "Ensuring HostGroup "+hostGroup.Name+" exists for volume "+volume.Name)

		// Check if the hostGroup already exists
		exists, _, err := provider.IgroupExists(hostGroup.Name, &volume.Svm.Name)
		if err != nil {
			return err
		}
		if !exists {
			activity.RecordHeartbeat(ctx, "Creating HostGroup "+hostGroup.Name+" for volume "+volume.Name)
			// Create the hostGroup if it doesn't exist
			if _, err = provider.IgroupCreate(vsa.IgroupCreateParams{
				IgroupName: hostGroup.Name,
				SvmName:    volume.Svm.Name,
				OsType:     hostGroup.OSType,
				Initiator:  hostGroup.Hosts.Hosts,
			}); err != nil {
				logger.Errorf("Failed to create igroup %s: %v", hostGroup.Name, err)
				return err
			}
		}
	}
	lunName := utils.GetLunName(volume.Name)
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
		lunName = (*volume.VolumeAttributes.BlockDevices)[0].Name
	}

	recordHeartbeat(ctx, fmt.Sprintf("Creating LUN map for volume %s in ONTAP", volume.UUID))
	err = provider.LunMapCreate(vsa.LunMapCreateParams{
		LunName:    "/vol/" + volume.Name + "/" + lunName,
		SvmName:    volume.Svm.Name,
		IGroupName: hgNames,
	})
	if err != nil {
		return err
	}

	activity.RecordHeartbeat(ctx, "Finished EnsureHostGroupsExistsAndMapDisk activity")
	return nil
}

// UnmapHostGroupFromDisk deletes the Disk HostGroup map
func (a *VolumeUpdateActivity) UnmapHostGroupFromDisk(ctx context.Context, volume *datamodel.Volume, iGroupUUIDs []string, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UnmapHostGroupFromDisk activity")
	se := a.SE
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	var lunUUID string
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockDevices != nil && len(*volume.VolumeAttributes.BlockDevices) > 0 {
		lunUUID = (*volume.VolumeAttributes.BlockDevices)[0].LunUUID
	} else if volume.VolumeAttributes != nil && volume.VolumeAttributes.BlockProperties != nil {
		lunUUID = volume.VolumeAttributes.BlockProperties.LunUUID
	}
	for _, iGroupUUID := range iGroupUUIDs {
		activity.RecordHeartbeat(ctx, "Unmapping HostGroup "+iGroupUUID+" from volume "+volume.Name)

		hgMapsToDelete, err := a.SE.GetHostGroup(ctx, iGroupUUID, volume.AccountID)
		if err != nil {
			return err
		}

		// Fetch iGroups uuid to delete the map
		recordHeartbeat(ctx, fmt.Sprintf("Checking igroup %s in ONTAP", iGroupUUID))
		exists, iGroupOntap, err := provider.IgroupExists(hgMapsToDelete.Name, &volume.Svm.Name)
		if err != nil {
			return err
		}
		if !exists {
			logger.Debugf("IGroup %s not found in vsa cluster, skipping unmapping lun map and igroup delete", iGroupUUID)
			continue
		}
		activity.RecordHeartbeat(ctx, "Deleting lun map for igroup "+iGroupUUID+" from volume "+volume.Name)
		err = provider.LunMapDelete(vsa.LunMapDeleteParams{
			LunUUID:    lunUUID,
			IGroupUUID: *iGroupOntap.UUID,
		})
		if err != nil {
			logger.Errorf("Failed to delete lun map for igroup %s: %v", iGroupUUID, err)
			return err
		}

		activity.RecordHeartbeat(ctx, "Fetching all volumes for HostGroup "+iGroupUUID+" to check if it can be deleted for volume "+volume.Name)
		// Fetch all volumes for the HG and delete the IGroup which doesn't have any volume in current pool
		hgAttachedVolumes, err := se.GetAllVolumesForHG(ctx, iGroupUUID, volume.AccountID)
		if err != nil {
			return err
		}

		// We have to check if there is any other volume using this HG in the same pool
		deleteIGroup := true
		for _, volumeWithHG := range hgAttachedVolumes {
			if volume.PoolID == volumeWithHG.PoolID && volume.UUID != volumeWithHG.UUID {
				deleteIGroup = false
				break
			}
		}
		activity.RecordHeartbeat(ctx, "Checking if HostGroup "+iGroupUUID+" can be deleted for volume "+volume.Name)
		if deleteIGroup {
			activity.RecordHeartbeat(ctx, "Deleting igroup "+iGroupUUID+" from volume "+volume.Name)
			err = provider.IgroupDelete(*iGroupOntap.UUID)
			if err != nil {
				logger.Errorf("Failed to delete igroup %s: %v", iGroupUUID, err)
				return err
			}
		}
	}

	activity.RecordHeartbeat(ctx, "Finished UnmapHostGroupFromDisk activity")
	return nil
}

func _hostGroupsUpdateDiffForVolume(existingIGroups []string, newIGroups []string) ([]string, []string) {
	toCreate := make([]string, 0)
	toDelete := make([]string, 0)
	for _, newIGroup := range newIGroups {
		if !utils.ContainsString(existingIGroups, newIGroup) {
			toCreate = append(toCreate, newIGroup)
		}
	}

	for _, existingIGroup := range existingIGroups {
		if !utils.ContainsString(newIGroups, existingIGroup) {
			toDelete = append(toDelete, existingIGroup)
		}
	}
	return toCreate, toDelete
}

// UpdateVolumeInDB updates the volume in the database with the new parameters
func (a *VolumeUpdateActivity) UpdateVolumeInDB(ctx context.Context, volume *datamodel.Volume, params *common.UpdateVolumeParams) error {
	logger := util.GetLogger(ctx)

	recordHeartbeat(ctx, fmt.Sprintf("Preparing database fields for volume %s", volume.UUID))
	updatedFields, err := prepareFieldsForUpdate(ctx, a.SE, volume, params)
	if err != nil {
		logger.Errorf("Failed to prepareFieldsForUpdate for the volume %s in the database: %v", volume.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	// Update the volume in the database
	recordHeartbeat(ctx, fmt.Sprintf("Updating volume %s fields in database", volume.UUID))
	err = a.SE.UpdateVolumeFields(ctx, volume.UUID, updatedFields)
	if err != nil {
		logger.Errorf("Failed to update volume %s in the database: %v", volume.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished UpdateVolumeInDB activity")
	logger.Debugf("Volume %s updated successfully in the database", volume.UUID)
	return nil
}

// getUpdatedFieldsFromParams prepares the fields to be updated in the database
func getUpdatedFieldsFromParams(ctx context.Context, se database.Storage, volume *datamodel.Volume, params *common.UpdateVolumeParams) (map[string]interface{}, error) {
	updates := make(map[string]interface{})
	if params.Description != "" {
		updates["description"] = params.Description
	}

	if params.QuotaInBytes != 0 {
		updates["size_in_bytes"] = params.QuotaInBytes
	}

	if volume.VolumeAttributes == nil {
		volume.VolumeAttributes = &datamodel.VolumeAttributes{}
	}

	if params.Labels != nil {
		volume.VolumeAttributes.Labels = params.Labels
	}

	if params.RestrictedActions != nil {
		volume.VolumeAttributes.RestrictedActions = *params.RestrictedActions
	}

	if params.SnapshotPolicy != nil {
		updates["snapshot_policy"] = volume.SnapshotPolicy
	}

	if params.DataProtection != nil {
		if volume.DataProtection == nil {
			volume.DataProtection = &datamodel.DataProtection{}
		}
		if params.DataProtection.BackupVaultID != nil {
			currentBackupVaultID := volume.DataProtection.BackupVaultID
			updatedBackupVaultID := *params.DataProtection.BackupVaultID
			volume.DataProtection.BackupVaultID = updatedBackupVaultID

			if currentBackupVaultID != updatedBackupVaultID {
				util.GetLogger(ctx).Infof("Backup vault changed for volume %s (old_vault=%s new_vault=%s); backup_chain_bytes unchanged until next backup size recalc", volume.UUID, currentBackupVaultID, updatedBackupVaultID)
			}
		}
		if params.DataProtection.BackupPolicyId != nil {
			volume.DataProtection.BackupPolicyID = *params.DataProtection.BackupPolicyId
		}
		volume.DataProtection.ScheduledBackupEnabled = params.DataProtection.ScheduledBackupEnabled
		if params.DataProtection.KmsGrant != nil {
			volume.DataProtection.KmsGrant = params.DataProtection.KmsGrant
		}
		updates["data_protection"] = volume.DataProtection
	}

	// Check BlockDevices first, then fallback to BlockProperties
	if len(params.BlockDevices) > 0 {
		// Use BlockDevices as primary source
		blockDevices := make([]datamodel.BlockDevice, 0, len(params.BlockDevices))
		for _, blockDeviceReq := range params.BlockDevices {
			blockDevice := datamodel.BlockDevice{
				Name:       blockDeviceReq.Name,
				OSType:     blockDeviceReq.OSType,
				Size:       blockDeviceReq.SizeInBytes,
				Identifier: blockDeviceReq.LunSerialNumber,
				LunUUID:    blockDeviceReq.LunUUID,
			}
			if len(blockDeviceReq.HostGroups) > 0 {
				for _, uuid := range blockDeviceReq.HostGroups {
					hg, err := getHostGroup(se, ctx, uuid, volume.AccountID)
					if err != nil {
						return nil, err
					}
					hgDetail := datamodel.HostGroupDetail{
						HostGroupUUID: uuid,
						HostQNs:       hg.Hosts.Hosts,
					}
					blockDevice.HostGroupDetails = append(blockDevice.HostGroupDetails, hgDetail)
				}
			}
			blockDevices = append(blockDevices, blockDevice)
		}
		volume.VolumeAttributes.BlockDevices = &blockDevices
	} else if params.BlockProperties != nil {
		// Fallback: Use BlockProperties if BlockDevices are not provided
		if volume.VolumeAttributes.BlockProperties == nil {
			volume.VolumeAttributes.BlockProperties = &datamodel.BlockProperties{}
		}
		hgDetails := make([]datamodel.HostGroupDetail, 0)
		for _, uuid := range params.BlockProperties.HostGroupUUIDs {
			hg, err := getHostGroup(se, ctx, uuid, volume.AccountID)
			if err != nil {
				return nil, err
			}
			hgDetail := datamodel.HostGroupDetail{
				HostGroupUUID: uuid,
				HostQNs:       hg.Hosts.Hosts,
			}
			hgDetails = append(hgDetails, hgDetail)
		}
		volume.VolumeAttributes.BlockProperties.HostGroupDetails = hgDetails
	}

	if params.AutoTieringPolicy != nil &&
		(params.AutoTieringPolicy.AutoTieringEnabled != volume.AutoTieringEnabled ||
			(params.AutoTieringPolicy.AutoTieringEnabled && volume.AutoTieringPolicy != nil &&
				(params.AutoTieringPolicy.CoolingThresholdDays != volume.AutoTieringPolicy.CoolingThresholdDays ||
					params.AutoTieringPolicy.HotTierBypassModeEnabled != volume.AutoTieringPolicy.HotTierBypassModeEnabled))) {
		updates["auto_tiering_enabled"] = params.AutoTieringPolicy.AutoTieringEnabled

		autoTieringPolicy := &datamodel.AutoTieringPolicy{
			TieringPolicy:            params.AutoTieringPolicy.TieringPolicy,
			HotTierBypassModeEnabled: params.AutoTieringPolicy.HotTierBypassModeEnabled,
			CoolingThresholdDays:     params.AutoTieringPolicy.CoolingThresholdDays,
			RetrievalPolicy:          params.AutoTieringPolicy.RetrievalPolicy,
			CloudWriteModeEnabled:    params.AutoTieringPolicy.CloudWriteModeEnabled,
		}
		updates["auto_tiering_policy"] = autoTieringPolicy
	}

	if params.SnapReserve != nil {
		if volume.VolumeAttributes == nil {
			volume.VolumeAttributes = &datamodel.VolumeAttributes{}
		}
		volume.VolumeAttributes.SnapReserve = *params.SnapReserve
	}

	updates[VolumeAttributesProperty] = volume.VolumeAttributes

	// Update SMB share settings if provided
	if len(params.SMBShareSettings) > 0 {
		if volume.VolumeAttributes == nil {
			volume.VolumeAttributes = &datamodel.VolumeAttributes{}
		}
		if volume.VolumeAttributes.FileProperties == nil {
			volume.VolumeAttributes.FileProperties = &datamodel.FileProperties{}
		}
		volume.VolumeAttributes.FileProperties.SMBShareSettings = params.SMBShareSettings
		updates[VolumeAttributesProperty] = volume.VolumeAttributes
	}

	updates["state"] = datamodel.LifeCycleStateREADY
	updates["state_details"] = datamodel.LifeCycleStateAvailableDetails

	if applyFlexCacheParameters(volume, params) {
		updates["cache_parameters"] = volume.CacheParameters
	}

	return updates, nil
}

func _applyFlexCacheParameters(volume *datamodel.Volume, params *common.UpdateVolumeParams) bool {
	if volume == nil || volume.CacheParameters == nil ||
		params == nil || params.CacheParameters == nil || params.CacheParameters.CacheConfig == nil {
		return false
	}

	src := params.CacheParameters.CacheConfig
	if volume.CacheParameters.CacheConfig == nil {
		volume.CacheParameters.CacheConfig = &datamodel.CacheConfig{}
	}
	dst := volume.CacheParameters.CacheConfig
	changed := false

	// update only when provided and value differs.
	if src.WritebackEnabled != nil && (dst.WritebackEnabled == nil || *dst.WritebackEnabled != *src.WritebackEnabled) {
		dst.WritebackEnabled = src.WritebackEnabled
		changed = true
	}
	if src.AtimeScrubEnabled != nil && (dst.AtimeScrubEnabled == nil || *dst.AtimeScrubEnabled != *src.AtimeScrubEnabled) {
		dst.AtimeScrubEnabled = src.AtimeScrubEnabled
		changed = true
	}
	if src.AtimeScrubDays != nil && (dst.AtimeScrubDays == nil || *dst.AtimeScrubDays != *src.AtimeScrubDays) {
		dst.AtimeScrubDays = src.AtimeScrubDays
		changed = true
	}
	if src.CifsChangeNotifyEnabled != nil && (dst.CifsChangeNotifyEnabled == nil || *dst.CifsChangeNotifyEnabled != *src.CifsChangeNotifyEnabled) {
		dst.CifsChangeNotifyEnabled = src.CifsChangeNotifyEnabled
		changed = true
	}

	if src.CachePrePopulate != nil {
		if dst.CachePrePopulate == nil {
			dst.CachePrePopulate = &datamodel.CachePrePopulate{}
		}
		if src.CachePrePopulate.Recursion != nil && (dst.CachePrePopulate.Recursion == nil || *dst.CachePrePopulate.Recursion != *src.CachePrePopulate.Recursion) {
			dst.CachePrePopulate.Recursion = src.CachePrePopulate.Recursion
			changed = true
		}
		if src.CachePrePopulate.PathList != nil {
			dst.CachePrePopulate.PathList = src.CachePrePopulate.PathList
			changed = true
		}
		if src.CachePrePopulate.ExcludePathList != nil {
			dst.CachePrePopulate.ExcludePathList = src.CachePrePopulate.ExcludePathList
			changed = true
		}
	}

	return changed
}

func _getHostGroup(se database.Storage, ctx context.Context, uuid string, accountId int64) (*datamodel.HostGroup, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Getting host group %s from database", uuid))
	hg, err := se.GetHostGroup(ctx, uuid, accountId)
	if err != nil {
		return nil, err
	}
	return hg, nil
}

// UpdateSnapshotPolicyInOntap updates the snapshot policy for the given volume in ONTAP.
func (a *VolumeUpdateActivity) UpdateSnapshotPolicyInOntap(ctx context.Context, node *models.Node, currentPolicy, updatingPolicy *datamodel.SnapshotPolicy) error {
	activity.RecordHeartbeat(ctx, "Initializing snapshot policy update")
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Updating snapshot policy in ONTAP")
	err = provider.UpdateSnapshotPolicy(ctx, &vsa.UpdateSnapshotPolicyParams{
		CurrentSnapshotPolicy: &vsa.SnapshotPolicy{
			Name:      currentPolicy.Name,
			IsEnabled: currentPolicy.IsEnabled,
			Schedules: ConvertToVSASnapshotPolicySchedules(currentPolicy.Schedules),
		},
		UpdatingSnapshotPolicy: &vsa.SnapshotPolicy{
			Name:      currentPolicy.Name,
			IsEnabled: updatingPolicy.IsEnabled,
			Schedules: ConvertToVSASnapshotPolicySchedules(updatingPolicy.Schedules),
		},
	})
	if err != nil {
		logger.Errorf("Failed to update snapshot policy %s in ONTAP: %v", updatingPolicy.Name, err)
		return err
	}

	activity.RecordHeartbeat(ctx, "Snapshot policy updated successfully")
	logger.Debugf("Snapshot policy %s updated successfully in ONTAP", updatingPolicy.Name)
	return nil
}

// FindTenancyDetails retrieves the tenancy information for the given consumer VPC and customer project number
func (a *VolumeUpdateActivity) FindTenancyDetails(ctx context.Context, consumerVPC string, customerProjectNumber string, tenantProjectRegion *string) (*common.TenancyInfo, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Resolving cloud service for customer project %s tenancy", customerProjectNumber))
	cloudService, err := hyperscaler.ResolveCloudService(ctx)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return FindTenancy(ctx, cloudService, consumerVPC, customerProjectNumber, tenantProjectRegion)
}

// CheckBackupVaultExistInVCP checks if a backup vault exists in the VCP
func (a *VolumeUpdateActivity) CheckBackupVaultExistInVCP(ctx context.Context, volume *datamodel.Volume, region string) (*datamodel.BackupVault, error) {
	return CheckBackupVaultExistsInVCP(ctx, a.SE, volume, region)
}

// CreateBucketForBackupVault creates a bucket in the specified region for the given resource name and tenancy details
func (a *VolumeUpdateActivity) CreateBucketForBackupVault(ctx context.Context, resourceName *common.ResourceNames, tenancyDetails *common.TenancyInfo, region string, kmsGrant *string) (*common.BucketDetails, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Creating backup bucket %s", resourceName.BucketName))
	return CreateBucket(ctx, resourceName, tenancyDetails, region, kmsGrant)
}

// GenerateResourceNamesForBackupVault generates resource names for the volume based on the tenancy details and GCP region
func (a *VolumeUpdateActivity) GenerateResourceNamesForBackupVault(ctx context.Context, volume *datamodel.Volume, tenancyDetails *common.TenancyInfo, gcpRegion string) (*common.ResourceNames, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Generating backup resource names for volume %s", volume.UUID))
	return GenerateResourceNames(ctx, volume, tenancyDetails, gcpRegion)
}

// CheckBucketResourceName checks if the volume has a bucket resource name and returns the bucket details
func (a *VolumeUpdateActivity) CheckBucketResourceName(ctx context.Context, volume *datamodel.Volume) (*common.BucketDetails, error) {
	return CheckForBucketResourceName(ctx, a.SE, volume)
}

// UpdateBucketDetailsOfBackupVault updates the backup vault with the bucket details
func (a *VolumeUpdateActivity) UpdateBucketDetailsOfBackupVault(ctx context.Context, volume *datamodel.Volume, bucketDetails *common.BucketDetails) error {
	return UpdateBackupVaultWithBucketDetails(a.SE, ctx, volume, bucketDetails)
}

// RefreshVolumeFieldsInDB updates the used bytes of a volume in the database
func (a *VolumeUpdateActivity) RefreshVolumeFieldsInDB(ctx context.Context, volumeUUID string, volResponse *vsa.VolumeResponse) error {
	se := a.SE

	// add more fields to update if needed
	recordHeartbeat(ctx, fmt.Sprintf("Refreshing volume %s fields in database", volumeUUID))
	err := se.UpdateVolumeFields(ctx, volumeUUID, map[string]interface{}{
		"used_bytes": uint64(volResponse.UsedBytes),
	})
	if err != nil {
		util.GetLogger(ctx).Errorf("Failed to update volume fields in DB: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return nil
}

// VerifyIfBackupPolicyExistsInVCP checks if a backup policy exists in the VCP
func (a *VolumeUpdateActivity) VerifyIfBackupPolicyExistsInVCP(ctx context.Context, backupPolicyUUID string, accountId int64) (bool, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Checking backup policy %s in database", backupPolicyUUID))
	return CheckIfBackupPolicyExistsInVCP(ctx, a.SE, backupPolicyUUID, accountId)
}

// FetchAndCreateBackupPolicyFromSDE creates a backup policy in the VCP
func (a *VolumeUpdateActivity) FetchAndCreateBackupPolicyFromSDE(ctx context.Context, volume *datamodel.Volume, region string) (*datamodel.BackupPolicy, error) {
	recordHeartbeat(ctx, fmt.Sprintf("Fetching backup policy for volume %s from CVP", volume.UUID))
	return CreateBackupPolicyFetchedFromSDE(ctx, a.SE, volume, region)
}

// CreateScheduleForBackupPolicy creates a backup policy schedule in the VCP
func (a *VolumeUpdateActivity) CreateScheduleForBackupPolicy(ctx context.Context, backupPolicy *datamodel.BackupPolicy, customSchedule string) error {
	recordHeartbeat(ctx, fmt.Sprintf("Creating Temporal schedule for backup policy %s", backupPolicy.UUID))
	return CreateBackupPolicySchedule(ctx, a.Scheduler, backupPolicy, customSchedule)
}

// UpdateJunctionPathInONTAP updates the junction path for a volume in ONTAP
func (a *VolumeUpdateActivity) UpdateJunctionPathInONTAP(ctx context.Context, volume *datamodel.Volume, junctionPath string, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateJunctionPathInONTAP activity")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Get current junction path from volume attributes
	var currentJunctionPath string
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil {
		currentJunctionPath = volume.VolumeAttributes.FileProperties.JunctionPath
	}

	// Only proceed if junction path is different
	if currentJunctionPath != junctionPath {
		// If volume is currently mounted (has existing junction path), unmount it first
		activity.RecordHeartbeat(ctx, "Unmounting volume before updating junction path")
		logger.Debugf("Unmounting volume %s from junction path %s", volume.Name, currentJunctionPath)
		_, err := provider.UnmountVolume(volume.VolumeAttributes.ExternalUUID)
		if err != nil {
			logger.Errorf("Failed to unmount volume %s: %v", volume.Name, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Debugf("Volume %s unmounted successfully", volume.Name)
	}

	// Now mount the volume to the new junction path
	logger.Debugf("Mounting volume %s to new junction path %s", volume.Name, junctionPath)

	activity.RecordHeartbeat(ctx, "Mounting volume to new junction path")
	_, err = provider.MountVolume(vsa.MountVolumeParams{
		UUID:         volume.VolumeAttributes.ExternalUUID,
		JunctionPath: junctionPath,
	})
	if err != nil {
		logger.Errorf("Failed to mount volume %s to junction path %s: %v", volume.Name, junctionPath, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished UpdateJunctionPathInONTAP activity")
	logger.Debugf("Junction path updated successfully for volume %s in ONTAP", volume.Name)
	return nil
}

// UpdateExportPolicyRulesInONTAP updates the export policy rules for a volume in ONTAP
func (a *VolumeUpdateActivity) UpdateExportPolicyRulesInONTAP(ctx context.Context, volume *datamodel.Volume, exportPolicy *models.ExportPolicy, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateExportPolicyRulesInONTAP activity")

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Convert models.ExportPolicy to vsa.ExportPolicy
	vsaExportPolicy := &vsa.ExportPolicy{
		ExportPolicyName: exportPolicy.ExportPolicyName,
		ExportRules:      make([]*vsa.ExportRule, 0, len(exportPolicy.ExportRules)),
	}

	var allSquashUid *int64
	for _, rule := range exportPolicy.ExportRules {
		vsaRule := &vsa.ExportRule{
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
		}
		if utils.IsAllSquashEnabled && rule.AllSquash != nil && *rule.AllSquash && rule.AnonUid != nil && allSquashUid == nil {
			allSquashUid = rule.AnonUid
		}
		vsaExportPolicy.ExportRules = append(vsaExportPolicy.ExportRules, vsaRule)
	}
	if allSquashUid == nil && exportPolicy.ExportRules != nil && isAllSquashExistInPreviousExportPolicy(volume) {
		defaultUserID := int64(0)
		allSquashUid = &defaultUserID
	}
	recordHeartbeat(ctx, fmt.Sprintf("Updating export policy rules for volume %s in ONTAP", volume.UUID))
	err = provider.UpdateExportPolicyRules(vsa.UpdateExportPolicyRulesParams{
		VolumeName:   volume.Name,
		SvmName:      volume.Svm.Name,
		ExportPolicy: vsaExportPolicy,
	})
	if err != nil {
		logger.Errorf("Failed to update export policy for volume %s in ONTAP: %v", volume.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// When AllSquash is enabled, align the volume NAS owner UID (nas.uid) with the AnonUid so
	// the volume root directory inode reflects the correct owner for squashed clients.
	if allSquashUid != nil {
		recordHeartbeat(ctx, fmt.Sprintf("Updating NAS UID for volume %s in ONTAP", volume.UUID))
		err = provider.UpdateVolume(vsa.UpdateVolumeParams{
			UUID:   volume.VolumeAttributes.ExternalUUID,
			NasUid: allSquashUid,
		})
		if err != nil {
			logger.Errorf("Failed to set NAS UID for AllSquash on volume %s in ONTAP: %v", volume.Name, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
		logger.Infof("NAS UID set to %d for AllSquash on volume %s", *allSquashUid, volume.Name)
	}

	activity.RecordHeartbeat(ctx, "Finished UpdateExportPolicyRulesInONTAP activity")
	logger.Debugf("Export policy updated successfully for volume %s in ONTAP", volume.Name)
	return nil
}

func _isAllSquashExistInPreviousExportPolicy(volume *datamodel.Volume) bool {
	if volume != nil && volume.VolumeAttributes != nil && volume.VolumeAttributes.FileProperties != nil &&
		volume.VolumeAttributes.FileProperties.ExportPolicy != nil && len(volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules) > 0 {
		for _, rule := range volume.VolumeAttributes.FileProperties.ExportPolicy.ExportRules {
			if rule.AllSquash != nil && *rule.AllSquash {
				return true
			}
		}
	}
	return false
}

func (a *VolumeUpdateActivity) UpdateSMBShareSettings(ctx context.Context, volume *datamodel.Volume, params *common.UpdateVolumeParams, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateSMBShareSettings activity")

	if volume == nil || params == nil {
		logger.Warn("Parameters are empty")
		return nil
	}
	if volume.VolumeAttributes == nil || volume.VolumeAttributes.FileProperties == nil {
		logger.Errorf("Volume attributes or file properties are nil for volume %v when attempting to update SMB share settings", volume.Name)
		return vsaerrors.WrapAsTemporalApplicationError(errors.NewNotFoundErr("share", nil))
	}
	junctionPath := volume.VolumeAttributes.FileProperties.JunctionPath
	if len(junctionPath) < 1 {
		logger.Errorf("SMB share for volume %v not found when attempting to update SMB share settings", volume.Name)
		return vsaerrors.WrapAsTemporalApplicationError(errors.NewNotFoundErr("share", nil))
	}
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	cifsShareName := junctionPath[1:]
	var share []string
	recordHeartbeat(ctx, fmt.Sprintf("Fetching SMB share settings for volume %s from ONTAP", volume.UUID))
	share, err = provider.CifsShareCollectionGet(volume.Svm.SvmDetails.ExternalUUID, cifsShareName, []string{utils.CIFSSharePropertyCA})
	if err != nil {
		if !errors.IsNotFoundErr(err) {
			logger.Errorf("Failed to get SMB share %s for volume %v: %v", cifsShareName, volume.Name, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		} else {
			logger.Warnf("SMB share %s for volume %v not found when attempting to update SMB share settings", cifsShareName, volume.Name)
			return nil
		}
	}
	if slices.Contains(share, utils.CIFSSharePropertyCA) {
		return vsaerrors.WrapAsTemporalApplicationError(errors.NewBadRequestErr("SMB continuously_available share property cannot be modified or added during update"))
	}

	// Check if all values in params.SMBShareSettings are already present in share
	allPresent := true
	for _, setting := range params.SMBShareSettings {
		if !slices.Contains(share, setting) {
			allPresent = false
			break
		}
	}
	if allPresent {
		logger.Infof("No changes detected in SMB share settings for volume %v, skipping update", volume.Name)
		return nil
	}

	recordHeartbeat(ctx, fmt.Sprintf("Updating SMB share settings for volume %s in ONTAP", volume.UUID))
	return provider.UpdateCIFSServer(volume.Svm.SvmDetails.ExternalUUID, cifsShareName, params.SMBShareSettings)
}

// UpdateQoSPolicyGroupForVolume updates an existing QoS policy group in ONTAP
// with new throughput and IOPS values for a volume's autogenerated VPG
// TODO: Update this with the utilities added to support volume create
func (a *VolumeUpdateActivity) UpdateQoSPolicyGroupForVolume(
	ctx context.Context,
	volume *datamodel.Volume,
	throughputMibps int64,
	iops int64,
	node *models.Node,
) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateQoSPolicyGroupForVolume activity")

	// Validate inputs
	if volume.VolumePerformanceGroup == nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("volume %s has no VolumePerformanceGroup", volume.UUID))
	}

	if volume.VolumePerformanceGroup.OntapQosPolicyID == "" {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("volume %s has autogenerated VPG but missing OntapQosPolicyID", volume.UUID))
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finding existing QoS policy group")
	findParams := vsa.FindQoSGroupPolicyParams{
		UUID: volume.VolumePerformanceGroup.OntapQosPolicyID,
	}
	if _, parseErr := uuid.Parse(volume.VolumePerformanceGroup.OntapQosPolicyID); parseErr != nil {
		// Backwards compatibility: some records store the policy name instead of UUID.
		findParams = vsa.FindQoSGroupPolicyParams{
			Name: volume.VolumePerformanceGroup.OntapQosPolicyID,
		}
	}
	existingQosPolicy, err := provider.FindQoSGroupPolicy(findParams)
	if err != nil {
		logger.Error("Failed to find existing QoS policy group", "error", err, "policyUUID", volume.VolumePerformanceGroup.OntapQosPolicyID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Check if update is needed
	if existingQosPolicy.MaxThroughput == throughputMibps && existingQosPolicy.MaxIOPS == iops {
		logger.Info("QoS policy already matches the new requirements, no update needed",
			"policyName", existingQosPolicy.Name,
			"currentThroughput", existingQosPolicy.MaxThroughput,
			"newThroughput", throughputMibps,
			"currentIOPS", existingQosPolicy.MaxIOPS,
			"newIOPS", iops)
		return nil
	}

	updateQosPolicyParams := vsa.UpdateQoSGroupPolicyParams{
		UUID:          existingQosPolicy.UUID,
		SvmName:       existingQosPolicy.SvmName,
		MaxThroughput: throughputMibps,
		MaxIOPS:       iops,
	}

	activity.RecordHeartbeat(ctx, "Updating QoS policy group in ONTAP")
	err = provider.UpdateQoSGroupPolicy(updateQosPolicyParams)
	if err != nil {
		logger.Error("Failed to update QoS policy group", "error", err, "policyName", existingQosPolicy.Name, "policyUUID", existingQosPolicy.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy group updated successfully", "policyName", existingQosPolicy.Name, "policyUUID", existingQosPolicy.UUID)

	activity.RecordHeartbeat(ctx, "Finished UpdateQoSPolicyGroupForVolume activity")
	return nil
}

// UpdateVolumePerformanceGroupInDB updates a VolumePerformanceGroup in the database
func (a *VolumeUpdateActivity) UpdateVolumePerformanceGroupInDB(ctx context.Context, vpg *datamodel.VolumePerformanceGroup) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateVolumePerformanceGroupInDB activity")

	if vpg == nil {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("VolumePerformanceGroup is nil"))
	}

	err := a.SE.UpdateVolumePerformanceGroup(ctx, vpg)
	if err != nil {
		logger.Errorf("Failed to update VolumePerformanceGroup %s in database: %v", vpg.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished UpdateVolumePerformanceGroupInDB activity")
	logger.Debugf("VolumePerformanceGroup %s updated successfully in database", vpg.UUID)
	return nil
}

// UpdateVolumePerformanceGroupInDBForVolume updates a volume's VolumePerformanceGroupID and VolumePerformanceGroup
// to match the provided VPG. If vpg is nil, it unassigns the VPG from the volume.
func (a *VolumeUpdateActivity) UpdateVolumePerformanceGroupInDBForVolume(ctx context.Context, volumeUUID string, vpg *datamodel.VolumePerformanceGroup) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UpdateVolumePerformanceGroupInDBForVolume activity")

	if volumeUUID == "" {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("volumeUUID is empty"))
	}

	updates := make(map[string]interface{})

	if vpg == nil {
		// Unassign VPG from volume
		updates["volume_performance_group_id"] = nil
		logger.Info("Unassigning VPG from volume", "volumeUUID", volumeUUID)
	} else {
		// Get the VPG from database to get its ID (not UUID)
		dbVPG, err := a.SE.GetVolumePerformanceGroupByUUID(ctx, vpg.UUID)
		if err != nil {
			logger.Errorf("Failed to get VolumePerformanceGroup %s from database: %v", vpg.UUID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}

		updates["volume_performance_group_id"] = dbVPG.ID
		logger.Info("Assigning VPG to volume", "volumeUUID", volumeUUID, "vpgUUID", vpg.UUID, "vpgID", dbVPG.ID)
	}

	recordHeartbeat(ctx, fmt.Sprintf("Updating volume %s VPG assignment in database", volumeUUID))
	err := a.SE.UpdateVolumeFields(ctx, volumeUUID, updates)
	if err != nil {
		logger.Errorf("Failed to update volume %s VolumePerformanceGroupID in database: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Finished UpdateVolumePerformanceGroupInDBForVolume activity")
	logger.Debugf("Volume %s VolumePerformanceGroupID updated successfully in database", volumeUUID)
	return nil
}

// UnassignQoSPolicyFromVolume unassigns the QoS policy from a volume in ONTAP
func (a *VolumeUpdateActivity) UnassignQoSPolicyFromVolume(ctx context.Context, volume *datamodel.Volume, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting UnassignQoSPolicyFromVolume activity")

	if volume.VolumeAttributes == nil || volume.VolumeAttributes.ExternalUUID == "" {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("volume %s has no ExternalUUID", volume.UUID))
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activity.RecordHeartbeat(ctx, "Unassigning QoS policy from volume")
	none := "none"
	updateVolumeParams := vsa.UpdateVolumeParams{
		UUID:          volume.VolumeAttributes.ExternalUUID,
		QosPolicyName: &none,
	}
	err = provider.UpdateVolume(updateVolumeParams)
	if err != nil {
		logger.Error("Failed to unassign QoS policy from volume", "error", err, "volumeUUID", volume.UUID)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy unassigned from volume successfully", "volumeUUID", volume.UUID)
	activity.RecordHeartbeat(ctx, "Finished UnassignQoSPolicyFromVolume activity")
	return nil
}

// CreateQoSPolicyGroupForVolume creates a new QoS policy group for a volume with autogenerated naming
func (a *VolumeUpdateActivity) CreateAutoGeneratedQoSPolicyGroupForVolume(ctx context.Context, volume *datamodel.Volume, throughputMibps int64, iops int64, node *models.Node) (*vsa.QoSGroupPolicyResponse, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting CreateQoSPolicyGroupForVolume activity")

	if volume.Svm == nil || volume.Svm.Name == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("volume %s has no SVM name", volume.UUID))
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Generate autogenerated QoS policy name for volume
	// Using volume UUID to ensure uniqueness
	qosPolicyName := fmt.Sprintf("autoGenerated-%s-%s", volume.Name, uuid.NewString())

	// Check if the QoS policy already exists (idempotent behavior)
	findQosPolicyParams := vsa.FindQoSGroupPolicyParams{
		Name: qosPolicyName,
	}

	activity.RecordHeartbeat(ctx, "Checking for existing QoS policy group")
	existingQosPolicy, err := provider.FindQoSGroupPolicy(findQosPolicyParams)
	if err == nil {
		// QoS policy already exists, check if it matches our requirements
		if existingQosPolicy.MaxThroughput == throughputMibps && existingQosPolicy.MaxIOPS == iops {
			logger.Info("QoS policy already exists and matches requirements",
				"policyName", qosPolicyName,
				"throughput", existingQosPolicy.MaxThroughput,
				"iops", existingQosPolicy.MaxIOPS)
			return existingQosPolicy, nil
		}
		// Policy exists but with different values
		logger.Error("Failed to create new QosPolicyGroup as QoS policy group already exists with different values", "error", err, "policyName", qosPolicyName)
	}

	// QoS policy doesn't exist, create it
	logger.Info("QoS policy does not exist, creating new one", "policyName", qosPolicyName)

	isShared := false
	qosPolicyParams := vsa.CreateQoSGroupPolicyParams{
		Name:          qosPolicyName,
		SvmName:       volume.Svm.Name,
		MaxThroughput: throughputMibps,
		MaxIOPS:       iops,
		IsShared:      &isShared,
	}

	activity.RecordHeartbeat(ctx, "Creating QoS policy group")
	qosPolicyResponse, err := provider.CreateQoSGroupPolicy(qosPolicyParams)
	if err != nil {
		logger.Error("Failed to create QoS policy group", "error", err, "policyName", qosPolicyName)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy group created successfully", "policyName", qosPolicyResponse.Name, "policyUUID", qosPolicyResponse.UUID)
	activity.RecordHeartbeat(ctx, "Finished CreateQoSPolicyGroupForVolume activity")
	return qosPolicyResponse, nil
}

// AssignQoSPolicyToVolume assigns a QoS policy to a volume in ONTAP
func (a *VolumeUpdateActivity) AssignQoSPolicyToVolume(ctx context.Context, volume *datamodel.Volume, qosPolicyName string, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting AssignQoSPolicyToVolume activity")

	if volume.VolumeAttributes == nil || volume.VolumeAttributes.ExternalUUID == "" {
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("volume %s has no ExternalUUID", volume.UUID))
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	updateVolumeParams := vsa.UpdateVolumeParams{
		UUID:          volume.VolumeAttributes.ExternalUUID,
		QosPolicyName: &qosPolicyName,
	}

	activity.RecordHeartbeat(ctx, "Assigning QoS policy to volume")
	err = provider.UpdateVolume(updateVolumeParams)
	if err != nil {
		logger.Error("Failed to assign QoS policy to volume", "error", err, "volumeUUID", volume.UUID, "policyName", qosPolicyName)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy assigned to volume successfully", "volumeUUID", volume.UUID, "policyName", qosPolicyName)
	activity.RecordHeartbeat(ctx, "Finished AssignQoSPolicyToVolume activity")
	return nil
}

// FindQoSGroupPolicyForVolume finds a QoS policy group by UUID for a volume
func (a *VolumeUpdateActivity) FindQoSGroupPolicyForVolume(ctx context.Context, policyUUID string, svmName string, node *models.Node) (*vsa.QoSGroupPolicyResponse, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Starting FindQoSGroupPolicyForVolume activity")

	if policyUUID == "" {
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("policyUUID is empty"))
	}

	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	findQosPolicyParams := vsa.FindQoSGroupPolicyParams{
		UUID:    policyUUID,
		SvmName: svmName,
	}
	if _, parseErr := uuid.Parse(policyUUID); parseErr != nil {
		// Backwards compatibility: some records store the policy name instead of UUID.
		findQosPolicyParams = vsa.FindQoSGroupPolicyParams{
			Name:    policyUUID,
			SvmName: svmName,
		}
	}

	activity.RecordHeartbeat(ctx, "Finding QoS policy group")
	qosPolicy, err := provider.FindQoSGroupPolicy(findQosPolicyParams)
	if err != nil {
		logger.Error("Failed to find QoS policy group", "error", err, "policyUUID", policyUUID)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("QoS policy group found", "policyName", qosPolicy.Name, "policyUUID", qosPolicy.UUID)
	activity.RecordHeartbeat(ctx, "Finished FindQoSGroupPolicyForVolume activity")
	return qosPolicy, nil
}
