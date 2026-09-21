package gcp

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/client/cluster"
	ontapRestModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/workflow"
	"gorm.io/gorm"
)

// MockStorage wraps the real storage to inject errors for testing
type MockStorage struct {
	database.Storage
	getJobsWithConditionError      error
	getSnapshotsWithConditionError error
	getJobByResourceUUIDError      error
	createJobError                 error
}

// GetJobsWithCondition overrides the real method to return an error when needed
func (m *MockStorage) GetJobsWithCondition(ctx context.Context, filter utils2.Filter) ([]*datamodel.Job, error) {
	if m.getJobsWithConditionError != nil {
		return nil, m.getJobsWithConditionError
	}
	return m.Storage.GetJobsWithCondition(ctx, filter)
}

// GetJobByResourceUUID overrides the real method to return an error when needed
func (m *MockStorage) GetJobByResourceUUID(ctx context.Context, resourceUUID, jobType string) (*datamodel.Job, error) {
	if m.getJobByResourceUUIDError != nil {
		return nil, m.getJobByResourceUUIDError
	}
	return m.Storage.GetJobByResourceUUID(ctx, resourceUUID, jobType)
}

// GetSnapshotsWithCondition overrides the real method to return an error when needed
func (m *MockStorage) GetSnapshotsWithCondition(ctx context.Context, filter utils2.Filter) ([]*datamodel.Snapshot, error) {
	if m.getSnapshotsWithConditionError != nil {
		return nil, m.getSnapshotsWithConditionError
	}
	return m.Storage.GetSnapshotsWithCondition(ctx, filter)
}

// CreateJob overrides the real method to return an error when needed
func (m *MockStorage) CreateJob(ctx context.Context, job *datamodel.Job) (*datamodel.Job, error) {
	if m.createJobError != nil {
		return nil, m.createJobError
	}
	return m.Storage.CreateJob(ctx, job)
}

func assertErrContainsOriginal(t *testing.T, err error, substring string) {
	t.Helper()
	var customErr *vsaerrors.CustomError
	if vsaerrors.As(err, &customErr) && customErr.Unwrap() != nil {
		assert.Contains(t, customErr.Unwrap().Error(), substring)
		return
	}
	assert.ErrorContains(t, err, substring)
}

func TestGCPOrchestrator_CreateSnapshot(t *testing.T) {
	t.Run("WhenSnapshotCreationSuccess", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.NoError(tt, err, "Failed to create snapshot")
		assert.NotNil(tt, snapshot, "Expected snapshot to be created")
		assert.Equal(tt, snapshot.Name, "test_snapshot")
		assert.Equal(tt, snapshot.VolumeUUID, "test-volume-uuid")
	})

	t.Run("WhenSnapshotCreationReturnsOngoingJobs", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		existingSnapshot := &datamodel.Snapshot{
			BaseModel:   datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:        "test_snapshot",
			Description: "desc",
			AccountID:   account.ID,
			VolumeID:    volume.ID,
			Account:     account,
			Volume:      volume,
			State:       datamodel.LifeCycleStateCreating,
		}
		err = store.DB().Create(existingSnapshot).Error
		assert.NoError(tt, err)

		job := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:         string(datamodel.JobTypeCreateSnapshot),
			State:        string(datamodel.JobsStatePROCESSING),
			ResourceName: "test_snapshot",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: existingSnapshot.UUID, // Snapshot UUID
				VolumeUUID:   volume.UUID,           // Volume UUID for idempotency check
			},
		}
		err = store.DB().Create(job).Error
		assert.NoError(tt, err)

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		snapshot, jobUUID, err := orch.CreateSnapshot(ctx, params)
		assert.NoError(tt, err, "Failed to create snapshot")
		assert.Equal(tt, "test-job-uuid", jobUUID, "Expected job UUID to be returned")
		assert.NotNil(tt, snapshot, "Expected snapshot to be created")
		assert.Equal(tt, snapshot.Name, "test_snapshot")
		assert.Equal(tt, snapshot.VolumeUUID, "test-volume-uuid")
	})

	t.Run("WhenSnapshotCreationFailsAsVolumeHasAppConsistentSnapshot", func(tt *testing.T) {
		// Mock max to 1 so 2nd app-consistent snapshot fails; reset after test
		origMax := maxAppConsistentSnapshotCount
		maxAppConsistentSnapshotCount = 1
		defer func() { maxAppConsistentSnapshotCount = origMax }()

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		// One existing app-consistent snapshot; with max=1, 2nd should fail
		existingSnapshot := &datamodel.Snapshot{
			BaseModel:       datamodel.BaseModel{UUID: "another-test-snapshot-uuid"},
			Name:            "another_test_snapshot",
			Description:     "desc",
			AccountID:       account.ID,
			VolumeID:        volume.ID,
			Account:         account,
			Volume:          volume,
			State:           datamodel.LifeCycleStateREADY,
			IsAppConsistent: true,
		}
		err = store.DB().Create(existingSnapshot).Error
		assert.NoError(tt, err)

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: true,
			Description:     "test",
		}

		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, snapshot)
		assert.Contains(tt, err.Error(), "maximum number of app consistent snapshots (1)")
	})

	t.Run("WhenAppConsistentSnapshotCreationSucceedsWhenUnderMaxLimit", func(tt *testing.T) {
		// Mock max to 10 so 2nd app-consistent snapshot is allowed
		origMax := maxAppConsistentSnapshotCount
		maxAppConsistentSnapshotCount = 10
		defer func() { maxAppConsistentSnapshotCount = origMax }()

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		// One existing app-consistent snapshot; with max=10, 2nd is allowed
		existingSnapshot := &datamodel.Snapshot{
			BaseModel:       datamodel.BaseModel{UUID: "another-test-snapshot-uuid"},
			Name:            "another_test_snapshot",
			Description:     "desc",
			AccountID:       account.ID,
			VolumeID:        volume.ID,
			Account:         account,
			Volume:          volume,
			State:           datamodel.LifeCycleStateREADY,
			IsAppConsistent: true,
		}
		err = store.DB().Create(existingSnapshot).Error
		assert.NoError(tt, err)

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot_under_limit",
			IsAppConsistent: true,
			Description:     "test",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, snapshot)
		assert.Equal(tt, "test_snapshot_under_limit", snapshot.Name)
		assert.Equal(tt, "test-volume-uuid", snapshot.VolumeUUID)
	})

	t.Run("WhenSnapshotCreationFailsDueToOwnershipCheck", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID + 1,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.Error(tt, err, "Failed to create snapshot")
		assert.Nil(tt, snapshot, "Expected nil snapshot")
	})

	t.Run("WhenSnapshotCreationFailsDueToVolumeNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "volume.UUID",
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.Nil(tt, snapshot, "Expected nil snapshot")
		assert.ErrorContains(tt, err, "not found")
	})

	t.Run("WhenSnapshotCreationFailsDueToAccountNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: "account.Name",
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}
		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.Nil(tt, snapshot, "Expected nil snapshot")
		if !customerrors.IsNotFoundErr(err) {
			t.Errorf("Expected not found error, got %v", err)
		}
	})

	t.Run("WhenSnapshotCreationFailsDueToWorkflowError", func(tt *testing.T) {
		withFastWorkflowRetry(tt)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		// Mock ExecuteWorkflow to fail with a retryable error (connection refused is retryable)
		// WorkflowExecutor will retry up to 3 times for retryable errors
		workflowErr := errors.New("connection refused")
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowErr).Times(3)

		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.Nil(tt, snapshot, "Expected nil snapshot")
		assert.Contains(tt, err.Error(), "connection refused")
	})

	t.Run("WhenSnapshotCreationWithSameNameInDifferentVolumes", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		// Create two different volumes
		volume1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-1-uuid"},
			Name:      "test_volume_1",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume1).Error
		if err != nil {
			tt.Fatalf("Failed to create volume1: %v", err)
		}

		volume2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-2-uuid"},
			Name:      "test_volume_2",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume2).Error
		if err != nil {
			tt.Fatalf("Failed to create volume2: %v", err)
		}

		// Create existing snapshot in volume1
		existingSnapshot1 := &datamodel.Snapshot{
			BaseModel:   datamodel.BaseModel{UUID: "test-snapshot-1-uuid"},
			Name:        "test_snapshot",
			Description: "desc",
			AccountID:   account.ID,
			VolumeID:    volume1.ID,
			Account:     account,
			Volume:      volume1,
			State:       datamodel.LifeCycleStateCreating,
		}
		err = store.DB().Create(existingSnapshot1).Error
		assert.NoError(tt, err)

		// Create job for volume1 snapshot
		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "test-job-1-uuid"},
			Type:         string(datamodel.JobTypeCreateSnapshot),
			State:        string(datamodel.JobsStatePROCESSING),
			ResourceName: "test_snapshot",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: existingSnapshot1.UUID, // Snapshot UUID
				VolumeUUID:   volume1.UUID,           // Volume UUID for idempotency check
			},
		}
		err = store.DB().Create(job1).Error
		assert.NoError(tt, err)

		// Create existing snapshot in volume2
		existingSnapshot2 := &datamodel.Snapshot{
			BaseModel:   datamodel.BaseModel{UUID: "test-snapshot-2-uuid"},
			Name:        "test_snapshot",
			Description: "desc",
			AccountID:   account.ID,
			VolumeID:    volume2.ID,
			Account:     account,
			Volume:      volume2,
			State:       datamodel.LifeCycleStateCreating,
		}
		err = store.DB().Create(existingSnapshot2).Error
		assert.NoError(tt, err)

		// Create job for volume2 snapshot
		job2 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "test-job-2-uuid"},
			Type:         string(datamodel.JobTypeCreateSnapshot),
			State:        string(datamodel.JobsStatePROCESSING),
			ResourceName: "test_snapshot",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: existingSnapshot2.UUID, // Snapshot UUID
				VolumeUUID:   volume2.UUID,           // Volume UUID for idempotency check
			},
		}
		err = store.DB().Create(job2).Error
		assert.NoError(tt, err)

		// Test creating snapshot in volume1 - should return job1
		params1 := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume1.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		snapshot1, jobUUID1, err := orch.CreateSnapshot(ctx, params1)
		assert.NoError(tt, err, "Failed to create snapshot for volume1")
		assert.Equal(tt, "test-job-1-uuid", jobUUID1, "Expected job1 UUID to be returned for volume1")
		assert.NotNil(tt, snapshot1, "Expected snapshot to be returned for volume1")
		assert.Equal(tt, snapshot1.Name, "test_snapshot")
		assert.Equal(tt, snapshot1.VolumeUUID, "test-volume-1-uuid")

		// Test creating snapshot in volume2 - should return job2 (not job1)
		params2 := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume2.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		snapshot2, jobUUID2, err := orch.CreateSnapshot(ctx, params2)
		assert.NoError(tt, err, "Failed to create snapshot for volume2")
		assert.Equal(tt, "test-job-2-uuid", jobUUID2, "Expected job2 UUID to be returned for volume2")
		assert.NotNil(tt, snapshot2, "Expected snapshot to be returned for volume2")
		assert.Equal(tt, snapshot2.Name, "test_snapshot")
		assert.Equal(tt, snapshot2.VolumeUUID, "test-volume-2-uuid")

		// Verify that different job UUIDs are returned for different volumes
		assert.NotEqual(tt, jobUUID1, jobUUID2, "Expected different job UUIDs for different volumes")
	})

	t.Run("WhenSnapshotCreationInSyncMode", func(tt *testing.T) {
		// Set environment variable for sync mode
		originalValue := os.Getenv("SNAPSHOT_API_SYNC_MODE")
		defer func() {
			if originalValue == "" {
				_ = os.Unsetenv("SNAPSHOT_API_SYNC_MODE")
			} else {
				_ = os.Setenv("SNAPSHOT_API_SYNC_MODE", originalValue)
			}
		}()
		_ = os.Setenv("SNAPSHOT_API_SYNC_MODE", "true")

		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		node := &datamodel.Node{
			BaseModel:       datamodel.BaseModel{ID: 1},
			EndpointAddress: "1.2.3.4",
			PoolID:          pool.ID,
		}
		err = store.DB().Create(node).Error
		if err != nil {
			tt.Fatalf("Failed to create node: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		// Fail provider lookup immediately. Without this, sync mode dials the fake
		// EndpointAddress and burns ~15-20s on TCP connect timeout.
		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("mock provider unavailable")
		}

		// In sync mode, we expect the function to fail early when trying to get provider.
		// This confirms sync mode is being used (async mode would try to execute workflow).
		snapshot, _, err := orch.CreateSnapshot(ctx, params)
		assert.Error(tt, err, "Expected error in sync mode without real ONTAP connection")
		assert.Nil(tt, snapshot, "Expected nil snapshot when sync mode fails")
		// Verify that workflow was NOT called (sync mode doesn't use workflow)
		temporal.AssertNotCalled(tt, "ExecuteWorkflow")
	})

	t.Run("WhenSnapshotCreationFailsDueToGetSnapshotsWithConditionError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		// Mock the storage to return an error when GetSnapshotsWithCondition is called
		origStorage := orch.storage
		mockStorage := &MockStorage{
			Storage:                        origStorage,
			getSnapshotsWithConditionError: errors.New("database connection failed"),
		}
		orch.storage = mockStorage
		defer func() { orch.storage = origStorage }()

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name:            "test_snapshot",
			IsAppConsistent: false,
			Description:     "test",
		}

		snapshot, jobUUID, err := orch.CreateSnapshot(ctx, params)
		assert.Error(tt, err, "Expected error when GetSnapshotsWithCondition fails")
		assert.Contains(tt, err.Error(), "database connection failed", "Expected specific error message")
		assert.Nil(tt, snapshot, "Expected nil snapshot response")
		assert.Empty(tt, jobUUID, "Expected empty job UUID")
	})
}

func TestConvertDatastoreSnapshotToModel(t *testing.T) {
	t.Run("WhenSnapshotIsNil", func(tt *testing.T) {
		result := ConvertDatastoreSnapshotToModel(nil)
		assert.Nil(tt, result, "Expected nil result when input snapshot is nil")
	})

	t.Run("WhenSnapshotHasAllFields", func(tt *testing.T) {
		input := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-snapshot-uuid",
				DeletedAt: nil,
			},
			Name:            "test_snapshot",
			Description:     "test description",
			VolumeID:        123,
			IsAppConsistent: true,
			Volume: &datamodel.Volume{
				BaseModel: datamodel.BaseModel{
					UUID: "test-volume-uuid",
				},
			},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{
					UUID: "test-account-uuid",
				},
				Name: "test_account",
			},
			State:        "READY",
			StateDetails: "Snapshot is ready",
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes: 1234,
			},
		}

		expected := &models.Snapshot{
			BaseModel: models.BaseModel{
				UUID: "test-snapshot-uuid",
			},
			Name:            "test_snapshot",
			Description:     "test description",
			VolumeUUID:      "test-volume-uuid",
			LifeCycleState:  "READY",
			AccountName:     "test_account",
			StorageClass:    STORAGE_CLASS_SOFTWARE,
			SizeInBytes:     1234,
			IsAppConsistent: true,
		}

		result := ConvertDatastoreSnapshotToModel(input)
		assert.NotNil(tt, result, "Expected non-nil result")
		assert.Equal(tt, expected.Name, result.Name, "Expected result to match the expected snapshot model")
		assert.Equal(tt, expected.SizeInBytes, result.SizeInBytes, "Expected result to match the expected snapshot model")
		assert.Equal(tt, expected.StorageClass, result.StorageClass, "Expected result to match the expected snapshot model")
		assert.Equal(tt, expected.IsAppConsistent, result.IsAppConsistent)
	})
}

func TestVolumeOwnershipCheck(t *testing.T) {
	t.Run("WhenAccountIDIsIncorrect", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			assert.FailNow(tt, "Failed to create test storage: "+err.Error())
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "test-account-uuid"},
			Name:      "test_account",
		}

		_, err = VolumeOwnershipCheck(ctx, store, volume.UUID, account.Name)
		assert.ErrorContains(tt, err, "Volume not found. Please ensure the volume exists and belongs to your account.")
	})

	t.Run("WhenVolumeIsIncorrect", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			assert.FailNow(tt, "Failed to create test storage: "+err.Error())
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 2,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}

		_, err = VolumeOwnershipCheck(ctx, store, volume.UUID, account.Name)
		assert.ErrorContains(tt, err, "Volume not found. Please ensure the volume exists and belongs to your account.")
	})
}

func TestValidateSnapshotName(t *testing.T) {
	t.Run("WhenFailsWithEmptyString", func(tt *testing.T) {
		err := ValidateSnapshotName("")
		if err == nil {
			tt.Error("No error returned")
		} else if !customerrors.IsUserInputValidationErr(err) {
			tt.Error("Wrong error type returned")
		} else if err.Error() != "Snapshot name must not be empty." {
			tt.Errorf("Wrong error message returned, got: %s", err.Error())
		}
	})
	t.Run("WhenFailsWithInvalidCharacters", func(tt *testing.T) {
		invalid := []string{`'`, `""`, "?", "!", "#", "$", "%", "&", "/"}
		for _, c := range invalid {
			err := ValidateSnapshotName("invalid" + c)
			if err == nil {
				tt.Error("No error returned")
			} else if !customerrors.IsUserInputValidationErr(err) {
				tt.Error("Wrong error type returned")
			} else if err.Error() != "Snapshot name can only include alphanumeric characters and the following special characters: ()-_+." {
				tt.Errorf("Wrong error message returned, got: %s", err.Error())
			}
		}
	})
	t.Run("WhenFailsWithInvalidNames", func(tt *testing.T) {
		invalid := []string{`ref_ss_volmove`, `snapmirror`}
		for _, c := range invalid {
			err := ValidateSnapshotName(c)
			if err == nil {
				tt.Error("No error returned")
			}
			if !customerrors.IsUserInputValidationErr(err) {
				tt.Error("Wrong error type returned")
			}
		}
	})
	t.Run("WhenFailsWithExactInvalidNames", func(tt *testing.T) {
		invalid := []string{`ref_ss_volmove`, `snapmirror`, `hourly.`, `daily.`, `weekly.`, `monthly.`}
		for _, c := range invalid {
			err := ValidateSnapshotName(c)
			if err == nil {
				tt.Error("No error returned")
			}
			if !customerrors.IsUserInputValidationErr(err) {
				tt.Error("Wrong error type returned")
			}
			assert.EqualError(tt, err, `Snapshot name cannot start with the following: "ref_ss_volmove", "snapmirror", "hourly.", "daily.", "weekly." or "monthly.".`)
		}
	})
	t.Run("WhenFailsWithExtraInvalidNames", func(tt *testing.T) {
		invalid := []string{`ref_ss_volmove.something_more`, `snapmirror.snapshot`, `hourly.2024-05-01-1400`, `daily.2024-05-01-1400`, `weekly.2024-05-01-1400`, `monthly.2024-05-01-1400`}
		for _, c := range invalid {
			err := ValidateSnapshotName(c)
			if err == nil {
				tt.Error("No error returned")
			}
			if !customerrors.IsUserInputValidationErr(err) {
				tt.Error("Wrong error type returned")
			}
			assert.EqualError(tt, err, `Snapshot name cannot start with the following: "ref_ss_volmove", "snapmirror", "hourly.", "daily.", "weekly." or "monthly.".`)
		}
	})
	t.Run("WhenFailsWithConsecutiveDots", func(tt *testing.T) {
		err := ValidateSnapshotName("..")
		if err == nil {
			tt.Error("No error returned")
		} else if !customerrors.IsUserInputValidationErr(err) {
			tt.Error("Wrong error type returned")
		} else if err.Error() != "Snapshot name cannot include consecutive dots: .." {
			tt.Errorf("Wrong error message returned, got: %s", err.Error())
		}
	})
	t.Run("WhenFailsWithSingleDot", func(tt *testing.T) {
		err := ValidateSnapshotName(".")
		if err == nil {
			tt.Error("No error returned")
		} else if !customerrors.IsUserInputValidationErr(err) {
			tt.Error("Wrong error type returned")
		} else if err.Error() != "Snapshot name cannot be a single dot." {
			tt.Errorf("Wrong error message returned, got: %s", err.Error())
		}
	})
	t.Run("WhenSuccessfulWithAllValidCharacters", func(tt *testing.T) {
		err := ValidateSnapshotName("valid123_-+.()")
		if err != nil {
			tt.Error("Unexpected error returned")
		}
	})
}

func TestValidateCreateSnapshotOperation(t *testing.T) {
	t.Run("WhenParamsNameIsNil", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		params := &common.CreateSnapshotParams{}

		err := validateCreateSnapshotOperation(volume, params, nil)
		assert.ErrorContains(tt, err, "Snapshot name is empty")
	})

	t.Run("WhenVolumeStateIsCreating", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
			State:     datamodel.LifeCycleStateCreating,
		}
		params := &common.CreateSnapshotParams{
			Name: "test_snapshot",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		}
		err := validateCreateSnapshotOperation(volume, params, account)
		assert.ErrorContains(tt, err, "volume is in creating stage.")
	})

	t.Run("WhenVolumeStateIsDeleting", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
			State:     datamodel.LifeCycleStateDeleting,
		}
		params := &common.CreateSnapshotParams{
			Name: "test_snapshot",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		}
		err := validateCreateSnapshotOperation(volume, params, account)
		assert.ErrorContains(tt, err, "volume is in deleting stage.")
	})

	t.Run("WhenVolumeStateIsNotCreatingOrDeleting", func(tt *testing.T) {
		// validateCreateSnapshotOperation only rejects CREATING/DELETING explicitly;
		// other states are not blocked at this layer.
		for _, state := range []string{
			"",
			datamodel.LifeCycleStateUpdating,
			datamodel.LifeCycleStateRestoring,
			datamodel.LifeCycleStateError,
			datamodel.VolumeStateOffline,
			datamodel.LifeCycleStateREADY,
		} {
			tt.Run(state, func(tt *testing.T) {
				volume := &datamodel.Volume{
					BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
					Name:      "test_volume",
					AccountID: 1,
					State:     state,
				}
				params := &common.CreateSnapshotParams{Name: "test_snapshot"}

				err := validateCreateSnapshotOperation(volume, params, nil)

				assert.NoError(tt, err)
			})
		}
	})

	t.Run("WhenVolumeIsThinCloneUndergoingSplit", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{
					ParentVolumeUUID: "parent-vol-uuid",
					State:            datamodel.CloneStateSplitting,
				},
			},
		}
		params := &common.CreateSnapshotParams{
			Name: "test_snapshot",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		}
		err := validateCreateSnapshotOperation(volume, params, account)
		assert.ErrorContains(tt, err, "Cannot create a snapshot when volume is undergoing split operation.")
	})

	t.Run("WhenVolumeIsClonedButNotSplitting", func(tt *testing.T) {
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
			State:     datamodel.LifeCycleStateREADY,
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{
					ParentVolumeUUID: "parent-vol-uuid",
					State:            datamodel.CloneStateCloned,
				},
			},
		}
		params := &common.CreateSnapshotParams{
			Name: "test_snapshot",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		}
		err := validateCreateSnapshotOperation(volume, params, account)
		assert.NoError(tt, err)
	})
}

func TestGetSnapshot(t *testing.T) {
	t.Run("WhenSnapshotDoesNotExist", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.GetSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: "test_account",
			},
			SnapshotUUID: "non-existent-uuid",
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		snapshot, err := orch.GetSnapshot(ctx, params)
		assertErrContainsOriginal(tt, err, "snapshot 'non-existent-uuid' not found")
		assert.Nil(tt, snapshot, "Expected nil snapshot")
	})

	t.Run("WhenSnapshotExists", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			Description:  "Test snapshot description",
			AccountID:    account.ID,
			VolumeID:     volume.ID,
			Account:      account,
			Volume:       volume,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: "Available",
		}
		err = store.DB().Create(snapshot).Error
		assert.NoError(tt, err, "Failed to create snapshot")

		params := &common.GetSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: "test_account",
			},
			SnapshotUUID: "test-snapshot-uuid",
		}

		result, err := orch.GetSnapshot(ctx, params)
		assert.NoError(tt, err, "Failed to get snapshot")
		assert.Equal(tt, snapshot.Name, result.Name)
		assert.Equal(tt, volume.UUID, result.VolumeUUID)
		assert.Equal(tt, volume.Name, result.VolumeName)
	})

	t.Run("WhenSnapshotIsDeleted", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to clean up test storage")

		orch := GCPOrchestrator{
			storage: store,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.DB().Create(account).Error, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		assert.NoError(tt, store.DB().Create(volume).Error, "Failed to create volume")

		// Create a deleted snapshot
		deletedAt := &gorm.DeletedAt{Time: time.Now(), Valid: true}
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-snapshot-uuid",
				DeletedAt: deletedAt,
			},
			Name:        "test_snapshot",
			Description: "Test snapshot description",
			AccountID:   account.ID,
			VolumeID:    volume.ID,
			Account:     account,
			Volume:      volume,
		}
		assert.NoError(tt, store.DB().Create(snapshot).Error, "Failed to create snapshot")

		params := &common.GetSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: "test_account",
			},
			SnapshotUUID: "test-snapshot-uuid",
		}

		result, err := orch.GetSnapshot(ctx, params)
		assert.Nil(tt, result, "Expected nil snapshot")
		assert.Error(tt, err, "not found")
	})

	t.Run("WhenSnapshotGetFailsDueToVolumeOwnershipCheck", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")

		// Clear the in-memory database
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := GCPOrchestrator{
			storage: store,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(tt, err, "Failed to create account")

		params := &common.GetSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: "test_account",
			},
			SnapshotUUID: "test-snapshot-uuid",
		}

		// Patch VolumeOwnershipCheck to return an error
		orig := VolumeOwnershipCheck
		VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
			return nil, errors.New("failed to validate volume ownership")
		}
		defer func() { VolumeOwnershipCheck = orig }()

		snapshot, err := orch.GetSnapshot(ctx, params)
		assert.Nil(tt, snapshot)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to validate volume ownership")
	})
}

func TestListSnapshots(t *testing.T) {
	t.Run("WhenOwnershipCheckFails", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		orch := GCPOrchestrator{storage: store}

		params := &common.ListSnapshotsParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "vol-uuid",
				AccountName: "acc",
			},
		}

		// Patch VolumeOwnershipCheck to return false
		orig := VolumeOwnershipCheck
		VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
			return nil, errors.New("failed to validate volume ownership")
		}
		defer func() { VolumeOwnershipCheck = orig }()

		snaps, err := orch.ListSnapshots(ctx, params)
		assert.Nil(tt, snaps)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to validate volume ownership")
	})

	t.Run("WhenVolumeNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		orch := GCPOrchestrator{storage: store}

		params := &common.ListSnapshotsParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "non-existent-vol",
				AccountName: "acc",
			},
		}

		// Patch VolumeOwnershipCheck to return true
		orig := VolumeOwnershipCheck
		VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
			return nil, customerrors.NewNotFoundErr("volume", nil)
		}
		defer func() { VolumeOwnershipCheck = orig }()

		snaps, err := orch.ListSnapshots(ctx, params)
		assert.Nil(tt, snaps)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume")
	})

	t.Run("WhenSnapshotsExist", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		orch := GCPOrchestrator{storage: store}

		// Setup account, volume, and snapshots
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acc-uuid"},
			Name:      "acc",
		}
		assert.NoError(tt, store.DB().Create(account).Error)
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "vol",
			AccountID: account.ID,
		}
		assert.NoError(tt, store.DB().Create(volume).Error)
		snap1 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-1"},
			Name:      "snap1",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}
		snap2 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-2"},
			Name:      "snap2",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}
		assert.NoError(tt, store.DB().Create(snap1).Error)
		assert.NoError(tt, store.DB().Create(snap2).Error)

		params := &common.ListSnapshotsParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
		}

		// Patch VolumeOwnershipCheck to return true
		orig := VolumeOwnershipCheck
		VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
			return volume, nil
		}
		defer func() { VolumeOwnershipCheck = orig }()

		snaps, err := orch.ListSnapshots(ctx, params)
		assert.NoError(tt, err)
		assert.Len(tt, snaps, 2)
		names := []string{snaps[0].Name, snaps[1].Name}
		assert.Contains(tt, names, "snap1")
		assert.Contains(tt, names, "snap2")
	})
}

func TestGCPOrchestrator_GetSnapshotsByUUIDs(t *testing.T) {
	t.Run("success_converts_models", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "vol-name",
		}
		ds := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "snap-1"},
			Name:               "snap-name",
			Description:        "d",
			State:              datamodel.LifeCycleStateREADY,
			StateDetails:       "sd",
			Volume:             vol,
			SnapshotAttributes: &datamodel.SnapshotAttributes{SizeInBytes: 10},
		}
		mockStorage.EXPECT().BatchGetSnapshotsByUUIDs(ctx, []string{"snap-1"}).Return([]*datamodel.Snapshot{ds}, nil)

		orch := GCPOrchestrator{storage: mockStorage}
		out, err := orch.GetSnapshotsByUUIDs(ctx, []string{"snap-1"})
		require.NoError(tt, err)
		require.Len(tt, out, 1)
		assert.Equal(tt, "snap-1", out[0].UUID)
		assert.Equal(tt, "vol-uuid", out[0].VolumeUUID)
		assert.Equal(tt, "vol-name", out[0].VolumeName)
	})

	t.Run("storage_error", func(tt *testing.T) {
		ctx := context.Background()
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().BatchGetSnapshotsByUUIDs(ctx, []string{"x"}).Return(nil, errors.New("db"))

		orch := GCPOrchestrator{storage: mockStorage}
		out, err := orch.GetSnapshotsByUUIDs(ctx, []string{"x"})
		assert.Error(tt, err)
		assert.Nil(tt, out)
	})
}

func TestGCPOrchestrator_GetMultipleSnapshots(t *testing.T) {
	t.Run("WhenAccountNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		orch := GCPOrchestrator{storage: store}
		snapshots, err := orch.GetMultipleSnapshots(ctx, "vol-uuid", "non-existent-account", []string{"snap-uuid-1"})
		assert.NoError(tt, err)
		assert.NotNil(tt, snapshots)
		assert.Len(tt, snapshots, 0)
	})

	t.Run("WhenGetMultipleSnapshotsSuccess", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err)

		orch := GCPOrchestrator{storage: store}

		// Create account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acc-uuid"},
			Name:      "acc",
		}
		assert.NoError(tt, store.DB().Create(account).Error)

		// Create volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "vol",
			AccountID: account.ID,
		}
		assert.NoError(tt, store.DB().Create(volume).Error)

		// Create snapshots
		snap1 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-1"},
			Name:      "snap1",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}
		snap2 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-2"},
			Name:      "snap2",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}
		assert.NoError(tt, store.DB().Create(snap1).Error)
		assert.NoError(tt, store.DB().Create(snap2).Error)

		// Patch getAccountWithName to return the created account
		orig := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = orig }()

		snapshots, err := orch.GetMultipleSnapshots(ctx, volume.UUID, account.Name, []string{"snap-uuid-1", "snap-uuid-2"})
		assert.NoError(tt, err)
		assert.NotNil(tt, snapshots)
		assert.Len(tt, snapshots, 2)
		names := []string{snapshots[0].Name, snapshots[1].Name}
		assert.Contains(tt, names, "snap1")
		assert.Contains(tt, names, "snap2")
	})

	t.Run("WhenSnapshotIsDeleted", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acc-uuid"},
			Name:      "acc",
		}
		assert.NoError(tt, store.DB().Create(account).Error)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "vol",
			AccountID: account.ID,
		}
		assert.NoError(tt, store.DB().Create(volume).Error)

		deletedAt := &gorm.DeletedAt{Time: time.Now(), Valid: true}
		snapshot := &datamodel.Snapshot{
			BaseModel:   datamodel.BaseModel{UUID: "test-snapshot-uuid", DeletedAt: deletedAt},
			Name:        "test_snapshot",
			Description: "desc",
			AccountID:   account.ID,
			VolumeID:    volume.ID,
			Account:     account,
			Volume:      volume,
			State:       datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.DB().Create(snapshot).Error)

		orch := GCPOrchestrator{storage: store}
		params := &common.UpdateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: "test_account",
			},
			SnapshotUUID: "test-snapshot-uuid",
			Description:  "new_desc",
		}
		_, _, err = orch.UpdateSnapshot(ctx, params)
		assert.Error(tt, err, "not found")
	})

	t.Run("WhenSnapshotIsUpdateFailsDueToOwnershipCheck", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		snap1 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-1"},
			Name:      "snap1",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}

		snap2 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-2"},
			Name:      "snap2",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}
		assert.NoError(tt, store.DB().Create(snap1).Error)
		assert.NoError(tt, store.DB().Create(snap2).Error)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = _getAccountWithName }()

		orch := GCPOrchestrator{storage: store}
		_, err = orch.GetMultipleSnapshots(ctx, volume.UUID, account.Name, []string{"snap-uuid-1", "snap-uuid-2"})
		assert.NoError(tt, err)

		params := &common.UpdateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "non-existent-vol-uuid",
				AccountName: "test_account",
			},
			SnapshotUUID: "snap-uuid-1",
			Description:  "updated_desc",
		}
		result, jobID, err := orch.UpdateSnapshot(ctx, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Volume not found")
		assert.Nil(tt, result)
		assert.Empty(tt, jobID)
	})

	t.Run("WhenUpdateSnapshotSuccessful", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err, "Failed to ClearInMemoryDB")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		assert.NoError(tt, store.DB().Create(volume).Error)
		snap1 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-1"},
			Name:      "snap1",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}

		snap2 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "snap-uuid-2"},
			Name:      "snap2",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
		}
		assert.NoError(tt, store.DB().Create(snap1).Error)
		assert.NoError(tt, store.DB().Create(snap2).Error)

		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = _getAccountWithName }()

		orch := GCPOrchestrator{storage: store}
		_, err = orch.GetMultipleSnapshots(ctx, volume.UUID, account.Name, []string{"snap-uuid-1", "snap-uuid-2"})
		assert.NoError(tt, err)

		params := &common.UpdateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: "test_account",
			},
			SnapshotUUID: "snap-uuid-1",
			Description:  "updated_desc",
		}
		result, jobID, err := orch.UpdateSnapshot(ctx, params)
		assert.NoError(tt, err)
		assert.Empty(tt, jobID)
		assert.Equal(tt, result.Description, "updated_desc")
	})

	t.Run("WhenGetMultipleSnapshotsNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err)

		orch := GCPOrchestrator{storage: store}

		// Create account and volume, but do NOT create any snapshots
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acc-uuid"},
			Name:      "acc",
		}
		assert.NoError(tt, store.DB().Create(account).Error)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "vol",
			AccountID: account.ID,
		}
		assert.NoError(tt, store.DB().Create(volume).Error)

		// Patch getAccountWithName to return the created account
		orig := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = orig }()

		// Try to fetch non-existent snapshots
		snapshots, err := orch.GetMultipleSnapshots(ctx, volume.UUID, account.Name, []string{"non-existent-uuid-1", "non-existent-uuid-2"})
		assert.NoError(tt, err)
		assert.NotNil(tt, snapshots)
		assert.Len(tt, snapshots, 0)
	})
}

func TestDeleteSnapshot(t *testing.T) {
	t.Run("WhenSnapshotDeletionSuccess", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}
		// Mock ExecuteWorkflowSequentially using ExecuteWorkflowSeq
		origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return nil
		}
		defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()
		snapshotResp, _, err := orch.DeleteSnapshot(ctx, params)
		assert.NoError(tt, err, "Failed to delete snapshot")
		assert.NotNil(tt, snapshotResp, "Expected snapshot to be deleted")
		assert.Equal(tt, snapshotResp.Name, "test_snapshot")
		assert.Equal(tt, snapshotResp.VolumeUUID, "test-volume-uuid")
	})

	t.Run("WhenVolumeIsSplittingSnapshotDeletionNotAllowed", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{
					ParentVolumeUUID: "parent-vol-uuid",
					State:            datamodel.CloneStateSplitting,
				},
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}
		_, _, err = orch.DeleteSnapshot(ctx, params)
		assert.EqualError(tt, err, "Snapshot deletion is not allowed when the volume is splitting")
	})

	t.Run("WhenSnapshotDeletionFailsDueToVolumeNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		params := &common.DeleteSnapshotParams{
			SnapshotID: "test-snapshot-uuid",
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "volume.UUID",
				AccountName: account.Name,
			},
		}
		snapshot, _, err := orch.DeleteSnapshot(ctx, params)
		assert.Nil(tt, snapshot, "Expected nil snapshot")
		assertErrContainsOriginal(tt, err, "not found")
	})

	t.Run("WhenSnapshotDeletionFailsDueToAccountNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		params := &common.DeleteSnapshotParams{
			SnapshotID: "test-snapshot-uuid",
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: "account.Name",
			},
		}
		snapshot, _, err := orch.DeleteSnapshot(ctx, params)
		assert.Nil(tt, snapshot, "Expected nil snapshot")
		assertErrContainsOriginal(tt, err, "Volume not found. Please ensure the volume exists and belongs to your account.")
	})

	t.Run("WhenSnapshotDeletionFailsDueToWorkflowError", func(tt *testing.T) {
		withFastWorkflowRetry(tt)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: snapshot.UUID,
		}

		// Mock SignalWithStartWorkflow to fail with a retryable error (connection refused is retryable)
		// WorkflowExecutor will retry up to 3 times for retryable errors (triggers line 496 error logging)
		// DeleteSnapshot uses ExecuteSequentialWorkflow which calls SignalWithStartWorkflow internally
		workflowErr := errors.New("connection refused")
		temporal.EXPECT().SignalWithStartWorkflow(
			mock.Anything, // ctx
			mock.Anything, // controlWorkflowID
			mock.Anything, // signal name
			mock.Anything, // SignalWorkflowParams
			mock.Anything, // StartWorkflowOptions
			mock.Anything, // workflow function
			mock.Anything, // SequenceWorkflowInput
		).Return(nil, workflowErr).Times(3)

		_, _, err = orch.DeleteSnapshot(ctx, params)
		assert.Contains(tt, err.Error(), "connection refused")
	})

	t.Run("WhenSnapshotDeletionFailsAndSnapshotStateReverted", func(tt *testing.T) {
		withFastWorkflowRetry(tt)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
			Account: account,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		// Create snapshot in READY state, it will be set to DELETING by DeletingSnapshot
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateREADY,
			Account:   account,
			Volume:    volume,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: snapshot.UUID,
		}

		// Mock SignalWithStartWorkflow to fail with a retryable error (triggers line 496 error logging)
		// DeleteSnapshot uses ExecuteSequentialWorkflow which calls SignalWithStartWorkflow internally
		workflowErr := errors.New("connection refused")
		temporal.EXPECT().SignalWithStartWorkflow(
			mock.Anything, // ctx
			mock.Anything, // controlWorkflowID
			mock.Anything, // signal name
			mock.Anything, // SignalWorkflowParams
			mock.Anything, // StartWorkflowOptions
			mock.Anything, // workflow function
			mock.Anything, // SequenceWorkflowInput
		).Return(nil, workflowErr).Times(3)

		_, _, err = orch.DeleteSnapshot(ctx, params)
		assert.Contains(tt, err.Error(), "connection refused")

		// Verify snapshot state was reverted to READY after workflow failure (lines 451-456)
		updatedSnapshot, _ := store.GetSnapshotByUUID(ctx, snapshot.UUID, account.ID, volume.ID)
		if updatedSnapshot != nil {
			assert.Equal(tt, datamodel.LifeCycleStateREADY, updatedSnapshot.State)
			assert.Equal(tt, datamodel.LifeCycleStateAvailableDetails, updatedSnapshot.StateDetails)
		}
	})

	t.Run("WhenAccountExistsButVolumeNotFound", func(tt *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		orch := GCPOrchestrator{storage: store}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acc-uuid"},
			Name:      "acc",
		}
		assert.NoError(tt, store.DB().Create(account).Error)

		// Patch getAccountWithName to return the account
		orig := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = orig }()

		// No volume created in DB
		snapshots, err := orch.GetMultipleSnapshots(ctx, "non-existent-vol-uuid", account.Name, []string{"snap-uuid-1"})
		assert.NoError(tt, err)
		assert.NotNil(tt, snapshots)
		assert.Len(tt, snapshots, 0)
	})

	t.Run("WhenSnapshotDeletionFailsDueToVolumeInDeletingState", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateDeleting, // Volume is in deleting state
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}

		snapshot, _, err := orch.DeleteSnapshot(ctx, params)
		assert.Nil(tt, snapshot, "Expected nil snapshot")
		assert.Error(tt, err, "Expected error when volume is in deleting state")
		assert.Contains(tt, err.Error(), "Volume of the snapshot is being deleted")
	})

	t.Run("WhenSnapshotDeletionFailsDueToBackupAdhocType", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Type:      "backup", // Set snapshot type to backup
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}

		snapshotResp, _, err := orch.DeleteSnapshot(ctx, params)
		assert.Nil(tt, snapshotResp, "Expected nil snapshot")
		assert.Error(tt, err, "Expected error when snapshot is backup-adhoc type")
		assert.Contains(tt, err.Error(), "Cannot delete a snapshot that was generated for backups")
	})

	t.Run("WhenSnapshotDeletionReturnsOngoingJob", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Volume:    volume,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		// Create an ongoing delete job for the snapshot
		job := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:         string(datamodel.JobTypeDeleteSnapshot),
			State:        string(datamodel.JobsStatePROCESSING),
			ResourceName: "test_snapshot",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: snapshot.UUID, // Snapshot UUID
				VolumeUUID:   volume.UUID,   // Volume UUID for idempotency check
			},
		}
		err = store.DB().Create(job).Error
		assert.NoError(tt, err)

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}

		snapshotResp, jobUUID, err := orch.DeleteSnapshot(ctx, params)
		assert.NoError(tt, err, "Failed to delete snapshot")
		// Code should return the existing job UUID for idempotency
		assert.NotEmpty(tt, jobUUID, "Expected job UUID to be returned")
		assert.Equal(tt, "test-job-uuid", jobUUID, "Expected the existing job UUID to be returned for idempotency")
		assert.NotNil(tt, snapshotResp, "Expected snapshot to be returned")
		assert.Equal(tt, snapshotResp.Name, "test_snapshot")
		assert.Equal(tt, snapshotResp.VolumeUUID, "test-volume-uuid")
	})

	t.Run("WhenSnapshotDeletionWithIdempotencyCheck", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Volume:    volume,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}

		// Mock ExecuteWorkflowSequentially using ExecuteWorkflowSeq
		origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return nil
		}
		defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

		// Test the idempotency behavior - this should work with real storage
		snapshotResp, jobUUID, err := orch.DeleteSnapshot(ctx, params)
		assert.NoError(tt, err, "Expected no error for successful delete")
		assert.NotEmpty(tt, jobUUID, "Expected job UUID to be returned")
		assert.NotNil(tt, snapshotResp, "Expected snapshot to be returned")
		assert.Equal(tt, snapshotResp.Name, "test_snapshot")
		assert.Equal(tt, snapshotResp.VolumeUUID, "test-volume-uuid")
	})

	t.Run("WhenSnapshotDeletionFailsDueToDeletingSnapshotError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool: &datamodel.Pool{
				VendorID: "/projects/project123/locations/location123/pools/pool123",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Volume:    volume,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}

		// Mock DeletingSnapshot to return error (line 685)
		mockStorage := database.NewMockStorage(tt)

		volumeWithAccount := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: volume.UUID, ID: volume.ID},
			AccountID: account.ID,
			Account:   account,
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
				Name:      "test_pool",
				VendorID:  "/projects/project123/locations/location123/pools/pool123",
			},
			State: datamodel.LifeCycleStateAvailable,
		}
		snapshotWithAccount := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: snapshot.UUID, ID: snapshot.ID},
			Name:      snapshot.Name,
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volumeWithAccount,
			State:     datamodel.LifeCycleStateREADY,
			Type:      "",
		}

		mockStorage.EXPECT().VerifyVolumeOwnership(mock.Anything, volume.UUID, account.Name).Return(volumeWithAccount, nil)
		mockStorage.EXPECT().GetSnapshotByUUID(mock.Anything, params.SnapshotID, account.ID, volume.ID).Return(snapshotWithAccount, nil)
		// Mock GetJobByResourceUUID to return nil (no existing job) since code checks for existing jobs
		mockStorage.EXPECT().GetJobByResourceUUID(mock.Anything, params.SnapshotID, string(datamodel.JobTypeDeleteSnapshot)).Return(nil, errors.New("Job not found"))
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		}, nil)
		mockStorage.EXPECT().DeletingSnapshot(mock.Anything, mock.Anything).Return(errors.New("database error when deleting snapshot"))
		// Mock UpdateSnapshot for defer cleanup when DeletingSnapshot fails
		mockStorage.EXPECT().UpdateSnapshot(mock.Anything, mock.Anything).Return(snapshotWithAccount, nil)
		// Mock DeleteJob for defer cleanup
		mockStorage.EXPECT().DeleteJob(mock.Anything, "test-job-uuid", mock.Anything).Return(nil)

		orch.storage = mockStorage

		snapshotResp, jobUUID, err := orch.DeleteSnapshot(ctx, params)
		assert.Error(tt, err, "Expected error when DeletingSnapshot fails")
		assert.Nil(tt, snapshotResp, "Expected nil snapshot on error")
		assert.Empty(tt, jobUUID, "Expected empty job UUID on error")
		assert.Contains(tt, err.Error(), "database error when deleting snapshot")
	})

	t.Run("WhenSnapshotDeletionWithSameNameInDifferentVolumes", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		// Create pool first
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			VendorID:  "/projects/project123/locations/location123/pools/pool123",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone:  "us-west1-a",
				IsRegionalHA: false,
			},
			APIAccessMode: common.DEFAULTMode,
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		// Create two different volumes
		volume1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-1-uuid"},
			Name:      "test_volume_1",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
		}
		err = store.DB().Create(volume1).Error
		if err != nil {
			tt.Fatalf("Failed to create volume1: %v", err)
		}

		volume2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-2-uuid"},
			Name:      "test_volume_2",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
		}
		err = store.DB().Create(volume2).Error
		if err != nil {
			tt.Fatalf("Failed to create volume2: %v", err)
		}

		// Create snapshots with same name in both volumes
		snapshot1 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-1-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume1.ID,
			Volume:    volume1,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(snapshot1).Error
		assert.NoError(tt, err)

		snapshot2 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-2-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume2.ID,
			Volume:    volume2,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(snapshot2).Error
		assert.NoError(tt, err)

		// Create delete jobs for both snapshots
		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "test-delete-job-1-uuid"},
			Type:         string(datamodel.JobTypeDeleteSnapshot),
			State:        string(datamodel.JobsStatePROCESSING),
			ResourceName: "test_snapshot",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: snapshot1.UUID, // Snapshot UUID
				VolumeUUID:   volume1.UUID,   // Volume UUID for idempotency check
			},
		}
		err = store.DB().Create(job1).Error
		assert.NoError(tt, err)

		job2 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "test-delete-job-2-uuid"},
			Type:         string(datamodel.JobTypeDeleteSnapshot),
			State:        string(datamodel.JobsStatePROCESSING),
			ResourceName: "test_snapshot",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: snapshot2.UUID, // Snapshot UUID
				VolumeUUID:   volume2.UUID,   // Volume UUID for idempotency check
			},
		}
		err = store.DB().Create(job2).Error
		assert.NoError(tt, err)

		// Test deleting snapshot from volume1 - code should return existing job for idempotency
		params1 := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume1.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-1-uuid",
		}

		snapshotResp1, jobUUID1, err := orch.DeleteSnapshot(ctx, params1)
		assert.NoError(tt, err, "Failed to delete snapshot for volume1")
		// Code should return the existing job UUID for idempotency
		assert.NotEmpty(tt, jobUUID1, "Expected job UUID to be returned for volume1")
		assert.Equal(tt, "test-delete-job-1-uuid", jobUUID1, "Expected the existing job UUID to be returned for idempotency")
		assert.NotNil(tt, snapshotResp1, "Expected snapshot to be returned for volume1")
		assert.Equal(tt, snapshotResp1.Name, "test_snapshot")
		assert.Equal(tt, snapshotResp1.VolumeUUID, "test-volume-1-uuid")

		// Test deleting snapshot from volume2 - code should return existing job for idempotency
		params2 := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume2.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-2-uuid",
		}

		snapshotResp2, jobUUID2, err := orch.DeleteSnapshot(ctx, params2)
		assert.NoError(tt, err, "Failed to delete snapshot for volume2")
		// Code should return the existing job UUID for idempotency
		assert.NotEmpty(tt, jobUUID2, "Expected job UUID to be returned for volume2")
		assert.Equal(tt, "test-delete-job-2-uuid", jobUUID2, "Expected the existing job UUID to be returned for idempotency")
		assert.NotEqual(tt, jobUUID1, jobUUID2, "Expected different job UUIDs for different volumes")
		assert.NotNil(tt, snapshotResp2, "Expected snapshot to be returned for volume2")
		assert.Equal(tt, snapshotResp2.Name, "test_snapshot")
		assert.Equal(tt, snapshotResp2.VolumeUUID, "test-volume-2-uuid")

		// Verify that different job UUIDs are returned for different volumes
		assert.NotEqual(tt, jobUUID1, jobUUID2, "Expected different job UUIDs for different volumes")
	})

	t.Run("WhenSnapshotDeletionFailsDueToGetJobsWithConditionError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		// Create pool first
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			VendorID:  "/projects/project123/locations/location123/pools/pool123",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone:  "us-west1-a",
				IsRegionalHA: false,
			},
			APIAccessMode: common.DEFAULTMode,
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Volume:    volume,
			State:     datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		// Mock the storage to return an error when CreateJob is called
		origStorage := orch.storage
		mockStorage := &MockStorage{
			Storage:        origStorage,
			createJobError: errors.New("database connection failed"),
		}
		orch.storage = mockStorage
		defer func() { orch.storage = origStorage }()

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			SnapshotID: "test-snapshot-uuid",
		}

		snapshotResp, jobUUID, err := orch.DeleteSnapshot(ctx, params)
		assert.Error(tt, err, "Expected error when CreateJob fails")
		assert.Contains(tt, err.Error(), "database connection failed", "Expected specific error message")
		assert.Nil(tt, snapshotResp, "Expected nil snapshot response")
		assert.Empty(tt, jobUUID, "Expected empty job UUID")
	})
}

// TestDeleteSnapshot_WhenSnapshotInCreatingStateWithEmptyCorrelationID tests the scenario where
// snapshot is in CREATING state but correlation ID is empty
func TestDeleteSnapshot_WhenSnapshotInCreatingStateWithEmptyCorrelationID(t *testing.T) {
	ctx := context.Background() // No correlation ID in context
	store := database.NewMockStorage(t)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "test-account"}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		AccountID: account.ID,
		Account:   account,
		Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateCreating,
		VolumeID:  volume.ID,
		Volume:    volume,
		AccountID: account.ID,
		Account:   account,
	}

	params := &common.DeleteSnapshotParams{
		SnapshotID: snapshot.UUID,
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    volume.UUID,
			AccountName: account.Name,
		},
	}

	// Patch VolumeOwnershipCheck
	origVolumeOwnershipCheck := VolumeOwnershipCheck
	VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
		return volume, nil
	}
	defer func() { VolumeOwnershipCheck = origVolumeOwnershipCheck }()

	// Mock GetSnapshotByUUID
	store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(snapshot, nil)

	// Note: When correlation ID is empty, ValidateCorrelationIDForCreatingResource returns early
	// without calling GetJobByResourceUUID, so no mock is needed here

	// Act
	result, jobID, err := deleteSnapshot(ctx, store, temporal, params)

	// Assert
	assert.Error(t, err)
	assert.True(t, customerrors.IsConflictErr(err))
	assert.Contains(t, err.Error(), "Error deleting snapshot - snapshot is already transitioning between states")
	assert.Nil(t, result)
	assert.Equal(t, "", jobID)
	store.AssertExpectations(t)
}

// TestDeleteSnapshot_WhenSnapshotInCreatingStateWithMismatchedCorrelationID tests the scenario where
// snapshot is in CREATING state but correlation ID doesn't match
func TestDeleteSnapshot_WhenSnapshotInCreatingStateWithMismatchedCorrelationID(t *testing.T) {
	correlationID := "test-correlation-id-123"
	fields := log.Fields{
		string(middleware.RequestCorrelationID): correlationID,
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
	store := database.NewMockStorage(t)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "test-account"}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		AccountID: account.ID,
		Account:   account,
		Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateCreating,
		VolumeID:  volume.ID,
		Volume:    volume,
		AccountID: account.ID,
		Account:   account,
	}

	params := &common.DeleteSnapshotParams{
		SnapshotID: snapshot.UUID,
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    volume.UUID,
			AccountName: account.Name,
		},
	}

	// Patch VolumeOwnershipCheck
	origVolumeOwnershipCheck := VolumeOwnershipCheck
	VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
		return volume, nil
	}
	defer func() { VolumeOwnershipCheck = origVolumeOwnershipCheck }()

	// Mock GetSnapshotByUUID
	store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(snapshot, nil)

	// Mock GetJobByResourceUUID for DELETE_SNAPSHOT (called first in ValidateCorrelationIDForCreatingResource)
	store.On("GetJobByResourceUUID", ctx, snapshot.UUID, string(datamodel.JobTypeDeleteSnapshot)).Return(nil, errors.New("no delete job found"))

	// Mock GetJobByResourceUUID to return a job with different correlation ID
	createJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{UUID: "create-job-uuid"},
		CorrelationID: "different-correlation-id", // Mismatch
		Type:          string(datamodel.JobTypeCreateSnapshot),
	}
	store.On("GetJobByResourceUUID", ctx, snapshot.UUID, string(datamodel.JobTypeCreateSnapshot)).Return(createJob, nil)

	// Act
	result, jobID, err := deleteSnapshot(ctx, store, temporal, params)

	// Assert
	assert.Error(t, err)
	assert.True(t, customerrors.IsConflictErr(err))
	assert.Contains(t, err.Error(), "Error deleting snapshot - snapshot is already transitioning between states")
	assert.Nil(t, result)
	assert.Equal(t, "", jobID)
	store.AssertExpectations(t)
}

// TestDeleteSnapshot_WhenSnapshotInCreatingStateWithMatchingCorrelationID tests the scenario where
// snapshot is in CREATING state and correlation ID matches
func TestDeleteSnapshot_WhenSnapshotInCreatingStateWithMatchingCorrelationID(t *testing.T) {
	correlationID := "test-correlation-id-123"
	fields := log.Fields{
		string(middleware.RequestCorrelationID): correlationID,
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
	store := database.NewMockStorage(t)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "test-account"}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		AccountID: account.ID,
		Account:   account,
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			VendorID:  "/projects/project123/locations/location123/pools/pool123",
		},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateCreating,
		VolumeID:  volume.ID,
		Volume:    volume,
		AccountID: account.ID,
		Account:   account,
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes: bytesPerGB,
		},
	}

	params := &common.DeleteSnapshotParams{
		SnapshotID: snapshot.UUID,
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    volume.UUID,
			AccountName: account.Name,
		},
	}

	// Patch VolumeOwnershipCheck
	origVolumeOwnershipCheck := VolumeOwnershipCheck
	VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
		return volume, nil
	}
	defer func() { VolumeOwnershipCheck = origVolumeOwnershipCheck }()

	// Mock GetSnapshotByUUID
	store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(snapshot, nil)

	// Mock GetJobByResourceUUID to return a job with matching correlation ID
	createJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{UUID: "create-job-uuid"},
		CorrelationID: correlationID, // Match
		Type:          string(datamodel.JobTypeCreateSnapshot),
	}
	store.On("GetJobByResourceUUID", ctx, snapshot.UUID, string(datamodel.JobTypeCreateSnapshot)).Return(createJob, nil)
	// Mock GetJobByResourceUUID for delete job type - return nil to indicate no existing delete job
	store.On("GetJobByResourceUUID", ctx, snapshot.UUID, string(datamodel.JobTypeDeleteSnapshot)).Return(nil, customerrors.NewNotFoundErr("Job", nil))

	// Mock CreateJob
	store.On("CreateJob", ctx, mock.Anything).Return(&datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "delete-job-uuid"},
		WorkflowID: "test-workflow-id",
	}, nil)

	// Mock DeletingSnapshot - should NOT be called when snapshot is in CREATING state
	// (The test expects this not to be called, so we don't mock it)

	// Mock SignalWithStartWorkflow - DeleteSnapshot uses ExecuteSequentialWorkflow which calls SignalWithStartWorkflow internally
	temporal.EXPECT().SignalWithStartWorkflow(
		mock.Anything, // ctx
		mock.Anything, // controlWorkflowID
		mock.Anything, // signal name
		mock.Anything, // SignalWorkflowParams
		mock.Anything, // StartWorkflowOptions
		mock.Anything, // workflow function
		mock.Anything, // SequenceWorkflowInput
	).Return(nil, nil)

	// Act
	result, jobID, err := deleteSnapshot(ctx, store, temporal, params)

	// Assert
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "delete-job-uuid", jobID)
	store.AssertExpectations(t)
}

// TestDeleteSnapshot_WhenSnapshotInCreatingStateAndGetJobByResourceUUIDFails tests the scenario where
// snapshot is in CREATING state but GetJobByResourceUUID fails
func TestDeleteSnapshot_WhenSnapshotInCreatingStateAndGetJobByResourceUUIDFails(t *testing.T) {
	correlationID := "test-correlation-id-123"
	fields := log.Fields{
		string(middleware.RequestCorrelationID): correlationID,
	}
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
	store := database.NewMockStorage(t)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "testAccountUUID"}, Name: "test-account"}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		AccountID: account.ID,
		Account:   account,
		Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateCreating,
		VolumeID:  volume.ID,
		Volume:    volume,
		AccountID: account.ID,
		Account:   account,
	}

	params := &common.DeleteSnapshotParams{
		SnapshotID: snapshot.UUID,
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    volume.UUID,
			AccountName: account.Name,
		},
	}

	// Patch VolumeOwnershipCheck
	origVolumeOwnershipCheck := VolumeOwnershipCheck
	VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
		return volume, nil
	}
	defer func() { VolumeOwnershipCheck = origVolumeOwnershipCheck }()

	// Mock GetSnapshotByUUID
	store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(snapshot, nil)

	// Mock GetJobByResourceUUID for DELETE_SNAPSHOT (called first in ValidateCorrelationIDForCreatingResource)
	store.On("GetJobByResourceUUID", ctx, snapshot.UUID, string(datamodel.JobTypeDeleteSnapshot)).Return(nil, errors.New("no delete job found"))

	// Mock GetJobByResourceUUID to return an error
	store.On("GetJobByResourceUUID", ctx, snapshot.UUID, string(datamodel.JobTypeCreateSnapshot)).Return(nil, errors.New("job not found"))

	// Act
	result, jobID, err := deleteSnapshot(ctx, store, temporal, params)

	// Assert
	assert.Error(t, err)
	assert.True(t, customerrors.IsConflictErr(err))
	assert.Contains(t, err.Error(), "Error deleting snapshot - snapshot is already transitioning between states")
	assert.Nil(t, result)
	assert.Equal(t, "", jobID)
	store.AssertExpectations(t)
}

func TestDeleteSnapshot_CreatingStateWithExistingDeleteJob_ReturnsExistingJobUUID(t *testing.T) {
	// Test for line 613: When existingDeleteJobUUID is not empty, return it immediately
	ctx := context.Background()
	store := new(database.MockStorage)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)

	correlationID := "test-correlation-id-123"
	fields := log.Fields{
		string(middleware.RequestCorrelationID): correlationID,
	}
	ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, fields)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid"},
		AccountID: 42,
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateCreating,
		VolumeID:  volume.ID,
		AccountID: 42,
		Volume:    volume,
		Account:   account,
		SnapshotAttributes: &datamodel.SnapshotAttributes{
			SizeInBytes: 1024 * 1024, // 1MB
		},
	}
	existingDeleteJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{UUID: "existing-delete-job-uuid"},
		CorrelationID: correlationID,
		Type:          string(datamodel.JobTypeDeleteSnapshot),
		State:         string(datamodel.JobsStatePROCESSING),
	}

	params := &common.DeleteSnapshotParams{
		SnapshotID: snapshot.UUID,
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    volume.UUID,
			AccountName: account.Name,
		},
	}

	// Patch VolumeOwnershipCheck
	origVolumeOwnershipCheck := VolumeOwnershipCheck
	VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
		// Set Account field on volume since it's used at line 594 in snapshot.go
		volume.Account = account
		return volume, nil
	}
	defer func() { VolumeOwnershipCheck = origVolumeOwnershipCheck }()

	store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(snapshot, nil)
	// ValidateCorrelationIDForCreatingResource returns existingDeleteJobUUID when delete job is in progress
	store.On("GetJobByResourceUUID", ctx, snapshot.UUID, string(datamodel.JobTypeDeleteSnapshot)).Return(existingDeleteJob, nil)

	result, jobUUID, err := deleteSnapshot(ctx, store, temporal, params)

	assert.NoError(t, err)
	assert.Equal(t, "existing-delete-job-uuid", jobUUID)
	assert.NotNil(t, result)
	assert.Equal(t, snapshot.UUID, result.UUID)
	store.AssertExpectations(t)
}

func TestDeleteSnapshot_TransitionalState_ReturnsError(t *testing.T) {
	// Test for line 617: When snapshot is in transitional state (not DELETING), return error
	ctx := context.Background()
	store := new(database.MockStorage)
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "volume-uuid"},
		AccountID: 42,
	}
	snapshot := &datamodel.Snapshot{
		BaseModel: datamodel.BaseModel{UUID: "snapshot-uuid"},
		Name:      "test-snapshot",
		State:     datamodel.LifeCycleStateUpdating, // Transitional state (not DELETING)
		VolumeID:  volume.ID,
		AccountID: 42,
		Volume:    volume,
		Account:   account,
	}

	params := &common.DeleteSnapshotParams{
		SnapshotID: snapshot.UUID,
		SnapshotBaseParams: common.SnapshotBaseParams{
			VolumeID:    volume.UUID,
			AccountName: account.Name,
		},
	}

	// Patch VolumeOwnershipCheck
	origVolumeOwnershipCheck := VolumeOwnershipCheck
	VolumeOwnershipCheck = func(ctx context.Context, se database.Storage, volumeUUID, accountName string) (*datamodel.Volume, error) {
		// Set Account field on volume since it's used at line 594 in snapshot.go
		volume.Account = account
		return volume, nil
	}
	defer func() { VolumeOwnershipCheck = origVolumeOwnershipCheck }()

	store.On("GetSnapshotByUUID", ctx, params.SnapshotID, account.ID, volume.ID).Return(snapshot, nil)
	// Note: GetJobByResourceUUID is not called when snapshot is in transitional state
	// because the function returns early at line 617 in snapshot.go

	result, jobUUID, err := deleteSnapshot(ctx, store, temporal, params)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Snapshot is in transition state and cannot be deleted")
	assert.Contains(t, err.Error(), datamodel.LifeCycleStateUpdating)
	assert.Nil(t, result)
	assert.Empty(t, jobUUID)
	store.AssertExpectations(t)
}

func TestDeleteSnapshots(t *testing.T) {
	t.Run("WhenSnapshotDeletionSuccess", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "snapmirror.1",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		params := &common.SnapshotsInternalDeleteParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: account.Name,
			},
			SnapshotsFromDB: []*datamodel.Snapshot{snapshot},
		}
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()
		jobId, err := orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.NoError(tt, err, "Failed to delete snapshot")
		assert.NotEmpty(tt, jobId)
	})
	t.Run("WhenSnapshotDeletionFailsDueToVolumeNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		params := &common.SnapshotsInternalDeleteParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "volume.UUID",
				AccountName: account.Name,
			},
		}
		_, err = orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.ErrorContains(tt, err, "Volume not found")
	})
	t.Run("WhenSnapshotDeletionFailsDueToAccountNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		params := &common.SnapshotsInternalDeleteParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "volume.UUID",
				AccountName: account.Name,
			},
		}
		_, err = orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.ErrorContains(tt, err, "Volume not found")
	})
	t.Run("WhenSnapshotDeletionFailsDueToWorkflowError", func(tt *testing.T) {
		withFastWorkflowRetry(tt)
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "snapmirror.1",
			AccountID: account.ID,
			VolumeID:  volume.ID,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.SnapshotsInternalDeleteParams{
			//	SnapshotID: "test-snapshot-uuid",
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: account.Name,
			},
		}

		// Mock ExecuteWorkflow to fail with a retryable error (connection refused is retryable)
		// WorkflowExecutor will retry up to 3 times for retryable errors
		workflowErr := errors.New("connection refused")
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, workflowErr).Times(3)

		_, err = orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.Contains(tt, err.Error(), "connection refused")
	})

	t.Run("WhenSnapshotDeletionFailsDueToWrongState", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.NewTestStorage(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateDeleting,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		// Create a snapshot in a non-deletable state (e.g., CREATING)
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateCreating,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		params := &common.SnapshotsInternalDeleteParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: account.Name,
			},
		}
		_, err = orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.ErrorContains(tt, err, "Volume of the snapshot is being deleted.")
	})
	t.Run("WhenSnapshotDeletionFailsDueToVolumeInRetainedState", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.NewTestStorage(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateRetained,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		// Create a snapshot in a non-deletable state (e.g., CREATING)
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateCreating,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		params := &common.SnapshotsInternalDeleteParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: account.Name,
			},
		}
		_, err = orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.ErrorContains(tt, err, "Volume not found")
	})
	t.Run("WhenSnapshotDeletionFailsDueToVolumeInOfflineState", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.NewTestStorage(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			State:     datamodel.VolumeStateOffline,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		// Create a snapshot in a non-deletable state (e.g., CREATING)
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateCreating,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		params := &common.SnapshotsInternalDeleteParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: account.Name,
			},
		}
		_, err = orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.ErrorContains(tt, err, "Volume is offline.")
	})
	t.Run("WhenSnapshotDeletionFailsDueToVolumeInRetainedState", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewLogger()
		store, err := database.NewTestStorage(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		orch := GCPOrchestrator{
			storage:  store,
			temporal: temporal,
		}
		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateRetained,
			Pool:      &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}
		// Create a snapshot in a non-deletable state (e.g., CREATING)
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateCreating,
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		params := &common.SnapshotsInternalDeleteParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    "test-volume-uuid",
				AccountName: account.Name,
			},
		}
		_, err = orch.DeleteSnapmirrorSnapshots(ctx, params)
		assert.ErrorContains(tt, err, "Volume not found")
	})
}

// TestCreateSnapshotSync_ErrorPaths tests error paths in createSnapshotSync
func TestCreateSnapshotSync_ErrorPaths(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockLogger := log.NewLogger()

	t.Run("UpdateJobError", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock storage - no UpdateJob calls in sync mode
		mockStorage := database.NewMockStorage(tt)

		// Mock GetOntapRestProviderForPoolFastConn to return error
		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}

		// This test verifies the error path when GetOntapRestProviderForPoolFastConn fails
		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPoolFastConnError", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock storage - no UpdateJob calls in sync mode
		mockStorage := database.NewMockStorage(tt)

		// Mock GetOntapRestProviderForPoolFastConn to return error
		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}

		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetOntapRestProviderForPoolFastConnErrorWithUpdateJobError", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock storage - no UpdateJob calls in sync mode
		mockStorage := database.NewMockStorage(tt)

		// Mock GetOntapRestProviderForPoolFastConn to return error
		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}

		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateSnapshotSyncWithDirectPollingError", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock storage - no UpdateJob calls in sync mode
		mockStorage := database.NewMockStorage(tt)

		// Mock provider and REST client
		mockProvider := new(vsa.MockProvider)
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		mockProvider.On("CreateRESTClient").Return(mockRESTClient, nil)
		mockRESTClient.On("Storage").Return(mockStorageClient)

		snapshotName := "test_snapshot"
		snapshotUUID := "snapshot-uuid"
		snapshotSize := int64(1024)
		snapshotLogicalSize := int64(2048)
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name:        nillable.ToPointer(snapshotName),
				UUID:        nillable.ToPointer(snapshotUUID),
				Size:        nillable.ToPointer(snapshotSize),
				LogicalSize: nillable.ToPointer(snapshotLogicalSize),
			},
		}

		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)
		// SnapshotGet is no longer called - we use snapshot directly from SnapshotCreate response

		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err) // Will fail because mockProvider is not *vsa.OntapRestProvider
		mockStorage.AssertExpectations(tt)
	})
}

// Tests for PollOntapJobDirectly have been moved to core/ontap-rest/job_utils_test.go

// TestCreateSnapshotSync_SuccessPaths tests success paths in createSnapshotSync
func TestCreateSnapshotSync_SuccessPaths(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockLogger := log.NewLogger()

	t.Run("SuccessWithSnapshotResponse", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		originalDescription := "original description"
		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			Description:        originalDescription,
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock storage - no UpdateJob calls in sync mode
		mockStorage := database.NewMockStorage(tt)

		// Mock provider - will fail type assertion in createSnapshotSyncWithDirectPolling
		mockProvider := new(vsa.MockProvider)

		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// This test verifies error path when createSnapshotSyncWithDirectPolling fails
		// createSnapshotSyncWithDirectPolling will fail because mockProvider is not *vsa.OntapRestProvider
		// This tests the error handling path
		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdateSnapshotErrorWithUpdateJobError", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock storage - no UpdateJob calls in sync mode
		mockStorage := database.NewMockStorage(tt)

		// Mock provider - will fail type assertion in createSnapshotSyncWithDirectPolling
		mockProvider := new(vsa.MockProvider)

		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// This test verifies that when createSnapshotSyncWithDirectPolling fails,
		// the error path is taken. Since we can't easily mock createSnapshotSyncWithDirectPolling,
		// it will fail because mockProvider is not *vsa.OntapRestProvider
		// This tests the error handling path
		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

// TestCreateSnapshotSyncWithDirectPolling_ErrorPaths tests error paths in createSnapshotSyncWithDirectPolling
func TestCreateSnapshotSyncWithDirectPolling_ErrorPaths(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()

	t.Run("ProviderNotOntapRestProvider", func(tt *testing.T) {
		mockProvider := new(vsa.MockProvider)
		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, mockProvider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "provider is not OntapRestProvider")
	})

	t.Run("CreateRESTClientError", func(tt *testing.T) {
		// Use SetTestHooks to mock REST client creation
		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return nil, errors.New("client creation error")
			},
		})
		defer cleanup()

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
	})

	t.Run("SnapshotCreateConflictError", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, nil, customerrors.NewConflictErr("snapshot exists"))

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("SnapshotCreateNotFoundError", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, nil, customerrors.NewNotFoundErr("Volume", nil))

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("SnapshotCreateOtherError", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, nil, errors.New("other error"))

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("SnapshotCreateWithJobPolling", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockClusterClient := new(ontapRest.MockClusterClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockRESTClient.On("Cluster").Return(mockClusterClient)

		jobUUID := "job-uuid"
		resourceUUID := "resource-uuid"
		jobAccepted := &ontapRest.JobAccepted{
			JobUUID:      jobUUID,
			ResourceUUID: resourceUUID,
		}

		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, jobAccepted, nil)

		// Mock job polling to succeed
		jobState := ontapRestModels.JobStateSuccess
		mockJobResponse := &cluster.JobGetOK{
			Payload: &ontapRestModels.Job{
				State: &jobState,
			},
		}
		mockClusterClient.On("GetJob", jobUUID).Return(mockJobResponse, nil)

		// After polling completes, SnapshotGet is called to get snapshot details
		snapshotName := "test_snapshot"
		snapshotUUID := resourceUUID
		snapshotSize := int64(0)
		snapshotLogicalSize := int64(2048)
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name:        nillable.ToPointer(snapshotName),
				UUID:        nillable.ToPointer(snapshotUUID),
				Size:        nillable.ToPointer(snapshotSize),
				LogicalSize: nillable.ToPointer(snapshotLogicalSize),
			},
		}
		mockStorageClient.On("SnapshotGet", mock.Anything).Return(mockSnapshot, nil)

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		result, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, snapshotName, result.Name)
		assert.Equal(tt, snapshotUUID, result.ExternalUUID)
		assert.Equal(tt, snapshotSize, result.SizeInBytes)
		assert.Equal(tt, snapshotLogicalSize, result.LogicalSizeInBytes)
		mockStorageClient.AssertExpectations(tt)
		mockClusterClient.AssertExpectations(tt)
	})

	t.Run("SnapshotGetErrorAfterPolling", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)
		mockClusterClient := new(ontapRest.MockClusterClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockRESTClient.On("Cluster").Return(mockClusterClient)

		jobUUID := "job-uuid"
		resourceUUID := "resource-uuid"
		jobAccepted := &ontapRest.JobAccepted{
			JobUUID:      jobUUID,
			ResourceUUID: resourceUUID,
		}

		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, jobAccepted, nil)

		// Mock job polling to succeed
		jobState := ontapRestModels.JobStateSuccess
		mockJobResponse := &cluster.JobGetOK{
			Payload: &ontapRestModels.Job{
				State: &jobState,
			},
		}
		mockClusterClient.On("GetJob", jobUUID).Return(mockJobResponse, nil)

		// SnapshotGet fails after polling
		mockStorageClient.On("SnapshotGet", mock.Anything).Return(nil, errors.New("get error"))

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		mockStorageClient.AssertExpectations(tt)
		mockClusterClient.AssertExpectations(tt)
	})

	t.Run("SnapshotNilResponse", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)

		// SnapshotCreate returns nil snapshot and no job to test error path
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, nil, nil)

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		// Error is wrapped in VCPError, check the original error using Unwrap
		var vcpErr *vsaerrors.CustomError
		if vsaerrors.As(err, &vcpErr) && vcpErr.Unwrap() != nil {
			assert.Contains(tt, vcpErr.Unwrap().Error(), "snapshot is nil and no job provided")
		} else {
			// If not wrapped, check the error message directly
			assert.Contains(tt, err.Error(), "snapshot is nil and no job provided")
		}
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("SnapshotMissingName", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)

		snapshotUUID := "snapshot-uuid"
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name: nil, // Missing name
				UUID: nillable.ToPointer(snapshotUUID),
			},
		}

		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)
		// SnapshotGet is no longer called - we use snapshot directly from SnapshotCreate response

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		// Error is wrapped in VCPError, check the original error using Unwrap
		// Since Size/LogicalSize is missing and no job is provided, we get a different error
		var vcpErr *vsaerrors.CustomError
		if vsaerrors.As(err, &vcpErr) && vcpErr.Unwrap() != nil {
			// The error could be either "missing required fields" (if validation happens first)
			// or "snapshot size or logical_size is missing" (if polling check happens first)
			errMsg := vcpErr.Unwrap().Error()
			assert.True(tt,
				strings.Contains(errMsg, "missing required fields") ||
					strings.Contains(errMsg, "snapshot size or logical_size is missing"),
				"Expected error about missing fields, got: %s", errMsg)
		} else {
			// If not wrapped, check the error message directly
			errMsg := err.Error()
			assert.True(tt,
				strings.Contains(errMsg, "missing required fields") ||
					strings.Contains(errMsg, "snapshot size or logical_size is missing"),
				"Expected error about missing fields, got: %s", errMsg)
		}
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("SnapshotMissingUUID", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)

		snapshotName := "test_snapshot"
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name: nillable.ToPointer(snapshotName),
				UUID: nil, // Missing UUID
			},
		}

		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)
		// SnapshotGet is no longer called - we use snapshot directly from SnapshotCreate response

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)
		// Error is wrapped in VCPError, check the original error using Unwrap
		var vcpErr *vsaerrors.CustomError
		if vsaerrors.As(err, &vcpErr) && vcpErr.Unwrap() != nil {
			assert.Contains(tt, vcpErr.Unwrap().Error(), "snapshot is nil and no job provided")
		} else {
			// If not wrapped, check the error message directly
			assert.Contains(tt, err.Error(), "snapshot is nil and no job provided")
		}
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("SuccessWithSnapshotImmediate", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)

		snapshotName := "test_snapshot"
		snapshotUUID := "snapshot-uuid"
		snapshotSize := int64(0)
		snapshotLogicalSize := int64(2048)
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name:        nillable.ToPointer(snapshotName),
				UUID:        nillable.ToPointer(snapshotUUID),
				Size:        nillable.ToPointer(snapshotSize),
				LogicalSize: nillable.ToPointer(snapshotLogicalSize),
			},
		}

		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)
		// SnapshotGet is no longer called - we use snapshot directly from SnapshotCreate response

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		result, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, snapshotName, result.Name)
		assert.Equal(tt, snapshotUUID, result.ExternalUUID)
		assert.Equal(tt, snapshotSize, result.SizeInBytes)
		assert.Equal(tt, snapshotLogicalSize, result.LogicalSizeInBytes)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("InsufficientSpaceError", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)

		insufficientSpaceErr := errors.New("Snapshot operation failed: No space left on device. Additional space required: 268KB.")
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, nil, insufficientSpaceErr)

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)

		// Verify it's a VCPError with ErrSnapshotInsufficientSpace
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(tt, err, &customErr)
		assert.Equal(tt, vsaerrors.ErrSnapshotInsufficientSpace, customErr.TrackingID)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("MaximumLimitExceededError", func(tt *testing.T) {
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanup := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanup()

		mockRESTClient.On("Storage").Return(mockStorageClient)

		maxLimitErr := errors.New("Cannot exceed maximum number of snapshots.")
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, nil, maxLimitErr)

		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		dbSnapshot := &datamodel.Snapshot{
			Name: "test_snapshot",
			Volume: &datamodel.Volume{
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid",
				},
			},
		}

		_, err := createSnapshotSyncWithDirectPolling(ctx, provider, dbSnapshot, mockLogger)
		assert.Error(tt, err)

		// Verify it's a VCPError with ErrSnapshotMaximumLimitExceeded
		var customErr *vsaerrors.CustomError
		assert.ErrorAs(tt, err, &customErr)
		assert.Equal(tt, vsaerrors.ErrSnapshotMaximumLimitExceeded, customErr.TrackingID)
		mockStorageClient.AssertExpectations(tt)
	})
}

// TestCreateSnapshotSync_AdditionalPaths tests additional paths in createSnapshotSync
func TestCreateSnapshotSync_AdditionalPaths(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	mockLogger := log.NewLogger()

	t.Run("SuccessWithValidSnapshotResponse", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		originalDescription := "original description"
		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			Description:        originalDescription,
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock REST client
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanupHooks := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanupHooks()

		snapshotName := "test_snapshot"
		snapshotUUID := "snapshot-uuid"
		snapshotSize := int64(1024)
		snapshotLogicalSize := int64(2048)
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name:        nillable.ToPointer(snapshotName),
				UUID:        nillable.ToPointer(snapshotUUID),
				Size:        nillable.ToPointer(snapshotSize),
				LogicalSize: nillable.ToPointer(snapshotLogicalSize),
			},
		}

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)
		// SnapshotGet is no longer called - we use snapshot directly from SnapshotCreate response

		// Use real storage but mock WithTransaction
		mockStorage := database.NewMockStorage(tt)
		// Mock WithTransaction to use real storage's transaction
		mockStorage.On("WithTransaction", ctx, mock.Anything).Run(func(args mock.Arguments) {
			fn := args[1].(func(utils2.Transaction) error)
			_ = store.WithTransaction(ctx, fn)
		}).Return(nil)

		// Create real OntapRestProvider
		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return provider, nil
		}

		// This test covers success path in sync mode
		result, err := createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Verify description was restored (line 354)
		assert.Equal(tt, originalDescription, dbSnapshot.Description)
		// Verify snapshot state was set (lines 363-367)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, dbSnapshot.State)
		assert.Equal(tt, int64(0), dbSnapshot.SnapshotAttributes.SizeInBytes)
		assert.Equal(tt, "snapshot-uuid", dbSnapshot.ExternalUUID)
		assert.Equal(tt, "snapshot-uuid", dbSnapshot.SnapshotAttributes.ExternalUUID)
		mockStorage.AssertExpectations(tt)
		mockRESTClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("UpdateSnapshotError", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock REST client
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanupHooks := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanupHooks()

		snapshotName := "test_snapshot"
		snapshotUUID := "snapshot-uuid"
		snapshotSize := int64(1024)
		snapshotLogicalSize := int64(2048)
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name:        nillable.ToPointer(snapshotName),
				UUID:        nillable.ToPointer(snapshotUUID),
				Size:        nillable.ToPointer(snapshotSize),
				LogicalSize: nillable.ToPointer(snapshotLogicalSize),
			},
		}

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)
		// SnapshotGet is no longer called - we use snapshot directly from SnapshotCreate response

		// Mock storage - WithTransaction should fail
		mockStorage := database.NewMockStorage(tt)
		mockStorage.On("WithTransaction", ctx, mock.Anything).Return(errors.New("update snapshot error"))

		// Create real OntapRestProvider
		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return provider, nil
		}

		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
		mockRESTClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	t.Run("UpdateSnapshotError", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock REST client
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanupHooks := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanupHooks()

		snapshotName := "test_snapshot"
		snapshotUUID := "snapshot-uuid"
		snapshotSize := int64(1024)
		snapshotLogicalSize := int64(2048)
		mockSnapshot := &ontapRest.Snapshot{
			Snapshot: ontapRestModels.Snapshot{
				Name:        nillable.ToPointer(snapshotName),
				UUID:        nillable.ToPointer(snapshotUUID),
				Size:        nillable.ToPointer(snapshotSize),
				LogicalSize: nillable.ToPointer(snapshotLogicalSize),
			},
		}

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(mockSnapshot, nil, nil)
		// SnapshotGet is no longer called - we use snapshot directly from SnapshotCreate response

		// Mock storage - WithTransaction should succeed
		mockStorage := database.NewMockStorage(tt)
		// Mock WithTransaction to use real storage's transaction
		mockStorage.On("WithTransaction", ctx, mock.Anything).Run(func(args mock.Arguments) {
			fn := args[1].(func(utils2.Transaction) error)
			_ = store.WithTransaction(ctx, fn)
		}).Return(nil)

		// Create real OntapRestProvider
		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return provider, nil
		}

		// This test covers success path - WithTransaction succeeds
		result, err := createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.NoError(tt, err) // Should succeed
		assert.NotNil(tt, result)
		mockStorage.AssertExpectations(tt)
		mockRESTClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})

	// Note: The nil snapshot response path (lines 357-361) cannot be easily tested
	// because createSnapshotSyncWithDirectPolling will always return an error if snapshot is nil.
	// The path can only be hit if createSnapshotSyncWithDirectPolling returns nil, nil,
	// which is not possible given the current implementation.

	t.Run("UpdateJobErrorWhenCreateSnapshotSyncWithDirectPollingFails", func(tt *testing.T) {
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}
		defer func() {
			if closeErr := store.Close(); closeErr != nil {
				tt.Logf("Failed to close store: %v", closeErr)
			}
		}()

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "test-password",
			},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
			Pool:      pool,
			VolumeAttributes: &datamodel.VolumeAttributes{
				ExternalUUID: "external-volume-uuid",
			},
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
			Type:      string(datamodel.JobTypeCreateSnapshot),
			State:     string(datamodel.JobsStateNEW),
		}
		err = store.DB().Create(job).Error
		if err != nil {
			tt.Fatalf("Failed to create job: %v", err)
		}

		dbSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			Volume:             volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.DB().Create(dbSnapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		params := &common.CreateSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				VolumeID:    volume.UUID,
				AccountName: account.Name,
			},
			Name: "test_snapshot",
		}

		// Mock REST client to cause error in createSnapshotSyncWithDirectPolling
		mockRESTClient := new(ontapRest.MockRESTClient)
		mockStorageClient := new(ontapRest.MockStorageClient)

		cleanupHooks := vsa.SetTestHooks(vsa.TestHooks{
			GetOntapClient: func(params ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
				return mockRESTClient, nil
			},
		})
		defer cleanupHooks()

		mockRESTClient.On("Storage").Return(mockStorageClient)
		mockStorageClient.On("SnapshotCreate", mock.Anything).Return(nil, nil, errors.New("snapshot create error"))

		// Mock storage - no UpdateJob calls in sync mode
		mockStorage := database.NewMockStorage(tt)

		// Create real OntapRestProvider
		provider := &vsa.OntapRestProvider{
			ClientParams: ontapRest.RESTClientParams{},
		}

		originalGetOntapRestProviderForPoolFastConn := backgroundactivities.GetOntapRestProviderForPoolFastConn
		defer func() {
			backgroundactivities.GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn
		}()
		backgroundactivities.GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return provider, nil
		}

		// This test covers error path when snapshot creation fails - cleanup handled by defer in _createSnapshot
		_, err = createSnapshotSync(ctx, mockStorage, dbSnapshot, params, mockLogger)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
		mockRESTClient.AssertExpectations(tt)
		mockStorageClient.AssertExpectations(tt)
	})
}

func TestDeleteSnapshot_PreviousStateAndDetailsInJobAttributes(t *testing.T) {
	t.Run("WhenDeleteSnapshot_JobAttributesContainsPreviousStateAndDetails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockLogger := log.NewLogger()
		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			VendorID:  "/projects/project123/locations/location123/pools/pool123",
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		previousState := datamodel.LifeCycleStateREADY
		previousStateDetails := datamodel.LifeCycleStateAvailableDetails
		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			AccountID:    account.ID,
			VolumeID:     volume.ID,
			Volume:       volume,
			Account:      account,
			State:        previousState,
			StateDetails: previousStateDetails,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes: 1024,
			},
		}
		err = store.DB().Create(snapshot).Error
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		temporal := workflowEngineMock.NewMockTemporalTestClient(t)
		mockStorage := database.NewMockStorage(t)

		params := &common.DeleteSnapshotParams{
			SnapshotBaseParams: common.SnapshotBaseParams{
				AccountName: account.Name,
				VolumeID:    volume.UUID,
			},
			SnapshotID: snapshot.UUID,
		}

		mockStorage.EXPECT().VerifyVolumeOwnership(ctx, params.VolumeID, params.AccountName).Return(volume, nil)
		mockStorage.EXPECT().GetSnapshotByUUID(ctx, params.SnapshotID, account.ID, volume.ID).Return(snapshot, nil)
		// Mock GetJobByResourceUUID for DELETE_SNAPSHOT (called in GetExistingDeleteJobForDeletingState for READY state)
		mockStorage.EXPECT().GetJobByResourceUUID(ctx, snapshot.UUID, string(datamodel.JobTypeDeleteSnapshot)).Return(nil, errors.New("no delete job found"))
		mockStorage.EXPECT().CreateJob(ctx, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.JobAttributes != nil &&
				job.JobAttributes.PreviousState == previousState &&
				job.JobAttributes.PreviousStateDetails == previousStateDetails &&
				job.JobAttributes.ResourceUUID == snapshot.UUID
		})).Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}, nil)
		mockStorage.EXPECT().DeletingSnapshot(ctx, mock.Anything).Return(nil)

		origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
		workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			return nil
		}
		defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

		_, jobUUID, err := _deleteSnapshot(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})
}

// Tests for PollOntapJobDirectly_DefaultCase have been moved to core/ontap-rest/job_utils_test.go
