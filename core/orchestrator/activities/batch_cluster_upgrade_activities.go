package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	factorycommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

// BatchClusterUpgradeActivity holds activities for batch cluster upgrade operations
type BatchClusterUpgradeActivity struct {
	SE database.Storage
}

// BatchUpgradeWaveCluster identifies one cluster to prepare for a batch upgrade wave.
type BatchUpgradeWaveCluster struct {
	SelectedCluster models.SelectedCluster `json:"selectedCluster"`
	JobID           string                 `json:"jobId"`
}

// PrepareBatchUpgradeWaveParams contains the clusters and job attributes for one wave.
type PrepareBatchUpgradeWaveParams struct {
	BatchUpgradeID     string                    `json:"batchUpgradeId"`
	Clusters           []BatchUpgradeWaveCluster `json:"clusters"`
	TargetVersion      string                    `json:"targetVersion"`
	VSABuildImage      string                    `json:"vsaBuildImage"`
	MediatorBuildImage string                    `json:"mediatorBuildImage"`
	ForceUpgrade       bool                      `json:"forceUpgrade"`
}

// PreparedClusterUpgrade contains the pool and job prepared for a child workflow.
type PreparedClusterUpgrade struct {
	ClusterID string          `json:"clusterId"`
	JobID     string          `json:"jobId"`
	Pool      *datamodel.Pool `json:"pool,omitempty"`
	Error     string          `json:"error,omitempty"`
}

// PrepareBatchUpgradeWaveActivity fetches all pools and creates all child jobs for one wave.
func (a *BatchClusterUpgradeActivity) PrepareBatchUpgradeWaveActivity(
	ctx context.Context,
	params *PrepareBatchUpgradeWaveParams,
) ([]PreparedClusterUpgrade, error) {
	logger := util.GetLogger(ctx)
	poolIDs := make([]string, 0, len(params.Clusters))
	for _, cluster := range params.Clusters {
		poolIDs = append(poolIDs, cluster.SelectedCluster.PoolID)
	}

	pools, err := a.SE.GetPoolsByUUIDs(ctx, poolIDs)
	if err != nil {
		logger.Error("Failed to get pools for batch upgrade wave", "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("get pools for batch upgrade wave: %w", err))
	}
	poolsByUUID := make(map[string]*datamodel.Pool, len(pools))
	for _, pool := range pools {
		if pool != nil {
			poolsByUUID[pool.UUID] = pool
		}
	}

	prepared := make([]PreparedClusterUpgrade, len(params.Clusters))
	for i, waveCluster := range params.Clusters {
		cluster := waveCluster.SelectedCluster
		prepared[i] = PreparedClusterUpgrade{
			ClusterID: cluster.ClusterID,
			JobID:     waveCluster.JobID,
		}

		pool := poolsByUUID[cluster.PoolID]
		if pool == nil {
			prepared[i].Error = fmt.Sprintf("pool %s not found for batch cluster upgrade", cluster.PoolID)
			continue
		}
		if !commonparams.IsPoolStateEligibleForClusterUpgrade(pool.State) {
			prepared[i].Error = fmt.Sprintf(
				"pool %s must be in READY or DISABLED state for upgrade; current state is %s",
				cluster.PoolID,
				pool.State,
			)
			continue
		}

		activeJob, checkErr := factorycommon.CheckActiveUpgradeJob(ctx, a.SE, pool.UUID)
		if checkErr != nil {
			prepared[i].Error = fmt.Sprintf("check active upgrade jobs for pool %s: %v", cluster.PoolID, checkErr)
			continue
		}
		if activeJob != nil {
			if isExpectedBatchChildUpgradeJob(activeJob, params.BatchUpgradeID, waveCluster) {
				logger.Info("Batch child upgrade job already prepared",
					"batchUpgradeID", params.BatchUpgradeID,
					"clusterId", cluster.ClusterID,
					"jobUUID", waveCluster.JobID)
				prepared[i].Pool = pool
				continue
			}
			prepared[i].Error = fmt.Sprintf(
				"pool %s already has active upgrade job %s in status %s",
				cluster.PoolID,
				activeJob.UUID,
				activeJob.Status,
			)
			continue
		}

		upgradeJob := newBatchChildUpgradeJob(params, waveCluster)
		if _, createErr := a.SE.CreateClusterUpgradeJob(ctx, upgradeJob); createErr != nil {
			existingJob, getErr := a.SE.GetClusterUpgradeJobByUUID(ctx, waveCluster.JobID)
			if getErr == nil && isExpectedBatchChildUpgradeJob(existingJob, params.BatchUpgradeID, waveCluster) {
				logger.Info("Batch child upgrade job was created concurrently",
					"batchUpgradeID", params.BatchUpgradeID,
					"clusterId", cluster.ClusterID,
					"jobUUID", waveCluster.JobID)
				prepared[i].Pool = pool
				continue
			}
			prepared[i].Error = fmt.Sprintf("create child upgrade job for pool %s: %v", cluster.PoolID, createErr)
			continue
		}
		prepared[i].Pool = pool
	}

	return prepared, nil
}

func isExpectedBatchChildUpgradeJob(
	job *datamodel.ClusterUpgradeJob,
	batchUpgradeID string,
	waveCluster BatchUpgradeWaveCluster,
) bool {
	if job == nil || job.BatchUpgradeID == nil {
		return false
	}
	cluster := waveCluster.SelectedCluster
	return job.UUID == waveCluster.JobID &&
		*job.BatchUpgradeID == batchUpgradeID &&
		job.ClusterID == cluster.ClusterID &&
		job.PoolID == cluster.PoolID
}

func newBatchChildUpgradeJob(
	params *PrepareBatchUpgradeWaveParams,
	waveCluster BatchUpgradeWaveCluster,
) *datamodel.ClusterUpgradeJob {
	cluster := waveCluster.SelectedCluster
	return &datamodel.ClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{
			UUID: waveCluster.JobID,
		},
		ClusterID:          cluster.ClusterID,
		PoolID:             cluster.PoolID,
		TargetVersion:      params.TargetVersion,
		CurrentVersion:     cluster.CurrentVersion,
		VSABuildImage:      params.VSABuildImage,
		MediatorBuildImage: params.MediatorBuildImage,
		Status:             string(models.UpgradeStatusPending),
		BatchUpgradeID:     &params.BatchUpgradeID,
		ForceUpgrade:       params.ForceUpgrade,
	}
}

// GetPoolForUpgradeActivity gets the complete pool record required by a cluster upgrade workflow.
func (a *BatchClusterUpgradeActivity) GetPoolForUpgradeActivity(ctx context.Context, poolUUID string) (*datamodel.Pool, error) {
	logger := util.GetLogger(ctx)
	logger.Info("Getting pool for batch cluster upgrade", "poolUUID", poolUUID)

	pool, err := a.SE.GetPoolByUUID(ctx, poolUUID)
	if err != nil {
		logger.Error("Failed to get pool for batch cluster upgrade", "poolUUID", poolUUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(fmt.Errorf("get pool %s for batch cluster upgrade: %w", poolUUID, err))
	}
	if pool == nil {
		err := fmt.Errorf("pool %s not found for batch cluster upgrade", poolUUID)
		logger.Error("Failed to get pool for batch cluster upgrade", "poolUUID", poolUUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if !commonparams.IsPoolStateEligibleForClusterUpgrade(pool.State) {
		err := fmt.Errorf("pool %s must be in READY or DISABLED state for upgrade; current state is %s", poolUUID, pool.State)
		logger.Error("Pool is no longer eligible for batch cluster upgrade", "poolUUID", poolUUID, "state", pool.State)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	activeJob, err := factorycommon.CheckActiveUpgradeJob(ctx, a.SE, pool.UUID)
	if err != nil {
		logger.Error("Failed to check active upgrade jobs before batch child start", "poolUUID", poolUUID, "error", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("check active upgrade jobs for pool %s: %w", poolUUID, err),
		)
	}
	if activeJob != nil {
		err := fmt.Errorf("pool %s already has active upgrade job %s in status %s", poolUUID, activeJob.UUID, activeJob.Status)
		logger.Error("Pool already has an active upgrade job before batch child start",
			"poolUUID", poolUUID, "activeJobID", activeJob.UUID, "activeStatus", activeJob.Status)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return pool, nil
}

// UpdateBatchJobStatusActivity updates the status of a batch cluster upgrade job
func (a *BatchClusterUpgradeActivity) UpdateBatchJobStatusActivity(ctx context.Context, batchJobID, status, errorMessage string) error {
	logger := util.GetLogger(ctx)
	logger.Info("Updating batch cluster upgrade job status", "batchJobID", batchJobID, "status", status)

	if status == string(models.UpgradeStatusInProgress) {
		return claimPendingUpgradeJob(ctx, a.SE, upgradeJobRef{
			Kind:   database.UpgradeJobKindBatch,
			UUID:   batchJobID,
			LogKey: "batchJobID",
			Label:  "batch cluster upgrade job",
		})
	}

	batchJob, err := a.SE.GetBatchClusterUpgradeJobByUUID(ctx, batchJobID)
	if err != nil {
		logger.Error("Failed to get batch cluster upgrade job", "batchJobID", batchJobID, "error", err)
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return upgradeJobNotFoundError(fmt.Errorf("batch cluster upgrade job %s not found: %w", batchJobID, err))
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if batchJob == nil {
		err := fmt.Errorf("batch cluster upgrade job %s not found", batchJobID)
		logger.Error("Failed to get batch cluster upgrade job", "batchJobID", batchJobID, "error", err)
		return upgradeJobNotFoundError(err)
	}

	batchJob.Status = status
	batchJob.UpdatedAt = time.Now()

	batchJob.ErrorDetails = nil
	if errorMessage != "" {
		batchJob.ErrorDetails = &datamodel.UpgradeErrorDetails{
			ErrorCode:    "BATCH_UPGRADE_FAILED",
			ErrorMessage: errorMessage,
			ErrorType:    "BATCH_UPGRADE_ERROR",
			Retryable:    false,
		}
	}

	if status == string(models.UpgradeStatusCompleted) || status == string(models.UpgradeStatusFailed) {
		now := time.Now()
		batchJob.CompletedAt = &now
		batchJob.DeletedAt = &gorm.DeletedAt{Time: now, Valid: true}
	}

	if err := a.SE.UpdateBatchClusterUpgradeJob(ctx, batchJob); err != nil {
		logger.Error("Failed to update batch cluster upgrade job", "batchJobID", batchJobID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Successfully updated batch cluster upgrade job status", "batchJobID", batchJobID, "status", status)
	return nil
}

// CompleteBatchJobActivity updates the batch job with final results
func (a *BatchClusterUpgradeActivity) CompleteBatchJobActivity(ctx context.Context, batchJobID, status, errorMessage string, succeededCount, failedCount, skippedCount int) error {
	logger := util.GetLogger(ctx)
	logger.Info("Completing batch cluster upgrade job",
		"batchJobID", batchJobID,
		"status", status,
		"succeeded", succeededCount,
		"failed", failedCount,
		"skipped", skippedCount)

	batchJob, err := a.SE.GetBatchClusterUpgradeJobByUUID(ctx, batchJobID)
	if err != nil {
		logger.Error("Failed to get batch cluster upgrade job", "batchJobID", batchJobID, "error", err)
		if vsaerrors.Is(err, gorm.ErrRecordNotFound) {
			return upgradeJobNotFoundError(fmt.Errorf("batch cluster upgrade job %s not found: %w", batchJobID, err))
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if batchJob == nil {
		err := fmt.Errorf("batch cluster upgrade job %s not found", batchJobID)
		logger.Error("Failed to get batch cluster upgrade job", "batchJobID", batchJobID, "error", err)
		return upgradeJobNotFoundError(err)
	}

	now := time.Now()
	batchJob.Status = status
	batchJob.UpdatedAt = now
	batchJob.SucceededCount = succeededCount
	batchJob.FailedCount = failedCount
	batchJob.SkippedCount = skippedCount

	batchJob.ErrorDetails = nil
	if errorMessage != "" {
		batchJob.ErrorDetails = &datamodel.UpgradeErrorDetails{
			ErrorCode:    "BATCH_UPGRADE_PARTIAL",
			ErrorMessage: errorMessage,
			ErrorType:    "BATCH_UPGRADE_ERROR",
			Retryable:    false,
		}
	}

	if status == string(models.UpgradeStatusCompleted) || status == string(models.UpgradeStatusFailed) {
		batchJob.CompletedAt = &now
		batchJob.DeletedAt = &gorm.DeletedAt{Time: now, Valid: true}
	} else {
		batchJob.CompletedAt = nil
		batchJob.DeletedAt = nil
	}

	if err := a.SE.UpdateBatchClusterUpgradeJob(ctx, batchJob); err != nil {
		logger.Error("Failed to complete batch cluster upgrade job", "batchJobID", batchJobID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Successfully completed batch cluster upgrade job", "batchJobID", batchJobID, "status", status)
	return nil
}

// CreateChildUpgradeJobActivity creates a child cluster upgrade job linked to a batch job
func (a *BatchClusterUpgradeActivity) CreateChildUpgradeJobActivity(ctx context.Context, batchUpgradeID, clusterID, poolID, currentVersion, targetVersion, vsaBuildImage, mediatorBuildImage string, forceUpgrade bool, jobUUID string) error {
	logger := util.GetLogger(ctx)
	logger.Info("Creating child upgrade job for batch",
		"batchUpgradeID", batchUpgradeID,
		"clusterId", clusterID,
		"jobUUID", jobUUID)

	upgradeJob := &datamodel.ClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{
			UUID: jobUUID,
		},
		ClusterID:          clusterID,
		PoolID:             poolID,
		TargetVersion:      targetVersion,
		CurrentVersion:     currentVersion,
		VSABuildImage:      vsaBuildImage,
		MediatorBuildImage: mediatorBuildImage,
		Status:             string(models.UpgradeStatusPending),
		BatchUpgradeID:     &batchUpgradeID,
		ForceUpgrade:       forceUpgrade,
	}

	_, err := a.SE.CreateClusterUpgradeJob(ctx, upgradeJob)
	if err != nil {
		logger.Error("Failed to create child upgrade job", "clusterId", clusterID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Successfully created child upgrade job", "clusterId", clusterID, "jobUUID", jobUUID)
	return nil
}
