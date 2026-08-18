package activities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/temporal"
	"gorm.io/gorm"
)

func TestPrepareBatchUpgradeWaveActivity(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &BatchClusterUpgradeActivity{SE: mockStorage}
	ctx := context.Background()
	pool1 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		State:     datamodel.LifeCycleStateREADY,
	}
	pool2 := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-2"},
		State:     datamodel.LifeCycleStateDisabled,
	}

	mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1", "pool-2"}).
		Return([]*datamodel.Pool{pool1, pool2}, nil).Once()
	mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").
		Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-2").
		Return([]*datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.On("CreateClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.ClusterUpgradeJob) bool {
		return job.UUID == "job-1" &&
			job.ClusterID == "cluster-1" &&
			job.BatchUpgradeID != nil &&
			*job.BatchUpgradeID == "batch-1"
	})).Return(&datamodel.ClusterUpgradeJob{}, nil)
	mockStorage.On("CreateClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.ClusterUpgradeJob) bool {
		return job.UUID == "job-2" &&
			job.ClusterID == "cluster-2" &&
			job.BatchUpgradeID != nil &&
			*job.BatchUpgradeID == "batch-1"
	})).Return(&datamodel.ClusterUpgradeJob{}, nil)

	prepared, err := activity.PrepareBatchUpgradeWaveActivity(ctx, &PrepareBatchUpgradeWaveParams{
		BatchUpgradeID: "batch-1",
		Clusters: []BatchUpgradeWaveCluster{
			{
				SelectedCluster: models.SelectedCluster{
					ClusterID:      "cluster-1",
					PoolID:         "pool-1",
					CurrentVersion: "9.14.1",
				},
				JobID: "job-1",
			},
			{
				SelectedCluster: models.SelectedCluster{
					ClusterID:      "cluster-2",
					PoolID:         "pool-2",
					CurrentVersion: "9.14.1",
				},
				JobID: "job-2",
			},
		},
		TargetVersion:      "9.15.1",
		VSABuildImage:      "vsa-image",
		MediatorBuildImage: "mediator-image",
		ForceUpgrade:       true,
	})

	assert.NoError(t, err)
	assert.Len(t, prepared, 2)
	assert.Same(t, pool1, prepared[0].Pool)
	assert.Same(t, pool2, prepared[1].Pool)
	assert.Empty(t, prepared[0].Error)
	assert.Empty(t, prepared[1].Error)
	mockStorage.AssertExpectations(t)
}

func TestPrepareBatchUpgradeWaveActivity_RetryUsesExistingChildJob(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &BatchClusterUpgradeActivity{SE: mockStorage}
	ctx := context.Background()
	batchUpgradeID := "batch-1"
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		State:     datamodel.LifeCycleStateREADY,
	}
	existingJob := &datamodel.ClusterUpgradeJob{
		BaseModel:      datamodel.BaseModel{UUID: "job-1"},
		ClusterID:      "cluster-1",
		PoolID:         "pool-1",
		Status:         string(models.UpgradeStatusPending),
		BatchUpgradeID: &batchUpgradeID,
	}

	mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).
		Return([]*datamodel.Pool{pool}, nil).Once()
	mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").
		Return([]*datamodel.ClusterUpgradeJob{existingJob}, nil).Once()

	prepared, err := activity.PrepareBatchUpgradeWaveActivity(ctx, &PrepareBatchUpgradeWaveParams{
		BatchUpgradeID: batchUpgradeID,
		Clusters: []BatchUpgradeWaveCluster{
			{
				SelectedCluster: models.SelectedCluster{
					ClusterID:      "cluster-1",
					PoolID:         "pool-1",
					CurrentVersion: "9.14.1",
				},
				JobID: "job-1",
			},
		},
		TargetVersion: "9.15.1",
	})

	assert.NoError(t, err)
	assert.Len(t, prepared, 1)
	assert.Same(t, pool, prepared[0].Pool)
	assert.Empty(t, prepared[0].Error)
	mockStorage.AssertNotCalled(t, "CreateClusterUpgradeJob", mock.Anything, mock.Anything)
	mockStorage.AssertExpectations(t)
}

func TestPrepareBatchUpgradeWaveActivity_CreateRaceUsesExistingChildJob(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	activity := &BatchClusterUpgradeActivity{SE: mockStorage}
	ctx := context.Background()
	batchUpgradeID := "batch-1"
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-1"},
		State:     datamodel.LifeCycleStateREADY,
	}
	existingJob := &datamodel.ClusterUpgradeJob{
		BaseModel:      datamodel.BaseModel{UUID: "job-1"},
		ClusterID:      "cluster-1",
		PoolID:         "pool-1",
		Status:         string(models.UpgradeStatusPending),
		BatchUpgradeID: &batchUpgradeID,
	}

	mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).
		Return([]*datamodel.Pool{pool}, nil).Once()
	mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").
		Return([]*datamodel.ClusterUpgradeJob{}, nil).Once()
	mockStorage.On("CreateClusterUpgradeJob", ctx, mock.Anything).
		Return(nil, errors.New("duplicate key")).Once()
	mockStorage.On("GetClusterUpgradeJobByUUID", ctx, "job-1").
		Return(existingJob, nil).Once()

	prepared, err := activity.PrepareBatchUpgradeWaveActivity(ctx, &PrepareBatchUpgradeWaveParams{
		BatchUpgradeID: batchUpgradeID,
		Clusters: []BatchUpgradeWaveCluster{
			{
				SelectedCluster: models.SelectedCluster{
					ClusterID:      "cluster-1",
					PoolID:         "pool-1",
					CurrentVersion: "9.14.1",
				},
				JobID: "job-1",
			},
		},
		TargetVersion: "9.15.1",
	})

	assert.NoError(t, err)
	assert.Len(t, prepared, 1)
	assert.Same(t, pool, prepared[0].Pool)
	assert.Empty(t, prepared[0].Error)
	mockStorage.AssertExpectations(t)
}

func TestGetPoolForUpgradeActivity(t *testing.T) {
	t.Run("ReturnsCompletePool", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()
		expectedPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
			VLMConfig: `{"cluster_name":"cluster-1"}`,
		}

		mockStorage.On("GetPoolByUUID", ctx, "pool-1").Return(expectedPool, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").Return([]*datamodel.ClusterUpgradeJob{}, nil)

		pool, err := activity.GetPoolForUpgradeActivity(ctx, "pool-1")

		assert.NoError(t, err)
		assert.Same(t, expectedPool, pool)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenPoolLookupFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On("GetPoolByUUID", ctx, "pool-1").Return(nil, errors.New("database unavailable"))

		pool, err := activity.GetPoolForUpgradeActivity(ctx, "pool-1")

		assert.Nil(t, pool)
		assert.ErrorContains(t, err, "database unavailable")
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenPoolIsNil", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On("GetPoolByUUID", ctx, "pool-1").Return(nil, nil)

		pool, err := activity.GetPoolForUpgradeActivity(ctx, "pool-1")

		assert.Nil(t, pool)
		assert.ErrorContains(t, err, "pool pool-1 not found")
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenPoolStateChanged", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     "DELETING",
		}

		mockStorage.On("GetPoolByUUID", ctx, "pool-1").Return(pool, nil)

		result, err := activity.GetPoolForUpgradeActivity(ctx, "pool-1")

		assert.Nil(t, result)
		assert.ErrorContains(t, err, "current state is DELETING")
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenAnotherUpgradeBecameActive", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
		}
		activeJob := &datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "active-job"},
			Status:    string(models.UpgradeStatusInProgress),
		}

		mockStorage.On("GetPoolByUUID", ctx, "pool-1").Return(pool, nil)
		mockStorage.On("GetClusterUpgradeJobsByClusterID", ctx, "pool-1").
			Return([]*datamodel.ClusterUpgradeJob{activeJob}, nil)

		result, err := activity.GetPoolForUpgradeActivity(ctx, "pool-1")

		assert.Nil(t, result)
		assert.ErrorContains(t, err, "active-job")
		assert.ErrorContains(t, err, "IN_PROGRESS")
		mockStorage.AssertExpectations(t)
	})
}

func TestUpdateBatchJobStatusActivity(t *testing.T) {
	t.Run("UpdatesStatusToInProgress", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On(
			"TransitionPendingUpgradeJobToInProgress",
			ctx,
			database.UpgradeJobKindBatch,
			"batch-1",
			mock.AnythingOfType("time.Time"),
		).Return(true, nil)

		err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusInProgress), "")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SetsErrorDetailsWhenErrorMessageProvided", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusInProgress),
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.Status == string(models.UpgradeStatusFailed) &&
				job.ErrorDetails != nil &&
				job.ErrorDetails.ErrorCode == "BATCH_UPGRADE_FAILED" &&
				job.ErrorDetails.ErrorMessage == "something went wrong" &&
				!job.ErrorDetails.Retryable &&
				job.CompletedAt != nil &&
				job.DeletedAt != nil
		})).Return(nil)

		err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusFailed), "something went wrong")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ClearsExistingErrorDetailsWhenErrorMessageIsEmpty", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On(
			"TransitionPendingUpgradeJobToInProgress",
			ctx,
			database.UpgradeJobKindBatch,
			"batch-1",
			mock.AnythingOfType("time.Time"),
		).Return(true, nil)

		err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusInProgress), "")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenGetFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On(
			"TransitionPendingUpgradeJobToInProgress",
			ctx,
			database.UpgradeJobKindBatch,
			"batch-1",
			mock.AnythingOfType("time.Time"),
		).Return(false, errors.New("db error"))

		err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusInProgress), "")
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenGetReturnsNil", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On(
			"TransitionPendingUpgradeJobToInProgress",
			ctx,
			database.UpgradeJobKindBatch,
			"batch-1",
			mock.AnythingOfType("time.Time"),
		).Return(false, nil)

		err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusInProgress), "")
		assert.Error(t, err)
		var appErr *temporal.ApplicationError
		require.True(t, errors.As(err, &appErr))
		assert.True(t, appErr.NonRetryable())
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenUpdateFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On(
			"TransitionPendingUpgradeJobToInProgress",
			ctx,
			database.UpgradeJobKindBatch,
			"batch-1",
			mock.AnythingOfType("time.Time"),
		).Return(false, errors.New("update failed"))

		err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusInProgress), "")
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("MissingJobIsNonRetryableForTerminalStatus", func(t *testing.T) {
		for name, getErr := range map[string]error{
			"get returns nil":  nil,
			"record not found": gorm.ErrRecordNotFound,
		} {
			t.Run(name, func(t *testing.T) {
				mockStorage := database.NewMockStorage(t)
				activity := &BatchClusterUpgradeActivity{SE: mockStorage}
				ctx := context.Background()

				mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(nil, getErr)

				err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusFailed), "")
				assert.Error(t, err)
				var appErr *temporal.ApplicationError
				require.True(t, errors.As(err, &appErr))
				assert.True(t, appErr.NonRetryable())
				mockStorage.AssertExpectations(t)
			})
		}
	})

	t.Run("SetsCompletedAtAndDeletedAtForFailedStatus", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusInProgress),
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.Status == string(models.UpgradeStatusFailed) &&
				job.ErrorDetails == nil &&
				job.CompletedAt != nil &&
				job.DeletedAt != nil &&
				job.StartedAt == nil
		})).Return(nil)

		err := activity.UpdateBatchJobStatusActivity(ctx, "batch-1", string(models.UpgradeStatusFailed), "")
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestCompleteBatchJobActivity(t *testing.T) {
	t.Run("CompletesWithSuccessAndSoftDeletes", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusInProgress),
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.Status == string(models.UpgradeStatusCompleted) &&
				job.CompletedAt != nil &&
				job.SucceededCount == 5 &&
				job.FailedCount == 0 &&
				job.SkippedCount == 0 &&
				job.DeletedAt != nil &&
				job.ErrorDetails == nil
		})).Return(nil)

		err := activity.CompleteBatchJobActivity(ctx, "batch-1", string(models.UpgradeStatusCompleted), "", 5, 0, 0)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ClearsExistingErrorDetailsWhenErrorMessageIsEmpty", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusFailed),
			ErrorDetails: &datamodel.UpgradeErrorDetails{
				ErrorCode:    "BATCH_UPGRADE_PARTIAL",
				ErrorMessage: "previous partial failure",
				ErrorType:    "BATCH_UPGRADE_ERROR",
			},
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.Status == string(models.UpgradeStatusCompleted) &&
				job.ErrorDetails == nil
		})).Return(nil)

		err := activity.CompleteBatchJobActivity(ctx, "batch-1", string(models.UpgradeStatusCompleted), "", 5, 0, 0)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("CompletesWithFailureAndSetsErrorDetails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusInProgress),
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.Status == string(models.UpgradeStatusFailed) &&
				job.SucceededCount == 2 &&
				job.FailedCount == 3 &&
				job.SkippedCount == 5 &&
				job.DeletedAt != nil &&
				job.ErrorDetails != nil &&
				job.ErrorDetails.ErrorCode == "BATCH_UPGRADE_PARTIAL"
		})).Return(nil)

		err := activity.CompleteBatchJobActivity(ctx, "batch-1", string(models.UpgradeStatusFailed), "3 of 10 failed", 2, 3, 5)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DoesNotSoftDeleteForCancelledStatus", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		previousCompletion := time.Now().Add(-time.Hour)
		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{
				UUID:      "batch-1",
				DeletedAt: &gorm.DeletedAt{Time: previousCompletion, Valid: true},
			},
			Status:      string(models.UpgradeStatusInProgress),
			CompletedAt: &previousCompletion,
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.BatchClusterUpgradeJob) bool {
			return job.Status == string(models.UpgradeStatusCancelled) &&
				job.CompletedAt == nil &&
				job.DeletedAt == nil
		})).Return(nil)

		err := activity.CompleteBatchJobActivity(ctx, "batch-1", string(models.UpgradeStatusCancelled), "", 0, 0, 0)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenGetFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(nil, errors.New("not found"))

		err := activity.CompleteBatchJobActivity(ctx, "batch-1", string(models.UpgradeStatusCompleted), "", 5, 0, 0)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("MissingJobIsNonRetryable", func(t *testing.T) {
		for name, getErr := range map[string]error{
			"get returns nil":  nil,
			"record not found": gorm.ErrRecordNotFound,
		} {
			t.Run(name, func(t *testing.T) {
				mockStorage := database.NewMockStorage(t)
				activity := &BatchClusterUpgradeActivity{SE: mockStorage}
				ctx := context.Background()

				mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(nil, getErr)

				err := activity.CompleteBatchJobActivity(ctx, "batch-1", string(models.UpgradeStatusCompleted), "", 5, 0, 0)
				assert.Error(t, err)
				var appErr *temporal.ApplicationError
				require.True(t, errors.As(err, &appErr))
				assert.True(t, appErr.NonRetryable())
				mockStorage.AssertExpectations(t)
			})
		}
	})

	t.Run("ReturnsErrorWhenUpdateFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		batchJob := &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
		}

		mockStorage.On("GetBatchClusterUpgradeJobByUUID", ctx, "batch-1").Return(batchJob, nil)
		mockStorage.On("UpdateBatchClusterUpgradeJob", ctx, mock.Anything).Return(errors.New("update failed"))

		err := activity.CompleteBatchJobActivity(ctx, "batch-1", string(models.UpgradeStatusCompleted), "", 5, 0, 0)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestCreateChildUpgradeJobActivity(t *testing.T) {
	t.Run("CreatesChildJobWithBatchLink", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.ClusterUpgradeJob) bool {
			return job.UUID == "child-job-1" &&
				job.ClusterID == "cluster-1" &&
				job.PoolID == "pool-1" &&
				job.CurrentVersion == "9.14.1" &&
				job.TargetVersion == "9.15.1" &&
				job.VSABuildImage == "vsa-image" &&
				job.MediatorBuildImage == "mediator-image" &&
				job.Status == string(models.UpgradeStatusPending) &&
				job.BatchUpgradeID != nil &&
				*job.BatchUpgradeID == "batch-1" &&
				job.ForceUpgrade
		})).Return(&datamodel.ClusterUpgradeJob{}, nil)

		err := activity.CreateChildUpgradeJobActivity(
			ctx, "batch-1", "cluster-1", "pool-1", "9.14.1", "9.15.1",
			"vsa-image", "mediator-image", true, "child-job-1",
		)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenCreateFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.Anything).Return(nil, errors.New("duplicate key"))

		err := activity.CreateChildUpgradeJobActivity(
			ctx, "batch-1", "cluster-1", "pool-1", "9.14.1", "9.15.1",
			"vsa-image", "mediator-image", false, "child-job-1",
		)
		assert.Error(t, err)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SetsForceUpgradeFalseWhenNotForced", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &BatchClusterUpgradeActivity{SE: mockStorage}
		ctx := context.Background()

		mockStorage.On("CreateClusterUpgradeJob", ctx, mock.MatchedBy(func(job *datamodel.ClusterUpgradeJob) bool {
			return !job.ForceUpgrade
		})).Return(&datamodel.ClusterUpgradeJob{}, nil)

		err := activity.CreateChildUpgradeJobActivity(
			ctx, "batch-1", "cluster-1", "pool-1", "9.14.1", "9.15.1",
			"vsa-image", "mediator-image", false, "child-job-1",
		)
		assert.NoError(t, err)
		mockStorage.AssertExpectations(t)
	})
}
