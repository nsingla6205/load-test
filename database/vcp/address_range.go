package database

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/gorm"
)

const (
	AddressRangeStateCreated  = "CREATED"
	AddressRangeStateInUse    = "IN_USE"
	AddressRangeStateDisabled = "DISABLED"
	AddressRangeStateDeleted  = "DELETED"

	AddressRangeLifTypeDataLIF         = "dataLIF"
	AddressRangeLifTypeInterclusterLIF = "interclusterLIF"
)

// CreateAddressRange inserts a new address range record.
func (d *DataStoreRepository) CreateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	// Duplicate CIDR check
	var count int64
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.AddressRange{}).
		Where("vpc_name = ? AND host_project_number = ? AND address_range_cidr = ? AND deleted_at IS NULL",
			ar.VpcName, ar.HostProjectNumber, ar.AddressRangeCidr).
		Count(&count).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if count > 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("address range with CIDR %s already exists for vpc %s", ar.AddressRangeCidr, ar.VpcName))
	}

	// Duplicate name check
	err = d.db.GORM().WithContext(ctx).
		Model(&datamodel.AddressRange{}).
		Where("vpc_name = ? AND host_project_number = ? AND name = ? AND deleted_at IS NULL",
			ar.VpcName, ar.HostProjectNumber, ar.Name).
		Count(&count).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if count > 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("address range with name %s already exists for vpc %s", ar.Name, ar.VpcName))
	}

	// Multiple interclusterLIF ranges are allowed per VPC, but they must not overlap.
	if ar.LifType == AddressRangeLifTypeInterclusterLIF {
		if err := d.rejectOverlappingInterclusterAddressRange(ctx, ar); err != nil {
			return nil, err
		}
	}

	ar.UUID = utils.RandomUUID()
	now := time.Now().UTC()
	ar.CreatedAt = now
	ar.UpdatedAt = now
	ar.AddressRangeState = AddressRangeStateCreated
	ar.AddressRangeStateDetails = AddressRangeStateCreated

	if err := d.db.GORM().WithContext(ctx).Create(ar).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("address range already exists (concurrent duplicate detected)"))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}
	return ar, nil
}

// rejectOverlappingInterclusterAddressRange returns a conflict error when the
// candidate interclusterLIF CIDR overlaps any existing active IC LIF range for
// the same VPC + host project.
func (d *DataStoreRepository) rejectOverlappingInterclusterAddressRange(ctx context.Context, ar *datamodel.AddressRange) error {
	var existing []*datamodel.AddressRange
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.AddressRange{}).
		Where("vpc_name = ? AND host_project_number = ? AND lif_type = ? AND deleted_at IS NULL",
			ar.VpcName, ar.HostProjectNumber, AddressRangeLifTypeInterclusterLIF).
		Find(&existing).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	for _, other := range existing {
		if addressRangeCIDRsOverlap(ar.AddressRangeCidr, other.AddressRangeCidr) {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("interclusterLIF address range CIDR %s overlaps existing range %s (%s) for vpc %s",
					ar.AddressRangeCidr, other.AddressRangeCidr, other.Name, ar.VpcName))
		}
	}
	return nil
}

func addressRangeCIDRsOverlap(a, b string) bool {
	_, netA, err := net.ParseCIDR(strings.TrimSpace(a))
	if err != nil {
		return false
	}
	_, netB, err := net.ParseCIDR(strings.TrimSpace(b))
	if err != nil {
		return false
	}
	startA, endA, okA := addressRangeCIDRBounds(netA)
	startB, endB, okB := addressRangeCIDRBounds(netB)
	if !okA || !okB {
		return false
	}
	return startA <= endB && startB <= endA
}

func addressRangeCIDRBounds(network *net.IPNet) (uint32, uint32, bool) {
	ip := network.IP.To4()
	if ip == nil {
		return 0, 0, false
	}
	start := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	maskSize, bitsTotal := network.Mask.Size()
	if bitsTotal != 32 || maskSize < 0 || maskSize > 32 {
		return 0, 0, false
	}
	size := uint32(1 << (32 - maskSize))
	return start, start + size - 1, true
}

// GetAddressRange retrieves a single address range by UUID.
func (d *DataStoreRepository) GetAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error) {
	var ar datamodel.AddressRange
	err := d.db.GORM().WithContext(ctx).
		Where("uuid = ? AND deleted_at IS NULL", arID).
		First(&ar).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("AddressRange", &arID))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &ar, nil
}

// ListAddressRanges lists address ranges filtered by hostProjectNumber and/or vpcName.
// Optional arID and lifType narrow the result further.
func (d *DataStoreRepository) ListAddressRanges(ctx context.Context, hostProjectNumber, vpcName string, arID, lifType *string) ([]*datamodel.AddressRange, error) {
	db := d.db.GORM().WithContext(ctx).Model(&datamodel.AddressRange{}).Where("deleted_at IS NULL")
	if hostProjectNumber != "" {
		db = db.Where("host_project_number = ?", hostProjectNumber)
	}
	if vpcName != "" {
		db = db.Where("vpc_name = ?", vpcName)
	}
	if arID != nil && *arID != "" {
		db = db.Where("uuid = ?", *arID)
	}
	if lifType != nil && *lifType != "" {
		db = db.Where("lif_type = ?", *lifType)
	}

	var results []*datamodel.AddressRange
	if err := db.Find(&results).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return results, nil
}

// UpdateAddressRange updates mutable fields (applyRouteAggregation, addressRangeState→DISABLED).
// Name, AddressRangeCidr, and Network are immutable after creation.
func (d *DataStoreRepository) UpdateAddressRange(ctx context.Context, ar *datamodel.AddressRange) (*datamodel.AddressRange, error) {
	existing, err := d.GetAddressRange(ctx, ar.UUID)
	if err != nil {
		return nil, err
	}

	if existing.RouteAggregationApplied {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("route aggregation is already applied, cannot update address range"))
	}

	switch existing.AddressRangeState {
	case AddressRangeStateInUse:
		// Only applyRouteAggregation may change in IN_USE state
		existing.ApplyRouteAggregation = ar.ApplyRouteAggregation
	case AddressRangeStateCreated:
		existing.ApplyRouteAggregation = ar.ApplyRouteAggregation
		if ar.AddressRangeState == AddressRangeStateDisabled {
			existing.AddressRangeState = AddressRangeStateDisabled
			existing.AddressRangeStateDetails = AddressRangeStateDisabled
		}
	default:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("cannot update address range in %s state", existing.AddressRangeState))
	}

	existing.UpdatedAt = time.Now().UTC()
	if err := d.db.GORM().WithContext(ctx).Save(existing).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return existing, nil
}

// UpdateAddressRangeState transitions address range state between CREATED and IN_USE.
func (d *DataStoreRepository) UpdateAddressRangeState(ctx context.Context, arID, state string, routeAggregationApplied *bool) (*datamodel.AddressRange, error) {
	existing, err := d.GetAddressRange(ctx, arID)
	if err != nil {
		return nil, err
	}

	switch state {
	case AddressRangeStateInUse:
		existing.AddressRangeState = AddressRangeStateInUse
		existing.AddressRangeStateDetails = AddressRangeStateInUse
		if routeAggregationApplied != nil && *routeAggregationApplied {
			existing.RouteAggregationApplied = true
			now := time.Now().UTC()
			existing.RouteAggregationAppliedAt = &now
		}
	case AddressRangeStateCreated:
		existing.AddressRangeState = AddressRangeStateCreated
		existing.AddressRangeStateDetails = AddressRangeStateCreated
		existing.RouteAggregationApplied = false
		existing.RouteAggregationAppliedAt = nil
	default:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("invalid address range state %s; must be CREATED or IN_USE", state))
	}

	existing.UpdatedAt = time.Now().UTC()
	if err := d.db.GORM().WithContext(ctx).Save(existing).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return existing, nil
}

// UpdateAddressRangeStateToCreatedIfLastPool atomically transitions a single IN_USE address range
// back to CREATED, but only when no other active pool on the same network has an allocated subnet
// CIDR that falls within the registered range. The check and update execute in a single
// conditional UPDATE with a NOT EXISTS subquery, eliminating the read-check-write race that
// would occur if concurrent pool deletions each read the pool list independently.
//
// Returns (true, nil) when the row was updated, (false, nil) when other pools still use the range
// (no-op), or (false, err) on failure.
func (d *DataStoreRepository) UpdateAddressRangeStateToCreatedIfLastPool(ctx context.Context, arUUID, network, excludePoolUUID, addressRangeCidr string) (bool, error) {
	now := time.Now().UTC()
	result := d.db.GORM().WithContext(ctx).Exec(`
		UPDATE address_ranges
		SET address_range_state = ?,
		    address_range_state_details = ?,
		    route_aggregation_applied = false,
		    route_aggregation_applied_at = NULL,
		    updated_at = ?
		WHERE uuid = ?
		  AND address_range_state = ?
		  AND deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM pools
		    WHERE network = ?
		      AND uuid != ?
		      AND deleted_at IS NULL
		      AND cluster_details->>'allocated_subnet_cidr' != ''
		      AND (cluster_details->>'allocated_subnet_cidr')::inet << ?::inet
		  )`,
		AddressRangeStateCreated, AddressRangeStateCreated, now,
		arUUID, AddressRangeStateInUse,
		network, excludePoolUUID, addressRangeCidr,
	)
	if result.Error != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}
	return result.RowsAffected > 0, nil
}

// MarkAddressRangeInUseByID atomically transitions an address range from CREATED to IN_USE
// using its numeric primary key. It is used by the interclusterLIF carve path, where the
// parent range is identified by ID (ip_ranges.address_range_id) rather than by CIDR
// containment of the pool's primary subnet.
//
// Returns (true, nil) when the row was updated, (false, nil) when the range was already
// IN_USE or is not in a transitionable state (idempotent), or (false, err) on failure.
func (d *DataStoreRepository) MarkAddressRangeInUseByID(ctx context.Context, addressRangeID int64) (bool, error) {
	if addressRangeID <= 0 {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("addressRangeID is required"))
	}
	now := time.Now().UTC()
	result := d.db.GORM().WithContext(ctx).
		Model(&datamodel.AddressRange{}).
		Where("id = ? AND address_range_state = ? AND deleted_at IS NULL",
			addressRangeID, AddressRangeStateCreated).
		Updates(map[string]interface{}{
			"address_range_state":         AddressRangeStateInUse,
			"address_range_state_details": AddressRangeStateInUse,
			"updated_at":                  now,
		})
	if result.Error != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}
	return result.RowsAffected > 0, nil
}

// ReleaseInterclusterAddressRangeIfUnused atomically transitions an IN_USE interclusterLIF
// address range back to CREATED, but only when no active carved ip_ranges row still
// references it. The check and update run as a single conditional UPDATE with a NOT EXISTS
// subquery so that concurrent pool deletions cannot race each other into releasing a range
// that another pool still occupies.
//
// Returns (true, nil) when the row was released, (false, nil) when carved children remain
// or the range was not IN_USE, or (false, err) on failure.
func (d *DataStoreRepository) ReleaseInterclusterAddressRangeIfUnused(ctx context.Context, addressRangeID int64) (bool, error) {
	if addressRangeID <= 0 {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("addressRangeID is required"))
	}
	now := time.Now().UTC()
	result := d.db.GORM().WithContext(ctx).Exec(`
		UPDATE address_ranges
		SET address_range_state = ?,
		    address_range_state_details = ?,
		    route_aggregation_applied = false,
		    route_aggregation_applied_at = NULL,
		    updated_at = ?
		WHERE id = ?
		  AND lif_type = ?
		  AND address_range_state = ?
		  AND deleted_at IS NULL
		  AND NOT EXISTS (
		    SELECT 1 FROM ip_ranges
		    WHERE address_range_id = ?
		      AND deleted_at IS NULL
		  )`,
		AddressRangeStateCreated, AddressRangeStateCreated, now,
		addressRangeID, AddressRangeLifTypeInterclusterLIF, AddressRangeStateInUse,
		addressRangeID,
	)
	if result.Error != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}
	return result.RowsAffected > 0, nil
}

// ResetAddressRangesInUseToCreated bulk-transitions all IN_USE dataLIF address ranges
// for the given VPC to CREATED in a single UPDATE query.
func (d *DataStoreRepository) ResetAddressRangesInUseToCreated(ctx context.Context, hostProjectNumber, vpcName string) error {
	now := time.Now().UTC()
	result := d.db.GORM().WithContext(ctx).
		Model(&datamodel.AddressRange{}).
		Where("host_project_number = ? AND vpc_name = ? AND lif_type = ? AND address_range_state = ? AND deleted_at IS NULL",
			hostProjectNumber, vpcName, AddressRangeLifTypeDataLIF, AddressRangeStateInUse).
		Updates(map[string]interface{}{
			"address_range_state":          AddressRangeStateCreated,
			"address_range_state_details":  AddressRangeStateCreated,
			"route_aggregation_applied":    false,
			"route_aggregation_applied_at": nil,
			"updated_at":                   now,
		})
	if result.Error != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}
	return nil
}

// DeleteAddressRange soft-deletes an address range.
func (d *DataStoreRepository) DeleteAddressRange(ctx context.Context, arID string) (*datamodel.AddressRange, error) {
	existing, err := d.GetAddressRange(ctx, arID)
	if err != nil {
		return nil, err
	}

	if existing.RouteAggregationApplied {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("route aggregation is already applied, cannot delete address range"))
	}
	if existing.AddressRangeState == AddressRangeStateInUse {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("address range is IN_USE, cannot delete"))
	}

	// An interclusterLIF range can still be occupied by carved ip_ranges even if its state
	// was never advanced to IN_USE (e.g. a range carved before the state transition existed).
	// Check for live children directly so a stale state cannot allow an occupied range to be
	// deleted out from under running pools.
	if existing.LifType == AddressRangeLifTypeInterclusterLIF {
		carved, err := d.countActiveIPRangesForAddressRange(ctx, existing.ID)
		if err != nil {
			return nil, err
		}
		if carved > 0 {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("address range %s has %d intercluster LIF subnet(s) carved from it, cannot delete", existing.AddressRangeCidr, carved))
		}
	}

	now := time.Now().UTC()
	existing.AddressRangeState = AddressRangeStateDeleted
	existing.AddressRangeStateDetails = AddressRangeStateDeleted
	existing.UpdatedAt = now
	deletedAt := gorm.DeletedAt{Time: now, Valid: true}
	existing.DeletedAt = &deletedAt

	if err := d.db.GORM().WithContext(ctx).Save(existing).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, err)
	}
	return existing, nil
}

// countActiveIPRangesForAddressRange returns the number of live ip_ranges rows carved from
// the given parent address range.
func (d *DataStoreRepository) countActiveIPRangesForAddressRange(ctx context.Context, addressRangeID int64) (int64, error) {
	var count int64
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where("address_range_id = ? AND deleted_at IS NULL", addressRangeID).
		Count(&count).Error
	if err != nil {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return count, nil
}
