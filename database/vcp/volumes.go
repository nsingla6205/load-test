package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

var (
	updateVolumeState            = _updateVolumeState
	deleteVolume                 = _deleteVolume
	getMultipleVolumes           = _getMultipleVolumes
	volumesWithHG                = _volumesWithHG
	listVolumesWithDetails       = _listVolumesWithDetails
	listAllVolumesWithDetails    = _listAllVolumesWithDetails
	eligibleVolDetails           = _eligibleVolDetails
	FindVolumeInRegionalPool     = _findVolumeInRegionalPool
	FindVolumeInZonalPool        = _findVolumeInZonalPool
	UpdateVolumeTieringBatchSize = env.GetInt("UPDATE_VOLUME_TIERING_BATCH_SIZE", 20)
)

func (d *DataStoreRepository) CreateVolume(ctx context.Context, volume *datamodel.Volume) (*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err1 := startTransaction(db)
	if err1 != nil {
		return nil, err1
	}
	var err error
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var volErr error
	if volume.Pool.PoolAttributes.IsRegionalHA {
		_, volErr = FindVolumeInRegionalPool(tx, volume.Name, volume.AccountID, false)
	} else {
		_, volErr = FindVolumeInZonalPool(tx, volume.Name, volume.AccountID, volume.Pool.PoolAttributes.PrimaryZone, false)
	}

	if errors.Is(volErr, gorm.ErrRecordNotFound) {
		volume.UUID = utils.RandomUUID()

		if volume.VolumeAttributes != nil && volume.VolumeAttributes.RestoredBackupPath != "" {
			// This is volume restore case
			volume.State = datamodel.LifeCycleStateRestoring
			volume.StateDetails = datamodel.LifeCycleStateRestoringDetails
		} else if volume.State == "" {
			// Normal volume creation case
			volume.State = datamodel.LifeCycleStateCreating
			volume.StateDetails = datamodel.LifeCycleStateCreatingDetails
		}
		volume.CreatedAt = time.Now()
		volume.UpdatedAt = volume.CreatedAt

		err = tx.Create(volume).Error
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		volume, err = getVolumeWithDetails(tx, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volume.UUID}})
		if err != nil {
			return nil, err
		}

		return volume, nil
	} else if volErr != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, volErr)
	}
	// Volume already exists in the same zone
	return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, customerrors.NewUserInputValidationErr("volume with this name already exists in the same zone"))
}

// GetVolume retrieves a volume by its UUID and if the deletedAt field is not set, it returns the volume details.
func (d *DataStoreRepository) GetVolume(ctx context.Context, volUUID string) (*datamodel.Volume, error) {
	return getVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volUUID}})
}

// DescribeVolume retrieves a volume by its UUID and returns the volume details, including deleted volumes.
func (d *DataStoreRepository) DescribeVolume(ctx context.Context, volUUID string) (*datamodel.Volume, error) {
	return getVolumeWithDetails(d.db.Unscoped().GORM().WithContext(ctx), &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volUUID}})
}

func (d *DataStoreRepository) GetVolumeWithAccountID(ctx context.Context, volUUID string, accountID int64) (*datamodel.Volume, error) {
	return getVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volUUID}, AccountID: accountID})
}

// GetVolumeByIDAndAccountID retrieves a volume by its database ID and account ID
func (d *DataStoreRepository) GetVolumeByIDAndAccountID(ctx context.Context, volumeID int64, accountID int64) (*datamodel.Volume, error) {
	return getVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: volumeID}, AccountID: accountID})
}

func (d *DataStoreRepository) GetVolumeByNameAndAccountID(ctx context.Context, name string, accountID int64) (*datamodel.Volume, error) {
	return getVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Volume{Name: name, AccountID: accountID})
}

// GetVolumeByNameAndAccountIDWithPool retrieves a volume by name and account ID with Pool preloaded.
func (d *DataStoreRepository) GetVolumeByNameAndAccountIDWithPool(ctx context.Context, name string, accountID int64) (*datamodel.Volume, error) {
	return getVolumeByNameAndAccountIDWithPool(d.db.GORM().WithContext(ctx), name, accountID)
}

func (d *DataStoreRepository) GetVolumeByName(ctx context.Context, volName string) (*datamodel.Volume, error) {
	return getVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Volume{Name: volName})
}

func (d *DataStoreRepository) UpdateVolume(ctx context.Context, volume *datamodel.Volume) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	dbVolume, err := getVolumeWithDetails(tx, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volume.UUID}})
	if err != nil {
		return err
	}

	// Prepare the fields to update
	updateFields := datamodel.Volume{
		VolumeAttributes: volume.VolumeAttributes,
		State:            volume.State,
		StateDetails:     volume.StateDetails,
	}

	// Update LargeVolumeAttributes only if LargeVolume is true
	if volume.LargeVolumeAttributes != nil && volume.LargeVolumeAttributes.LargeCapacity {
		updateFields.LargeVolumeAttributes = volume.LargeVolumeAttributes
	}

	err = tx.Model(&dbVolume).Updates(updateFields).Error
	if err != nil {
		return err
	}

	return nil
}

func (d *DataStoreRepository) RevertedVolume(ctx context.Context, volume *datamodel.Volume, snapshot *datamodel.Snapshot) ([]*datamodel.Snapshot, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	snapshots, err := revertDeleteSnapshots(ctx, tx, volume.ID, snapshot.UUID)
	if err != nil {
		return nil, err
	}

	volume.State = datamodel.LifeCycleStateREADY
	volume.StateDetails = datamodel.LifeCycleStateAvailableDetails
	err = tx.Unscoped().Save(volume).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return snapshots, nil
}

func revertDeleteSnapshots(ctx context.Context, db *gorm.DB, volumeID int64, snapshotID string) ([]*datamodel.Snapshot, error) {
	db = db.Preload("Account").Preload("Volume").Preload("Volume.Pool")
	logger := util.GetLogger(ctx)

	var snapshots []*datamodel.Snapshot
	err := db.Where(
		"volume_id = ? and created_at > (select created_at from (select created_at from snapshots where uuid = ?) as ss)",
		volumeID, snapshotID,
	).Find(&snapshots).Error
	if err != nil {
		logger.Warnf("failed to revert delete snapshots: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
			customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "snapshot", nil))
	}

	result := db.Exec(
		"UPDATE snapshots SET deleted_at = CURRENT_TIMESTAMP, state = ?, state_details = ? "+
			"WHERE volume_id = ? AND created_at > (SELECT created_at FROM snapshots WHERE uuid = ?)",
		datamodel.LifeCycleStateDeleted, datamodel.LifeCycleStateDeletedDetails,
		volumeID, snapshotID,
	)
	if result.Error != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}

	return snapshots, nil
}

func (d *DataStoreRepository) UpdateVolumeFields(ctx context.Context, volumeUUID string, updates map[string]interface{}) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	dbVolume, err := getVolumeWithDetails(tx, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}})
	if err != nil {
		return err
	}

	updates["updated_at"] = time.Now()

	// Use a fresh model with only the primary key. Passing the fully-loaded dbVolume
	// (which has preloaded associations) causes GORM to save association foreign keys
	// alongside the map values, overwriting map entries like volume_performance_group_id.
	target := &datamodel.Volume{BaseModel: datamodel.BaseModel{ID: dbVolume.ID}}
	result := tx.Model(target).Updates(updates)
	if result.Error != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
		return err
	}
	if result.RowsAffected == 0 {
		logger.Error("UpdateVolumeFields affected 0 rows", "volumeUUID", volumeUUID, "volumeID", dbVolume.ID, "updates", updates)
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, fmt.Errorf("no rows affected when updating volume %s (ID=%d)", volumeUUID, dbVolume.ID))
		return err
	}

	return nil
}

// BatchUpdateVolumeFields efficiently updates specific fields for multiple volumes using PostgreSQL bulk operations
// Currently supports updating: used_bytes
//
// To add new fields in the future:
// 1. Add the field to the buildVolumeUpdateQuery method (placeholders, args, and SET clause)
// 2. Update the paramCounter increment (currently +=2, will be +=3 for 3 fields, etc.)
// 3. Ensure the field exists in VolumeFieldUpdate.Fields map before calling
func (d *DataStoreRepository) BatchUpdateVolumeFields(ctx context.Context, updates []datamodel.VolumeFieldUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// Build parameterized query to prevent SQL injection
	sql, args := d.buildVolumeUpdateQuery(ctx, updates)

	err = tx.Exec(sql, args...).Error
	if err != nil {
		logger.Errorf("Bulk volume field update failed: %v", err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	logger.Infof("Successfully bulk updated %d volumes", len(updates))
	return nil
}

// buildVolumeUpdateQuery creates a parameterized SQL query for bulk volume updates
// Returns the SQL string and arguments slice to prevent SQL injection
func (d *DataStoreRepository) buildVolumeUpdateQuery(ctx context.Context, updates []datamodel.VolumeFieldUpdate) (string, []interface{}) {
	// Build parameterized query with placeholders
	placeholders := make([]string, len(updates))
	args := make([]interface{}, len(updates)*2) // 2 params per update: UUID + used_bytes

	paramCounter := 1 // PostgreSQL params start at $1
	argIndex := 0

	for i, update := range updates {
		uuidParam := paramCounter
		usedBytesParam := paramCounter + 1

		placeholders[i] = fmt.Sprintf("($%d::uuid, $%d::bigint)", uuidParam, usedBytesParam)

		args[argIndex] = update.UUID

		// Simple existence check to prevent panic
		if usedBytes, exists := update.Fields["used_bytes"]; exists {
			args[argIndex+1] = usedBytes
		} else {
			args[argIndex+1] = 0 // Default value if missing
		}

		paramCounter += 2 // Next update needs next 2 parameter slots (UUID + used_bytes)
		argIndex += 2     // Next 2 positions in args array

		// TO ADD NEW FIELDS:
		// - Add new parameter: newFieldParam := paramCounter + 2
		// - Update placeholder: fmt.Sprintf("($%d::uuid, $%d::bigint, $%d::type)", uuidParam, usedBytesParam, newFieldParam)
		// - Add to args: args[argIndex+2] = update.Fields["new_field"]
		// - Update counters: paramCounter += 3, argIndex += 3
		// - Update args slice size above: len(updates)*3
		// - Update SET clause below to include: new_field = tmp.new_field
	}

	// Use parameterized query to prevent SQL injection
	sql := fmt.Sprintf("UPDATE volumes "+
		"SET used_bytes = tmp.used_bytes, updated_at = NOW() "+
		"FROM (VALUES %s) AS tmp(uuid, used_bytes) "+
		"WHERE volumes.uuid::text = tmp.uuid::text",
		strings.Join(placeholders, ", "))

	return sql, args
}

// BatchUpdateVolumeTieringFields efficiently updates tiering fields for multiple volumes using PostgreSQL bulk operations
// Updates: hot_tier_size_gib, cold_tier_size_gib
// Processes updates in batches to avoid overwhelming the database with large queries
func (d *DataStoreRepository) BatchUpdateVolumeTieringFields(ctx context.Context, updates map[string]datamodel.VolumeTieringUpdate) error {
	if len(updates) == 0 {
		return nil
	}

	db := d.db.GORM().WithContext(ctx)
	logger := util.GetLogger(ctx)

	// Convert map to slice for easier batching
	updateSlice := make([]struct {
		UUID   string
		Update datamodel.VolumeTieringUpdate
	}, 0, len(updates))
	for uuid, update := range updates {
		updateSlice = append(updateSlice, struct {
			UUID   string
			Update datamodel.VolumeTieringUpdate
		}{UUID: uuid, Update: update})
	}

	totalUpdates := len(updateSlice)
	logger.Infof("Starting batch update of tiering fields for %d volumes with batch size %d", totalUpdates, UpdateVolumeTieringBatchSize)

	// Process updates in batches
	for i := 0; i < totalUpdates; i += UpdateVolumeTieringBatchSize {
		end := i + UpdateVolumeTieringBatchSize
		if end > totalUpdates {
			end = totalUpdates
		}

		batch := updateSlice[i:end]

		// Start transaction for this batch
		tx, err := startTransaction(db)
		if err != nil {
			return err
		}
		defer commitOrRollbackOnError(logger, tx, &err)

		// Build VALUES clause and args for this batch
		placeholders := make([]string, 0, len(batch))
		args := make([]interface{}, 0, len(batch)*3)
		paramCounter := 1

		for _, item := range batch {
			placeholders = append(placeholders, fmt.Sprintf("($%d::uuid, $%d::bigint, $%d::bigint)",
				paramCounter, paramCounter+1, paramCounter+2))
			args = append(args, item.UUID, item.Update.HotTierSizeGib, item.Update.ColdTierSizeGib)
			paramCounter += 3
		}

		sql := fmt.Sprintf(`UPDATE volumes
			SET hot_tier_size_gib = tmp.hot_tier_size_gib,
			    cold_tier_size_gib = tmp.cold_tier_size_gib,
			    updated_at = NOW()
			FROM (VALUES %s) AS tmp(uuid, hot_tier_size_gib, cold_tier_size_gib)
			WHERE volumes.uuid::text = tmp.uuid::text`,
			strings.Join(placeholders, ", "))

		err = tx.Exec(sql, args...).Error
		if err != nil {
			logger.Errorf("Bulk volume tiering update failed for batch %d-%d: %v", i+1, end, err)
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		}

		logger.Infof("Successfully updated tiering fields for batch %d-%d (%d volumes)", i+1, end, len(batch))
	}

	logger.Infof("Successfully bulk updated tiering fields for all %d volumes", totalUpdates)
	return nil
}

func (d *DataStoreRepository) DeleteVolume(ctx context.Context, volumeUUID string) (*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	return deleteVolume(tx, volumeUUID)
}

func (d *DataStoreRepository) DeleteVolumeAndChildResources(ctx context.Context, volumeUUID string) (*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	volume, err := getVolumeWithDetails(tx, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}})
	if err != nil {
		return nil, err
	}

	// Mark associated snapshots as deleted
	err = tx.Model(&datamodel.Snapshot{}).Where("volume_id = ?", volume.ID).Updates(
		datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true},
			},
			State:        datamodel.LifeCycleStateDeleted,
			StateDetails: datamodel.LifeCycleStateDeletedDetails,
		}).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	// Finally, mark the volume as deleted
	deletedVolume, err := deleteVolume(tx, volumeUUID)
	if err != nil {
		return nil, err
	}

	return deletedVolume, nil
}

func (d *DataStoreRepository) UpdateVolumeState(ctx context.Context, volumeUUID string, state string, stateDetails string) (*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	return updateVolumeState(tx, volumeUUID, state, stateDetails)
}

func _deleteVolume(db *gorm.DB, volumeUUID string) (*datamodel.Volume, error) {
	volume, err := getVolumeWithDetails(db, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}})
	if err != nil {
		return nil, err
	}

	// If volume has a VPG, dereference it before deleting the volume
	// This prevents foreign key constraint issues when cleaning up the VPG later
	needsVPGDereference := false
	if volume.VolumePerformanceGroupID.Valid {
		vpg, vpgErr := getVolumePerformanceGroupByID(db, volume.VolumePerformanceGroupID.Int64)
		if vpgErr == nil && vpg != nil {
			needsVPGDereference = true
		}
	}

	// Prepare updates map
	updates := map[string]interface{}{
		"deleted_at":    &gorm.DeletedAt{Time: time.Now(), Valid: true},
		"state":         datamodel.LifeCycleStateDeleted,
		"state_details": "",
	}

	// If VPG needs to be dereferenced, include it in the updates
	// GORM's Updates with nil will set the field to NULL
	if needsVPGDereference {
		updates["volume_performance_group_id"] = nil
	}

	err = db.Model(&datamodel.Volume{}).Where("id = ?", volume.ID).Updates(updates).Error
	if err != nil {
		return nil, err
	}

	// Update the in-memory struct to reflect the changes
	volume.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	volume.State = datamodel.LifeCycleStateDeleted
	volume.StateDetails = ""
	if needsVPGDereference {
		volume.VolumePerformanceGroupID = sql.NullInt64{Valid: false}
	}

	return volume, nil
}

func _updateVolumeState(db *gorm.DB, volumeUUID string, state string, stateDetails string) (*datamodel.Volume, error) {
	volume, err := getVolumeWithDetails(db, &datamodel.Volume{BaseModel: datamodel.BaseModel{UUID: volumeUUID}})
	if err != nil {
		return nil, err
	}

	volume.State = state
	volume.StateDetails = stateDetails
	err = db.Save(volume).Error
	if err != nil {
		return nil, err
	}

	return volume, nil
}

func (d *DataStoreRepository) ListVolumes(ctx context.Context, conditions [][]interface{}) ([]*datamodel.Volume, error) {
	return listVolumesWithDetails(d.db.ApplyFilter(conditions).GORM().WithContext(ctx))
}

func _listVolumesWithDetails(db *gorm.DB) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := db.Preload("Account").
		Preload("Pool").
		Preload("Pool.ActiveDirectory").
		Preload("Svm").
		Preload("Pool.KmsConfig").
		Preload("VolumePerformanceGroup").
		Find(&volumes).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return volumes, nil
}

// ListVolumesWithPagination retrieves volumes with pagination support including deleted volumes
func (d *DataStoreRepository) ListVolumesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	return _listVolumesWithDetailsPagination(d.db.ApplyFilter(conditions).Unscoped().GORM().WithContext(ctx), pagination)
}

// _listVolumesWithDetailsPagination paginates volumes with deterministic order by id
// so OFFSET/LIMIT pages do not overlap when the planner changes row order between queries.
func _listVolumesWithDetailsPagination(db *gorm.DB, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := db.Preload("Account").
		Preload("Pool").
		Preload("Pool.ActiveDirectory").
		Preload("Svm").
		Order("id").
		Scopes(dbutils.Paginate(pagination)).
		Find(&volumes).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return volumes, nil
}

func getVolumeWithDetails(db *gorm.DB, query *datamodel.Volume) (*datamodel.Volume, error) {
	volume := &datamodel.Volume{}
	err := db.Preload("Account").
		Preload("Pool").
		Preload("Pool.ActiveDirectory").
		Preload("Svm").
		Preload("Pool.KmsConfig").
		Preload("VolumePerformanceGroup").
		First(&volume, query).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVolumeNotFound,
			customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "volume", nil))
	}
	return volume, nil
}

func getVolumeByNameAndAccountIDWithPool(db *gorm.DB, name string, accountID int64) (*datamodel.Volume, error) {
	volume := &datamodel.Volume{}
	err := db.Preload("Pool").First(volume, &datamodel.Volume{Name: name, AccountID: accountID}).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVolumeNotFound,
			customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "volume", nil))
	}
	return volume, nil
}

func (d *DataStoreRepository) GetVolumesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := d.db.GORM().WithContext(ctx).
		Preload("Account").
		Preload("Pool").
		Preload("Pool.ActiveDirectory").
		Preload("Svm").
		Preload("Pool.KmsConfig").
		Preload("VolumePerformanceGroup").
		Where("pool_id = ?", poolID).
		Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

// GetVolumesByPoolIDNoPreload retrieves active volumes by pool ID without loading associations.
// Intended for background sync/tiering flows that only need base volume columns.
func (d *DataStoreRepository) GetVolumesByPoolIDNoPreload(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := d.db.GORM().WithContext(ctx).
		Where("pool_id = ?", poolID).
		Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

// GetPoolVolumesForQosTransition loads active volumes for a pool using a single query and only the
// columns required to unassign/assign QoS policies during manual↔auto qosType transition.
func (d *DataStoreRepository) GetPoolVolumesForQosTransition(ctx context.Context, poolID int64) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Volume{}).
		Select("id", "uuid", "pool_id", "volume_performance_group_id", "volume_attributes").
		Where("pool_id = ?", poolID).
		Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

func (d *DataStoreRepository) GetVolumesByVolumePerformanceGroupID(ctx context.Context, vpgID int64) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := d.db.GORM().WithContext(ctx).
		Preload("Account").
		Preload("Pool").
		Preload("Pool.ActiveDirectory").
		Preload("Svm").
		Preload("Pool.KmsConfig").
		Preload("VolumePerformanceGroup").
		Where("volume_performance_group_id = ?", vpgID).
		Where("deleted_at IS NULL").
		Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

// DereferencePoolVolumesFromVPGs sets volume_performance_group_id to NULL for all volumes in the pool
// that reference any VPG (including soft-deleted volumes). Used before deleting VPGs in manual→auto
// qosType transition to avoid foreign key constraint violations. Returns the number of rows updated.
func (d *DataStoreRepository) DereferencePoolVolumesFromVPGs(ctx context.Context, poolID int64) (int64, error) {
	var err error
	db := d.db.GORM().WithContext(ctx)
	tx, txErr := startTransaction(db)
	if txErr != nil {
		err = txErr
		return 0, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	result := tx.Unscoped().
		Model(&datamodel.Volume{}).
		Where("pool_id = ?", poolID).
		Where("volume_performance_group_id IS NOT NULL").
		Updates(map[string]interface{}{
			"volume_performance_group_id": nil,
			"updated_at":                  time.Now(),
		})

	if result.Error != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
		return 0, err
	}
	return result.RowsAffected, nil
}

// DereferenceVPGFromDeletedVolumes sets volume_performance_group_id to NULL for all deleted volumes
// that reference the given VPG. This prevents foreign key constraint violations when deleting the VPG.
func (d *DataStoreRepository) DereferenceVPGFromDeletedVolumes(ctx context.Context, vpgID int64) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// Update all deleted volumes that reference this VPG to set volume_performance_group_id = NULL
	result := tx.Unscoped().
		Model(&datamodel.Volume{}).
		Where("volume_performance_group_id = ?", vpgID).
		Where("deleted_at IS NOT NULL").
		Update("volume_performance_group_id", nil)

	if result.Error != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}

	if result.RowsAffected > 0 {
		logger.Debug("Dereferenced VPG from deleted volumes", "vpg_id", vpgID, "volumes_updated", result.RowsAffected)
	}

	return nil
}

// HasDependentChildThinClone reports whether any active volume in the pool is still
// a thin clone whose parent is parentVolumeUUID (blocks ONTAP split on the parent).
//
// If this query becomes slow in production (large pools, high clone churn), consider a
// partial expression index, for example:
//
//	CREATE INDEX CONCURRENTLY idx_volumes_clone_parent_volume_uuid
//	ON volumes ((volume_attributes->'clone_parent_info'->>'parent_volume_uuid'))
//	WHERE volume_attributes->'clone_parent_info' IS NOT NULL;
//
// Optionally include pool_id in the index definition if plans show sequential scans per pool.
func (d *DataStoreRepository) HasDependentChildThinClone(ctx context.Context, poolID int64, parentVolumeUUID string) (bool, error) {
	var exists bool
	err := d.db.GORM().WithContext(ctx).Raw(`
		SELECT EXISTS (
			SELECT 1
			FROM volumes
			WHERE pool_id = ?
				AND deleted_at IS NULL
				AND state <> 'DELETED'
				AND volume_attributes->'clone_parent_info' IS NOT NULL
				AND volume_attributes->'clone_parent_info' <> 'null'::jsonb
				AND volume_attributes->'clone_parent_info'->>'parent_volume_uuid' = ?
		)`, poolID, parentVolumeUUID).Scan(&exists).Error
	if err != nil {
		return false, err
	}
	return exists, nil
}

func (d *DataStoreRepository) GetVolumeCountByPoolID(ctx context.Context, poolID int64) (int64, error) {
	var count int64
	err := d.db.GORM().WithContext(ctx).Model(&datamodel.Volume{}).Where("pool_id = ?", poolID).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetVolumeCountByVolumePerformanceGroupID(ctx context.Context, vpgID int64) (int64, error) {
	var count int64
	err := d.db.GORM().WithContext(ctx).Model(&datamodel.Volume{}).
		Where("volume_performance_group_id = ?", vpgID).
		Where("deleted_at IS NULL").Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetFlexCacheVolumeCountByClusterPeerID(ctx context.Context, clusterPeerID int64) (int64, error) {
	var count int64
	err := d.db.GORM().
		WithContext(ctx).
		Model(&datamodel.Volume{}).
		Joins("JOIN cluster_peerings ON cluster_peerings.id = volumes.cluster_peer_id AND cluster_peerings.deleted_at IS NULL").
		Where("volumes.cluster_peer_id = ?", clusterPeerID).
		Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetMultipleVolumes(ctx context.Context, conditions [][]interface{}) ([]*datamodel.Volume, error) {
	return getMultipleVolumes(d.db.ApplyFilter(conditions).GORM().WithContext(ctx))
}

type VolumePreloadOptions struct {
	ActiveDirectory        bool
	KmsConfig              bool
	VolumePerformanceGroup bool
}

func (d *DataStoreRepository) GetMultipleVolumesSelective(ctx context.Context, conditions [][]interface{}, opts VolumePreloadOptions) ([]*datamodel.Volume, error) {
	return getMultipleVolumesSelective(d.db.ApplyFilter(conditions).GORM().WithContext(ctx), opts)
}

func _getMultipleVolumes(db *gorm.DB) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := db.Preload("Account").
		Preload("Pool").
		Preload("Pool.ActiveDirectory").
		Preload("Svm").
		Preload("Pool.KmsConfig").
		Preload("VolumePerformanceGroup").
		Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

func getMultipleVolumesSelective(db *gorm.DB, opts VolumePreloadOptions) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume

	query := db.Preload("Account").
		Preload("Pool")

	if opts.ActiveDirectory {
		query = query.Preload("Pool.ActiveDirectory")
	}
	if opts.KmsConfig {
		query = query.Preload("Pool.KmsConfig")
	}
	if opts.VolumePerformanceGroup {
		query = query.Preload("VolumePerformanceGroup")
	}

	err := query.Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

func (d *DataStoreRepository) VerifyVolumeOwnership(ctx context.Context, volumeUUID string, accountName string) (*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)
	var account *datamodel.Account
	if err := db.Where("name = ?", accountName).First(&account).Error; err != nil {
		return nil, err
	}
	var volume *datamodel.Volume
	if err := db.Preload("Account").Preload("Pool").Preload("Svm").Where("uuid = ?", volumeUUID).Where("account_id= ?", account.ID).First(&volume).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("volume", &volumeUUID)
		}
		return nil, err
	}
	return volume, nil
}

func (d *DataStoreRepository) GetVolumeCount(ctx context.Context, accountName string) (int64, error) {
	var count int64
	account, err := d.GetAccount(ctx, accountName)
	if err != nil {
		return 0, err
	}
	err = d.db.GORM().WithContext(ctx).Model(&datamodel.Volume{}).Where("account_id = ?", account.ID).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

func (d *DataStoreRepository) GetAllVolumesForHG(ctx context.Context, hostGroupUUID string, accountID int64) ([]*datamodel.Volume, error) {
	return volumesWithHG(d.db.GORM().WithContext(ctx), hostGroupUUID, accountID)
}

func _volumesWithHG(db *gorm.DB, hostGroupUUID string, accountID int64) ([]*datamodel.Volume, error) {
	var volumesWithBD []*datamodel.Volume
	err := db.Model(&datamodel.Volume{}).
		Preload("Account").
		Preload("Pool").
		Preload("Svm").
		Where("account_id = ?", accountID).
		Where("(volume_attributes::jsonb->'block_devices' != 'null'::jsonb) AND EXISTS(SELECT 1 FROM jsonb_array_elements(volume_attributes->'block_devices') AS bd WHERE (bd->'host_group_details' != 'null'::jsonb) AND EXISTS (SELECT 1 FROM jsonb_array_elements(bd->'host_group_details') AS hgd WHERE hgd->>'host_group_uuid' = ?))", hostGroupUUID).
		Find(&volumesWithBD).Error
	if err != nil {
		return nil, err
	}

	var volumesWithBP []*datamodel.Volume
	err = db.Model(&datamodel.Volume{}).
		Preload("Account").
		Preload("Pool").
		Preload("Svm").
		Where("account_id = ?", accountID).
		Where("(volume_attributes::jsonb->'block_properties' IS NOT NULL) AND (volume_attributes::jsonb->'block_properties'->'host_group_details' != 'null'::jsonb) AND EXISTS (SELECT 1 FROM jsonb_array_elements(volume_attributes::jsonb->'block_properties'->'host_group_details') AS elem WHERE elem->>'host_group_uuid' = ?)", hostGroupUUID).
		Find(&volumesWithBP).Error
	if err != nil {
		return nil, err
	}

	volumesWithBD = append(volumesWithBD, volumesWithBP...)
	return volumesWithBD, err
}

// ListAllVolumes retrieves all volumes
func (d *DataStoreRepository) ListAllVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	return listAllVolumesWithDetails(d.db.ApplyFilter(conditions).GORM().WithContext(ctx), pagination)
}

func _listAllVolumesWithDetails(db *gorm.DB, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := db.Select("name, state, account_id, auto_tiering_enabled, snapshot_policy, large_volume_attributes, data_protection").Preload("Account").Where("deleted_at IS NULL").Scopes(dbutils.Paginate(pagination)).Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

// ListVolumesWithAccounts retrieves all volumes with preloaded accounts
func (d *DataStoreRepository) ListVolumesWithAccounts(ctx context.Context) ([]*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)
	var volumes []*datamodel.Volume

	// Query to get all volumes with preloaded accounts and pools (only deployment_name for pools)
	err := db.Preload("Account", func(db *gorm.DB) *gorm.DB {
		return db.Select("id, name")
	}).
		Preload("Pool", func(db *gorm.DB) *gorm.DB {
			return db.Select("id, deployment_name")
		}).
		Find(&volumes).Error

	if err != nil {
		return nil, err
	}

	return volumes, nil
}

func (d *DataStoreRepository) GetEligibleVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	return eligibleVolDetails(d.db.ApplyFilter(conditions).GORM().WithContext(ctx), pagination)
}

func _eligibleVolDetails(db *gorm.DB, pagination *dbutils.Pagination) ([]*datamodel.Volume, error) {
	var volumes []*datamodel.Volume
	err := db.Select("name, state").Preload("Account").Where("deleted_at IS NULL").Scopes(dbutils.Paginate(pagination)).Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

// _findVolumeInRegionalPool finds a volume by name and account ID in regional pools
// Returns gorm.ErrRecordNotFound if no matching volume exists
func _findVolumeInRegionalPool(db *gorm.DB, volumeName string, accountID int64, preloadAssociations bool) (*datamodel.Volume, error) {
	var volume datamodel.Volume
	query := db.Table("volumes").
		Joins("JOIN pools ON volumes.pool_id = pools.id").
		Where("volumes.name = ? AND volumes.account_id = ?", volumeName, accountID).
		Where("pools.pool_attributes->>'is_regional_ha' = 'true'")

	if preloadAssociations {
		query = query.Preload("Account").Preload("Pool")
	}

	err := query.First(&volume).Error
	if err != nil {
		return nil, err
	}
	return &volume, nil
}

// _findVolumeInZonalPool finds a volume by name and account ID in a specific zone's non-regional i.e zonal pools
// Returns gorm.ErrRecordNotFound if no matching volume exists
func _findVolumeInZonalPool(db *gorm.DB, volumeName string, accountID int64, zone string, preloadAssociations bool) (*datamodel.Volume, error) {
	var volume datamodel.Volume
	query := db.Table("volumes").
		Joins("JOIN pools ON volumes.pool_id = pools.id").
		Where("volumes.name = ? AND volumes.account_id = ?", volumeName, accountID).
		Where("pools.pool_attributes->>'primary_zone' = ?", zone).
		Where("pools.pool_attributes->>'is_regional_ha' = 'false'")

	if preloadAssociations {
		query = query.Preload("Account").Preload("Pool")
	}

	err := query.First(&volume).Error
	if err != nil {
		return nil, err
	}
	return &volume, nil
}

func (d *DataStoreRepository) GetVolumeByNameAccountIDAndZone(ctx context.Context, name string, accountID int64, zone string, isRegionalPool bool) (*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)

	var volume *datamodel.Volume
	var err error

	if isRegionalPool {
		volume, err = FindVolumeInRegionalPool(db, name, accountID, true)
	} else {
		volume, err = FindVolumeInZonalPool(db, name, accountID, zone, true)
	}

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrVolumeNotFound,
				customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "volume", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return volume, nil
}

func (d *DataStoreRepository) GetVolumeByJunctionPath(ctx context.Context, junctionPath string, accountID int64, poolId int64) (*datamodel.Volume, error) {
	db := d.db.GORM().WithContext(ctx)

	var volume datamodel.Volume
	query := db.Table("volumes").
		Where("volume_attributes #>> '{file_properties,junction_path}' = ?", "/"+junctionPath).
		Where("account_id = ?", accountID)

	if poolId != 0 {
		query = query.Where("pool_id = ?", poolId)
	}

	err := query.First(&volume).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrVolumeNotFound,
			customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "volume", nil))
	}
	return &volume, nil
}

// GetActivePrepopulateJobs retrieves all active (NEW or PROCESSING) prepopulate jobs
func (d *DataStoreRepository) GetActivePrepopulateJobs(ctx context.Context) ([]*datamodel.Job, error) {
	return getActivePrepopulateJobs(d.db.GORM().WithContext(ctx))
}

func getActivePrepopulateJobs(db *gorm.DB) ([]*datamodel.Job, error) {
	var jobs []*datamodel.Job

	err := db.
		Where("type = ?", datamodel.JobTypeFlexCachePrePopulate).
		Where("state IN ?", []string{
			string(datamodel.JobsStateNEW),
			string(datamodel.JobsStatePROCESSING),
		}).
		Order("created_at ASC"). // Process oldest jobs first
		Find(&jobs).Error

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return jobs, nil
}

// VolumeResourceData contains only the fields required for aggregator resource data collection.
// This is an optimized structure for fetchVolumeData in telemetry aggregator.
// Account name, deployment name, labels, and IsRegionalHA are extracted from volume_attributes JSONB.
// LargeCapacity is extracted from large_volume_attributes JSONB.
type VolumeResourceData struct {
	UUID                   string                           `gorm:"column:uuid"`
	Name                   string                           `gorm:"column:name"`
	AccountID              int64                            `gorm:"column:account_id"`
	VolumeAttributes       *datamodel.VolumeAttributes      `gorm:"column:volume_attributes;type:jsonb"`
	LargeVolumeAttributes  *datamodel.LargeVolumeAttributes `gorm:"column:large_volume_attributes;type:jsonb"`
	CreatedAt              time.Time                        `gorm:"column:created_at"`
	BackupVaultServiceType string                           `gorm:"column:backup_vault_service_type"`
	BackupVaultAccountName string                           `gorm:"column:backup_vault_account_name"`
	BackupVaultAccountID   int64                            `gorm:"column:backup_vault_account_id"`
}

// GetLargeCapacity returns the large capacity flag from LargeVolumeAttributes
func (v *VolumeResourceData) GetLargeCapacity() bool {
	if v.LargeVolumeAttributes != nil {
		return v.LargeVolumeAttributes.LargeCapacity
	}
	return false
}

// GetAccountName returns the account name from VolumeAttributes
func (v *VolumeResourceData) GetAccountName() string {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.AccountName
	}
	return ""
}

// GetDeploymentName returns the deployment name from VolumeAttributes
func (v *VolumeResourceData) GetDeploymentName() string {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.DeploymentName
	}
	return ""
}

// GetLabels returns labels from VolumeAttributes
func (v *VolumeResourceData) GetLabels() *datamodel.JSONB {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.Labels
	}
	return nil
}

// IsRegionalHA returns whether volume is in a regional HA pool
func (v *VolumeResourceData) IsRegionalHA() bool {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.IsRegionalHA
	}
	return false
}

// GetBackupVaultServiceType returns the service type of the volume's attached backup vault.
func (v *VolumeResourceData) GetBackupVaultServiceType() string {
	return v.BackupVaultServiceType
}

// GetBackupVaultAccountName returns the account name of the volume's attached backup vault.
func (v *VolumeResourceData) GetBackupVaultAccountName() string {
	return v.BackupVaultAccountName
}

// GetBackupVaultAccountID returns the internal account id of the volume's attached backup vault.
func (v *VolumeResourceData) GetBackupVaultAccountID() int64 {
	return v.BackupVaultAccountID
}

// ListVolumesForResourceData retrieves volumes with only the fields required for aggregator resource data collection.
// This is an optimized query with pagination support for fetchVolumeData in telemetry aggregator.
// Account name, deployment name, labels and IsRegionalHA are extracted from volume_attributes JSONB.
// Backup vault service type, account name, and account id come from a LEFT JOIN on the attached backup vault.
// Includes support for deleted_at filter to include recently deleted volumes.
func (d *DataStoreRepository) ListVolumesForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*VolumeResourceData, error) {
	db := d.db.GORM().WithContext(ctx)

	var results []*VolumeResourceData

	query := db.Table("volumes v").
		Joins("LEFT JOIN backup_vaults bv ON bv.uuid = v.data_protection->>'backup_vault_id'").
		Joins("LEFT JOIN accounts va ON va.id = bv.account_id").
		Select(`
			v.uuid,
			v.name,
			v.account_id,
			v.volume_attributes,
			v.large_volume_attributes,
			v.created_at,
			COALESCE(bv.service_type, '') AS backup_vault_service_type,
			COALESCE(va.name, '') AS backup_vault_account_name,
			COALESCE(bv.account_id, 0) AS backup_vault_account_id
		`).
		Where("(v.deleted_at IS NULL OR (v.deleted_at >= ? AND v.deleted_at <= ?))", startTime, endTime)

	// Apply pagination
	if pagination != nil {
		if pagination.Limit > 0 {
			query = query.Limit(pagination.Limit)
		}
		if pagination.Offset > 0 {
			query = query.Offset(pagination.Offset)
		}
	}

	err := query.Find(&results).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return results, nil
}

// VolumeMetricsData contains only the fields required for telemetry volume metrics collection.
// This is an optimized structure that avoids JOINs with Account and Pool tables.
// Account name, deployment name, and protocols are extracted from volume_attributes JSONB.
type VolumeMetricsData struct {
	UUID             string                      `gorm:"column:uuid"`
	Name             string                      `gorm:"column:name"`
	SizeInBytes      int64                       `gorm:"column:size_in_bytes"`
	Throughput       int64                       `gorm:"column:throughput"`
	Iops             int64                       `gorm:"column:iops"`
	PoolID           int64                       `gorm:"column:pool_id"`
	VolumeAttributes *datamodel.VolumeAttributes `gorm:"column:volume_attributes;type:jsonb"`
	DataProtection   *datamodel.DataProtection   `gorm:"column:data_protection;type:jsonb"`
}

// GetAccountName returns the account name from VolumeAttributes
func (v *VolumeMetricsData) GetAccountName() string {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.AccountName
	}
	return ""
}

// GetDeploymentName returns the deployment name from VolumeAttributes
func (v *VolumeMetricsData) GetDeploymentName() string {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.DeploymentName
	}
	return ""
}

// GetProtocols returns protocols from VolumeAttributes
func (v *VolumeMetricsData) GetProtocols() []string {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.Protocols
	}
	return nil
}

// IsRegionalHA returns whether volume is in a regional HA pool
func (v *VolumeMetricsData) IsRegionalHA() bool {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.IsRegionalHA
	}
	return false
}

// ExpertModeVolumeMetricsData contains the fields required for telemetry billing metrics collection
// for expert mode volumes. Account name, account ID, and deployment name are resolved via JOINs
// with accounts and pools tables; protocols are extracted from the volume_attributes JSONB column.
// PoolIsRegionalHA reflects the pool's is_regional_ha attribute and is used by the billing
// aggregator to select the correct resource type (Volume vs VolumeRegionalHA).
type ExpertModeVolumeMetricsData struct {
	UUID                   string
	ExternalUUID           string
	Name                   string
	SizeInBytes            int64
	PoolID                 int64
	AccountID              int64
	AccountName            string
	DeploymentName         string
	PoolIsRegionalHA       bool
	PoolAPIAccessMode      string
	PoolServiceLevel       string
	BackupConfig           *datamodel.DataProtection
	VolumeAttributes       *datamodel.ExpertModeVolumeAttributes
	BackupVaultServiceType string
	BackupVaultAccountName string
	BackupVaultAccountID   int64
}

// BackupVolumeUUID returns the identity that backups.volume_uuid holds for this volume.
// Expert mode backups reference the volume's external UUID, not the internal one, so any
// join against backup rows must use this. Volumes provisioned before an external UUID was
// assigned fall back to the internal UUID to keep the value distinct per volume.
func (v *ExpertModeVolumeMetricsData) BackupVolumeUUID() string {
	if v.ExternalUUID != "" {
		return v.ExternalUUID
	}
	return v.UUID
}

// GetProtocols returns the protocols from volume_attributes for expert mode volumes.
func (v *ExpertModeVolumeMetricsData) GetProtocols() []string {
	if v.VolumeAttributes != nil {
		return v.VolumeAttributes.Protocols
	}
	return nil
}

// ListExpertModeVolumesForTelemetryMetrics retrieves expert mode volumes with only the fields
// required for telemetry billing metrics collection. Account name and deployment name are
// resolved via JOINs with accounts and pools tables. When pagination is nil all records are
// returned (existing behaviour); callers that process large datasets should pass a non-nil
// Pagination to iterate in pages.
func (d *DataStoreRepository) ListExpertModeVolumesForTelemetryMetrics(ctx context.Context, pagination *dbutils.Pagination) ([]*ExpertModeVolumeMetricsData, error) {
	db := d.db.GORM().WithContext(ctx)

	var results []*ExpertModeVolumeMetricsData

	query := db.Table("expert_mode_volumes").
		Joins("LEFT JOIN accounts a ON a.id = expert_mode_volumes.account_id").
		Joins("LEFT JOIN pools p ON p.id = expert_mode_volumes.pool_id").
		Joins("LEFT JOIN backup_vaults bv ON bv.uuid = expert_mode_volumes.data_protection->>'backup_vault_id'").
		Joins("LEFT JOIN accounts va ON va.id = bv.account_id").
		Select(`
			expert_mode_volumes.uuid,
			COALESCE(expert_mode_volumes.external_uuid, '') AS external_uuid,
			expert_mode_volumes.name,
			expert_mode_volumes.size_in_bytes,
			expert_mode_volumes.pool_id,
			COALESCE(a.id, 0) AS account_id,
			COALESCE(a.name, '') AS account_name,
			COALESCE(p.deployment_name, '') AS deployment_name,
			COALESCE((p.pool_attributes->>'is_regional_ha')::boolean, false) AS pool_is_regional_ha,
			COALESCE(p.api_access_mode, '') AS pool_api_access_mode,
			COALESCE(NULLIF(p.pool_attributes->>'service_level', ''), p.service_level, '') AS pool_service_level,
			expert_mode_volumes.data_protection AS backup_config,
			expert_mode_volumes.volume_attributes,
			COALESCE(bv.service_type, '') AS backup_vault_service_type,
			COALESCE(va.name, '') AS backup_vault_account_name,
			COALESCE(bv.account_id, 0) AS backup_vault_account_id
		`).
		Where("expert_mode_volumes.deleted_at IS NULL").
		Order("expert_mode_volumes.id ASC")

	if pagination != nil {
		if pagination.Limit > 0 {
			query = query.Limit(pagination.Limit)
		}
		if pagination.Offset > 0 {
			query = query.Offset(pagination.Offset)
		}
	}

	err := query.Find(&results).Error

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return results, nil
}

// ListExpertModeVolumesForResourceData retrieves expert-mode volumes for aggregator
// resource-data lookup. Recently deleted volumes whose deleted_at falls in [startTime,
// endTime] are included so in-flight aggregation windows still resolve.
// ListExpertModeVolumesForTelemetryMetrics is left unchanged so backup/throughput collectors
// keep their active-only query.
func (d *DataStoreRepository) ListExpertModeVolumesForResourceData(ctx context.Context, startTime, endTime time.Time, pagination *dbutils.Pagination) ([]*ExpertModeVolumeMetricsData, error) {
	db := d.db.GORM().WithContext(ctx)

	var results []*ExpertModeVolumeMetricsData
	query := db.Table("expert_mode_volumes").
		Joins("LEFT JOIN accounts a ON a.id = expert_mode_volumes.account_id").
		Joins("LEFT JOIN pools p ON p.id = expert_mode_volumes.pool_id").
		Joins("LEFT JOIN backup_vaults bv ON bv.uuid = expert_mode_volumes.data_protection->>'backup_vault_id'").
		Joins("LEFT JOIN accounts va ON va.id = bv.account_id").
		Select(`
			expert_mode_volumes.uuid,
			COALESCE(expert_mode_volumes.external_uuid, '') AS external_uuid,
			expert_mode_volumes.name,
			expert_mode_volumes.size_in_bytes,
			expert_mode_volumes.pool_id,
			COALESCE(a.id, 0) AS account_id,
			COALESCE(a.name, '') AS account_name,
			COALESCE(p.deployment_name, '') AS deployment_name,
			COALESCE((p.pool_attributes->>'is_regional_ha')::boolean, false) AS pool_is_regional_ha,
			COALESCE(p.api_access_mode, '') AS pool_api_access_mode,
			COALESCE(NULLIF(p.pool_attributes->>'service_level', ''), p.service_level, '') AS pool_service_level,
			expert_mode_volumes.data_protection AS backup_config,
			expert_mode_volumes.volume_attributes,
			COALESCE(bv.service_type, '') AS backup_vault_service_type,
			COALESCE(va.name, '') AS backup_vault_account_name,
			COALESCE(bv.account_id, 0) AS backup_vault_account_id
		`).
		Where("(expert_mode_volumes.deleted_at IS NULL OR (expert_mode_volumes.deleted_at >= ? AND expert_mode_volumes.deleted_at <= ?))", startTime, endTime).
		Order("expert_mode_volumes.id ASC")

	if pagination != nil {
		if pagination.Limit > 0 {
			query = query.Limit(pagination.Limit)
		}
		if pagination.Offset > 0 {
			query = query.Offset(pagination.Offset)
		}
	}

	err := query.Find(&results).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return results, nil
}

// ListVolumesForTelemetryMetrics retrieves volumes with only the fields required for telemetry metrics collection.
// This is an optimized query that avoids JOINs with Account and Pool tables.
// Account name, deployment name, and protocols are extracted from volume_attributes JSONB.
func (d *DataStoreRepository) ListVolumesForTelemetryMetrics(ctx context.Context) ([]*VolumeMetricsData, error) {
	db := d.db.GORM().WithContext(ctx)

	var results []*VolumeMetricsData

	// Select only the required columns from volumes table
	// Account name, deployment name, and protocols are in volume_attributes JSONB, JOIN on vpg needed for throughput/iops
	err := db.Table("volumes").
		Joins("LEFT JOIN volume_performance_groups vpg ON vpg.id = volumes.volume_performance_group_id").
		Select(`
			volumes.uuid,
			volumes.name,
			volumes.size_in_bytes,
			COALESCE(vpg.throughput_mibps, 0) AS throughput,
			COALESCE(vpg.iops, 0) AS iops,
			volumes.pool_id,
			volumes.volume_attributes,
			volumes.data_protection
		`).
		Where("volumes.deleted_at IS NULL").
		Find(&results).Error

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return results, nil
}
