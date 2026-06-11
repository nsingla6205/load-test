package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// CreateBillingLedger creates a new billing ledger entry for a resource.
// If an active entry already exists for the same resource_uuid, resource_type, and
// measured_type with an identical quantity, it is left untouched and returned as-is.
// Otherwise the previous entry is deleted and a new one is created.
func (d *DataStoreRepository) CreateBillingLedger(ctx context.Context, ledger *datamodel.BillingLedger) (*datamodel.BillingLedger, error) {
	logger := util.GetLogger(ctx)

	if ledger.UUID == "" {
		ledger.UUID = utils.RandomUUID()
	}

	// Replace any existing entry for this resource and measured type, but only
	// when the quantity actually changed. If the current active entry already
	// reflects the same quantity, leave it untouched to avoid churning the
	// ledger (and emitting spurious billing changes) for no-op updates.
	if ledger.ResourceUUID != "" {
		existing, err := d.getActiveBillingLedger(ctx, ledger.ResourceUUID, ledger.ResourceType, ledger.MeasuredType)
		if err != nil {
			return nil, err
		}
		if existing != nil && existing.Quantity == ledger.Quantity {
			logger.Infof("Skipping billing ledger creation; active entry unchanged: UUID=%s, ResourceUUID=%s, ResourceType=%s, MeasuredType=%s, Quantity=%v",
				existing.UUID, existing.ResourceUUID, existing.ResourceType, existing.MeasuredType, existing.Quantity)
			return existing, nil
		}
		if err := d.DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx, ledger.ResourceUUID, ledger.ResourceType, ledger.MeasuredType); err != nil {
			return nil, err
		}
	}

	ledger.CreatedAt = time.Now()
	ledger.UpdatedAt = ledger.CreatedAt

	err := d.db.GORM().WithContext(ctx).Create(ledger).Error
	if err != nil {
		logger.Errorf("Failed to create billing ledger entry: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	logger.Infof("Created billing ledger entry: ID=%d, UUID=%s, ResourceName=%s, ResourceUUID=%s, ResourceType=%s, MeasuredType=%s, Size=%d",
		ledger.ID, ledger.UUID, ledger.ResourceName, ledger.ResourceUUID, ledger.ResourceType, ledger.MeasuredType, ledger.Quantity)

	return ledger, nil
}

// GetBillingLedgersByResourceUUID retrieves billing ledger entries for a specific resource
func (d *DataStoreRepository) GetBillingLedgersByResourceUUID(ctx context.Context, resourceUUID string) ([]*datamodel.BillingLedger, error) {
	logger := util.GetLogger(ctx)

	var ledgers []*datamodel.BillingLedger
	err := d.db.GORM().WithContext(ctx).
		Where("resource_uuid = ?", resourceUUID).
		Order("created_at DESC").
		Find(&ledgers).Error
	if err != nil {
		logger.Errorf("Failed to get billing ledger entries for resource %s: %v", resourceUUID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return ledgers, nil
}

// getActiveBillingLedger returns the current (non-deleted) billing ledger entry for a
// resource, resource type, and measured type, or nil if none exists.
func (d *DataStoreRepository) getActiveBillingLedger(ctx context.Context, resourceUUID string, resourceType metadata.ResourceType, measuredType metadata.MeasuredType) (*datamodel.BillingLedger, error) {
	var ledger datamodel.BillingLedger
	err := d.db.GORM().WithContext(ctx).
		Where("resource_uuid = ?", resourceUUID).
		Where("resource_type = ?", resourceType).
		Where("measured_type = ?", measuredType).
		Where("deleted_at IS NULL").
		First(&ledger).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		util.GetLogger(ctx).Errorf("Failed to get active billing ledger for resource %s (resource_type: %s, measured_type: %s): %v",
			resourceUUID, resourceType, measuredType, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &ledger, nil
}

// DeleteBillingLedgersByResourceUUIDAndMeasuredType deletes billing ledger entries for a resource, resource type, and measured type.
func (d *DataStoreRepository) DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx context.Context, resourceUUID string, resourceType metadata.ResourceType, measuredType metadata.MeasuredType) error {
	logger := util.GetLogger(ctx)

	if resourceUUID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("resourceUUID cannot be empty"))
	}

	deleteResult := d.db.GORM().WithContext(ctx).
		Where("resource_uuid = ?", resourceUUID).
		Where("resource_type = ?", resourceType).
		Where("measured_type = ?", measuredType).
		Delete(&datamodel.BillingLedger{})
	if deleteResult.Error != nil {
		logger.Errorf("Failed to delete billing ledger entries for resource %s (resource_type: %s, measured_type: %s): %v",
			resourceUUID, resourceType, measuredType, deleteResult.Error)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, deleteResult.Error)
	}

	logger.Infof("Deleted %d billing ledger entries for resource %s (resource_type: %s, measured_type: %s)",
		deleteResult.RowsAffected, resourceUUID, resourceType, measuredType)
	return nil
}
