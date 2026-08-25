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

// isIPRangeBlockUniqueViolation reports whether err is a duplicate-key violation from the partial
// unique index on active intercluster blocks. The typed sentinel is not raised by every driver, so
// the message is checked as well.
func isIPRangeBlockUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique constraint") || strings.Contains(s, "duplicate key")
}

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
		// The overlap check above is a read followed by a write in one transaction, so two
		// concurrent creates can both pass it. idx_ip_ranges_unique_ic_block catches that case at
		// insert time, and it means the same thing as the check: somebody else owns this block.
		if isIPRangeBlockUniqueViolation(err) {
			err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("IP range already exists (concurrent duplicate detected)"))
			return nil, err
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	ir.RegionUUID = region.UUID
	ir.RegionName = region.Name
	ir.AccountUUID = account.UUID
	ir.AccountName = account.Name
	return ir, nil
}

// InterclusterCarveResult is the ip_ranges row claimed under the per-VPC carve lock.
// Inserted is true only when this call created the row; reused/adopted existing rows are false.
type InterclusterCarveResult struct {
	IPRange    *datamodel.IPRange
	CarvedCIDR string
	ParentID   int64
	Inserted   bool
}

func interclusterCarveResultFromExisting(existing *datamodel.IPRange, region *datamodel.Region, account *datamodel.Account, carvedCIDRFallback string) (*InterclusterCarveResult, error) {
	existingCIDR, cidrErr := CidrFromStartEnd(existing.StartIP, existing.EndIP)
	if cidrErr != nil {
		if carvedCIDRFallback != "" {
			existingCIDR = carvedCIDRFallback
		} else {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, cidrErr)
		}
	}
	parentID := int64(0)
	if existing.AddressRangeID.Valid {
		parentID = existing.AddressRangeID.Int64
	}
	existing.RegionUUID = region.UUID
	existing.RegionName = region.Name
	existing.AccountUUID = account.UUID
	existing.AccountName = account.Name
	return &InterclusterCarveResult{IPRange: existing, CarvedCIDR: existingCIDR, ParentID: parentID}, nil
}

// ClaimCarvedInterclusterIPRange serializes carve + insert per VPC so concurrent pool creates
// cannot pick the same first-fit block. The advisory lock is held only for the transaction
// (list reserved → carve → INSERT), not across the GCP patch.
func (d *DataStoreRepository) ClaimCarvedInterclusterIPRange(ctx context.Context, claim InterclusterCarveClaim) (*InterclusterCarveResult, error) {
	if strings.TrimSpace(claim.Network) == "" || claim.AccountUUID == "" || claim.RegionUUID == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("intercluster carve claim requires network, account, and region"))
	}
	if len(claim.Parents) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("no active interclusterLIF address range found"))
	}

	region, err := d.getRegionByUUID(ctx, claim.RegionUUID)
	if err != nil {
		return nil, err
	}
	account, err := d.GetAccountByUUID(ctx, claim.AccountUUID)
	if err != nil {
		return nil, err
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	if tx.Dialector.Name() == "postgres" {
		lockKey := fmt.Sprintf("ic-lif-carve:%s/%s", claim.HostProjectNumber, claim.VPCName)
		if err = tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
				fmt.Errorf("lock intercluster carve for %s: %w", lockKey, err))
		}
	}

	var existing datamodel.IPRange
	findErr := tx.Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL AND account_id = ? AND network = ? AND lif_type = ?", account.ID, claim.Network, LifTypeInterclusterLIF).
		First(&existing).Error
	if findErr == nil {
		return interclusterCarveResultFromExisting(&existing, region, account, "")
	}
	if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, findErr)
	}

	parentIDs := make([]int64, 0, len(claim.Parents))
	candidateCIDRs := make([]string, 0, len(claim.Parents))
	for _, p := range claim.Parents {
		parentIDs = append(parentIDs, p.ID)
		candidateCIDRs = append(candidateCIDRs, p.CIDR)
	}

	var reservedRows []*datamodel.IPRange
	err = tx.Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL AND lif_type = ? AND address_range_id IN ?", LifTypeInterclusterLIF, parentIDs).
		Find(&reservedRows).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	reservedCIDRs := make([]string, 0, len(reservedRows)+len(claim.ExtraReservedCIDRs))
	for _, ir := range reservedRows {
		if ir.StartIP != "" && ir.EndIP != "" {
			cidr, cidrErr := CidrFromStartEnd(ir.StartIP, ir.EndIP)
			if cidrErr == nil {
				reservedCIDRs = append(reservedCIDRs, cidr)
			}
		}
	}
	reservedCIDRs = append(reservedCIDRs, claim.ExtraReservedCIDRs...)

	carvedCIDR, parentCIDR, err := CarveFreeCIDRBlock(candidateCIDRs, reservedCIDRs, claim.RequiredIPs)
	if err != nil {
		return nil, err
	}
	parentID := parentIDForCarvedCIDR(claim.Parents, parentCIDR, carvedCIDR)
	if parentID == 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("carved IC LIF block %s could not be attributed to an active interclusterLIF address range", carvedCIDR))
		return nil, err
	}

	startIP, endIP, mask, err := CidrToStartEndMask(carvedCIDR)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
	}

	network := claim.Network
	ir := &datamodel.IPRange{
		Network:        &network,
		StartIP:        startIP,
		EndIP:          endIP,
		Netmask:        mask,
		LifType:        LifTypeInterclusterLIF,
		AccountUUID:    account.UUID,
		RegionUUID:     region.UUID,
		AddressRangeID: sql.NullInt64{Int64: parentID, Valid: true},
	}
	now := time.Now().UTC()
	ir.UUID = utils.RandomUUID()
	ir.CreatedAt = now
	ir.UpdatedAt = now
	ir.RegionID = sql.NullInt64{Int64: region.ID, Valid: true}
	ir.AccountID = sql.NullInt64{Int64: account.ID, Valid: true}

	var count int64
	overlapQuery := tx.Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL AND region_id = ? AND network = ? AND account_id = ?", region.ID, ir.Network, account.ID)
	if tx.Dialector.Name() == "postgres" {
		overlapQuery = overlapQuery.Where("end_ip::inet >= ?::inet AND start_ip::inet <= ?::inet", ir.StartIP, ir.EndIP)
	} else {
		overlapQuery = overlapQuery.Where("end_ip >= ? AND start_ip <= ?", ir.StartIP, ir.EndIP)
	}
	err = overlapQuery.Count(&count).Error
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
		if isIPRangeBlockUniqueViolation(err) {
			err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("IP range already exists (concurrent duplicate detected)"))
			return nil, err
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	ir.RegionUUID = region.UUID
	ir.RegionName = region.Name
	ir.AccountUUID = account.UUID
	ir.AccountName = account.Name
	return &InterclusterCarveResult{IPRange: ir, CarvedCIDR: carvedCIDR, ParentID: parentID, Inserted: true}, nil
}

// AdoptInterclusterIPRange records a GCP-attached IC LIF secondary in ip_ranges under the per-VPC
// carve lock. If the subnet already has an interclusterLIF row, that row is returned idempotently.
func (d *DataStoreRepository) AdoptInterclusterIPRange(ctx context.Context, claim InterclusterAdoptClaim) (*InterclusterCarveResult, error) {
	carvedCIDR := strings.TrimSpace(claim.CarvedCIDR)
	if carvedCIDR == "" || strings.TrimSpace(claim.Network) == "" || claim.AccountUUID == "" || claim.RegionUUID == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("intercluster adopt requires carved CIDR, network, account, and region"))
	}
	if len(claim.Parents) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("no active interclusterLIF address range found"))
	}

	region, err := d.getRegionByUUID(ctx, claim.RegionUUID)
	if err != nil {
		return nil, err
	}
	account, err := d.GetAccountByUUID(ctx, claim.AccountUUID)
	if err != nil {
		return nil, err
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	if tx.Dialector.Name() == "postgres" {
		lockKey := fmt.Sprintf("ic-lif-carve:%s/%s", claim.HostProjectNumber, claim.VPCName)
		if err = tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", lockKey).Error; err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
				fmt.Errorf("lock intercluster carve for %s: %w", lockKey, err))
		}
	}

	var existing datamodel.IPRange
	findErr := tx.Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL AND account_id = ? AND network = ? AND lif_type = ?", account.ID, claim.Network, LifTypeInterclusterLIF).
		First(&existing).Error
	if findErr == nil {
		return interclusterCarveResultFromExisting(&existing, region, account, carvedCIDR)
	}
	if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, findErr)
	}

	parentID := parentIDForCarvedCIDR(claim.Parents, "", carvedCIDR)
	if parentID == 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("GCP IC LIF block %s is not contained in any active interclusterLIF address range", carvedCIDR))
		return nil, err
	}

	startIP, endIP, mask, err := CidrToStartEndMask(carvedCIDR)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
	}

	network := claim.Network
	ir := &datamodel.IPRange{
		Network:        &network,
		StartIP:        startIP,
		EndIP:          endIP,
		Netmask:        mask,
		LifType:        LifTypeInterclusterLIF,
		AccountUUID:    account.UUID,
		RegionUUID:     region.UUID,
		AddressRangeID: sql.NullInt64{Int64: parentID, Valid: true},
	}
	now := time.Now().UTC()
	ir.UUID = utils.RandomUUID()
	ir.CreatedAt = now
	ir.UpdatedAt = now
	ir.RegionID = sql.NullInt64{Int64: region.ID, Valid: true}
	ir.AccountID = sql.NullInt64{Int64: account.ID, Valid: true}

	var count int64
	overlapQuery := tx.Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL AND region_id = ? AND network = ? AND account_id = ?", region.ID, ir.Network, account.ID)
	if tx.Dialector.Name() == "postgres" {
		overlapQuery = overlapQuery.Where("end_ip::inet >= ?::inet AND start_ip::inet <= ?::inet", ir.StartIP, ir.EndIP)
	} else {
		overlapQuery = overlapQuery.Where("end_ip >= ? AND start_ip <= ?", ir.StartIP, ir.EndIP)
	}
	err = overlapQuery.Count(&count).Error
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
		if isIPRangeBlockUniqueViolation(err) {
			err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("IP range already exists (concurrent duplicate detected)"))
			return nil, err
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	ir.RegionUUID = region.UUID
	ir.RegionName = region.Name
	ir.AccountUUID = account.UUID
	ir.AccountName = account.Name
	return &InterclusterCarveResult{IPRange: ir, CarvedCIDR: carvedCIDR, ParentID: parentID, Inserted: true}, nil
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

// GetDataLIFIPRanges returns data LIF IP ranges scoped to account and network.
func (d *DataStoreRepository) GetDataLIFIPRanges(ctx context.Context, accountUUID, network string) ([]*datamodel.IPRange, error) {
	accountUUID = strings.TrimSpace(accountUUID)
	network = strings.TrimSpace(network)
	if accountUUID == "" || network == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("accountUUID and network are required"))
	}

	account, err := d.GetAccountByUUID(ctx, accountUUID)
	if err != nil {
		return nil, err
	}

	networkVal := network
	var results []*datamodel.IPRange
	err = d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where(&datamodel.IPRange{
			LifType:   LifTypeDataLIF,
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			Network:   &networkVal,
		}).
		Order("id ASC").
		Find(&results).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if len(results) == 0 {
		return results, nil
	}

	region, err := d.getRegionByID(ctx, results[0].RegionID.Int64)
	if err != nil {
		return nil, err
	}
	for _, ipRange := range results {
		ipRange.AccountUUID = account.UUID
		ipRange.AccountName = account.Name
		ipRange.RegionUUID = region.UUID
		ipRange.RegionName = region.Name
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

// DeleteIPRangeByID soft-deletes an IP range by internal ID.
func (d *DataStoreRepository) DeleteIPRangeByID(ctx context.Context, ipRangeID int64) (*datamodel.IPRange, error) {
	if ipRangeID == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("ipRangeID is required"))
	}
	var existing datamodel.IPRange
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where("id = ? AND deleted_at IS NULL", ipRangeID).
		First(&existing).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			ipRangeIDStr := fmt.Sprintf("%d", ipRangeID)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("IPRange", &ipRangeIDStr))
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

// ListInterclusterIPRangesForVPC returns carved interclusterLIF ip_ranges rows linked to
// interclusterLIF address_ranges for the given VPC.
func (d *DataStoreRepository) ListInterclusterIPRangesForVPC(ctx context.Context, hostProjectNumber, vpcName string) ([]*datamodel.IPRange, error) {
	lifType := LifTypeInterclusterLIF
	addressRanges, err := d.ListAddressRanges(ctx, hostProjectNumber, vpcName, nil, &lifType)
	if err != nil {
		return nil, err
	}
	if len(addressRanges) == 0 {
		return nil, nil
	}
	parentIDs := make([]int64, 0, len(addressRanges))
	for _, ar := range addressRanges {
		parentIDs = append(parentIDs, ar.ID)
	}
	var results []*datamodel.IPRange
	err = d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where("deleted_at IS NULL AND lif_type = ? AND address_range_id IN ?", LifTypeInterclusterLIF, parentIDs).
		Find(&results).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return results, nil
}
