package common

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func TestSelectClustersForBatchUpgrade(t *testing.T) {
	t.Run("RoutesToPoolListSelection", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.14.1"},
		}
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).Return([]*datamodel.Pool{pool}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1"},
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "pool-1", result[0].ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("RoutesToPercentageSelection", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		now := time.Now()
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1", CreatedAt: now}, State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-2", CreatedAt: now.Add(time.Hour)}, State: datamodel.LifeCycleStateREADY}},
		}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(pools, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    100,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorForUnknownType", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: "unknown",
			Percentage:    50,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.True(t, customerrors.IsBadRequestErr(err))
		mockStorage.AssertExpectations(t)
	})
}

func TestValidateBatchClusterSelectionParams(t *testing.T) {
	tests := []struct {
		name   string
		params *commonparams.BatchUpgradeClusterParams
		valid  bool
	}{
		{
			name: "valid percentage",
			params: &commonparams.BatchUpgradeClusterParams{
				SelectionType: string(models.SelectionTypePercentage),
				Percentage:    50,
			},
			valid: true,
		},
		{
			name: "valid pool list",
			params: &commonparams.BatchUpgradeClusterParams{
				SelectionType: string(models.SelectionTypePoolList),
				PoolIDs:       []string{"pool-1"},
			},
			valid: true,
		},
		{
			name: "missing percentage",
			params: &commonparams.BatchUpgradeClusterParams{
				SelectionType: string(models.SelectionTypePercentage),
			},
		},
		{
			name: "percentage above maximum",
			params: &commonparams.BatchUpgradeClusterParams{
				SelectionType: string(models.SelectionTypePercentage),
				Percentage:    101,
			},
		},
		{
			name: "empty pool list",
			params: &commonparams.BatchUpgradeClusterParams{
				SelectionType: string(models.SelectionTypePoolList),
			},
		},
		{
			name: "unknown selection type",
			params: &commonparams.BatchUpgradeClusterParams{
				SelectionType: "unknown",
			},
		},
		{
			name: "nil parameters",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateBatchClusterSelectionParams(test.params)
			if test.valid {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			assert.True(t, customerrors.IsBadRequestErr(err))
		})
	}
}

func TestSelectByPoolList(t *testing.T) {
	t.Run("SelectsValidPools", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		pool1 := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.14.1"},
		}
		pool2 := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-2"},
			State:     datamodel.LifeCycleStateDisabled,
			BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.13.1"},
		}

		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1", "pool-2"}).
			Return([]*datamodel.Pool{pool1, pool2}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1", "pool-2"},
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "pool-1", result[0].ClusterID)
		assert.Equal(t, "9.14.1", result[0].CurrentVersion)
		assert.Equal(t, 1, result[0].Priority)
		assert.Equal(t, "pool-2", result[1].ClusterID)
		assert.Equal(t, "9.13.1", result[1].CurrentVersion)
		assert.Equal(t, 2, result[1].Priority)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SkipsPoolsNotFound", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		pool1 := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
		}
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1", "pool-not-found"}).
			Return([]*datamodel.Pool{pool1}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1", "pool-not-found"},
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "pool-1", result[0].ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SkipsNilPools", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-nil"}).Return([]*datamodel.Pool{nil}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-nil"},
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Empty(t, result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SkipsPoolsInInvalidState", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		readyPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-ready"},
			State:     datamodel.LifeCycleStateREADY,
		}
		creatingPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-creating"},
			State:     "CREATING",
		}
		disabledPool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-disabled"},
			State:     datamodel.LifeCycleStateDisabled,
		}

		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-ready", "pool-creating", "pool-disabled"}).
			Return([]*datamodel.Pool{readyPool, creatingPool, disabledPool}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-ready", "pool-creating", "pool-disabled"},
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "pool-ready", result[0].ClusterID)
		assert.Equal(t, 1, result[0].Priority)
		assert.Equal(t, "pool-disabled", result[1].ClusterID)
		assert.Equal(t, 2, result[1].Priority)
		mockStorage.AssertExpectations(t)
	})

	t.Run("HandlesNilBuildInfo", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-1"},
			State:     datamodel.LifeCycleStateREADY,
			BuildInfo: nil,
		}
		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1"}).Return([]*datamodel.Pool{pool}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1"},
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, env.CurrentOntapVersionDetails, result[0].CurrentVersion)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsEmptyForAllInvalidPools", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.On("GetPoolsByUUIDs", ctx, []string{"pool-1", "pool-2"}).
			Return([]*datamodel.Pool{}, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePoolList),
			PoolIDs:       []string{"pool-1", "pool-2"},
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Empty(t, result)
		mockStorage.AssertExpectations(t)
	})
}

func TestSelectByPercentage(t *testing.T) {
	t.Run("Selects50PercentOfPools", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		now := time.Now()
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1", CreatedAt: now}, State: datamodel.LifeCycleStateREADY, BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.14.1"}}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-2", CreatedAt: now.Add(time.Hour)}, State: datamodel.LifeCycleStateREADY, BuildInfo: &datamodel.PoolBuildInfo{OntapVersion: "9.14.1"}}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-3", CreatedAt: now.Add(2 * time.Hour)}, State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-4", CreatedAt: now.Add(3 * time.Hour)}, State: datamodel.LifeCycleStateREADY}},
		}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(pools, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    50,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "pool-1", result[0].ClusterID)
		assert.Equal(t, "9.14.1", result[0].CurrentVersion)
		assert.Equal(t, 1, result[0].Priority)
		assert.Equal(t, "pool-2", result[1].ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SelectsAtLeastOneWhenPercentageIsSmall", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		now := time.Now()
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1", CreatedAt: now}, State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-2", CreatedAt: now.Add(time.Hour)}, State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-3", CreatedAt: now.Add(2 * time.Hour)}, State: datamodel.LifeCycleStateREADY}},
		}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(pools, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    1,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		mockStorage.AssertExpectations(t)
	})

	t.Run("RespectsMaxClustersLimit", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		now := time.Now()
		pools := make([]*datamodel.PoolView, 10)
		for i := 0; i < 10; i++ {
			pools[i] = &datamodel.PoolView{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{UUID: "pool-" + string(rune('a'+i)), CreatedAt: now.Add(time.Duration(i) * time.Hour)},
					State:     datamodel.LifeCycleStateREADY,
				},
			}
		}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(pools, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    100,
			MaxClusters:   3,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 3)
		mockStorage.AssertExpectations(t)
	})

	t.Run("FiltersOutNonReadyAndNonDisabledPools", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		now := time.Now()
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "ready", CreatedAt: now}, State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "creating", CreatedAt: now.Add(time.Hour)}, State: "CREATING"}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "disabled", CreatedAt: now.Add(2 * time.Hour)}, State: datamodel.LifeCycleStateDisabled}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "error", CreatedAt: now.Add(3 * time.Hour)}, State: "ERROR"}},
		}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(pools, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    100,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, "ready", result[0].ClusterID)
		assert.Equal(t, "disabled", result[1].ClusterID)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsNilWhenNoEligiblePools", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "creating"}, State: "CREATING"}},
		}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(pools, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    100,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Nil(t, result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("ReturnsErrorWhenListPoolsFails", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(nil, assert.AnError)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    50,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.Error(t, err)
		assert.Nil(t, result)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SortsByCreationDateForDeterministicSelection", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		ctx := context.Background()

		now := time.Now()
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "newest", CreatedAt: now.Add(2 * time.Hour)}, State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "oldest", CreatedAt: now}, State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "middle", CreatedAt: now.Add(time.Hour)}, State: datamodel.LifeCycleStateREADY}},
		}
		mockStorage.On("ListPoolsSelective", ctx, mock.Anything, database.PoolPreloadOptions{}).Return(pools, nil)

		params := &commonparams.BatchUpgradeClusterParams{
			SelectionType: string(models.SelectionTypePercentage),
			Percentage:    100,
		}

		result, err := SelectClustersForBatchUpgrade(ctx, mockStorage, params)
		assert.NoError(t, err)
		assert.Len(t, result, 3)
		assert.Equal(t, "oldest", result[0].ClusterID)
		assert.Equal(t, "middle", result[1].ClusterID)
		assert.Equal(t, "newest", result[2].ClusterID)
		mockStorage.AssertExpectations(t)
	})
}

func TestBuildClusterFilter(t *testing.T) {
	t.Run("ReturnsEmptyFilterWhenNilFilters", func(t *testing.T) {
		filter := buildClusterFilter(nil)
		assert.NotNil(t, filter)
		assert.Empty(t, filter.Conditions)
	})

	t.Run("AddsZoneCondition", func(t *testing.T) {
		filters := &commonparams.ClusterFilters{
			Zones: []string{"us-central1-a", "us-central1-b"},
		}
		filter := buildClusterFilter(filters)
		assert.NotNil(t, filter)
		assert.Len(t, filter.Conditions, 1)
		assert.Equal(t, "zone", filter.Conditions[0].Field)
		assert.Equal(t, "IN", filter.Conditions[0].Op)
	})

	t.Run("AddsOntapVersionCondition", func(t *testing.T) {
		ontapVersions := []string{"9.17.1", "9.18.1"}
		filters := &commonparams.ClusterFilters{
			OntapVersions: ontapVersions,
		}
		filter := buildClusterFilter(filters)
		assert.NotNil(t, filter)
		assert.Len(t, filter.Conditions, 1)
		assert.Equal(t, "build_info->>'ontapVersion'", filter.Conditions[0].Field)
		assert.Equal(t, "IN", filter.Conditions[0].Op)
		assert.Equal(t, ontapVersions, filter.Conditions[0].Value)
	})

	t.Run("AddsClusterStateCondition", func(t *testing.T) {
		filters := &commonparams.ClusterFilters{
			ClusterStates: []string{"READY", "DISABLED"},
		}
		filter := buildClusterFilter(filters)
		assert.NotNil(t, filter)
		assert.Len(t, filter.Conditions, 1)
		assert.Equal(t, "state", filter.Conditions[0].Field)
	})

	t.Run("AddsBothZoneAndStateConditions", func(t *testing.T) {
		filters := &commonparams.ClusterFilters{
			Zones:         []string{"us-central1-a"},
			ClusterStates: []string{"READY"},
		}
		filter := buildClusterFilter(filters)
		assert.NotNil(t, filter)
		assert.Len(t, filter.Conditions, 2)
	})

	t.Run("AddsAllConditions", func(t *testing.T) {
		filters := &commonparams.ClusterFilters{
			Zones:         []string{"us-central1-a"},
			OntapVersions: []string{"9.18.1"},
			ClusterStates: []string{"READY"},
		}
		filter := buildClusterFilter(filters)
		assert.NotNil(t, filter)
		assert.Len(t, filter.Conditions, 3)
	})

	t.Run("ReturnsEmptyFilterWhenNoFilterFieldsSet", func(t *testing.T) {
		filters := &commonparams.ClusterFilters{}
		filter := buildClusterFilter(filters)
		assert.NotNil(t, filter)
		assert.Empty(t, filter.Conditions)
	})
}
