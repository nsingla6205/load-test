package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	getBackupVaultWithDetails = _getBackupVaultWithDetails
	checkBVExists             = _checkBVExists
)

func (d *DataStoreRepository) DeleteBackupVaultInVCP(ctx context.Context, backupVaultId string) (*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	logger := util.GetLogger(ctx)

	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(logger, tx, &err)

	dbBackupVault, err := getBackupVaultWithDetails(tx, &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: backupVaultId}})
	if err != nil {
		return nil, err
	}

	dbBackupVault.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	dbBackupVault.LifeCycleState = datamodel.LifeCycleStateDeleted
	dbBackupVault.LifeCycleStateDetails = datamodel.LifeCycleStateDeletedDetails
	err = tx.Save(dbBackupVault).Error
	if err != nil {
		return nil, err
	}

	return dbBackupVault, nil
}

func (d *DataStoreRepository) RestoreDeletedBackupVault(ctx context.Context, backupVaultUUID string, accountID int64, state, stateDetails string) (*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	logger := util.GetLogger(ctx)

	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(logger, tx, &err)

	var bv datamodel.BackupVault
	err = tx.Unscoped().Preload("Account").Where("uuid = ? AND account_id = ?", backupVaultUUID, accountID).First(&bv).Error
	if err != nil {
		return nil, err
	}

	bv.DeletedAt = nil
	bv.LifeCycleState = state
	bv.LifeCycleStateDetails = stateDetails
	err = tx.Unscoped().Save(&bv).Error
	if err != nil {
		return nil, err
	}

	return &bv, nil
}

func (d *DataStoreRepository) UpdateBackupVaultInVCP(ctx context.Context, updatedBackupVault *datamodel.BackupVault, existingBackupVault *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}

	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	existingBackupVault, err = getBackupVaultWithDetails(tx, &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: existingBackupVault.UUID}})
	if err != nil {
		return nil, err
	}

	// A nil description means this update did not include the field, so keep the stored value.
	// An empty string is an explicit clear. GORM Updates skips nil, so the column is set to NULL below.
	clearDescription := false
	if updatedBackupVault.Description != nil {
		if *updatedBackupVault.Description == "" {
			clearDescription = true
			existingBackupVault.Description = nil
		} else {
			existingBackupVault.Description = updatedBackupVault.Description
		}
	}
	existingBackupVault.ImmutableAttributes = updatedBackupVault.ImmutableAttributes
	existingBackupVault.CmekAttributes = updatedBackupVault.CmekAttributes
	existingBackupVault.LifeCycleState = updatedBackupVault.LifeCycleState
	existingBackupVault.LifeCycleStateDetails = updatedBackupVault.LifeCycleStateDetails
	if updatedBackupVault.ExternalUUID != nil {
		existingBackupVault.ExternalUUID = updatedBackupVault.ExternalUUID
	}
	existingBackupVault.UpdatedAt = time.Now()

	err = tx.Updates(existingBackupVault).Error
	if err != nil {
		return nil, err
	}
	if clearDescription {
		err = tx.Model(&datamodel.BackupVault{}).
			Where("uuid = ?", existingBackupVault.UUID).
			Update("description", nil).Error
		if err != nil {
			return nil, err
		}
	}

	return existingBackupVault, nil
}

// UpdateBackupVaultFields updates only the given columns on a backup vault, identified by UUID.
// Callers pass a map of column name to new value (e.g. {"external_uuid": uuid}); only those
// columns are touched, avoiding accidental overwrites of unrelated fields.
func (d *DataStoreRepository) UpdateBackupVaultFields(ctx context.Context, backupVaultUUID string, updates map[string]interface{}) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	updates["updated_at"] = time.Now()

	result := tx.Model(&datamodel.BackupVault{}).
		Where("uuid = ?", backupVaultUUID).
		Updates(updates)
	if result.Error != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
		return err
	}
	if result.RowsAffected == 0 {
		err = customerrors.NewNotFoundErr("backup vault", &backupVaultUUID)
		return err
	}

	return nil
}

func _getBackupVaultWithDetails(db *gorm.DB, query *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	bv := &datamodel.BackupVault{}
	err := db.Preload("Account").First(&bv, query).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var identifier *string
			if query != nil {
				switch {
				case query.UUID != "":
					identifier = &query.UUID
				case query.ExternalUUID != nil:
					identifier = query.ExternalUUID
				}
			}
			return nil, customerrors.NewNotFoundErr("backup vault", identifier)
		}
		return nil, err
	}
	return bv, nil
}

func _checkBVExists(tx *gorm.DB, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	bvDetails := &datamodel.BackupVault{}
	err1 := tx.Where("uuid = ? and account_id = ?", bv.UUID, bv.AccountID).First(&bvDetails).Error
	return bvDetails, err1
}

func (d *DataStoreRepository) GetBackupVault(ctx context.Context, backupVaultId string) (*datamodel.BackupVault, error) {
	var bv datamodel.BackupVault
	err := d.db.GORM().WithContext(ctx).Preload("Account").Where("uuid = ?", backupVaultId).First(&bv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("backup vault", &backupVaultId)
		}
		return nil, err
	}
	return &bv, nil
}

func (d *DataStoreRepository) GetBackupVaultById(ctx context.Context, backupVaultId int64) (*datamodel.BackupVault, error) {
	var bv datamodel.BackupVault
	err := d.db.GORM().WithContext(ctx).Preload("Account").Where("id = ?", backupVaultId).First(&bv).Error
	if err != nil {
		return nil, err
	}
	return &bv, nil
}

func (d *DataStoreRepository) GetBackupVaultByNameAndOwnerID(ctx context.Context, backupVaultName, ownerID string) (*datamodel.BackupVault, error) {
	var bv datamodel.BackupVault
	err := d.db.GORM().WithContext(ctx).Preload("Account").Where("name = ?", backupVaultName).Where("account_id = ?", ownerID).First(&bv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("backup vault", &backupVaultName)
		}
		return nil, err
	}
	return &bv, nil
}

func (d *DataStoreRepository) GetBackupVaultByCrossRegionBackupVaultName(ctx context.Context, crossRegionBackupVaultName string, accountID int64) (*datamodel.BackupVault, error) {
	var bv datamodel.BackupVault
	err := d.db.GORM().WithContext(ctx).Preload("Account").Where("cross_region_backup_vault_name = ?", crossRegionBackupVaultName).Where("account_id = ?", accountID).First(&bv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("backup vault", &crossRegionBackupVaultName)
		}
		return nil, err
	}
	return &bv, nil
}

func (d *DataStoreRepository) GetBackupVaultByUUIDndOwnerID(ctx context.Context, backupVaultID string, accountID int64) (*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	dbBackupVault, err := getBackupVaultWithDetails(db, &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: backupVaultID}, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	return dbBackupVault, nil
}

// GetBackupVaultByExternalUUIDAndOwnerID gets a BackupVault by external UUID and owner ID (account ID)
func (d *DataStoreRepository) GetBackupVaultByExternalUUIDAndOwnerID(ctx context.Context, externalUUID string, accountID int64) (*datamodel.BackupVault, error) {
	var bv datamodel.BackupVault
	db := d.db.GORM().WithContext(ctx)
	err := db.Preload("Account").Where("external_uuid = ? AND account_id = ?", externalUUID, accountID).First(&bv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("backup vault", &externalUUID)
		}
		return nil, err
	}
	return &bv, nil
}

func (d *DataStoreRepository) CreateBackupVaultEntryInVCP(ctx context.Context, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}

	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	bvDetails, err1 := checkBVExists(tx, bv)
	if errors.Is(err1, gorm.ErrRecordNotFound) {
		err = tx.Create(bv).Error
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		err := tx.Where("uuid = ? and account_id = ?", bv.UUID, bv.AccountID).Preload("Account").First(&bvDetails).Error
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		return bvDetails, nil
	} else if err1 != nil {
		logger.Errorf("Error while Attaching BackupVault: %v", err1)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err1)
	}

	backupVaultDetails, err := getBackupVaultWithDetails(db, &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: bv.UUID}})
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return backupVaultDetails, nil
}

func (d *DataStoreRepository) UpdateBackupVaultBucketDetails(ctx context.Context, updatedBv *datamodel.BackupVault) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var existingBv datamodel.BackupVault
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("uuid = ?", updatedBv.UUID).
		First(&existingBv).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return customerrors.NewNotFoundErr("backup vault", &updatedBv.UUID)
		}
		return err
	}

	existingBv.UpdatedAt = time.Now()
	existingBv.UpdateBucketDetails(updatedBv.BucketDetails)

	if err = tx.Save(&existingBv).Error; err != nil {
		return err
	}
	return nil
}

func (d *DataStoreRepository) UpdateBackupVaultState(ctx context.Context, bv *datamodel.BackupVault, state, stateDetails string) (*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	bv.LifeCycleState = state
	bv.LifeCycleStateDetails = stateDetails
	err = tx.Save(bv).Error
	if err != nil {
		return nil, err
	}

	dbBackupVault, err := getBackupVaultWithDetails(tx, bv)
	if err != nil {
		return nil, err
	}
	return dbBackupVault, nil
}
func (d *DataStoreRepository) ListBackupVaults(ctx context.Context, accountID int64) ([]*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	var backupVaults []*datamodel.BackupVault
	err := db.Preload("Account").Where("account_id = ?", accountID).Find(&backupVaults).Error
	if err != nil {
		return nil, err
	}
	return backupVaults, nil
}

// CreatingBackupVault creates a new backup vault in the database
func (d *DataStoreRepository) CreatingBackupVault(ctx context.Context, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}

	logger := util.GetLogger(ctx)
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
			panic(r)
		} else if err != nil {
			tx.Rollback()
		} else {
			tx.Commit()
		}
	}()

	var dbBackupVault *datamodel.BackupVault
	err1 := tx.Where("name = ?", bv.Name).Where("account_id = ?", bv.AccountID).First(&dbBackupVault).Error
	if errors.Is(err1, gorm.ErrRecordNotFound) {
		bv.UUID = utils.RandomUUID()
		bv.LifeCycleState = datamodel.LifeCycleStateCreating
		bv.LifeCycleStateDetails = datamodel.LifeCycleStateCreatingDetails
		bv.CreatedAt = time.Now()
		bv.UpdatedAt = bv.CreatedAt
		err = tx.Create(&bv).Error
		if err != nil {
			return nil, err
		}

		dbBackupVault, err = getBackupVaultWithDetails(tx, bv)
		if err != nil {
			return nil, err
		}
		return dbBackupVault, nil
	} else if err1 != nil {
		logger.Errorf("Error while checking if backup vault exists: %v", err1)
		return nil, err1
	}
	return nil, customerrors.NewConflictErr("backup vault already exists")
}

// GetMultipleBackupVaults retrieves multiple BackupVaults based on the provided conditions
func (d *DataStoreRepository) GetMultipleBackupVaults(ctx context.Context, conditions [][]interface{}) ([]*datamodel.BackupVault, error) {
	return getMultipleBackupVaults(d.db.ApplyFilter(conditions).GORM().WithContext(ctx))
}

func getMultipleBackupVaults(db *gorm.DB) ([]*datamodel.BackupVault, error) {
	var backupVaults []*datamodel.BackupVault
	err := db.Preload("Account").Find(&backupVaults).Error
	if err != nil {
		return nil, err
	}
	return backupVaults, nil
}

// GetBackupVaultUUIDsFromBackupPolicyUUID retrieves all backup vault UUIDs associated with a backup policy
func (d *DataStoreRepository) GetBackupVaultUUIDsFromBackupPolicyUUID(ctx context.Context, backupPolicyUUID string, accountID int64) ([]string, error) {
	var backupVaultUUIDs []string
	db := d.db.GORM().WithContext(ctx)
	err := db.Model(&datamodel.Volume{}).
		Distinct("data_protection->>'backup_vault_id'").
		Where("data_protection->>'backup_policy_id' = ?", backupPolicyUUID).
		Where("data_protection->>'backup_vault_id' IS NOT NULL").
		Where("data_protection->>'backup_vault_id' != ''").
		Pluck("data_protection->>'backup_vault_id'", &backupVaultUUIDs).Error

	if err != nil {
		return nil, fmt.Errorf("failed to get backup vault UUIDs from backup policy: %v", err)
	}

	return backupVaultUUIDs, nil
}

// CmekRotationJobStatus represents a CMEK rotation job status from the jobs table
type CmekRotationJobStatus struct {
	ID                int64     `gorm:"column:id"`
	Status            string    `gorm:"column:status"`
	BackupVaultUUID   string    `gorm:"column:backup_vault_uuid"`
	UpdatedAt         time.Time `gorm:"column:updated_at"`
	BackupVaultName   string    `gorm:"column:backup_vault_name"`
	Region            string    `gorm:"column:region"`
	NewKmsKeyURL      string    `gorm:"column:new_kms_key_url"`
	AccountIdentifier string    `gorm:"column:account_identifier"`
}

// GetCmekRotationJobStatuses retrieves CMEK rotation job statuses from the jobs table
// This queries the VCP jobs table for job statuses related to CMEK rotation
//
// Note: This function requires PostgreSQL as it uses PostgreSQL-specific features:
// - JSONB operators (->>) for extracting values from JSONB columns
// - Type casting (::text) for explicit type conversion
// This function is incompatible with SQLite and will fail in SQLite environments.
func (d *DataStoreRepository) GetCmekRotationJobStatuses(ctx context.Context, startTime, endTime time.Time, limit, offset int) ([]*CmekRotationJobStatus, error) {
	db := d.db.GORM().WithContext(ctx)
	var results []*CmekRotationJobStatus

	var selectClause, resourceUUIDFilter string
	if utils.EnableJobResourceUUIDIndex {
		selectClause = "id, state as status, resource_uuid as backup_vault_uuid, updated_at, resource_name as backup_vault_name, (job_attributes->>'location')::text as region, (job_attributes->'kms_attributes'->>'new_kms_key_url')::text as new_kms_key_url, COALESCE((job_attributes->'kms_attributes'->>'account_identifier')::text, '') as account_identifier"
		resourceUUIDFilter = "resource_uuid IS NOT NULL AND resource_uuid != ''"
	} else {
		selectClause = "id, state as status, (job_attributes->>'resource_uuid')::text as backup_vault_uuid, updated_at, resource_name as backup_vault_name, (job_attributes->>'location')::text as region, (job_attributes->'kms_attributes'->>'new_kms_key_url')::text as new_kms_key_url, COALESCE((job_attributes->'kms_attributes'->>'account_identifier')::text, '') as account_identifier"
		resourceUUIDFilter = "(job_attributes->>'resource_uuid') IS NOT NULL"
	}

	err := db.Model(&datamodel.Job{}).
		Select(selectClause).
		Where("type = ? AND updated_at >= ? AND updated_at <= ? AND deleted_at IS NULL", datamodel.JobTypeRotateCmekBackups, startTime, endTime).
		Where(resourceUUIDFilter + " AND resource_name IS NOT NULL AND resource_name != '' AND (job_attributes->>'location') IS NOT NULL AND (job_attributes->'kms_attributes'->>'new_kms_key_url') IS NOT NULL AND (job_attributes->'kms_attributes'->>'account_identifier') IS NOT NULL").
		Order("updated_at").
		Limit(limit).
		Offset(offset).
		Scan(&results).Error
	if err != nil {
		return nil, err
	}

	return results, nil
}
