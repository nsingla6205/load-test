package activities

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// BillingActivity handles billing-related operations
type BillingActivity struct {
	SE database.Storage
}

// CreateBillingLedgerInput contains the input parameters for creating a billing ledger entry
type CreateBillingLedgerInput struct {
	ResourceName   string
	ResourceUUID   string
	Size           int64
	ConsumerID     string
	DeploymentName string
}

// newBillingLedgerInput creates a CreateBillingLedgerInput from a pool with the specified size
func newBillingLedgerInput(pool *datamodel.Pool, size int64) CreateBillingLedgerInput {
	// Use Account.Name (project number) as ConsumerID for billing, fall back to VendorID if needed
	consumerID := ""
	if pool.Account != nil {
		consumerID = pool.Account.Name
	}
	if consumerID == "" {
		consumerID = pool.VendorID
	}

	return CreateBillingLedgerInput{
		ResourceName:   pool.Name,
		ResourceUUID:   pool.UUID,
		Size:           size,
		ConsumerID:     consumerID,
		DeploymentName: pool.DeploymentName,
	}
}

// CreateBillingLedger creates a billing ledger entry for a resource
func (a *BillingActivity) CreateBillingLedger(ctx context.Context, input CreateBillingLedgerInput, operation string) error {
	activity.RecordHeartbeat(ctx, "Creating billing ledger entry")
	logger := util.GetLogger(ctx)
	logger.Infof("Creating billing ledger entry for %s - Resource: %s (UUID: %s), Size: %d, ConsumerID: %s, Deployment: %s",
		operation, input.ResourceName, input.ResourceUUID, input.Size, input.ConsumerID, input.DeploymentName)

	ledger := &datamodel.BillingLedger{
		BaseModel: datamodel.BaseModel{
			UUID: utils.RandomUUID(),
		},
		ResourceName:   input.ResourceName,
		ResourceUUID:   input.ResourceUUID,
		Size:           input.Size,
		ConsumerID:     input.ConsumerID,
		DeploymentName: input.DeploymentName,
	}

	createdLedger, err := a.SE.CreateBillingLedger(ctx, ledger)
	if err != nil {
		logger.Errorf("Failed to create billing ledger entry for %s - Resource: %s, Error: %v",
			operation, input.ResourceUUID, err)
		return err
	}

	logger.Infof("Successfully created billing ledger entry for %s - ID: %d, UUID: %s, Resource: %s, Size: %d",
		operation, createdLedger.ID, createdLedger.UUID, input.ResourceUUID, input.Size)
	return nil
}

// CreatePoolBillingLedger creates a billing ledger entry for a pool
func (a *BillingActivity) CreatePoolBillingLedger(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Creating billing ledger entry for pool")
	return a.CreateBillingLedger(ctx, newBillingLedgerInput(pool, pool.SizeInBytes), "pool-creation")
}

// CreatePoolBillingLedgerForUpdate creates a billing ledger entry for a pool update (size change)
func (a *BillingActivity) CreatePoolBillingLedgerForUpdate(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Creating billing ledger entry for pool update")
	return a.CreateBillingLedger(ctx, newBillingLedgerInput(pool, pool.SizeInBytes), "pool-update")
}

// CreatePoolBillingLedgerForDelete creates a billing ledger entry for a pool deletion (size 0)
func (a *BillingActivity) CreatePoolBillingLedgerForDelete(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Creating billing ledger entry for pool deletion")
	return a.CreateBillingLedger(ctx, newBillingLedgerInput(pool, pool.SizeInBytes), "pool-deletion")
}

// DeleteBillingLedgersByResourceUUID deletes all billing ledger entries for a resource
func (a *BillingActivity) DeleteBillingLedgersByResourceUUID(ctx context.Context, resourceUUID string) error {
	activity.RecordHeartbeat(ctx, "Deleting billing ledger entries")
	return a.SE.DeleteBillingLedgersByResourceUUID(ctx, resourceUUID)
}
