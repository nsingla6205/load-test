package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupUpgradeSupervisorStore(t *testing.T) (*DataStoreRepository, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&datamodel.ClusterUpgradeJob{},
		&datamodel.BatchClusterUpgradeJob{},
	))
	return NewDataStoreRepository(gormwrapper.New(db)), db
}

func TestListPendingUpgradeJobsCreatedBefore_ReturnsStaleJobsOfEveryKind(t *testing.T) {
	store, db := setupUpgradeSupervisorStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	batchID := "parent-batch"
	startedAt := now.Add(-20 * time.Minute)

	singleJobs := []*datamodel.ClusterUpgradeJob{
		{
			BaseModel: datamodel.BaseModel{UUID: "stale-single", CreatedAt: now.Add(-30 * time.Minute)},
			ClusterID: "cluster-1",
			Status:    pendingUpgradeStatus,
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "recent-single", CreatedAt: now.Add(-5 * time.Minute)},
			ClusterID: "cluster-2",
			Status:    pendingUpgradeStatus,
		},
		{
			BaseModel:      datamodel.BaseModel{UUID: "batch-child", CreatedAt: now.Add(-30 * time.Minute)},
			ClusterID:      "cluster-3",
			Status:         pendingUpgradeStatus,
			BatchUpgradeID: &batchID,
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "started-single", CreatedAt: now.Add(-30 * time.Minute)},
			ClusterID: "cluster-4",
			Status:    inProgressUpgradeStatus,
			StartedAt: &startedAt,
		},
	}
	for _, job := range singleJobs {
		require.NoError(t, db.Create(job).Error)
	}

	require.NoError(t, db.Create(&datamodel.BatchClusterUpgradeJob{
		BaseModel:     datamodel.BaseModel{UUID: "stale-batch", CreatedAt: now.Add(-30 * time.Minute)},
		Status:        pendingUpgradeStatus,
		TotalClusters: 3,
	}).Error)
	require.NoError(t, db.Create(&datamodel.BatchClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{UUID: "recent-batch", CreatedAt: now.Add(-5 * time.Minute)},
		Status:    pendingUpgradeStatus,
	}).Error)

	jobs, err := store.ListPendingUpgradeJobsCreatedBefore(ctx, now.Add(-15*time.Minute))

	require.NoError(t, err)
	require.Len(t, jobs, 3)
	assert.ElementsMatch(t, []PendingUpgradeJob{
		{
			UUID:      "stale-single",
			Kind:      UpgradeJobKindSingle,
			CreatedAt: singleJobs[0].CreatedAt,
		},
		{
			// A child left PENDING keeps blocking its cluster, so it needs the
			// same timeout path as a top-level job.
			UUID:           "batch-child",
			Kind:           UpgradeJobKindBatchChild,
			CreatedAt:      singleJobs[2].CreatedAt,
			BatchUpgradeID: batchID,
			ClusterID:      "cluster-3",
		},
		{
			UUID:      "stale-batch",
			Kind:      UpgradeJobKindBatch,
			CreatedAt: now.Add(-30 * time.Minute),
		},
	}, jobs)
}

func TestTransitionPendingUpgradeJobToInProgress_CommonBehavior(t *testing.T) {
	store, db := setupUpgradeSupervisorStore(t)
	ctx := context.Background()
	startedAt := time.Now().UTC().Truncate(time.Second)

	require.NoError(t, db.Create(&datamodel.ClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{UUID: "single-job"},
		ClusterID: "cluster-1",
		Status:    pendingUpgradeStatus,
		ErrorDetails: &datamodel.UpgradeErrorDetails{
			ErrorCode:    "PREVIOUS",
			ErrorMessage: "cleared on claim",
		},
	}).Error)
	childBatchID := "parent-batch"
	require.NoError(t, db.Create(&datamodel.ClusterUpgradeJob{
		BaseModel:      datamodel.BaseModel{UUID: "child-job"},
		ClusterID:      "cluster-2",
		Status:         pendingUpgradeStatus,
		BatchUpgradeID: &childBatchID,
	}).Error)
	require.NoError(t, db.Create(&datamodel.BatchClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{UUID: "batch-job"},
		Status:    pendingUpgradeStatus,
		ErrorDetails: &datamodel.UpgradeErrorDetails{
			ErrorCode:    "PREVIOUS",
			ErrorMessage: "cleared on claim",
		},
	}).Error)

	for _, tc := range []struct {
		name string
		kind UpgradeJobKind
		uuid string
	}{
		{name: "single", kind: UpgradeJobKindSingle, uuid: "single-job"},
		{name: "batch child", kind: UpgradeJobKindBatchChild, uuid: "child-job"},
		{name: "batch", kind: UpgradeJobKindBatch, uuid: "batch-job"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transitioned, err := store.TransitionPendingUpgradeJobToInProgress(ctx, tc.kind, tc.uuid, startedAt)
			require.NoError(t, err)
			assert.True(t, transitioned)

			var (
				status       string
				jobStarted   *time.Time
				jobUpdated   time.Time
				errorDetails *datamodel.UpgradeErrorDetails
				deletedAt    *gorm.DeletedAt
			)
			switch tc.kind {
			case UpgradeJobKindSingle, UpgradeJobKindBatchChild:
				var job datamodel.ClusterUpgradeJob
				require.NoError(t, db.Unscoped().Where("uuid = ?", tc.uuid).First(&job).Error)
				status, jobStarted, jobUpdated = job.Status, job.StartedAt, job.UpdatedAt
				errorDetails, deletedAt = job.ErrorDetails, job.DeletedAt
			case UpgradeJobKindBatch:
				var job datamodel.BatchClusterUpgradeJob
				require.NoError(t, db.Unscoped().Where("uuid = ?", tc.uuid).First(&job).Error)
				status, jobStarted, jobUpdated = job.Status, job.StartedAt, job.UpdatedAt
				errorDetails, deletedAt = job.ErrorDetails, job.DeletedAt
			}
			assert.Equal(t, inProgressUpgradeStatus, status)
			require.NotNil(t, jobStarted)
			assert.True(t, jobStarted.Equal(startedAt))
			assert.True(t, jobUpdated.Equal(startedAt))
			assert.Nil(t, errorDetails)
			assert.Nil(t, deletedAt)

			retryAt := startedAt.Add(time.Minute)
			transitioned, err = store.TransitionPendingUpgradeJobToInProgress(ctx, tc.kind, tc.uuid, retryAt)
			require.NoError(t, err)
			assert.True(t, transitioned, "the transition must be idempotent for Temporal retries")

			switch tc.kind {
			case UpgradeJobKindSingle, UpgradeJobKindBatchChild:
				var job datamodel.ClusterUpgradeJob
				require.NoError(t, db.Unscoped().Where("uuid = ?", tc.uuid).First(&job).Error)
				require.NotNil(t, job.StartedAt)
				assert.True(t, job.StartedAt.Equal(startedAt), "retry must not rewrite started_at")
				assert.True(t, job.UpdatedAt.Equal(startedAt), "retry must not rewrite updated_at")
				assert.Nil(t, job.ErrorDetails)
				assert.Nil(t, job.DeletedAt)
			case UpgradeJobKindBatch:
				var job datamodel.BatchClusterUpgradeJob
				require.NoError(t, db.Unscoped().Where("uuid = ?", tc.uuid).First(&job).Error)
				require.NotNil(t, job.StartedAt)
				assert.True(t, job.StartedAt.Equal(startedAt), "retry must not rewrite started_at")
				assert.True(t, job.UpdatedAt.Equal(startedAt), "retry must not rewrite updated_at")
				assert.Nil(t, job.ErrorDetails)
				assert.Nil(t, job.DeletedAt)
			}
		})
	}
}

func TestTransitionPendingUpgradeJobToInProgress_AlreadyInProgressIsNoOp(t *testing.T) {
	store, db := setupUpgradeSupervisorStore(t)
	ctx := context.Background()
	originalStarted := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	originalUpdated := originalStarted.Add(5 * time.Minute)
	preservedError := &datamodel.UpgradeErrorDetails{
		ErrorCode:    "KEEP_ME",
		ErrorMessage: "must survive idempotent claim",
	}

	require.NoError(t, db.Create(&datamodel.ClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{
			UUID:      "already-running",
			UpdatedAt: originalUpdated,
		},
		ClusterID:    "cluster-1",
		Status:       inProgressUpgradeStatus,
		StartedAt:    &originalStarted,
		ErrorDetails: preservedError,
	}).Error)

	transitioned, err := store.TransitionPendingUpgradeJobToInProgress(
		ctx,
		UpgradeJobKindSingle,
		"already-running",
		time.Now().UTC(),
	)
	require.NoError(t, err)
	assert.True(t, transitioned)

	var job datamodel.ClusterUpgradeJob
	require.NoError(t, db.Unscoped().Where("uuid = ?", "already-running").First(&job).Error)
	require.NotNil(t, job.StartedAt)
	assert.True(t, job.StartedAt.Equal(originalStarted))
	assert.True(t, job.UpdatedAt.Equal(originalUpdated))
	require.NotNil(t, job.ErrorDetails)
	assert.Equal(t, "KEEP_ME", job.ErrorDetails.ErrorCode)
}

func TestTransitionPendingUpgradeJobToInProgress_TerminalJobIsNotClaimed(t *testing.T) {
	store, db := setupUpgradeSupervisorStore(t)
	ctx := context.Background()

	require.NoError(t, db.Create(&datamodel.ClusterUpgradeJob{
		BaseModel: datamodel.BaseModel{UUID: "failed-job"},
		ClusterID: "cluster-1",
		Status:    "FAILED",
	}).Error)

	transitioned, err := store.TransitionPendingUpgradeJobToInProgress(
		ctx,
		UpgradeJobKindSingle,
		"failed-job",
		time.Now().UTC(),
	)

	require.NoError(t, err)
	assert.False(t, transitioned)
}

func TestMergePendingUpgradeJobsByCreatedAt_KeepsOldestAcrossKinds(t *testing.T) {
	now := time.Now().UTC()
	jobs := []PendingUpgradeJob{
		{UUID: "single-old", Kind: UpgradeJobKindSingle, CreatedAt: now.Add(-30 * time.Minute)},
		{UUID: "single-new", Kind: UpgradeJobKindSingle, CreatedAt: now.Add(-5 * time.Minute)},
		{UUID: "batch-mid", Kind: UpgradeJobKindBatch, CreatedAt: now.Add(-20 * time.Minute)},
		{UUID: "batch-newer", Kind: UpgradeJobKindBatch, CreatedAt: now.Add(-10 * time.Minute)},
	}

	merged := mergePendingUpgradeJobsByCreatedAt(jobs, 3)

	require.Len(t, merged, 3)
	assert.Equal(t, []string{"single-old", "batch-mid", "batch-newer"}, []string{
		merged[0].UUID, merged[1].UUID, merged[2].UUID,
	})
}
