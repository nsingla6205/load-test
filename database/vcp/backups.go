package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

var (
	deleteBackup                         = _deleteBackup
	getBackupWithDetails                 = _getBackupWithDetails
	getBackupVaultByNameAndBackupVaultID = _getBackupVaultByNameAndBackupVaultID
)

const (
	BackupTypeScheduled = "SCHEDULED"
	Daily               = "daily"
	Weekly              = "weekly"
	Monthly             = "monthly"
	OntapFgVolumeStyle  = "flexgroup"
)

// backupChainHistoryParams holds parameters for creating/updating backup chain history
type backupChainHistoryParams struct {
	ResourceName       string
	VolumeUUID         string
	Size               int64
	ConsumerID         string
	DeploymentName     string
	Timestamp          time.Time
	IsExpertModeBackup bool
	EndpointUUID       string
}

type BackupMetricsData struct {
	UUID          string                      `gorm:"column:uuid"`
	VolumeUUID    string                      `gorm:"column:volume_uuid"`
	Attributes    *datamodel.BackupAttributes `gorm:"column:attributes;type:jsonb"`
	BackupVaultID int64                       `gorm:"column:backup_vault_id"`
	// BackupVault fields (from JOIN)
	VaultAccountID        int64   `gorm:"column:vault_account_id"`
	VaultAccountName      string  `gorm:"column:vault_account_name"`
	VaultName             string  `gorm:"column:vault_name"`
	VaultBackupRegionName *string `gorm:"column:vault_backup_region_name"`
	VaultServiceType      string  `gorm:"column:vault_service_type"`
	VolumeAccountID       int64   `gorm:"column:volume_account_id"`
}

// createBackupChainHistoryEntry creates a new backup chain history entry
func createBackupChainHistoryEntry(ctx context.Context, tx *gorm.DB, params backupChainHistoryParams) error {
	var endpointUUID *string
	if params.EndpointUUID != "" {
		endpointUUID = &params.EndpointUUID
	}
	history := &datamodel.BackupChainHistory{
		BaseModel: datamodel.BaseModel{
			UUID:      utils.RandomUUID(),
			CreatedAt: params.Timestamp,
			UpdatedAt: params.Timestamp,
			DeletedAt: nil,
		},
		ResourceName:       params.ResourceName,
		ResourceUUID:       params.VolumeUUID,
		Size:               params.Size,
		ConsumerID:         params.ConsumerID,
		DeploymentName:     params.DeploymentName,
		IsExpertModeBackup: params.IsExpertModeBackup,
		EndpointUUID:       endpointUUID,
	}
	if err := tx.Create(history).Error; err != nil {
		return err
	}
	util.GetLogger(ctx).Infof("Ledger: Successfully created backup chain history for volume %s with size %d",
		params.VolumeUUID, params.Size)
	return nil
}

type backupChainHistoryUpdateParams struct {
	VolumeUUID     string
	EndpointUUID   string
	NewSize        int64
	TimeStamp      time.Time
	ResourceName   string
	ConsumerID     string
	DeploymentName string
	SkipCreate     bool
}

// pickRowToTombstone returns the active row to tombstone for the given endpoint, or nil if none should be touched.
func pickRowToTombstone(activeRows []datamodel.BackupChainHistory, endpointUUID string) *datamodel.BackupChainHistory {
	switch len(activeRows) {
	case 0:
		return nil
	case 1:
		row := &activeRows[0]
		if row.EndpointUUID == nil || *row.EndpointUUID == endpointUUID {
			return row
		}
		return nil
	default:
		if endpointUUID == "" {
			return nil
		}
		for i := range activeRows {
			if activeRows[i].EndpointUUID != nil && *activeRows[i].EndpointUUID == endpointUUID {
				return &activeRows[i]
			}
		}
		return nil
	}
}

// writeBackupChainHistory implements the full multi-endpoint decision matrix for
// inserting/updating backup chain history rows.
func writeBackupChainHistory(ctx context.Context, tx *gorm.DB, params backupChainHistoryUpdateParams) error {
	logger := util.GetLogger(ctx)

	var activeRows []datamodel.BackupChainHistory
	err := tx.Where("resource_uuid = ? AND deleted_at IS NULL", params.VolumeUUID).
		Find(&activeRows).Error
	if err != nil {
		return err
	}

	rowToTombstone := pickRowToTombstone(activeRows, params.EndpointUUID)

	if rowToTombstone != nil && rowToTombstone.Size == params.NewSize {
		logger.Debugf("Ledger: Backup chain history size unchanged for volume %s endpoint %q (size: %d)",
			params.VolumeUUID, params.EndpointUUID, params.NewSize)
		return nil
	}

	if rowToTombstone != nil {
		err = tx.Model(&datamodel.BackupChainHistory{}).
			Where("id = ?", rowToTombstone.ID).
			Update("deleted_at", params.TimeStamp).Error
		if err != nil {
			logger.Warnf("Ledger: Failed to mark current backup chain history as deleted for volume %s: %v", params.VolumeUUID, err)
			return err
		}
	}

	if params.SkipCreate {
		return nil
	}

	resourceName := params.ResourceName
	consumerID := params.ConsumerID
	deploymentName := params.DeploymentName
	if rowToTombstone != nil {
		if resourceName == "" {
			resourceName = rowToTombstone.ResourceName
		}
		if consumerID == "" {
			consumerID = rowToTombstone.ConsumerID
		}
		if deploymentName == "" {
			deploymentName = rowToTombstone.DeploymentName
		}
	}

	isExpertModeBackup := rowToTombstone != nil && rowToTombstone.IsExpertModeBackup
	err = createBackupChainHistoryEntry(ctx, tx, backupChainHistoryParams{
		ResourceName:       resourceName,
		VolumeUUID:         params.VolumeUUID,
		Size:               params.NewSize,
		ConsumerID:         consumerID,
		DeploymentName:     deploymentName,
		Timestamp:          params.TimeStamp,
		IsExpertModeBackup: isExpertModeBackup,
		EndpointUUID:       params.EndpointUUID,
	})
	if err != nil {
		return err
	}

	oldSize := int64(0)
	if rowToTombstone != nil {
		oldSize = rowToTombstone.Size
	}
	logger.Infof("Ledger: Updated backup chain history for volume %s endpoint %q: old size %d -> new size %d",
		params.VolumeUUID, params.EndpointUUID, oldSize, params.NewSize)
	return nil
}

func (d *DataStoreRepository) GetBackupByNameAndBackupVaultID(ctx context.Context, backupName string, backupVaultID int64) (*datamodel.Backup, error) {
	return getBackupVaultByNameAndBackupVaultID(d.db.GORM().WithContext(ctx), &datamodel.Backup{Name: backupName, BackupVaultID: backupVaultID})
}

func _getBackupVaultByNameAndBackupVaultID(db *gorm.DB, query *datamodel.Backup) (*datamodel.Backup, error) {
	backup := &datamodel.Backup{}
	err := db.Preload("BackupVault").First(&backup, query).Error
	if err != nil {
		return nil, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "backup", &backup.UUID)
	}
	return backup, nil
}

func (d *DataStoreRepository) CreateBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	if tx.Where("name = ?", backup.Name).Where("backup_vault_id = ?", backup.BackupVaultID).First(&backup).Error != nil {
		backup.UUID = utils.RandomUUID()
		backup.State = datamodel.LifeCycleStateCreating
		backup.StateDetails = datamodel.LifeCycleStateCreatingDetails
		backup.CreatedAt = time.Now()
		backup.UpdatedAt = backup.CreatedAt

		err := tx.Create(backup).Error
		if err != nil {
			return nil, err
		}

		dbBackup, err := getBackupWithDetails(tx, &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: backup.UUID}})
		if err != nil {
			return nil, err
		}

		return dbBackup, nil
	}
	return nil, customerrors.NewUserInputValidationErr("backup already exists")
}

// markPreviousBackupChainHistoryAsDeleted soft-deletes active backup chain history rows for a volume.
// When endpointUUID is non-empty only the row for that endpoint is soft-deleted; when empty all
// active rows for the volume are soft-deleted (used when no backups remain at all).
func markPreviousBackupChainHistoryAsDeleted(ctx context.Context, tx *gorm.DB, volumeUUID string, endpointUUID string, timeStamp time.Time) error {
	q := tx.Model(&datamodel.BackupChainHistory{}).
		Where("resource_uuid = ?", volumeUUID).
		Where("deleted_at IS NULL")
	if endpointUUID != "" {
		q = q.Where("endpoint_uuid = ?", endpointUUID)
	}
	result := q.Update("deleted_at", timeStamp)
	if result.Error != nil {
		return result.Error
	}
	util.GetLogger(ctx).Infof("Ledger: Successfully marked %d backup chain history entries as deleted for volume %s endpoint %q",
		result.RowsAffected, volumeUUID, endpointUUID)
	return nil
}

// shouldSkipBackupChainHistory returns true for backup types that are not billed based on
// current feature flag configuration, so chain history entries are unnecessary.
func shouldSkipBackupChainHistory(ctx context.Context, backup *datamodel.Backup, config *common.TelemetryConfig) bool {
	logger := util.GetLogger(ctx)
	if backup == nil || config == nil {
		logger.Warnf("shouldSkipBackupChainHistory called with nil backup or config, skipping chain history to avoid invalid billing records")
		return true
	}
	// Cross-region backups where region is nil or matches current region (even when billing enabled)
	if backup.BackupVault != nil &&
		backup.BackupVault.BackupVaultType == datamodel.BackupVaultTypeCrossRegion {
		if backup.BackupVault.BackupRegionName == nil {
			logger.Warnf("Skipping BackupLogicalSize billing for cross-region backup %s (volume %s): BackupRegionName is nil", backup.UUID, backup.VolumeUUID)
			return true
		}
		if *backup.BackupVault.BackupRegionName == config.RegionName {
			logger.Warnf("Skipping BackupLogicalSize billing for cross-region backup %s (volume %s): BackupRegionName %s matches current region", backup.UUID, backup.VolumeUUID, *backup.BackupVault.BackupRegionName)
			return true
		}
	}
	return false
}

func _getBackupWithDetails(db *gorm.DB, query *datamodel.Backup) (*datamodel.Backup, error) {
	backup := &datamodel.Backup{}
	err := db.Preload("BackupVault").First(&backup, query).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var identifier *string
			if query != nil {
				switch {
				case query.UUID != "":
					identifier = &query.UUID
				case query.ExternalUUID != "":
					identifier = &query.ExternalUUID
				}
			}
			return nil, customerrors.NewNotFoundErr("backup", identifier)
		}
		return nil, err
	}
	return backup, nil
}

func (d *DataStoreRepository) GetBackupCountByBackupVaultID(ctx context.Context, backupVaultID int64) (int64, error) {
	var count int64
	err := d.db.GORM().WithContext(ctx).Model(&datamodel.Backup{}).Where("backup_vault_id = ?", backupVaultID).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetVolumeCountByBackupVaultID(ctx context.Context, backupVaultUUID string) (int64, error) {
	var volumeCount int64
	var expertModeVolumeCount int64
	err := d.db.GORM().WithContext(ctx).Model(&datamodel.Volume{}).
		Where("data_protection->>'backup_vault_id' = ?", backupVaultUUID).
		Count(&volumeCount).Error
	if err != nil {
		return 0, err
	}

	// fetch count from expert mode volumes as well
	err = d.db.GORM().WithContext(ctx).Model(&datamodel.ExpertModeVolumes{}).
		Where("data_protection->>'backup_vault_id' = ?", backupVaultUUID).
		Count(&expertModeVolumeCount).Error
	if err != nil {
		return 0, err
	}
	return volumeCount + expertModeVolumeCount, nil
}

func (d *DataStoreRepository) GetVolumesByBackupVaultID(ctx context.Context, backupVaultUUID string) ([]*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)
	var volumes []*datamodel.Volume
	err := db.Where("data_protection->>'backup_vault_id' = ?", backupVaultUUID).Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

func (d *DataStoreRepository) GetExpertModeVolumesByBackupVaultID(ctx context.Context, backupVaultUUID string) ([]*datamodel.ExpertModeVolumes, error) {
	db := d.db.GORM().WithContext(ctx)
	var volumes []*datamodel.ExpertModeVolumes
	err := db.Where("data_protection->>'backup_vault_id' = ?", backupVaultUUID).Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

func (d *DataStoreRepository) GetBackupsByBackupVaultOwnerIDAndFilter(ctx context.Context, backupVaultUUID string, accountID int64, filters [][]interface{}) ([]*datamodel.Backup, error) {
	bv, err := d.GetBackupVaultByUUIDndOwnerID(ctx, backupVaultUUID, accountID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, customerrors.NewNotFoundErr("backup vault", nil)
		}
		return nil, err
	}
	// If no filters are provided, fetch all backups for the backup vault
	if len(filters) == 0 {
		return getBackupsByBackupVault(d.db.GORM().WithContext(ctx), bv.ID)
	}
	return getBackupsByBackupVault(d.db.ApplyFilter(filters).GORM().WithContext(ctx), bv.ID)
}

// GetBackupsByBackupVaultUUIDAndFilter retrieves backups by vault UUID without account filtering
// This is used for GCBDR vaults where backups can come from multiple accounts/projects
func (d *DataStoreRepository) GetBackupsByBackupVaultUUIDAndFilter(ctx context.Context, backupVaultUUID string, filters [][]interface{}) ([]*datamodel.Backup, error) {
	bv, err := d.GetBackupVault(ctx, backupVaultUUID)
	if err != nil {
		if customerrors.IsNotFoundErr(err) {
			return nil, customerrors.NewNotFoundErr("backup vault", nil)
		}
		return nil, err
	}
	// If no filters are provided, fetch all backups for the backup vault
	if len(filters) == 0 {
		return getBackupsByBackupVault(d.db.GORM().WithContext(ctx), bv.ID)
	}
	return getBackupsByBackupVault(d.db.ApplyFilter(filters).GORM().WithContext(ctx), bv.ID)
}

func getBackupsByBackupVault(db *gorm.DB, backupVaultUUID int64) ([]*datamodel.Backup, error) {
	var backups []*datamodel.Backup

	err := db.Preload("BackupVault").Where("backup_vault_id = ?", backupVaultUUID).Find(&backups).Error
	if err != nil {
		return nil, err
	}

	return backups, nil
}

func (d *DataStoreRepository) GetBackup(ctx context.Context, backupVaultUUID string, backupUUID string, accountName string) (*datamodel.Backup, error) {
	return getBackup(d.db.GORM().WithContext(ctx), backupVaultUUID, backupUUID, accountName)
}

func getBackup(db *gorm.DB, backupVaultUUID string, backupUUID string, accountName string) (*datamodel.Backup, error) {
	// Retrieve the backup vault details using the backupVaultUUID and account
	backupVault, err := getBackupVaultWithDetails(db, &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: backupVaultUUID},
		Account: &datamodel.Account{
			Name: accountName,
		},
	})
	if err != nil {
		return nil, err
	}
	if backupVault.Account.Name != accountName {
		return nil, customerrors.NewNotFoundErr("backup vault", &backupVaultUUID)
	}

	// Retrieve the backup using the backupVaultUUID and backupUUID
	var backup *datamodel.Backup
	backup, err = getBackupWithDetails(db, &datamodel.Backup{
		BaseModel:     datamodel.BaseModel{UUID: backupUUID},
		BackupVaultID: backupVault.ID,
	})
	if err != nil {
		return nil, err
	}

	return backup, nil
}

func (d *DataStoreRepository) GetBackupByExternalUUID(ctx context.Context, backupVaultUUID string, externalUUID string, accountName string) (*datamodel.Backup, error) {
	return getBackupByExternalUUID(d.db.GORM().WithContext(ctx), backupVaultUUID, externalUUID, accountName)
}

func getBackupByExternalUUID(db *gorm.DB, backupVaultUUID string, externalUUID string, accountName string) (*datamodel.Backup, error) {
	// Retrieve the backup vault details using the backupVaultUUID and account
	backupVault, err := getBackupVaultWithDetails(db, &datamodel.BackupVault{
		ExternalUUID: &backupVaultUUID,
		Account: &datamodel.Account{
			Name: accountName,
		},
	})
	if err != nil {
		return nil, err
	}
	if backupVault.Account.Name != accountName {
		return nil, customerrors.NewNotFoundErr("backup vault", &backupVaultUUID)
	}

	// Retrieve the backup using the backupVaultID and externalUUID
	var backup *datamodel.Backup
	backup, err = getBackupWithDetails(db, &datamodel.Backup{
		ExternalUUID:  externalUUID,
		BackupVaultID: backupVault.ID,
	})
	if err != nil {
		return nil, err
	}

	return backup, nil
}

func (d *DataStoreRepository) IsBackupInCreatingorDeletingStateByVolume(ctx context.Context, volumeUUID string) (bool, error) {
	return isBackupInCreatingorDeletingStateByVolume(d.db.GORM().WithContext(ctx), volumeUUID)
}

func isBackupInCreatingorDeletingStateByVolume(db *gorm.DB, volumeUUID string) (bool, error) {
	var backups int64
	err := db.Model(&datamodel.Backup{}).Where("volume_uuid = ?", volumeUUID).Where("state = ? OR state = ?", datamodel.LifeCycleStateCreating, datamodel.LifeCycleStateDeleting).Count(&backups).Error

	if err != nil && err == gorm.ErrRecordNotFound {
		return false, nil
	}
	if backups > 0 {
		return true, nil
	}
	return false, err
}

func (d *DataStoreRepository) AreBackupsInProgressForVolume(ctx context.Context, volumeUUID string, excludeBackupUUIDs []string, createdBefore *time.Time) (bool, error) {
	return areBackupsInProgressForVolume(d.db.GORM().WithContext(ctx), volumeUUID, excludeBackupUUIDs, createdBefore)
}

// GetEarliestCreatingBackupTime returns created_at of the oldest CREATING backup for the volume, or nil if none exist.
func (d *DataStoreRepository) GetEarliestCreatingBackupTime(ctx context.Context, volumeUUID string) (*time.Time, error) {
	db := d.db.GORM().WithContext(ctx)
	var backup datamodel.Backup
	err := db.Where("volume_uuid = ? AND state = ?", volumeUUID, datamodel.LifeCycleStateCreating).
		Order("created_at ASC").
		First(&backup).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	t := backup.CreatedAt
	return &t, nil
}

func areBackupsInProgressForVolume(db *gorm.DB, volumeUUID string, excludeBackupUUIDs []string, createdBefore *time.Time) (bool, error) {
	var backups int64
	query := db.Model(&datamodel.Backup{}).Where("volume_uuid = ?", volumeUUID).Where("state = ? OR state = ?", datamodel.LifeCycleStateCreating, datamodel.LifeCycleStateDeleting)

	if len(excludeBackupUUIDs) > 0 {
		query = query.Where("uuid NOT IN ?", excludeBackupUUIDs)
	}

	if createdBefore != nil {
		query = query.Where("created_at < ?", *createdBefore)
	}

	err := query.Count(&backups).Error

	if err != nil && errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if backups > 0 {
		return true, nil
	}
	return false, err
}

func (d *DataStoreRepository) DeleteBackup(ctx context.Context, backupUUID string) (*datamodel.Backup, error) {
	return deleteBackup(ctx, d.db.GORM().WithContext(ctx), backupUUID)
}

func _deleteBackup(ctx context.Context, db *gorm.DB, backupUUID string) (*datamodel.Backup, error) {
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)
	backup, err := getBackupWithDetails(tx, &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: backupUUID}})
	if err != nil {
		return nil, err
	}

	// Check if this is the last backup for the volume before deleting
	var remainingBackupCount int64
	if backup.VolumeUUID != "" {
		err = tx.Model(&datamodel.Backup{}).
			Where("volume_uuid = ? AND uuid != ? AND deleted_at IS NULL", backup.VolumeUUID, backupUUID).
			Count(&remainingBackupCount).Error
		if err != nil {
			return nil, err
		}
	}

	backup.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	backup.State = datamodel.LifeCycleStateDeleted
	backup.StateDetails = ""
	err = tx.Save(backup).Error
	if err != nil {
		return nil, err
	}

	// History cleanup:
	//  • Volume-wide: when no backups remain at all, soft-delete every active history row
	//    (all endpoints) so billing stops entirely.
	//  • Endpoint-scoped: when other backups still exist but this backup belongs to a GCBDR
	//    endpoint chain, check whether any backup remains for that specific endpoint.  If
	//    none do, soft-delete only that endpoint's history row so the other chains keep
	//    billing correctly.
	if remainingBackupCount == 0 {
		err = markPreviousBackupChainHistoryAsDeleted(ctx, tx, backup.VolumeUUID, "", backup.DeletedAt.Time)
		if err != nil {
			util.GetLogger(ctx).Warnf("Ledger: Failed to mark backup chain history as deleted for volume %s: %v", backup.VolumeUUID, err)
			// Don't fail the entire backup deletion if history update fails
		}
	} else if backup.BackupVault != nil && backup.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject && backup.Attributes != nil {
		endpointUUID := backup.Attributes.EndpointUUID
		var endpointBackupCount int64
		countErr := tx.Model(&datamodel.Backup{}).
			Where("volume_uuid = ? AND uuid != ? AND deleted_at IS NULL AND attributes->>'endpoint_uuid' = ?",
				backup.VolumeUUID, backupUUID, endpointUUID).
			Count(&endpointBackupCount).Error
		if countErr != nil {
			util.GetLogger(ctx).Warnf("Failed to count remaining backups for volume %s endpoint %s: %v", backup.VolumeUUID, endpointUUID, countErr)
		} else if endpointBackupCount == 0 {
			err = markPreviousBackupChainHistoryAsDeleted(ctx, tx, backup.VolumeUUID, endpointUUID, backup.DeletedAt.Time)
			if err != nil {
				util.GetLogger(ctx).Warnf("Failed to mark backup chain history as deleted for volume %s endpoint %s: %v", backup.VolumeUUID, endpointUUID, err)
				// Don't fail the entire backup deletion if history update fails
			}
		}
	}

	return backup, nil
}

func (d *DataStoreRepository) FinishBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)
	dbBackup, err := getBackupWithDetails(tx, &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: backup.UUID}})
	if err != nil {
		return nil, err
	}

	err = tx.Model(&dbBackup).Updates(datamodel.Backup{
		Description:             backup.Description,
		State:                   datamodel.LifeCycleStateAvailable,
		StateDetails:            datamodel.LifeCycleStateAvailableDetails,
		Attributes:              backup.Attributes,
		AssetMetadata:           backup.AssetMetadata,
		SizeInBytes:             backup.SizeInBytes,
		LatestLogicalBackupSize: backup.LatestLogicalBackupSize,
	}).Error
	if err != nil {
		return nil, err
	}

	if dbBackup.VolumeUUID != "" && backup.LatestLogicalBackupSize > 0 {
		endpointUUID := ""
		if backup.Attributes != nil {
			endpointUUID = backup.Attributes.EndpointUUID
		}

		volumeName := ""
		if dbBackup.Attributes != nil && dbBackup.Attributes.VolumeName != "" {
			volumeName = dbBackup.Attributes.VolumeName
		}
		deploymentName := ""
		if dbBackup.BackupVault != nil {
			deploymentName = dbBackup.BackupVault.Name
		}
		consumerID := ""
		if dbBackup.BackupVault != nil && dbBackup.BackupVault.ServiceType == datamodel.ServiceTypeCrossProject {
			consumerID = dbBackup.BackupVault.AccountVendorID
		} else if dbBackup.Attributes != nil {
			consumerID = dbBackup.Attributes.AccountIdentifier
		}

		err = writeBackupChainHistory(ctx, tx, backupChainHistoryUpdateParams{
			VolumeUUID:     dbBackup.VolumeUUID,
			EndpointUUID:   endpointUUID,
			NewSize:        backup.LatestLogicalBackupSize,
			TimeStamp:      time.Now(),
			ResourceName:   volumeName,
			ConsumerID:     consumerID,
			DeploymentName: deploymentName,
			SkipCreate:     shouldSkipBackupChainHistory(ctx, dbBackup, common.LoadConfig()),
		})
		if err != nil {
			util.GetLogger(ctx).Warnf("Ledger: Failed to write backup chain history for backup %s: %v", dbBackup.UUID, err)
			// Don't fail the entire operation if history update fails
		}
	}

	return dbBackup, nil
}

func (d *DataStoreRepository) UpdateBackup(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)
	dbBackup, err := getBackupWithDetails(tx, &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: backup.UUID}})
	if err != nil {
		return nil, err
	}

	// Prepare update fields
	updateFields := datamodel.Backup{
		Description: backup.Description,
	}

	updateFields.State = datamodel.LifeCycleStateAvailable
	updateFields.StateDetails = datamodel.LifeCycleStateAvailableDetails

	err = tx.Model(&dbBackup).Updates(updateFields).Error
	if err != nil {
		return nil, err
	}

	return dbBackup, nil
}

func (d *DataStoreRepository) UpdateBackupFields(ctx context.Context, backupUUID string, updates map[string]interface{}) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	dbBackup, err := getBackupWithDetails(tx, &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: backupUUID}})
	if err != nil {
		return err
	}

	updates["updated_at"] = time.Now()

	err = tx.Model(&dbBackup).Updates(updates).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return nil
}

func (d *DataStoreRepository) UpdateBackupState(ctx context.Context, backup *datamodel.Backup) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)
	dbBackup, err := getBackupWithDetails(tx, &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: backup.UUID}})
	if err != nil {
		return nil, err
	}

	err = tx.Model(&dbBackup).Updates(datamodel.Backup{
		State:        backup.State,
		StateDetails: backup.StateDetails,
		Attributes:   backup.Attributes,
	}).Error
	if err != nil {
		return nil, err
	}
	return dbBackup, nil
}

func (d *DataStoreRepository) IsLatestBackup(ctx context.Context, backupUUID, volumeUUID string) (bool, error) {
	db := d.db.GORM().WithContext(ctx)
	backup := &datamodel.Backup{}
	// get backup by created_at timestamp under a volume
	err := db.Where("volume_uuid = ? and (state = ? or (state = ? and attributes->>'delete_initiated' = 'true'))", volumeUUID, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateError).Order("created_at desc").First(&backup).Error
	if err != nil {
		return false, err
	}
	// check if the backup is latest
	if backup.UUID == backupUUID {
		return true, nil
	}
	return false, nil
}

// IsLatestBackupAnyState checks if a backup is the latest for its volume regardless of state
func (d *DataStoreRepository) IsLatestBackupAnyState(ctx context.Context, backupUUID, volumeUUID string) (bool, error) {
	db := d.db.GORM().WithContext(ctx)
	backup := &datamodel.Backup{}
	// get backup by id under a volume (any state)
	err := db.Where("volume_uuid = ?", volumeUUID).Last(&backup).Error
	if err != nil {
		return false, err
	}
	// check if the backup is latest
	if backup.UUID == backupUUID {
		return true, nil
	}
	return false, nil
}

// IsLatestBackupInVault IsLatestBackupAnyStateInVault checks if a backup is the latest for its volume in the given vault regardless of state
func (d *DataStoreRepository) IsLatestBackupInVault(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64) (bool, error) {
	db := d.db.GORM().WithContext(ctx)
	backup := &datamodel.Backup{}
	err := db.Where("volume_uuid = ? AND backup_vault_id = ?", volumeUUID, backupVaultID).Last(&backup).Error
	if err != nil {
		return false, err
	}
	return backup.UUID == backupUUID, nil
}

// IsLatestBackupInVaultAndInEndpoint checks if a backup is the latest for its volume scoped to the
// given backup vault and object-store endpoint (attributes.endpoint_uuid). Mirrors the available/error-with-delete-initiated
// filter used by IsLatestBackup. If endpointUUID is empty (after trim), matches rows whose endpoint is
// missing or blank in JSON (legacy / unset).
func (d *DataStoreRepository) IsLatestBackupInVaultAndInEndpoint(ctx context.Context, backupUUID, volumeUUID string, backupVaultID int64, endpointUUID string) (bool, error) {
	db := d.db.GORM().WithContext(ctx)
	backup := &datamodel.Backup{}
	endpointUUID = strings.TrimSpace(endpointUUID)
	q := db.Where(
		"volume_uuid = ? AND backup_vault_id = ? AND (state = ? OR state = ? OR (state = ? AND attributes->>'delete_initiated' = 'true'))",
		volumeUUID, backupVaultID, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateError,
	)
	if endpointUUID == "" {
		q = q.Where("TRIM(COALESCE(attributes->>'endpoint_uuid', '')) = ''")
	} else {
		q = q.Where("attributes->>'endpoint_uuid' = ?", endpointUUID)
	}
	err := q.Order("created_at desc").First(&backup).Error
	if err != nil {
		return false, err
	}
	return backup.UUID == backupUUID, nil
}

func (d *DataStoreRepository) BackupCountByVolumeID(ctx context.Context, volumeUUID string) (int64, error) {
	db := d.db.GORM().WithContext(ctx)
	var count int64
	err := db.Model(&datamodel.Backup{}).Where("volume_uuid = ? and state != ?", volumeUUID, datamodel.LifeCycleStateError).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// BackupCountByVolumeIDVaultAndEndpoint returns the count of backups for the given volume scoped to the
// given backup vault and object-store endpoint (attributes.endpoint_uuid). Mirrors the state filter used by
// BackupCountByVolumeID (excludes error state). If endpointUUID is empty (after trim), counts rows whose
// endpoint is missing or blank in JSON (legacy / unset).
func (d *DataStoreRepository) BackupCountByVolumeIDVaultAndEndpoint(ctx context.Context, volumeUUID string, backupVaultID int64, endpointUUID string) (int64, error) {
	db := d.db.GORM().WithContext(ctx)
	var count int64
	endpointUUID = strings.TrimSpace(endpointUUID)
	q := db.Model(&datamodel.Backup{}).
		Where("volume_uuid = ? AND backup_vault_id = ? AND state != ?", volumeUUID, backupVaultID, datamodel.LifeCycleStateError)
	if endpointUUID == "" {
		q = q.Where("TRIM(COALESCE(attributes->>'endpoint_uuid', '')) = ''")
	} else {
		q = q.Where("attributes->>'endpoint_uuid' = ?", endpointUUID)
	}
	err := q.Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) FetchScheduledBackupsForDeletion(ctx context.Context, volume *datamodel.Volume, backupPolicy *datamodel.BackupPolicy, isExpertMode bool) ([]*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	if volume.DataProtection == nil || volume.DataProtection.BackupPolicyID == "" {
		return nil, errors.New("volume does not have a backup policy associated with it")
	}

	var allBackups []*datamodel.Backup
	var volumeUUID string
	if isExpertMode {
		volumeUUID = volume.VolumeAttributes.ExternalUUID
	} else {
		volumeUUID = volume.UUID
	}
	var dailyBackups []*datamodel.Backup
	err = tx.Where("volume_uuid = ?", volumeUUID).
		Where("type = ?", BackupTypeScheduled).
		Where("schedule_tag = ?", Daily).
		Where("state != ?", datamodel.LifeCycleStateCreating).
		Order("id desc").
		Offset(int(backupPolicy.DailyBackupsToKeep)).
		Find(&dailyBackups).Error
	if err != nil {
		return nil, err
	}
	allBackups = append(allBackups, dailyBackups...)

	var weeklyBackups []*datamodel.Backup
	err = tx.Where("volume_uuid = ?", volumeUUID).
		Where("type = ?", BackupTypeScheduled).
		Where("schedule_tag = ?", Weekly).
		Where("state != ?", datamodel.LifeCycleStateCreating).
		Order("id desc").
		Offset(int(backupPolicy.WeeklyBackupsToKeep)).
		Find(&weeklyBackups).Error
	if err != nil {
		return nil, err
	}
	allBackups = append(allBackups, weeklyBackups...)

	var monthlyBackups []*datamodel.Backup
	err = tx.Where("volume_uuid = ?", volumeUUID).
		Where("type = ?", BackupTypeScheduled).
		Where("schedule_tag = ?", Monthly).
		Where("state != ?", datamodel.LifeCycleStateCreating).
		Order("id desc").
		Offset(int(backupPolicy.MonthlyBackupsToKeep)).
		Find(&monthlyBackups).Error
	if err != nil {
		return nil, err
	}
	allBackups = append(allBackups, monthlyBackups...)

	return allBackups, nil
}

func (d *DataStoreRepository) IsBackupShared(ctx context.Context, backup *datamodel.Backup) (bool, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return false, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	// Check if the backup is shared by looking for any other backup with the same snapshot ID
	var count int64
	err = tx.Model(&datamodel.Backup{}).
		Where("attributes->>'snapshot_id' = ? AND uuid != ?", backup.Attributes.SnapshotID, backup.UUID).
		Count(&count).Error
	if err != nil {
		return false, err
	}

	return count > 0, nil
}

func (d *DataStoreRepository) GetBackupCountByVolumeUUIDs(ctx context.Context, volumeUUIDs []string, conditions [][]interface{}) (map[string]int64, error) {
	var results []struct {
		VolumeUUID  string `json:"volume_uuid"`
		BackupCount int64  `json:"backup_count"`
	}
	db := d.db.ApplyFilter(conditions).GORM().WithContext(ctx)
	err := db.Model(&datamodel.Backup{}).
		Select("volume_uuid, count(*) as backup_count").
		Where("volume_uuid IN ?", volumeUUIDs).
		Group("volume_uuid").
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	backupsCountByVolume := make(map[string]int64)
	for _, result := range results {
		backupsCountByVolume[result.VolumeUUID] = result.BackupCount
	}
	return backupsCountByVolume, nil
}

// GetBackupCountByVolumeAndVault returns the count of backups for the given volume and backup vault,
// excluding backups in deleted state (and soft-deleted rows).
func (d *DataStoreRepository) GetBackupCountByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (int64, error) {
	var count int64
	db := d.db.GORM().WithContext(ctx)
	err := db.Model(&datamodel.Backup{}).
		Where("volume_uuid = ? AND backup_vault_id = ? AND state != ?", volumeUUID, backupVaultID, datamodel.LifeCycleStateDeleted).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetBackupCountByVolumeVaultAndEndpoint returns the count of backups for the given volume, backup vault,
// and object-store endpoint (attributes.endpoint_uuid), excluding deleted state.
// Used when backup vault switching is enabled so "last backup" teardown (snapmirror, cloud endpoint) is scoped per endpoint.
// If endpointUUID is empty (after trim), counts rows whose endpoint is missing or blank in JSON (legacy / unset).
func (d *DataStoreRepository) GetBackupCountByVolumeVaultAndEndpoint(ctx context.Context, volumeUUID string, backupVaultID int64, endpointUUID string) (int64, error) {
	var count int64
	db := d.db.GORM().WithContext(ctx)
	endpointUUID = strings.TrimSpace(endpointUUID)
	q := db.Model(&datamodel.Backup{}).
		Where("volume_uuid = ? AND backup_vault_id = ? AND state != ?", volumeUUID, backupVaultID, datamodel.LifeCycleStateDeleted)
	if endpointUUID == "" {
		q = q.Where("TRIM(COALESCE(attributes->>'endpoint_uuid', '')) = ''")
	} else {
		q = q.Where("attributes->>'endpoint_uuid' = ?", endpointUUID)
	}
	err := q.Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// GetDistinctBackupVaultIDsByVolumeUUID returns distinct backup_vault_id values for backups
// that belong to the given volume and are in available state (not deleted, not soft-deleted).
func (d *DataStoreRepository) GetDistinctBackupVaultIDsByVolumeUUID(ctx context.Context, volumeUUID string) ([]int64, error) {
	var ids []int64
	db := d.db.GORM().WithContext(ctx)
	err := db.Model(&datamodel.Backup{}).
		Where("volume_uuid = ? AND state = ?", volumeUUID, datamodel.LifeCycleStateAvailable).
		Distinct("backup_vault_id").
		Pluck("backup_vault_id", &ids).Error
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// GetDistinctBackupVaultServiceTypesByVaultIDs returns distinct service_type values for the given
// backup_vault primary keys. Orphan backup_vault_id values (no row in backup_vaults) do not appear
// in the result; callers may compare row count / result emptiness against their vault id list.
func (d *DataStoreRepository) GetDistinctBackupVaultServiceTypesByVaultIDs(ctx context.Context, backupVaultIDs []int64) ([]string, error) {
	ids := make([]int64, 0, len(backupVaultIDs))
	for _, id := range backupVaultIDs {
		if id != 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var serviceTypes []string
	db := d.db.GORM().WithContext(ctx)
	err := db.Model(&datamodel.BackupVault{}).
		Select("DISTINCT service_type").
		Where("id IN ?", ids).
		Pluck("service_type", &serviceTypes).Error
	if err != nil {
		return nil, err
	}
	return serviceTypes, nil
}

func (d *DataStoreRepository) GetBackupsByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	var backups []*datamodel.Backup

	err := db.Preload("BackupVault").Where("volume_uuid = ?", volumeUUID).Find(&backups).Error
	if err != nil {
		return nil, err
	}

	return backups, nil
}

// BatchGetBackupsByUUIDs fetches backups for the given UUID list with a narrow
// BackupVault preload. Only the BackupVault columns consumed by the batch API
// response are selected
func (d *DataStoreRepository) BatchGetBackupsByUUIDs(ctx context.Context, backupUUIDs []string) ([]*datamodel.Backup, error) {
	var backups []*datamodel.Backup
	if len(backupUUIDs) == 0 {
		return backups, nil
	}

	db := d.db.GORM().WithContext(ctx)
	err := db.Preload("BackupVault", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, uuid, source_region_name, backup_region_name, service_type, bucket_details, immutable_attributes")
	}).Where("uuid IN ?", backupUUIDs).Find(&backups).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return backups, nil
}

func (d *DataStoreRepository) UpdateBackupLatestLogicalBackupSizeByVolume(ctx context.Context, volumeUUID, excludeBackupUUID string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	// Update all backups for the volume except the specified one, setting latest_logical_backup_size to 0
	err = tx.Model(&datamodel.Backup{}).
		Where("volume_uuid = ? AND uuid != ?", volumeUUID, excludeBackupUUID).
		Update("latest_logical_backup_size", 0).Error
	if err != nil {
		return err
	}

	return nil
}

// GetLatestBackupByVolumeUUID returns the single latest backup (by id) for the volume across all vaults.
// Used when vault switching is on to set the "latest backup" (across vaults) latest_logical_backup_size to the summed chain bytes.
func (d *DataStoreRepository) GetLatestBackupByVolumeUUID(ctx context.Context, volumeUUID string) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	var b datamodel.Backup
	err := db.Where("volume_uuid = ? AND state = ?", volumeUUID, datamodel.LifeCycleStateAvailable).Last(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// GetLatestBackupByVolumeAndVault returns the latest backup (by id) for the given volume and backup vault with state Available.
// Used when resolving destination endpoint UUID for reattach so the current vault is considered even if not yet in distinct list.
func (d *DataStoreRepository) GetLatestBackupByVolumeAndVault(ctx context.Context, volumeUUID string, backupVaultID int64) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	var b datamodel.Backup
	err := db.Where("volume_uuid = ? AND backup_vault_id = ? AND state = ?", volumeUUID, backupVaultID, datamodel.LifeCycleStateAvailable).Last(&b).Error
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// GetLatestBackupsPerVaultByVolumeUUID returns the latest backup (by id) per backup_vault_id for the given volume.
// Used by sync when vault switching is on to fetch logical size from each vault's endpoint and sum.
func (d *DataStoreRepository) GetLatestBackupsPerVaultByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error) {
	vaultIDs, err := d.GetDistinctBackupVaultIDsByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		return nil, err
	}
	db := d.db.GORM().WithContext(ctx)
	var out []*datamodel.Backup
	for _, vaultID := range vaultIDs {
		var b datamodel.Backup
		err = db.Where("volume_uuid = ? AND backup_vault_id = ? AND state = ?", volumeUUID, vaultID, datamodel.LifeCycleStateAvailable).Last(&b).Error
		if err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, nil
}

// GetLatestBackupsPerEndpointByVolumeUUID returns the latest AVAILABLE backup (by id) per distinct
// (backup_vault_id, attributes.endpoint_uuid) for the volume. Rows with blank endpoint_uuid are omitted.
func (d *DataStoreRepository) GetLatestBackupsPerEndpointByVolumeUUID(ctx context.Context, volumeUUID string) ([]*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)

	sub := db.Table("backups").
		Select("MAX(id) AS id").
		Where("volume_uuid = ? AND state = ?", volumeUUID, datamodel.LifeCycleStateAvailable).
		Where("COALESCE(attributes->>'endpoint_uuid', '') != ''").
		Group("backup_vault_id, attributes->>'endpoint_uuid'")

	var backups []*datamodel.Backup
	err := db.Model(&datamodel.Backup{}).
		Where("id IN (?)", sub).
		Order("id ASC").
		Find(&backups).Error
	if err != nil {
		return nil, err
	}
	return backups, nil
}

// SumVolumeBackupChainBytes returns the sum of latest_logical_backup_size across latest backups per
// (backup_vault_id, endpoint_uuid) for the volume.
func (d *DataStoreRepository) SumVolumeBackupChainBytes(ctx context.Context, volumeUUID string) (int64, error) {
	backups, err := d.GetLatestBackupsPerEndpointByVolumeUUID(ctx, volumeUUID)
	if err != nil {
		return 0, err
	}
	var sum int64
	for _, b := range backups {
		if b != nil {
			sum += b.LatestLogicalBackupSize
		}
	}
	return sum, nil
}

func (d *DataStoreRepository) GetMostRecentlyDeletedBackupForEndpoint(ctx context.Context, volumeUUID, endpointUUID string, since time.Time) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	var b datamodel.Backup
	err := db.Unscoped().
		Where("volume_uuid = ?", volumeUUID).
		Where("COALESCE(attributes->>'endpoint_uuid', '') = ?", endpointUUID).
		Where("deleted_at IS NOT NULL").
		Where("deleted_at >= ?", since).
		Order("deleted_at DESC").
		First(&b).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

func (d *DataStoreRepository) DecrementIntermediateBackupChainSizes(ctx context.Context, volumeUUID, endpointUUID string, pivotID, excludeLatestID, delta int64) (updated int64, err error) {
	if delta <= 0 {
		return 0, nil
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return 0, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	logger := util.GetLogger(ctx)

	var backups []*datamodel.Backup
	err = tx.Model(&datamodel.Backup{}).
		Where("volume_uuid = ?", volumeUUID).
		Where("COALESCE(attributes->>'endpoint_uuid', '') = ?", endpointUUID).
		Where("state = ?", datamodel.LifeCycleStateAvailable).
		Where("id > ?", pivotID).
		Where("id != ?", excludeLatestID).
		Find(&backups).Error
	if err != nil {
		return 0, err
	}

	for _, b := range backups {
		newSize := b.LatestLogicalBackupSize - delta
		if newSize < 0 {
			newSize = 0
		}
		if newSize == b.LatestLogicalBackupSize {
			continue
		}
		oldSize := b.LatestLogicalBackupSize
		if err = tx.Model(&datamodel.Backup{}).
			Where("id = ?", b.ID).
			Update("latest_logical_backup_size", newSize).Error; err != nil {
			return 0, err
		}
		logger.Infof("GCBDR back-propagation: backup %s (id %d) latest_logical_backup_size %d -> %d (delta %d) volume %s endpoint %s",
			b.UUID, b.ID, oldSize, newSize, delta, volumeUUID, endpointUUID)
		updated++
	}

	return updated, err
}

// GetBackupMetrics retrieves backup logical size metrics grouped by volume UUID with pagination
// Returns the latest backup entry for each volume with state 'available'
func (d *DataStoreRepository) GetBackupMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error) {
	db := d.db.ApplyFilter(conditions).GORM().WithContext(ctx)
	var results []*datamodel.Backup

	// Query to get the latest backup for each volume with state 'available'
	// Use Find instead of Scan to ensure Preload works correctly
	err := db.Preload("BackupVault", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, uuid, name, account_id, backup_vault_type, cmek_attributes, backup_region_name, service_type")
	}).
		Where("state = ?", datamodel.LifeCycleStateAvailable).
		Where("id IN (?)", db.Table("backups").
			Select("MAX(id)").
			Where("state = ?", datamodel.LifeCycleStateAvailable).
			Group("volume_uuid")).
		Scopes(dbutils.Paginate(pagination)).
		Find(&results).Error

	if err != nil {
		return nil, err
	}

	return results, nil
}

// GetBackupChainMetrics retrieves backup logical size metrics per backup chain with pagination.
// Returns the latest backup entry per (volume_uuid, backup_vault_id, endpoint_uuid) chain with
// state 'available'. For volumes with a single chain this matches GetBackupMetrics. For GCBDR
// volumes with multiple chains (vault switching) it returns one row per chain so the collector
// can sum LatestLogicalBackupSize across chains for correct per-(volume, vault) billing totals.
//
// This method is only used by telemetry collectors when EnableGcbdrBackupBilling is true.
// Other callers (e.g. customer adoption) continue to use GetBackupMetrics.
func (d *DataStoreRepository) GetBackupChainMetrics(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Backup, error) {
	db := d.db.ApplyFilter(conditions).GORM().WithContext(ctx)
	var results []*datamodel.Backup

	err := db.Preload("BackupVault", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, uuid, name, account_id, backup_vault_type, cmek_attributes, backup_region_name, service_type")
	}).Preload("BackupVault.Account").
		Where("state = ?", datamodel.LifeCycleStateAvailable).
		Where("id IN (?)", db.Table("backups").
			Select("MAX(id)").
			Where("state = ?", datamodel.LifeCycleStateAvailable).
			Group("volume_uuid, backup_vault_id, attributes->>'endpoint_uuid'")).
		Scopes(dbutils.Paginate(pagination)).
		Find(&results).Error

	if err != nil {
		return nil, err
	}

	return results, nil
}

// VolumeVaultPair holds the minimal (VolumeUUID, VaultUUID) pair returned by GetDistinctVolumeGCBDRVaultPairs.
type VolumeVaultPair struct {
	VolumeUUID string
	VaultUUID  string
}

// GetDistinctVolumeGCBDRVaultPairs returns one row per distinct (volume_uuid, vault_uuid)
// combination that has at least one available backup in a CrossProject vault.
func (d *DataStoreRepository) GetDistinctVolumeGCBDRVaultPairs(ctx context.Context) ([]VolumeVaultPair, error) {
	var results []VolumeVaultPair
	err := d.db.GORM().WithContext(ctx).
		Table("backups").
		Select("DISTINCT backups.volume_uuid, backup_vaults.uuid AS vault_uuid").
		Joins("JOIN backup_vaults ON backup_vaults.id = backups.backup_vault_id").
		Where("backups.state = ? AND backup_vaults.service_type = ?", datamodel.LifeCycleStateAvailable, datamodel.ServiceTypeCrossProject).
		Scan(&results).Error
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (d *DataStoreRepository) GetBackupResourceDataForAggregation(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination, freeTrialAccounts map[int64]*time.Time) ([]*datamodel.Backup, error) {
	db := d.db.Unscoped().ApplyFilter(conditions).GORM().WithContext(ctx)

	var metricsData []*BackupMetricsData

	// One row per (volume, vault): a volume may hold backups in several vaults after a GCBDR
	// vault switch or detach/re-attach, and the collector emits a billing sample per vault.
	subquery := db.Table("backups").
		Select("MAX(id)").
		Group("volume_uuid, backup_vault_id")

	// Query backups table with JOIN to backup_vaults, the vault's owning account, and volumes
	query := db.Table("backups").
		Select(`
			backups.uuid,
			backups.volume_uuid,
			backups.attributes,
			backups.backup_vault_id,
			backup_vaults.account_id AS vault_account_id,
			COALESCE(vault_accounts.name, '') AS vault_account_name,
			backup_vaults.name AS vault_name,
			backup_vaults.backup_region_name AS vault_backup_region_name,
			backup_vaults.service_type AS vault_service_type,
			volumes.account_id AS volume_account_id
		`).
		Joins("LEFT JOIN backup_vaults ON backups.backup_vault_id = backup_vaults.id").
		Joins("LEFT JOIN accounts vault_accounts ON vault_accounts.id = backup_vaults.account_id").
		Joins("LEFT JOIN volumes ON backups.volume_uuid = volumes.uuid").
		Where("backups.id IN (?)", subquery)

	// Apply pagination
	if pagination != nil {
		if pagination.Limit > 0 {
			query = query.Limit(pagination.Limit)
		}
		if pagination.Offset > 0 {
			query = query.Offset(pagination.Offset)
		}
	}

	err := query.Find(&metricsData).Error
	if err != nil {
		return nil, err
	}

	// Convert BackupMetricsData to datamodel.Backup for backward compatibility
	results := make([]*datamodel.Backup, len(metricsData))
	for i, data := range metricsData {
		var vaultAccount *datamodel.Account
		if data.VaultAccountID != 0 || data.VolumeAccountID != 0 {
			vaultAccount = &datamodel.Account{
				BaseModel:       datamodel.BaseModel{ID: data.VaultAccountID},
				Name:            data.VaultAccountName,
				AccountMetadata: &datamodel.AccountMetadata{},
			}
			// Free trial belongs to the volume owner. Callers only apply it to
			// non cross-project vaults, where vault owner and volume owner are the same account.
			if end, ok := freeTrialAccounts[data.VolumeAccountID]; ok {
				vaultAccount.AccountMetadata.TrialMode = &datamodel.AccountTrialMode{EndTime: end}
			}
		}
		results[i] = &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID: data.UUID,
			},
			VolumeUUID:    data.VolumeUUID,
			Attributes:    data.Attributes,
			BackupVaultID: data.BackupVaultID,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					ID: data.BackupVaultID,
				},
				Name:             data.VaultName,
				AccountID:        data.VaultAccountID,
				BackupRegionName: data.VaultBackupRegionName,
				ServiceType:      data.VaultServiceType,
				Account:          vaultAccount,
			},
		}
	}

	return results, nil
}

// GetBackupMetadata retrieves backup metadata entries with pagination and conditions
func (d *DataStoreRepository) GetBackupMetadata(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupMetadata, error) {
	db := d.db.ApplyFilter(conditions).GORM().WithContext(ctx)
	var results []*datamodel.BackupMetadata

	err := db.Unscoped().Scopes(dbutils.Paginate(pagination)).Find(&results).Error
	if err != nil {
		return nil, err
	}

	return results, nil
}

// UpdateLatestBackupLogicalSize updates the latest backup's logical size for a given volume and
// updates the backup chain history. endpointUUID scopes the history update to a specific
// endpoint's row for GCBDR vaults; pass "" for non-GCBDR (legacy/ADC) paths.
func (d *DataStoreRepository) UpdateLatestBackupLogicalSize(ctx context.Context, volumeUUID string, endpointUUID string, newLogicalSize int64) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	// Find the latest backup for the volume (by id)
	var latestBackup datamodel.Backup
	err = tx.Where("volume_uuid = ? AND state = ?", volumeUUID, datamodel.LifeCycleStateAvailable).Last(&latestBackup).Error
	if err != nil {
		return err
	}

	// Update the latest backup's logical size
	err = tx.Model(&latestBackup).
		Update("latest_logical_backup_size", newLogicalSize).Error
	if err != nil {
		return err
	}

	err = writeBackupChainHistory(ctx, tx, backupChainHistoryUpdateParams{
		VolumeUUID:   volumeUUID,
		EndpointUUID: endpointUUID,
		NewSize:      newLogicalSize,
		TimeStamp:    time.Now(),
	})
	if err != nil {
		util.GetLogger(ctx).Warnf("Ledger: Failed to update backup chain history for volume %s endpoint %s: %v", volumeUUID, endpointUUID, err)
		// Don't fail the entire operation if history update fails
	}

	return nil
}

func (d *DataStoreRepository) GetVolumeLatestBackupMap(ctx context.Context) (map[int64]*datamodel.VolumeLatestBackup, error) {
	logger := util.GetLogger(ctx)
	logger.Infof("Getting volume node latest backup map")

	// Step 1: Get latest backups grouped by volume_uuid
	latestBackups, err := d.GetLatestBackupsGroupedByVolumeUUID(ctx)
	if err != nil {
		logger.Errorf("Failed to get latest backups grouped by volume UUID: %v", err)
		return nil, err
	}
	logger.Infof("Retrieved %d latest backups", len(latestBackups))

	if len(latestBackups) == 0 {
		logger.Info("No latest backups found, returning empty map")
		return make(map[int64]*datamodel.VolumeLatestBackup), nil
	}

	// Partition latest backups into regular and expert mode sets.
	regularBackupMap := make(map[string]*datamodel.Backup)
	expertModeBackupMap := make(map[string]*datamodel.Backup)
	for i := range latestBackups {
		if latestBackups[i].Attributes != nil && latestBackups[i].Attributes.IsExpertModeBackup {
			expertModeBackupMap[latestBackups[i].VolumeUUID] = &latestBackups[i]
		} else {
			regularBackupMap[latestBackups[i].VolumeUUID] = &latestBackups[i]
		}
	}
	logger.Infof("Partitioned backups: %d regular, %d expert mode", len(regularBackupMap), len(expertModeBackupMap))

	resultMap := make(map[int64]*datamodel.VolumeLatestBackup)

	// Step 2a: Fetch regular volumes by uuid.
	if len(regularBackupMap) > 0 {
		regularVolumeUUIDs := make([]string, 0, len(regularBackupMap))
		for uuid := range regularBackupMap {
			regularVolumeUUIDs = append(regularVolumeUUIDs, uuid)
		}
		regularConditions := [][]interface{}{
			{"uuid in ?", regularVolumeUUIDs},
			{"state = ?", datamodel.LifeCycleStateREADY},
		}
		logger.Info("Fetching regular volumes with pool preloaded")
		volumes, err2 := d.GetMultipleVolumes(ctx, regularConditions)
		if err2 != nil {
			logger.Errorf("Failed to get regular volumes: %v", err2)
			return nil, err2
		}
		logger.Infof("Retrieved %d regular volumes", len(volumes))
		for i := range volumes {
			volumeUUID := volumes[i].UUID
			if backup, exists := regularBackupMap[volumeUUID]; exists {
				resultMap[volumes[i].ID] = &datamodel.VolumeLatestBackup{
					Volume:       volumes[i],
					LatestBackup: backup,
				}
				logger.Infof("Mapped regular volume %s (ID: %d) with its latest backup %s", volumeUUID, volumes[i].ID, backup.UUID)
			}
		}
	}

	// Step 2b: Fetch expert mode volumes by external_uuid (= backup.VolumeUUID for expert mode backups).
	if len(expertModeBackupMap) > 0 {
		expertModeUUIDs := make([]string, 0, len(expertModeBackupMap))
		for uuid := range expertModeBackupMap {
			expertModeUUIDs = append(expertModeUUIDs, uuid)
		}
		expertModeConditions := [][]interface{}{
			{"external_uuid in ?", expertModeUUIDs},
			{"state = ?", datamodel.LifeCycleStateREADY},
		}
		logger.Info("Fetching expert mode volumes with pool preloaded")
		expertModeVolumes, err3 := d.GetMultipleVolumesWithExpertMode(ctx, expertModeConditions)
		if err3 != nil {
			logger.Errorf("Failed to get expert mode volumes: %v", err3)
			return nil, err3
		}
		logger.Infof("Retrieved %d expert mode volumes", len(expertModeVolumes))
		for i := range expertModeVolumes {
			externalUUID := expertModeVolumes[i].ExternalUUID
			if backup, exists := expertModeBackupMap[externalUUID]; exists {
				resultMap[-expertModeVolumes[i].ID] = &datamodel.VolumeLatestBackup{
					ExpertModeVolume: expertModeVolumes[i],
					LatestBackup:     backup,
				}
				logger.Infof("Mapped expert mode volume %s (ID: %d) with its latest backup %s", externalUUID, expertModeVolumes[i].ID, backup.UUID)
			}
		}
	}

	logger.Infof("Created result map with %d volume-backup pairs", len(resultMap))

	return resultMap, nil
}

// latestBackupScanRow is the private scan target for GetLatestBackupsGroupedByVolumeUUID.
// backup_vaults is already JOINed for the current-vault filter, so selecting
// bv.service_type here costs zero extra DB work — it is just one more column from a
// table already in the query plan.  GORM Raw().Scan() cannot auto-populate nested
// structs, so we capture the column here and manually wire it into BackupVault below.
type latestBackupScanRow struct {
	datamodel.Backup
	VaultServiceType string `gorm:"column:vault_service_type"`
}

// GetLatestBackupsGroupedByVolumeUUID returns the single most-recently-created available
// backup per volume, restricted to the vault that is currently configured for that volume
// (volumes.data_protection->>'backup_vault_id').
//
// The JOIN filters out detached backups that belong to a vault the volume no longer uses
// (e.g. after a GCBDR vault switch where the first backup in the new vault is still
// creating).  When no available backup exists in the current vault the volume is simply
// absent from the result set — it will be skipped by the periodic sync and picked up again
// on the next cycle once the first available backup lands in the new vault.
//
// Each returned Backup has BackupVault.ServiceType populated so callers can gate
// GCBDR-specific logic (e.g. endpoint scoping) without any extra DB round-trip.
func (d *DataStoreRepository) GetLatestBackupsGroupedByVolumeUUID(ctx context.Context) ([]datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	var rows []latestBackupScanRow

	err := db.Raw(`
		SELECT id, uuid, name, attributes, volume_uuid, state,
		       size_in_bytes, latest_logical_backup_size, backup_vault_id,
		       vault_service_type
		FROM (
			SELECT b.id, b.uuid, b.name, b.attributes, b.volume_uuid, b.state,
			       b.size_in_bytes, b.latest_logical_backup_size, b.backup_vault_id,
			       bv.service_type AS vault_service_type,
			       ROW_NUMBER() OVER (PARTITION BY b.volume_uuid ORDER BY b.created_at DESC) AS rn
			FROM backups b
			LEFT JOIN volumes v   ON v.uuid = b.volume_uuid
			                     AND v.deleted_at IS NULL
			JOIN backup_vaults bv ON bv.id = b.backup_vault_id
			WHERE b.deleted_at IS NULL
			  AND b.state = ?
			  AND (v.uuid IS NULL OR v.data_protection->>'backup_vault_id' IS NULL OR bv.uuid = v.data_protection->>'backup_vault_id')
		) ranked
		WHERE rn = 1
	`, datamodel.LifeCycleStateAvailable).Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	latestBackups := make([]datamodel.Backup, 0, len(rows))
	for _, r := range rows {
		b := r.Backup
		if r.VaultServiceType != "" {
			b.BackupVault = &datamodel.BackupVault{ServiceType: r.VaultServiceType}
		}
		latestBackups = append(latestBackups, b)
	}
	return latestBackups, nil
}

func (d *DataStoreRepository) UpdateBackupConstituentCountFromVolume(ctx context.Context, backup *datamodel.Backup, volume *datamodel.Volume) (*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)
	dbBackup, err := getBackupWithDetails(tx, &datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: backup.UUID}})
	if err != nil {
		return nil, err
	}

	lvCount := int32(0)
	if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeVolumeConstituentCount != nil {
		lvCount = *volume.LargeVolumeAttributes.LargeVolumeConstituentCount
	}

	backup.Attributes.ConstituentCountOfBackup = lvCount
	backup.Attributes.OntapVolumeStyle = OntapFgVolumeStyle

	// Prepare update fields
	updateFields := datamodel.Backup{
		Description: backup.Description,
		Attributes:  backup.Attributes,
	}

	err = tx.Model(&dbBackup).Updates(updateFields).Error
	if err != nil {
		return nil, err
	}

	return dbBackup, nil
}

// CreateBackupMetadata creates a new BackupMetadata entry in the database
func (d *DataStoreRepository) CreateBackupMetadata(ctx context.Context, backupMetadata *datamodel.BackupMetadata) (*datamodel.BackupMetadata, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	// Check if BackupMetadata already exists for this volume
	var existingBackupMetadata datamodel.BackupMetadata
	err = tx.Where("volume_uuid = ?", backupMetadata.VolumeUUID).First(&existingBackupMetadata).Error
	if err == nil {
		// BackupMetadata already exists for this volume
		return &existingBackupMetadata, nil
	}
	if !vsaerrors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// Create new BackupMetadata entry
	backupMetadata.UUID = utils.RandomUUID()

	err = tx.Create(backupMetadata).Error
	if err != nil {
		return nil, err
	}

	return backupMetadata, nil
}

// DeleteBackupMetadata deletes a BackupMetadata entry by volume UUID
func (d *DataStoreRepository) DeleteBackupMetadata(ctx context.Context, volumeUUID string) error {
	db := d.db.GORM().WithContext(ctx)

	// Delete BackupMetadata entry by volume UUID
	err := db.Where("volume_uuid = ?", volumeUUID).Delete(&datamodel.BackupMetadata{}).Error
	if err != nil {
		return err
	}

	return nil
}

// GetBackupMetadataByVolumeUUID gets a BackupMetadata entry by volume UUID
func (d *DataStoreRepository) GetBackupMetadataByVolumeUUID(ctx context.Context, volumeUUID string) (*datamodel.BackupMetadata, error) {
	db := d.db.GORM().WithContext(ctx)
	var backupMetadata datamodel.BackupMetadata

	err := db.Where("volume_uuid = ?", volumeUUID).First(&backupMetadata).Error
	if err != nil {
		return nil, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "backup metadata", &volumeUUID)
	}

	return &backupMetadata, nil
}

// UpdateBackupMetadata updates a BackupMetadata entry
func (d *DataStoreRepository) UpdateBackupMetadata(ctx context.Context, backupMetadata *datamodel.BackupMetadata) (*datamodel.BackupMetadata, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	// First check if the record exists
	var existingBackupMetadata datamodel.BackupMetadata
	err = tx.Where("uuid = ?", backupMetadata.UUID).First(&existingBackupMetadata).Error
	if err != nil {
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("backup metadata", &backupMetadata.UUID)
		}
		return nil, err
	}

	// Update the existing record
	err = tx.Model(&existingBackupMetadata).Updates(backupMetadata).Error
	if err != nil {
		return nil, err
	}

	// Return the updated record
	return &existingBackupMetadata, nil
}

// CreateSfrMetadata creates a new SfrMetadata entry in the database
func (d *DataStoreRepository) CreateSfrMetadata(ctx context.Context, sfrMetadata *datamodel.SfrMetadata) (*datamodel.SfrMetadata, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	err = tx.Create(sfrMetadata).Error
	if err != nil {
		return nil, err
	}

	return sfrMetadata, nil
}

// GetSfrMetricsByTimeRange fetches SFR metadata records between startTime and endTime,
// aggregates them by volume UUID, and returns a map of volume UUID to aggregated metrics
func (d *DataStoreRepository) GetSfrMetricsByTimeRange(ctx context.Context, startTime, endTime time.Time) (map[string]datamodel.SfrMetricsAggregate, error) {
	db := d.db.GORM().WithContext(ctx)

	var results []struct {
		VolumeUUID string `gorm:"column:volume_uuid"`
		TotalSize  int64  `gorm:"column:total_size"`
		TotalCount int64  `gorm:"column:total_count"`
	}

	err := db.Model(&datamodel.SfrMetadata{}).
		Select("volume_uuid, SUM(files_size) as total_size, SUM(file_count) as total_count").
		Where("created_at >= ? AND created_at <= ?", startTime, endTime).
		Group("volume_uuid").
		Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// Convert results to map
	sfrMetricsMap := make(map[string]datamodel.SfrMetricsAggregate)
	for _, result := range results {
		sfrMetricsMap[result.VolumeUUID] = datamodel.SfrMetricsAggregate{
			TotalSize:  result.TotalSize,
			TotalCount: result.TotalCount,
		}
	}

	return sfrMetricsMap, nil
}

func (d *DataStoreRepository) GetSfrMetadataByJobID(ctx context.Context, jobID int64) (*datamodel.SfrMetadata, error) {
	db := d.db.GORM().WithContext(ctx)
	var sfrMetadata datamodel.SfrMetadata
	err := db.Where("job_id = ?", jobID).First(&sfrMetadata).Error
	if err != nil {
		return nil, err
	}
	return &sfrMetadata, nil
}

func (d *DataStoreRepository) GetBackupWithVaultByUUID(ctx context.Context, backupUUID string) (*datamodel.Backup, error) {
	var backup datamodel.Backup
	err := d.db.GORM().WithContext(ctx).Unscoped().Preload("BackupVault").Where("uuid = ?", backupUUID).First(&backup).Error
	if err != nil {
		return nil, err
	}
	return &backup, nil
}

// UpdateBackupChainHistory updates the backup chain history with a new size for the active
// backup. It marks the current active entry as deleted and creates a new one with the updated
// size. endpointUUID scopes the operation to a single endpoint's row (periodic-sync / GCBDR
// path); pass "" for the legacy path where no endpoint scoping is required.
// When no matching active row exists, a new row is created (metadata inherited from a tombstoned
// row when applicable).
func (d *DataStoreRepository) UpdateBackupChainHistory(ctx context.Context, volumeUUID string, endpointUUID string, newSize int64) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	err = writeBackupChainHistory(ctx, tx, backupChainHistoryUpdateParams{
		VolumeUUID:   volumeUUID,
		EndpointUUID: endpointUUID,
		NewSize:      newSize,
		TimeStamp:    time.Now(),
	})
	if err != nil {
		return err
	}

	return nil
}

// DeleteBackupChainHistoryOlderThan removes backup chain history records that have been soft deleted and are older than the specified time
// Uses batch deletion to avoid long-running transactions and lock contention
func (d *DataStoreRepository) DeleteBackupChainHistoryOlderThan(ctx context.Context, olderThan time.Time) (int64, error) {
	db := d.db.GORM().WithContext(ctx)
	logger := util.GetLogger(ctx)

	batchSize := common.LoadConfig().PageSize
	var totalDeleted int64

	// Batch delete in chunks to avoid long-running transactions and lock contention
	for {
		result := db.Unscoped().Where("deleted_at IS NOT NULL AND deleted_at < ?", olderThan).Limit(int(batchSize)).Delete(&datamodel.BackupChainHistory{})
		if result.Error != nil {
			logger.Warnf("Ledger: Failed to delete backup chain history older than %s (deleted %d rows so far): %v",
				olderThan.UTC().Format(time.RFC3339), totalDeleted, result.Error)
			return totalDeleted, result.Error
		}
		if result.RowsAffected == 0 {
			break
		}
		totalDeleted += result.RowsAffected
	}

	logger.Infof("Ledger: Successfully deleted %d backup chain history entries older than %s",
		totalDeleted, olderThan.UTC().Format(time.RFC3339))
	return totalDeleted, nil
}

// ListBackupChainHistoriesWithPagination retrieves backup chain history entries with pagination and conditions.
func (d *DataStoreRepository) ListBackupChainHistoriesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.BackupChainHistory, error) {
	// Use Unscoped to include soft-deleted history rows when deleted_at filters are provided.
	db := d.db.ApplyFilter(conditions).Unscoped().GORM().WithContext(ctx).Order("created_at ASC")
	var results []*datamodel.BackupChainHistory

	err := db.Scopes(dbutils.Paginate(pagination)).Find(&results).Error
	if err != nil {
		return nil, err
	}

	return results, nil
}

func (d *DataStoreRepository) GetExpertModeBackupsByVolumeExternalUUID(ctx context.Context, volumeExternalUUID string) ([]*datamodel.Backup, error) {
	db := d.db.GORM().WithContext(ctx)
	var backups []*datamodel.Backup

	err := db.Where("volume_uuid = ? AND state != ?", volumeExternalUUID, datamodel.LifeCycleStateError).
		Order("created_at DESC").
		Find(&backups).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return backups, nil
}
