package activities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
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
	activity.RecordHeartbeat(ctx, fmt.Sprintf("Creating billing ledger entry for %s/%s", input.ResourceType, input.MeasuredType))
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
		logger.Errorf("Failed to create billing ledger entry for %s/%s - Resource: %s, Error: %v",
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
		util.GetLogger(ctx).Errorf(
			"Failed to get backup vault %s for volume %s billing eligibility: %v",
			volume.DataProtection.BackupVaultID, volume.UUID, err,
		)
		return true, nil
	}
	return false, nil
}

// CreateVolumeBillingLedger creates or updates a billing ledger entry for backup-enabled volume allocated size.
// Deletes existing ledger entries when backup is not enabled or the backup vault cannot be resolved.
// Uses VOLUME_REGIONAL_HA resource type for Regional HA volumes, VOLUME otherwise.
func (a *BillingActivity) CreateVolumeBillingLedger(ctx context.Context, volume *datamodel.Volume) error {
	resourceType := getVolumeResourceType(volume)

	if !isBackupEnabledVolume(volume) {
		return a.DeleteBillingLedgersByResource(ctx, volume.UUID, string(resourceType))
	}

	skip, err := a.shouldSkipVolumeBackupBilling(ctx, volume)
	if err != nil {
		return err
	}
	if skip {
		return a.DeleteBillingLedgersByResource(ctx, volume.UUID, string(resourceType))
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

// DeleteVolumeBillingLedger deletes all billing ledger entries for a volume.
func (a *BillingActivity) DeleteVolumeBillingLedger(ctx context.Context, volume *datamodel.Volume) error {
	return a.DeleteBillingLedgersByResource(ctx, volume.UUID, string(getVolumeResourceType(volume)))
}

// DeleteBillingLedgersByResourceUUID deletes all billing ledger entries for a resource and resource type.
func (a *BillingActivity) DeleteBillingLedgersByResource(ctx context.Context, resourceUUID string, resourceType string) error {
	activity.RecordHeartbeat(ctx, "Deleting billing ledger entries")
	logger := util.GetLogger(ctx)
	logger.Infof("Deleting billing ledger entries for resource UUID: %s, resource type: %s", resourceUUID, resourceType)
	return a.SE.DeleteBillingLedgersByResourceUUID(ctx, resourceUUID, resourceType)
}
