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

// DeleteBillingLedgersByResourceUUID deletes all billing ledger entries for a resource and resource type.
func (a *BillingActivity) DeleteBillingLedgersByResource(ctx context.Context, resourceUUID string, resourceType string) error {
	activity.RecordHeartbeat(ctx, "Deleting billing ledger entries")
	logger := util.GetLogger(ctx)
	logger.Infof("Deleting billing ledger entries for resource UUID: %s, resource type: %s", resourceUUID, resourceType)
	return a.SE.DeleteBillingLedgersByResourceUUID(ctx, resourceUUID, resourceType)
}
