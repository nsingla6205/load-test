package database

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var deleteSnapshot = _deleteSnapshot

func (d *DataStoreRepository) CreatingSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) (_ *datamodel.Snapshot, err error) {
	db := d.db.GORM().WithContext(ctx)
	logger := util.GetLogger(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(logger, tx, &err)

	allowedByVolume, lockErr := lockVolumeAndCheckSnapshotWrite(tx, []int64{snapshot.VolumeID})
	if lockErr != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, lockErr)
		return nil, err
	}
	if !allowedByVolume[snapshot.VolumeID] {
		err = customerrors.NewConflictErr("volume is missing, deleting, or deleted")
		return nil, err
	}

	snapshotEntry := &datamodel.Snapshot{}

	dbError := tx.Where("account_id = ?", snapshot.AccountID).Where("volume_id = ?", snapshot.VolumeID).Where("name = ?", snapshot.Name).First(&snapshotEntry).Error

	if snapshotEntry.ID != 0 {
		logger.Warnf("Snapshot with name %s already exists", snapshot.Name)
		err = customerrors.NewConflictErr("Snapshot already exists")
		return nil, err
	}
	if dbError != nil && !errors.Is(dbError, gorm.ErrRecordNotFound) {
		logger.Errorf("Snapshot create operation failed with error: %v", dbError)
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, dbError)
		return nil, err
	}
	snapshot.UUID = utils.RandomUUID()
	snapshot.State = datamodel.LifeCycleStateCreating
	snapshot.StateDetails = datamodel.LifeCycleStateCreatingDetails
	snapshot.CreatedAt = time.Now()
	snapshot.UpdatedAt = snapshot.CreatedAt
	snapshot.DeletedAt = nil

	err = tx.Create(snapshot).Error
	if err != nil {
		logger.Errorf("Snapshot create operation failed with error: %v", err)
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		return nil, err
	}
	return snapshot, nil
}

func getSnapshotWithDetails(db *gorm.DB, query *datamodel.Snapshot) (*datamodel.Snapshot, error) {
	snapshot := &datamodel.Snapshot{}
	db = db.Preload("Account").Preload("Volume").Preload("Volume.Pool")
	err := db.First(&snapshot, query).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			var identifier *string
			if query != nil {
				if query.UUID != "" {
					identifier = &query.UUID
				} else if query.Name != "" {
					identifier = &query.Name
				}
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("snapshot", identifier))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return snapshot, nil
}

func (d *DataStoreRepository) UpdateSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error) {
	logger := util.GetLogger(ctx)
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(logger, tx, &err)
	dbSnapshot, err := getSnapshotWithDetails(tx, &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: snapshot.UUID}})
	if err != nil {
		return nil, err
	}
	err = tx.Model(&dbSnapshot).Updates(datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{
			DeletedAt: snapshot.DeletedAt,
			UpdatedAt: time.Now(),
		},
		Name:               snapshot.Name,
		Description:        snapshot.Description,
		ExternalUUID:       snapshot.ExternalUUID,
		SnapshotAttributes: snapshot.SnapshotAttributes,
		State:              snapshot.State,
		StateDetails:       snapshot.StateDetails,
	}).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return dbSnapshot, nil
}

func (d *DataStoreRepository) UpdateSnapshotForHandleResource(ctx context.Context, snapshot *datamodel.Snapshot) (*datamodel.Snapshot, error) {
	logger := util.GetLogger(ctx)
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(logger, tx, &err)
	dbSnapshot, err := getSnapshotWithDetails(tx, &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: snapshot.UUID}})
	if err != nil {
		return nil, err
	}
	if dbSnapshot.State == datamodel.LifeCycleStateCreating ||
		dbSnapshot.State == datamodel.LifeCycleStateDeleting ||
		dbSnapshot.State == datamodel.LifeCycleStateUpdating {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrHREResourceIsTransitioning, errors.New("snapshot is in transition state"))
	}
	err = tx.Model(&dbSnapshot).Updates(datamodel.Snapshot{
		Name:               snapshot.Name,
		Description:        snapshot.Description,
		ExternalUUID:       snapshot.ExternalUUID,
		SnapshotAttributes: snapshot.SnapshotAttributes,
		State:              snapshot.State,
		StateDetails:       snapshot.StateDetails,
	}).Error

	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return dbSnapshot, nil
}

func (d *DataStoreRepository) GetAppConsistentSnapshotsForVolume(ctx context.Context, accountID, volumeID int64) ([]*datamodel.Snapshot, error) {
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("account_id", "=", accountID),
		utils2.NewFilterCondition("volume_id", "=", volumeID),
		utils2.NewFilterCondition("is_app_consistent", "=", true))
	return d.GetSnapshotsWithCondition(ctx, *filter)
}

// GetSnapshotByUUID Retrieves a snapshot by UUID, account ID, and volume ID from the database.
func (d *DataStoreRepository) GetSnapshotByUUID(ctx context.Context, uuid string, accountID int64, volumeID int64) (*datamodel.Snapshot, error) {
	return getSnapshotWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Snapshot{AccountID: accountID, VolumeID: volumeID, BaseModel: datamodel.BaseModel{UUID: uuid}})
}

// GetSnapshotByNameAndVolumeId Retrieves a snapshot by name, account ID, and volume ID from the database.
func (d *DataStoreRepository) GetSnapshotByNameAndVolumeId(ctx context.Context, snapshotName string, accountID int64, volumeID int64) (*datamodel.Snapshot, error) {
	return getSnapshotWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Snapshot{AccountID: accountID, VolumeID: volumeID, Name: snapshotName})
}

// GetSnapshotByPoolID Retrieves a snapshot by UUID, account ID, and pool ID, validating the pool association and optionally preloading parent snapshot details.
func (d *DataStoreRepository) GetSnapshotByPoolID(ctx context.Context, uuid string, accountID int64, poolID int64, isParentSnapshot bool) (*datamodel.Snapshot, error) {
	db := d.db.GORM().WithContext(ctx).Preload("Volume").Preload("Volume.Pool")
	if isParentSnapshot {
		db = db.Preload("Volume.Svm")
	}

	snapshot := &datamodel.Snapshot{}
	err := db.First(&snapshot, &datamodel.Snapshot{
		AccountID: accountID,
		BaseModel: datamodel.BaseModel{UUID: uuid},
	}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "snapshot", nil)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Check if the PoolID of the associated volume matches the provided PoolID
	if snapshot.Volume != nil && snapshot.Volume.PoolID == poolID {
		return snapshot, nil
	}

	return nil, customerrors.NewUserInputValidationErr("Restore snapshots across pool is not supported")
}

func (d *DataStoreRepository) GetSnapshotsWithCondition(ctx context.Context, filter utils2.Filter) ([]*datamodel.Snapshot, error) {
	db := d.db.ApplyFilter(filter.Apply()).GORM().WithContext(ctx)
	var snapshots []*datamodel.Snapshot
	err := db.Preload("Volume").Find(&snapshots).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return snapshots, nil
}

func (d *DataStoreRepository) DeleteSnapshot(ctx context.Context, snapshotUUID string) (*datamodel.Snapshot, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	return deleteSnapshot(tx, snapshotUUID)
}

func _deleteSnapshot(db *gorm.DB, snapshotUUID string) (*datamodel.Snapshot, error) {
	snapshot, err := getSnapshotWithDetails(db, &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: snapshotUUID}})
	if err != nil {
		return nil, err
	}
	snapshot.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	snapshot.State = datamodel.LifeCycleStateDeleted
	snapshot.StateDetails = datamodel.LifeCycleStateDeletedDetails
	err = db.Save(snapshot).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, err)
	}

	return snapshot, nil
}

// DeletingSnapshot updates the snapshot entry to deleting state
func (d *DataStoreRepository) DeletingSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	snapshot.State = datamodel.LifeCycleStateDeleting
	snapshot.StateDetails = datamodel.LifeCycleStateDeletingDetails
	err = tx.Updates(snapshot).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

func (d *DataStoreRepository) BatchDeleteSnapshots(ctx context.Context, snapshotIDs []int64) ([]*datamodel.Snapshot, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	err = tx.Model(&datamodel.Snapshot{}).Where("id IN ?", snapshotIDs).Updates(
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

	var snapshots []*datamodel.Snapshot
	err = tx.Unscoped().Preload("Account").Preload("Volume").Preload("Volume.Pool").Where("id IN ?", snapshotIDs).Find(&snapshots).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return snapshots, nil
}

// BatchDeleteReadySnapshotsIfUnchanged soft-deletes READY snapshots whose updated_at still matches the baseline.
// A row that changed after that read is left in place and omitted from the result, so sync does not hydrate a delete it did not perform.
func (d *DataStoreRepository) BatchDeleteReadySnapshotsIfUnchanged(ctx context.Context, snapshots []*datamodel.Snapshot) (_ []*datamodel.Snapshot, err error) {
	candidates := readySnapshotDeleteCandidates(snapshots)
	if len(candidates) == 0 {
		return nil, nil
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	predicate, args := optimisticReadySnapshotDeletePredicate(candidates)
	now := time.Now()
	// Single UPDATE ... RETURNING * mutates only rows that still match the optimistic
	// baseline and returns exactly that write set (no follow-up scan of already-deleted rows).
	var updated []datamodel.Snapshot
	err = tx.Model(&updated).
		Clauses(clause.Returning{}).
		Where(predicate, args...).
		Updates(datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				DeletedAt: &gorm.DeletedAt{Time: now, Valid: true},
			},
			State:        datamodel.LifeCycleStateDeleted,
			StateDetails: datamodel.LifeCycleStateDeletedDetails,
		}).Error
	if err != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		return nil, err
	}
	if len(updated) == 0 {
		if len(candidates) > 0 {
			logger.Info("skipped stale snapshot deletes because rows changed", "skipped", len(candidates), "deleted", 0)
		}
		return nil, nil
	}

	deletedIDs := make([]int64, len(updated))
	for i := range updated {
		deletedIDs[i] = updated[i].ID
	}
	var deleted []*datamodel.Snapshot
	err = tx.Unscoped().
		Preload("Account").
		Preload("Volume").
		Preload("Volume.Pool").
		Where("id IN ?", deletedIDs).
		Find(&deleted).Error
	if err != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		return nil, err
	}
	if skipped := len(candidates) - len(deleted); skipped > 0 {
		logger.Info("skipped stale snapshot deletes because rows changed", "skipped", skipped, "deleted", len(deleted))
	}
	return deleted, nil
}

// readySnapshotDeleteCandidates keeps the first occurrence of each snapshot ID.
func readySnapshotDeleteCandidates(snapshots []*datamodel.Snapshot) []*datamodel.Snapshot {
	seen := make(map[int64]struct{}, len(snapshots))
	candidates := make([]*datamodel.Snapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot == nil || snapshot.ID == 0 {
			continue
		}
		if _, ok := seen[snapshot.ID]; ok {
			continue
		}
		seen[snapshot.ID] = struct{}{}
		candidates = append(candidates, snapshot)
	}
	return candidates
}

// optimisticReadySnapshotDeletePredicate builds a single WHERE clause that matches each
// candidate only when id, READY state, and baseline updated_at still agree.
func optimisticReadySnapshotDeletePredicate(candidates []*datamodel.Snapshot) (string, []interface{}) {
	parts := make([]string, 0, len(candidates))
	args := make([]interface{}, 0, len(candidates)*3)
	for _, snapshot := range candidates {
		parts = append(parts, "(id = ? AND state = ? AND updated_at = ?)")
		args = append(args, snapshot.ID, datamodel.LifeCycleStateREADY, snapshot.UpdatedAt)
	}
	return strings.Join(parts, " OR "), args
}

func (d *DataStoreRepository) GetSnapshotsByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.Snapshot, error) {
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("volume_id", "=", volumeID),
	)
	return d.GetSnapshotsWithCondition(ctx, *filter)
}

// GetReplicationSnapshotsByVolumeID ge the snapshots with name starting with "snapmirror." for a given volume ID
func (d *DataStoreRepository) GetReplicationSnapshotsByVolumeID(ctx context.Context, volumeID int64) ([]*datamodel.Snapshot, error) {
	filter := utils2.CreateFilterWithConditions(
		utils2.NewFilterCondition("volume_id", "=", volumeID),
		utils2.NewFilterCondition("name", "LIKE", "snapmirror.%"))
	db := d.db.ApplyFilter(filter.Apply()).GORM().WithContext(ctx)

	var snapshots []*datamodel.Snapshot
	err := db.Preload("Volume").Preload("Volume.Pool").Preload("Account").Find(&snapshots).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return snapshots, nil
}

func (d *DataStoreRepository) GetSnapshotsByVolumeIDs(ctx context.Context, volumeIDs []int64) ([]*datamodel.Snapshot, error) {
	var snapshots []*datamodel.Snapshot
	db := d.db.GORM().WithContext(ctx)
	err := db.Preload("Volume").Where("volume_id IN ? AND state = ?", volumeIDs, datamodel.LifeCycleStateREADY).Find(&snapshots).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return snapshots, nil
}

// BatchCreateSnapshots adds all new snapshots that are passed as param and returns a list of snapshotUUIDs
func (d *DataStoreRepository) BatchCreateSnapshots(ctx context.Context, newSnapshots []*datamodel.Snapshot, returnCreatedSnapshotUUIDs bool) (_ []string, err error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	snapshotWriteAllowedByVolumeID, err := lockVolumeAndCheckSnapshotWrite(tx, getUniqueSnapshotVolumeIDs(newSnapshots))
	if err != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		return nil, err
	}

	var createdSnapshots []string
	for _, snapshot := range newSnapshots {
		if snapshot == nil {
			continue
		}
		if !snapshotWriteAllowedByVolumeID[snapshot.VolumeID] {
			logger.Info("skipping snapshot: volume not writable", "snapshotName", snapshot.Name, "volumeID", snapshot.VolumeID)
			continue
		}
		snapshot.UUID = utils.RandomUUID()
		snapshot.CreatedAt = time.Now()
		snapshot.UpdatedAt = snapshot.CreatedAt
		snapshot.DeletedAt = nil

		if err = tx.Create(snapshot).Error; err != nil {
			logger.Errorf("Batch snapshot create failed for %s: %v", snapshot.Name, err)
			err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
			return nil, err
		}
		if returnCreatedSnapshotUUIDs {
			createdSnapshots = append(createdSnapshots, snapshot.UUID)
		}
	}

	return createdSnapshots, nil
}

// BatchUpdateSnapshots updates existing snapshot rows in one transaction.
// The volume write guard is not used: this only updates existing rows, and snapshot sync does not set VolumeID.
func (d *DataStoreRepository) BatchUpdateSnapshots(ctx context.Context, snapshots []*datamodel.Snapshot) (err error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	for _, snapshot := range snapshots {
		if snapshot == nil {
			continue
		}
		snapshot.UpdatedAt = time.Now()
		if err = tx.Model(&datamodel.Snapshot{}).
			Where("uuid = ?", snapshot.UUID).
			Updates(snapshot).Error; err != nil {
			logger.Errorf("Batch snapshot update failed for %s: %v", snapshot.Name, err)
			err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
			return err
		}
	}

	return nil
}

// SnapshotVolumeExternalUUID identifies a snapshot row by its volume and ONTAP external UUID.
type SnapshotVolumeExternalUUID struct {
	VolumeID     int64  `gorm:"column:volume_id"`
	ExternalUUID string `gorm:"column:external_uuid"`
}

// ListSnapshotVolumeExternalUUIDs returns volume and external UUID pairs that already exist, including every state and soft-deleted rows. Empty inputs return no pairs.
func (d *DataStoreRepository) ListSnapshotVolumeExternalUUIDs(ctx context.Context, volumeIDs []int64, externalUUIDs []string) ([]SnapshotVolumeExternalUUID, error) {
	if len(volumeIDs) == 0 || len(externalUUIDs) == 0 {
		return nil, nil
	}

	var pairs []SnapshotVolumeExternalUUID
	err := d.db.GORM().WithContext(ctx).Unscoped().
		Model(&datamodel.Snapshot{}).
		Select("volume_id, snapshot_attributes ->> 'external_uuid' AS external_uuid").
		Where("volume_id IN ? AND snapshot_attributes ->> 'external_uuid' IN ?", volumeIDs, externalUUIDs).
		Find(&pairs).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return pairs, nil
}

// getUniqueSnapshotVolumeIDs returns the distinct volume ids from the snapshots, sorted ascending.
func getUniqueSnapshotVolumeIDs(snapshots []*datamodel.Snapshot) []int64 {
	seen := make(map[int64]struct{}, len(snapshots))
	ids := make([]int64, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot == nil {
			continue
		}
		if _, ok := seen[snapshot.VolumeID]; ok {
			continue
		}
		seen[snapshot.VolumeID] = struct{}{}
		ids = append(ids, snapshot.VolumeID)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// lockVolumeAndCheckSnapshotWrite locks volumes in id order and reports whether each is writable.
// Missing, deleting, and deleted volumes are not writable.
func lockVolumeAndCheckSnapshotWrite(tx *gorm.DB, volumeIDs []int64) (map[int64]bool, error) {
	allowed := make(map[int64]bool, len(volumeIDs))
	if len(volumeIDs) == 0 {
		return allowed, nil
	}

	query := tx.Model(&datamodel.Volume{}).
		Select("id", "state").
		Where("id IN ?", volumeIDs).
		Order("id")
	if isPostgresDialect(tx) {
		query = query.Clauses(clause.Locking{Strength: "UPDATE"})
	}

	var volumes []datamodel.Volume
	if err := query.Find(&volumes).Error; err != nil {
		return nil, err
	}

	byID := make(map[int64]datamodel.Volume, len(volumes))
	for _, volume := range volumes {
		byID[volume.ID] = volume
	}
	for _, volumeID := range volumeIDs {
		volume, ok := byID[volumeID]
		if !ok || volume.State == datamodel.LifeCycleStateDeleting || volume.State == datamodel.LifeCycleStateDeleted {
			allowed[volumeID] = false
			continue
		}
		allowed[volumeID] = true
	}
	return allowed, nil
}

// BatchGetSnapshotsByUUIDs retrieves multiple snapshots by their UUIDs
func (d *DataStoreRepository) BatchGetSnapshotsByUUIDs(ctx context.Context, snapshotUUIDs []string) ([]*datamodel.Snapshot, error) {
	db := d.db.GORM().WithContext(ctx)
	var snapshots []*datamodel.Snapshot

	if len(snapshotUUIDs) == 0 {
		return snapshots, nil
	}

	err := db.Preload("Account").Preload("Volume").Preload("Volume.Pool").Where("uuid IN ?", snapshotUUIDs).Find(&snapshots).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return snapshots, nil
}

// GetSnapshotsByTypeAndVolumeID retrieves snapshots by type and volume ID, ordered by creation time (newest first)
func (d *DataStoreRepository) GetSnapshotsByTypeAndVolumeID(ctx context.Context, snapshotType string, volumeID int64) ([]*datamodel.Snapshot, error) {
	db := d.db.GORM().WithContext(ctx)
	var snapshots []*datamodel.Snapshot

	err := db.Preload("Volume").Preload("Account").
		Where("type = ? AND volume_id = ? AND state != ?", snapshotType, volumeID, datamodel.LifeCycleStateError).
		Order("created_at DESC").
		Find(&snapshots).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return snapshots, nil
}
