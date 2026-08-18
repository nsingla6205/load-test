package tasks

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
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	workflowpb "go.temporal.io/api/workflow/v1"
	workflowservice "go.temporal.io/api/workflowservice/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// runningUpgradeWorkflowResponse describes a RUNNING execution that started
// startedAgo in the past, which is what decides whether the running grace period
// has elapsed.
func runningUpgradeWorkflowResponse(startedAgo time.Duration) *workflowservice.DescribeWorkflowExecutionResponse {
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Status:    enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING,
			StartTime: timestamppb.New(time.Now().UTC().Add(-startedAgo)),
		},
	}
}

func closedUpgradeWorkflowResponse(status enumspb.WorkflowExecutionStatus) *workflowservice.DescribeWorkflowExecutionResponse {
	return &workflowservice.DescribeWorkflowExecutionResponse{
		WorkflowExecutionInfo: &workflowpb.WorkflowExecutionInfo{
			Status:    status,
			StartTime: timestamppb.New(time.Now().UTC().Add(-30 * time.Minute)),
		},
	}
}

func testUpgradeJobSupervisorConfig() upgradeJobSupervisorConfig {
	return upgradeJobSupervisorConfig{
		PickupTimeout:      15 * time.Minute,
		RunningGracePeriod: 15 * time.Minute,
		DescribeTimeout:    time.Second,
		TerminateTimeout:   time.Second,
	}
}

func expectUpgradeTimeoutTransaction(
	t *testing.T,
	storage *database.MockStorage,
	job database.PendingUpgradeJob,
	status string,
	totalClusters int,
) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&datamodel.ClusterUpgradeJob{},
		&datamodel.BatchClusterUpgradeJob{},
	))

	seedPendingUpgradeJobRow(t, db, job, status, totalClusters)

	storage.EXPECT().WithTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, fn func(dbutils.Transaction) error) error {
			tx := dbutils.NewMockTransaction(t)
			tx.EXPECT().GORM().Return(db).Maybe()
			return fn(tx)
		})
	return db
}

func seedPendingUpgradeJobRow(
	t *testing.T,
	db *gorm.DB,
	job database.PendingUpgradeJob,
	status string,
	totalClusters int,
) {
	t.Helper()

	switch job.Kind {
	case database.UpgradeJobKindSingle:
		require.NoError(t, db.Create(&datamodel.ClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: job.UUID, CreatedAt: job.CreatedAt},
			ClusterID: "cluster-1",
			Status:    status,
		}).Error)
	case database.UpgradeJobKindBatchChild:
		batchUpgradeID := job.BatchUpgradeID
		require.NoError(t, db.Create(&datamodel.ClusterUpgradeJob{
			BaseModel:      datamodel.BaseModel{UUID: job.UUID, CreatedAt: job.CreatedAt},
			ClusterID:      job.ClusterID,
			Status:         status,
			BatchUpgradeID: &batchUpgradeID,
		}).Error)
	case database.UpgradeJobKindBatch:
		require.NoError(t, db.Create(&datamodel.BatchClusterUpgradeJob{
			BaseModel:     datamodel.BaseModel{UUID: job.UUID, CreatedAt: job.CreatedAt},
			Status:        status,
			TotalClusters: totalClusters,
		}).Error)
	}
}

// expectUpgradeTimeoutTransactionOutsideTerminate asserts the DB transaction
// completes before any Temporal TerminateWorkflow call.
func expectUpgradeTimeoutTransactionOutsideTerminate(
	t *testing.T,
	storage *database.MockStorage,
	temporal *workflowengine.MockTemporalTestClient,
	job database.PendingUpgradeJob,
	status string,
	totalClusters int,
) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&datamodel.ClusterUpgradeJob{},
		&datamodel.BatchClusterUpgradeJob{},
	))

	seedPendingUpgradeJobRow(t, db, job, status, totalClusters)

	txnDone := false
	storage.EXPECT().WithTransaction(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, fn func(dbutils.Transaction) error) error {
			tx := dbutils.NewMockTransaction(t)
			tx.EXPECT().GORM().Return(db).Maybe()
			err := fn(tx)
			txnDone = true
			return err
		})
	temporal.EXPECT().TerminateWorkflow(
		mock.Anything,
		mock.Anything,
		"",
		"Upgrade workflow pickup timeout exceeded",
	).Run(func(_ context.Context, _ string, _ string, _ string, _ ...interface{}) {
		require.True(t, txnDone, "TerminateWorkflow must not run while the DB transaction is open")
	}).Return(nil)
	return db
}

func TestUpgradeJobPickupSupervisor_Process_CommonTimeoutPath(t *testing.T) {
	for _, tc := range []struct {
		name          string
		job           database.PendingUpgradeJob
		totalClusters int
	}{
		{
			name: "single upgrade",
			job: database.PendingUpgradeJob{
				UUID:      "single-upgrade",
				Kind:      database.UpgradeJobKindSingle,
				CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
			},
		},
		{
			name: "batch child upgrade",
			job: database.PendingUpgradeJob{
				UUID:           "child-upgrade",
				Kind:           database.UpgradeJobKindBatchChild,
				CreatedAt:      time.Now().UTC().Add(-30 * time.Minute),
				BatchUpgradeID: "batch-1",
				ClusterID:      "cluster-7",
			},
		},
		{
			name: "batch upgrade",
			job: database.PendingUpgradeJob{
				UUID:      "batch-upgrade",
				Kind:      database.UpgradeJobKindBatch,
				CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
			},
			totalClusters: 3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			storage := database.NewMockStorage(t)
			temporal := workflowengine.NewMockTemporalTestClient(t)
			expectedWorkflowID := tc.job.UUID
			if tc.job.Kind == database.UpgradeJobKindBatchChild {
				expectedWorkflowID = "batch-1-child-cluster-7"
			}
			temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, expectedWorkflowID, "").
				Return(runningUpgradeWorkflowResponse(30*time.Minute), nil)
			db := expectUpgradeTimeoutTransactionOutsideTerminate(
				t, storage, temporal, tc.job, string(models.UpgradeStatusPending), tc.totalClusters,
			)

			supervisor := &upgradeJobPickupSupervisor{
				storage:  storage,
				temporal: temporal,
				config:   testUpgradeJobSupervisorConfig(),
			}
			supervisor.process(context.Background(), tc.job, time.Now().UTC().Add(-15*time.Minute))

			switch tc.job.Kind {
			case database.UpgradeJobKindSingle, database.UpgradeJobKindBatchChild:
				var persisted datamodel.ClusterUpgradeJob
				require.NoError(t, db.Unscoped().Where("uuid = ?", tc.job.UUID).First(&persisted).Error)
				assert.Equal(t, string(models.UpgradeStatusFailed), persisted.Status)
				assert.NotNil(t, persisted.CompletedAt)
				require.NotNil(t, persisted.ErrorDetails)
				assert.Equal(t, upgradePickupTimeoutErrorCode, persisted.ErrorDetails.ErrorCode)
			case database.UpgradeJobKindBatch:
				var persisted datamodel.BatchClusterUpgradeJob
				require.NoError(t, db.Unscoped().Where("uuid = ?", tc.job.UUID).First(&persisted).Error)
				assert.Equal(t, string(models.UpgradeStatusFailed), persisted.Status)
				assert.Equal(t, tc.totalClusters, persisted.SkippedCount)
				require.NotNil(t, persisted.ErrorDetails)
				assert.Equal(t, upgradePickupTimeoutErrorCode, persisted.ErrorDetails.ErrorCode)
			}
		})
	}
}

func TestUpgradeJobPickupSupervisor_Process_DescribeFailureDefersCleanup(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowengine.NewMockTemporalTestClient(t)
	job := database.PendingUpgradeJob{
		UUID:      "single-upgrade",
		Kind:      database.UpgradeJobKindSingle,
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
	}
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.UUID, "").
		Return(nil, errors.New("Temporal unavailable"))

	supervisor := &upgradeJobPickupSupervisor{
		storage:  storage,
		temporal: temporal,
		config:   testUpgradeJobSupervisorConfig(),
	}
	supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

	storage.AssertNotCalled(t, "WithTransaction", mock.Anything, mock.Anything)
	temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// A workflow that Temporal still reports as RUNNING owns its job, so a job left
// PENDING inside the running grace period must not be failed or terminated.
func TestUpgradeJobPickupSupervisor_Process_RunningWithinGraceDefersCleanup(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowengine.NewMockTemporalTestClient(t)
	job := database.PendingUpgradeJob{
		UUID:      "single-upgrade",
		Kind:      database.UpgradeJobKindSingle,
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
	}
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.UUID, "").
		Return(runningUpgradeWorkflowResponse(time.Minute), nil)

	supervisor := &upgradeJobPickupSupervisor{
		storage:  storage,
		temporal: temporal,
		config:   testUpgradeJobSupervisorConfig(),
	}
	supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

	storage.AssertNotCalled(t, "WithTransaction", mock.Anything, mock.Anything)
	temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// Child rows are committed by the wave activity before the parent starts the child
// workflows, so a missing child execution must not fail the row while the batch is
// still able to start it.
func TestUpgradeJobPickupSupervisor_Process_MissingChildWorkflowDefersWhileBatchActive(t *testing.T) {
	for name, parentStatus := range map[string]string{
		"batch pending":     string(models.UpgradeStatusPending),
		"batch in progress": string(models.UpgradeStatusInProgress),
	} {
		t.Run(name, func(t *testing.T) {
			storage := database.NewMockStorage(t)
			temporal := workflowengine.NewMockTemporalTestClient(t)
			job := database.PendingUpgradeJob{
				UUID:           "child-upgrade",
				Kind:           database.UpgradeJobKindBatchChild,
				CreatedAt:      time.Now().UTC().Add(-30 * time.Minute),
				BatchUpgradeID: "batch-1",
				ClusterID:      "cluster-7",
			}
			temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, "batch-1-child-cluster-7", "").
				Return(nil, serviceerror.NewNotFound("workflow not found"))
			storage.EXPECT().GetBatchClusterUpgradeJobByUUID(mock.Anything, "batch-1").
				Return(&datamodel.BatchClusterUpgradeJob{
					BaseModel: datamodel.BaseModel{UUID: "batch-1"},
					Status:    parentStatus,
				}, nil)

			supervisor := &upgradeJobPickupSupervisor{
				storage:  storage,
				temporal: temporal,
				config:   testUpgradeJobSupervisorConfig(),
			}
			supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

			storage.AssertNotCalled(t, "WithTransaction", mock.Anything, mock.Anything)
			temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// Once the batch is terminal or gone, nothing will ever start the child, so the
// orphaned row is retired.
func TestUpgradeJobPickupSupervisor_Process_MissingChildWorkflowFailsWhenBatchCannotStartIt(t *testing.T) {
	for name, parent := range map[string]struct {
		job *datamodel.BatchClusterUpgradeJob
		err error
	}{
		"batch failed": {job: &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusFailed),
		}},
		"batch completed": {job: &datamodel.BatchClusterUpgradeJob{
			BaseModel: datamodel.BaseModel{UUID: "batch-1"},
			Status:    string(models.UpgradeStatusCompleted),
		}},
		"batch row gone": {err: gorm.ErrRecordNotFound},
	} {
		t.Run(name, func(t *testing.T) {
			storage := database.NewMockStorage(t)
			temporal := workflowengine.NewMockTemporalTestClient(t)
			job := database.PendingUpgradeJob{
				UUID:           "child-upgrade",
				Kind:           database.UpgradeJobKindBatchChild,
				CreatedAt:      time.Now().UTC().Add(-30 * time.Minute),
				BatchUpgradeID: "batch-1",
				ClusterID:      "cluster-7",
			}
			temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, "batch-1-child-cluster-7", "").
				Return(nil, serviceerror.NewNotFound("workflow not found"))
			storage.EXPECT().GetBatchClusterUpgradeJobByUUID(mock.Anything, "batch-1").
				Return(parent.job, parent.err)
			db := expectUpgradeTimeoutTransaction(t, storage, job, string(models.UpgradeStatusPending), 0)

			supervisor := &upgradeJobPickupSupervisor{
				storage:  storage,
				temporal: temporal,
				config:   testUpgradeJobSupervisorConfig(),
			}
			supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

			var persisted datamodel.ClusterUpgradeJob
			require.NoError(t, db.Unscoped().Where("uuid = ?", job.UUID).First(&persisted).Error)
			assert.Equal(t, string(models.UpgradeStatusFailed), persisted.Status)
			temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// A transient failure reading the parent must not be mistaken for a finished batch.
func TestUpgradeJobPickupSupervisor_Process_ParentLookupFailureDefersChildCleanup(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowengine.NewMockTemporalTestClient(t)
	job := database.PendingUpgradeJob{
		UUID:           "child-upgrade",
		Kind:           database.UpgradeJobKindBatchChild,
		CreatedAt:      time.Now().UTC().Add(-30 * time.Minute),
		BatchUpgradeID: "batch-1",
		ClusterID:      "cluster-7",
	}
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, "batch-1-child-cluster-7", "").
		Return(nil, serviceerror.NewNotFound("workflow not found"))
	storage.EXPECT().GetBatchClusterUpgradeJobByUUID(mock.Anything, "batch-1").
		Return(nil, errors.New("connection reset"))

	supervisor := &upgradeJobPickupSupervisor{
		storage:  storage,
		temporal: temporal,
		config:   testUpgradeJobSupervisorConfig(),
	}
	supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

	storage.AssertNotCalled(t, "WithTransaction", mock.Anything, mock.Anything)
}

// A closed workflow can never claim its job, so the row is failed without a
// terminate call.
func TestUpgradeJobPickupSupervisor_Process_ClosedWorkflowFailsWithoutTerminate(t *testing.T) {
	for name, status := range map[string]enumspb.WorkflowExecutionStatus{
		"timed out":  enumspb.WORKFLOW_EXECUTION_STATUS_TIMED_OUT,
		"failed":     enumspb.WORKFLOW_EXECUTION_STATUS_FAILED,
		"terminated": enumspb.WORKFLOW_EXECUTION_STATUS_TERMINATED,
	} {
		t.Run(name, func(t *testing.T) {
			storage := database.NewMockStorage(t)
			temporal := workflowengine.NewMockTemporalTestClient(t)
			job := database.PendingUpgradeJob{
				UUID:      "single-upgrade",
				Kind:      database.UpgradeJobKindSingle,
				CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
			}
			db := expectUpgradeTimeoutTransaction(t, storage, job, string(models.UpgradeStatusPending), 0)
			temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.UUID, "").
				Return(closedUpgradeWorkflowResponse(status), nil)

			supervisor := &upgradeJobPickupSupervisor{
				storage:  storage,
				temporal: temporal,
				config:   testUpgradeJobSupervisorConfig(),
			}
			supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

			var persisted datamodel.ClusterUpgradeJob
			require.NoError(t, db.Unscoped().Where("uuid = ?", job.UUID).First(&persisted).Error)
			assert.Equal(t, string(models.UpgradeStatusFailed), persisted.Status)
			temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestUpgradeJobPickupSupervisor_Process_TerminateFailureAfterMarkStillSucceeds(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowengine.NewMockTemporalTestClient(t)
	job := database.PendingUpgradeJob{
		UUID:      "single-upgrade",
		Kind:      database.UpgradeJobKindSingle,
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
	}
	db := expectUpgradeTimeoutTransaction(t, storage, job, string(models.UpgradeStatusPending), 0)
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.UUID, "").
		Return(runningUpgradeWorkflowResponse(30*time.Minute), nil)
	temporal.EXPECT().TerminateWorkflow(
		mock.Anything,
		job.UUID,
		"",
		"Upgrade workflow pickup timeout exceeded",
	).Return(errors.New("temporal unavailable"))

	supervisor := &upgradeJobPickupSupervisor{
		storage:  storage,
		temporal: temporal,
		config:   testUpgradeJobSupervisorConfig(),
	}
	supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

	var persisted datamodel.ClusterUpgradeJob
	require.NoError(t, db.Unscoped().Where("uuid = ?", job.UUID).First(&persisted).Error)
	assert.Equal(t, string(models.UpgradeStatusFailed), persisted.Status)
	require.NotNil(t, persisted.ErrorDetails)
	assert.Equal(t, upgradePickupTimeoutErrorCode, persisted.ErrorDetails.ErrorCode)
}

func TestUpgradeJobPickupSupervisor_Process_StateChangeWinsRace(t *testing.T) {
	storage := database.NewMockStorage(t)
	temporal := workflowengine.NewMockTemporalTestClient(t)
	job := database.PendingUpgradeJob{
		UUID:      "single-upgrade",
		Kind:      database.UpgradeJobKindSingle,
		CreatedAt: time.Now().UTC().Add(-30 * time.Minute),
	}
	_ = expectUpgradeTimeoutTransaction(t, storage, job, string(models.UpgradeStatusInProgress), 0)
	temporal.EXPECT().DescribeWorkflowExecution(mock.Anything, job.UUID, "").
		Return(runningUpgradeWorkflowResponse(30*time.Minute), nil)

	supervisor := &upgradeJobPickupSupervisor{
		storage:  storage,
		temporal: temporal,
		config:   testUpgradeJobSupervisorConfig(),
	}
	supervisor.process(context.Background(), job, time.Now().UTC().Add(-15*time.Minute))

	temporal.AssertNotCalled(t, "TerminateWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}
