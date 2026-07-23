package backgroundactivities

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	ontaprestmodel "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/testsuite"
)

func init() {
	vlm.SetActiveProvider(vlm.OCICloud)
}

func TestAutoTierSyncActivity_UpdateAggregateInOntap(t *testing.T) {
	t.Run("UpdateAggregateInOntapSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateAggregatesInOntap)

		node := &models.Node{
			EndpointAddress: "test-endpoint",
		}
		tieringFullnessThreshold := int64(80)

		// Mock provider and aggregate
		mockProvider := new(vsa.MockProvider)
		aggregate1 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid1",
			Name: "aggr1",
		}
		aggregate2 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid2",
			Name: "aggr2",
		}
		aggregate3 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid3",
			Name: "aggr3",
		}

		// Patch vsa.GetProviderByNodeWithFastConnection
		originalGetProviderByNode := vsa.GetProviderByNodeWithFastConnection
		defer func() { vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNode }()
		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetAggregateByName", "aggr1").Return(aggregate1, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate1.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(nil)
		mockProvider.On("GetAggregateByName", "aggr2").Return(aggregate2, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate2.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(nil)
		mockProvider.On("GetAggregateByName", "aggr3").Return(aggregate3, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate3.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(nil)

		_, err := env.ExecuteActivity(activity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, []string{"aggr1", "aggr2", "aggr3"})
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("UpdateAggregateInOntap_GetProviderFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateAggregatesInOntap)

		node := &models.Node{
			EndpointAddress: "test-endpoint",
		}
		tieringFullnessThreshold := int64(80)

		// Patch vsa.GetProviderByNodeWithFastConnection to return error
		originalGetProviderByNode := vsa.GetProviderByNodeWithFastConnection
		defer func() { vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNode }()
		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider")
		}

		_, err := env.ExecuteActivity(activity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, []string{"aggr1", "aggr2", "aggr3"})
		assert.Error(tt, err)
	})

	t.Run("UpdateAggregateInOntap_GetAggregateFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateAggregatesInOntap)

		node := &models.Node{
			EndpointAddress: "test-endpoint",
		}
		tieringFullnessThreshold := int64(80)

		mockProvider := new(vsa.MockProvider)

		// Patch vsa.GetProviderByNodeWithFastConnection
		originalGetProviderByNode := vsa.GetProviderByNodeWithFastConnection
		defer func() { vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNode }()
		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetAggregateByName", activities.AggregateName).Return(nil, errors.New("failed to get aggregate"))

		_, err := env.ExecuteActivity(activity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, []string{"aggr1", "aggr2", "aggr3"})
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("UpdateAggregateInOntap_UpdateAggregateFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateAggregatesInOntap)

		node := &models.Node{
			EndpointAddress: "test-endpoint",
		}
		tieringFullnessThreshold := int64(80)

		mockProvider := new(vsa.MockProvider)
		aggregate := &vsa.Aggregate{
			UUID: "test-aggregate-uuid",
			Name: activities.AggregateName,
		}

		// Patch vsa.GetProviderByNodeWithFastConnection
		originalGetProviderByNode := vsa.GetProviderByNodeWithFastConnection
		defer func() { vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNode }()
		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetAggregateByName", activities.AggregateName).Return(aggregate, nil)
		mockProvider.On("UpdateAggregate", mock.AnythingOfType("vsa.UpdateAggregateParams")).Return(errors.New("failed to update aggregate"))

		_, err := env.ExecuteActivity(activity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, []string{"aggr1", "aggr2", "aggr3"})
		assert.Error(tt, err)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("UpdateAggregateInOntap_FirstAggregateSucceedsSecondFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateAggregatesInOntap)

		node := &models.Node{
			EndpointAddress: "test-endpoint",
		}
		tieringFullnessThreshold := int64(80)
		aggrNames := []string{"aggr1", "aggr2", "aggr3"}

		mockProvider := new(vsa.MockProvider)
		aggregate1 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid-1",
			Name: "aggr1",
		}
		aggregate2 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid-2",
			Name: "aggr2",
		}

		// Patch vsa.GetProviderByNodeWithFastConnection
		originalGetProviderByNode := vsa.GetProviderByNodeWithFastConnection
		defer func() { vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNode }()
		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// First aggregate succeeds
		mockProvider.On("GetAggregateByName", "aggr1").Return(aggregate1, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate1.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(nil)

		// Second aggregate fails
		mockProvider.On("GetAggregateByName", "aggr2").Return(aggregate2, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate2.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(errors.New("failed to update aggregate aggr2"))

		// Third aggregate should still be processed (function continues after errors)
		aggregate3 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid-3",
			Name: "aggr3",
		}
		mockProvider.On("GetAggregateByName", "aggr3").Return(aggregate3, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate3.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(nil)

		_, err := env.ExecuteActivity(activity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, aggrNames)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to update aggregate aggr2")
		mockProvider.AssertExpectations(tt)
	})

	t.Run("UpdateAggregateInOntap_MultipleAggregatesFail_ReturnsConcatenatedErrors", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateAggregatesInOntap)

		node := &models.Node{
			EndpointAddress: "test-endpoint",
		}
		tieringFullnessThreshold := int64(80)
		aggrNames := []string{"aggr1", "aggr2", "aggr3"}

		mockProvider := new(vsa.MockProvider)
		aggregate1 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid-1",
			Name: "aggr1",
		}
		aggregate2 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid-2",
			Name: "aggr2",
		}
		aggregate3 := &vsa.Aggregate{
			UUID: "test-aggregate-uuid-3",
			Name: "aggr3",
		}

		// Patch vsa.GetProviderByNodeWithFastConnection
		originalGetProviderByNode := vsa.GetProviderByNodeWithFastConnection
		defer func() { vsa.GetProviderByNodeWithFastConnection = originalGetProviderByNode }()
		vsa.GetProviderByNodeWithFastConnection = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// First aggregate fails
		mockProvider.On("GetAggregateByName", "aggr1").Return(aggregate1, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate1.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(errors.New("failed to update aggregate aggr1"))

		// Second aggregate fails
		mockProvider.On("GetAggregateByName", "aggr2").Return(aggregate2, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate2.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(errors.New("failed to update aggregate aggr2"))

		// Third aggregate succeeds
		mockProvider.On("GetAggregateByName", "aggr3").Return(aggregate3, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate3.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(nil)

		_, err := env.ExecuteActivity(activity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, aggrNames)
		assert.Error(tt, err)
		// Verify both errors are in the concatenated error message
		assert.Contains(tt, err.Error(), "failed to update aggregate aggr1")
		assert.Contains(tt, err.Error(), "failed to update aggregate aggr2")
		mockProvider.AssertExpectations(tt)
	})

	t.Run("UpdateAggregateInOntap_UsesRegularConnectionWhenFlagDisabled", func(tt *testing.T) {
		originalFlag := autoTieringFastOntapConnection
		defer func() { autoTieringFastOntapConnection = originalFlag }()
		autoTieringFastOntapConnection = false

		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateAggregatesInOntap)

		node := &models.Node{
			EndpointAddress: "test-endpoint",
		}
		tieringFullnessThreshold := int64(80)

		mockProvider := new(vsa.MockProvider)
		aggregate := &vsa.Aggregate{
			UUID: "test-aggregate-uuid",
			Name: "aggr1",
		}

		originalGetProviderByNode := vsa.GetProviderByNode
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()
		vsa.GetProviderByNode = func(ctx context.Context, node *models.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}

		mockProvider.On("GetAggregateByName", "aggr1").Return(aggregate, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.UUID == aggregate.UUID && params.TieringFullnessThreshold == tieringFullnessThreshold
		})).Return(nil)

		_, err := env.ExecuteActivity(activity.UpdateAggregatesInOntap, node, tieringFullnessThreshold, []string{"aggr1"})
		assert.NoError(tt, err)
		mockProvider.AssertExpectations(tt)
	})
}

func TestAutoTierSyncActivity_SegregatePools(t *testing.T) {
	t.Run("SegregatePoolsSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "pool-to-pause-uuid",
				AccountID: 123,
				Name:      "pool-to-pause",
			},
			{
				UUID:      "pool-to-resume-uuid",
				AccountID: 124,
				Name:      "pool-to-resume",
			},
			{
				UUID:      "pool-to-autoresize-uuid",
				AccountID: 125,
				Name:      "pool-to-autoresize",
			},
			{
				UUID:      "pool-not-ready-uuid",
				AccountID: 126,
				Name:      "pool-not-ready",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"pool-to-pause-uuid": {
				PoolConsumptionHotTier:              500000000000, // 500GB
				PoolConsumptionColdTier:             600000000000, // 600GB - will exceed pool size
				PoolConsumptionHotTierForAutoResize: 500000000000,
			},
			"pool-to-resume-uuid": {
				PoolConsumptionHotTier:              200000000000, // 200GB
				PoolConsumptionColdTier:             100000000000, // 100GB - under pool size
				PoolConsumptionHotTierForAutoResize: 200000000000,
			},
			"pool-to-autoresize-uuid": {
				PoolConsumptionHotTier:              500000000000, // 100% of 500GB hot tier
				PoolConsumptionColdTier:             50000000000,  // 50GB
				PoolConsumptionHotTierForAutoResize: 500000000000,
			},
		}

		// Mock pool responses
		pausePool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "pool-to-pause-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000, // 1TB
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes: 500000000000, // 500GB
					TieringStatus:      datamodel.TieringStatusResumed,
				},
			},
		}

		resumePool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 2, UUID: "pool-to-resume-uuid"},
				AccountID:        124,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000, // 1TB
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes: 500000000000, // 500GB
					TieringStatus:      datamodel.TieringStatusPaused,
				},
			},
		}

		autoResizePool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 3, UUID: "pool-to-autoresize-uuid"},
				AccountID:        125,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000, // 1TB
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      500000000000, // 500GB
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		notReadyPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 4, UUID: "pool-not-ready-uuid"},
				AccountID:        126,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateCreating,
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "pool-to-pause-uuid", mock.Anything).Return(&pausePool.Pool, nil)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "pool-to-resume-uuid", mock.Anything).Return(&resumePool.Pool, nil)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "pool-to-autoresize-uuid", mock.Anything).Return(&autoResizePool.Pool, nil)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "pool-not-ready-uuid", mock.Anything).Return(&notReadyPool.Pool, nil)

		// When flag is disabled (default), checkPoolVolumesWithBypassModeEnabled is called for pools eligible for auto-resize.
		// Mock GetVolumesByPoolID to return no bypass volumes so auto-resize proceeds.
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(3)).Return([]*datamodel.Volume{}, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 1)
		assert.Equal(tt, "pool-to-pause-uuid", result[PoolsToPauseKey][0].UUID)
		assert.Len(tt, result[PoolsToResumeKey], 1)
		assert.Equal(tt, "pool-to-resume-uuid", result[PoolsToResumeKey][0].UUID)
		assert.Len(tt, result[PoolsToAutoResizeKey], 1)
		assert.Equal(tt, "pool-to-autoresize-uuid", result[PoolsToAutoResizeKey][0].UUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsWithEmptyPools", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{}
		poolConsumptionsMap := map[string]map[string]float64{}

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
	})

	t.Run("SegregatePoolsGetPoolFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "failed-pool-uuid",
				AccountID: 123,
				Name:      "failed-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"failed-pool-uuid": {
				PoolConsumptionHotTier:  500000000000,
				PoolConsumptionColdTier: 600000000000,
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "failed-pool-uuid", mock.Anything).Return(nil, errors.New("failed to get pool"))

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsAutoTieringDisabled", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "disabled-pool-uuid",
				AccountID: 123,
				Name:      "disabled-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"disabled-pool-uuid": {
				PoolConsumptionHotTier:  500000000000,
				PoolConsumptionColdTier: 600000000000,
			},
		}

		disabledPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "disabled-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: false, // Auto-tiering disabled
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "disabled-pool-uuid", mock.Anything).Return(&disabledPool.Pool, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsPoolNotReady", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "not-ready-pool-uuid",
				AccountID: 123,
				Name:      "not-ready-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"not-ready-pool-uuid": {
				PoolConsumptionHotTier:  500000000000,
				PoolConsumptionColdTier: 600000000000,
			},
		}

		notReadyPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "not-ready-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateCreating, // Not ready
				SizeInBytes:      1000000000000,                    // 1TB
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes: 500000000000, // 500GB
					TieringStatus:      datamodel.TieringStatusResumed,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "not-ready-pool-uuid", mock.Anything).Return(&notReadyPool.Pool, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		// Pool should still be segregated to pause even if not ready, since logic doesn't check state for pause/resume
		assert.Len(tt, result[PoolsToPauseKey], 1)
		assert.Equal(tt, "not-ready-pool-uuid", result[PoolsToPauseKey][0].UUID)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsNoConsumptionData", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "no-consumption-pool-uuid",
				AccountID: 123,
				Name:      "no-consumption-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{} // No consumption data

		noConsumptionPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "no-consumption-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "no-consumption-pool-uuid", mock.Anything).Return(&noConsumptionPool.Pool, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsWithBypassModeDisabled", func(tt *testing.T) {
		// Test when feature flag is disabled (default) and volumes don't have bypass mode enabled
		// In this case, checkPoolVolumesWithBypassModeEnabled is called and returns false
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "bypass-disabled-pool-uuid",
				AccountID: 123,
				Name:      "bypass-disabled-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"bypass-disabled-pool-uuid": {
				PoolConsumptionHotTier:              500000000000, // 100% of 500GB hot tier
				PoolConsumptionColdTier:             50000000000,  // 50GB
				PoolConsumptionHotTierForAutoResize: 500000000000,
			},
		}

		bypassDisabledPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "bypass-disabled-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000, // 1TB
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      500000000000, // 500GB
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "bypass-disabled-pool-uuid", mock.Anything).Return(&bypassDisabledPool.Pool, nil)
		// When feature flag is disabled (default), checkPoolVolumesWithBypassModeEnabled is called.
		// Return volumes without bypass mode enabled.
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{
			{
				AutoTieringEnabled: true,
				AutoTieringPolicy:  &datamodel.AutoTieringPolicy{HotTierBypassModeEnabled: false},
			},
		}, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		// Should auto-resize since no bypass volumes found
		assert.Len(tt, result[PoolsToAutoResizeKey], 1)
		assert.Equal(tt, "bypass-disabled-pool-uuid", result[PoolsToAutoResizeKey][0].UUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsAutoResizeNotMeetingThreshold", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "low-usage-pool-uuid",
				AccountID: 123,
				Name:      "low-usage-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"low-usage-pool-uuid": {
				PoolConsumptionHotTier:              300000000000, // 300GB - 60% of 500GB hot tier (below 80% threshold)
				PoolConsumptionColdTier:             50000000000,  // 50GB
				PoolConsumptionHotTierForAutoResize: 300000000000,
			},
		}

		lowUsagePool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "low-usage-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000, // 1TB
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      500000000000, // 500GB
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "low-usage-pool-uuid", mock.Anything).Return(&lowUsagePool.Pool, nil)
		// When feature flag is disabled (default), checkPoolVolumesWithBypassModeEnabled is called for pools eligible for auto-resize
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{}, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0) // Should not auto-resize due to low usage
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsSkipsPoolWithBypassVolumes_FlagDisabled", func(tt *testing.T) {
		// Test when feature flag is disabled (default) and pool has volumes with bypass mode enabled
		// In this case, the pool should be skipped from auto-resize (old behavior)
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Save original flag value and ensure it's disabled
		originalFlag := AllowAutogrowForHTBypassVolumeContainingPool
		AllowAutogrowForHTBypassVolumeContainingPool = false
		defer func() { AllowAutogrowForHTBypassVolumeContainingPool = originalFlag }()

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "bypass-pool-uuid",
				AccountID: 123,
				Name:      "bypass-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"bypass-pool-uuid": {
				PoolConsumptionHotTier:              500000000000, // 100% of 500GB hot tier
				PoolConsumptionColdTier:             50000000000,
				PoolConsumptionHotTierForAutoResize: 100000000000, // Lower since bypass volumes excluded
			},
		}

		bypassPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "bypass-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000,
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      500000000000,
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "bypass-pool-uuid", mock.Anything).Return(&bypassPool.Pool, nil)
		// When flag is disabled, checkPoolVolumesWithBypassModeEnabled is called.
		// Return a volume with bypass mode enabled - pool should be skipped
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{
			{
				AutoTieringEnabled: true,
				AutoTieringPolicy:  &datamodel.AutoTieringPolicy{HotTierBypassModeEnabled: true},
			},
		}, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		// Should NOT auto-resize since bypass volumes present and flag is disabled
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsIncludesPoolWithBypassVolumes_FlagEnabled", func(tt *testing.T) {
		// Test when feature flag is enabled and pool has volumes with bypass mode enabled
		// In this case, the pool should still be considered for auto-resize using adjusted hot tier consumption
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Enable the feature flag
		originalFlag := AllowAutogrowForHTBypassVolumeContainingPool
		AllowAutogrowForHTBypassVolumeContainingPool = true
		defer func() { AllowAutogrowForHTBypassVolumeContainingPool = originalFlag }()

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "bypass-pool-uuid",
				AccountID: 123,
				Name:      "bypass-pool",
			},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"bypass-pool-uuid": {
				PoolConsumptionHotTier:              500000000000, // 100% of 500GB hot tier
				PoolConsumptionColdTier:             50000000000,
				PoolConsumptionHotTierForAutoResize: 500000000000, // Exceeds threshold so should auto-resize
			},
		}

		bypassPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "bypass-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000,
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      500000000000,
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "bypass-pool-uuid", mock.Anything).Return(&bypassPool.Pool, nil)
		// When flag is enabled, checkPoolVolumesWithBypassModeEnabled is NOT called.
		// No need to mock GetVolumesByPoolID for bypass check

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		// Should auto-resize using adjusted hot tier consumption (bypass volumes excluded in FetchAndSavePoolsTieringInfo)
		assert.Len(tt, result[PoolsToAutoResizeKey], 1)
		assert.Equal(tt, "bypass-pool-uuid", result[PoolsToAutoResizeKey][0].UUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsAutoResize_FlagEnabled_CalculatesNewHotTierSize", func(tt *testing.T) {
		// Test case: Verifies newHotTierSizeInBytes calculation
		// when flag is enabled and pool meets auto-resize conditions with lower threshold
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Enable the feature flag to use the adjusted consumption path
		originalFlag := AllowAutogrowForHTBypassVolumeContainingPool
		AllowAutogrowForHTBypassVolumeContainingPool = true
		defer func() { AllowAutogrowForHTBypassVolumeContainingPool = originalFlag }()

		// Save and restore threshold - set to a value that allows trigger at 90%
		originalThreshold := AutoTierHotTierAutoResizeThresholdPercent
		AutoTierHotTierAutoResizeThresholdPercent = 80
		defer func() { AutoTierHotTierAutoResizeThresholdPercent = originalThreshold }()

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{UUID: "autoresize-calc-pool-uuid", AccountID: 123, Name: "autoresize-calc-pool"},
		}

		// Hot tier size: 100GB, consumption: 90GB (90% >= 80% threshold)
		// New hot tier size after 10% increase: ~110GB rounded to GiB
		// Total after resize: ~110GB + 50GB = ~160GB < 500GB pool size
		poolConsumptionsMap := map[string]map[string]float64{
			"autoresize-calc-pool-uuid": {
				PoolConsumptionHotTier:              90000000000,
				PoolConsumptionColdTier:             50000000000,
				PoolConsumptionHotTierForAutoResize: 90000000000, // 90% of 100GB hot tier
			},
		}

		autoResizePool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "autoresize-calc-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      500000000000, // 500GB pool
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      100000000000, // 100GB hot tier
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "autoresize-calc-pool-uuid", mock.Anything).Return(&autoResizePool.Pool, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		// Pool should be added to auto-resize list, confirming newHotTierSizeInBytes was calculated
		assert.Len(tt, result[PoolsToAutoResizeKey], 1)
		assert.Equal(tt, "autoresize-calc-pool-uuid", result[PoolsToAutoResizeKey][0].UUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsAutoResize_NewHotTierExceedsPoolSize", func(tt *testing.T) {
		// Test case: Verifies line 184 condition is false when newHotTierSizeInBytes + coldTier >= poolSize
		// This confirms newHotTierSizeInBytes was calculated but pool was NOT added due to size constraint
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		originalFlag := AllowAutogrowForHTBypassVolumeContainingPool
		AllowAutogrowForHTBypassVolumeContainingPool = true
		defer func() { AllowAutogrowForHTBypassVolumeContainingPool = originalFlag }()

		originalThreshold := AutoTierHotTierAutoResizeThresholdPercent
		AutoTierHotTierAutoResizeThresholdPercent = 80
		defer func() { AutoTierHotTierAutoResizeThresholdPercent = originalThreshold }()

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{UUID: "exceeds-size-pool-uuid", AccountID: 123, Name: "exceeds-size-pool"},
		}

		// Hot tier: 400GB at 100% usage, cold tier: 565GB
		// New hot tier after 10% increase: 400GB * 1.1 / 1GiB = 409 GiB = 439162699776 bytes (~409 GiB)
		// 439162699776 + 565000000000 = 1004162699776 > 1000000000000 pool size
		// So auto-resize should NOT happen
		poolConsumptionsMap := map[string]map[string]float64{
			"exceeds-size-pool-uuid": {
				PoolConsumptionHotTier:              400000000000, // 400GB
				PoolConsumptionColdTier:             565000000000, // 565GB - ensures total exceeds pool size
				PoolConsumptionHotTierForAutoResize: 400000000000, // 100% usage
			},
		}

		exceedsSizePool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "exceeds-size-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000, // 1TB pool
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      400000000000, // 400GB hot tier
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "exceeds-size-pool-uuid", mock.Anything).Return(&exceedsSizePool.Pool, nil)

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		// Pool is NOT paused because HotTierSizeInBytes + ColdTier (400GB + 565GB = 965GB) < 1TB
		// But it's also NOT in resume since TieringStatus is already Resumed
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		// Should NOT auto-resize because newHotTierSizeInBytes + coldTier >= poolSize
		// (409 GiB + 565GB = ~1004GB > 1TB)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SegregatePoolsCheckBypassModeError", func(tt *testing.T) {
		// Test case: error when checkPoolVolumesWithBypassModeEnabled fails
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Ensure feature flag is disabled so checkPoolVolumesWithBypassModeEnabled is called
		originalFlag := AllowAutogrowForHTBypassVolumeContainingPool
		AllowAutogrowForHTBypassVolumeContainingPool = false
		defer func() { AllowAutogrowForHTBypassVolumeContainingPool = originalFlag }()

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.SegregatePools)

		pools := []*database.PoolIdentifier{
			{UUID: "bypass-error-pool-uuid", AccountID: 123, Name: "bypass-error-pool"},
		}

		poolConsumptionsMap := map[string]map[string]float64{
			"bypass-error-pool-uuid": {
				PoolConsumptionHotTier:  450000000000, // 90% of 500GB hot tier
				PoolConsumptionColdTier: 50000000000,  // 50GB
			},
		}

		bypassErrorPool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "bypass-error-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				SizeInBytes:      1000000000000, // 1TB
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					HotTierSizeInBytes:      500000000000, // 500GB
					TieringStatus:           datamodel.TieringStatusResumed,
					EnableHotTierAutoResize: true,
				},
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "bypass-error-pool-uuid", mock.Anything).Return(&bypassErrorPool.Pool, nil)
		// Return error when checking bypass mode
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(nil, errors.New("failed to get volumes"))

		encodedValue, err := env.ExecuteActivity(activity.SegregatePools, pools, poolConsumptionsMap)
		assert.NoError(tt, err)
		var result map[string][]*database.PoolIdentifier
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		// Pool should not be in any list due to error
		assert.Len(tt, result[PoolsToPauseKey], 0)
		assert.Len(tt, result[PoolsToResumeKey], 0)
		assert.Len(tt, result[PoolsToAutoResizeKey], 0)
		mockStorage.AssertExpectations(tt)
	})
}

func TestAutoTierSyncActivity_FetchAndSavePoolsTieringInfo(t *testing.T) {
	t.Run("FetchAndSavePoolsTieringInfoSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)), // 100GB
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),  // 50GB
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)), // 150GB logical
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		assert.Equal(tt, float64(100000000000), result["test-pool-uuid"][PoolConsumptionColdTier])
		assert.Equal(tt, float64(50000000000), result["test-pool-uuid"][PoolConsumptionHotTier])
		// Verify PoolConsumptionHotTierForAutoResize is set (covers line 377)
		assert.Equal(tt, float64(50000000000), result["test-pool-uuid"][PoolConsumptionHotTierForAutoResize])
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_VerifiesAllConsumptionFields_WithBypassFlag", func(tt *testing.T) {
		// Test case: shouldExcludeFromHTConsumption function when flag is enabled
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Enable the feature flag to trigger the shouldExcludeFromHTConsumption function
		originalFlag := AllowAutogrowForHTBypassVolumeContainingPool
		AllowAutogrowForHTBypassVolumeContainingPool = true
		defer func() { AllowAutogrowForHTBypassVolumeContainingPool = originalFlag }()

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{UUID: "consumption-pool-uuid", AccountID: 456, Name: "consumption-pool"},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 10, UUID: "consumption-pool-uuid"},
				AccountID:        456,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		// Include volumes with bypass mode to exercise shouldExcludeFromHTConsumption
		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "vol-1-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "ext-vol-1-uuid",
				},
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					HotTierBypassModeEnabled: true, // This volume has bypass mode enabled
				},
			},
			{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "vol-2-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "ext-vol-2-uuid",
				},
				AutoTieringEnabled: false, // This volume does not have bypass mode
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("ext-vol-1-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(200000000000)), // 200GB cold
						PerformanceTierFootprint: nillable.ToPointer(int64(100000000000)), // 100GB hot
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(300000000000)),
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("ext-vol-2-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(150000000000)), // 150GB cold
						PerformanceTierFootprint: nillable.ToPointer(int64(75000000000)),  // 75GB hot
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(225000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "consumption-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(10)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, p *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)

		// Verify the pool is in the result map
		assert.Contains(tt, result, "consumption-pool-uuid")
		poolResult := result["consumption-pool-uuid"]

		// Verify all three consumption fields are present and correct
		// PoolConsumptionHotTier
		assert.Contains(tt, poolResult, PoolConsumptionHotTier)
		assert.Equal(tt, float64(175000000000), poolResult[PoolConsumptionHotTier]) // 100GB + 75GB

		// PoolConsumptionColdTier
		assert.Contains(tt, poolResult, PoolConsumptionColdTier)
		assert.Equal(tt, float64(350000000000), poolResult[PoolConsumptionColdTier]) // 200GB + 150GB

		// PoolConsumptionHotTierForAutoResize (with bypass exclusion)
		// ext-vol-1-uuid has bypass mode enabled, so its 100GB is excluded; only ext-vol-2-uuid's 75GB is counted
		assert.Contains(tt, poolResult, PoolConsumptionHotTierForAutoResize)
		assert.Equal(tt, float64(75000000000), poolResult[PoolConsumptionHotTierForAutoResize]) // Only ext-vol-2 (75GB), ext-vol-1 excluded

		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_ONTAPMode_ExpertModeVolumes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{UUID: "test-pool-uuid", AccountID: 123, Name: "test-pool"},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				APIAccessMode:    common.ONTAPMode,
			},
		}

		expertVolumes := []*datamodel.ExpertModeVolumes{
			{BaseModel: datamodel.BaseModel{UUID: "db-expert-1"}, ExternalUUID: "ext-uuid-1"},
			{BaseModel: datamodel.BaseModel{UUID: "db-expert-2"}, ExternalUUID: ""}, // skipped in map
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("ext-uuid-1"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("ListExpertModeVolumesByPoolID", mock.Anything, int64(1)).Return(expertVolumes, nil)
		mockProvider.On("GetVolumes").Return(ontapVolumes, nil)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, p *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_GetPoolFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(nil, errors.New("failed to get pool"))

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_PoolNotAutoTiering", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: false, // Not auto-tiering enabled
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotContains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_PoolNotReady", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateCreating, // Not ready
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)

		// Mock GetOntapRestProviderForPoolFastConn to return error for non-ready pool
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("pool not ready")
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotContains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_GetOntapProviderFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)

		// Mock GetOntapRestProviderForPoolFastConn to return error
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider")
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotContains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_GetVolumesFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockProvider.On("GetVolumes").Return(nil, errors.New("failed to get volumes"))

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotContains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_VolumeWithNilVolumeAttributes_SkipsInMap", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{UUID: "test-pool-uuid", AccountID: 123, Name: "test-pool"},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "db-vol-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ext-uuid-1"},
			},
			{
				BaseModel:        datamodel.BaseModel{ID: 2, UUID: "db-vol-2"},
				VolumeAttributes: nil,
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("ext-uuid-1"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(ontapVolumes, nil)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, p *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_GetVolumesByPoolIDFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(nil, errors.New("failed to get volumes"))
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.NotContains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_CalculateConsumptionFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid-1",
				},
			},
			{
				BaseModel: datamodel.BaseModel{ID: 2, UUID: "db-vol-uuid-2"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid-2",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid-1"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil) // Returns 2 volumes but ONTAP has 1
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		// With the updated logic, calculateAndUpdateHotColdTierConsumption no longer returns
		// an error for volume count mismatch, so the pool will be included in the result
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	// Tests for TieringFullnessThreshold migration logic
	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_ThresholdIs50AndNotPaused", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		// Create VLMConfig with DataAggr for threshold migration test
		vlmConfig := &vlm.VLMConfig{
			DataAggr: []vlm.DataAggrConfig{
				{
					Name:     "aggr1",
					Aggruuid: "aggr-uuid",
					Size:     1000,
					HomeNode: "node1",
				},
			},
		}
		vlmConfigJSON, _ := json.Marshal(vlmConfig)

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				Name:             "test-pool",
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				VLMConfig:        string(vlmConfigJSON),
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 50,
					TieringStatus:            datamodel.TieringStatusResumed,
				},
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations - should be called to update threshold to 0
		mockAggregate := &vsa.Aggregate{
			UUID: "aggr-uuid",
			Name: "aggr1",
		}
		mockProvider.On("GetAggregateByName", "aggr1").Return(mockAggregate, nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			// Verify the threshold is being set to 0
			return params.TieringFullnessThreshold == 0 && params.UUID == "aggr-uuid"
		})).Return(nil)

		// Mock UpdatePoolTieringConfig - should be called with threshold 0
		thresholdZero := int64(0)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "test-pool-uuid", (*int64)(nil), (*int64)(nil), &thresholdZero, (*datamodel.TieringStatus)(nil)).Return(nil)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_ThresholdIs50ButPaused", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 50,
					TieringStatus:            datamodel.TieringStatusPaused, // Paused - should not update
				},
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations - should NOT be called because pool is paused
		// (Not adding mock expectations means test will fail if they're called)

		// UpdatePoolTieringConfig should NOT be called
		// (Not adding mock expectation means test will fail if it's called)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_ThresholdIsNot50", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 75, // Not 50 - should not update
					TieringStatus:            datamodel.TieringStatusResumed,
				},
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations - should NOT be called because threshold is not 50
		// (Not adding mock expectations means test will fail if they're called)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_AutoTieringConfigIsNil", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:         datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:         123,
				AllowAutoTiering:  true,
				State:             datamodel.LifeCycleStateREADY,
				AutoTieringConfig: nil, // Nil - should not crash
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations - should NOT be called because AutoTieringConfig is nil
		// (Not adding mock expectations means test will fail if they're called)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_AggregateUpdateFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		// Create VLMConfig with DataAggr for threshold migration test
		vlmConfig := &vlm.VLMConfig{
			DataAggr: []vlm.DataAggrConfig{
				{
					Name:     "aggr1",
					Aggruuid: "aggr-uuid",
					Size:     1000,
					HomeNode: "node1",
				},
			},
		}
		vlmConfigJSON, _ := json.Marshal(vlmConfig)

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				Name:             "test-pool",
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				VLMConfig:        string(vlmConfigJSON),
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 50,
					TieringStatus:            datamodel.TieringStatusResumed,
				},
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations - should fail
		mockAggregate := &vsa.Aggregate{
			UUID: "aggr-uuid",
			Name: "aggr1",
		}
		mockProvider.On("GetAggregateByName", "aggr1").Return(mockAggregate, nil)
		mockProvider.On("UpdateAggregate", mock.Anything).Return(errors.New("failed to update aggregate"))

		// UpdatePoolTieringConfig should NOT be called because aggregate update failed
		// (Not adding mock expectation means test will fail if it's called)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err) // Should not error, just log warning
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_DBUpdateFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		// Create VLMConfig with DataAggr for threshold migration test
		vlmConfig := &vlm.VLMConfig{
			DataAggr: []vlm.DataAggrConfig{
				{
					Name:     "aggr1",
					Aggruuid: "aggr-uuid",
					Size:     1000,
					HomeNode: "node1",
				},
			},
		}
		vlmConfigJSON, _ := json.Marshal(vlmConfig)

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				Name:             "test-pool",
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				VLMConfig:        string(vlmConfigJSON),
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 50,
					TieringStatus:            datamodel.TieringStatusResumed,
				},
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations - should succeed
		mockAggregate := &vsa.Aggregate{
			UUID: "aggr-uuid",
			Name: "aggr1",
		}
		mockProvider.On("GetAggregateByName", "aggr1").Return(mockAggregate, nil)
		mockProvider.On("UpdateAggregate", mock.Anything).Return(nil)

		// Mock UpdatePoolTieringConfig - should fail
		thresholdZero := int64(0)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "test-pool-uuid", (*int64)(nil), (*int64)(nil), &thresholdZero, (*datamodel.TieringStatus)(nil)).Return(errors.New("database error"))

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err) // Should not error, just log warning
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_MultipleAggregates", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		// Create VLMConfig with multiple DataAggr for threshold migration test
		vlmConfig := &vlm.VLMConfig{
			DataAggr: []vlm.DataAggrConfig{
				{
					Name:     "aggr1",
					Aggruuid: "aggr-uuid-1",
					Size:     1000,
					HomeNode: "node1",
				},
				{
					Name:     "aggr2",
					Aggruuid: "aggr-uuid-2",
					Size:     2000,
					HomeNode: "node2",
				},
				{
					Name:     "aggr3",
					Aggruuid: "aggr-uuid-3",
					Size:     3000,
					HomeNode: "node3",
				},
			},
		}
		vlmConfigJSON, _ := json.Marshal(vlmConfig)

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				Name:             "test-pool",
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				VLMConfig:        string(vlmConfigJSON),
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 50,
					TieringStatus:            datamodel.TieringStatusResumed,
				},
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations for multiple aggregates - all should be updated
		mockAggregate1 := &vsa.Aggregate{
			UUID: "aggr-uuid-1",
			Name: "aggr1",
		}
		mockAggregate2 := &vsa.Aggregate{
			UUID: "aggr-uuid-2",
			Name: "aggr2",
		}
		mockAggregate3 := &vsa.Aggregate{
			UUID: "aggr-uuid-3",
			Name: "aggr3",
		}

		// Mock GetAggregateByName for each aggregate
		mockProvider.On("GetAggregateByName", "aggr1").Return(mockAggregate1, nil)
		mockProvider.On("GetAggregateByName", "aggr2").Return(mockAggregate2, nil)
		mockProvider.On("GetAggregateByName", "aggr3").Return(mockAggregate3, nil)

		// Mock UpdateAggregate for each aggregate - all should be called with threshold 0
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.TieringFullnessThreshold == 0 && params.UUID == "aggr-uuid-1"
		})).Return(nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.TieringFullnessThreshold == 0 && params.UUID == "aggr-uuid-2"
		})).Return(nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.TieringFullnessThreshold == 0 && params.UUID == "aggr-uuid-3"
		})).Return(nil)

		// Mock UpdatePoolTieringConfig - should be called with threshold 0 after all aggregates are updated
		thresholdZero := int64(0)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "test-pool-uuid", (*int64)(nil), (*int64)(nil), &thresholdZero, (*datamodel.TieringStatus)(nil)).Return(nil)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err)
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_MultipleAggregates_OneFails", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		// Create VLMConfig with multiple DataAggr for threshold migration test
		vlmConfig := &vlm.VLMConfig{
			DataAggr: []vlm.DataAggrConfig{
				{
					Name:     "aggr1",
					Aggruuid: "aggr-uuid-1",
					Size:     1000,
					HomeNode: "node1",
				},
				{
					Name:     "aggr2",
					Aggruuid: "aggr-uuid-2",
					Size:     2000,
					HomeNode: "node2",
				},
				{
					Name:     "aggr3",
					Aggruuid: "aggr-uuid-3",
					Size:     3000,
					HomeNode: "node3",
				},
			},
		}
		vlmConfigJSON, _ := json.Marshal(vlmConfig)

		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				Name:             "test-pool",
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				VLMConfig:        string(vlmConfigJSON),
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 50,
					TieringStatus:            datamodel.TieringStatusResumed,
				},
			},
		}

		dbVolumes := []*datamodel.Volume{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "db-vol-uuid"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-uuid",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-vol-uuid"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(dbVolumes, nil)
		mockStorage.On("BatchUpdateVolumeTieringFields", mock.Anything, mock.Anything).Return(nil)
		mockProvider.On("GetVolumes").Return(volumes, nil)

		// Mock aggregate operations for multiple aggregates
		mockAggregate1 := &vsa.Aggregate{
			UUID: "aggr-uuid-1",
			Name: "aggr1",
		}
		mockAggregate2 := &vsa.Aggregate{
			UUID: "aggr-uuid-2",
			Name: "aggr2",
		}
		mockAggregate3 := &vsa.Aggregate{
			UUID: "aggr-uuid-3",
			Name: "aggr3",
		}

		// Mock GetAggregateByName for each aggregate
		mockProvider.On("GetAggregateByName", "aggr1").Return(mockAggregate1, nil)
		mockProvider.On("GetAggregateByName", "aggr2").Return(mockAggregate2, nil)
		mockProvider.On("GetAggregateByName", "aggr3").Return(mockAggregate3, nil)

		// Mock UpdateAggregate - aggr1 and aggr3 succeed, aggr2 fails
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.TieringFullnessThreshold == 0 && params.UUID == "aggr-uuid-1"
		})).Return(nil)
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.TieringFullnessThreshold == 0 && params.UUID == "aggr-uuid-2"
		})).Return(errors.New("failed to update aggregate aggr2"))
		mockProvider.On("UpdateAggregate", mock.MatchedBy(func(params vsa.UpdateAggregateParams) bool {
			return params.TieringFullnessThreshold == 0 && params.UUID == "aggr-uuid-3"
		})).Return(nil)

		// UpdatePoolTieringConfig should NOT be called because one aggregate update failed
		// (The code only updates DB if allAggregatesUpdated is true - see line 268 in sync_auto_tiering_activities.go)
		// (Not adding mock expectation means test will fail if it's called)

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err) // Should not error, just log warnings for failed aggregates
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		assert.Contains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("FetchAndSavePoolsTieringInfo_TieringThresholdMigration_InvalidVLMConfig", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FetchAndSavePoolsTieringInfo)

		pools := []*database.PoolIdentifier{
			{
				UUID:      "test-pool-uuid",
				AccountID: 123,
				Name:      "test-pool",
			},
		}

		// Create pool with invalid VLMConfig JSON to trigger parseVlmConfig error
		pool := &datamodel.PoolView{
			Pool: datamodel.Pool{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
				Name:             "test-pool",
				AllowAutoTiering: true,
				State:            datamodel.LifeCycleStateREADY,
				VLMConfig:        "invalid json {", // Invalid JSON that will cause parseVlmConfig to fail
				AutoTieringConfig: &datamodel.AutoTieringConfig{
					TieringFullnessThreshold: 50,
					TieringStatus:            datamodel.TieringStatusResumed,
				},
			},
		}

		mockProvider := new(vsa.MockProvider)
		mockStorage.On("GetPoolByUUIDForAccount", mock.Anything, "test-pool-uuid", mock.Anything).Return(&pool.Pool, nil)

		// The following should NOT be called because parseVlmConfig failure causes early return
		// (Not adding mock expectations means test will fail if they're called):
		// - GetVolumesByPoolID
		// - BatchUpdateVolumeTieringFields
		// - GetVolumes
		// - GetAggregateByName
		// - UpdateAggregate
		// - UpdatePoolTieringConfig

		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		encodedValue, err := env.ExecuteActivity(activity.FetchAndSavePoolsTieringInfo, pools)
		assert.NoError(tt, err) // Should not error, just log error and skip pool
		var result map[string]map[string]float64
		err = encodedValue.Get(&result)
		assert.NoError(tt, err)
		// Pool should NOT be in result because parseVlmConfig failure causes early return
		// from the goroutine, skipping volume processing
		assert.NotContains(tt, result, "test-pool-uuid")
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})
}

// callCalculateAndUpdateHotColdTierConsumptionWithStorage is a test helper that adapts calculateAndUpdateHotColdTierConsumption to mockStorage and dbVolumeMap.
func callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx context.Context, volumes []*vsa.Volume, expectedVolCount int64, mockStorage *database.MockStorage, dbVolumeMap map[string]*datamodel.Volume) (int64, int64, error) {
	getDBUUID := func(extUUID string) (string, bool) {
		v, ok := dbVolumeMap[extUUID]
		if !ok || v == nil {
			return "", false
		}
		return v.UUID, true
	}
	hotTier, coldTier, _, err := calculateAndUpdateHotColdTierConsumption(ctx, volumes, expectedVolCount, getDBUUID, nil, true, mockStorage)
	return hotTier, coldTier, err
}

func TestCalculateAndUpdateHotColdTierConsumption(t *testing.T) {
	ctx := context.TODO()

	t.Run("calculateAndUpdateHotColdTierConsumptionSuccess", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)

		dbVolumeMap := map[string]*datamodel.Volume{
			"external-uuid-1": {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid-1",
				},
			},
			"external-uuid-2": {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-2"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid-2",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-uuid-1"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)), // 100GB
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),  // 50GB
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)), // 150GB logical
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-uuid-2"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(200000000000)), // 200GB
						PerformanceTierFootprint: nillable.ToPointer(int64(100000000000)), // 100GB
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(300000000000)), // 300GB logical
						},
					},
				},
			},
		}
		expectedVolCount := int64(2)

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, volumes, expectedVolCount, mockStorage, dbVolumeMap)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(300000000000), coldTier) // 300GB total
		assert.Equal(tt, int64(150000000000), hotTier)  // 150GB total
		mockStorage.AssertExpectations(tt)
	})

	t.Run("calculateAndUpdateHotColdTierConsumption_SkipSvmRoot", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"external-uuid-1": {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid-1",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-uuid-root"),
					IsSvmRoot: nillable.ToPointer(true), // Should be skipped
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-uuid-1"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(200000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(100000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(300000000000)),
						},
					},
				},
			},
		}
		expectedVolCount := int64(1) // Only counting non-SVM root volumes

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, volumes, expectedVolCount, mockStorage, dbVolumeMap)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(200000000000), coldTier)
		assert.Equal(tt, int64(100000000000), hotTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("calculateAndUpdateHotColdTierConsumption_SkipVolumeWithoutSpace", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"external-uuid-1": {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid-1",
				},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-uuid-no-space"),
					IsSvmRoot: nillable.ToPointer(false),
					Space:     nil, // Should be skipped
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-uuid-1"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(200000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(100000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(300000000000)),
						},
					},
				},
			},
		}
		expectedVolCount := int64(1) // Only counting volumes with space data

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, volumes, expectedVolCount, mockStorage, dbVolumeMap)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(200000000000), coldTier)
		assert.Equal(tt, int64(100000000000), hotTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("calculateAndUpdateHotColdTierConsumption_VolumeCountMismatch", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			"external-uuid-1": {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-uuid-1"},
			},
		}

		volumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      nillable.ToPointer("external-uuid-1"),
					IsSvmRoot: nillable.ToPointer(false),
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nillable.ToPointer(int64(100000000000)),
						PerformanceTierFootprint: nillable.ToPointer(int64(50000000000)),
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: nillable.ToPointer(int64(150000000000)),
						},
					},
				},
			},
		}
		expectedVolCount := int64(2) // Mismatch: expecting 2 but got 1

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, volumes, expectedVolCount, mockStorage, dbVolumeMap)
		// The function no longer returns an error for volume count mismatch
		assert.NoError(tt, err)
		// The function still computes based on what it got
		assert.NotEqual(tt, int64(0), hotTier)
		assert.NotEqual(tt, int64(0), coldTier)
	})

	t.Run("calculateAndUpdateHotColdTierConsumption_EmptyVolumes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{}
		volumes := []*vsa.Volume{}
		expectedVolCount := int64(0)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, volumes, expectedVolCount, mockStorage, dbVolumeMap)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), hotTier)
		assert.Equal(tt, int64(0), coldTier)
	})
}

func TestAutoTierSyncActivity_ToggleHotTierBypassModeForPoolVolumes(t *testing.T) {
	t.Run("ToggleHotTierBypassModeForPoolVolumes_Success_PauseMode", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.ToggleHotTierBypassModeForPoolVolumes)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusPaused, // Paused - should set to "none"
			},
		}

		volumes := []*datamodel.Volume{
			{
				BaseModel:          datamodel.BaseModel{ID: 1, UUID: "vol-1-uuid"},
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					HotTierBypassModeEnabled: true,
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-1-uuid",
				},
			},
			{
				BaseModel:          datamodel.BaseModel{ID: 2, UUID: "vol-2-uuid"},
				AutoTieringEnabled: false, // Should be skipped
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-2-uuid",
				},
			},
		}

		mockProvider := vsa.NewMockProvider(tt)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(volumes, nil)
		mockProvider.On("UpdateVolume", mock.MatchedBy(func(params vsa.UpdateVolumeParams) bool {
			return params.UUID == "external-vol-1-uuid" &&
				params.TieringPolicy.CoolAccessTieringPolicy == ontaprestmodel.VolumeInlineTieringPolicyNone
		})).Return(nil)

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		_, err := env.ExecuteActivity(activity.ToggleHotTierBypassModeForPoolVolumes, pool)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("ToggleHotTierBypassModeForPoolVolumes_Success_ResumeMode", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.ToggleHotTierBypassModeForPoolVolumes)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusResumed, // Not paused - should set to "all"
			},
		}

		volumes := []*datamodel.Volume{
			{
				BaseModel:          datamodel.BaseModel{ID: 1, UUID: "vol-1-uuid"},
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					HotTierBypassModeEnabled: true,
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-1-uuid",
				},
			},
		}

		mockProvider := vsa.NewMockProvider(tt)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(volumes, nil)
		mockProvider.On("UpdateVolume", mock.MatchedBy(func(params vsa.UpdateVolumeParams) bool {
			return params.UUID == "external-vol-1-uuid" &&
				params.TieringPolicy.CoolAccessTieringPolicy == ontaprestmodel.VolumeInlineTieringPolicyAll
		})).Return(nil)

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		_, err := env.ExecuteActivity(activity.ToggleHotTierBypassModeForPoolVolumes, pool)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("ToggleHotTierBypassModeForPoolVolumes_NoVolumesWithBypassMode", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.ToggleHotTierBypassModeForPoolVolumes)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusResumed,
			},
		}

		volumes := []*datamodel.Volume{
			{
				BaseModel:          datamodel.BaseModel{ID: 1, UUID: "vol-1-uuid"},
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					HotTierBypassModeEnabled: false, // No bypass mode
				},
			},
		}

		mockProvider := vsa.NewMockProvider(tt)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(volumes, nil)

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		_, err := env.ExecuteActivity(activity.ToggleHotTierBypassModeForPoolVolumes, pool)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
		// Provider should not be called since no volumes need updating
	})

	t.Run("ToggleHotTierBypassModeForPoolVolumes_GetProviderFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.ToggleHotTierBypassModeForPoolVolumes)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusResumed,
			},
		}

		// Mock GetOntapRestProviderForPoolFastConn to return error
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, errors.New("failed to get provider")
		}

		_, err := env.ExecuteActivity(activity.ToggleHotTierBypassModeForPoolVolumes, pool)
		assert.Error(tt, err)
	})

	t.Run("ToggleHotTierBypassModeForPoolVolumes_GetVolumesFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.ToggleHotTierBypassModeForPoolVolumes)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusResumed,
			},
		}

		mockProvider := vsa.NewMockProvider(tt)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(nil, errors.New("failed to get volumes"))

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		_, err := env.ExecuteActivity(activity.ToggleHotTierBypassModeForPoolVolumes, pool)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ToggleHotTierBypassModeForPoolVolumes_UpdateVolumeFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.ToggleHotTierBypassModeForPoolVolumes)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
				AccountID:        123,
			AutoTieringConfig: &datamodel.AutoTieringConfig{
				TieringStatus: datamodel.TieringStatusResumed,
			},
		}

		volumes := []*datamodel.Volume{
			{
				BaseModel:          datamodel.BaseModel{ID: 1, UUID: "vol-1-uuid"},
				AutoTieringEnabled: true,
				AutoTieringPolicy: &datamodel.AutoTieringPolicy{
					HotTierBypassModeEnabled: true,
				},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: "external-vol-1-uuid",
				},
			},
		}

		mockProvider := vsa.NewMockProvider(tt)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return(volumes, nil)
		mockProvider.On("UpdateVolume", mock.Anything).Return(errors.New("failed to update volume"))

		// Mock GetOntapRestProviderForPoolFastConn
		originalGetOntapRestProviderForPoolFastConn := GetOntapRestProviderForPoolFastConn
		defer func() { GetOntapRestProviderForPoolFastConn = originalGetOntapRestProviderForPoolFastConn }()
		GetOntapRestProviderForPoolFastConn = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		_, err := env.ExecuteActivity(activity.ToggleHotTierBypassModeForPoolVolumes, pool)
		assert.Error(tt, err)
		mockStorage.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("ToggleHotTierBypassMode_UsesRegularConnectionWhenFlagDisabled", func(tt *testing.T) {
		originalFlag := autoTieringFastOntapConnection
		defer func() { autoTieringFastOntapConnection = originalFlag }()
		autoTieringFastOntapConnection = false

		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.ToggleHotTierBypassModeForPoolVolumes)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		}

		mockProvider := vsa.NewMockProvider(tt)
		mockStorage.On("GetVolumesByPoolIDNoPreload", mock.Anything, int64(1)).Return([]*datamodel.Volume{}, nil)

		originalGetOntapRestProviderForPool := GetOntapRestProviderForPool
		defer func() { GetOntapRestProviderForPool = originalGetOntapRestProviderForPool }()
		GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		_, err := env.ExecuteActivity(activity.ToggleHotTierBypassModeForPoolVolumes, pool)
		assert.NoError(tt, err)
	})
}

func TestAutoTierSyncActivity_UpdatePoolTieringConsumptionInDB(t *testing.T) {
	t.Run("UpdatePoolTieringConsumptionInDB_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolTieringConsumptionInDB)

		poolsConsumptionsMap := map[string]map[string]float64{
			"pool-1-uuid": {
				PoolConsumptionHotTier:  500000000000, // 500GB
				PoolConsumptionColdTier: 600000000000, // 600GB
			},
			"pool-2-uuid": {
				PoolConsumptionHotTier:  300000000000, // 300GB
				PoolConsumptionColdTier: 400000000000, // 400GB
			},
		}

		hot1 := int64(500000000000)
		cold1 := int64(600000000000)
		hot2 := int64(300000000000)
		cold2 := int64(400000000000)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "pool-1-uuid", &hot1, &cold1, (*int64)(nil), (*datamodel.TieringStatus)(nil)).Return(nil)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "pool-2-uuid", &hot2, &cold2, (*int64)(nil), (*datamodel.TieringStatus)(nil)).Return(nil)

		_, err := env.ExecuteActivity(activity.UpdatePoolTieringConsumptionInDB, poolsConsumptionsMap)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdatePoolTieringConsumptionInDB_EmptyMap", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolTieringConsumptionInDB)

		poolsConsumptionsMap := map[string]map[string]float64{}

		_, err := env.ExecuteActivity(activity.UpdatePoolTieringConsumptionInDB, poolsConsumptionsMap)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdatePoolTieringConsumptionInDB_UpdateFailed", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolTieringConsumptionInDB)

		poolsConsumptionsMap := map[string]map[string]float64{
			"pool-1-uuid": {
				PoolConsumptionHotTier:  500000000000,
				PoolConsumptionColdTier: 600000000000,
			},
		}

		hot := int64(500000000000)
		cold := int64(600000000000)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "pool-1-uuid", &hot, &cold, (*int64)(nil), (*datamodel.TieringStatus)(nil)).Return(errors.New("database error"))

		_, err := env.ExecuteActivity(activity.UpdatePoolTieringConsumptionInDB, poolsConsumptionsMap)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to update pool tiering consumption in DB")
		assert.Contains(tt, err.Error(), "pool-1-uuid")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdatePoolTieringConsumptionInDB_SinglePool", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolTieringConsumptionInDB)

		poolsConsumptionsMap := map[string]map[string]float64{
			"single-pool-uuid": {
				PoolConsumptionHotTier:  100000000000, // 100GB
				PoolConsumptionColdTier: 200000000000, // 200GB
			},
		}

		hot := int64(100000000000)
		cold := int64(200000000000)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "single-pool-uuid", &hot, &cold, (*int64)(nil), (*datamodel.TieringStatus)(nil)).Return(nil)

		_, err := env.ExecuteActivity(activity.UpdatePoolTieringConsumptionInDB, poolsConsumptionsMap)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdatePoolTieringConsumptionInDB_ZeroConsumption", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolTieringConsumptionInDB)

		poolsConsumptionsMap := map[string]map[string]float64{
			"zero-pool-uuid": {
				PoolConsumptionHotTier:  0,
				PoolConsumptionColdTier: 0,
			},
		}

		zero := int64(0)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "zero-pool-uuid", &zero, &zero, (*int64)(nil), (*datamodel.TieringStatus)(nil)).Return(nil)

		_, err := env.ExecuteActivity(activity.UpdatePoolTieringConsumptionInDB, poolsConsumptionsMap)
		assert.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("UpdatePoolTieringConsumptionInDB_MultiplePoolsWithOneFailure", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		activity := AutoTierSyncActivity{SE: mockStorage}

		// Create Temporal test environment for activity context
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolTieringConsumptionInDB)

		poolsConsumptionsMap := map[string]map[string]float64{
			"failing-pool-uuid": {
				PoolConsumptionHotTier:  500000000000,
				PoolConsumptionColdTier: 600000000000,
			},
		}

		// Mock to return error for this specific pool
		hot := int64(500000000000)
		cold := int64(600000000000)
		mockStorage.On("UpdatePoolTieringConfig", mock.Anything, "failing-pool-uuid", &hot, &cold, (*int64)(nil), (*datamodel.TieringStatus)(nil)).Return(errors.New("database error"))

		_, err := env.ExecuteActivity(activity.UpdatePoolTieringConsumptionInDB, poolsConsumptionsMap)
		// Should fail when encountering the error
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failing-pool-uuid")
		mockStorage.AssertExpectations(tt)
	})
}

func Test_calculateAndUpdateHotColdTierConsumption(t *testing.T) {
	ctx := context.TODO()

	// Helper variables for pointers
	boolFalse := false
	boolTrue := true
	int64Val0 := int64(0)
	int64Val50 := int64(50000000)
	int64Val100 := int64(100000000)
	int64Val150 := int64(150000000)
	int64Val200 := int64(200000000)
	int64Val300 := int64(300000000)
	int64Val350 := int64(350000000)
	int64Val500 := int64(500000000)
	int64Val800 := int64(800000000)
	int64Val1000 := int64(1000000000)

	// Helper UUID strings
	uuidStr1 := "uuid-1"
	uuidStr2 := "uuid-2"

	t.Run("SuccessfulCalculationWithValidVolumes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			uuidStr1: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr1,
				},
			},
			uuidStr2: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-2"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr2,
				},
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr1,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val100, // 100MB cold tier
						PerformanceTierFootprint: &int64Val200, // 200MB hot tier
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val300, // 300MB logical space used
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr2,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val200, // 200MB cold tier
						PerformanceTierFootprint: &int64Val800, // 800MB hot tier
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val1000, // 1000MB logical space used
						},
					},
				},
			},
		}
		expectedVolCount := int64(2)

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		assert.NoError(tt, err)
		// Hot tier should be sum of performance tier footprints: 200000000 + 800000000 = 1000000000
		assert.Equal(tt, int64(1000000000), hotTier)
		// Cold tier calculation involves ratio correction
		// Volume 1: ratio = 100/(100+200) = 0.333, corrected = 300*0.333 = 100000000
		// Volume 2: ratio = 200/(200+800) = 0.2, corrected = 1000*0.2 = 200000000
		// Total: 100000000 + 200000000 = 300000000
		assert.Equal(tt, int64(300000000), coldTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SkipsVolumeWhenDenominatorIsZero", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			uuidStr2: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-2"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr2,
				},
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr1,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val0, // 0MB cold tier
						PerformanceTierFootprint: &int64Val0, // 0MB hot tier - denominator will be 0
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val100,
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr2,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val100,
						PerformanceTierFootprint: &int64Val200,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val300,
						},
					},
				},
			},
		}
		expectedVolCount := int64(2)

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		assert.NoError(tt, err)
		// First volume should be skipped (denominator = 0)
		// Only second volume should be counted
		assert.Equal(tt, int64(200000000), hotTier)
		assert.Equal(tt, int64(100000000), coldTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SkipsSvmRootVolumes", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			uuidStr2: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-2"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr2,
				},
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr1,
					IsSvmRoot: &boolTrue, // SVM root volume - should be skipped
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val100,
						PerformanceTierFootprint: &int64Val200,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val300,
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr2,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val50,
						PerformanceTierFootprint: &int64Val150,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val200,
						},
					},
				},
			},
		}
		expectedVolCount := int64(1) // Only counting non-root volumes

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		assert.NoError(tt, err)
		// Only second volume should be counted
		assert.Equal(tt, int64(150000000), hotTier)
		assert.Equal(tt, int64(50000000), coldTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SkipsVolumesWithNilSpace", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			uuidStr2: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-2"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr2,
				},
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr1,
					IsSvmRoot: &boolFalse,
					Space:     nil, // Nil space - should be skipped
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr2,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val100,
						PerformanceTierFootprint: &int64Val200,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val300,
						},
					},
				},
			},
		}
		expectedVolCount := int64(1)

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		assert.NoError(tt, err)
		assert.Equal(tt, int64(200000000), hotTier)
		assert.Equal(tt, int64(100000000), coldTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("SkipsVolumesWithNilSpaceFields", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			uuidStr2: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-2"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr2,
				},
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr1,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    nil, // Nil field - should be skipped
						PerformanceTierFootprint: &int64Val200,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val300,
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr2,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val100,
						PerformanceTierFootprint: &int64Val200,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val300,
						},
					},
				},
			},
		}
		expectedVolCount := int64(1)

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		assert.NoError(tt, err)
		assert.Equal(tt, int64(200000000), hotTier)
		assert.Equal(tt, int64(100000000), coldTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ReturnsErrorWhenVolumeCountMismatch", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{
			uuidStr1: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-1"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr1,
				},
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr1,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val100,
						PerformanceTierFootprint: &int64Val200,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val300,
						},
					},
				},
			},
		}
		expectedVolCount := int64(2) // Expecting 2 but only got 1

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		// The function no longer returns an error for volume count mismatch
		assert.NoError(tt, err)
		// The function still computes based on what it got
		assert.NotEqual(tt, int64(0), hotTier)
		assert.NotEqual(tt, int64(0), coldTier)
	})

	t.Run("HandlesMultipleVolumesWithDenominatorZero", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		uuidStr3 := "uuid-3"
		dbVolumeMap := map[string]*datamodel.Volume{
			uuidStr3: {
				BaseModel: datamodel.BaseModel{UUID: "db-vol-3"},
				VolumeAttributes: &datamodel.VolumeAttributes{
					ExternalUUID: uuidStr3,
				},
			},
		}

		ontapVolumes := []*vsa.Volume{
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr1,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val0,
						PerformanceTierFootprint: &int64Val0, // Denominator = 0
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val100,
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr2,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val0,
						PerformanceTierFootprint: &int64Val0, // Denominator = 0
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val200,
						},
					},
				},
			},
			{
				Volume: ontaprestmodel.Volume{
					UUID:      &uuidStr3,
					IsSvmRoot: &boolFalse,
					Space: &ontaprestmodel.VolumeInlineSpace{
						CapacityTierFootprint:    &int64Val150,
						PerformanceTierFootprint: &int64Val350,
						LogicalSpace: &ontaprestmodel.VolumeInlineSpaceInlineLogicalSpace{
							Used: &int64Val500,
						},
					},
				},
			},
		}
		expectedVolCount := int64(3)

		mockStorage.On("BatchUpdateVolumeTieringFields", ctx, mock.Anything).Return(nil)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		assert.NoError(tt, err)
		// First two volumes skipped (denominator = 0), only third volume counted
		assert.Equal(tt, int64(350000000), hotTier)
		assert.Equal(tt, int64(150000000), coldTier)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("HandlesEmptyVolumesList", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		dbVolumeMap := map[string]*datamodel.Volume{}
		ontapVolumes := []*vsa.Volume{}
		expectedVolCount := int64(0)

		hotTier, coldTier, err := callCalculateAndUpdateHotColdTierConsumptionWithStorage(ctx, ontapVolumes, expectedVolCount, mockStorage, dbVolumeMap)

		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), hotTier)
		assert.Equal(tt, int64(0), coldTier)
	})
}

func TestCheckPoolVolumesWithBypassModeEnabled_ONTAPMode(t *testing.T) {
	pool := &datamodel.Pool{
		BaseModel:     datamodel.BaseModel{UUID: "pool-uuid"},
		APIAccessMode: common.ONTAPMode,
	}

	enabled, err := checkPoolVolumesWithBypassModeEnabled(context.Background(), nil, pool)

	assert.NoError(t, err)
	assert.False(t, enabled)
}
