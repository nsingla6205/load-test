package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

const dataLIFType = LifTypeDataLIF

func dataLifByIPRangeID(ipRangeID int64) *datamodel.Lif {
	return &datamodel.Lif{
		Type:      dataLIFType,
		IPRangeID: sql.NullInt64{Int64: ipRangeID, Valid: true},
	}
}

func dataLifBySvmID(svmID int64) *datamodel.Lif {
	return &datamodel.Lif{
		Type:  dataLIFType,
		SvmID: sql.NullInt64{Int64: svmID, Valid: true},
	}
}

// GetAllocatedIPsInIPRange returns IP addresses already reserved by active LIF
// rows for the range, regardless of LIF type.
func (d *DataStoreRepository) GetAllocatedIPsInIPRange(ctx context.Context, ipRangeID int64) (map[string]bool, error) {
	if ipRangeID <= 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("ipRangeID must be positive"))
	}
	return queryAllocatedIPsInIPRange(ctx, d.db.GORM(), ipRangeID)
}

// queryAllocatedIPsInIPRange executes the "active LIFs by ip_range" scan on
// the supplied gorm handle. Callers inside a transaction (e.g. IC LIF
// allocation with SELECT FOR UPDATE on the range row) pass the tx handle so
// the scan observes the tx's view; the exported wrapper uses the master
// handle.
func queryAllocatedIPsInIPRange(ctx context.Context, db *gorm.DB, ipRangeID int64) (map[string]bool, error) {
	var lifs []*datamodel.Lif
	err := db.WithContext(ctx).
		Model(&datamodel.Lif{}).
		Where("ip_range_id = ? AND deleted_at IS NULL", ipRangeID).
		Find(&lifs).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	allocated := make(map[string]bool, len(lifs))
	for _, lif := range lifs {
		if lif != nil && strings.TrimSpace(lif.IPAddress) != "" {
			allocated[lif.IPAddress] = true
		}
	}
	return allocated, nil
}

// ValidateIPAddressesAvailableInIPRange checks whether count addresses can be allocated from the range.
func (d *DataStoreRepository) ValidateIPAddressesAvailableInIPRange(ctx context.Context, ipRangeUUID string, ipAddressCount int) error {
	ipRange, err := d.GetIPRangeByUUID(ctx, ipRangeUUID)
	if err != nil {
		return err
	}
	return d.validateIPAddressesAvailable(ctx, ipRange, ipAddressCount)
}

func (d *DataStoreRepository) validateIPAddressesAvailable(ctx context.Context, ipRange *datamodel.IPRange, ipAddressCount int) error {
	if ipRange == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("nil IP range"))
	}
	if ipAddressCount <= 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("ipAddressCount must be positive"))
	}

	allocatedIPs, err := d.GetAllocatedIPsInIPRange(ctx, ipRange.ID)
	if err != nil {
		return err
	}

	_, err = commonparams.AllocateIPAddressesFromRange(
		ipRange.StartIP, ipRange.EndIP, ipRange.Gateway, ipRange.Netmask, ipAddressCount, allocatedIPs)
	if err != nil {
		if customerrors.IsUnavailableErr(err) {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, err)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
	}
	return nil
}

// CreateDataLifReservation inserts a data LIF row that reserves an IP within an ip_range.
func (d *DataStoreRepository) CreateDataLifReservation(ctx context.Context, lif *datamodel.Lif) (*datamodel.Lif, error) {
	if err := validateDataLifReservation(lif); err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var existingCount int64
	err = tx.Model(&datamodel.Lif{}).
		Where(&datamodel.Lif{IPRangeID: lif.IPRangeID, IPAddress: lif.IPAddress}).
		Count(&existingCount).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if existingCount > 0 {
		err = customerrors.NewConflictErr("IP address has already been allocated")
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, err)
	}

	lif.UUID = utils.RandomUUID()
	lif.Type = dataLIFType
	lif.CreatedAt = time.Now().UTC()
	lif.UpdatedAt = lif.CreatedAt

	if err = tx.Create(lif).Error; err != nil {
		if isDataLifIPRangeUniqueViolation(err) {
			dupErr := customerrors.NewConflictErr("IP address has already been allocated")
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, dupErr)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	return lif, nil
}

// isDataLifIPRangeUniqueViolation reports duplicate-key errors from the partial
// unique index on (ip_range_id, ip_address) for active data LIF reservations.
func isDataLifIPRangeUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unique constraint") || strings.Contains(s, "duplicate key")
}

func validateDataLifReservation(lif *datamodel.Lif) error {
	if lif == nil {
		return fmt.Errorf("lif must not be nil")
	}
	if strings.TrimSpace(lif.Name) == "" {
		return fmt.Errorf("lif name must not be empty")
	}
	if strings.TrimSpace(lif.IPAddress) == "" {
		return fmt.Errorf("lif IP address must not be empty")
	}
	if lif.AccountID <= 0 {
		return fmt.Errorf("lif account id must be positive")
	}
	if !lif.IPRangeID.Valid || lif.IPRangeID.Int64 <= 0 {
		return fmt.Errorf("lif ip_range_id must be set")
	}
	return nil
}

// GetLifsBySvmID returns active data LIF rows for the given SVM.
func (d *DataStoreRepository) GetLifsBySvmID(ctx context.Context, svmID int64) ([]*datamodel.Lif, error) {
	if svmID <= 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("svmID must be positive"))
	}

	var lifs []*datamodel.Lif
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Lif{}).
		Find(&lifs, dataLifBySvmID(svmID)).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return lifs, nil
}

// DeleteLifsBySvmID soft-deletes all active data LIF rows for the given SVM.
func (d *DataStoreRepository) DeleteLifsBySvmID(ctx context.Context, svmID int64) error {
	if svmID <= 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("svmID must be positive"))
	}

	lifs, err := d.GetLifsBySvmID(ctx, svmID)
	if err != nil {
		return err
	}
	for _, lif := range lifs {
		if err := d.DeleteLif(ctx, lif); err != nil {
			return err
		}
	}
	return nil
}

// AllocateDataLifIPs selects and reserves count data LIF IPs from the given ip_range UUID.
// When reservations include a common svm_id and matching rows already exist, returns them
// without allocating new IPs (idempotent for Temporal activity retries).
func (d *DataStoreRepository) AllocateDataLifIPs(ctx context.Context, ipRangeUUID string, reservations []*datamodel.Lif) ([]*datamodel.Lif, error) {
	if len(reservations) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("at least one reservation is required"))
	}

	ipRange, err := d.GetIPRangeByUUID(ctx, ipRangeUUID)
	if err != nil {
		return nil, err
	}
	if ipRange.LifType != LifTypeDataLIF {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("ip range %s has lif_type %q, expected %q", ipRange.UUID, ipRange.LifType, LifTypeDataLIF))
	}

	if svmID := reservationSvmID(reservations); svmID > 0 {
		existing, getErr := d.GetLifsBySvmID(ctx, svmID)
		if getErr != nil {
			return nil, getErr
		}
		if len(existing) > 0 {
			return existingDataLifReservationsForAllocate(existing, reservations, ipRange.ID)
		}
	}

	allocatedIPs, err := d.GetAllocatedIPsInIPRange(ctx, ipRange.ID)
	if err != nil {
		return nil, err
	}

	addresses, err := commonparams.AllocateIPAddressesFromRange(
		ipRange.StartIP, ipRange.EndIP, ipRange.Gateway, ipRange.Netmask, len(reservations), allocatedIPs)
	if err != nil {
		if customerrors.IsUnavailableErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
	}

	created := make([]*datamodel.Lif, 0, len(reservations))
	for i, reservation := range reservations {
		reservation.IPAddress = addresses[i]
		reservation.IPRangeID = sql.NullInt64{Int64: ipRange.ID, Valid: true}
		reservation.SubnetMask = ipRange.Netmask

		lif, createErr := d.createDataLifReservationWithRetry(ctx, reservation, ipRange.ID, allocatedIPs)
		if createErr != nil {
			return nil, createErr
		}
		created = append(created, lif)
		allocatedIPs[lif.IPAddress] = true
	}
	return created, nil
}

func reservationSvmID(reservations []*datamodel.Lif) int64 {
	var svmID int64
	for _, reservation := range reservations {
		if reservation == nil || !reservation.SvmID.Valid || reservation.SvmID.Int64 <= 0 {
			return 0
		}
		if svmID == 0 {
			svmID = reservation.SvmID.Int64
			continue
		}
		if reservation.SvmID.Int64 != svmID {
			return 0
		}
	}
	return svmID
}

func existingDataLifReservationsForAllocate(
	existing []*datamodel.Lif,
	reservations []*datamodel.Lif,
	ipRangeID int64,
) ([]*datamodel.Lif, error) {
	if len(existing) != len(reservations) {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("inconsistent existing data LIF reservations: found %d active rows, expected %d",
				len(existing), len(reservations)))
	}

	byName := make(map[string]*datamodel.Lif, len(existing))
	for _, lif := range existing {
		if lif == nil {
			continue
		}
		byName[lif.Name] = lif
	}

	matched := make([]*datamodel.Lif, 0, len(reservations))
	for _, want := range reservations {
		if want == nil || strings.TrimSpace(want.Name) == "" {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("inconsistent existing data LIF reservations: requested reservation has no name"))
		}
		got, ok := byName[want.Name]
		if !ok {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("inconsistent existing data LIF reservations: missing reservation for lif %q", want.Name))
		}
		if err := validateExistingDataLifReservation(got, want, ipRangeID); err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, err)
		}
		matched = append(matched, got)
	}
	return matched, nil
}

func validateExistingDataLifReservation(existing, want *datamodel.Lif, ipRangeID int64) error {
	if existing.Type != dataLIFType {
		return fmt.Errorf("existing lif %q has type %q, expected %q", existing.Name, existing.Type, dataLIFType)
	}
	if !existing.IPRangeID.Valid || existing.IPRangeID.Int64 != ipRangeID {
		return fmt.Errorf("existing lif %q is not reserved in ip_range_id %d", existing.Name, ipRangeID)
	}
	if strings.TrimSpace(existing.IPAddress) == "" {
		return fmt.Errorf("existing lif %q has no reserved IP address", existing.Name)
	}
	if want.NodeID > 0 && existing.NodeID != want.NodeID {
		return fmt.Errorf("existing lif %q is linked to node_id %d, expected %d", existing.Name, existing.NodeID, want.NodeID)
	}
	return nil
}

func (d *DataStoreRepository) createDataLifReservationWithRetry(
	ctx context.Context,
	lif *datamodel.Lif,
	ipRangeID int64,
	allocatedIPs map[string]bool,
) (*datamodel.Lif, error) {
	for {
		created, err := d.CreateDataLifReservation(ctx, lif)
		if err == nil {
			return created, nil
		}
		if !customerrors.IsConflictErr(err) {
			return nil, err
		}

		freshAllocated, allocErr := d.GetAllocatedIPsInIPRange(ctx, ipRangeID)
		if allocErr != nil {
			return nil, allocErr
		}
		for ip, taken := range allocatedIPs {
			if taken {
				freshAllocated[ip] = true
			}
		}

		var rangeRow datamodel.IPRange
		if err := d.db.GORM().WithContext(ctx).First(&rangeRow, ipRangeID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("IPRange", nil))
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}

		addresses, allocErr := commonparams.AllocateIPAddressesFromRange(
			rangeRow.StartIP, rangeRow.EndIP, rangeRow.Gateway, rangeRow.Netmask, 1, freshAllocated)
		if allocErr != nil {
			if customerrors.IsUnavailableErr(allocErr) {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, allocErr)
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, allocErr)
		}
		lif.IPAddress = addresses[0]
	}
}
