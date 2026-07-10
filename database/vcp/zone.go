package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// CreateZone inserts a new zone, rejecting duplicate names within the same non-deleted region.
func (d *DataStoreRepository) CreateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error) {
	region, err := d.GetRegionByUUID(ctx, regionUUID)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	zone.UUID = utils.RandomUUID()
	zone.CreatedAt = now
	zone.UpdatedAt = now
	zone.RegionID = sql.NullInt64{Int64: region.ID, Valid: true}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var count int64
	if err = tx.Model(&datamodel.Zone{}).
		Where("region_id = ? AND name = ?", region.ID, zone.Name).
		Count(&count).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if count > 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("zone name already exists in region"))
		return nil, err
	}
	if err = tx.Create(zone).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}
	return zone, nil
}

// ListZones returns all active zones with their associated regions preloaded.
func (d *DataStoreRepository) ListZones(ctx context.Context) ([]*datamodel.Zone, error) {
	var zones []*datamodel.Zone
	if err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Zone{}).
		Where("deleted_at IS NULL").
		Find(&zones).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return zones, nil
}

// UpdateZone updates name, timezone, and region of an existing zone, rejecting
// duplicate names within the target region via a conditional raw SQL update.
func (d *DataStoreRepository) UpdateZone(ctx context.Context, zone *datamodel.Zone, regionUUID string) (*datamodel.Zone, error) {
	existing, err := d.getZoneByUUID(ctx, zone.UUID)
	if err != nil {
		return nil, err
	}

	region, err := d.GetRegionByUUID(ctx, regionUUID)
	if err != nil {
		return nil, err
	}

	newRegionID := region.ID

	// fast-path: nothing changed
	if existing.Name == zone.Name && existing.Timezone == zone.Timezone && existing.RegionID.Int64 == newRegionID {
		return existing, nil
	}

	existing.Name = zone.Name
	existing.Timezone = zone.Timezone
	existing.RegionID = sql.NullInt64{Int64: newRegionID, Valid: true}
	existing.UpdatedAt = time.Now().UTC()

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var count int64
	if err = tx.Model(&datamodel.Zone{}).
		Where("region_id = ? AND name = ? AND id != ?", newRegionID, existing.Name, existing.ID).
		Count(&count).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if count > 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("zone name already exists in region"))
		return nil, err
	}
	if err = tx.Model(&datamodel.Zone{}).
		Where("id = ?", existing.ID).
		Updates(map[string]interface{}{
			"updated_at": existing.UpdatedAt,
			"name":       existing.Name,
			"timezone":   existing.Timezone,
			"region_id":  newRegionID,
		}).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return existing, nil
}

// GetZoneByRegionName returns an active zone in the given region. PO datapath
// provisioning only needs any zone UUID for the deployment region.
func (d *DataStoreRepository) GetZoneByRegionName(ctx context.Context, regionName string) (*datamodel.Zone, error) {
	region, err := d.GetRegionByName(ctx, regionName)
	if err != nil {
		return nil, err
	}

	var zone datamodel.Zone
	err = d.db.GORM().WithContext(ctx).
		Where("region_id = ? AND deleted_at IS NULL", region.ID).
		First(&zone).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("Zone", &regionName))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &zone, nil
}

// getZoneByUUID retrieves an active zone by UUID.
func (d *DataStoreRepository) getZoneByUUID(ctx context.Context, uuid string) (*datamodel.Zone, error) {
	var zone datamodel.Zone
	err := d.db.GORM().WithContext(ctx).
		Where("uuid = ? AND deleted_at IS NULL", uuid).
		First(&zone).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("Zone", &uuid))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &zone, nil
}
