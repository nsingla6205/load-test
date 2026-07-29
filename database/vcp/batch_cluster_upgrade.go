package database

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

// CreateBatchClusterUpgradeJob creates a new batch cluster upgrade job in the database
func (d *DataStoreRepository) CreateBatchClusterUpgradeJob(ctx context.Context, batchJob *datamodel.BatchClusterUpgradeJob) (*datamodel.BatchClusterUpgradeJob, error) {
	err := d.db.GORM().WithContext(ctx).Create(batchJob).Error
	if err != nil {
		return nil, err
	}
	return batchJob, nil
}

// GetBatchClusterUpgradeJobByUUID retrieves a batch cluster upgrade job by its UUID (including soft-deleted)
func (d *DataStoreRepository) GetBatchClusterUpgradeJobByUUID(ctx context.Context, jobUUID string) (*datamodel.BatchClusterUpgradeJob, error) {
	var batchJob datamodel.BatchClusterUpgradeJob
	err := d.db.GORM().WithContext(ctx).Unscoped().Where("uuid = ?", jobUUID).First(&batchJob).Error
	if err != nil {
		return nil, err
	}
	return &batchJob, nil
}

// UpdateBatchClusterUpgradeJob updates an existing batch cluster upgrade job
func (d *DataStoreRepository) UpdateBatchClusterUpgradeJob(ctx context.Context, batchJob *datamodel.BatchClusterUpgradeJob) error {
	return d.db.GORM().WithContext(ctx).Save(batchJob).Error
}

// GetClusterUpgradeJobsByBatchID retrieves all cluster upgrade jobs belonging to a batch upgrade
func (d *DataStoreRepository) GetClusterUpgradeJobsByBatchID(ctx context.Context, batchUpgradeID string) ([]*datamodel.ClusterUpgradeJob, error) {
	var upgradeJobs []*datamodel.ClusterUpgradeJob
	err := d.db.GORM().WithContext(ctx).Unscoped().Where("batch_upgrade_id = ?", batchUpgradeID).Find(&upgradeJobs).Error
	if err != nil {
		return nil, err
	}
	return upgradeJobs, nil
}
