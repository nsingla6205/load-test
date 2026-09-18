package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	expertModeWorkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/expertMode"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflowenginemock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"go.temporal.io/sdk/client"
	"gorm.io/gorm"
)

// mockWorkflowRun is a simple mock for client.WorkflowRun
type mockWorkflowRun struct {
	mock.Mock
}

func (m *mockWorkflowRun) GetID() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockWorkflowRun) GetRunID() string {
	args := m.Called()
	return args.String(0)
}

func (m *mockWorkflowRun) Get(ctx context.Context, valuePtr interface{}) error {
	args := m.Called(ctx, valuePtr)
	return args.Error(0)
}

func (m *mockWorkflowRun) GetWithOptions(ctx context.Context, valuePtr interface{}, options client.WorkflowRunGetOptions) error {
	args := m.Called(ctx, valuePtr, options)
	return args.Error(0)
}

func TestCreateExpertModeVolume(t *testing.T) {
	// Other tests in this package monkey-patch the package-level getAccountWithName
	// and may not restore it. This test relies on the real DB-backed lookup, so pin
	// it to the real implementation and restore whatever was there when we finish.
	previousGetAccountWithName := getAccountWithName
	getAccountWithName = _getAccountWithName
	t.Cleanup(func() { getAccountWithName = previousGetAccountWithName })

	setupStore := func(tt *testing.T) (*log.MockLogger, database.Storage, *datamodel.Account, *datamodel.Pool, *datamodel.Svm) {
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

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
			BaseModel:      datamodel.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:           "test_pool",
			AccountID:      account.ID,
			SizeInBytes:    2199023255552, // 2TB
			LargeCapacity:  false,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test-svm",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "660e8400-e29b-41d4-a716-446655440001",
				IPSpace:      "Default",
			},
			State: datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(svm).Error
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		return mockLogger, store, account, pool, svm
	}

	t.Run("Success_WithSvmUuid", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, account, pool, svm := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776, // 1TB
			Style:       "flexvol",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
			SvmName:     "",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.NoError(tt, err)

		var createdVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("name = ?", params.VolumeName).First(&createdVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, params.VolumeName, createdVolume.Name)
		assert.Equal(tt, params.SizeInBytes, createdVolume.SizeInBytes)
		assert.Equal(tt, pool.ID, createdVolume.PoolID)
		assert.Equal(tt, account.ID, createdVolume.AccountID)
		assert.Equal(tt, svm.ID, createdVolume.SvmID)
		assert.Equal(tt, params.Style, createdVolume.Style)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, createdVolume.State)
		// ExternalUUID is only populated after the volume is fetched from ONTAP in the workflow
		assert.Empty(tt, createdVolume.ExternalUUID)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_WithSvmName", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, svm := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// FLEX (shared) pool: SVM membership is validated by name and poolID.
		pool.ServiceLevel = "FLEX"
		assert.NoError(tt, store.DB().Save(pool).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "",
			SvmName:     "test-svm",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.NoError(tt, err)

		var createdVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("name = ?", params.VolumeName).First(&createdVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, svm.ID, createdVolume.SvmID)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_Flexgroup_WithLargeCapacityPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		pool.LargeCapacity = true
		err := store.DB().Save(pool).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-flexgroup-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexgroup",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
			SvmName:     "",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.CreateExpertModeVolume(ctx, params)

		assert.NoError(tt, err)

		var createdVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("name = ?", params.VolumeName).First(&createdVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, "flexgroup", createdVolume.Style)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("PoolNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(tt, err)

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    "non-existent-pool-uuid",
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "",
			SvmName:     "",
			AccountName: account.Name,
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Pool not found")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
	t.Run("InsufficientPoolCapacity", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, account, pool, svm := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		existingVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "existing-volume-uuid"},
			Name:        "existing-volume",
			SizeInBytes: 2000000000000, // Almost 2TB
			PoolID:      pool.ID,
			AccountID:   account.ID,
			SvmID:       svm.ID,
			Style:       "flexvol",
		}
		err := store.DB().Create(existingVolume).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 500000000000, // 500GB - would exceed pool capacity
			Style:       "flexvol",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
			SvmName:     "",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "insufficient pool capacity")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("DuplicateVolumeName", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, account, pool, svm := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		existingVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "existing-volume-uuid"},
			Name:        "duplicate-volume-name",
			SizeInBytes: 1099511627776,
			PoolID:      pool.ID,
			AccountID:   account.ID,
			SvmID:       svm.ID,
			Style:       "flexvol",
		}
		err := store.DB().Create(existingVolume).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "duplicate-volume-name",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
			SvmName:     "",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "a volume named 'duplicate-volume-name' already exists in this pool")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("SvmNotFound_ByUuid", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "non-existent-svm-uuid",
			SvmName:     "",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM with UUID")
		assert.Contains(tt, err.Error(), "not found in pool")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("SvmNotFound_ByName", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// FLEX (shared) pool: SVM membership is validated by name and poolID.
		pool.ServiceLevel = "FLEX"
		assert.NoError(tt, store.DB().Save(pool).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "",
			SvmName:     "non-existent-svm",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM with name")
		assert.Contains(tt, err.Error(), "not found in pool")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_WithSvmName_DedicatedPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, account, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Dedicated pools validate SVM membership against the pool, same as FLEX.
		pool.ServiceLevel = "PREMIUM"
		assert.NoError(tt, store.DB().Save(pool).Error)

		dedicatedSvm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "dedicated-svm-uuid"},
			Name:      "svm_dedicated_01",
			PoolID:    pool.ID,
			AccountID: account.ID,
			State:     datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.DB().Create(dedicatedSvm).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmName:     dedicatedSvm.Name,
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.NoError(tt, err)

		var createdVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("name = ?", params.VolumeName).First(&createdVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, dedicatedSvm.ID, createdVolume.SvmID)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	// An SVM in the same account but mapped to another pool must be rejected.
	t.Run("SvmNameFromAnotherPool_DedicatedPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, account, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		pool.ServiceLevel = "PREMIUM"
		pool.APIAccessMode = APIAccessModeONTAP
		assert.NoError(tt, store.DB().Save(pool).Error)

		otherPool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440099"},
			Name:           "other_pool",
			DeploymentName: "other-pool-deployment",
			AccountID:      account.ID,
			ServiceLevel:   "PREMIUM",
			SizeInBytes:    2199023255552,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		assert.NoError(tt, store.DB().Create(otherPool).Error)

		otherPoolSvm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "other-pool-svm-uuid"},
			Name:      "svm_other_pool_01",
			PoolID:    otherPool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "660e8400-e29b-41d4-a716-446655440099",
				IPSpace:      "Default",
			},
			State: datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.DB().Create(otherPoolSvm).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmName:     otherPoolSvm.Name,
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM with name")
		assert.Contains(tt, err.Error(), "not found in pool")
		assert.Contains(tt, err.Error(), "the SVM for this pool is 'test-svm'")

		var count int64
		assert.NoError(tt, store.DB().Model(&datamodel.ExpertModeVolumes{}).Where("name = ?", params.VolumeName).Count(&count).Error)
		assert.Zero(tt, count, "no volume row must be created for a cross-pool SVM")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("SvmUuidFromAnotherPool_DedicatedPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, account, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		pool.ServiceLevel = "PREMIUM"
		pool.APIAccessMode = APIAccessModeONTAP
		assert.NoError(tt, store.DB().Save(pool).Error)

		otherPool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440099"},
			Name:           "other_pool",
			DeploymentName: "other-pool-deployment",
			AccountID:      account.ID,
			ServiceLevel:   "PREMIUM",
			SizeInBytes:    2199023255552,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		assert.NoError(tt, store.DB().Create(otherPool).Error)

		otherPoolSvm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "other-pool-svm-uuid"},
			Name:      "svm_other_pool_01",
			PoolID:    otherPool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "660e8400-e29b-41d4-a716-446655440099",
				IPSpace:      "Default",
			},
			State: datamodel.LifeCycleStateREADY,
		}
		assert.NoError(tt, store.DB().Create(otherPoolSvm).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440099",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM with UUID")
		assert.Contains(tt, err.Error(), "not found in pool")
		assert.Contains(tt, err.Error(), "the SVM for this pool is 'test-svm'")

		var count int64
		assert.NoError(tt, store.DB().Model(&datamodel.ExpertModeVolumes{}).Where("name = ?", params.VolumeName).Count(&count).Error)
		assert.Zero(tt, count, "no volume row must be created for a cross-pool SVM")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_WithSvmUuid_DedicatedPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, svm := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// The setupStore SVM is mapped to this pool.
		pool.ServiceLevel = "PREMIUM"
		assert.NoError(tt, store.DB().Save(pool).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.NoError(tt, err)

		var createdVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("name = ?", params.VolumeName).First(&createdVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, svm.ID, createdVolume.SvmID)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("SvmUuidNotFound_DedicatedPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		pool.ServiceLevel = "PREMIUM"
		assert.NoError(tt, store.DB().Save(pool).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "does-not-exist-uuid",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "SVM with UUID")
		assert.Contains(tt, err.Error(), "not found in pool")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("SvmNameDoesNotMatchAccount_DedicatedPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		pool.ServiceLevel = "PREMIUM"
		assert.NoError(tt, store.DB().Save(pool).Error)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			// SVM belongs to another account, so it is not in this pool either.
			Style:   "flexvol",
			SvmName: "svm_someotheraccount_01",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "not found in pool")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("NeitherSvmUuidNorSvmNameProvided", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "",
			SvmName:     "",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "neither svmName nor svmUUID has been passed")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("GetExpertModeVolumeByUUID_Fails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:           "test_pool",
			AccountID:      account.ID,
			SizeInBytes:    2199023255552, // 2TB
			LargeCapacity:  false,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(tt, err)

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test-svm",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "660e8400-e29b-41d4-a716-446655440001",
				IPSpace:      "Default",
			},
			State: datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(svm).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776, // 1TB
			Style:       "flexvol",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
			SvmName:     "",
			AccountName: account.Name,
		}

		// Create a mock storage that will fail on GetExpertModeVolumeByUUID
		mockStorage := database.NewMockStorage(tt)

		// Mock getAccountWithName to return the account
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Mock all the calls that happen before GetExpertModeVolumeByUUID
		mockStorage.EXPECT().GetPool(ctx, params.PoolUUID, account.ID).Return(&datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: pool.ID},
				Name:           pool.Name,
				AccountID:      account.ID,
				ServiceLevel:   "FLEX",
				SizeInBytes:    pool.SizeInBytes,
				LargeCapacity:  pool.LargeCapacity,
				PoolAttributes: pool.PoolAttributes,
			},
		}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(&database.ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 0}, nil).Once()
		mockStorage.EXPECT().GetSvmByExternalUUID(ctx, params.SvmUuid, pool.ID).Return(svm, nil).Once()
		createdVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		}
		mockStorage.EXPECT().CreateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(createdVolume, nil).Once()
		// This is where we simulate the failure
		mockStorage.EXPECT().GetExpertModeVolumeByUUID(ctx, createdVolume.UUID).Return(nil, errors.New("failed to get volume with preloads")).Once()

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err = orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get volume with preloads")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WorkflowExecutionFailure_ReturnsError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
			SvmName:     "",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow execution failed")).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		// When workflow fails to start, the function should return the error
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "workflow execution failed")

		// Volume should still be created in DB (in CREATING state)
		var createdVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("name = ?", params.VolumeName).First(&createdVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, params.VolumeName, createdVolume.Name)
		assert.Equal(tt, datamodel.LifeCycleStateCreating, createdVolume.State)

		// Job should be marked as ERROR
		var job datamodel.Job
		err = store.DB().Where("resource_name = ?", params.VolumeName).First(&job).Error
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateERROR), job.State)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("AllStyleTypes", func(tt *testing.T) {
		styles := []string{"flexvol", "flexgroup", "flexcache"}

		for _, style := range styles {
			tt.Run(style, func(ttt *testing.T) {
				ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
				mockLogger, store, _, pool, _ := setupStore(ttt)
				temporal := workflowenginemock.NewMockTemporalTestClient(ttt)

				if style == "flexgroup" {
					pool.LargeCapacity = true
					err := store.DB().Save(pool).Error
					assert.NoError(ttt, err)
				}

				params := &commonparams.ExpertModeVolumeParams{
					PoolUUID:    pool.UUID,
					Action:      "post",
					VolumeName:  "test-volume-" + style,
					SizeInBytes: 1099511627776,
					Style:       style,
					SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
					SvmName:     "",
				}

				temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

				orch := &GCPOrchestrator{storage: store, temporal: temporal}
				err := orch.CreateExpertModeVolume(ctx, params)

				assert.NoError(ttt, err)

				var createdVolume datamodel.ExpertModeVolumes
				err = store.DB().Where("name = ?", params.VolumeName).First(&createdVolume).Error
				assert.NoError(ttt, err)
				assert.Equal(ttt, style, createdVolume.Style)

				mockLogger.AssertExpectations(ttt)
				temporal.AssertExpectations(ttt)
			})
		}
	})

	t.Run("Failure_GetExpertModeVolumeByNameAndPoolID_NonRecordNotFoundError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:          "test_pool",
			AccountID:     account.ID,
			SizeInBytes:   2199023255552,
			LargeCapacity: false,
		}

		mockStorage := database.NewMockStorage(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetPool(ctx, "550e8400-e29b-41d4-a716-446655440000", account.ID).Return(&datamodel.PoolView{
			Pool: *pool,
		}, nil).Once()
		// Simulate a database error (not ErrRecordNotFound)
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "my-expert-volume", pool.ID).Return(nil, errors.New("database connection error")).Once()

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			SvmUuid:     "",
			SvmName:     "",
			AccountName: account.Name,
		}

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "database connection error")

		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_CanFitInPool_SizeZero", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, account, pool, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 0, // Zero size should fail
			SvmUuid:     "660e8400-e29b-41d4-a716-446655440001",
			SvmName:     "",
			AccountName: account.Name,
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume size must be greater than 0")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_CanFitInPool_GetCapacityError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:          "test_pool",
			AccountID:     account.ID,
			SizeInBytes:   2199023255552,
			LargeCapacity: false,
		}

		mockStorage := database.NewMockStorage(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{
			Pool: *pool,
		}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "my-expert-volume", pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		// Simulate error getting capacity
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(nil, errors.New("failed to get capacity")).Once()

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "my-expert-volume",
			SizeInBytes: 1099511627776,
			SvmUuid:     "",
			SvmName:     "",
			AccountName: account.Name,
		}

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.CreateExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get capacity")

		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
}

func TestResolveCloneIdentifiers(t *testing.T) {
	t.Run("NilClone", func(t *testing.T) {
		isClone, pvUUID, pvName, psUUID, psName := resolveCloneIdentifiers(nil)
		assert.False(t, isClone)
		assert.Equal(t, "", pvUUID)
		assert.Equal(t, "", pvName)
		assert.Equal(t, "", psUUID)
		assert.Equal(t, "", psName)
	})

	t.Run("CloneWithUUIDAndNames", func(t *testing.T) {
		isClone, pvUUID, pvName, psUUID, psName := resolveCloneIdentifiers(&commonparams.ExpertModeVolumeCloneParams{
			IsFlexclone: true,
			ParentVolume: &commonparams.ExpertModeVolumeCloneParent{
				UUID: "pv-uuid",
				Name: "pv-name",
			},
			ParentSnapshot: &commonparams.ExpertModeVolumeCloneParent{
				UUID: "ps-uuid",
				Name: "ps-name",
			},
		})
		assert.True(t, isClone)
		assert.Equal(t, "pv-uuid", pvUUID)
		assert.Equal(t, "pv-name", pvName)
		assert.Equal(t, "ps-uuid", psUUID)
		assert.Equal(t, "ps-name", psName)
	})
}

func TestFetchParentVolumeSizeForCloneCreate(t *testing.T) {
	ctx := context.Background()
	pool := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}}

	t.Run("UUIDNotFoundReturnsBadRequest", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, "pv-uuid").Return(nil, gorm.ErrRecordNotFound)

		_, _, _, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "pv-uuid", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parent volume 'pv-uuid' not found")
	})

	t.Run("UUIDPoolMismatchReturnsBadRequest", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, "pv-uuid").Return(&datamodel.ExpertModeVolumes{
			PoolID: 999,
		}, nil)

		_, _, _, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "pv-uuid", "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parent volume is not in the requested pool")
	})

	t.Run("UUIDNameMismatchReturnsBadRequest", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, "pv-uuid").Return(&datamodel.ExpertModeVolumes{
			Name:   "actual-name",
			PoolID: 10,
		}, nil)

		_, _, _, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "pv-uuid", "requested-name")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parent volume name does not match parent volume UUID")
	})

	t.Run("NameNotFoundReturnsBadRequest", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetExpertModeVolumeByNameAndPoolID", mock.Anything, "missing", int64(10)).Return(nil, gorm.ErrRecordNotFound)

		_, _, _, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "", "missing")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parent volume 'missing' not found")
	})

	t.Run("NameLookupUnexpectedErrorReturnedAsIs", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		boom := errors.New("db down")
		mockStorage.On("GetExpertModeVolumeByNameAndPoolID", mock.Anything, "pv-name", int64(10)).Return(nil, boom)

		_, _, _, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "", "pv-name")
		assert.ErrorIs(t, err, boom)
	})

	t.Run("InvalidParentSizeReturnsBadRequest", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetExpertModeVolumeByNameAndPoolID", mock.Anything, "pv-name", int64(10)).Return(&datamodel.ExpertModeVolumes{
			Name:         "pv-name",
			ExternalUUID: "pv-uuid",
			SizeInBytes:  0,
			PoolID:       10,
		}, nil)

		_, _, _, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "", "pv-name")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid parent volume size for clone create")
	})

	t.Run("NameLookupSuccess", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetExpertModeVolumeByNameAndPoolID", mock.Anything, "pv-name", int64(10)).Return(&datamodel.ExpertModeVolumes{
			Name:         "pv-name",
			ExternalUUID: "pv-uuid",
			SizeInBytes:  1024,
			PoolID:       10,
		}, nil)

		size, resolvedUUID, resolvedName, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "", "pv-name")
		assert.NoError(t, err)
		assert.Equal(t, int64(1024), size)
		assert.Equal(t, "pv-uuid", resolvedUUID)
		assert.Equal(t, "pv-name", resolvedName)
	})
}

func TestCreateExpertModeVolume_CloneValidationBranches(t *testing.T) {
	setup := func(t *testing.T) database.Storage {
		mockLogger := log.NewMockLogger(t)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(t, err)
		assert.NoError(t, database.ClearInMemoryDB(store.DB()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "acct-uuid-1"},
			Name:      "acct-1",
		}
		assert.NoError(t, store.DB().Create(account).Error)

		pool := &datamodel.Pool{
			BaseModel:    datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:         "pool-1",
			AccountID:    account.ID,
			ServiceLevel: "FLEX",
			SizeInBytes:  10 * 1024 * 1024,
		}
		assert.NoError(t, store.DB().Create(pool).Error)

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-uuid-1"},
			Name:      "svm-1",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		assert.NoError(t, store.DB().Create(svm).Error)

		parent := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-parent-1"},
			Name:         "parent-vol",
			ExternalUUID: "parent-ext-1",
			AccountID:    account.ID,
			PoolID:       pool.ID,
			SvmID:        svm.ID,
			SizeInBytes:  1024,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
		}
		assert.NoError(t, store.DB().Create(parent).Error)

		return store
	}

	t.Run("CloneMissingParentVolumeRef", func(t *testing.T) {
		store := setup(t)
		err := _createExpertModeVolume(context.Background(), store, nil, &commonparams.ExpertModeVolumeParams{
			AccountName: "acct-1",
			PoolUUID:    "pool-uuid-1",
			Action:      "Create",
			VolumeName:  "clone-a",
			Style:       "flexvol",
			Clone: &commonparams.ExpertModeVolumeCloneParams{
				IsFlexclone: true,
			},
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "clone.parentVolume.uuid or clone.parentVolume.name is required")
	})

	t.Run("CloneParentResolvedBeforeSvmValidation", func(t *testing.T) {
		store := setup(t)
		err := _createExpertModeVolume(context.Background(), store, nil, &commonparams.ExpertModeVolumeParams{
			AccountName: "acct-1",
			PoolUUID:    "pool-uuid-1",
			Action:      "Create",
			VolumeName:  "clone-b",
			Style:       "flexvol",
			Clone: &commonparams.ExpertModeVolumeCloneParams{
				IsFlexclone: true,
				ParentVolume: &commonparams.ExpertModeVolumeCloneParent{
					Name: "parent-vol",
				},
			},
			// intentionally omit svm fields to fail after clone size resolution path
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "neither svmName nor svmUUID has been passed")
	})

	t.Run("CloneCreatePersistsCloneMetadata", func(t *testing.T) {
		store := setup(t)
		temporal := workflowenginemock.NewMockTemporalTestClient(t)
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		err := _createExpertModeVolume(context.Background(), store, temporal, &commonparams.ExpertModeVolumeParams{
			AccountName: "acct-1",
			PoolUUID:    "pool-uuid-1",
			Action:      "Create",
			VolumeName:  "clone-ok-vol",
			Style:       "flexvol",
			SvmName:     "svm-1",
			Clone: &commonparams.ExpertModeVolumeCloneParams{
				IsFlexclone: true,
				ParentVolume: &commonparams.ExpertModeVolumeCloneParent{
					Name: "parent-vol",
				},
				ParentSnapshot: &commonparams.ExpertModeVolumeCloneParent{
					UUID: "snap-ext",
					Name: "snap-n",
				},
			},
		})
		assert.NoError(t, err)

		var created datamodel.ExpertModeVolumes
		assert.NoError(t, store.DB().Where("name = ?", "clone-ok-vol").First(&created).Error)
		if assert.NotNil(t, created.VolumeAttributes) {
			assert.True(t, created.VolumeAttributes.IsFlexclone)
			if assert.NotNil(t, created.VolumeAttributes.Clone) {
				assert.Equal(t, "parent-ext-1", created.VolumeAttributes.Clone.ParentVolume.UUID)
				assert.Equal(t, "parent-vol", created.VolumeAttributes.Clone.ParentVolume.Name)
				assert.Equal(t, "snap-ext", created.VolumeAttributes.Clone.ParentSnapshot.UUID)
				assert.Equal(t, "snap-n", created.VolumeAttributes.Clone.ParentSnapshot.Name)
			}
		}
		temporal.AssertExpectations(t)
	})
}

func TestFetchParentVolumeSizeForCloneCreate_UUIDNonNotFoundErrorPropagates(t *testing.T) {
	ctx := context.Background()
	pool := datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}}
	mockStorage := database.NewMockStorage(t)
	dbErr := errors.New("db read failure")
	mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, "pv-uuid").Return(nil, dbErr)

	_, _, _, err := fetchParentVolumeSizeForCloneCreate(ctx, mockStorage, pool, "pv-uuid", "")
	assert.ErrorIs(t, err, dbErr)
	mockStorage.AssertExpectations(t)
}

func TestCreateExpertModeVolume_CloneParentFetchPropagatesDatabaseError(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	orig := getAccountWithName
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: accountName}, nil
	}
	defer func() { getAccountWithName = orig }()

	poolUUID := "pool-uuid-1"
	mockStorage.On("GetPool", mock.Anything, poolUUID, int64(1)).Return(&datamodel.PoolView{
		Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 10}, SizeInBytes: 1 << 40},
	}, nil)
	mockStorage.On("GetExpertModeVolumeByNameAndPoolID", mock.Anything, "clone-d", int64(10)).Return(nil, gorm.ErrRecordNotFound)
	dbErr := errors.New("parent lookup failed")
	mockStorage.On("GetExpertModeVolumeByExternalUUID", mock.Anything, "bad-parent").Return(nil, dbErr)

	err := _createExpertModeVolume(ctx, mockStorage, nil, &commonparams.ExpertModeVolumeParams{
		AccountName: "acct",
		PoolUUID:    poolUUID,
		Action:      "Create",
		VolumeName:  "clone-d",
		Style:       "flexvol",
		SvmName:     "svm-1",
		Clone: &commonparams.ExpertModeVolumeCloneParams{
			IsFlexclone:  true,
			ParentVolume: &commonparams.ExpertModeVolumeCloneParent{UUID: "bad-parent"},
		},
	})
	assert.ErrorIs(t, err, dbErr)
	mockStorage.AssertExpectations(t)
}

func TestVolumeReconciliationWorkflow(t *testing.T) {
	t.Run("WorkflowExists", func(tt *testing.T) {
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-volume",
			SizeInBytes: 1099511627776,
			Style:       "flexvol",
		}
		correlationID := "test-correlation-id"

		// Verify the workflow function exists in the expertMode package
		assert.NotNil(tt, expertModeWorkflows.VolumeCreateReconciliationWorkflow)
		assert.NotNil(tt, expertModeVolume)
		assert.NotEmpty(tt, correlationID)
	})
}

func TestDeleteExpertModeVolume(t *testing.T) {
	setupStore := func(tt *testing.T) (*log.MockLogger, database.Storage, *datamodel.Account, *datamodel.Pool, *datamodel.Svm, *datamodel.ExpertModeVolumes) {
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

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
			BaseModel:      datamodel.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:           "test_pool",
			AccountID:      account.ID,
			SizeInBytes:    2199023255552, // 2TB
			LargeCapacity:  false,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test-svm",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "660e8400-e29b-41d4-a716-446655440001",
				IPSpace:      "Default",
			},
			State: datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(svm).Error
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
		}
		err = store.DB().Create(volume).Error
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		return mockLogger, store, account, pool, svm, volume
	}

	t.Run("Success_VolumeDeleted", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, volume := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.NoError(tt, err)

		// Verify volume state is updated to DELETING
		var updatedVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("uuid = ?", volume.UUID).First(&updatedVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateDeleting, updatedVolume.State)

		// Verify job was created
		var job datamodel.Job
		err = store.DB().Where("resource_name = ?", volume.Name).First(&job).Error
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobTypeDeleteExpertModeVolume), job.Type)
		assert.Equal(tt, string(datamodel.JobsStateNEW), job.State)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeUUIDRequired", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "", // Empty UUID
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "either volumeUUID or (volumeName and poolUUID) is required")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, _ := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "non-existent-volume-uuid",
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		// The underlying storage layer returns gorm's "record not found" error
		assert.Contains(tt, err.Error(), "not found")

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_VolumeAlreadyDeleted", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, volume := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mark volume as already deleted
		volume.State = datamodel.LifeCycleStateDeleted
		err := store.DB().Save(volume).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.DeleteExpertModeVolume(ctx, params)

		// Should return nil without starting workflow
		assert.NoError(tt, err)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_WorkflowExecutionFails_RevertsState", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, volume := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalState := volume.State

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow execution failed")).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		// Should return error
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "workflow execution failed")

		// Verify volume state is reverted to original state
		var updatedVolume datamodel.ExpertModeVolumes
		err = store.DB().Where("uuid = ?", volume.UUID).First(&updatedVolume).Error
		assert.NoError(tt, err)
		assert.Equal(tt, originalState, updatedVolume.State)

		// Verify job was marked as ERROR
		var job datamodel.Job
		err = store.DB().Where("resource_name = ?", volume.Name).First(&job).Error
		assert.NoError(tt, err)
		assert.Equal(tt, string(datamodel.JobsStateERROR), job.State)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_AccountNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		mockStorage := database.NewMockStorage(tt)

		// Mock getAccountWithName to return error
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("Account not found")
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "some-volume-uuid",
			AccountName: "non_existent_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Account not found")

		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_GetPoolFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:      "test_pool",
		}
		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			Pool:         pool,
		}
		mockStorage := database.NewMockStorage(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(nil, errors.New("failed to get pool")).Once()
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
		}
		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get pool")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNotAssociatedToPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		volumePool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "aaaaaaaa-e29b-41d4-a716-446655440001"},
			Name:      "volume_pool",
		}
		requestPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:      "request_pool",
		}
		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			PoolID:       volumePool.ID,
			Pool:         volumePool,
		}
		mockStorage := database.NewMockStorage(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, requestPool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *requestPool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: account.Name,
			PoolUUID:    requestPool.UUID,
		}
		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "not associated to the specified pool")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateVolumeStateFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:      "test_pool",
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			Pool:         pool,
		}

		mockStorage := database.NewMockStorage(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(ctx, volume.ExternalUUID).Return(false, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(nil, errors.New("failed to update volume state")).Once()

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to update volume state")

		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_CreateJobFails", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:      "test_pool",
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			Pool:         pool,
		}

		mockStorage := database.NewMockStorage(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(ctx, volume.ExternalUUID).Return(false, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("failed to create job")).Once()

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to create job")

		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("VolumeInDeletingState_StartsWorkflow", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, volume := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Set volume to DELETING state (not DELETED)
		volume.State = datamodel.LifeCycleStateDeleting
		err := store.DB().Save(volume).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.DeleteExpertModeVolume(ctx, params)

		// Should still start the workflow (only DELETED state returns early)
		assert.NoError(tt, err)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
	t.Run("Failure_BackupInCreatingState_BlocksDeletion", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, volume := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Insert a backup in CREATING state for this volume.
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "backup-creating-uuid"},
			VolumeUUID: volume.ExternalUUID,
			State:      datamodel.LifeCycleStateCreating,
		}
		err := store.DB().Create(backup).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup operation on volume is currently in progress")

		// Volume state must remain unchanged (not set to DELETING).
		var updatedVolume datamodel.ExpertModeVolumes
		dbErr := store.DB().Where("uuid = ?", volume.UUID).First(&updatedVolume).Error
		assert.NoError(tt, dbErr)
		assert.NotEqual(tt, datamodel.LifeCycleStateDeleting, updatedVolume.State)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_BackupInDeletingState_BlocksDeletion", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, volume := setupStore(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Insert a backup in DELETING state for this volume.
		backup := &datamodel.Backup{
			BaseModel:  datamodel.BaseModel{UUID: "backup-deleting-uuid"},
			VolumeUUID: volume.ExternalUUID,
			State:      datamodel.LifeCycleStateDeleting,
		}
		err := store.DB().Create(backup).Error
		assert.NoError(tt, err)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		orch := &GCPOrchestrator{storage: store, temporal: temporal}
		err = orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup operation on volume is currently in progress")

		// Volume state must remain unchanged.
		var updatedVolume datamodel.ExpertModeVolumes
		dbErr := store.DB().Where("uuid = ?", volume.UUID).First(&updatedVolume).Error
		assert.NoError(tt, dbErr)
		assert.NotEqual(tt, datamodel.LifeCycleStateDeleting, updatedVolume.State)

		mockLogger.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_BackupTransitionCheck_DBError_BlocksDeletion", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:      "test_pool",
		}
		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			Pool:         pool,
		}

		mockStorage := database.NewMockStorage(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(ctx, volume.ExternalUUID).Return(false, errors.New("db query failed")).Once()

		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
		}

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.DeleteExpertModeVolume(ctx, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db query failed")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
}

func TestGetExpertModeVolumeByExternalUUID(t *testing.T) {
	setupStoreForGet := func(tt *testing.T) (*log.MockLogger, database.Storage, *datamodel.Account, *datamodel.Pool, *datamodel.Svm, *datamodel.ExpertModeVolumes) {
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

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
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			SizeInBytes:    2199023255552, // 2TB
			LargeCapacity:  false,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		err = store.DB().Create(pool).Error
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test-svm",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "660e8400-e29b-41d4-a716-446655440001",
				IPSpace:      "Default",
			},
			State: datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(svm).Error
		if err != nil {
			tt.Fatalf("Failed to create svm: %v", err)
		}

		expertModeVolume := &datamodel.ExpertModeVolumes{
			Name:         "test-expert-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			Style:        "flexvol",
			State:        datamodel.LifeCycleStateREADY,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
		}
		err = store.DB().Create(expertModeVolume).Error
		if err != nil {
			tt.Fatalf("Failed to create expert mode volume: %v", err)
		}

		return mockLogger, store, account, pool, svm, expertModeVolume
	}

	t.Run("Success_VolumeExists", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger, store, _, _, _, volume := setupStoreForGet(tt)

		orch := &GCPOrchestrator{storage: store}
		result, err := orch.GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, volume.ExternalUUID, result.ExternalUUID)
		assert.Equal(tt, volume.Name, result.Name)
		assert.Equal(tt, volume.SizeInBytes, result.SizeInBytes)
		assert.Equal(tt, volume.Style, result.Style)
		assert.Equal(tt, volume.State, result.State)

		mockLogger.AssertExpectations(tt)
	})

	t.Run("Error_VolumeNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := &GCPOrchestrator{storage: store}
		result, err := orch.GetExpertModeVolumeByExternalUUID(ctx, "non-existent-uuid")

		assert.Error(tt, err)
		assert.Nil(tt, result)

		mockLogger.AssertExpectations(tt)
	})

	t.Run("Error_EmptyUUID", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := &GCPOrchestrator{storage: store}
		result, err := orch.GetExpertModeVolumeByExternalUUID(ctx, "")

		assert.Error(tt, err)
		assert.Nil(tt, result)

		mockLogger.AssertExpectations(tt)
	})

	t.Run("Error_StorageError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		expectedErr := errors.New("database connection error")
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, "test-uuid").Return(nil, expectedErr).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		result, err := orch.GetExpertModeVolumeByExternalUUID(ctx, "test-uuid")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedErr, err)

		mockStorage.AssertExpectations(tt)
	})
}

func Test_updateExpertModeVolume(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	setupTestData := func() (*datamodel.Account, *datamodel.Pool, *datamodel.ExpertModeVolumes) {
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:        "test_pool",
			AccountID:   account.ID,
			SizeInBytes: 2199023255552, // 2TB
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			Pool:         pool,
			Account:      account,
		}

		return account, pool, volume
	}

	t.Run("Success_UpdateSizeOnly", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		newSize := int64(2199023255552) // 2TB
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: newSize,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(&database.ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 0}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_UpdateNameOnly", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		newName := "updated-volume-name"
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  newName,
			SizeInBytes: 0, // No size update
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, newName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_UpdateBothNameAndSize", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		newName := "updated-volume-name"
		newSize := int64(2199023255552) // 2TB
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  newName,
			SizeInBytes: newSize,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, newName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(&database.ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 0}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeUUIDRequired", func(tt *testing.T) {
		account, pool, _ := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "", // Empty UUID
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
		}
		mockStorage.EXPECT().GetPool(ctx, "", account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "either volumeUUID or (volumeName and poolUUID) is required")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNotFound", func(tt *testing.T) {
		account, pool, _ := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "non-existent-volume-uuid",
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    pool.UUID,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID).Return(nil, gorm.ErrRecordNotFound).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume with UUID 'non-existent-volume-uuid' not found")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNotFound_NotFoundError", func(tt *testing.T) {
		account, pool, _ := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "non-existent-volume-uuid",
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    pool.UUID,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID).Return(nil, customerrors.NewNotFoundErr("volume not found", nil)).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume with UUID 'non-existent-volume-uuid' not found")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateDeleted", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateDeleted
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is deleted")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateError", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateError
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is deleted")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateCreating", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateCreating
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is in a transitional state")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateDeleting", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateDeleting
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is in a transitional state")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateUpdating", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateUpdating
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is in a transitional state")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_SizeNegative", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: -1, // Negative size
			AccountName: "test_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Volume size must be greater than or equal to 0")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_DuplicateVolumeName", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Create another volume with the duplicate name
		duplicateVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "other-volume-uuid"},
			Name:         "duplicate-name",
			ExternalUUID: "other-external-uuid",
		}

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  "duplicate-name",
			SizeInBytes: 0,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "duplicate-name", pool.ID).Return(duplicateVolume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume with name 'duplicate-name' already exists in pool")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_SameVolumeName_NoDuplicate", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Using the same name as the volume being updated (should not be considered duplicate)
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  volume.Name, // Same name
			SizeInBytes: 0,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, volume.Name, pool.ID).Return(volume, nil).Once() // Returns same volume
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_AccountNotFound", func(tt *testing.T) {
		_, _, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName to return error
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("Account not found")
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: "non_existent_account",
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Account not found")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_GetPoolFails", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(nil, errors.New("failed to get pool")).Once()
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
		}
		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get pool")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_AccountMismatch", func(tt *testing.T) {
		_, pool, volume := setupTestData()
		otherAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "other-account-uuid"},
			Name:      "other_account",
		}
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName to return different account
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return otherAccount, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: otherAccount.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, "550e8400-e29b-41d4-a716-446655440000", otherAccount.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume does not belong to the specified account")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_AccountMismatch_UsingAccountID", func(tt *testing.T) {
		_, pool, volume := setupTestData()
		otherAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "other-account-uuid"},
			Name:      "other_account",
		}
		// Remove Account relationship to test AccountID path
		volume.Account = nil
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName to return different account
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return otherAccount, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			AccountName: otherAccount.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, "550e8400-e29b-41d4-a716-446655440000", otherAccount.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume does not belong to the specified account")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_InsufficientPoolCapacity", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Try to increase size beyond pool capacity
		// Pool size: 2TB, existing volume: 1TB, trying to add another 2TB = 3TB total (exceeds pool)
		newSize := int64(3298534883328) // 3TB
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: newSize,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		// Pool has 1TB used capacity (the existing volume), trying to add 2TB more (3TB - 1TB = 2TB increase)
		// Pool size is 2TB, so 1TB + 2TB = 3TB > 2TB, should fail
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(&database.ExpertModePoolCapacity{TotalSize: volume.SizeInBytes, VolumeCount: 1}, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "insufficient pool capacity")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_SizeDecrease_NoCapacityCheck", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		// Decrease size (should not check pool capacity)
		newSize := int64(549755813888) // 512GB (half of original 1TB)
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: newSize,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		// Should not call GetExpertModePoolUsedCapacityAndVolumeCount for size decrease
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_GetExpertModeVolumeByNameAndPoolID_NonRecordNotFoundError", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  "new-name",
			SizeInBytes: 0,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		// Simulate a database error (not ErrRecordNotFound)
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "new-name", pool.ID).Return(nil, errors.New("database connection error")).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "database connection error")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_GetExpertModePoolUsedCapacityAndVolumeCount_Error", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		newSize := int64(2199023255552) // 2TB
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: newSize,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(nil, errors.New("failed to get capacity")).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get capacity")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_SizeZero_NoSizeUpdate", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 0, // Zero means no size update
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		// Should not call GetExpertModePoolUsedCapacityAndVolumeCount for zero size
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_GetExpertModeVolumeByExternalUUID_DatabaseError", func(tt *testing.T) {
		account, pool, _ := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "test-uuid",
			SizeInBytes: 2199023255552,
			AccountName: "test_account",
			PoolUUID:    pool.UUID,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, params.VolumeUUID).Return(nil, errors.New("database connection error")).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "database connection error")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_CreateJobFails", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		newSize := int64(2199023255552) // 2TB
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: newSize,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(&database.ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 0}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("failed to create job")).Once()
		// Defer function should revert the state
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to create job")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_ExecuteWorkflowFails", func(tt *testing.T) {
		account, pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() {
			getAccountWithName = originalGetAccountWithName
		}()

		newSize := int64(2199023255552) // 2TB
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: newSize,
			AccountName: account.Name,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, pool.ID).Return(&database.ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 0}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to start workflow")).Once()
		// Defer function should update job status and revert volume state
		mockStorage.EXPECT().UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, mock.AnythingOfType("string")).Return(nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to start workflow")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
}

func TestRenameExpertModeVolume(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	setupRenameTestData := func() (*datamodel.Account, *datamodel.Pool, *datamodel.Svm, *datamodel.ExpertModeVolumes) {
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		pool := &datamodel.Pool{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:        "test_pool",
			AccountID:   account.ID,
			SizeInBytes: 2199023255552,
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "svm-uuid"},
			Name:      "test-svm",
			PoolID:    pool.ID,
			AccountID: account.ID,
		}
		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776,
			PoolID:       pool.ID,
			AccountID:    account.ID,
			SvmID:        svm.ID,
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			Pool:         pool,
			Account:      account,
			Svm:          svm,
		}
		return account, pool, svm, volume
	}

	poolViewFromPool := func(pool *datamodel.Pool) *datamodel.PoolView {
		return &datamodel.PoolView{Pool: *pool}
	}

	t.Run("Success_RenameVolume", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed-volume",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.NewName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_OrchestratorRenameExpertModeVolume", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed-volume",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "workflow-id", TrackingID: 1}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.NewName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
		err := orch.RenameExpertModeVolume(ctx, params)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNotFound", func(tt *testing.T) {
		account, pool, _, _ := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  "nonexistent",
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     "test-svm",
			AccountName: account.Name,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()

		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "not found")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeHasNoSvm", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		volume.Svm = nil
		volume.SvmID = 0
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed-volume",
			PoolUUID:    pool.UUID,
			SvmName:     "test-svm",
			AccountName: account.Name,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()

		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "no SVM")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_SvmNameMismatch", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed-volume",
			PoolUUID:    pool.UUID,
			SvmName:     "wrong-svm-name",
			AccountName: account.Name,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()

		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "SVM name")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_NewNameAlreadyExists", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		existingVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "other-uuid"},
			Name:         "renamed-volume",
			ExternalUUID: "other-external-uuid",
			PoolID:       pool.ID,
		}
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed-volume",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.NewName, pool.ID).Return(existingVolume, nil).Once()

		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "already exists")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeInTransitionalState", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		volume.State = datamodel.LifeCycleStateUpdating
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed-volume",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()

		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "transitional")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_ExecuteWorkflowFails_RevertsState", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed-volume",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
			TrackingID: 1,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.NewName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow failed")).Once()
		mockStorage.EXPECT().UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, mock.AnythingOfType("string")).Return(nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()

		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "workflow failed")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_AccountNotFound", func(tt *testing.T) {
		_, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: "missing",
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "account not found")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_GetPoolFails", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(nil, errors.New("failed to get pool by UUID")).Once()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get pool by UUID")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_GetVolumeByName_DatabaseError", func(tt *testing.T) {
		account, pool, _, _ := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "my-vol", pool.ID).Return(nil, errors.New("database connection error")).Once()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  "my-vol",
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     "test-svm",
			AccountName: account.Name,
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "database connection error")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeDoesNotBelongToAccount", func(tt *testing.T) {
		_, pool, _, volume := setupRenameTestData()
		volume.AccountID = 1
		otherAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "other-account-uuid"},
			Name:      "other_account",
		}
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return otherAccount, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, otherAccount.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, volume.Name, pool.ID).Return(volume, nil).Once()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: otherAccount.Name,
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "does not belong to the specified account")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_CheckNewNameExists_DatabaseError", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, volume.Name, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "new-name", pool.ID).Return(nil, errors.New("db error")).Once()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "new-name",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db error")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_CreateJobFails", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, volume.Name, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "renamed", pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("failed to create job for expert mode volume rename")).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to create job")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateExpertModeVolumeFails", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, volume.Name, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "renamed", pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(nil, errors.New("Failed to update volume name and state for rename")).Once()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Failed to update volume name and state for rename")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_ExecuteWorkflowFails_UpdateJobFails", func(tt *testing.T) {
		account, pool, _, volume := setupRenameTestData()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "workflow-id", TrackingID: 1}
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(poolViewFromPool(pool), nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, volume.Name, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "renamed", pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow failed")).Once()
		mockStorage.EXPECT().UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, mock.AnythingOfType("string")).Return(errors.New("failed to update job status to error")).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		params := &commonparams.ExpertModeVolumeRenameParams{
			VolumeName:  volume.Name,
			NewName:     "renamed",
			PoolUUID:    pool.UUID,
			SvmName:     volume.Svm.Name,
			AccountName: account.Name,
		}
		err := _renameExpertModeVolume(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "workflow failed")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
}

func TestGetExpertModeVolume(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	t.Run("Failure_ByName_DatabaseError", func(tt *testing.T) {
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test_account"}
		pool := &datamodel.Pool{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:        "test_pool",
			AccountID:   account.ID,
			SizeInBytes: 2199023255552,
		}
		dbPoolView := &datamodel.PoolView{Pool: *pool}
		mockStorage := database.NewMockStorage(tt)
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "",
			VolumeName:  "my-volume",
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
		}
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "my-volume", pool.ID).Return(nil, errors.New("database error")).Once()
		vol, err := getExpertModeVolume(ctx, mockStorage, params, dbPoolView)
		assert.Error(tt, err)
		assert.Nil(tt, vol)
		assert.Contains(tt, err.Error(), "database error")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ByName_NilPoolView", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID: "",
			VolumeName: "my-volume",
			PoolUUID:   "550e8400-e29b-41d4-a716-446655440000",
		}
		vol, err := getExpertModeVolume(ctx, mockStorage, params, nil)
		assert.Error(tt, err)
		assert.Nil(tt, vol)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "volume not found")
		mockStorage.AssertExpectations(tt)
	})
}

func TestValidateUpdateParams(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	setupTestData := func() (*datamodel.Pool, *datamodel.ExpertModeVolumes) {
		pool := &datamodel.Pool{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:        "test_pool",
			SizeInBytes: 2199023255552, // 2TB
		}

		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:         "test-volume",
			SizeInBytes:  1099511627776, // 1TB
			PoolID:       pool.ID,
			State:        datamodel.LifeCycleStateAvailable,
			ExternalUUID: "770e8400-e29b-41d4-a716-446655440002",
			Pool:         pool,
		}

		return pool, volume
	}

	t.Run("Success_ValidParams", func(tt *testing.T) {
		_, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_ValidParams_WithVolumeName_NoConflict", func(tt *testing.T) {
		pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  "new-volume-name",
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_ValidParams_WithVolumeName_SameVolume", func(tt *testing.T) {
		pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  volume.Name, // Same name as current volume
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		// When checking for existing volume, it returns the same volume (same ExternalUUID)
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(volume, nil).Once()

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_IncorrectPoolUUID", func(tt *testing.T) {
		_, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  "550e8400", // Empty UUID
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440001",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume is not associated to the pool for update operation")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_NegativeSizeInBytes", func(tt *testing.T) {
		_, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: -1, // Negative size
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Volume size must be greater than or equal to 0")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateDeleted", func(tt *testing.T) {
		_, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateDeleted
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is deleted")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateError", func(tt *testing.T) {
		_, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateError
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is deleted")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateCreating", func(tt *testing.T) {
		_, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateCreating
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is in a transitional state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateDeleting", func(tt *testing.T) {
		_, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateDeleting
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is in a transitional state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeStateUpdating", func(tt *testing.T) {
		_, volume := setupTestData()
		volume.State = datamodel.LifeCycleStateUpdating
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "is in a transitional state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VolumeNameConflict_DifferentVolume", func(tt *testing.T) {
		pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		duplicateVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 2, UUID: "different-volume-uuid"},
			Name:         "duplicate-name",
			ExternalUUID: "880e8400-e29b-41d4-a716-446655440003", // Different UUID
			PoolID:       pool.ID,
		}

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  "duplicate-name",
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(duplicateVolume, nil).Once()

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "volume with name 'duplicate-name' already exists in pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_GetExpertModeVolumeByNameAndPoolID_DatabaseError", func(tt *testing.T) {
		pool, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  "new-name",
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		dbError := errors.New("database connection error")
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, params.VolumeName, pool.ID).Return(nil, dbError).Once()

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.Error(tt, err)
		assert.Equal(tt, dbError, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_ZeroSizeInBytes", func(tt *testing.T) {
		_, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 0, // Zero size is allowed
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_EmptyVolumeName", func(tt *testing.T) {
		_, volume := setupTestData()
		mockStorage := database.NewMockStorage(tt)

		params := &commonparams.ExpertModeVolumeParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  "", // Empty name should not trigger name validation
			SizeInBytes: 2199023255552,
			PoolUUID:    "550e8400-e29b-41d4-a716-446655440000",
		}

		err := validateUpdateParams(ctx, mockStorage, params, volume)

		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetExpertModeVolumeByUUID(t *testing.T) {
	t.Run("Success_VolumeExists", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

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
		assert.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			SizeInBytes:    2199023255552,
			LargeCapacity:  false,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(tt, err)

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "test-svm-uuid"},
			Name:      "test-svm",
			PoolID:    pool.ID,
			AccountID: account.ID,
			SvmDetails: &datamodel.SvmDetails{
				ExternalUUID: "660e8400-e29b-41d4-a716-446655440001",
				IPSpace:      "Default",
			},
			State: datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(svm).Error
		assert.NoError(tt, err)

		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test-expert-volume",
			SizeInBytes: 1099511627776,
			PoolID:      pool.ID,
			AccountID:   account.ID,
			SvmID:       svm.ID,
			Style:       "flexvol",
			State:       datamodel.LifeCycleStateREADY,
		}
		err = store.DB().Create(expertModeVolume).Error
		assert.NoError(tt, err)

		orch := &GCPOrchestrator{storage: store}
		result, err := orch.GetExpertModeVolumeByUUID(ctx, expertModeVolume.UUID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, expertModeVolume.UUID, result.UUID)
		assert.Equal(tt, expertModeVolume.Name, result.Name)
		assert.Equal(tt, expertModeVolume.SizeInBytes, result.SizeInBytes)

		mockLogger.AssertExpectations(tt)
	})

	t.Run("Error_VolumeNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")

		store, err := database.SetupStorageForTest(mockLogger)
		if err != nil {
			tt.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			tt.Fatalf("Failed to clean up test storage: %v", err)
		}

		orch := &GCPOrchestrator{storage: store}
		result, err := orch.GetExpertModeVolumeByUUID(ctx, "non-existent-uuid")

		assert.Error(tt, err)
		assert.Nil(tt, result)

		mockLogger.AssertExpectations(tt)
	})
}

func TestManageBackupConfigForExpertModeVolume(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	baseParams := func() *commonparams.ManageBackupConfigForExpertModeVolumeParams {
		return &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			PoolUUID:      "pool-uuid",
			VolumeUUID:    "volume-uuid",
			BackupVaultID: nillable.ToPointer("vault-uuid"),
			Region:        "us-east4",
		}
	}
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
		Name:      "test-account",
	}
	poolView := &datamodel.PoolView{Pool: datamodel.Pool{
		BaseModel:     datamodel.BaseModel{UUID: "pool-uuid"},
		APIAccessMode: commonparams.ONTAPMode,
	}}
	expertModeVolume := &datamodel.ExpertModeVolumes{
		BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
		ExternalUUID: "volume-uuid",
		Name:         "expert-vol",
		State:        datamodel.LifeCycleStateREADY,
		Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
	}

	t.Run("GetAccountWithNameError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetPoolError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(nil, errors.New("db error"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PoolNotONTAPMode", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		nonONTAPPool := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: "DEFAULT"}}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(nonONTAPPool, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "expert mode")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetExpertModeVolumeNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(nil, gorm.ErrRecordNotFound)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetExpertModeVolumeDBError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(nil, errors.New("db error"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VolumeNotInPool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		volumeInDifferentPool := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "other-pool-uuid"}},
		}
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeInDifferentPool, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "does not belong to the specified pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VolumeDeleting_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		deletingVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateDeleting,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(deletingVolume, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not in a ready state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VolumeDeleted_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		deletedVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateDeleted,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(deletedVolume, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not in a ready state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PolicyRequiresVault_NoVaultAnywhere", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		params := baseParams()
		params.BackupVaultID = nil // vault not provided; policy requires one but none exists on volume
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup vault id is required to assign a backup policy to a volume")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("KmsGrantRequiresVault_NoVaultAnywhere", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		kmsGrant := "projects/p/locations/l/keyRings/r/cryptoKeys/k"
		params := baseParams()
		params.BackupVaultID = nil // vault not provided; KMS requires one but none exists on volume
		params.KmsGrant = &kmsGrant
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup vault id is required to assign a backup policy to a volume")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PolicyWithExistingVaultCarriedForward", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "existing-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "workflow-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "existing-vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		enabled := true
		params := baseParams()
		params.BackupVaultID = nil // vault not provided; existing vault on volume should be carried forward
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		assert.Equal(tt, nillable.ToPointer("existing-vault-uuid"), params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("DetachVaultBlockedByExistingPolicy", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithPolicy := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "vault-uuid", BackupPolicyID: "policy-uuid"},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithPolicy, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "cannot remove backup vault while a backup policy is attached")
		mockStorage.AssertExpectations(tt)
	})

	// An existing KMS grant must not block detach.
	t.Run("DetachVaultAllowedWithExistingKmsGrant", func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(false)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		kmsGrant := "projects/p/locations/l/keyRings/r/cryptoKeys/k"
		volumeWithKms := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "vault-uuid", KmsGrant: &kmsGrant},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithKms, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeUUIDs(mock.Anything, []string{"volume-uuid"}, mock.Anything).
			Return(map[string]int64{}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithKms, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("DetachVaultBlockedByExistingBackups", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "vault-uuid"},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeUUIDs(mock.Anything, []string{"volume-uuid"}, mock.Anything).
			Return(map[string]int64{"volume-uuid": 3}, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "cannot remove backup vault as there are backups associated with it")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DetachVaultBackupCountError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "vault-uuid"},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeUUIDs(mock.Anything, []string{"volume-uuid"}, mock.Anything).
			Return(nil, errors.New("db error"))

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "db error")
		mockStorage.AssertExpectations(tt)
	})

	// ── Detach with both vault-switching flags off ────────────────────────────

	// Detaching a vault with no backups must be allowed regardless of the vault family.
	detachNoBackupsAllowed := func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(false)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeUUIDs(mock.Anything, []string{"volume-uuid"}, mock.Anything).
			Return(map[string]int64{"volume-uuid": 0}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertNotCalled(tt, "GetBackupVault", mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	}

	t.Run("Detach_NoBackups_BothFlagsOff_GCNVVault_Allowed", func(tt *testing.T) {
		detachNoBackupsAllowed(tt)
	})

	t.Run("Detach_NoBackups_BothFlagsOff_GCBDRVault_Allowed", func(tt *testing.T) {
		detachNoBackupsAllowed(tt)
	})

	// ── Detach with EnableBackupVaultSwitching on ─────────────────────────────

	t.Run("Detach_BVSOn_BackupInProgress_Blocked", func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-uuid"},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(true, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup operation on the volume is currently in progress")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Detach_BVSOn_NoAvailableBackups_Allowed", func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Detach_BVSOn_GCNVOff_AvailableBackups_CurrentGCBDR_Allowed", func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-uuid"},
		}
		gcbdrVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 7, UUID: "current-vault-uuid"}, ServiceType: activities.GCBDRServiceType}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{7}, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "current-vault-uuid").Return(gcbdrVault, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Detach_BVSOn_GCNVOff_AvailableBackups_CurrentVaultNotFound_Blocked", func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "missing-vault-uuid"},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{7}, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "missing-vault-uuid").Return(nil, gorm.ErrRecordNotFound)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.ErrorIs(tt, err, gorm.ErrRecordNotFound)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Detach_BVSOn_GCNVOff_AvailableBackups_CurrentGCNV_Blocked", func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-uuid"},
		}
		gcnvVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 7, UUID: "current-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{7}, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "current-vault-uuid").Return(gcnvVault, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "cannot remove backup vault as there are backups associated with it")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Detach_BVSOn_GCNVOn_AvailableBackups_CurrentGCNV_Allowed", func(tt *testing.T) {
		origBVS := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(origBVS)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(true)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "current-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{7}, nil).Maybe()
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("VaultInErrorStateRejected", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		errorVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateError}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(errorVault, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup vault is in error state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CMEKVaultRequiresKmsGrant", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		kmsPath := "projects/p/locations/l/keyRings/r/cryptoKeys/k"
		cmekVault := &datamodel.BackupVault{
			LifeCycleState: datamodel.LifeCycleStateREADY,
			CmekAttributes: &datamodel.CmekAttributes{KmsConfigResourcePath: &kmsPath},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(cmekVault, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "KMS Grant is required for CMEK Backup vault")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BackupPolicyNotReadyRejected", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		notReadyPolicy := &datamodel.BackupPolicy{LifeCycleState: datamodel.LifeCycleStateCreating}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(notReadyPolicy, nil)

		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup policy is not in ready state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ScheduledBackupEnabledRequiredWithPolicy", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(nil, nil)

		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = nil
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "scheduled backups needs to be enabled/disabled when a backup policy is assigned")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ScheduledBackupEnabledTrueWithoutPolicy", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		enabled := true
		params := baseParams()
		params.BackupPolicyID = nil // not provided; volume has no existing policy → error expected
		params.ScheduledBackupEnabled = &enabled
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "cannot enable scheduled backups without a backup policy")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitchNotAllowed_BackupsExist", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		existingVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 42, UUID: "existing-vault-uuid"}, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithExistingVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "existing-vault-uuid"},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithExistingVault, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "new-different-vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "existing-vault-uuid").Return(existingVault, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeAndVault(mock.Anything, "volume-uuid", int64(42)).Return(int64(3), nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-different-vault-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "switching backup vault is not supported while backups exist")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitchAllowed_NoBackupsExist", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		existingVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 42, UUID: "existing-vault-uuid"}, LifeCycleState: datamodel.LifeCycleStateREADY}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-different-vault-uuid"}, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithExistingVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "existing-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithExistingVault, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "new-different-vault-uuid", int64(1)).Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "existing-vault-uuid").Return(existingVault, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeAndVault(mock.Anything, "volume-uuid", int64(42)).Return(int64(0), nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithExistingVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-different-vault-uuid")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})

	// ── EnableBackupVaultSwitching: cross-project vault lookup ────────────────

	t.Run("BackupVaultSwitchingEnabled_UsesGetBackupVault", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		readyVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateREADY}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		// Must call GetBackupVault (UUID-only lookup), NOT GetBackupVaultByUUIDndOwnerID.
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(readyVault, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(expertModeVolume, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	// ── GCBDR re-attach guard ─────────────────────────────────────────────────

	t.Run("ReattachVault_NonGCBDR_BlockedByDetachedBackups", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		// Volume has no vault attached.
		volNoVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		nonGCBDRVault := &datamodel.BackupVault{
			LifeCycleState: datamodel.LifeCycleStateREADY,
			ServiceType:    "GCNV", // not GCBDR
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volNoVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(nonGCBDRVault, nil)
		// Volume has backups from a previously detached vault, keyed by the volume's external UUID.
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{99}, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "attach a cross project backup vault")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReattachVault_GCBDR_AllowedWithDetachedBackups", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		volNoVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		gcbdrVault := &datamodel.BackupVault{
			LifeCycleState: datamodel.LifeCycleStateREADY,
			ServiceType:    activities.GCBDRServiceType,
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volNoVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(gcbdrVault, nil)
		// Has detached backups, but GCBDR is allowed.
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{99}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volNoVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("ReattachVault_GetDistinctVaultIDsError", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		volNoVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		nonGCBDRVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateREADY, ServiceType: "GCNV"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volNoVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(nonGCBDRVault, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return(nil, errors.New("db error"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "db error")
		mockStorage.AssertExpectations(tt)
	})

	// With GCNV vault switching on, the re-attached vault must match the family of the
	// backups left behind by the detached vault.
	t.Run("ReattachVault_GCNVFlagOn_FamilyMatches_Allowed", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(true)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		volNoVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		gcnvVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateREADY, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 1}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volNoVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(gcnvVault, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{99}, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultServiceTypesByVaultIDs(mock.Anything, []int64{99}).
			Return([]string{datamodel.ServiceTypeGCNV}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volNoVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("ReattachVault_GCNVFlagOn_FamilyMismatch_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(true)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		volNoVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		gcnvVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateREADY, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 1}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volNoVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(gcnvVault, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{99}, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultServiceTypesByVaultIDs(mock.Anything, []int64{99}).
			Return([]string{activities.GCBDRServiceType}, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "attach a CrossProject backup vault only")
		mockStorage.AssertExpectations(tt)
	})

	// A GCNV vault must belong to the same account as the volume; GCBDR may be cross-project.
	t.Run("InitialAttach_GCNVVaultDifferentAccount_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		volNoVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		foreignGCNVVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateREADY, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 999}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volNoVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(foreignGCNVVault, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "belongs to a different account")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("InitialAttach_GCBDRVaultDifferentAccount_Allowed", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		volNoVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
		}
		foreignGCBDRVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateREADY, ServiceType: activities.GCBDRServiceType, AccountID: 999}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volNoVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "vault-uuid").Return(foreignGCBDRVault, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volNoVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	// ── Immutable backup policy validation ────────────────────────────────────

	t.Run("ImmutableBackup_ValidationFails_Rejected", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		utils.SetImmutableBackupEnabledForTest(true)
		defer utils.SetImmutableBackupEnabledForTest(false)

		origFn := checkIsValidImmutableBackupPolicyWithRetry
		checkIsValidImmutableBackupPolicyWithRetry = func(_ context.Context, _ database.Storage, _, _ string, _ int64, _, _ string) error {
			return errors.New("daily backup retention must be at least 7 days for immutable vault")
		}
		defer func() { checkIsValidImmutableBackupPolicyWithRetry = origFn }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		readyPolicy := &datamodel.BackupPolicy{LifeCycleState: datamodel.LifeCycleStateREADY}
		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(readyPolicy, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "immutable backup vault settings")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ImmutableBackup_BackupPolicyUpdating_ReturnsUnavailable", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		utils.SetImmutableBackupEnabledForTest(true)
		defer utils.SetImmutableBackupEnabledForTest(false)

		origFn := checkIsValidImmutableBackupPolicyWithRetry
		checkIsValidImmutableBackupPolicyWithRetry = func(_ context.Context, _ database.Storage, _, _ string, _ int64, _, _ string) error {
			return vsaerrors.NewVCPError(vsaerrors.ErrImmutableValidationWithUpdatingBackupPolicy, errors.New("backup policy is updating"))
		}
		defer func() { checkIsValidImmutableBackupPolicyWithRetry = origFn }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		readyPolicy := &datamodel.BackupPolicy{LifeCycleState: datamodel.LifeCycleStateREADY}
		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(readyPolicy, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUnavailableErr(err))
		assert.Contains(tt, err.Error(), "currently being updated")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ImmutableBackup_BackupVaultUpdating_ReturnsUnavailable", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		utils.SetImmutableBackupEnabledForTest(true)
		defer utils.SetImmutableBackupEnabledForTest(false)

		origFn := checkIsValidImmutableBackupPolicyWithRetry
		checkIsValidImmutableBackupPolicyWithRetry = func(_ context.Context, _ database.Storage, _, _ string, _ int64, _, _ string) error {
			return vsaerrors.NewVCPError(vsaerrors.ErrImmutableValidationWithUpdatingBackupVault, errors.New("backup vault is updating"))
		}
		defer func() { checkIsValidImmutableBackupPolicyWithRetry = origFn }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		readyPolicy := &datamodel.BackupPolicy{LifeCycleState: datamodel.LifeCycleStateREADY}
		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(readyPolicy, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUnavailableErr(err))
		assert.Contains(tt, err.Error(), "currently being updated")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ImmutableBackup_ValidationPasses_Proceeds", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		utils.SetImmutableBackupEnabledForTest(true)
		defer utils.SetImmutableBackupEnabledForTest(false)

		origFn := checkIsValidImmutableBackupPolicyWithRetry
		checkIsValidImmutableBackupPolicyWithRetry = func(_ context.Context, _ database.Storage, _, _ string, _ int64, _, _ string) error {
			return nil
		}
		defer func() { checkIsValidImmutableBackupPolicyWithRetry = origFn }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		readyPolicy := &datamodel.BackupPolicy{LifeCycleState: datamodel.LifeCycleStateREADY}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(readyPolicy, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(expertModeVolume, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	// ─────────────────────────────────────────────────────────────────────────

	t.Run("CreateJobError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(nil, errors.New("create job failed"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "create job failed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WorkflowStartError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		// Defer 1: mark job ERROR.
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed").Return(nil)
		// Set volume to UPDATING before launching the workflow.
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(expertModeVolume, nil).Once()
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))
		// Defer 2: mark volume ERROR after workflow launch fails.
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(expertModeVolume, nil).Once()

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "workflow start failed")
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	// ── State-management tests (new behaviour) ───────────────────────────────

	t.Run("CreateJobFirst_VolumeStateNotChangedOnCreateJobFailure", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// Job must be created BEFORE UpdateExpertModeVolume(UPDATING).
		// If CreateJob fails, UpdateExpertModeVolume must never be called.
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(nil, errors.New("db unavailable"))
		// UpdateExpertModeVolume must NOT be called.

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "db unavailable")
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("UpdateToUpdatingFails_JobMarkedError_VolumeNotLaunched", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// After CreateJob succeeds, setting volume to UPDATING fails.
		// Defer 1 must mark the job as ERROR; workflow must not be launched.
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		// Defer 1: job must be marked ERROR.
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), mock.Anything, mock.Anything).Return(nil)
		// Setting UPDATING fails.
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(nil, errors.New("db write failed"))
		// Workflow must NOT be launched (Defer 2 does not fire since UPDATING was never set).

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "db write failed")
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("WorkflowLaunchFails_BothDefersFireCorrectly", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// Workflow launch fails after UPDATING is set.
		// Defer 1: job → ERROR. Defer 2: volume → ERROR.
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		// Defer 1: job → ERROR.
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), mock.Anything, mock.Anything).Return(nil)
		// Set volume UPDATING (succeeds).
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(expertModeVolume, nil).Once()
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("temporal unavailable"))
		// Defer 2: volume → ERROR.
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(expertModeVolume, nil).Once()

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "temporal unavailable")
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Success_BackupConfigFieldsReturnedFromParams", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// On success, the returned *DataProtection must reflect the params values.
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		readyPolicy := &datamodel.BackupPolicy{LifeCycleState: datamodel.LifeCycleStateREADY}
		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(readyPolicy, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(expertModeVolume, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", jobUUID)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "vault-uuid", backupConfig.BackupVaultID)
		assert.Equal(tt, "policy-uuid", backupConfig.BackupPolicyID)
		assert.NotNil(tt, backupConfig.ScheduledBackupEnabled)
		assert.True(tt, *backupConfig.ScheduledBackupEnabled)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Success", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err)

		dbAccount := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(dbAccount).Error
		assert.NoError(tt, err)
		dbPool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:           "test_pool",
			AccountID:      dbAccount.ID,
			SizeInBytes:    2199023255552,
			APIAccessMode:  commonparams.ONTAPMode,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		err = store.DB().Create(dbPool).Error
		assert.NoError(tt, err)
		dbVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "ext-volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Style:        datamodel.LifeCycleStateAvailableDetails,
			PoolID:       dbPool.ID,
			AccountID:    dbAccount.ID,
		}
		err = store.DB().Create(dbVolume).Error
		assert.NoError(tt, err)

		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test_account",
			PoolUUID:      dbPool.UUID,
			VolumeUUID:    dbVolume.ExternalUUID,
			BackupVaultID: nillable.ToPointer("vault-uuid"),
			Region:        "us-east4",
		}

		orch := &GCPOrchestrator{storage: store, temporal: mockTemporal}
		backupConfig, jobUUID, err := orch.ManageBackupConfigForExpertModeVolume(ctx, params)

		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.NotEmpty(tt, jobUUID)
		var job datamodel.Job
		err = store.DB().Where("resource_name = ?", dbVolume.Name).First(&job).Error
		assert.NoError(tt, err)
		assert.Equal(tt, jobUUID, job.UUID)
		assert.Equal(tt, string(datamodel.JobTypeManageBackupConfigExpertModeVolume), job.Type)
		mockLogger.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Success_WithBackupPolicy", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid-2"},
			WorkflowID: "workflow-id-2",
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(expertModeVolume, nil).Once()
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, createdJob.UUID, jobUUID)
		assert.NotNil(tt, backupConfig)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	// ── Vault lookup non-not-found error (line 119) ───────────────────────────
	t.Run("GetBackupVaultDBError_ReturnsErr", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).
			Return(nil, errors.New("db connection error"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "db connection error")
		mockStorage.AssertExpectations(tt)
	})

	// ── bv == nil && UseVCPRegion (line 124) ─────────────────────────────────
	t.Run("BackupVaultNotFound_UseVCPRegion_ReturnsNotFound", func(tt *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsNotFoundErr(err))
		assert.Contains(tt, err.Error(), "backup vault")
		mockStorage.AssertExpectations(tt)
	})

	// ── validateCRBBackupVault error (line 131) ───────────────────────────────
	t.Run("CRBVaultDisabled_ValidateCRBFails", func(tt *testing.T) {
		origCRB := utils.IsCrossRegionBackupEnabled()
		utils.SetCrossRegionBackupEnabledForTest(false)
		defer utils.SetCrossRegionBackupEnabledForTest(origCRB)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		crbVault := &datamodel.BackupVault{
			LifeCycleState:  datamodel.LifeCycleStateREADY,
			BackupVaultType: activities.CrossRegionBackupType,
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(crbVault, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), activities.CrossRegionBackupVaultErrMsg)
		mockStorage.AssertExpectations(tt)
	})

	// ── GetBackupPolicyByUUIDAndOwnerID non-not-found error (line 158) ────────
	t.Run("GetBackupPolicyDBError_ReturnsErr", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).
			Return(nil, errors.New("policy db error"))

		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "policy db error")
		mockStorage.AssertExpectations(tt)
	})

	// ── backupPolicy == nil && UseVCPRegion (line 163) ────────────────────────
	t.Run("BackupPolicyNotFound_UseVCPRegion_ReturnsNotFound", func(tt *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		// Vault must be found (non-nil) so code proceeds past vault check to the policy check.
		readyVault := &datamodel.BackupVault{LifeCycleState: datamodel.LifeCycleStateREADY}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(readyVault, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(nil, nil)

		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsNotFoundErr(err))
		assert.Contains(tt, err.Error(), "backup policy")
		mockStorage.AssertExpectations(tt)
	})

	// ── ImmutableBackup UnavailableErr (line 181) ─────────────────────────────
	t.Run("ImmutableBackup_UnavailableErr_ReturnsUnavailable", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		utils.SetImmutableBackupEnabledForTest(true)
		defer utils.SetImmutableBackupEnabledForTest(false)

		origFn := checkIsValidImmutableBackupPolicyWithRetry
		checkIsValidImmutableBackupPolicyWithRetry = func(_ context.Context, _ database.Storage, _, _ string, _ int64, _, _ string) error {
			return customerrors.NewUnavailableErr("service temporarily unavailable")
		}
		defer func() { checkIsValidImmutableBackupPolicyWithRetry = origFn }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		readyPolicy := &datamodel.BackupPolicy{LifeCycleState: datamodel.LifeCycleStateREADY}
		enabled := true
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("policy-uuid")
		params.ScheduledBackupEnabled = &enabled
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupPolicyByUUIDAndOwnerID(mock.Anything, "policy-uuid", int64(1)).Return(readyPolicy, nil)

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUnavailableErr(err))
		assert.Contains(tt, err.Error(), "temporarily unavailable")
		mockStorage.AssertExpectations(tt)
	})

	// ── Vault-switch GetBackupVault error (lines 206-207) ────────────────────
	t.Run("VaultSwitchCheck_GetBackupVaultError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithDifferentVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithDifferentVault, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").
			Return(nil, errors.New("vault lookup error"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "vault lookup error")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitchCheck_GetBackupCountError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		volumeWithDifferentVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}
		currentVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithDifferentVault, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(currentVault, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeAndVault(mock.Anything, "volume-uuid", int64(10)).
			Return(int64(0), errors.New("count error"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "count error")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_GCNVFlagOn_SameFamily_WithBackups_Allowed", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(true)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 1, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{10}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_GCNVFlagOn_CrossFamily_WithBackups_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(true)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: activities.GCBDRServiceType, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{10}, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Cannot switch from a")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_FlagOn_BackupInProgress_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(true, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "backup operation on the volume is currently in progress")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_FlagOff_BackupsExist_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(false)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		// Flag off: account-scoped lookup; must return a non-nil vault so the vault-switch check is reached.
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "new-vault-uuid", int64(1)).Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().GetBackupCountByVolumeAndVault(mock.Anything, "volume-uuid", int64(10)).Return(int64(3), nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "switching backup vault is not supported while backups exist")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_BVSOnly_NoBackups_AnyFamily_Allowed", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: activities.GCBDRServiceType, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_BVSOnly_GCNVDifferentAccount_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 999, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{}, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "belongs to a different account")
		mockStorage.AssertExpectations(tt)
	})

	// With only EnableBackupVaultSwitching on, a switch with backups is limited to GCBDR → GCBDR.
	t.Run("VaultSwitch_BVSOnly_GCBDRToGCBDR_WithBackups_Allowed", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: activities.GCBDRServiceType}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: activities.GCBDRServiceType, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{10}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_BVSOnly_GCNVToGCNV_WithBackups_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(false)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 1, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{10}, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "only allowed for cross project backup vaults")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_GCNVFlagOn_NoBackups_CrossFamily_Allowed", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(true)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: activities.GCBDRServiceType}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 1, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{}, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.Anything).Return(volumeWithVault, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		backupConfig, jobUUID, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, backupConfig)
		assert.Equal(tt, "job-uuid", jobUUID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_GCNVFlagOn_GCNVDifferentAccount_Blocked", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)
		origGCNV := utils.EnableGCNVVaultSwitching
		utils.SetEnableGCNVVaultSwitchingForTest(true)
		defer utils.SetEnableGCNVVaultSwitchingForTest(origGCNV)

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		oldVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV}
		newVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "new-vault-uuid"}, ServiceType: datamodel.ServiceTypeGCNV, AccountID: 999, LifeCycleState: datamodel.LifeCycleStateREADY}
		volumeWithVault := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			AccountID:    1,
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateREADY,
			Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}},
			BackupConfig: &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"},
		}

		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(volumeWithVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "new-vault-uuid").Return(newVault, nil)
		mockStorage.EXPECT().GetBackupVault(mock.Anything, "old-vault-uuid").Return(oldVault, nil)
		mockStorage.EXPECT().IsBackupInCreatingorDeletingStateByVolume(mock.Anything, "volume-uuid").Return(false, nil)
		mockStorage.EXPECT().GetDistinctBackupVaultIDsByVolumeUUID(mock.Anything, "volume-uuid").Return([]int64{10}, nil)

		params := baseParams()
		params.BackupVaultID = nillable.ToPointer("new-vault-uuid")
		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "belongs to a different account")
		mockStorage.AssertExpectations(tt)
	})

	// ── Deferred UpdateJob fails when workflow start fails (line 239) ─────────
	t.Run("WorkflowStartError_UpdateJobAlsoFails_LogsButReturnsWorkflowErr", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "workflow-id"}
		mockStorage.EXPECT().GetPool(mock.Anything, "pool-uuid", int64(1)).Return(poolView, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByUUIDndOwnerID(mock.Anything, "vault-uuid", int64(1)).Return(nil, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		// Set volume to UPDATING before launching the workflow.
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(expertModeVolume, nil).Once()
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("workflow start failed"))
		// Defer 2: mark volume ERROR after workflow launch fails.
		mockStorage.EXPECT().UpdateExpertModeVolume(mock.Anything, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(expertModeVolume, nil).Once()
		// Defer 1: UpdateJob also fails; deferred function logs the error and swallows it
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed").
			Return(errors.New("update job failed"))

		result, _, err := manageBackupConfigForExpertModeVolume(ctx, mockStorage, mockTemporal, baseParams())
		assert.Error(tt, err)
		assert.Nil(tt, result)
		// The returned error is still the workflow error, not the UpdateJob error
		assert.Contains(tt, err.Error(), "workflow start failed")
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

func TestRestoreOntapModeBackupExpertMode(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

	t.Run("GetOrCreateAccountError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account error")
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PoolIDRequired", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "PoolID is required")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DescribePoolError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(nil, errors.New("describe pool failed"))

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PoolNotExpertMode", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		poolViewNonONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: "DEFAULT"}}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewNonONTAP, nil)

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not an expert mode (ONTAP) pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetExpertModeVolumeByExternalUUIDError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(nil, errors.New("db error"))

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VolumeNotReady", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "emv-uuid"},
			State:     datamodel.LifeCycleStateCreating,
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Volume is not available")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateJobError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(nil, errors.New("create job failed"))

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdateExpertModeVolumeFieldsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
		}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(errors.New("update state failed"))
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "update state failed").Return(nil)

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ExecuteWorkflowError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
		}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(nil) // defer rollback
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed").Return(nil)

		result, err := restoreOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Success", func(tt *testing.T) {
		mockLogger := log.NewMockLogger(tt)
		mockLogger.EXPECT().InfoContext(mock.Anything, "Running AutoMigrate for model changes")
		store, err := database.SetupStorageForTest(mockLogger)
		assert.NoError(tt, err)
		err = database.ClearInMemoryDB(store.DB())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.DB().Create(account).Error
		assert.NoError(tt, err)
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "550e8400-e29b-41d4-a716-446655440000"},
			Name:           "test_pool",
			AccountID:      account.ID,
			SizeInBytes:    2199023255552,
			APIAccessMode:  commonparams.ONTAPMode,
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-west1-a"},
		}
		err = store.DB().Create(pool).Error
		assert.NoError(tt, err)
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "ext-volume-uuid",
			Name:         "expert-vol",
			State:        datamodel.LifeCycleStateAvailable,
			Style:        "flexvol",
			PoolID:       pool.ID,
			AccountID:    account.ID,
		}
		err = store.DB().Create(expertModeVolume).Error
		assert.NoError(tt, err)

		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test_account",
			PoolID:      pool.UUID,
			VolumeUUID:  expertModeVolume.ExternalUUID,
			BackupPath:  "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
			Region:      "us-east4",
		}

		orch := &GCPOrchestrator{storage: store, temporal: mockTemporal}
		result, err := orch.RestoreOntapModeBackup(ctx, params)

		assert.NoError(tt, err)
		assert.NotEmpty(tt, result)
		var job datamodel.Job
		err = store.DB().Where("resource_name = ?", expertModeVolume.UUID).First(&job).Error
		assert.NoError(tt, err)
		assert.Equal(tt, result, job.UUID)
		assert.Equal(tt, string(datamodel.JobTypeRestoreOntapModeBackup), job.Type)
		mockLogger.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

// TestSfrOntapModeBackup covers sfrOntapModeBackup: SFR-on-ONTAP orchestration (validation, backup resolution, job, state, workflow start).
func TestSfrOntapModeBackup(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	validBackupPath := "projects/p/locations/us-east4/backupVaults/bv/backups/backup-id"
	vendorIDSameRegion := "/projects/p/locations/us-east4/pools/pool1"

	t.Run("GetOrCreateAccountError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account error")
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
	})

	t.Run("PoolIDRequired", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "PoolID is required")
	})

	t.Run("DescribePoolError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(nil, errors.New("describe pool failed"))

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PoolNotExpertMode", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewNonONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: "DEFAULT"}}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewNonONTAP, nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not an expert mode (ONTAP) pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetExpertModeVolumeByExternalUUIDError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(nil, errors.New("db error"))

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VolumeNotReady", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "emv-uuid"},
			State:     datamodel.LifeCycleStateCreating,
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "Volume is not available")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BackupPathEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "",
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "emv-uuid"},
			State:     datamodel.LifeCycleStateAvailable,
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "BackupPath must be provided")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BackupPathInvalidFormat", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  "projects/p/locations/reg", // too few components
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "emv-uuid"},
			State:     datamodel.LifeCycleStateAvailable,
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not in correct format")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetBackupVaultError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: vendorIDSameRegion},
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(nil, errors.New("vault not found"))

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetBackupError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: vendorIDSameRegion},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10}}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(nil, errors.New("backup not found"))

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BackupNotAvailable", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: vendorIDSameRegion},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10}}
		backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{ID: 1}, State: datamodel.LifeCycleStateCreating}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not available")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateJobError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: vendorIDSameRegion},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10}}
		backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{ID: 1}, State: datamodel.LifeCycleStateAvailable}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(nil, errors.New("create job failed"))

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdateExpertModeVolumeFieldsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: vendorIDSameRegion},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10}}
		backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{ID: 1}, State: datamodel.LifeCycleStateAvailable}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "workflow-id"}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(errors.New("update state failed"))
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "update state failed").Return(nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ExecuteWorkflowError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: vendorIDSameRegion},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10}}
		backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{ID: 1}, State: datamodel.LifeCycleStateAvailable}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "workflow-id"}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(nil) // defer rollback
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed").Return(nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
			Region:      "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: vendorIDSameRegion},
		}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10}}
		backup := &datamodel.Backup{BaseModel: datamodel.BaseModel{ID: 1}, State: datamodel.LifeCycleStateAvailable}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "workflow-id"}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		result, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid", result)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

func Test_startExpertModeFlexCloneSplit(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	setup := func() (*datamodel.Account, *datamodel.PoolView, *datamodel.ExpertModeVolumes, *commonparams.ExpertModeFlexCloneSplitParams) {
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
				APIAccessMode: APIAccessModeONTAP,
				SizeInBytes:   1000,
			},
		}
		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{ID: 20, UUID: "db-vol-uuid"},
			ExternalUUID: "ext-vol-uuid",
			Name:         "clone-vol",
			SizeInBytes:  1000,
			SharedBytes:  900,
			State:        datamodel.LifeCycleStateAvailable,
			AccountID:    account.ID,
			PoolID:       pool.ID,
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
			},
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				IsFlexclone: true,
			},
		}
		params := &commonparams.ExpertModeFlexCloneSplitParams{
			VolumeUUID:  volume.ExternalUUID,
			VolumeName:  "",
			PoolUUID:    pool.UUID,
			AccountName: account.Name,
		}
		return account, pool, volume, params
	}

	t.Run("Success_WhenRemainingFreeSpaceCoversSharedBytes", func(tt *testing.T) {
		account, pool, volume, params := setup()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "wf-id",
			TrackingID: 1,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(pool, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		// remaining = pool 1000 - used 100 = 900; need = vol.SharedBytes = 900 -> pass.
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, volume.PoolID).
			Return(&database.ExpertModePoolCapacity{TotalSize: 100, VolumeCount: 1}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.UUID == volume.UUID && v.State == datamodel.LifeCycleStateUpdating
		})).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		err := _startExpertModeFlexCloneSplit(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Success_WhenOnlyVolumeNameProvided_UsesPoolNameLookup", func(tt *testing.T) {
		account, pool, volume, params := setup()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params.VolumeUUID = ""
		params.VolumeName = volume.Name
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "wf-id",
			TrackingID: 1,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(pool, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, volume.Name, pool.ID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, volume.PoolID).
			Return(&database.ExpertModePoolCapacity{TotalSize: 100, VolumeCount: 1}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.UUID == volume.UUID && v.State == datamodel.LifeCycleStateUpdating
		})).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		err := _startExpertModeFlexCloneSplit(ctx, mockStorage, temporal, params)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_InsufficientCapacity", func(tt *testing.T) {
		account, pool, volume, params := setup()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(pool, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		// remaining = 1000 - 950 = 50; need = vol.SharedBytes = 900 -> should fail.
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, volume.PoolID).
			Return(&database.ExpertModePoolCapacity{TotalSize: 950, VolumeCount: 1}, nil).Once()

		err := _startExpertModeFlexCloneSplit(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "insufficient pool capacity for flexclone split")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("Failure_ExecuteWorkflowFails_RollsBackStateAndUpdatesJob", func(tt *testing.T) {
		account, pool, volume, params := setup()
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		updatedVolume := *volume
		updatedVolume.State = datamodel.LifeCycleStateUpdating
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "wf-id",
			TrackingID: 99,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(pool, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, volume.PoolID).
			Return(&database.ExpertModePoolCapacity{TotalSize: 100, VolumeCount: 1}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.UUID == volume.UUID && v.State == datamodel.LifeCycleStateUpdating
		})).Return(&updatedVolume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("failed to start workflow")).Once()
		mockStorage.EXPECT().UpdateJob(ctx, createdJob.UUID, string(datamodel.JobsStateERROR), createdJob.TrackingID, mock.AnythingOfType("string")).
			Return(nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.UUID == volume.UUID && v.State == datamodel.LifeCycleStateAvailable
		})).Return(&updatedVolume, nil).Once()

		err := _startExpertModeFlexCloneSplit(ctx, mockStorage, temporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to start workflow")
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
}

func TestGetBackupConfigsForPool(t *testing.T) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
		Name:      "test_account",
	}

	pool := &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
		Name:          "test_pool",
		AccountID:     account.ID,
		APIAccessMode: commonparams.ONTAPMode,
	}

	t.Run("Success_WithBackupVaultAndPolicy", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-1",
				ExternalUUID: "vol-1",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID:  "vault-uuid-1",
					BackupPolicyID: "policy-uuid-1",
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{
			{BaseModel: datamodel.BaseModel{UUID: "vault-uuid-1"}, Name: "my-vault"},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{
			{BaseModel: datamodel.BaseModel{UUID: "policy-uuid-1"}, Name: "my-policy"},
		}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-1", configs[0].VolumeResourceID)
		assert.NotNil(tt, configs[0].BackupVaultPath)
		assert.Equal(tt, "projects/test_account/locations/us-east4/backupVaults/my-vault", *configs[0].BackupVaultPath)
		assert.NotNil(tt, configs[0].BackupPolicyPath)
		assert.Equal(tt, "projects/test_account/locations/us-east4/backupPolicies/my-policy", *configs[0].BackupPolicyPath)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NoBackupConfig", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-no-backup",
				BackupConfig: nil,
				ExternalUUID: "vol-no-backup",
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-no-backup", configs[0].VolumeResourceID)
		assert.Nil(tt, configs[0].BackupVaultPath)
		assert.Nil(tt, configs[0].BackupPolicyPath)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_EmptyVolumes", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Empty(tt, configs)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_VaultNotFoundInMap", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-orphan",
				ExternalUUID: "vol-orphan",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID:  "non-existent-vault-uuid",
					BackupPolicyID: "non-existent-policy-uuid",
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().GetBackupVault(ctx, "non-existent-vault-uuid").Return(nil, customerrors.NewNotFoundErr("backup vault", nil)).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-orphan", configs[0].VolumeResourceID)
		assert.Nil(tt, configs[0].BackupVaultPath)
		assert.Nil(tt, configs[0].BackupPolicyPath)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_CrossProjectVaultResolvedByUUID", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "the_vol_1",
				ExternalUUID: "vol-1",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID: "gcbdr-vault-uuid",
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().GetBackupVault(ctx, "gcbdr-vault-uuid").Return(&datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
			Name:        "cxwwmefkzval1783414955573",
			ServiceType: datamodel.ServiceTypeCrossProject,
			Account: &datamodel.Account{
				Name: "1075717434858",
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-central1")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.NotNil(tt, configs[0].BackupVaultPath)
		assert.Equal(tt, "projects/1075717434858/locations/us-central1/backupVaults/cxwwmefkzval1783414955573", *configs[0].BackupVaultPath)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CrossProjectVaultMissingAccount_Unresolved", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "the_vol_1",
				ExternalUUID: "vol-1",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID: "gcbdr-vault-uuid",
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().GetBackupVault(ctx, "gcbdr-vault-uuid").Return(&datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{UUID: "gcbdr-vault-uuid"},
			Name:        "cxwwmefkzval1783414955573",
			ServiceType: datamodel.ServiceTypeCrossProject,
		}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-central1")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Nil(tt, configs[0].BackupVaultPath)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_MultipleVolumes_MixedConfigs", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-with-vault-only",
				ExternalUUID: "vol-with-vault-only",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID: "vault-uuid-1",
				},
			},
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-2"},
				Name:         "vol-no-config",
				BackupConfig: nil,
				ExternalUUID: "vol-no-config",
			},
			{
				BaseModel: datamodel.BaseModel{UUID: "vol-uuid-3"},
				Name:      "vol-with-both",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID:  "vault-uuid-1",
					BackupPolicyID: "policy-uuid-1",
				},
				ExternalUUID: "vol-with-both",
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{
			{BaseModel: datamodel.BaseModel{UUID: "vault-uuid-1"}, Name: "my-vault"},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{
			{BaseModel: datamodel.BaseModel{UUID: "policy-uuid-1"}, Name: "my-policy"},
		}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 3)

		assert.Equal(tt, "vol-with-vault-only", configs[0].VolumeResourceID)
		assert.NotNil(tt, configs[0].BackupVaultPath)
		assert.Equal(tt, "projects/test_account/locations/us-east4/backupVaults/my-vault", *configs[0].BackupVaultPath)
		assert.Nil(tt, configs[0].BackupPolicyPath)

		assert.Equal(tt, "vol-no-config", configs[1].VolumeResourceID)
		assert.Nil(tt, configs[1].BackupVaultPath)
		assert.Nil(tt, configs[1].BackupPolicyPath)

		assert.Equal(tt, "vol-with-both", configs[2].VolumeResourceID)
		assert.NotNil(tt, configs[2].BackupVaultPath)
		assert.NotNil(tt, configs[2].BackupPolicyPath)
		assert.Equal(tt, "projects/test_account/locations/us-east4/backupPolicies/my-policy", *configs[2].BackupPolicyPath)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_AccountNotFound", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, "non_existent_account", "us-east4")

		assert.Error(tt, err)
		assert.Nil(tt, configs)
		assert.Contains(tt, err.Error(), "account not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_GetPoolError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(nil, errors.New("pool not found")).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.Error(tt, err)
		assert.Nil(tt, configs)
		assert.Contains(tt, err.Error(), "pool not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_NonONTAPPool", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		nonOntapPool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			Name:          "non_ontap_pool",
			AccountID:     account.ID,
			APIAccessMode: "STANDARD",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *nonOntapPool}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.Error(tt, err)
		assert.Nil(tt, configs)
		assert.Contains(tt, err.Error(), "backup configurations are only available for ONTAP pools")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ListExpertModeVolumesError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return(nil, errors.New("db error listing volumes")).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.Error(tt, err)
		assert.Nil(tt, configs)
		assert.Contains(tt, err.Error(), "db error listing volumes")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ListBackupVaultsError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return(nil, errors.New("db error listing vaults")).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.Error(tt, err)
		assert.Nil(tt, configs)
		assert.Contains(tt, err.Error(), "db error listing vaults")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ListBackupPoliciesError", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return(nil, errors.New("db error listing policies")).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.Error(tt, err)
		assert.Nil(tt, configs)
		assert.Contains(tt, err.Error(), "db error listing policies")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_BackupConfigWithEmptyIDs", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-empty-ids",
				ExternalUUID: "vol-empty-ids",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID:  "",
					BackupPolicyID: "",
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-empty-ids", configs[0].VolumeResourceID)
		assert.Nil(tt, configs[0].BackupVaultPath)
		assert.Nil(tt, configs[0].BackupPolicyPath)
	})

	t.Run("Success_WithScheduledBackupEnabled", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		scheduledBackupEnabled := true
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-1",
				ExternalUUID: "vol-sched-backup",
				BackupConfig: &datamodel.DataProtection{
					ScheduledBackupEnabled: &scheduledBackupEnabled,
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-sched-backup", configs[0].VolumeResourceID)
		assert.NotNil(tt, configs[0].ScheduledBackupEnabled)
		assert.True(tt, *configs[0].ScheduledBackupEnabled)
		assert.Nil(tt, configs[0].BackupChainBytes)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_WithBackupChainBytes", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		chainBytes := int64(1073741824) // 1 GiB
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-chain",
				ExternalUUID: "vol-chain",
				BackupConfig: &datamodel.DataProtection{
					BackupChainBytes: &chainBytes,
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-chain", configs[0].VolumeResourceID)
		assert.NotNil(tt, configs[0].BackupChainBytes)
		assert.Equal(tt, int64(1073741824), *configs[0].BackupChainBytes)
		assert.Nil(tt, configs[0].ScheduledBackupEnabled)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_WithAllNewFields", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		scheduledBackupEnabled := true
		chainBytes := int64(2147483648) // 2 GiB
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-full",
				ExternalUUID: "vol-full",
				BackupConfig: &datamodel.DataProtection{
					BackupVaultID:          "vault-uuid-1",
					BackupPolicyID:         "policy-uuid-1",
					ScheduledBackupEnabled: &scheduledBackupEnabled,
					BackupChainBytes:       &chainBytes,
				},
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{
			{BaseModel: datamodel.BaseModel{UUID: "vault-uuid-1"}, Name: "my-vault"},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{
			{BaseModel: datamodel.BaseModel{UUID: "policy-uuid-1"}, Name: "my-policy"},
		}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-full", configs[0].VolumeResourceID)
		assert.NotNil(tt, configs[0].BackupVaultPath)
		assert.NotNil(tt, configs[0].BackupPolicyPath)
		assert.NotNil(tt, configs[0].ScheduledBackupEnabled)
		assert.True(tt, *configs[0].ScheduledBackupEnabled)
		assert.NotNil(tt, configs[0].BackupChainBytes)
		assert.Equal(tt, int64(2147483648), *configs[0].BackupChainBytes)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NewFieldsNilWhenBackupConfigIsNil", func(tt *testing.T) {
		ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
		mockStorage := database.NewMockStorage(tt)

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().ListExpertModeVolumesByPoolID(ctx, pool.ID).Return([]*datamodel.ExpertModeVolumes{
			{
				BaseModel:    datamodel.BaseModel{UUID: "vol-uuid-1"},
				Name:         "vol-nil-config",
				ExternalUUID: "vol-nil-config",
				BackupConfig: nil,
			},
		}, nil).Once()
		mockStorage.EXPECT().ListBackupVaults(ctx, account.ID).Return([]*datamodel.BackupVault{}, nil).Once()
		mockStorage.EXPECT().ListBackupPolicies(ctx, mock.Anything).Return([]*datamodel.BackupPolicy{}, nil).Once()

		orch := &GCPOrchestrator{storage: mockStorage}
		configs, err := orch.GetBackupConfigsForPool(ctx, pool.UUID, account.Name, "us-east4")

		assert.NoError(tt, err)
		assert.Len(tt, configs, 1)
		assert.Equal(tt, "vol-nil-config", configs[0].VolumeResourceID)
		assert.Nil(tt, configs[0].ScheduledBackupEnabled)
		assert.Nil(tt, configs[0].BackupChainBytes)
		mockStorage.AssertExpectations(tt)
	})
}

// TestExpertModeVolumeToVolumeForSFR covers the unexported expertModeVolumeToVolumeForSFR helper.
func TestExpertModeVolumeToVolumeForSFR(t *testing.T) {
	emv := &datamodel.ExpertModeVolumes{
		BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
		ExternalUUID: "ext-uuid",
		Name:         "vol",
		Description:  "desc",
		State:        datamodel.LifeCycleStateREADY,
		SizeInBytes:  1024,
		AccountID:    1,
		PoolID:       2,
		SvmID:        3,
		Account:      &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "acc"},
		Pool:         &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 2}},
		Svm:          &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 3}},
	}
	vol := expertModeVolumeToVolumeForSFR(emv)
	assert.NotNil(t, vol)
	assert.Equal(t, "emv-uuid", vol.UUID)
	assert.Equal(t, "vol", vol.Name)
	assert.Equal(t, "desc", vol.Description)
	assert.Equal(t, datamodel.LifeCycleStateREADY, vol.State)
	assert.Equal(t, int64(1024), vol.SizeInBytes)
	assert.Equal(t, int64(1), vol.AccountID)
	assert.Equal(t, int64(2), vol.PoolID)
	assert.Equal(t, int64(3), vol.SvmID)
	assert.NotNil(t, vol.VolumeAttributes)
	assert.Equal(t, "ext-uuid", vol.VolumeAttributes.ExternalUUID)
	assert.Same(t, emv.Account, vol.Account)
	assert.Same(t, emv.Pool, vol.Pool)
	assert.Same(t, emv.Svm, vol.Svm)
}

func TestSFROntapModeBackup(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	validBackupPath := "projects/p/locations/us-east4/backupVaults/bv/backups/backup-id"

	t.Run("GetOrCreateAccountError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account error")
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "account error")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PoolIDRequired", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "PoolID is required")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DescribePoolError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(nil, errors.New("describe pool failed"))

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "describe pool failed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PoolNotExpertMode", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewNonONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: "DEFAULT"}}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewNonONTAP, nil)

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "not an expert mode (ONTAP) pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetExpertModeVolumeByExternalUUIDError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(nil, errors.New("db error"))

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db error")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VolumeNotReady", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "emv-uuid"},
			State:     datamodel.LifeCycleStateCreating,
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Volume is not available")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetBackupVaultByNameAndOwnerIDError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(nil, errors.New("vault not found"))

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "vault not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetBackupByNameAndBackupVaultIDError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "bv-uuid"}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(nil, errors.New("backup not found"))

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "backup not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BackupPathEmpty", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      "",
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "BackupPath must be provided")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BackupPathInvalidFormat", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      "too/short",
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "Backup path is not in correct format")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BackupNotAvailable", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "bv-uuid"}}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "backup-id",
			State:     datamodel.LifeCycleStateCreating,
		}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "not available")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateJobError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "bv-uuid"}}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "backup-id",
			State:     datamodel.LifeCycleStateAvailable,
		}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(nil, errors.New("create job failed"))

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "create job failed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdateExpertModeVolumeFieldsError_DeferRollback", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "bv-uuid"}}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "backup-id",
			State:     datamodel.LifeCycleStateAvailable,
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Style:        "flexvol",
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(errors.New("update state failed"))
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "update state failed").Return(nil)

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ExecuteWorkflowError_DeferRollback", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolViewONTAP := &datamodel.PoolView{Pool: datamodel.Pool{APIAccessMode: commonparams.ONTAPMode}}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "bv-uuid"}}
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Name:      "backup-id",
			State:     datamodel.LifeCycleStateAvailable,
		}
		createdJob := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}, WorkflowID: "wf-id"}
		expertModeVolume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
			ExternalUUID: "volume-uuid",
			State:        datamodel.LifeCycleStateAvailable,
			Style:        "flexvol",
			Pool:         &datamodel.Pool{VendorID: "/projects/p/locations/us-east4/pools/pool1"},
		}
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName:     "test-account",
			PoolID:          "pool-uuid",
			VolumeUUID:      "volume-uuid",
			BackupPath:      validBackupPath,
			SourceFileList:  []string{"/file1"},
			RestoreFilePath: "/restore",
			Region:          "us-east4",
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()
		mockStorage.EXPECT().DescribePool(mock.Anything, "pool-uuid", int64(1)).Return(poolViewONTAP, nil)
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(mock.Anything, "volume-uuid").Return(expertModeVolume, nil)
		mockStorage.EXPECT().GetBackupVaultByNameAndOwnerID(mock.Anything, "bv", "1").Return(backupVault, nil)
		mockStorage.EXPECT().GetBackupByNameAndBackupVaultID(mock.Anything, "backup-id", int64(10)).Return(backup, nil)
		mockStorage.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(createdJob, nil)
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow failed"))
		mockStorage.EXPECT().UpdateExpertModeVolumeFields(mock.Anything, "volume-uuid", mock.Anything).Return(nil)
		mockStorage.EXPECT().UpdateJob(mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow failed").Return(nil)

		_, err := sfrOntapModeBackup(ctx, mockStorage, mockTemporal, params)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("SFROntapModeBackup_OrchestratorWrapper", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowenginemock.NewMockTemporalTestClient(tt)
		params := &commonparams.RestoreOntapModeBackupParams{
			AccountName: "test-account",
			PoolID:      "pool-uuid",
			VolumeUUID:  "volume-uuid",
			BackupPath:  validBackupPath,
		}
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account error")
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		orch := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		_, err := orch.SFROntapModeBackup(ctx, params)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestShouldSkipLogicalPoolCapacityCheck(t *testing.T) {
	ctx := context.Background()
	thin := datamodel.ProvisioningModelThin
	thick := datamodel.ProvisioningModelThick
	unknown := datamodel.ProvisioningModel("WEIRD")

	extremeONTAPPool := func(hostUUID string) *datamodel.Pool {
		return &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  commonparams.ServiceLevelExtreme,
			HostUUID:      hostUUID,
			SizeInBytes:   1000,
		}
	}

	t.Run("non_extreme_returns_false_without_host_lookup", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		skip, err := shouldSkipLogicalPoolCapacityCheck(ctx, mockStorage, &datamodel.Pool{
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  "FLEX",
			HostUUID:      "host-1",
		})
		assert.NoError(tt, err)
		assert.False(tt, skip)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("premium_returns_false_without_host_lookup_when_flag_disabled", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		skip, err := shouldSkipLogicalPoolCapacityCheck(ctx, mockStorage, &datamodel.Pool{
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  commonparams.ServiceLevelPremium,
			HostUUID:      "host-1",
		})
		assert.NoError(tt, err)
		assert.False(tt, skip)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_host_uuid_missing_returns_bad_request", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		skip, err := shouldSkipLogicalPoolCapacityCheck(ctx, mockStorage, extremeONTAPPool(""))
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), commonparams.POValidationHostUUIDMsg)
		assert.False(tt, skip)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_host_lookup_fails_returns_error", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(nil, errors.New("host missing")).Once()
		skip, err := shouldSkipLogicalPoolCapacityCheck(ctx, mockStorage, extremeONTAPPool("host-1"))
		assert.EqualError(tt, err, "host missing")
		assert.False(tt, skip)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_provisioning_model_unknown_returns_bad_request", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &unknown}, nil).Once()
		skip, err := shouldSkipLogicalPoolCapacityCheck(ctx, mockStorage, extremeONTAPPool("host-1"))
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), "unknown provisioning model")
		assert.False(tt, skip)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("thick_and_nil_model_do_not_skip", func(tt *testing.T) {
		for _, model := range []*datamodel.ProvisioningModel{nil, &thick} {
			mockStorage := database.NewMockStorage(tt)
			mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: model}, nil).Once()
			skip, err := shouldSkipLogicalPoolCapacityCheck(ctx, mockStorage, extremeONTAPPool("host-1"))
			assert.NoError(tt, err)
			assert.False(tt, skip)
			mockStorage.AssertExpectations(tt)
		}
	})

	t.Run("thin_skips", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &thin}, nil).Once()
		skip, err := shouldSkipLogicalPoolCapacityCheck(ctx, mockStorage, extremeONTAPPool("host-1"))
		assert.NoError(tt, err)
		assert.True(tt, skip)
		mockStorage.AssertExpectations(tt)
	})
}

func TestCanFitInPool_ThinProvisioning(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	thin := datamodel.ProvisioningModelThin

	t.Run("thin_extreme_ontap_skips_capacity_query", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &thin}, nil).Once()
		pool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  commonparams.ServiceLevelExtreme,
			HostUUID:      "host-1",
			SizeInBytes:   100,
		}
		err := canFitInPool(ctx, mockStorage, pool, 1000)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("thick_extreme_ontap_still_enforces", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{}, nil).Once()
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, int64(10)).
			Return(&database.ExpertModePoolCapacity{TotalSize: 50, VolumeCount: 1}, nil).Once()
		pool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  commonparams.ServiceLevelExtreme,
			HostUUID:      "host-1",
			SizeInBytes:   100,
		}
		err := canFitInPool(ctx, mockStorage, pool, 60)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "insufficient pool capacity")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("non_extreme_still_enforces_without_host_lookup", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, int64(10)).
			Return(&database.ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 0}, nil).Once()
		pool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  "FLEX",
			SizeInBytes:   100,
		}
		err := canFitInPool(ctx, mockStorage, pool, 50)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

func TestValidatePoolCapacityForExpertModeFlexCloneSplit_ThinProvisioning(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	thin := datamodel.ProvisioningModelThin
	vol := &datamodel.ExpertModeVolumes{SharedBytes: 900}

	t.Run("thin_extreme_ontap_skips_capacity_query", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &thin}, nil).Once()
		pool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  commonparams.ServiceLevelExtreme,
			HostUUID:      "host-1",
			SizeInBytes:   100,
		}
		err := validatePoolCapacityForExpertModeFlexCloneSplit(ctx, mockStorage, vol, pool)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("non_thin_still_enforces_shared_bytes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetExpertModePoolUsedCapacityAndVolumeCount(ctx, int64(10)).
			Return(&database.ExpertModePoolCapacity{TotalSize: 200, VolumeCount: 1}, nil).Once()
		pool := &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  "FLEX",
			SizeInBytes:   1000,
		}
		err := validatePoolCapacityForExpertModeFlexCloneSplit(ctx, mockStorage, vol, pool)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "insufficient pool capacity for flexclone split")
		mockStorage.AssertExpectations(tt)
	})
}

func TestThinProvisioning_OperationCapacityGates(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})
	thin := datamodel.ProvisioningModelThin

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"},
		Name:      "test-account",
	}
	extremePool := func(hostUUID string, size int64) *datamodel.Pool {
		return &datamodel.Pool{
			BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
			Name:          "extreme-pool",
			AccountID:     account.ID,
			APIAccessMode: APIAccessModeONTAP,
			ServiceLevel:  commonparams.ServiceLevelExtreme,
			HostUUID:      hostUUID,
			SizeInBytes:   size,
		}
	}

	t.Run("When_create_on_extreme_pool_without_host_returns_error", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		pool := extremePool("", 100)
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "vol-1", pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()

		err := _createExpertModeVolume(ctx, mockStorage, nil, &commonparams.ExpertModeVolumeParams{
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "vol-1",
			SizeInBytes: 1000,
			Style:       "flexvol",
			SvmName:     "svm-1",
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Contains(tt, err.Error(), commonparams.POValidationHostUUIDMsg)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_create_on_thin_extreme_pool_skips_logical_capacity", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		pool := extremePool("host-1", 100)
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "vol-thin", pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &thin}, nil).Once()

		// Intentionally omit SVM so create fails after capacity evaluation.
		err := _createExpertModeVolume(ctx, mockStorage, nil, &commonparams.ExpertModeVolumeParams{
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "vol-thin",
			SizeInBytes: 1000, // exceeds pool size 100
			Style:       "flexvol",
		})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "neither svmName nor svmUUID has been passed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_clone_create_on_thin_extreme_pool_skips_logical_capacity", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		pool := extremePool("host-1", 100)
		parent := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "parent-db"},
			ExternalUUID: "parent-ext",
			Name:         "parent-vol",
			PoolID:       pool.ID,
			SizeInBytes:  1000, // exceeds pool size
			State:        datamodel.LifeCycleStateAvailable,
		}
		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByNameAndPoolID(ctx, "clone-thin", pool.ID).Return(nil, gorm.ErrRecordNotFound).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, parent.ExternalUUID).Return(parent, nil).Once()
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &thin}, nil).Once()

		err := _createExpertModeVolume(ctx, mockStorage, nil, &commonparams.ExpertModeVolumeParams{
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
			Action:      "post",
			VolumeName:  "clone-thin",
			Style:       "flexvol",
			Clone: &commonparams.ExpertModeVolumeCloneParams{
				IsFlexclone: true,
				ParentVolume: &commonparams.ExpertModeVolumeCloneParent{
					UUID: parent.ExternalUUID,
				},
			},
		})
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "neither svmName nor svmUUID has been passed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_expand_on_thin_extreme_pool_skips_logical_capacity", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		pool := extremePool("host-1", 100)
		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "vol-db", ID: 20},
			ExternalUUID: "vol-ext",
			Name:         "vol-1",
			AccountID:    account.ID,
			PoolID:       pool.ID,
			SizeInBytes:  50,
			State:        datamodel.LifeCycleStateAvailable,
			Pool:         pool,
		}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "wf-id",
			TrackingID: 1,
		}
		mockWorkflowRun := &mockWorkflowRun{}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &thin}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.AnythingOfType("*datamodel.ExpertModeVolumes")).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(mockWorkflowRun, nil).Once()

		err := _updateExpertModeVolume(ctx, mockStorage, temporal, &commonparams.ExpertModeVolumeParams{
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
			VolumeUUID:  volume.ExternalUUID,
			SizeInBytes: 1000, // increase far beyond pool size
		})
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})

	t.Run("When_flexclone_split_on_thin_extreme_pool_skips_logical_capacity", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		temporal := workflowenginemock.NewMockTemporalTestClient(tt)
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		pool := extremePool("host-1", 100)
		volume := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "clone-db", ID: 20},
			ExternalUUID: "clone-ext",
			Name:         "clone-vol",
			AccountID:    account.ID,
			PoolID:       pool.ID,
			SizeInBytes:  1000,
			SharedBytes:  900,
			State:        datamodel.LifeCycleStateAvailable,
			Pool: &datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: pool.UUID},
			},
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				IsFlexclone: true,
			},
		}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "wf-id",
			TrackingID: 1,
		}

		mockStorage.EXPECT().GetPool(ctx, pool.UUID, account.ID).Return(&datamodel.PoolView{Pool: *pool}, nil).Once()
		mockStorage.EXPECT().GetExpertModeVolumeByExternalUUID(ctx, volume.ExternalUUID).Return(volume, nil).Once()
		mockStorage.EXPECT().GetHost(ctx, "host-1").Return(&datamodel.Host{ProvisioningModel: &thin}, nil).Once()
		mockStorage.EXPECT().UpdateExpertModeVolume(ctx, mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
			return v.UUID == volume.UUID && v.State == datamodel.LifeCycleStateUpdating
		})).Return(volume, nil).Once()
		mockStorage.EXPECT().CreateJob(ctx, mock.AnythingOfType("*datamodel.Job")).Return(createdJob, nil).Once()
		temporal.EXPECT().ExecuteWorkflow(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil).Once()

		err := _startExpertModeFlexCloneSplit(ctx, mockStorage, temporal, &commonparams.ExpertModeFlexCloneSplitParams{
			AccountName: account.Name,
			PoolUUID:    pool.UUID,
			VolumeUUID:  volume.ExternalUUID,
		})
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		temporal.AssertExpectations(tt)
	})
}

func TestResolveExpertModeSvm_CrossSvmNamesPoolSvm(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	poolView := func(apiAccessMode string) *datamodel.PoolView {
		return &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:     datamodel.BaseModel{ID: 10, UUID: "pool-uuid"},
				Name:          "po-pool",
				APIAccessMode: apiAccessMode,
				ServiceLevel:  commonparams.ServiceLevelExtreme,
				HostUUID:      "host-1",
			},
		}
	}
	poolSvm := &datamodel.Svm{
		BaseModel: datamodel.BaseModel{ID: 5, UUID: "svm-uuid"},
		Name:      "svm_po_01",
		PoolID:    10,
		State:     datamodel.LifeCycleStateREADY,
	}

	t.Run("WhenSvmNameBelongsToAnotherSvm", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(ctx, "svm_other_01", int64(10)).
			Return(nil, customerrors.NewNotFoundErr("svm", nil)).Once()
		mockStorage.EXPECT().GetSvmsByPoolID(ctx, int64(10)).Return([]*datamodel.Svm{poolSvm}, nil).Once()

		_, err := resolveExpertModeSvm(ctx, mockStorage, poolView(APIAccessModeONTAP), &commonparams.ExpertModeVolumeParams{
			SvmName: "svm_other_01",
		})
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsBadRequestErr(err))
		assert.Equal(tt, "SVM with name 'svm_other_01' not found in pool; the SVM for this pool is 'svm_po_01'", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSvmUuidBelongsToAnotherSvm", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByExternalUUID(ctx, "other-svm-uuid", int64(10)).
			Return(nil, customerrors.NewNotFoundErr("svm", nil)).Once()
		mockStorage.EXPECT().GetSvmsByPoolID(ctx, int64(10)).Return([]*datamodel.Svm{poolSvm}, nil).Once()

		_, err := resolveExpertModeSvm(ctx, mockStorage, poolView(APIAccessModeONTAP), &commonparams.ExpertModeVolumeParams{
			SvmUuid: "other-svm-uuid",
		})
		assert.Error(tt, err)
		assert.Equal(tt, "SVM with UUID 'other-svm-uuid' not found in pool; the SVM for this pool is 'svm_po_01'", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenPoolHasMultipleSvmsDeletedOnesAreExcluded", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(ctx, "svm_other_01", int64(10)).
			Return(nil, customerrors.NewNotFoundErr("svm", nil)).Once()
		mockStorage.EXPECT().GetSvmsByPoolID(ctx, int64(10)).Return([]*datamodel.Svm{
			{Name: "svm_po_02", State: datamodel.LifeCycleStateREADY},
			poolSvm,
			{Name: "svm_po_gone", State: datamodel.LifeCycleStateDeleted},
		}, nil).Once()

		_, err := resolveExpertModeSvm(ctx, mockStorage, poolView(APIAccessModeONTAP), &commonparams.ExpertModeVolumeParams{
			SvmName: "svm_other_01",
		})
		assert.Error(tt, err)
		assert.Equal(tt, "SVM with name 'svm_other_01' not found in pool; the SVMs for this pool are 'svm_po_01', 'svm_po_02'", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenPoolSvmLookupFailsMessageStaysPlain", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(ctx, "svm_other_01", int64(10)).
			Return(nil, customerrors.NewNotFoundErr("svm", nil)).Once()
		mockStorage.EXPECT().GetSvmsByPoolID(ctx, int64(10)).Return(nil, errors.New("db down")).Once()

		_, err := resolveExpertModeSvm(ctx, mockStorage, poolView(APIAccessModeONTAP), &commonparams.ExpertModeVolumeParams{
			SvmName: "svm_other_01",
		})
		assert.Error(tt, err)
		assert.Equal(tt, "SVM with name 'svm_other_01' not found in pool", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenPoolIsNotOntapModeNoSvmHintIsAdded", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockStorage.EXPECT().GetSvmByNameAndPoolID(ctx, "svm_other_01", int64(10)).
			Return(nil, customerrors.NewNotFoundErr("svm", nil)).Once()

		_, err := resolveExpertModeSvm(ctx, mockStorage, poolView(commonparams.DEFAULTMode), &commonparams.ExpertModeVolumeParams{
			SvmName: "svm_other_01",
		})
		assert.Error(tt, err)
		assert.Equal(tt, "SVM with name 'svm_other_01' not found in pool", err.Error())
		mockStorage.AssertExpectations(tt)
	})
}
