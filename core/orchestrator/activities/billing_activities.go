package activities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// BillingActivity handles billing-related operations
type BillingActivity struct {
	SE database.Storage
}

// BillingLedgerInput contains the input parameters for creating a billing ledger entry
type BillingLedgerInput struct {
	ResourceName   string
	ResourceUUID   string
	ResourceType   metadata.ResourceType
	MeasuredType   metadata.MeasuredType
	Size           float64
	ConsumerID     string
	DeploymentName string
}

// CreateBillingLedgerEntry creates a billing ledger entry for any resource type
func (a *BillingActivity) CreateBillingLedgerEntry(ctx context.Context, input BillingLedgerInput) error {
	recordHeartbeat(ctx, fmt.Sprintf("Ledger: Creating billing ledger entry for %s/%s", input.ResourceType, input.MeasuredType))
	logger := util.GetLogger(ctx)

	ledger := &datamodel.BillingLedger{
		BaseModel: datamodel.BaseModel{
			UUID: utils.RandomUUID(),
		},
		ResourceName:   input.ResourceName,
		ResourceUUID:   input.ResourceUUID,
		ResourceType:   input.ResourceType,
		MeasuredType:   input.MeasuredType,
		Quantity:       input.Size,
		ConsumerID:     input.ConsumerID,
		DeploymentName: input.DeploymentName,
	}

	_, err := a.SE.CreateBillingLedger(ctx, ledger)
	if err != nil {
		logger.Errorf("Ledger: Failed to create billing ledger entry for %s/%s - Resource: %s, Error: %v",
			input.ResourceType, input.MeasuredType, input.ResourceUUID, err)
		return err
	}

	return nil
}

// extractConsumerID extracts the consumer ID from an account, falling back to vendor ID
func extractConsumerID(account *datamodel.Account, vendorID string) string {
	if account != nil && account.Name != "" {
		return account.Name
	}
	return vendorID
}

// getPoolResourceType returns the appropriate ResourceType based on whether pool is Regional HA
func getPoolResourceType(pool *datamodel.Pool) metadata.ResourceType {
	if pool.PoolAttributes != nil && pool.PoolAttributes.IsRegionalHA {
		return metadata.VolumePoolRegionalHA
	}
	return metadata.VolumePool
}

// CreatePoolBillingLedger creates billing ledger entries for a pool.
// Creates entries for both ALLOCATED_SIZE and POOL_HOT_TIER_PROVISIONED_SIZE (if AT is enabled).
// Uses VOLUME_POOL_REGIONAL_HA resource type for Regional HA pools, VOLUME_POOL otherwise.
func (a *BillingActivity) CreatePoolBillingLedger(ctx context.Context, pool *datamodel.Pool) error {
	resourceType := getPoolResourceType(pool)

	// Create main pool billing ledger entry (ALLOCATED_SIZE)
	if err := a.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
		ResourceName:   pool.Name,
		ResourceUUID:   pool.UUID,
		ResourceType:   resourceType,
		MeasuredType:   metadata.PoolAllocatedSize,
		Size:           float64(pool.SizeInBytes),
		ConsumerID:     extractConsumerID(pool.Account, pool.VendorID),
		DeploymentName: pool.DeploymentName,
	}); err != nil {
		return err
	}

	// Create hot tier billing ledger entry for AT pools
	if err := a.CreatePoolHotTierBillingLedger(ctx, pool); err != nil {
		return err
	}

	return nil
}

// CreatePoolHotTierBillingLedger creates a billing ledger entry for a pool's hot tier size.
// Skips creating ledger for non-AT (non-Auto Tiering) pools.
// Uses VOLUME_POOL_REGIONAL_HA resource type for Regional HA pools, VOLUME_POOL otherwise.
func (a *BillingActivity) CreatePoolHotTierBillingLedger(ctx context.Context, pool *datamodel.Pool) error {
	// Skip for non-AT pools
	if !pool.AllowAutoTiering || pool.AutoTieringConfig == nil {
		return nil
	}

	resourceType := getPoolResourceType(pool)

	return a.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
		ResourceName:   pool.Name,
		ResourceUUID:   pool.UUID,
		ResourceType:   resourceType,
		MeasuredType:   metadata.PoolHotTierProvisionedSize,
		Size:           float64(pool.AutoTieringConfig.HotTierSizeInBytes),
		ConsumerID:     extractConsumerID(pool.Account, pool.VendorID),
		DeploymentName: pool.DeploymentName,
	})
}

// getVolumeResourceType returns the appropriate ResourceType based on whether volume is Regional HA
func getVolumeResourceType(volume *datamodel.Volume) metadata.ResourceType {
	if volume.VolumeAttributes != nil && volume.VolumeAttributes.IsRegionalHA {
		return metadata.VolumeRegionalHA
	}
	return metadata.Volume
}

// isBackupEnabledVolume returns true if the volume has backup enabled for billing purposes.
func isBackupEnabledVolume(volume *datamodel.Volume) bool {
	return volume.DataProtection != nil &&
		volume.DataProtection.BackupChainBytes != nil &&
		*volume.DataProtection.BackupChainBytes > 0
}

// getVolumeVendorID safely returns the pool vendor ID for a volume.
func getVolumeVendorID(volume *datamodel.Volume) string {
	if volume.Pool != nil {
		return volume.Pool.VendorID
	}
	return ""
}

// getVolumeDeploymentName safely returns the pool deployment name for a volume.
func getVolumeDeploymentName(volume *datamodel.Volume) string {
	if volume.Pool != nil {
		return volume.Pool.DeploymentName
	}
	return ""
}

func (a *BillingActivity) shouldSkipVolumeBackupBilling(ctx context.Context, volume *datamodel.Volume) (bool, error) {
	if volume.DataProtection.BackupVaultID == "" {
		return false, nil
	}
	_, err := a.SE.GetBackupVault(ctx, volume.DataProtection.BackupVaultID)
	if err != nil {
		return false, err
	}
	return false, nil
}

// CreateVolumeBillingLedger creates or updates a billing ledger entry for backup-enabled volume allocated size.
// Deletes existing ledger entries when backup is not enabled or the backup vault cannot be resolved.
// Uses VOLUME_REGIONAL_HA resource type for Regional HA volumes, VOLUME otherwise.
func (a *BillingActivity) CreateVolumeBillingLedger(ctx context.Context, volume *datamodel.Volume) error {
	resourceType := getVolumeResourceType(volume)

	if !isBackupEnabledVolume(volume) {
		return a.DeleteBillingLedgerByMeasuredType(ctx, volume.UUID, resourceType, metadata.BackupEnabledVolumeAllocatedSize)
	}

	skip, err := a.shouldSkipVolumeBackupBilling(ctx, volume)
	if err != nil {
		return err
	}
	if skip {
		return a.DeleteBillingLedgerByMeasuredType(ctx, volume.UUID, resourceType, metadata.BackupEnabledVolumeAllocatedSize)
	}

	return a.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
		ResourceName:   volume.Name,
		ResourceUUID:   volume.UUID,
		ResourceType:   resourceType,
		MeasuredType:   metadata.BackupEnabledVolumeAllocatedSize,
		Size:           float64(volume.SizeInBytes),
		ConsumerID:     extractConsumerID(volume.Account, getVolumeVendorID(volume)),
		DeploymentName: getVolumeDeploymentName(volume),
	})
}

// CreateCrossRegionBackupTransferBillingLedger creates a billing ledger entry capturing the
// cross-region transfer bytes of a newly created backup. It is invoked whenever a backup is
// created, with that backup passed directly (it is the latest backup by definition). It is a
// no-op when the backup is nil, or when its vault is not a cross-region vault (transfer-bytes
// billing only applies to cross-region backups). Uses VOLUME_UUID as the resource so the entry
// is keyed per volume.
func (a *BillingActivity) CreateCrossRegionBackupTransferBillingLedger(ctx context.Context, backup *datamodel.Backup) error {
	logger := util.GetLogger(ctx)

	if backup == nil || backup.Attributes == nil {
		return nil
	}

	// A backup without an associated vault cannot be cross-region; skip the lookup entirely.
	// GetBackupVaultById would otherwise query "id = 0" and return a not-found error.
	if backup.BackupVaultID == 0 {
		logger.Debugf("Ledger: Backup %s for volume %s has no backup vault; skipping transfer billing ledger", backup.UUID, backup.VolumeUUID)
		return nil
	}

	backupVault, err := a.SE.GetBackupVaultById(ctx, backup.BackupVaultID)
	if err != nil {
		return err
	}
	if backupVault == nil || backupVault.BackupVaultType != CrossRegionBackupType {
		logger.Debugf("Ledger: Backup %s for volume %s is not cross-region; skipping transfer billing ledger", backup.UUID, backup.VolumeUUID)
		return nil
	}

	ledger := &datamodel.BillingLedger{
		BaseModel:    datamodel.BaseModel{UUID: utils.RandomUUID()},
		ResourceName: backup.Attributes.VolumeName,
		ResourceUUID: backup.VolumeUUID,
		ResourceType: metadata.Backup,
		MeasuredType: metadata.CbsCrossRegionVolumeBackupTransferBytes,
		Quantity:     float64(backup.Attributes.GetTotalTransferBytes()),
		ConsumerID:   backup.Attributes.AccountIdentifier,
	}
	if _, err := a.SE.CreateBillingLedger(ctx, ledger); err != nil {
		logger.Errorf("Ledger: Failed to create cross-region backup transfer billing ledger for volume %s: %v", backup.VolumeUUID, err)
		return err
	}
	return nil
}

// DeleteVolumeBillingLedger deletes the backup-enabled volume allocated size billing ledger entry.
func (a *BillingActivity) DeleteVolumeBillingLedger(ctx context.Context, volume *datamodel.Volume) error {
	resourceType := getVolumeResourceType(volume)
	return a.DeleteBillingLedgerByMeasuredType(ctx, volume.UUID, resourceType, metadata.BackupEnabledVolumeAllocatedSize)
}

// DeletePoolBillingLedgers deletes pool billing ledger entries (allocated size and hot tier when present).
func (a *BillingActivity) DeletePoolBillingLedgers(ctx context.Context, pool *datamodel.Pool) error {
	resourceType := getPoolResourceType(pool)
	if err := a.DeleteBillingLedgerByMeasuredType(ctx, pool.UUID, resourceType, metadata.PoolAllocatedSize); err != nil {
		return err
	}
	return a.DeleteBillingLedgerByMeasuredType(ctx, pool.UUID, resourceType, metadata.PoolHotTierProvisionedSize)
}

// DeleteBillingLedgerByMeasuredType deletes a billing ledger entry for a resource, resource type, and measured type.
func (a *BillingActivity) DeleteBillingLedgerByMeasuredType(ctx context.Context, resourceUUID string, resourceType metadata.ResourceType, measuredType metadata.MeasuredType) error {
	recordHeartbeat(ctx, "Ledger: Deleting billing ledger entry")
	logger := util.GetLogger(ctx)
	logger.Infof("Ledger: Deleting billing ledger for resource UUID: %s, resource type: %s, measured type: %s", resourceUUID, resourceType, measuredType)
	return a.SE.DeleteBillingLedgersByResourceUUIDAndMeasuredType(ctx, resourceUUID, resourceType, measuredType)
}
