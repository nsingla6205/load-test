package backgroundactivities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"github.com/xyproto/randomstring"
)

const (
	backupTypeSCHEDULED       = "SCHEDULED"
	scheduledBackupNameFormat = "%s-scheduled-backup-%s-%s"
)

// ScheduledBackupActivity represents activities related to scheduled backups.
type ScheduledBackupActivity struct {
	SE database.Storage
}

// CheckExpertModeVolumeReady verifies that an expert mode volume is not in DELETING or DELETED state.
// Used as a pre-creation guard: if the volume has started or completed deletion, the backup operation
// must not proceed.
func (j *ScheduledBackupActivity) CheckExpertModeVolumeReady(ctx context.Context, volumeExternalUUID string) error {
	logger := util.GetLogger(ctx)
	vol, err := j.SE.GetExpertModeVolumeByExternalUUID(ctx, volumeExternalUUID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			logger.Warnf("Expert mode volume %s not found, cannot proceed with backup operation", volumeExternalUUID)
			return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("expert mode volume %s not found, cannot proceed with backup operation", volumeExternalUUID)),
			)
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if vol.State == datamodel.LifeCycleStateDeleting || vol.State == datamodel.LifeCycleStateDeleted {
		logger.Warnf("Expert mode volume %s is in state %s, cannot proceed with backup operation", volumeExternalUUID, vol.State)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("expert mode volume %s is in state %s, cannot proceed with backup operation", volumeExternalUUID, vol.State)),
		)
	}
	return nil
}

// CreateScheduledBackup creates a scheduled backup for the given volume and backup vault.
// Returns the created Backup object or an error.
func (j *ScheduledBackupActivity) CreateScheduledBackup(ctx context.Context, volume *datamodel.Volume, backupVault *datamodel.BackupVault, timestamp, scheduleTag string, isExpertMode bool) (*datamodel.Backup, error) {
	se := j.SE
	logger := util.GetLogger(ctx)

	var volumeUUIDForScheduledBackup string
	if isExpertMode {
		volumeUUIDForScheduledBackup = volume.VolumeAttributes.ExternalUUID

		// Guard against the race where deletion started after GetVolumesByBackupPolicyUUID
		// picked the volume up as READY.
		if err := j.CheckExpertModeVolumeReady(ctx, volumeUUIDForScheduledBackup); err != nil {
			return nil, err
		}
	} else {
		volumeUUIDForScheduledBackup = volume.UUID
	}

	name := fmt.Sprintf(scheduledBackupNameFormat, scheduleTag, RandomString(8), timestamp)
	isExpertModeBackup := volume.Pool != nil && volume.Pool.APIAccessMode == "ONTAP"

	var protocols []string
	if volume.VolumeAttributes != nil {
		protocols = volume.VolumeAttributes.Protocols
	}

	var accountIdentifier string
	if volume.Account != nil {
		accountIdentifier = volume.Account.Name
	}

	backupAttributes := &datamodel.BackupAttributes{
		VolumeName:         volume.Name,
		Protocols:          protocols,
		AccountIdentifier:  accountIdentifier,
		IsExpertModeBackup: isExpertModeBackup,
	}

	backup, err := se.CreateBackup(ctx, &datamodel.Backup{
		BaseModel: datamodel.BaseModel{
			UUID: utils.RandomUUID(),
		},
		Name:          name,
		State:         datamodel.LifeCycleStateCreating,
		StateDetails:  datamodel.LifeCycleStateCreatingDetails,
		Type:          backupTypeSCHEDULED,
		ScheduleTag:   &scheduleTag,
		VolumeUUID:    volumeUUIDForScheduledBackup,
		BackupVaultID: backupVault.ID,
		BackupVault:   backupVault,
		Attributes:    backupAttributes,
	})
	if err != nil {
		return nil, err
	}

	logger.Infof("Created scheduled backup: name=%s volumeUUID=%s backupUUID=%s",
		backup.Name, volume.UUID, backup.UUID)

	return backup, nil
}

// GenerateScheduledSnapshotName generates a name for a scheduled snapshot using a random string and timestamp.
// Returns the generated name or an error.
func (j *ScheduledBackupActivity) GenerateScheduledSnapshotName(ctx context.Context, timestamp string) (string, error) {
	return fmt.Sprintf("scheduled-snapshot-%s-%s", RandomString(8), timestamp), nil
}

// HydrateCreatedBackupsToCCFE sends information about created scheduled backups to CCFE.
// Returns an error if the operation fails.
func (j *ScheduledBackupActivity) HydrateCreatedBackupsToCCFE(ctx context.Context, volume *datamodel.Volume, backups []*datamodel.Backup, backupVaultName string) error {
	logger := util.GetLogger(ctx)

	if !hydrationEnabled {
		logger.Info("Hydration is disabled, skipping created backups hydration to CCFE")
		return nil
	}

	if len(backups) == 0 {
		logger.Warnf("HydrateCreatedBackupsToCCFE called with no backups for volume %s", volume.Name)
		return nil
	}

	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		return err
	}
	region, err := utils.GetBackupRegion(volume)
	if err != nil {
		return err
	}
	projectId := volume.Account.Name
	mode := models.BackupHydrationModeDefault
	var sourceStoragePool string
	if volume.Pool != nil && volume.Pool.APIAccessMode == "ONTAP" {
		mode = models.BackupHydrationModeONTAP
		var location string
		if volume.Pool.PoolAttributes.IsRegionalHA {
			location = region
		} else {
			location = volume.Pool.PoolAttributes.PrimaryZone
		}
		sourceStoragePool = fmt.Sprintf("projects/%s/locations/%s/storagePools/%s", projectId, location, volume.Pool.Name)
	}
	requests := common.ConvertToGCPHydrateBackupCreateRequests(backups, mode, sourceStoragePool)
	err = common.HydrateCreatedBackups(ctx, logger, requests, backupVaultName, region, projectId, token)
	if err != nil {
		return err
	}
	return nil
}

// HydrateDeletedBackupsToCCFE sends information about deleted scheduled backups to CCFE.
// Returns an error if the operation fails.
func (j *ScheduledBackupActivity) HydrateDeletedBackupsToCCFE(ctx context.Context, volume *datamodel.Volume, backups []*datamodel.Backup, backupVaultName string) error {
	logger := util.GetLogger(ctx)

	if !hydrationEnabled {
		logger.Info("Hydration is disabled, skipping deleted backups hydration to CCFE")
		return nil
	}

	if len(backups) == 0 {
		logger.Warnf("HydrateDeletedBackupsToCCFE called with no backups for volume %s", volume.Name)
		return nil
	}

	token, err := auth.GenerateCallbackToken(ctx)
	if err != nil {
		return err
	}
	region, err := utils.GetBackupRegion(volume)
	if err != nil {
		return err
	}
	projectId := volume.Account.Name
	names := common.ConvertToGCPHydrateBackupDeleteRequests(backups)
	err = common.HydrateDeletedBackups(ctx, logger, names, backupVaultName, region, projectId, token)
	if err != nil {
		return err
	}
	return nil
}

// GetBackupPolicyByUUID retrieves a backup policy from the database by UUID and account ID.
// Returns the BackupPolicy object or an error.
func (j *ScheduledBackupActivity) GetBackupPolicyByUUID(ctx context.Context, backupPolicyUUID string, accountID int64) (*datamodel.BackupPolicy, error) {
	se := j.SE
	backupPolicy, err := se.GetBackupPolicyByUUIDAndOwnerID(ctx, backupPolicyUUID, accountID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, vsaerrors.WrapAsTemporalApplicationError(
				vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, err),
			)
		}
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return backupPolicy, nil
}

// GetVolumesByBackupPolicyUUID retrieves volumes that have the specified backup policy enabled for a given account.
// Returns a slice of Volume objects or an error.
func (j *ScheduledBackupActivity) GetVolumesByBackupPolicyUUID(ctx context.Context, backupPolicyUUID string, accountID int64, limit, offset int) ([]*datamodel.Volume, error) {
	se := j.SE
	// Get the list of all volumes which have the specified backup policy enabled
	logger := util.GetLogger(ctx)

	conditions := [][]interface{}{
		{"account_id = ?", accountID},
		{"state = ?", datamodel.LifeCycleStateREADY},
		{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
		{"data_protection->>'scheduled_backup_enabled' = 'true'"},
	}
	pagination := &dbutils.Pagination{
		Limit:  limit,
		Offset: offset,
	}
	volumes, err := se.ListVolumesWithPagination(ctx, conditions, pagination)
	if err != nil {
		return nil, err
	}

	expertModeConditions := [][]interface{}{
		{"account_id = ?", accountID},
		{"state = ?", datamodel.LifeCycleStateAvailable},
		{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
		{"data_protection->>'scheduled_backup_enabled' = 'true'"},
	}
	// Also fetch expert mode volumes with the same backup policy
	// Expert mode volumes are stored separately but need to be included for scheduled backups
	expertModeVolumes, err := se.ListExpertModeVolumesWithPagination(ctx, expertModeConditions, pagination)
	if err != nil {
		logger.Warnf("Failed to fetch expert mode volumes with backup policy %s: %v", backupPolicyUUID, err)
		// Don't fail the entire operation, just log and continue with regular volumes
		return volumes, nil
	}

	// Convert expert mode volumes to regular volume format for processing
	for _, expertVol := range expertModeVolumes {
		// Convert expert mode volume to datamodel.Volume format
		// This allows the same scheduled backup workflow to process both types
		convertedVolume := &datamodel.Volume{
			BaseModel:   expertVol.BaseModel,
			Name:        expertVol.Name,
			Description: expertVol.Description,
			SizeInBytes: expertVol.SizeInBytes,
			State:       expertVol.State,
			AccountID:   expertVol.AccountID,
			PoolID:      expertVol.PoolID,
			Account:     expertVol.Account,
			Pool:        expertVol.Pool,
			Svm:         expertVol.Svm,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID:   expertVol.ExternalUUID,
				Protocols:      []string{},
				VendorSubnetID: expertVol.Pool.Network,
			},
			DataProtection: expertVol.BackupConfig,
		}
		volumes = append(volumes, convertedVolume)
	}

	logger.Infof("Found %d total volumes (%d regular + %d expert mode) with backup policy %s",
		len(volumes), len(volumes)-len(expertModeVolumes), len(expertModeVolumes), backupPolicyUUID)
	return volumes, nil
}

// FetchScheduledBackupForDeletion fetches scheduled backups for a volume and backup policy that are eligible for deletion.
// Returns a slice of Backup objects or an error.
func (j *ScheduledBackupActivity) FetchScheduledBackupForDeletion(ctx context.Context, volume *datamodel.Volume, backupPolicy *datamodel.BackupPolicy, isExpertMode bool) ([]*datamodel.Backup, error) {
	se := j.SE
	return se.FetchScheduledBackupsForDeletion(ctx, volume, backupPolicy, isExpertMode)
}

func (j *ScheduledBackupActivity) CreateBackupSnapshotInDB(ctx context.Context, volume *datamodel.Volume, snapshotName string) (*datamodel.Snapshot, error) {
	se := j.SE
	logger := util.GetLogger(ctx)

	snapshot := &datamodel.Snapshot{
		Name:               snapshotName,
		Description:        activities.BackupComment,
		VolumeID:           volume.ID,
		AccountID:          volume.AccountID,
		Volume:             volume,
		Account:            volume.Account,
		IsAppConsistent:    false,
		Type:               SnapshotTypeBackup,
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	}
	dbSnapshot, err := se.CreatingSnapshot(ctx, snapshot)
	if err != nil {
		logger.Errorf("Failed to create snapshot in database. Error: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return dbSnapshot, nil
}

func (j *ScheduledBackupActivity) UpdateBackupSnapshotInDB(ctx context.Context, dbSnapshot *datamodel.Snapshot, ontapSnapshot *vsa.SnapshotProviderResponse) (*datamodel.Snapshot, error) {
	se := j.SE
	logger := util.GetLogger(ctx)

	// Update the snapshot in the database
	dbSnapshot.State = datamodel.LifeCycleStateREADY
	dbSnapshot.StateDetails = datamodel.LifeCycleStateAvailableDetails
	dbSnapshot.SnapshotAttributes = &datamodel.SnapshotAttributes{
		SizeInBytes:            ontapSnapshot.SizeInBytes,
		LogicalSizeUsedInBytes: ontapSnapshot.LogicalSizeInBytes,
	}
	dbSnapshot.SetExternalUUID(ontapSnapshot.ExternalUUID)

	_, err := se.UpdateSnapshot(ctx, dbSnapshot)
	if err != nil {
		logger.Errorf("Failed to update snapshot details in database. Error: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return dbSnapshot, nil
}

func (j *ScheduledBackupActivity) DeleteBackupSnapshotInDB(ctx context.Context, snapshotUUID string) error {
	se := j.SE
	logger := util.GetLogger(ctx)

	_, err := se.DeleteSnapshot(ctx, snapshotUUID)
	if err != nil {
		logger.Errorf("Failed to delete snapshot in database. Error: %v", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *ScheduledBackupActivity) UpdateBackupState(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	se := j.SE
	updated, err := se.UpdateBackupState(ctx, backup)
	if err != nil {
		return nil, err
	}
	return updated, nil
}

// UpdateBackupSize persists backup size via FinishBackup and updates volume backup_chain_bytes (regular volumes only).
func (j *ScheduledBackupActivity) UpdateBackupSize(ctx context.Context, backup *datamodel.Backup, volume *datamodel.Volume, isExpertMode bool) error {
	logger := util.GetLogger(ctx)
	se := j.SE

	var volUUID string
	if isExpertMode {
		volUUID = volume.VolumeAttributes.ExternalUUID
	} else {
		volUUID = volume.UUID
	}

	_, err := se.FinishBackup(ctx, backup)
	if err != nil {
		logger.Errorf("Failed to update backup %s with size information: %v", backup.UUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if utils.EnableBackupVaultSwitching {
		latestBackup, latestErr := se.GetLatestBackupByVolumeUUID(ctx, volUUID)
		if latestErr == nil && latestBackup != nil {
			if err := se.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, volUUID, latestBackup.UUID); err != nil {
				logger.Errorf("Failed to zero other backups for volume %s: %v", volUUID, err)
				return vsaerrors.WrapAsTemporalApplicationError(err)
			}
		}
	} else if backup.LatestLogicalBackupSize != 0 {
		err = se.UpdateBackupLatestLogicalBackupSizeByVolume(ctx, volUUID, backup.UUID)
		if err != nil {
			logger.Errorf("Failed to reset LatestLogicalBackupSize for previous backups of volume %s: %v", volUUID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	if isExpertMode {
		chainBytes := backup.LatestLogicalBackupSize
		volume.DataProtection.BackupChainBytes = &chainBytes
		updates := map[string]interface{}{
			"data_protection": volume.DataProtection,
		}
		err = se.UpdateExpertModeVolumeFields(ctx, volUUID, updates)
		if err != nil {
			logger.Errorf("Failed to update expert mode volume %s with latest logical backup size: %v", volUUID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	} else {
		backupActivity := activities.BackupActivity{SE: se}
		if err = backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity(ctx, volUUID); err != nil {
			return err
		}
	}

	logger.Debugf("Successfully updated backup size fields for backup %s and volume %s", backup.UUID, volUUID)
	return nil
}

func (j *ScheduledBackupActivity) GetSnapshotByNameAndVolumeID(ctx context.Context, snapshotName string, accountID, volumeID int64) (*datamodel.Snapshot, error) {
	logger := util.GetLogger(ctx)

	se := j.SE
	snapshot, err := se.GetSnapshotByNameAndVolumeId(ctx, snapshotName, accountID, volumeID)
	if err != nil {
		logger.Errorf("Failed to get snapshot by name and volumeID: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return snapshot, nil
}

// RandomString generates a human-friendly random string of the specified length.
// Returns the generated string.
func RandomString(n int) string {
	return randomstring.HumanFriendlyEnglishString(n)
}

// CreateRemoteScheduledBackupsFromVCPActivity creates remote backups in the remote region for scheduled backups
func (j *ScheduledBackupActivity) CreateRemoteScheduledBackupsFromVCPActivity(ctx context.Context, backupVault *datamodel.BackupVault, backups []*datamodel.Backup, volume *datamodel.Volume, projectNumber string) error {
	logger := util.GetLogger(ctx)

	// Check if this is a cross-region backup vault
	if backupVault.BackupVaultType != activities.CrossRegionBackupType || backupVault.BackupRegionName == nil {
		// Not a cross-region backup, skip
		return nil
	}

	// Create remote backup for each backup
	backupActivity := &activities.BackupActivity{SE: j.SE}
	for _, backup := range backups {
		// Create context with backup vault and volume information
		backupActivitiesContext := &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
		}

		err := backupActivity.CreateRemoteBackupFromVCPActivity(ctx, backupActivitiesContext)
		if err != nil {
			logger.Errorf("Failed to create remote backup from VCP for scheduled backup %s: %v", backup.UUID, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}
	}

	logger.Infof("Successfully created %d remote backups for scheduled backups", len(backups))
	return nil
}

// DeleteRemoteScheduledBackupFromVCPActivity deletes a remote backup in the remote region for scheduled backup
func (j *ScheduledBackupActivity) DeleteRemoteScheduledBackupFromVCPActivity(ctx context.Context, backupUUID, backupVaultUUID, projectNumber, region string) error {
	logger := util.GetLogger(ctx)
	backupActivity := &activities.BackupActivity{SE: j.SE}

	err := backupActivity.DeleteRemoteBackupFromVCPActivity(ctx, backupUUID, backupVaultUUID, projectNumber, region)
	if err != nil {
		logger.Errorf("Failed to delete remote backup from VCP for scheduled backup %s: %v", backupUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully deleted remote backup for scheduled backup %s", backupUUID)
	return nil
}

// CheckBackupsInProgressByVolume checks if any backup for the volume is in CREATING or DELETING state.
// Excludes the specified backup UUIDs from the check (typically the backups being created in the current workflow).
// Returns an error if a backup is found in these states to prevent parallel transfers on the same Snapmirror instance.
func (j *ScheduledBackupActivity) CheckBackupsInProgressByVolume(ctx context.Context, volumeUUID string, excludedBackupUUIDs []string, createdBefore *time.Time) error {
	se := j.SE
	logger := util.GetLogger(ctx)

	backupInTransition, err := se.AreBackupsInProgressForVolume(ctx, volumeUUID, excludedBackupUUIDs, createdBefore)
	if err != nil {
		logger.Errorf("Failed to check backup state for volume %s: %v", volumeUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	if backupInTransition {
		logger.Warnf("Another backup operation is already in progress for volume %s. Skipping to prevent parallel transfers on the same Snapmirror instance", volumeUUID)
		return vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("another backup operation is already in progress for volume %s. Please wait for it to complete before starting a new backup", volumeUUID))
	}

	logger.Infof("No in-progress backups blocking transfer for volume: volumeUUID=%s", volumeUUID)
	return nil
}
