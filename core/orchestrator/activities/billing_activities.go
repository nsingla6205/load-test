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

// CreatePoolBillingLedger creates a billing ledger entry for a pool (ALLOCATED_SIZE)
func (a *BillingActivity) CreatePoolBillingLedger(ctx context.Context, pool *datamodel.Pool) error {
	return a.CreateBillingLedgerEntry(ctx, BillingLedgerInput{
		ResourceName:   pool.Name,
		ResourceUUID:   pool.UUID,
		ResourceType:   metadata.VolumePool,
		MeasuredType:   metadata.PoolAllocatedSize,
		Size:           float64(pool.SizeInBytes),
		ConsumerID:     extractConsumerID(pool.Account, pool.VendorID),
		DeploymentName: pool.DeploymentName,
	})
}

// CreatePoolBillingLedgerForUpdate creates a billing ledger entry for a pool update
func (a *BillingActivity) CreatePoolBillingLedgerForUpdate(ctx context.Context, pool *datamodel.Pool) error {
	return a.CreatePoolBillingLedger(ctx, pool)
}

// CreatePoolBillingLedgerForDelete creates a billing ledger entry for pool deletion
func (a *BillingActivity) CreatePoolBillingLedgerForDelete(ctx context.Context, pool *datamodel.Pool) error {
	return a.CreatePoolBillingLedger(ctx, pool)
}

// DeleteBillingLedgersByResourceUUID deletes all billing ledger entries for a resource
func (a *BillingActivity) DeleteBillingLedgersByResourceUUID(ctx context.Context, resourceUUID string) error {
	activity.RecordHeartbeat(ctx, "Deleting billing ledger entries")
	return a.SE.DeleteBillingLedgersByResourceUUID(ctx, resourceUUID)
}
