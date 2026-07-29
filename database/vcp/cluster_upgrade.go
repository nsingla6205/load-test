package database

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/gorm"
)

// CreateClusterUpgradeJob creates a new cluster upgrade job in the database
func (d *DataStoreRepository) CreateClusterUpgradeJob(ctx context.Context, upgradeJob *datamodel.ClusterUpgradeJob) (*datamodel.ClusterUpgradeJob, error) {
	db := d.db.GORM().WithContext(ctx)
	err := db.Transaction(func(tx *gorm.DB) error {
		if tx.Dialector.Name() == "postgres" {
			if err := tx.Exec(
				"SELECT pg_advisory_xact_lock(hashtextextended(?, 0))",
				upgradeJob.ClusterID,
			).Error; err != nil {
				return fmt.Errorf("lock cluster %s for upgrade job creation: %w", upgradeJob.ClusterID, err)
			}
		}

		var activeJob datamodel.ClusterUpgradeJob
		result := tx.
			Where("cluster_id = ? AND status IN ?", upgradeJob.ClusterID, []string{"PENDING", "IN_PROGRESS"}).
			Order("created_at DESC").
			Limit(1).
			Find(&activeJob)
		if result.Error != nil {
			return fmt.Errorf("check active upgrade job for cluster %s: %w", upgradeJob.ClusterID, result.Error)
		}
		if result.RowsAffected > 0 {
			if activeJob.UUID == upgradeJob.UUID {
				upgradeJob = &activeJob
				return nil
			}
			return customerrors.NewConflictErr(
				fmt.Sprintf("cluster %s already has active upgrade job %s in status %s", upgradeJob.ClusterID, activeJob.UUID, activeJob.Status),
			)
		}

		return tx.Create(upgradeJob).Error
	})
	if err != nil {
		return nil, err
	}
	return upgradeJob, nil
}

// GetClusterUpgradeJobByUUID retrieves a cluster upgrade job by its UUID(including soft-deleted)
func (d *DataStoreRepository) GetClusterUpgradeJobByUUID(ctx context.Context, jobUUID string) (*datamodel.ClusterUpgradeJob, error) {
	var upgradeJob datamodel.ClusterUpgradeJob
	err := d.db.GORM().WithContext(ctx).Unscoped().Where("uuid = ?", jobUUID).First(&upgradeJob).Error
	if err != nil {
		return nil, err
	}
	return &upgradeJob, nil
}

// GetClusterUpgradeJobsByClusterID retrieves all cluster upgrade jobs for a given cluster ID
func (d *DataStoreRepository) GetClusterUpgradeJobsByClusterID(ctx context.Context, clusterID string) ([]*datamodel.ClusterUpgradeJob, error) {
	var upgradeJobs []*datamodel.ClusterUpgradeJob
	err := d.db.GORM().WithContext(ctx).Where("cluster_id = ?", clusterID).Find(&upgradeJobs).Error
	if err != nil {
		return nil, err
	}
	return upgradeJobs, nil
}

// UpdateClusterUpgradeJob updates an existing cluster upgrade job
func (d *DataStoreRepository) UpdateClusterUpgradeJob(ctx context.Context, upgradeJob *datamodel.ClusterUpgradeJob) error {
	err := d.db.GORM().WithContext(ctx).Save(upgradeJob).Error
	if err != nil {
		return err
	}
	return nil
}
