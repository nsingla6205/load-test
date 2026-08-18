package database

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

const (
	pendingUpgradeStatus         = "PENDING"
	inProgressUpgradeStatus      = "IN_PROGRESS"
	maxPendingUpgradeJobsPerScan = 100
)

// UpgradeJobKind identifies the persistence model used by a pending upgrade job.
type UpgradeJobKind string

const (
	// UpgradeJobKindSingle identifies a top-level single-cluster upgrade job.
	UpgradeJobKindSingle UpgradeJobKind = "single"
	// UpgradeJobKindBatch identifies a batch-cluster upgrade job.
	UpgradeJobKindBatch UpgradeJobKind = "batch"
	// UpgradeJobKindBatchChild identifies a cluster upgrade job started by a
	// batch. It shares the ClusterUpgradeJob model but its Temporal execution is
	// the batch child workflow, not the job UUID.
	UpgradeJobKindBatchChild UpgradeJobKind = "batch-child"
)

// PendingUpgradeJob contains the common fields needed to supervise an upgrade
// workflow that has not transitioned out of PENDING. BatchUpgradeID and
// ClusterID are set for UpgradeJobKindBatchChild only, so callers can rebuild
// the child workflow ID.
type PendingUpgradeJob struct {
	UUID           string
	Kind           UpgradeJobKind
	CreatedAt      time.Time
	BatchUpgradeID string
	ClusterID      string
}

// ListPendingUpgradeJobsCreatedBefore returns single, batch, and batch-child
// upgrade jobs that have not started before the supplied cutoff, oldest first,
// capped per scan to bound Temporal describe/terminate work. Batch children are
// included because a child row left PENDING keeps blocking its cluster through
// CheckActiveUpgradeJob, and no other path retires it.
func (d *DataStoreRepository) ListPendingUpgradeJobsCreatedBefore(ctx context.Context, cutoff time.Time) ([]PendingUpgradeJob, error) {
	var singleJobs []datamodel.ClusterUpgradeJob
	err := d.db.GORM().WithContext(ctx).
		Select("uuid", "created_at").
		Where("status = ? AND started_at IS NULL AND batch_upgrade_id IS NULL AND created_at <= ?", pendingUpgradeStatus, cutoff).
		Order("created_at ASC, uuid ASC").
		Limit(maxPendingUpgradeJobsPerScan).
		Find(&singleJobs).Error
	if err != nil {
		return nil, err
	}

	var childJobs []datamodel.ClusterUpgradeJob
	err = d.db.GORM().WithContext(ctx).
		Select("uuid", "created_at", "batch_upgrade_id", "cluster_id").
		Where("status = ? AND started_at IS NULL AND batch_upgrade_id IS NOT NULL AND created_at <= ?", pendingUpgradeStatus, cutoff).
		Order("created_at ASC, uuid ASC").
		Limit(maxPendingUpgradeJobsPerScan).
		Find(&childJobs).Error
	if err != nil {
		return nil, err
	}

	var batchJobs []datamodel.BatchClusterUpgradeJob
	err = d.db.GORM().WithContext(ctx).
		Select("uuid", "created_at").
		Where("status = ? AND started_at IS NULL AND created_at <= ?", pendingUpgradeStatus, cutoff).
		Order("created_at ASC, uuid ASC").
		Limit(maxPendingUpgradeJobsPerScan).
		Find(&batchJobs).Error
	if err != nil {
		return nil, err
	}

	jobs := make([]PendingUpgradeJob, 0, len(singleJobs)+len(childJobs)+len(batchJobs))
	for i := range singleJobs {
		jobs = append(jobs, PendingUpgradeJob{
			UUID:      singleJobs[i].UUID,
			Kind:      UpgradeJobKindSingle,
			CreatedAt: singleJobs[i].CreatedAt,
		})
	}
	for i := range childJobs {
		batchUpgradeID := ""
		if childJobs[i].BatchUpgradeID != nil {
			batchUpgradeID = *childJobs[i].BatchUpgradeID
		}
		jobs = append(jobs, PendingUpgradeJob{
			UUID:           childJobs[i].UUID,
			Kind:           UpgradeJobKindBatchChild,
			CreatedAt:      childJobs[i].CreatedAt,
			BatchUpgradeID: batchUpgradeID,
			ClusterID:      childJobs[i].ClusterID,
		})
	}
	for i := range batchJobs {
		jobs = append(jobs, PendingUpgradeJob{
			UUID:      batchJobs[i].UUID,
			Kind:      UpgradeJobKindBatch,
			CreatedAt: batchJobs[i].CreatedAt,
		})
	}

	return mergePendingUpgradeJobsByCreatedAt(jobs, maxPendingUpgradeJobsPerScan), nil
}

// mergePendingUpgradeJobsByCreatedAt interleaves the per-kind result sets into a
// single oldest-first page so one kind cannot starve the others when more than
// maxPendingUpgradeJobsPerScan rows are pending.
func mergePendingUpgradeJobsByCreatedAt(jobs []PendingUpgradeJob, limit int) []PendingUpgradeJob {
	sort.SliceStable(jobs, func(i, j int) bool {
		if !jobs[i].CreatedAt.Equal(jobs[j].CreatedAt) {
			return jobs[i].CreatedAt.Before(jobs[j].CreatedAt)
		}
		return jobs[i].UUID < jobs[j].UUID
	})

	if len(jobs) > limit {
		jobs = jobs[:limit]
	}
	return jobs
}

// TransitionPendingUpgradeJobToInProgress atomically claims a pending upgrade
// job for workflow execution. Repeated calls for an already-running job are
// treated as successful no-ops (without rewriting fields) to preserve Temporal
// activity idempotency.
func (d *DataStoreRepository) TransitionPendingUpgradeJobToInProgress(
	ctx context.Context,
	kind UpgradeJobKind,
	jobUUID string,
	startedAt time.Time,
) (bool, error) {
	var model interface{}
	switch kind {
	case UpgradeJobKindSingle, UpgradeJobKindBatchChild:
		model = &datamodel.ClusterUpgradeJob{}
	case UpgradeJobKindBatch:
		model = &datamodel.BatchClusterUpgradeJob{}
	default:
		return false, fmt.Errorf("unsupported upgrade job kind %q", kind)
	}

	db := d.db.GORM().WithContext(ctx).Unscoped()
	result := db.Model(model).
		Where("uuid = ? AND status = ?", jobUUID, pendingUpgradeStatus).
		Updates(map[string]interface{}{
			"status":        inProgressUpgradeStatus,
			"started_at":    startedAt,
			"updated_at":    startedAt,
			"error_details": nil,
			"deleted_at":    nil,
		})
	if result.Error != nil {
		return false, result.Error
	}
	if result.RowsAffected == 1 {
		return true, nil
	}

	// Already claimed: succeed without mutating updated_at / error_details / deleted_at.
	// Use a fresh session — GORM keeps prior WHERE clauses on the cloned handle.
	var countModel interface{}
	switch kind {
	case UpgradeJobKindSingle, UpgradeJobKindBatchChild:
		countModel = &datamodel.ClusterUpgradeJob{}
	case UpgradeJobKindBatch:
		countModel = &datamodel.BatchClusterUpgradeJob{}
	}
	var count int64
	if err := d.db.GORM().WithContext(ctx).Unscoped().Model(countModel).
		Where("uuid = ? AND status = ?", jobUUID, inProgressUpgradeStatus).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count == 1, nil
}
