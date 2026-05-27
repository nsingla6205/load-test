package database

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// CreateBillingLedger creates a new billing ledger entry for a resource.
// It first deletes any previous entries with the same resource_uuid and resource_type to maintain only the latest state.
func (d *DataStoreRepository) CreateBillingLedger(ctx context.Context, ledger *datamodel.BillingLedger) (*datamodel.BillingLedger, error) {
	logger := util.GetLogger(ctx)

	if ledger.UUID == "" {
		ledger.UUID = utils.RandomUUID()
	}

	ledger.CreatedAt = time.Now()
	ledger.UpdatedAt = ledger.CreatedAt

	db := d.db.GORM().WithContext(ctx)

	// Delete previous entries with the same resource_uuid and resource_type
	if ledger.ResourceUUID != "" {
		deleteResult := db.Where("resource_uuid = ? AND resource_type = ?", ledger.ResourceUUID, ledger.ResourceType).Delete(&datamodel.BillingLedger{})
		if deleteResult.Error != nil {
			logger.Errorf("Failed to delete previous billing ledger entries for resource %s (resource_type: %s): %v", ledger.ResourceUUID, ledger.ResourceType, deleteResult.Error)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, deleteResult.Error)
		}
		if deleteResult.RowsAffected > 0 {
			logger.Infof("Deleted %d previous billing ledger entries for resource %s (resource_type: %s)", deleteResult.RowsAffected, ledger.ResourceUUID, ledger.ResourceType)
		}
	}

	err := db.Create(ledger).Error
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

// GetLatestBillingLedgerByResourceUUID retrieves the most recent billing ledger entry for a resource
func (d *DataStoreRepository) GetLatestBillingLedgerByResourceUUID(ctx context.Context, resourceUUID string) (*datamodel.BillingLedger, error) {
	logger := util.GetLogger(ctx)

	var ledger datamodel.BillingLedger
	err := d.db.GORM().WithContext(ctx).
		Where("resource_uuid = ?", resourceUUID).
		Order("created_at DESC").
		First(&ledger).Error
	if err != nil {
		logger.Errorf("Failed to get latest billing ledger entry for resource %s: %v", resourceUUID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return &ledger, nil
}

// DeleteBillingLedgersByResourceUUID deletes all billing ledger entries for a specific resource and resource type.
// This should be called when a pool is permanently deleted.
func (d *DataStoreRepository) DeleteBillingLedgersByResourceUUID(ctx context.Context, resourceUUID string, resourceType string) error {
	logger := util.GetLogger(ctx)

	if resourceUUID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, fmt.Errorf("resourceUUID cannot be empty"))
	}

	deleteResult := d.db.GORM().WithContext(ctx).
		Where("resource_uuid = ?", resourceUUID).
		Where("resource_type = ?", resourceType).
		Delete(&datamodel.BillingLedger{})
	if deleteResult.Error != nil {
		logger.Errorf("Failed to delete billing ledger entries for resource %s (resource_type: %s): %v", resourceUUID, resourceType, deleteResult.Error)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, deleteResult.Error)
	}

	logger.Infof("Deleted %d billing ledger entries for resource %s (resource_type: %s)", deleteResult.RowsAffected, resourceUUID, resourceType)
	return nil
}
