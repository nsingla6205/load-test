package database

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// ExpertModePoolCapacity represents the total size and volume count for expert mode volumes in a pool
type ExpertModePoolCapacity struct {
	TotalSize   int64
	VolumeCount int64
}

type expertModePoolCapacityRow struct {
	PoolID           int64 `gorm:"column:pool_id"`
	TotalVolumeSize  int64 `gorm:"column:total_volume_size"`
	TotalSharedBytes int64 `gorm:"column:total_shared_bytes"`
	VolumeCount      int64 `gorm:"column:volume_count"`
}

var (
	getMultipleVolumesWithExpertMode = _getMultipleVolumesWithExpertMode
)

// CreateExpertModeVolume creates a new expert mode volume record
func (d *DataStoreRepository) CreateExpertModeVolume(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) (*datamodel.ExpertModeVolumes, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	if expertModeVolume.UUID == "" {
		expertModeVolume.UUID = utils.RandomUUID()
	}

	if err := tx.Create(expertModeVolume).Error; err != nil {
		return nil, err
	}

	return expertModeVolume, nil
}

// GetExpertModePoolUsedCapacityAndVolumeCount calculates effective pool used size and count of all expert mode volumes for a given pool ID.
// Effective used size is max(total volume size - total shared bytes, 0).
func (d *DataStoreRepository) GetExpertModePoolUsedCapacityAndVolumeCount(ctx context.Context, poolID int64) (*ExpertModePoolCapacity, error) {
	totalVolumeSize, volumeCount, err := d.getExpertModePoolTotalVolumeSizeAndCount(ctx, poolID)
	if err != nil {
		return nil, err
	}
	totalSharedBytes, err := d.getExpertModePoolTotalSharedBytes(ctx, poolID)
	if err != nil {
		return nil, err
	}
	effectiveUsed := totalVolumeSize - totalSharedBytes
	if effectiveUsed < 0 {
		effectiveUsed = 0
	}

	return &ExpertModePoolCapacity{
		TotalSize:   effectiveUsed,
		VolumeCount: volumeCount,
	}, nil
}

// GetExpertModePoolsUsedCapacityAndVolumeCount calculates effective used size and
// volume count for each requested pool in one grouped query.
func (d *DataStoreRepository) GetExpertModePoolsUsedCapacityAndVolumeCount(ctx context.Context, poolIDs []int64) (map[int64]*ExpertModePoolCapacity, error) {
	capacities := make(map[int64]*ExpertModePoolCapacity, len(poolIDs))
	for _, poolID := range poolIDs {
		capacities[poolID] = &ExpertModePoolCapacity{}
	}
	if len(poolIDs) == 0 {
		return capacities, nil
	}

	var rows []expertModePoolCapacityRow
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.ExpertModeVolumes{}).
		Select(`
			pool_id,
			COALESCE(SUM(size_in_bytes), 0) AS total_volume_size,
			COALESCE(SUM(COALESCE(shared_bytes, 0)), 0) AS total_shared_bytes,
			COUNT(*) AS volume_count
		`).
		Where("pool_id IN ?", poolIDs).
		Group("pool_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	for _, row := range rows {
		effectiveUsed := row.TotalVolumeSize - row.TotalSharedBytes
		if effectiveUsed < 0 {
			effectiveUsed = 0
		}
		capacities[row.PoolID] = &ExpertModePoolCapacity{
			TotalSize:   effectiveUsed,
			VolumeCount: row.VolumeCount,
		}
	}

	return capacities, nil
}

func (d *DataStoreRepository) getExpertModePoolTotalVolumeSizeAndCount(ctx context.Context, poolID int64) (int64, int64, error) {
	var result struct {
		TotalSize   int64 `gorm:"column:total_size"`
		VolumeCount int64 `gorm:"column:volume_count"`
	}
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.ExpertModeVolumes{}).
		Select("COALESCE(SUM(size_in_bytes), 0) as total_size, COUNT(*) as volume_count").
		Where("pool_id = ?", poolID).
		Scan(&result).Error
	if err != nil {
		return 0, 0, err
	}
	return result.TotalSize, result.VolumeCount, nil
}

func (d *DataStoreRepository) getExpertModePoolTotalSharedBytes(ctx context.Context, poolID int64) (int64, error) {
	var result struct {
		TotalSharedBytes int64 `gorm:"column:total_shared_bytes"`
	}
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.ExpertModeVolumes{}).
		Select("COALESCE(SUM(COALESCE(shared_bytes, 0)), 0) as total_shared_bytes").
		Where("pool_id = ?", poolID).
		Scan(&result).Error
	if err != nil {
		return 0, err
	}
	return result.TotalSharedBytes, nil
}

// GetExpertModeVolumeByNameAndPoolID retrieves an expert mode volume by its name and pool ID
func (d *DataStoreRepository) GetExpertModeVolumeByNameAndPoolID(ctx context.Context, name string, poolID int64) (*datamodel.ExpertModeVolumes, error) {
	return getExpertModeVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.ExpertModeVolumes{
		Name:   name,
		PoolID: poolID,
	})
}

// GetExpertModeVolumeByUUID retrieves an expert mode volume by UUID with details
func (d *DataStoreRepository) GetExpertModeVolumeByUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error) {
	return getExpertModeVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: volumeUUID}})
}

func (d *DataStoreRepository) GetExpertModeVolumeByExternalUUID(ctx context.Context, volumeUUID string) (*datamodel.ExpertModeVolumes, error) {
	return getExpertModeVolumeWithDetails(d.db.GORM().WithContext(ctx), &datamodel.ExpertModeVolumes{ExternalUUID: volumeUUID})
}

func getExpertModeVolumeWithDetails(db *gorm.DB, query *datamodel.ExpertModeVolumes) (*datamodel.ExpertModeVolumes, error) {
	volume := &datamodel.ExpertModeVolumes{}
	err := db.Preload("Account").Preload("Pool").Preload("Svm").First(volume, query).Error
	if err != nil {
		return nil, err
	}
	return volume, nil
}

// UpdateExpertModeVolume updates an expert mode volume
func (d *DataStoreRepository) UpdateExpertModeVolume(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) (*datamodel.ExpertModeVolumes, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	dbVolume, err := getExpertModeVolumeWithDetails(tx, &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: expertModeVolume.UUID}})
	if err != nil {
		return nil, err
	}

	// Update the fields
	dbVolume.Name = expertModeVolume.Name
	dbVolume.SizeInBytes = expertModeVolume.SizeInBytes
	dbVolume.Style = expertModeVolume.Style
	dbVolume.State = expertModeVolume.State
	dbVolume.ExternalUUID = expertModeVolume.ExternalUUID
	dbVolume.VolumeAttributes = expertModeVolume.VolumeAttributes
	dbVolume.SharedBytes = expertModeVolume.SharedBytes

	err = tx.Save(dbVolume).Error
	if err != nil {
		return nil, err
	}

	return dbVolume, nil
}

// DeleteExpertModeVolume soft deletes an expert mode volume by setting DeletedAt and State.
func (d *DataStoreRepository) DeleteExpertModeVolume(ctx context.Context, volumeUUID string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	volume := &datamodel.ExpertModeVolumes{}
	err = tx.Unscoped().First(volume, &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: volumeUUID}}).Error
	if err != nil {
		return err
	}

	if volume.DeletedAt != nil && volume.DeletedAt.Valid {
		return nil
	}

	volume.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	volume.State = datamodel.LifeCycleStateDeleted

	err = tx.Save(volume).Error
	if err != nil {
		return err
	}

	return nil
}

// UpdateExpertModeVolume updates an expert mode volume in the database
func (d *DataStoreRepository) UpdateExpertModeVolumeDataProtection(ctx context.Context, expertModeVolume *datamodel.ExpertModeVolumes) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// Prepare the fields to update - only update BackupConfig
	updateFields := map[string]interface{}{
		"data_protection": expertModeVolume.BackupConfig,
	}

	err = tx.Model(expertModeVolume).
		Where("uuid = ?", expertModeVolume.UUID).
		Updates(updateFields).Error
	if err != nil {
		return err
	}

	return nil
}

func (d *DataStoreRepository) UpdateExpertModeVolumeFields(ctx context.Context, volumeUUID string, updates map[string]interface{}) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	dbVolume, err := getExpertModeVolumeWithDetails(tx, &datamodel.ExpertModeVolumes{ExternalUUID: volumeUUID})
	if err != nil {
		return err
	}
	updates["updated_at"] = time.Now()

	err = tx.Model(&dbVolume).Updates(updates).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return nil
}

// ListExpertModeVolumesByPoolID retrieves all expert mode volumes for a given pool ID
func (d *DataStoreRepository) ListExpertModeVolumesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.ExpertModeVolumes, error) {
	var volumes []*datamodel.ExpertModeVolumes

	// Query the database for all volumes with the given pool ID
	err := d.db.GORM().WithContext(ctx).
		Where("pool_id = ?", poolID).
		Find(&volumes).Error

	if err != nil {
		return nil, err
	}

	return volumes, nil
}

// CountNonDeletedExpertModeVolumesByAccountID returns the number of expert mode volumes that have not been soft-deleted for a given account
func (d *DataStoreRepository) GetActiveExpertModeVolumesCountByAccountID(ctx context.Context, accountID int64) (int64, error) {
	var count int64
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.ExpertModeVolumes{}).
		Where("account_id = ? AND deleted_at IS NULL", accountID).
		Count(&count).Error
	return count, err
}

// GetEligibleExpertModeVolumes retrieves non-deleted expert mode volumes (name, state only) with pagination for eligibility metrics
func (d *DataStoreRepository) GetEligibleExpertModeVolumes(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.ExpertModeVolumes, error) {
	var volumes []*datamodel.ExpertModeVolumes
	err := d.db.ApplyFilter(conditions).GORM().WithContext(ctx).
		Select("name, state").
		Where("deleted_at IS NULL").
		Scopes(dbutils.Paginate(pagination)).
		Find(&volumes).Error
	if err != nil {
		return nil, err
	}
	return volumes, nil
}

func (d *DataStoreRepository) GetMultipleVolumesWithExpertMode(ctx context.Context, conditions [][]interface{}) ([]*datamodel.ExpertModeVolumes, error) {
	return getMultipleVolumesWithExpertMode(d.db.ApplyFilter(conditions).GORM().WithContext(ctx))
}

func _getMultipleVolumesWithExpertMode(db *gorm.DB) ([]*datamodel.ExpertModeVolumes, error) {
	var expertModeVolumes []*datamodel.ExpertModeVolumes
	err := db.Preload("Account").
		Preload("Pool").
		Preload("Pool.ActiveDirectory").
		Preload("Svm").
		Preload("Pool.KmsConfig").
		Find(&expertModeVolumes).Error
	if err != nil {
		return nil, err
	}
	return expertModeVolumes, nil
}

// ListExpertModeVolumesWithPagination retrieves expert mode volumes matching the given conditions with pagination support
func (d *DataStoreRepository) ListExpertModeVolumesWithPagination(ctx context.Context, conditions [][]interface{}, pagination *dbutils.Pagination) ([]*datamodel.ExpertModeVolumes, error) {
	var volumes []*datamodel.ExpertModeVolumes

	err := d.db.ApplyFilter(conditions).GORM().WithContext(ctx).
		Preload("Account").
		Preload("Pool").
		Preload("Svm").
		Scopes(dbutils.Paginate(pagination)).
		Find(&volumes).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return volumes, nil
}
