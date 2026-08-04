package backgroundactivities

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type SyncBackupZiZsActivity struct {
	SE database.Storage
}

func (a *SyncBackupZiZsActivity) GetAllBackupVaults(ctx context.Context) ([]*datamodel.BackupVault, error) {
	// Filter for backup vaults in READY state
	conditions := [][]interface{}{
		{"life_cycle_state = ?", datamodel.LifeCycleStateREADY},
	}
	safeRecordHeartbeat(ctx, "listing READY backup vaults from database")
	return a.SE.GetMultipleBackupVaults(ctx, conditions)
}

func (a *SyncBackupZiZsActivity) SyncBucketDetails(ctx context.Context, bucketDetails *datamodel.BucketDetails) (*datamodel.BucketDetails, error) {
	logger := util.GetLogger(ctx)

	if bucketDetails == nil {
		logger.Errorf("BucketDetails parameter is nil")
		return nil, fmt.Errorf("bucket details parameter is required")
	}

	// Use existing tenant project number from bucket details
	tenantProjectNumber := bucketDetails.TenantProjectNumber
	if tenantProjectNumber == "" {
		logger.Errorf("TenantProjectNumber is empty in bucket details for bucket: %s", bucketDetails.BucketName)
		return nil, fmt.Errorf("tenant project number is required but not found in bucket details")
	}
	logger.Infof("Using tenant project: %s for bucket: %s", tenantProjectNumber, bucketDetails.BucketName)

	// Get cloud service
	safeRecordHeartbeat(ctx, fmt.Sprintf("resolving cloud service for tenant project %s", tenantProjectNumber))
	cloudService, err := activities.GetCloudService(ctx)
	if err != nil {
		logger.Errorf("Failed to get cloud service: %v", err)
		return nil, err
	}

	// Get bucket details from GCP API
	logger.Infof("Getting bucket details for bucket: %s in project: %s", bucketDetails.BucketName, tenantProjectNumber)
	safeRecordHeartbeat(ctx, fmt.Sprintf("getting bucket %s details from GCP for project %s", bucketDetails.BucketName, tenantProjectNumber))
	cloudBucketDetails, err := cloudService.GetBucket(ctx, bucketDetails.BucketName)
	if err != nil {
		logger.Errorf("Failed to get bucket details from GCP: %v", err)
		return nil, err
	}
	logger.Infof("Successfully retrieved bucket details for: %s", bucketDetails.BucketName)

	// Update existing bucket details with ZiZs information from GCP
	bucketDetails.SatisfiesPzi = cloudBucketDetails.SatisfiesPzi
	bucketDetails.SatisfiesPzs = cloudBucketDetails.SatisfiesPzs
	logger.Infof("Updated bucket details - satisfiesPzi: %t, satisfiesPzs: %t", bucketDetails.SatisfiesPzi, bucketDetails.SatisfiesPzs)
	return bucketDetails, nil
}

// UpdateBackupVault updates the backup vault in the database with the synced ZiZs information
func (a *SyncBackupZiZsActivity) UpdateBackupVault(ctx context.Context, backupVault *datamodel.BackupVault) error {
	logger := util.GetLogger(ctx)

	if backupVault == nil {
		logger.Errorf("BackupVault parameter is nil")
		return fmt.Errorf("backup vault parameter is required")
	}

	logger.Infof("Updating backup vault %s with synced ZiZs information", backupVault.UUID)

	// Call the storage engine to update the backup vault
	safeRecordHeartbeat(ctx, fmt.Sprintf("updating backup vault %s bucket details in database", backupVault.UUID))
	err := a.SE.UpdateBackupVaultBucketDetails(ctx, backupVault)
	if err != nil {
		logger.Errorf("Failed to update backup vault %s: %v", backupVault.UUID, err)
		return fmt.Errorf("failed to update backup vault %s: %w", backupVault.UUID, err)
	}

	logger.Infof("Successfully updated backup vault %s", backupVault.UUID)
	return nil
}
