package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
)

func TestCreateVolumePerformanceGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("SuccessfullyCreatesVPGAndStartsWorkflow", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
			},
		}
		createdVPG := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid-1"},
			Name:            "test-vpg",
			PoolID:          1,
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
			IsAutoGen:       false,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("CreateVolumePerformanceGroupWithCap", ctx, mock.MatchedBy(func(vpg *datamodel.VolumePerformanceGroup) bool {
			return vpg != nil && vpg.Name == "test-vpg" && vpg.PoolID == 1 && vpg.ThroughputMibps == 100 &&
				vpg.Iops == 1000 && vpg.IsPerVolume() && !vpg.IsAutoGen
		}), mock.Anything).Return(createdVPG, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, "vpg-uuid-1").Return(nil, nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "vpg-uuid-1", result.UUID)
		assert.Equal(tt, "test-vpg", result.Name)
		assert.Equal(tt, int64(100), result.ThroughputMibps)
		assert.Equal(tt, int64(1000), result.Iops)
		assert.Equal(tt, models.AllocationTypePerVolume, result.AllocationType)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenExecuteWorkflowFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
			},
		}
		createdVPG := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid-1"},
			Name:            "test-vpg",
			PoolID:          1,
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
			IsAutoGen:       false,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("CreateVolumePerformanceGroupWithCap", ctx, mock.Anything, mock.Anything).Return(createdVPG, nil)
		mockStorage.On("DeleteVolumePerformanceGroup", ctx, createdVPG).Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, "vpg-uuid-1").Return(nil, errors.New("workflow start failed"))

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "workflow start failed", err.Error())
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenAccountNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "account not found", err.Error())
	})

	t.Run("ReturnsErrorWhenGetPoolFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(nil, errors.New("pool not found"))

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "pool not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenPoolNotReady", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     "DELETING",
				QosType:   utils.QosTypeManual,
			},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "pool is not in a ready state")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenPoolQosTypeIsNotManual", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeAuto, // Not manual
			},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "manual QoS type")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenOntapModePool", func(tt *testing.T) {
		mm := newMonkeyMockAndPatch(tt)
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:     datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:         datamodel.LifeCycleStateREADY,
				QosType:       utils.QosTypeManual,
				APIAccessMode: common.ONTAPMode,
			},
		}
		mm.EXPECT().getAccountWithName(ctx, mock.Anything, "test-account").Return(account, nil)
		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "ONTAP mode")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenCreateVolumePerformanceGroupFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
			},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("CreateVolumePerformanceGroupWithCap", ctx, mock.Anything, mock.Anything).Return(nil, errors.New("db create failed"))

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "db create failed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VSCP_6089_WhenAtVPGCap_ReturnsCapExceededError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
			},
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(12000), nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "maximum number of Volume Performance Groups")
		assert.Contains(tt, err.Error(), "12000")
		assert.Contains(tt, err.Error(), "Delete unused VPGs")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VSCP_6089_RejectsWhenAboveCap", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
			},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(12500), nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "test-pool-id", Name: "test-vpg",
			ThroughputMibps: 100, Iops: 1000, AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VSCP_6089_PropagatesDBCountError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
			},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(0), errors.New("db: connection lost"))

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "test-pool-id", Name: "test-vpg",
			ThroughputMibps: 100, Iops: 1000, AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.False(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "db: connection lost")
		mockStorage.AssertExpectations(tt)
		mockStorage.AssertNotCalled(tt, "CreateVolumePerformanceGroupWithCap", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("DeferLogsErrorWhenWorkflowFailsAndDeleteVPGFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
			},
		}
		createdVPG := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid-1"},
			Name:            "test-vpg",
			PoolID:          1,
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
			IsAutoGen:       false,
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("CreateVolumePerformanceGroupWithCap", ctx, mock.Anything, mock.Anything).Return(createdVPG, nil)
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, "vpg-uuid-1").Return(nil, errors.New("workflow start failed"))
		// Defer runs and tries to delete VPG; delete also fails (covers logger.Error in defer)
		mockStorage.On("DeleteVolumePerformanceGroup", ctx, createdVPG).Return(errors.New("delete vpg failed"))

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName:     "test-account",
			PoolID:          "test-pool-id",
			Name:            "test-vpg",
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VSCP_6580_RejectsWhenThroughputExceedsPoolTotal", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
				PoolAttributes: &datamodel.PoolAttributes{
					ThroughputMibps: 100,
					Iops:            1600,
				},
			},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(0), nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "test-pool-id", Name: "test-vpg",
			ThroughputMibps: 101, Iops: 800, AllocationType: models.AllocationTypePerVolume,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "throughput")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VSCP_6580_RejectsWhenIopsExceedsPoolTotal", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				State:     datamodel.LifeCycleStateREADY,
				QosType:   utils.QosTypeManual,
				PoolAttributes: &datamodel.PoolAttributes{
					ThroughputMibps: 100,
					Iops:            1600,
				},
			},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("CountVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(int64(0), nil)

		params := &common.CreateVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "test-pool-id", Name: "test-vpg",
			ThroughputMibps: 50, Iops: 1601, AllocationType: models.AllocationTypeShared,
		}

		result, err := orchestrator.CreateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "IOPS")
		mockStorage.AssertExpectations(tt)
	})
}

func TestValidatePoolLimitsForVPGCreate(t *testing.T) {
	t.Run("ReturnsNilWhenPoolHasNoCustomPerformance", func(t *testing.T) {
		pool := &datamodel.PoolView{Throughput: 100, Iops: 500}
		err := validatePoolLimitsForVPGCreate(pool, 999, 9999)
		assert.NoError(t, err)
	})

	t.Run("ReturnsErrorWhenThroughputExceedsPoolTotal", func(t *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1600},
			},
		}
		err := validatePoolLimitsForVPGCreate(pool, 101, 800)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "throughput")
	})

	t.Run("ReturnsErrorWhenIopsExceedsPoolTotal", func(t *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1600},
			},
		}
		err := validatePoolLimitsForVPGCreate(pool, 50, 1601)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "IOPS")
	})

	t.Run("ReturnsNilWhenLimitsEqualPoolTotal", func(t *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1600},
			},
		}
		err := validatePoolLimitsForVPGCreate(pool, 100, 1600)
		assert.NoError(t, err)
	})

	t.Run("ReturnsNilWhenLimitsWithinPoolTotal", func(t *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1600},
			},
		}
		err := validatePoolLimitsForVPGCreate(pool, 50, 800)
		assert.NoError(t, err)
	})

	t.Run("Brownfield_PriorPoolConsumptionDoesNotAffectPerVPGDeclaredLimitCheck", func(t *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1600},
			},
			// Simulate brownfield pool with other existing VPGs/volumes already consuming capacity.
			Throughput: 95,
			Iops:       1500,
		}
		// Per-VPG guard only compares against pool totals, not current consumption by other VPGs.
		err := validatePoolLimitsForVPGCreate(pool, 100, 1600)
		assert.NoError(t, err)
	})
}

func TestListVolumePerformanceGroups(t *testing.T) {
	ctx := context.Background()

	t.Run("SuccessfullyListsVPGs", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}
		vpgs := []*datamodel.VolumePerformanceGroup{
			{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid-1"},
				Name:            "vpg-1",
				PoolID:          1,
				ThroughputMibps: 1000,
				Iops:            5000,
				AllocationType: models.AllocationTypeShared,
				IsAutoGen:       false,
			},
			{
				BaseModel:       datamodel.BaseModel{ID: 2, UUID: "vpg-uuid-2"},
				Name:            "vpg-2",
				PoolID:          1,
				ThroughputMibps: 2000,
				Iops:            10000,
				AllocationType: models.AllocationTypePerVolume,
				IsAutoGen:       false,
			},
			{
				BaseModel:       datamodel.BaseModel{ID: 3, UUID: "vpg-uuid-auto"},
				Name:            "autoGenerated-volume-123",
				PoolID:          1,
				ThroughputMibps: 500,
				Iops:            2500,
				AllocationType: models.AllocationTypeShared,
				IsAutoGen:       true, // This should be filtered out
			},
		}

		// Mock getAccountWithName
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("ListVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(vpgs, nil)

		params := &common.ListVolumePerformanceGroupsParams{
			AccountName: "test-account",
			PoolID:      "test-pool-id",
		}

		result, err := orchestrator.ListVolumePerformanceGroups(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should only return 2 VPGs (auto-generated one is filtered out)
		assert.Len(tt, result, 2)
		assert.Equal(tt, "vpg-1", result[0].Name)
		assert.Equal(tt, "vpg-2", result[1].Name)
		assert.Equal(tt, int64(1000), result[0].ThroughputMibps)
		assert.Equal(tt, int64(2000), result[1].ThroughputMibps)
		assert.Equal(tt, int64(5000), result[0].Iops)
		assert.Equal(tt, int64(10000), result[1].Iops)
		assert.Equal(tt, models.AllocationTypeShared, result[0].AllocationType)
		assert.Equal(tt, models.AllocationTypePerVolume, result[1].AllocationType)
	})

	t.Run("ReturnsEmptyListWhenNoVPGs", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("ListVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return([]*datamodel.VolumePerformanceGroup{}, nil)

		params := &common.ListVolumePerformanceGroupsParams{
			AccountName: "test-account",
			PoolID:      "test-pool-id",
		}

		result, err := orchestrator.ListVolumePerformanceGroups(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Len(tt, result, 0)
	})

	t.Run("ReturnsEmptyListWhenOnlyAutoGeneratedVPGs", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}
		// Only auto-generated VPGs
		vpgs := []*datamodel.VolumePerformanceGroup{
			{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid-auto-1"},
				Name:            "autoGenerated-volume-123",
				PoolID:          1,
				ThroughputMibps: 500,
				Iops:            2500,
				AllocationType: models.AllocationTypeShared,
				IsAutoGen:       true,
			},
			{
				BaseModel:       datamodel.BaseModel{ID: 2, UUID: "vpg-uuid-auto-2"},
				Name:            "autoGenerated-volume-456",
				PoolID:          1,
				ThroughputMibps: 750,
				Iops:            3750,
				AllocationType: models.AllocationTypePerVolume,
				IsAutoGen:       true,
			},
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("ListVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(vpgs, nil)

		params := &common.ListVolumePerformanceGroupsParams{
			AccountName: "test-account",
			PoolID:      "test-pool-id",
		}

		result, err := orchestrator.ListVolumePerformanceGroups(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		// Should return empty list since all VPGs are auto-generated
		assert.Len(tt, result, 0)
	})

	t.Run("ReturnsErrorWhenAccountNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &common.ListVolumePerformanceGroupsParams{
			AccountName: "test-account",
			PoolID:      "test-pool-id",
		}

		result, err := orchestrator.ListVolumePerformanceGroups(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "account not found")
	})

	t.Run("ReturnsErrorWhenPoolNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(nil, errors.New("pool not found"))

		params := &common.ListVolumePerformanceGroupsParams{
			AccountName: "test-account",
			PoolID:      "test-pool-id",
		}

		result, err := orchestrator.ListVolumePerformanceGroups(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "pool not found")
	})

	t.Run("ReturnsNotFoundErrWhenPoolDeletedOrNonExistent", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "deleted-pool-id", int64(1)).Return(nil, utilErrors.NewNotFoundErr("pool", nil))

		params := &common.ListVolumePerformanceGroupsParams{
			AccountName: "test-account",
			PoolID:      "deleted-pool-id",
		}

		result, err := orchestrator.ListVolumePerformanceGroups(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, utilErrors.IsNotFoundErr(err))
	})

	t.Run("ReturnsErrorWhenListVPGsFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("ListVolumePerformanceGroupsByPoolID", ctx, int64(1)).Return(nil, errors.New("list vpgs failed"))

		params := &common.ListVolumePerformanceGroupsParams{
			AccountName: "test-account",
			PoolID:      "test-pool-id",
		}

		result, err := orchestrator.ListVolumePerformanceGroups(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "list vpgs failed")
	})
}

func TestGetVolumePerformanceGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("SuccessfullyGetsVPG", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:            "test-vpg",
			PoolID:          1,
			ThroughputMibps: 1500,
			Iops:            7500,
			AllocationType: models.AllocationTypeShared,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)

		params := &common.GetVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		result, err := orchestrator.GetVolumePerformanceGroup(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-vpg", result.Name)
		assert.Equal(tt, "vpg-uuid", result.UUID)
		assert.Equal(tt, int64(1500), result.ThroughputMibps)
		assert.Equal(tt, int64(7500), result.Iops)
		assert.Equal(tt, models.AllocationTypeShared, result.AllocationType)
	})

	t.Run("ReturnsErrorWhenVPGNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(nil, errors.New("vpg not found"))

		params := &common.GetVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		result, err := orchestrator.GetVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "vpg not found")
	})

	t.Run("ReturnsErrorWhenVPGBelongsToDifferentPool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:            "test-vpg",
			PoolID:          2, // Different pool ID
			ThroughputMibps: 1500,
			Iops:            7500,
			AllocationType: models.AllocationTypeShared,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)

		params := &common.GetVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		result, err := orchestrator.GetVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "volume performance group does not belong to the specified pool")
	})

	t.Run("ReturnsErrorWhenAccountNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		params := &common.GetVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		result, err := orchestrator.GetVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "account not found")
	})

	t.Run("ReturnsErrorWhenPoolNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(nil, errors.New("pool not found"))

		params := &common.GetVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		result, err := orchestrator.GetVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "pool not found")
	})

	t.Run("HandlesVPGWithZeroIOPS", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			},
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:            "test-vpg",
			PoolID:          1,
			ThroughputMibps: 1500,
			Iops:            0, // Zero IOPS
			AllocationType: models.AllocationTypeShared,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)

		params := &common.GetVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		result, err := orchestrator.GetVolumePerformanceGroup(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, int64(0), result.Iops) // IOPS should be 0 when zero
	})
}

func TestConvertDatastoreVPGToModel(t *testing.T) {
	t.Run("ReturnsNilWhenInputNil", func(tt *testing.T) {
		assert.Nil(tt, convertDatastoreVPGToModel(nil))
	})

	t.Run("ConvertsFields", func(tt *testing.T) {
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 7, UUID: "vpg-uuid"},
			Name:            "vpg-name",
			PoolID:          9,
			ThroughputMibps: 3000,
			Iops:            9000,
			AllocationType: models.AllocationTypeShared,
		}

		result := convertDatastoreVPGToModel(vpg)
		assert.NotNil(tt, result)
		assert.Equal(tt, int64(7), result.ID)
		assert.Equal(tt, "vpg-uuid", result.UUID)
		assert.Equal(tt, "vpg-name", result.Name)
		assert.Equal(tt, int64(3000), result.ThroughputMibps)
		assert.Equal(tt, int64(9000), result.Iops)
		assert.Equal(tt, models.AllocationTypeShared, result.AllocationType)
	})
}

func TestValidatePoolCapacityForVPGUpdate(t *testing.T) {
	ctx := context.Background()

	// mockStorageWithVPGCount returns a mock storage that reports the given volume count for the VPG.
	mockStorageWithVPGCount := func(tt *testing.T, vpgID int64, count int64) *database.MockStorage {
		m := database.NewMockStorage(tt)
		m.On("GetVolumeCountByVolumePerformanceGroupID", ctx, vpgID).Return(count, nil)
		return m
	}

	t.Run("ReturnsNilWhenPoolHasNoCustomPerformance", func(tt *testing.T) {
		// Pool has no PoolAttributes (or zero throughput), so we return nil before calling storage
		pool := &datamodel.PoolView{Throughput: 100, Iops: 500}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1}, ThroughputMibps: 50, Iops: 200, AllocationType: models.AllocationTypeShared}
		se := database.NewMockStorage(tt) // GetVolumeCountByVolumePerformanceGroupID not called (early return)
		throughput := int64(100)
		iops := int64(500)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &throughput, &iops)
		assert.NoError(tt, err)
	})

	t.Run("ReturnsErrorWhenThroughputExceedsPoolTotal", func(tt *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1}, ThroughputMibps: 50, Iops: 500, AllocationType: models.AllocationTypeShared}
		se := database.NewMockStorage(tt)
		// With SHARED allocation and 1 volume: pre=50, post=110 → total 50-50+110=110 > 100
		throughputExceed := int64(110)
		iops := int64(500)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &throughputExceed, &iops)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "throughput")
	})

	t.Run("ReturnsErrorWhenIopsExceedsPoolTotal", func(tt *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1}, ThroughputMibps: 50, Iops: 500, AllocationType: models.AllocationTypeShared}
		se := database.NewMockStorage(tt)
		throughput := int64(50)
		iopsExceed := int64(1100)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &throughput, &iopsExceed)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "IOPS")
	})

	t.Run("ReturnsNilWhenNewValuesWithinCapacity", func(tt *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1}, ThroughputMibps: 50, Iops: 500, AllocationType: models.AllocationTypeShared}
		se := mockStorageWithVPGCount(tt, 1, 1)
		throughput := int64(40)
		iops := int64(400)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &throughput, &iops)
		assert.NoError(tt, err)
	})

	// When no volumes are assigned, pool-capacity deltas are zero; however, declared limits must still not exceed pool totals.
	t.Run("ReturnsErrorWhenNoVolumesAssigned_SharedVPGAndDeclaredLimitsExceedPoolTotals", func(tt *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1}, ThroughputMibps: 999, Iops: 9999, AllocationType: models.AllocationTypeShared}
		se := database.NewMockStorage(tt)
		newThroughput := int64(999)
		newIops := int64(9999)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &newThroughput, &newIops)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "throughput")
	})

	t.Run("ReturnsErrorWhenNoVolumesAssigned_NonSharedVPGAndDeclaredLimitsExceedPoolTotals", func(tt *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 2}, ThroughputMibps: 500, Iops: 5000, AllocationType: models.AllocationTypePerVolume}
		se := database.NewMockStorage(tt)
		newThroughput := int64(600)
		newIops := int64(6000)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &newThroughput, &newIops)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "throughput")
	})

	t.Run("ReturnsNilWhenNoVolumesAssignedAndDeclaredLimitsWithinPoolTotals", func(tt *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 3}, ThroughputMibps: 80, Iops: 800, AllocationType: models.AllocationTypeShared}
		se := mockStorageWithVPGCount(tt, 3, 0)
		newThroughput := int64(90)
		newIops := int64(900)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &newThroughput, &newIops)
		assert.NoError(tt, err)
	})

	t.Run("AccountsForNonSharedVPGWithMultipleVolumes", func(tt *testing.T) {
		// Pool 1000 MiBps; 3 volumes on this VPG, PER_VOLUME → pre = 100*3 = 300. Other = 1000-300 = 700. Post = 200*3 = 600. Total = 700+600 = 1300 > 1000 → error
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 1000, Iops: 10000},
			},
			Throughput: 1000, // current utilized (includes this VPG's 300)
			Iops:       10000,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 10}, ThroughputMibps: 100, Iops: 1000, AllocationType: models.AllocationTypePerVolume}
		se := mockStorageWithVPGCount(tt, 10, 3)
		newThroughput := int64(200) // 200*3 = 600 post; 1000 - 300 + 600 = 1300 > 1000
		newIops := int64(2000)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &newThroughput, &newIops)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "throughput")
	})

	// Shared VPG: pre/post = vpg value once (no multiply). Multiple volumes share the same allocation.
	t.Run("SharedVPGWithMultipleVolumes_WithinCapacity", func(tt *testing.T) {
		// Pool 500 MiBps; shared VPG with 5 volumes, pre=100 post=120 → total = pool.Throughput - 100 + 120. Use pool.Throughput=100 → 100-100+120=120 ≤ 500
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 500, Iops: 5000},
			},
			Throughput: 100,
			Iops:       1000,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 5}, ThroughputMibps: 100, Iops: 1000, AllocationType: models.AllocationTypeShared}
		se := mockStorageWithVPGCount(tt, 5, 5)
		newThroughput := int64(120)
		newIops := int64(1200)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &newThroughput, &newIops)
		assert.NoError(tt, err)
	})

	t.Run("SharedVPGWithMultipleVolumes_ExceedsThroughput", func(tt *testing.T) {
		// Pool 500; shared VPG pre=100, post=450 with pool configured=400 -> total 400-100+450=750 > 500.
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 500, Iops: 5000},
			},
			Throughput: 400,
			Iops:       1000,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 5}, ThroughputMibps: 100, Iops: 1000, AllocationType: models.AllocationTypeShared}
		se := mockStorageWithVPGCount(tt, 5, 5)
		newThroughput := int64(450)
		newIops := int64(1000)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &newThroughput, &newIops)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "throughput")
	})

	// Non-shared VPG: pre/post = vpg * numVolumes. Update to lower value can stay within capacity.
	t.Run("NonSharedVPGWithMultipleVolumes_WithinCapacity", func(tt *testing.T) {
		// Pool 500; 2 volumes, PER_VOLUME. pre=100*2=200, post=80*2=160. total=200-200+160=160 ≤ 500
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 500, Iops: 5000},
			},
			Throughput: 200,
			Iops:       2000,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 7}, ThroughputMibps: 100, Iops: 1000, AllocationType: models.AllocationTypePerVolume}
		se := mockStorageWithVPGCount(tt, 7, 2)
		newThroughput := int64(80)
		newIops := int64(800)
		err := validatePoolCapacityForVPGUpdate(ctx, se, pool, vpg, &newThroughput, &newIops)
		assert.NoError(tt, err)
	})

	t.Run("ReturnsErrorWhenGetVolumeCountFails", func(tt *testing.T) {
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1}, ThroughputMibps: 50, Iops: 500, AllocationType: models.AllocationTypeShared}
		mockStorage := database.NewMockStorage(tt)
		mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(0), errors.New("db error"))
		err := validatePoolCapacityForVPGUpdate(ctx, mockStorage, pool, vpg, nil, nil)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "db error")
	})
}

func TestUpdateVolumePerformanceGroup(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnsErrorWhenAccountNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		throughput := int64(200)
		iops := int64(5000)
		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "test-vpg-id",
			Name:                     "updated-vpg",
			ThroughputMibps:          &throughput,
			Iops:                     &iops,
		}

		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "account not found")
	})

	t.Run("ReturnsErrorWhenPoolNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: nil}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(nil, errors.New("pool not found"))

		throughput := int64(200)
		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
			ThroughputMibps:          &throughput,
		}
		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "pool not found")
	})

	t.Run("ReturnsErrorWhenPoolNotManualQoS", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: nil}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, QosType: "auto"},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)

		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}
		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "manual QoS")
	})

	t.Run("ReturnsErrorWhenVPGNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: nil}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, QosType: "manual"},
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(nil, errors.New("vpg not found"))

		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}
		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "vpg not found")
	})

	t.Run("ReturnsErrorWhenVPGBelongsToDifferentPool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: nil}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, QosType: "manual"},
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"},
			PoolID:    2,
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)

		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}
		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "does not belong to the specified pool")
	})

	t.Run("ReturnsErrorWhenVPGIsAutogenerated", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: nil}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, QosType: "manual"},
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"},
			PoolID:    1,
			IsAutoGen: true,
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)

		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}
		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "only manually created volume performance groups can be updated")
	})

	t.Run("ReturnsErrorWhenAggregatePoolCapacityWouldBeExceeded", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: nil}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				QosType:        "manual",
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 1000, Iops: 10000},
			},
			Throughput: 900,
			Iops:       9000,
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			PoolID:          1,
			ThroughputMibps: 50,
			Iops:            500,
			AllocationType: models.AllocationTypeShared,
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
		mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(1), nil)
		// Declared limits stay under pool totals (200 <= 1000), but aggregate configured throughput would exceed:
		// 900 - 50 + 200 = 1050 > 1000
		throughputExceedAggregate := int64(200)
		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
			ThroughputMibps:          &throughputExceedAggregate,
		}
		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Sum of configured throughput")
	})

	t.Run("ReturnsErrorWhenUpdatedDeclaredLimitsExceedPoolTotalsEvenWithoutAssignedVolumes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: nil}

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:      datamodel.BaseModel{ID: 1},
				QosType:        "manual",
				PoolAttributes: &datamodel.PoolAttributes{ThroughputMibps: 100, Iops: 1000},
			},
			Throughput: 50,
			Iops:       500,
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			PoolID:          1,
			ThroughputMibps: 50,
			Iops:            500,
			AllocationType:  models.AllocationTypeShared,
		}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "test-pool-id", int64(1)).Return(pool, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
		throughputExceed := int64(110)
		params := &common.UpdateVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "test-pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
			ThroughputMibps:          &throughputExceed,
		}
		result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "throughput")
	})
}

func TestUpdateVolumePerformanceGroup_SuccessfullyStartsWorkflow(t *testing.T) {
	ctx := context.Background()

	mockStorage := database.NewMockStorage(t)
	mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
	orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
	pool := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			QosType:   utils.QosTypeManual,
		},
	}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "old-name",
		PoolID:           1,
		ThroughputMibps:  100,
		Iops:             500,
		AllocationType: models.AllocationTypeShared,
		OntapQosPolicyID: "ontap-id",
	}
	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "wf-id",
		State:      string(datamodel.JobsStateNEW),
	}

	originalGetAccountWithName := getAccountWithName
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = originalGetAccountWithName }()

	mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(pool, nil)
	mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(createdJob, nil)
	mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateUpdating, "").Return(nil)
	mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	throughput := int64(200)
	iops := int64(600)
	params := &common.UpdateVolumePerformanceGroupParams{
		AccountName:              "test-account",
		PoolID:                   "pool-id",
		VolumePerformanceGroupID: "vpg-uuid",
		Name:                     "new-name",
		ThroughputMibps:          &throughput,
		Iops:                     &iops,
	}

	result, jobUUID, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "job-uuid", jobUUID)
	assert.Equal(t, "vpg-uuid", result.UUID)
	mockStorage.AssertExpectations(t)
}

func TestUpdateVolumePerformanceGroup_SetUpdatingStateFails(t *testing.T) {
	ctx := context.Background()

	mockStorage := database.NewMockStorage(t)
	orchestrator := &GCPOrchestrator{storage: mockStorage}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
	pool := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			QosType:   utils.QosTypeManual,
		},
	}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "vpg-name",
		PoolID:           1,
		ThroughputMibps:  100,
		Iops:             500,
		AllocationType: models.AllocationTypeShared,
		OntapQosPolicyID: "ontap-id",
	}
	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "wf-id",
		State:      string(datamodel.JobsStateNEW),
	}

	originalGetAccountWithName := getAccountWithName
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = originalGetAccountWithName }()

	mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(pool, nil)
	mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(createdJob, nil)
	mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateUpdating, "").Return(errors.New("state update failed"))
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

	params := &common.UpdateVolumePerformanceGroupParams{
		AccountName:              "test-account",
		PoolID:                   "pool-id",
		VolumePerformanceGroupID: "vpg-uuid",
	}

	result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "state update failed")
	mockStorage.AssertExpectations(t)
}

func TestUpdateVolumePerformanceGroup_WorkflowFails_RollbackAlsoFails(t *testing.T) {
	ctx := context.Background()

	mockStorage := database.NewMockStorage(t)
	mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)
	orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
	pool := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			QosType:   utils.QosTypeManual,
		},
	}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "vpg-name",
		PoolID:           1,
		ThroughputMibps:  100,
		Iops:             500,
		AllocationType: models.AllocationTypeShared,
		OntapQosPolicyID: "ontap-id",
	}
	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "wf-id",
		State:      string(datamodel.JobsStateNEW),
	}

	originalGetAccountWithName := getAccountWithName
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = originalGetAccountWithName }()

	mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(pool, nil)
	mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(createdJob, nil)
	mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateUpdating, "").Return(nil)
	mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow failed"))
	mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails).Return(errors.New("rollback also failed"))
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

	params := &common.UpdateVolumePerformanceGroupParams{
		AccountName:              "test-account",
		PoolID:                   "pool-id",
		VolumePerformanceGroupID: "vpg-uuid",
	}

	result, _, err := orchestrator.UpdateVolumePerformanceGroup(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "workflow failed")
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolumePerformanceGroup_SetDeletingStateFails(t *testing.T) {
	ctx := context.Background()

	mockStorage := database.NewMockStorage(t)
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			DeploymentName:  "deploy-1",
			PoolCredentials: &datamodel.PoolCredentials{},
		},
	}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "vpg-name",
		OntapQosPolicyID: "ontap-policy",
		PoolID:           1,
	}
	createdJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid-delete"},
		Type:      string(datamodel.JobTypeDeleteVolumePerformanceGroup),
		State:     string(datamodel.JobsStateNEW),
	}

	originalGetAccountWithName := getAccountWithName
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = originalGetAccountWithName }()

	mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
	mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
	mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(0), nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(createdJob, nil)
	mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateDeleting, "").Return(errors.New("set deleting state failed"))
	mockStorage.On("UpdateJob", ctx, "job-uuid-delete", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

	o := &GCPOrchestrator{storage: mockStorage}
	params := &common.DeleteVolumePerformanceGroupParams{
		AccountName:              "test-account",
		PoolID:                   "pool-id",
		VolumePerformanceGroupID: "vpg-uuid",
	}

	deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, deletedVpg)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "set deleting state failed")
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolumePerformanceGroup_WorkflowFails_RollbackAlsoFails(t *testing.T) {
	withFastWorkflowRetry(t)
	ctx := context.Background()

	mockStorage := database.NewMockStorage(t)
	mockTemporal := workflowEngineMock.NewMockTemporalTestClient(t)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
	poolView := &datamodel.PoolView{
		Pool: datamodel.Pool{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			DeploymentName:  "deploy-1",
			PoolCredentials: &datamodel.PoolCredentials{},
		},
	}
	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
		Name:             "vpg-name",
		OntapQosPolicyID: "ontap-policy",
		PoolID:           1,
	}
	createdJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid-delete"},
		Type:      string(datamodel.JobTypeDeleteVolumePerformanceGroup),
		State:     string(datamodel.JobsStateNEW),
	}

	originalGetAccountWithName := getAccountWithName
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = originalGetAccountWithName }()

	mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
	mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
	mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(0), nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(createdJob, nil)
	mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateDeleting, "").Return(nil)
	mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("temporal unavailable"))
	mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails).Return(errors.New("rollback also failed"))
	mockStorage.On("UpdateJob", ctx, "job-uuid-delete", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

	o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
	params := &common.DeleteVolumePerformanceGroupParams{
		AccountName:              "test-account",
		PoolID:                   "pool-id",
		VolumePerformanceGroupID: "vpg-uuid",
	}

	deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, deletedVpg)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "temporal unavailable")
	mockStorage.AssertExpectations(t)
}

func TestDeleteVolumePerformanceGroup(t *testing.T) {
	ctx := context.Background()

	createdJob := &datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-uuid-delete"},
		Type:      string(datamodel.JobTypeDeleteVolumePerformanceGroup),
		State:     string(datamodel.JobsStateNEW),
	}

	mockCreateJob := func(mockStorage *database.MockStorage) {
		mockStorage.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.Type == string(datamodel.JobTypeDeleteVolumePerformanceGroup) &&
				j.State == string(datamodel.JobsStateNEW)
		})).Return(createdJob, nil)
	}

	_ = func(mockStorage *database.MockStorage) {
		mockStorage.On("UpdateJob", ctx, "job-uuid-delete", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)
	}

	_ = func(mockStorage *database.MockStorage) {
		mockStorage.On("UpdateJob", ctx, "job-uuid-delete", string(datamodel.JobsStateDONE), 0, "").Return(nil)
	}

	t.Run("Success_StartsDeleteWorkflow", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "deploy-1",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "vpg-name",
			OntapQosPolicyID: "ontap-policy-name",
			PoolID:           1,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
		mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(0), nil)
		mockCreateJob(mockStorage)
		mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateDeleting, "").Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, deletedVpg)
		assert.Equal(tt, "vpg-uuid", deletedVpg.UUID)
		assert.Equal(tt, "job-uuid-delete", jobUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Conflict_WhenVolumesAttached", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 1}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
		mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(2), nil)

		o := &GCPOrchestrator{storage: mockStorage}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.True(tt, utilErrors.IsConflictErr(err))
		assert.Contains(tt, err.Error(), "attached to one or more volumes")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("NotFound_WhenVPGMissing", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(nil, utilErrors.NewNotFoundErr("vpg", nil))

		o := &GCPOrchestrator{storage: mockStorage}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.True(tt, utilErrors.IsNotFoundErr(err))
		mockStorage.AssertExpectations(tt)
	})

	t.Run("BadRequest_WhenVPGBelongsToDifferentPool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 2}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)

		o := &GCPOrchestrator{storage: mockStorage}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "does not belong to the specified pool")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenWorkflowStartFails", func(tt *testing.T) {
		withFastWorkflowRetry(tt)
		mockStorage := database.NewMockStorage(tt)
		mockTemporal := workflowEngineMock.NewMockTemporalTestClient(tt)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:       datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
				DeploymentName:  "deploy-1",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"},
			Name:             "vpg-name",
			OntapQosPolicyID: "ontap-policy-name",
			PoolID:           1,
		}

		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()

		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
		mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(0), nil)
		mockCreateJob(mockStorage)
		mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateDeleting, "").Return(nil)
		mockTemporal.EXPECT().ExecuteWorkflow(ctx, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("temporal unavailable"))
		mockStorage.On("UpdateVolumePerformanceGroupState", ctx, "vpg-uuid", datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails).Return(nil)
		mockStorage.On("UpdateJob", ctx, "job-uuid-delete", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName:              "test-account",
			PoolID:                   "pool-id",
			VolumePerformanceGroupID: "vpg-uuid",
		}

		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.Contains(tt, err.Error(), "temporal unavailable")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenGetPoolFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(nil, errors.New("pool not found"))
		o := &GCPOrchestrator{storage: mockStorage}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "pool-id", VolumePerformanceGroupID: "vpg-uuid",
		}
		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.Contains(tt, err.Error(), "pool not found")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenGetVolumePerformanceGroupByUUIDFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(nil, errors.New("db error"))
		o := &GCPOrchestrator{storage: mockStorage}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "pool-id", VolumePerformanceGroupID: "vpg-uuid",
		}
		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.Contains(tt, err.Error(), "db error")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenGetVolumeCountByVolumePerformanceGroupIDFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, PoolID: 1}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
		mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(0), errors.New("db error"))
		o := &GCPOrchestrator{storage: mockStorage}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "pool-id", VolumePerformanceGroupID: "vpg-uuid",
		}
		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.Contains(tt, err.Error(), "db error")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenCreateJobFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"}
		poolView := &datamodel.PoolView{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}}}
		vpg := &datamodel.VolumePerformanceGroup{BaseModel: datamodel.BaseModel{ID: 1, UUID: "vpg-uuid"}, Name: "vpg-name", PoolID: 1}
		originalGetAccountWithName := getAccountWithName
		getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getAccountWithName = originalGetAccountWithName }()
		mockStorage.On("GetPool", ctx, "pool-id", int64(1)).Return(poolView, nil)
		mockStorage.On("GetVolumePerformanceGroupByUUID", ctx, "vpg-uuid").Return(vpg, nil)
		mockStorage.On("GetVolumeCountByVolumePerformanceGroupID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("job create failed"))
		o := &GCPOrchestrator{storage: mockStorage}
		params := &common.DeleteVolumePerformanceGroupParams{
			AccountName: "test-account", PoolID: "pool-id", VolumePerformanceGroupID: "vpg-uuid",
		}
		deletedVpg, jobUUID, err := o.DeleteVolumePerformanceGroup(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, deletedVpg)
		assert.Empty(tt, jobUUID)
		assert.Contains(tt, err.Error(), "job create failed")
		mockStorage.AssertExpectations(tt)
	})
}
