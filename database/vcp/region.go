package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// GetRegionByUUID retrieves a region by its UUID.
func (d *DataStoreRepository) GetRegionByUUID(ctx context.Context, uuid string) (*datamodel.Region, error) {
	var region datamodel.Region
	err := d.db.GORM().WithContext(ctx).
		First(&region, &datamodel.Region{BaseModel: datamodel.BaseModel{UUID: uuid}}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("Region", &uuid))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &region, nil
}

// GetRegionByName retrieves a region by its name.
func (d *DataStoreRepository) GetRegionByName(ctx context.Context, name string) (*datamodel.Region, error) {
	var region datamodel.Region
	err := d.db.GORM().WithContext(ctx).
		First(&region, &datamodel.Region{Name: name}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("Region", &name))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &region, nil
}

// CreateRegion inserts a new region, rejecting duplicate non-deleted names.
func (d *DataStoreRepository) CreateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error) {
	now := time.Now().UTC()
	region.UUID = utils.RandomUUID()
	region.CreatedAt = now
	region.UpdatedAt = now

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var count int64
	if err = tx.Model(&datamodel.Region{}).
		Where("name = ?", region.Name).
		Count(&count).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if count > 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("region name already exists"))
		return nil, err
	}
	if err = tx.Create(region).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}
	return region, nil
}

// ListRegions returns all active regions.
func (d *DataStoreRepository) ListRegions(ctx context.Context) ([]*datamodel.Region, error) {
	var regions []*datamodel.Region
	if err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Region{}).
		Where("deleted_at IS NULL").
		Find(&regions).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return regions, nil
}

// UpdateRegion updates mutable fields of a region.
func (d *DataStoreRepository) UpdateRegion(ctx context.Context, region *datamodel.Region) (*datamodel.Region, error) {
	existing, err := d.GetRegionByUUID(ctx, region.UUID)
	if err != nil {
		return nil, err
	}

	existing.Name = region.Name
	existing.UpdatedAt = time.Now().UTC()
	if err := d.db.GORM().WithContext(ctx).Save(existing).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, fmt.Errorf("region already exists"))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return existing, nil
}
