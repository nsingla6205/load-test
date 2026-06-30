package database

import (
	"context"
	"database/sql"
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

const (
	LifTypeDataLIF         = "dataLIF"
	LifTypeInterclusterLIF = "interclusterLIF"
)

// CreateIPRange inserts a new IP range record
func (d *DataStoreRepository) CreateIPRange(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error) {
	region, err := d.getRegionByUUID(ctx, ir.RegionUUID)
	if err != nil {
		return nil, err
	}

	account, err := d.GetAccountByUUID(ctx, ir.AccountUUID)
	if err != nil {
		return nil, err
	}

	if ir.LifType == "" {
		ir.LifType = LifTypeDataLIF
	}

	if ir.Network == nil || strings.TrimSpace(*ir.Network) == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("IPRanges require a subnet identifier"))
	}

	now := time.Now().UTC()
	ir.UUID = utils.RandomUUID()
	ir.CreatedAt = now
	ir.UpdatedAt = now
	ir.RegionID = sql.NullInt64{Int64: region.ID, Valid: true}
	ir.AccountID = sql.NullInt64{Int64: account.ID, Valid: true}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// Reject overlapping ranges within the same region/network/account
	var count int64
	err = tx.Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL AND region_id = ? AND network = ? AND account_id = ?", region.ID, ir.Network, account.ID).
		Where("end_ip::inet >= ?::inet AND start_ip::inet <= ?::inet", ir.StartIP, ir.EndIP).
		Count(&count).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if count > 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("IP range already exists"))
		return nil, err
	}
	err = tx.Create(ir).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	ir.RegionUUID = region.UUID
	ir.RegionName = region.Name
	ir.AccountUUID = account.UUID
	ir.AccountName = account.Name
	return ir, nil
}

// GetIPRangeByUUID retrieves a single IP range by UUID.
func (d *DataStoreRepository) GetIPRangeByUUID(ctx context.Context, irID string) (*datamodel.IPRange, error) {
	var ir datamodel.IPRange
	err := d.db.GORM().WithContext(ctx).
		Where("uuid = ? AND deleted_at IS NULL", irID).
		First(&ir).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("IPRange", &irID))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &ir, nil
}

// GetIPRanges returns IP ranges, by accountUUID and network.
func (d *DataStoreRepository) GetIPRanges(ctx context.Context, accountUUID, network *string) ([]*datamodel.IPRange, error) {
	db := d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL")
	var account *datamodel.Account
	if accountUUID != nil && *accountUUID != "" {
		var err error
		account, err = d.GetAccountByUUID(ctx, *accountUUID)
		if err != nil {
			return nil, err
		}
		db = db.Where("account_id = ?", account.ID)
	}
	if network != nil && *network != "" {
		db = db.Where("network = ?", *network)
	}

	var results []*datamodel.IPRange
	if err := db.Find(&results).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if len(results) == 0 {
		return results, nil
	}
	region, err := d.getRegionByID(ctx, results[0].RegionID.Int64)
	if err != nil {
		return nil, err
	}
	if account != nil {
		for _, ipRange := range results {
			ipRange.AccountUUID = account.UUID
			ipRange.AccountName = account.Name
			ipRange.RegionUUID = region.UUID
			ipRange.RegionName = region.Name
		}
	}

	return results, nil
}

// getRegionByID returns region for the given id.
func (d *DataStoreRepository) getRegionByID(ctx context.Context, id int64) (*datamodel.Region, error) {
	var region *datamodel.Region
	if err := d.db.GORM().WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", id).
		Find(&region).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return region, nil
}

// getAccountByID returns account for the given id
func (d *DataStoreRepository) getAccountByID(ctx context.Context, id int64) (*datamodel.Account, error) {
	var account *datamodel.Account
	if err := d.db.GORM().WithContext(ctx).
		Where("id = ? AND deleted_at IS NULL", id).
		Find(&account).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return account, nil
}

// UpdateIPRangeNetwork updates the network of an existing IP range.
func (d *DataStoreRepository) UpdateIPRangeNetwork(ctx context.Context, ir *datamodel.IPRange) (*datamodel.IPRange, error) {
	existing, err := d.GetIPRangeByUUID(ctx, ir.UUID)
	if err != nil {
		return nil, err
	}

	existing.Network = ir.Network
	existing.UpdatedAt = time.Now().UTC()

	if err := d.db.GORM().WithContext(ctx).Save(existing).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	var updated datamodel.IPRange
	if err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where("id = ? AND deleted_at IS NULL", existing.ID).
		First(&updated).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("IPRange", &ir.UUID))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	account, err := d.getAccountByID(ctx, updated.AccountID.Int64)
	if err != nil {
		return nil, err
	}
	region, err := d.getRegionByID(ctx, updated.RegionID.Int64)
	if err != nil {
		return nil, err
	}
	updated.AccountUUID = account.UUID
	updated.AccountName = account.Name
	updated.RegionUUID = region.UUID
	updated.RegionName = region.Name

	return &updated, nil
}

// DeleteIPRange soft-deletes an IP range by UUID.
func (d *DataStoreRepository) DeleteIPRange(ctx context.Context, irID string) (*datamodel.IPRange, error) {
	var existing datamodel.IPRange
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where("uuid = ? AND deleted_at IS NULL", irID).
		First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("IPRange", &irID))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	now := time.Now().UTC()
	existing.UpdatedAt = now
	deletedAt := gorm.DeletedAt{Time: now, Valid: true}
	existing.DeletedAt = &deletedAt

	if err := d.db.GORM().WithContext(ctx).Save(&existing).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, err)
	}
	account, err := d.getAccountByID(ctx, existing.AccountID.Int64)
	if err != nil {
		return nil, err
	}
	region, err := d.getRegionByID(ctx, existing.RegionID.Int64)
	if err != nil {
		return nil, err
	}
	existing.AccountUUID = account.UUID
	existing.AccountName = account.Name
	existing.RegionUUID = region.UUID
	existing.RegionName = region.Name

	return &existing, nil
}

// getRegionByUUID retrieves a region by its UUID.
func (d *DataStoreRepository) getRegionByUUID(ctx context.Context, regionUUID string) (*datamodel.Region, error) {
	var region datamodel.Region
	err := d.db.GORM().WithContext(ctx).
		Where("uuid = ? AND deleted_at IS NULL", regionUUID).
		First(&region).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("Region", &regionUUID))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &region, nil
}
