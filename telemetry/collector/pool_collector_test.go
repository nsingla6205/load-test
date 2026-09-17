package collector

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	datamodel2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/telemetry/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/entity"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/metadata"
)

// Helper function to safely dereference float64 pointer
func derefFloat64(ptr *float64) float64 {
	if ptr == nil {
		return 0
	}
	return *ptr
}

type mockStorage struct {
	mock.Mock
	database.Storage
}

func (m *mockStorage) ListPools(ctx context.Context, filter *utils.Filter) ([]*datamodel.PoolView, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*datamodel.PoolView), args.Error(1)
}

func (m *mockStorage) ListPoolsForMetrics(ctx context.Context) ([]*database.PoolMetricsData, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*database.PoolMetricsData), args.Error(1)
}

func (m *mockStorage) GetExpertModePoolUsedCapacityAndVolumeCount(ctx context.Context, poolID int64) (*database.ExpertModePoolCapacity, error) {
	args := m.Called(ctx, poolID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*database.ExpertModePoolCapacity), args.Error(1)
}

func (m *mockStorage) GetExpertModePoolsUsedCapacityAndVolumeCount(ctx context.Context, poolIDs []int64) (map[int64]*database.ExpertModePoolCapacity, error) {
	args := m.Called(ctx, poolIDs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[int64]*database.ExpertModePoolCapacity), args.Error(1)
}

func (m *mockStorage) ListAccountsForTelemetry(ctx context.Context, pagination *utils.Pagination) ([]*database.AccountTelemetryData, error) {
	args := m.Called(ctx, pagination)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*database.AccountTelemetryData), args.Error(1)
}

func Test_GetPoolMetrics_ReturnsMetrics(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	var pools []*database.PoolMetricsData
	pools = append(
		pools,
		&database.PoolMetricsData{
			ID:             1,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 100,
				Iops:            1000,
				AccountName:     "Account1",
			},
			QuotaInBytes: 500,
		},
	)

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 4)          // Should have 4 metrics: PoolAllocatedSize, AllocatedUsed, ThroughputMibps, Iops
	assert.Len(t, result.HydratedMetricsDataModel, 4) // Should have 4 hydrated metrics

	// Test new PoolMetadataMap field
	assert.NotNil(t, result.PoolMetadataMap, "PoolMetadataMap should not be nil")
	assert.Len(t, result.PoolMetadataMap, 1, "PoolMetadataMap should contain one entry")

	// Check first metric (PoolAllocatedSize)
	assert.Equal(t, metadata.PoolAllocatedSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(1000), result.HydratedMetrics[0].Quantity)
	assert.Equal(t, "Pool1", derefString(result.HydratedMetrics[0].Metadata.ResourceName))
	assert.Equal(t, "us-east-1", derefString(result.HydratedMetrics[0].Metadata.RegionName))
	assert.Equal(t, "Account1", derefString(result.HydratedMetrics[0].Metadata.AccountName))

	// Check second metric (AllocatedUsed)
	assert.Equal(t, metadata.AllocatedUsed, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(500), result.HydratedMetrics[1].Quantity)

	// Check hydrated metrics data model - first hydrated metric (PoolAllocatedSize)
	assert.Equal(t, string(metadata.PoolAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[0].ResourceType)
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Pool1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, "us-east-1", result.HydratedMetricsDataModel[0].Location)
	assert.Equal(t, float64(1000), result.HydratedMetricsDataModel[0].Quantity)

	// Check second hydrated metric (AllocatedUsed)
	assert.Equal(t, string(metadata.AllocatedUsed), result.HydratedMetricsDataModel[1].MeasuredType)
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[1].ResourceType)
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[1].ConsumerID)
	assert.Equal(t, "Pool1", result.HydratedMetricsDataModel[1].ResourceName)
	assert.Equal(t, "us-east-1", result.HydratedMetricsDataModel[1].Location)
	assert.Equal(t, float64(500), result.HydratedMetricsDataModel[1].Quantity)

	// Verify the type is correct
	assert.IsType(t, datamodel2.HydratedMetrics{}, result.HydratedMetricsDataModel[0])
	assert.IsType(t, datamodel2.HydratedMetrics{}, result.HydratedMetricsDataModel[1])
}

func Test_GetPoolMetrics_ExpertModeUsesExpertModeAllocatedCapacity(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "expert-mode-pool-uuid",
			Name:           "ExpertModePool",
			ServiceLevel:   commonparams.ServiceLevelExtreme,
			APIAccessMode:  commonparams.ONTAPMode,
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				AccountName: "Account1",
			},
			QuotaInBytes: 500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("GetExpertModePoolsUsedCapacityAndVolumeCount", mock.Anything, []int64{1}).
		Return(map[int64]*database.ExpertModePoolCapacity{
			1: {TotalSize: 750, VolumeCount: 2},
		}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())

	assert.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 2)
	require.Len(t, result.HydratedMetricsDataModel, 2)
	assert.Equal(t, metadata.PoolAllocatedSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(1000), result.HydratedMetrics[0].Quantity)
	assert.Equal(t, metadata.AllocatedUsed, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(750), result.HydratedMetrics[1].Quantity)
	for _, metric := range result.HydratedMetrics {
		assert.NotEqual(t, metadata.PoolTotalThroughputMibps, metric.MeasuredType)
		assert.NotEqual(t, metadata.PoolTotalIops, metric.MeasuredType)
	}
	m.AssertExpectations(t)
}

func Test_GetPoolMetrics_BatchesExpertModeAllocatedCapacity(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			Name:           "ExpertModePool1",
			ServiceLevel:   commonparams.ServiceLevelExtreme,
			APIAccessMode:  commonparams.ONTAPMode,
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{AccountName: "Account1"},
		},
		{
			ID:             2,
			Name:           "ExpertModePool2",
			ServiceLevel:   commonparams.ServiceLevelPremium,
			APIAccessMode:  commonparams.ONTAPMode,
			SizeInBytes:    2000,
			DeploymentName: "gcp-deployment-2",
			PoolAttributes: &datamodel.PoolAttributes{AccountName: "Account2"},
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("GetExpertModePoolsUsedCapacityAndVolumeCount", mock.Anything, []int64{1, 2}).
		Return(map[int64]*database.ExpertModePoolCapacity{
			1: {TotalSize: 750, VolumeCount: 2},
			2: {TotalSize: 1250, VolumeCount: 3},
		}, nil).
		Once()

	result, err := GetPoolMetrics(ctx, m, config, time.Now())

	require.NoError(t, err)
	assert.Len(t, result.HydratedMetrics, 4)
	assert.Len(t, result.HydratedMetricsDataModel, 4)
	m.AssertNotCalled(t, "GetExpertModePoolUsedCapacityAndVolumeCount", mock.Anything, mock.Anything)
	m.AssertExpectations(t)
}

func Test_GetPoolMetrics_BatchFailureFallsBackToExistingPerPoolQuery(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			Name:           "ExpertModePool",
			ServiceLevel:   commonparams.ServiceLevelExtreme,
			APIAccessMode:  commonparams.ONTAPMode,
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{AccountName: "Account1"},
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("GetExpertModePoolsUsedCapacityAndVolumeCount", mock.Anything, []int64{1}).
		Return((map[int64]*database.ExpertModePoolCapacity)(nil), assert.AnError)
	m.On("GetExpertModePoolUsedCapacityAndVolumeCount", mock.Anything, int64(1)).
		Return(&database.ExpertModePoolCapacity{TotalSize: 750, VolumeCount: 2}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())

	require.NoError(t, err)
	require.Len(t, result.HydratedMetrics, 2)
	require.Len(t, result.HydratedMetricsDataModel, 2)
	assert.Equal(t, metadata.AllocatedUsed, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(750), result.HydratedMetrics[1].Quantity)
	m.AssertExpectations(t)
}

func Test_GetPoolMetrics_DefaultModeExtremeKeepsLegacyAllocatedCapacity(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "default-pool-uuid",
			Name:           "DefaultPool",
			ServiceLevel:   commonparams.ServiceLevelExtreme,
			APIAccessMode:  commonparams.DEFAULTMode,
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				AccountName: "Account1",
			},
			QuotaInBytes: 500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())

	assert.NoError(t, err)
	assert.Len(t, result.HydratedMetrics, 4)
	assert.Equal(t, metadata.AllocatedUsed, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(500), result.HydratedMetrics[1].Quantity)
	m.AssertNotCalled(t, "GetExpertModePoolsUsedCapacityAndVolumeCount", mock.Anything, mock.Anything)
	m.AssertNotCalled(t, "GetExpertModePoolUsedCapacityAndVolumeCount", mock.Anything, mock.Anything)
}

func Test_GetPoolMetrics_ExpertModeAllocatedCapacityFailureOmitsOnlyAllocated(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "expert-mode-pool-uuid",
			Name:           "ExpertModePool",
			ServiceLevel:   commonparams.ServiceLevelPremium,
			APIAccessMode:  commonparams.ONTAPMode,
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				AccountName: "Account1",
			},
			QuotaInBytes: 500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
	m.On("GetExpertModePoolsUsedCapacityAndVolumeCount", mock.Anything, []int64{1}).
		Return((map[int64]*database.ExpertModePoolCapacity)(nil), assert.AnError)
	m.On("GetExpertModePoolUsedCapacityAndVolumeCount", mock.Anything, int64(1)).
		Return(nil, assert.AnError)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())

	assert.NoError(t, err)
	assert.Len(t, result.HydratedMetrics, 1)
	assert.Len(t, result.HydratedMetricsDataModel, 1)
	assert.Equal(t, metadata.PoolAllocatedSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(1000), result.HydratedMetrics[0].Quantity)
	for _, metric := range result.HydratedMetrics {
		assert.NotEqual(t, metadata.AllocatedUsed, metric.MeasuredType)
	}
	m.AssertExpectations(t)
}

func Test_GetPoolMetrics_ExpertModeAllocatedCapacityEdgeCases(t *testing.T) {
	tests := []struct {
		name              string
		capacity          *database.ExpertModePoolCapacity
		expectedCount     int
		expectAllocated   bool
		expectedAllocated float64
	}{
		{
			name:              "empty expert mode pool emits zero allocated",
			capacity:          &database.ExpertModePoolCapacity{TotalSize: 0, VolumeCount: 0},
			expectedCount:     2,
			expectAllocated:   true,
			expectedAllocated: 0,
		},
		{
			name:            "nil helper result omits only allocated",
			capacity:        nil,
			expectedCount:   1,
			expectAllocated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := new(mockStorage)
			ctx := context.Background()
			config := &common.TelemetryConfig{RegionName: "us-east-1"}
			pools := []*database.PoolMetricsData{
				{
					ID:             1,
					UUID:           "expert-mode-pool-uuid",
					Name:           "ExpertModePool",
					ServiceLevel:   commonparams.ServiceLevelExtreme,
					APIAccessMode:  commonparams.ONTAPMode,
					SizeInBytes:    1000,
					DeploymentName: "gcp-deployment-1",
					PoolAttributes: &datamodel.PoolAttributes{AccountName: "Account1"},
					QuotaInBytes:   500,
				},
			}

			m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
			m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)
			m.On("GetExpertModePoolsUsedCapacityAndVolumeCount", mock.Anything, []int64{1}).
				Return(map[int64]*database.ExpertModePoolCapacity{1: tt.capacity}, nil)
			if tt.capacity == nil {
				m.On("GetExpertModePoolUsedCapacityAndVolumeCount", mock.Anything, int64(1)).
					Return(nil, nil)
			}

			result, err := GetPoolMetrics(ctx, m, config, time.Now())

			assert.NoError(t, err)
			assert.Len(t, result.HydratedMetrics, tt.expectedCount)
			assert.Equal(t, metadata.PoolAllocatedSize, result.HydratedMetrics[0].MeasuredType)
			assert.Equal(t, float64(1000), result.HydratedMetrics[0].Quantity)
			allocatedFound := false
			for _, metric := range result.HydratedMetrics {
				if metric.MeasuredType == metadata.AllocatedUsed {
					allocatedFound = true
					assert.Equal(t, tt.expectedAllocated, metric.Quantity)
				}
			}
			assert.Equal(t, tt.expectAllocated, allocatedFound)
			m.AssertExpectations(t)
		})
	}
}

func TestIsExpertModeMetricsPool(t *testing.T) {
	tests := []struct {
		name string
		pool *database.PoolMetricsData
		want bool
	}{
		{
			name: "ONTAP extreme from pool column",
			pool: &database.PoolMetricsData{
				APIAccessMode: commonparams.ONTAPMode,
				ServiceLevel:  commonparams.ServiceLevelExtreme,
			},
			want: true,
		},
		{
			name: "ONTAP premium from pool attributes",
			pool: &database.PoolMetricsData{
				APIAccessMode: commonparams.ONTAPMode,
				ServiceLevel:  "FLEX",
				PoolAttributes: &datamodel.PoolAttributes{
					ServiceLevel: commonparams.ServiceLevelPremium,
				},
			},
			want: true,
		},
		{
			name: "default extreme is not expert mode",
			pool: &database.PoolMetricsData{
				APIAccessMode: commonparams.DEFAULTMode,
				ServiceLevel:  commonparams.ServiceLevelExtreme,
			},
			want: false,
		},
		{
			name: "ONTAP flex is not eligible expert mode",
			pool: &database.PoolMetricsData{
				APIAccessMode: commonparams.ONTAPMode,
				ServiceLevel:  "FLEX",
			},
			want: false,
		},
		{name: "nil pool", pool: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isExpertModeMetricsPool(tt.pool))
		})
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func Test_GetPoolMetrics_MultiplePools(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 0,
				Iops:            0,
				AccountName:     "Account1",
			},
			QuotaInBytes: 300,
		},
		{
			ID:             2,
			UUID:           "pool-uuid-2",
			Name:           "Pool2",
			SizeInBytes:    2000,
			DeploymentName: "gcp-deployment-2",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 0,
				Iops:            0,
				AccountName:     "Account2",
			},
			QuotaInBytes: 800,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 8)          // Should have 8 metrics: 2 pools * 4 metric types each
	assert.Len(t, result.HydratedMetricsDataModel, 8) // Should have 8 hydrated metrics

	// Check first pool metrics
	assert.Equal(t, metadata.PoolAllocatedSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(1000), result.HydratedMetrics[0].Quantity)
	assert.Equal(t, "Pool1", derefString(result.HydratedMetrics[0].Metadata.ResourceName))
	assert.Equal(t, "us-east-1", derefString(result.HydratedMetrics[0].Metadata.RegionName))
	assert.Equal(t, "Account1", derefString(result.HydratedMetrics[0].Metadata.AccountName))

	assert.Equal(t, metadata.AllocatedUsed, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(300), result.HydratedMetrics[1].Quantity)

	// Check second pool metrics
	assert.Equal(t, metadata.PoolAllocatedSize, result.HydratedMetrics[4].MeasuredType)
	assert.Equal(t, float64(2000), result.HydratedMetrics[4].Quantity)
	assert.Equal(t, "Pool2", derefString(result.HydratedMetrics[4].Metadata.ResourceName))
	assert.Equal(t, "Account2", derefString(result.HydratedMetrics[4].Metadata.AccountName))

	assert.Equal(t, metadata.AllocatedUsed, result.HydratedMetrics[5].MeasuredType)
	assert.Equal(t, float64(800), result.HydratedMetrics[5].Quantity)

	// Check hydrated metrics - Pool1 PoolAllocatedSize
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[0].ConsumerID)
	assert.Equal(t, "Pool1", result.HydratedMetricsDataModel[0].ResourceName)
	assert.Equal(t, string(metadata.PoolAllocatedSize), result.HydratedMetricsDataModel[0].MeasuredType)
	assert.Equal(t, float64(1000), result.HydratedMetricsDataModel[0].Quantity)

	// Check hydrated metrics - Pool1 AllocatedUsed
	assert.Equal(t, "Account1", result.HydratedMetricsDataModel[1].ConsumerID)
	assert.Equal(t, "Pool1", result.HydratedMetricsDataModel[1].ResourceName)
	assert.Equal(t, string(metadata.AllocatedUsed), result.HydratedMetricsDataModel[1].MeasuredType)
	assert.Equal(t, float64(300), result.HydratedMetricsDataModel[1].Quantity)

	// Check hydrated metrics - Pool2 PoolAllocatedSize
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[4].ConsumerID)
	assert.Equal(t, "Pool2", result.HydratedMetricsDataModel[4].ResourceName)
	assert.Equal(t, string(metadata.PoolAllocatedSize), result.HydratedMetricsDataModel[4].MeasuredType)
	assert.Equal(t, float64(2000), result.HydratedMetricsDataModel[4].Quantity)

	// Check hydrated metrics - Pool2 AllocatedUsed
	assert.Equal(t, "Account2", result.HydratedMetricsDataModel[5].ConsumerID)
	assert.Equal(t, "Pool2", result.HydratedMetricsDataModel[5].ResourceName)
	assert.Equal(t, string(metadata.AllocatedUsed), result.HydratedMetricsDataModel[5].MeasuredType)
	assert.Equal(t, float64(800), result.HydratedMetricsDataModel[5].Quantity)
}

func Test_GetPoolMetrics_EmptyPools(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	m.On("ListPoolsForMetrics", mock.Anything).Return([]*database.PoolMetricsData{}, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

func Test_GetPoolMetrics_ListPoolsForMetricsError(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}
	m.On("ListPoolsForMetrics", mock.Anything).Return(nil, assert.AnError)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.Error(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.HydratedMetrics)
	assert.Empty(t, result.HydratedMetricsDataModel)
}

// Test for zero throughput handling in setupHydratedMetricsDataModel
func TestSetupHydratedMetricsDataModel_PoolTotalThroughputMibps_ZeroQuantity(t *testing.T) {
	resourceMetadata := metadata.ResourceMetadata{}
	resourceName := "zero-throughput-pool"
	regionName := "us-west-2"
	deploymentName := "zero-deployment"
	sizeInBytes := int64(1024)
	throughput := 0.0

	resourceMetadata.SetResourceName(resourceName)
	resourceMetadata.SetRegionName(regionName)
	resourceMetadata.SetDeploymentName(deploymentName)
	resourceMetadata.SetSizeInBytes(sizeInBytes)
	resourceMetadata.SetThroughput(throughput)

	measuredType := metadata.PoolTotalThroughputMibps
	resourceType := metadata.VolumePool
	projectID := "test-project-456"
	timestamp := time.Now()

	result := setupHydratedMetricsDataModel(measuredType, resourceType, projectID, resourceMetadata, timestamp, throughput)
	assert.NotNil(t, result)
	assert.Equal(t, 0.0, result.Quantity)
}

// Test for the new setupHydratedMetricsDataModel function
func TestSetupHydratedMetricsDataModel(t *testing.T) {
	// Create test metadata
	resourceMetadata := metadata.ResourceMetadata{}
	resourceName := "test-pool"
	regionName := "us-west-2"
	deploymentName := "test-deployment"
	sizeInBytes := int64(2048)

	resourceMetadata.SetResourceName(resourceName)
	resourceMetadata.SetRegionName(regionName)
	resourceMetadata.SetDeploymentName(deploymentName)
	resourceMetadata.SetSizeInBytes(sizeInBytes)

	// Test parameters
	measuredType := metadata.PoolAllocatedSize
	resourceType := metadata.VolumePool
	projectID := "test-project-123"
	timestamp := time.Now()

	// Call the function
	result := setupHydratedMetricsDataModel(measuredType, resourceType, projectID, resourceMetadata, timestamp, float64(sizeInBytes))

	// Assertions
	assert.Equal(t, timestamp, result.MetricTimestamp)
	assert.Equal(t, string(measuredType), result.MeasuredType)
	assert.Equal(t, projectID, result.ConsumerID)
	assert.Equal(t, string(resourceType), result.ResourceType)
	assert.Equal(t, resourceName, result.ResourceName)
	assert.Equal(t, regionName, result.Location)
	assert.Equal(t, float64(sizeInBytes), result.Quantity)
}

func TestSetupHydratedMetricsDataModel_NilMetadataFields(t *testing.T) {
	tests := []struct {
		name             string
		setupMetadata    func() metadata.ResourceMetadata
		measuredType     metadata.MeasuredType
		expectNil        bool
		expectLogWarning string
	}{
		{
			name: "Nil ResourceName",
			setupMetadata: func() metadata.ResourceMetadata {
				rm := metadata.ResourceMetadata{}
				regionName := "us-west-1"
				deploymentName := "test-deployment"
				rm.SetRegionName(regionName)
				rm.SetDeploymentName(deploymentName)
				// ResourceName not set (nil)
				return rm
			},
			measuredType:     metadata.PoolAllocatedSize,
			expectNil:        true,
			expectLogWarning: "ResourceName is nil",
		},
		{
			name: "Nil RegionName",
			setupMetadata: func() metadata.ResourceMetadata {
				rm := metadata.ResourceMetadata{}
				resourceName := "test-resource"
				deploymentName := "test-deployment"
				rm.SetResourceName(resourceName)
				rm.SetDeploymentName(deploymentName)
				// RegionName not set (nil)
				return rm
			},
			measuredType:     metadata.PoolAllocatedSize,
			expectNil:        true,
			expectLogWarning: "RegionName is nil",
		},
		{
			name: "Nil DeploymentName",
			setupMetadata: func() metadata.ResourceMetadata {
				rm := metadata.ResourceMetadata{}
				resourceName := "test-resource"
				regionName := "us-west-1"
				rm.SetResourceName(resourceName)
				rm.SetRegionName(regionName)
				// DeploymentName not set (nil)
				return rm
			},
			measuredType:     metadata.PoolAllocatedSize,
			expectNil:        true,
			expectLogWarning: "DeploymentName is nil",
		},
		{
			name: "Nil Throughput for PoolTotalIops metric",
			setupMetadata: func() metadata.ResourceMetadata {
				rm := metadata.ResourceMetadata{}
				resourceName := "test-resource"
				regionName := "us-west-1"
				deploymentName := "test-deployment"
				rm.SetResourceName(resourceName)
				rm.SetRegionName(regionName)
				rm.SetDeploymentName(deploymentName)
				// Throughput not set (nil)
				return rm
			},
			measuredType:     metadata.PoolTotalIops,
			expectNil:        false, // Should not return nil, but quantity should be 0
			expectLogWarning: "Setting IOPS quantity to 0 due to nil Throughput",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resourceMetadata := tt.setupMetadata()
			timestamp := time.Now()
			projectID := "test-project"
			quantity := 1000.0

			result := setupHydratedMetricsDataModel(
				tt.measuredType,
				metadata.VolumePool,
				projectID,
				resourceMetadata,
				timestamp,
				quantity,
			)

			if tt.expectNil {
				assert.Nil(t, result, "Expected nil result due to validation failure")
			} else {
				assert.NotNil(t, result, "Expected non-nil result")
				if tt.measuredType == metadata.PoolTotalIops && resourceMetadata.Throughput == nil {
					assert.Equal(t, 0.0, result.Quantity, "Expected quantity to be 0 when Throughput is nil")
				}
			}
		})
	}
}

// Test that verifies the integration between GetPoolMetrics and setupHydratedMetricsDataModel
func TestGetPoolMetrics_HydratedMetricsDataModelIntegration(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "ap-south-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-integration",
			Name:           "IntegrationPool",
			SizeInBytes:    5000,
			DeploymentName: "gcp-integration-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 0,
				AccountName:     "IntegrationAccount",
			},
			QuotaInBytes: 1500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify that both PoolAllocatedSize and AllocatedUsed metrics are converted to HydratedMetrics
	assert.Len(t, result.HydratedMetricsDataModel, 4)

	// Find the PoolAllocatedSize metric in the metrics slice
	var poolAllocatedSizeMetric *entity.HydratedMetric
	for i := range result.HydratedMetrics {
		if result.HydratedMetrics[i].MeasuredType == metadata.PoolAllocatedSize {
			poolAllocatedSizeMetric = &result.HydratedMetrics[i]
			break
		}
	}
	assert.NotNil(t, poolAllocatedSizeMetric)

	// Verify the HydratedMetrics data model is correctly populated - PoolAllocatedSize
	hmPoolAllocated := result.HydratedMetricsDataModel[0]
	assert.Equal(t, string(metadata.PoolAllocatedSize), hmPoolAllocated.MeasuredType)
	assert.Equal(t, string(metadata.VolumePool), hmPoolAllocated.ResourceType)
	assert.Equal(t, "IntegrationAccount", hmPoolAllocated.ConsumerID)
	assert.Equal(t, "IntegrationPool", hmPoolAllocated.ResourceName)
	assert.Equal(t, "ap-south-1", hmPoolAllocated.Location)
	assert.Equal(t, float64(5000), hmPoolAllocated.Quantity)

	// Verify the HydratedMetrics data model is correctly populated - AllocatedUsed
	hmAllocatedUsed := result.HydratedMetricsDataModel[1]
	assert.Equal(t, string(metadata.AllocatedUsed), hmAllocatedUsed.MeasuredType)
	assert.Equal(t, string(metadata.VolumePool), hmAllocatedUsed.ResourceType)
	assert.Equal(t, "IntegrationAccount", hmAllocatedUsed.ConsumerID)
	assert.Equal(t, "IntegrationPool", hmAllocatedUsed.ResourceName)
	assert.Equal(t, "ap-south-1", hmAllocatedUsed.Location)
	assert.Equal(t, float64(1500), hmAllocatedUsed.Quantity)

	// Verify timestamp is recent (within last minute)
	timeDiff := time.Since(hmPoolAllocated.MetricTimestamp)
	assert.True(t, timeDiff < time.Minute, "Timestamp should be recent")
	timeDiff2 := time.Since(hmAllocatedUsed.MetricTimestamp)
	assert.True(t, timeDiff2 < time.Minute, "Timestamp should be recent")
}

// Test for new throughput functionality and PoolMetadataMap
func Test_GetPoolMetrics_WithThroughputAndMetadataMap(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             42,
			UUID:           "pool-uuid-throughput",
			Name:           "ThroughputPool",
			SizeInBytes:    2048,
			DeploymentName: "gcp-deployment-throughput",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 150,
				AccountName:     "ThroughputAccount",
			},
			QuotaInBytes: 1024,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Test new PoolMetadataMap field
	assert.NotNil(t, result.PoolMetadataMap, "PoolMetadataMap should not be nil")
	assert.Len(t, result.PoolMetadataMap, 1, "PoolMetadataMap should contain one entry")

	poolMetadata, exists := result.PoolMetadataMap[42]
	assert.True(t, exists, "Pool metadata should exist for pool ID 42")

	// Test throughput metadata
	assert.Equal(t, 150.0, *poolMetadata.Throughput, "Throughput should be set correctly")
	assert.Equal(t, int64(42), *poolMetadata.ResourceID, "ResourceID should be set correctly")
	assert.Equal(t, "pool-uuid-throughput", *poolMetadata.ResourceUUID, "ResourceUUID should match")
	assert.Equal(t, "ThroughputPool", *poolMetadata.ResourceName, "ResourceName should match")
	assert.Equal(t, "gcp-deployment-throughput", *poolMetadata.DeploymentName, "DeploymentName should match")
	assert.Equal(t, metadata.VolumePool, poolMetadata.ResourceType, "ResourceType should be VolumePool")
	assert.Equal(t, "us-east-1", *poolMetadata.RegionName, "RegionName should match config")
	assert.Equal(t, "ThroughputAccount", *poolMetadata.AccountName, "AccountName should match")
	assert.Equal(t, int64(2048), *poolMetadata.SizeInBytes, "SizeInBytes should match")
}

func Test_GetPoolMetrics_MultiplePoolsWithDifferentThroughput(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-west-2"}

	pools := []*database.PoolMetricsData{
		{
			ID:             100,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 200,
				AccountName:     "Account1",
			},
			QuotaInBytes: 300,
		},
		{
			ID:             200,
			UUID:           "pool-uuid-2",
			Name:           "Pool2",
			SizeInBytes:    2000,
			DeploymentName: "gcp-deployment-2",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 350,
				AccountName:     "Account2",
			},
			QuotaInBytes: 800,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Test PoolMetadataMap contains both pools
	assert.Len(t, result.PoolMetadataMap, 2, "PoolMetadataMap should contain two entries")

	// Test first pool metadata
	pool1Metadata, exists := result.PoolMetadataMap[100]
	assert.True(t, exists, "Pool metadata should exist for pool ID 100")
	assert.Equal(t, 200.0, *pool1Metadata.Throughput, "Pool1 throughput should be 200.0")
	assert.Equal(t, int64(100), *pool1Metadata.ResourceID, "Pool1 ResourceID should be 100")

	// Test second pool metadata
	pool2Metadata, exists := result.PoolMetadataMap[200]
	assert.True(t, exists, "Pool metadata should exist for pool ID 200")
	assert.Equal(t, 350.0, *pool2Metadata.Throughput, "Pool2 throughput should be 350.0")
	assert.Equal(t, int64(200), *pool2Metadata.ResourceID, "Pool2 ResourceID should be 200")
}

func Test_GetPoolMetrics_ZeroThroughput(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-zero-throughput",
			Name:           "ZeroThroughputPool",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-zero",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 0,
				AccountName:     "ZeroAccount",
			},
			QuotaInBytes: 500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Test PoolMetadataMap with zero throughput
	poolMetadata, exists := result.PoolMetadataMap[1]
	assert.True(t, exists, "Pool metadata should exist for pool ID 1")
	assert.Equal(t, 0.0, *poolMetadata.Throughput, "Throughput should be 0.0")
	assert.Equal(t, int64(1), *poolMetadata.ResourceID, "ResourceID should be set")
}

// Test pool metadata includes throughput and resource ID
func Test_GetPoolMetrics_IncludesThroughputAndResourceID(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-west-2"}

	pools := []*database.PoolMetricsData{
		{
			ID:             42,
			UUID:           "pool-uuid-throughput",
			Name:           "ThroughputPool",
			SizeInBytes:    5000,
			DeploymentName: "throughput-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 500,
				AccountName:     "ThroughputAccount",
			},
			QuotaInBytes: 2500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Len(t, result.HydratedMetrics, 4)          // Should have 2 metrics: PoolAllocatedSize and AllocatedUsed
	assert.Len(t, result.HydratedMetricsDataModel, 4) // Should have 2 hydrated metrics

	// Test new PoolMetadataMap functionality
	assert.NotNil(t, result.PoolMetadataMap, "PoolMetadataMap should not be nil")
	assert.Len(t, result.PoolMetadataMap, 1, "PoolMetadataMap should contain one entry")

	// Verify the pool metadata contains throughput and resource ID
	poolMetadata, exists := result.PoolMetadataMap[42]
	assert.True(t, exists, "Pool metadata should exist for pool ID 42")
	assert.NotNil(t, poolMetadata.Throughput, "Pool throughput should be set")
	assert.Equal(t, 500.0, *poolMetadata.Throughput, "Pool throughput should match")
	assert.NotNil(t, poolMetadata.ResourceID, "Pool resource ID should be set")
	assert.Equal(t, int64(42), *poolMetadata.ResourceID, "Pool resource ID should match")

	// Verify other metadata fields
	assert.Equal(t, "pool-uuid-throughput", derefString(poolMetadata.ResourceUUID))
	assert.Equal(t, "ThroughputPool", derefString(poolMetadata.ResourceName))
	assert.Equal(t, "ThroughputAccount", derefString(poolMetadata.AccountName))
	assert.Equal(t, metadata.VolumePool, poolMetadata.ResourceType)

	// Check metric metadata also has throughput and resource ID
	assert.NotNil(t, result.HydratedMetrics[0].Metadata.Throughput)
	assert.Equal(t, 500.0, *result.HydratedMetrics[0].Metadata.Throughput)
	assert.NotNil(t, result.HydratedMetrics[0].Metadata.ResourceID)
	assert.Equal(t, int64(42), *result.HydratedMetrics[0].Metadata.ResourceID)
}

// Test assemblePoolMetadata function with throughput and resource ID
func Test_AssemblePoolMetadata_WithThroughputAndResourceID(t *testing.T) {
	config := &common.TelemetryConfig{RegionName: "eu-west-1"}

	pool := &database.PoolMetricsData{
		ID:             123,
		UUID:           "test-pool-uuid",
		Name:           "TestPool",
		SizeInBytes:    8192,
		DeploymentName: "test-deployment",
		ServiceLevel:   commonparams.ServiceLevelExtreme,
		APIAccessMode:  commonparams.ONTAPMode,
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 1000,
			AccountName:     "TestAccount",
		},
	}

	result := assemblePoolMetadata(pool, config)

	// Verify all metadata fields
	assert.Equal(t, "test-pool-uuid", derefString(result.ResourceUUID))
	assert.Equal(t, "TestPool", derefString(result.ResourceName))
	assert.Equal(t, "TestPool", derefString(result.ResourceDisplayName))
	assert.Equal(t, metadata.VolumePool, result.ResourceType)
	assert.Equal(t, int64(8192), derefInt64(result.SizeInBytes))
	assert.Equal(t, "eu-west-1", derefString(result.RegionName))
	assert.Equal(t, "TestAccount", derefString(result.AccountName))
	assert.Equal(t, "test-deployment", derefString(result.DeploymentName))

	// Verify new fields
	assert.NotNil(t, result.Throughput, "Throughput should be set")
	assert.Equal(t, 1000.0, *result.Throughput, "Throughput should match")
	assert.NotNil(t, result.ResourceID, "ResourceID should be set")
	assert.Equal(t, int64(123), *result.ResourceID, "ResourceID should match")
	assert.Equal(t, commonparams.ServiceLevelExtreme, derefString(result.ServiceLevel))
	assert.Equal(t, commonparams.ONTAPMode, derefString(result.APIAccessMode))
}

func Test_GetPoolMetrics_NilPoolAttributes(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	// Pool with nil PoolAttributes
	pools := []*database.PoolMetricsData{
		{
			ID:             123,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "test-deployment",
			PoolAttributes: nil, // This should trigger the skip condition
			QuotaInBytes:   500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should be empty since pool was skipped due to nil PoolAttributes
	assert.Len(t, result.HydratedMetrics, 0)
	assert.Len(t, result.HydratedMetricsDataModel, 0)
	assert.Len(t, result.PoolMetadataMap, 0)

	m.AssertExpectations(t)
}

// Test_GetPoolMetrics_RegionalHAPool tests the resource type mapping for regional HA pools
func Test_GetPoolMetrics_RegionalHAPool(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-central1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-regional-ha",
			Name:           "RegionalHAPool",
			SizeInBytes:    2000000,
			DeploymentName: "regional-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 250,
				IsRegionalHA:    true, // This should map to VolumePoolRegionalHA
				AccountName:     "RegionalAccount",
			},
			QuotaInBytes: 1500000,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify metrics were created
	assert.Len(t, result.HydratedMetrics, 4)
	assert.Len(t, result.HydratedMetricsDataModel, 4)
	assert.Len(t, result.PoolMetadataMap, 1)

	// Check that the resource type is correctly set to VolumePoolRegionalHA
	poolMetadata := result.PoolMetadataMap[1]
	assert.Equal(t, metadata.VolumePoolRegionalHA, poolMetadata.ResourceType)

	// Verify throughput is properly set
	assert.Equal(t, float64(250), *poolMetadata.Throughput)

	// Check hydrated metrics have correct resource type
	assert.Equal(t, string(metadata.VolumePoolRegionalHA), result.HydratedMetricsDataModel[0].ResourceType)
	assert.Equal(t, string(metadata.VolumePoolRegionalHA), result.HydratedMetricsDataModel[1].ResourceType)

	// Verify specific metric values
	assert.Equal(t, metadata.PoolAllocatedSize, result.HydratedMetrics[0].MeasuredType)
	assert.Equal(t, float64(2000000), result.HydratedMetrics[0].Quantity)
	assert.Equal(t, metadata.AllocatedUsed, result.HydratedMetrics[1].MeasuredType)
	assert.Equal(t, float64(1500000), result.HydratedMetrics[1].Quantity)

	m.AssertExpectations(t)
}

// Test_GetPoolMetrics_ZonalPool tests the resource type mapping for regular (zonal) pools
func Test_GetPoolMetrics_ZonalPool(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-west1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             2,
			UUID:           "pool-uuid-zonal",
			Name:           "ZonalPool",
			SizeInBytes:    1500000,
			DeploymentName: "zonal-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 150,
				IsRegionalHA:    false, // This should map to VolumePool
				AccountName:     "ZonalAccount",
			},
			QuotaInBytes: 900000,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify metrics were created
	assert.Len(t, result.HydratedMetrics, 4)
	assert.Len(t, result.HydratedMetricsDataModel, 4)
	assert.Len(t, result.PoolMetadataMap, 1)

	// Check that the resource type is correctly set to VolumePool (regular zonal)
	poolMetadata := result.PoolMetadataMap[2]
	assert.Equal(t, metadata.VolumePool, poolMetadata.ResourceType)

	// Verify throughput is properly set
	assert.Equal(t, float64(150), *poolMetadata.Throughput)

	// Check hydrated metrics have correct resource type
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[0].ResourceType)
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[1].ResourceType)

	m.AssertExpectations(t)
}

// Test_GetPoolMetrics_MixedPoolTypes tests both regional HA and zonal pools in the same call
func Test_GetPoolMetrics_MixedPoolTypes(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "europe-west1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-regional-mixed",
			Name:           "RegionalPool",
			SizeInBytes:    3000000,
			DeploymentName: "mixed-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 300,
				IsRegionalHA:    true,
				AccountName:     "MixedAccount1",
			},
			QuotaInBytes: 2000000,
		},
		{
			ID:             2,
			UUID:           "pool-uuid-zonal-mixed",
			Name:           "ZonalPool",
			SizeInBytes:    2000000,
			DeploymentName: "mixed-deployment-2",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 200,
				IsRegionalHA:    false,
				AccountName:     "MixedAccount2",
			},
			QuotaInBytes: 1200000,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return([]*database.AccountTelemetryData{}, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Verify metrics were created for both pools
	assert.Len(t, result.HydratedMetrics, 8)          // 2 metrics per pool
	assert.Len(t, result.HydratedMetricsDataModel, 8) // 2 hydrated metrics per pool
	assert.Len(t, result.PoolMetadataMap, 2)          // 2 pools

	// Check first pool (Regional HA)
	regionalPoolMetadata := result.PoolMetadataMap[1]
	assert.Equal(t, metadata.VolumePoolRegionalHA, regionalPoolMetadata.ResourceType)
	assert.Equal(t, float64(300), *regionalPoolMetadata.Throughput)

	// Check second pool (Zonal)
	zonalPoolMetadata := result.PoolMetadataMap[2]
	assert.Equal(t, metadata.VolumePool, zonalPoolMetadata.ResourceType)
	assert.Equal(t, float64(200), *zonalPoolMetadata.Throughput)

	// Verify the hydrated metrics have correct resource types
	// First two metrics should be for Regional HA pool
	assert.Equal(t, string(metadata.VolumePoolRegionalHA), result.HydratedMetricsDataModel[0].ResourceType)
	assert.Equal(t, string(metadata.VolumePoolRegionalHA), result.HydratedMetricsDataModel[1].ResourceType)
	assert.Equal(t, string(metadata.VolumePoolRegionalHA), result.HydratedMetricsDataModel[2].ResourceType)
	assert.Equal(t, string(metadata.VolumePoolRegionalHA), result.HydratedMetricsDataModel[3].ResourceType)

	// Last two metrics should be for Zonal pool
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[4].ResourceType)
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[5].ResourceType)
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[6].ResourceType)
	assert.Equal(t, string(metadata.VolumePool), result.HydratedMetricsDataModel[7].ResourceType)

	m.AssertExpectations(t)
}

// Test_AssemblePoolMetadata_RegionalHA tests assemblePoolMetadata function for regional HA pools
func Test_AssemblePoolMetadata_RegionalHA(t *testing.T) {
	pool := &database.PoolMetricsData{
		ID:             123,
		UUID:           "test-pool-uuid-regional",
		Name:           "TestRegionalPool",
		SizeInBytes:    5000000,
		DeploymentName: "test-deployment-regional",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 400,
			IsRegionalHA:    true,
			AccountName:     "TestRegionalAccount",
		},
	}

	config := &common.TelemetryConfig{
		RegionName: "asia-southeast1",
	}

	resourceMetadata := assemblePoolMetadata(pool, config)

	// Verify all fields are properly set
	assert.Equal(t, "test-pool-uuid-regional", derefString(resourceMetadata.ResourceUUID))
	assert.Equal(t, metadata.VolumePoolRegionalHA, resourceMetadata.ResourceType) // Should be Regional HA
	assert.Equal(t, int64(5000000), derefInt64(resourceMetadata.SizeInBytes))
	assert.Equal(t, "asia-southeast1", derefString(resourceMetadata.RegionName))
	assert.Equal(t, "TestRegionalPool", derefString(resourceMetadata.ResourceName))
	assert.Equal(t, "TestRegionalPool", derefString(resourceMetadata.ResourceDisplayName))
	assert.Equal(t, "TestRegionalAccount", derefString(resourceMetadata.AccountName))
	assert.Equal(t, "test-deployment-regional", derefString(resourceMetadata.DeploymentName))
	assert.Equal(t, float64(400), derefFloat64(resourceMetadata.Throughput))
	assert.Equal(t, int64(123), derefInt64(resourceMetadata.ResourceID))
}

// Test_AssemblePoolMetadata_Zonal tests assemblePoolMetadata function for zonal pools
func Test_AssemblePoolMetadata_Zonal(t *testing.T) {
	pool := &database.PoolMetricsData{
		ID:             456,
		UUID:           "test-pool-uuid-zonal",
		Name:           "TestZonalPool",
		SizeInBytes:    3000000,
		DeploymentName: "test-deployment-zonal",
		PoolAttributes: &datamodel.PoolAttributes{
			ThroughputMibps: 100,
			IsRegionalHA:    false,
			AccountName:     "TestZonalAccount",
		},
	}

	config := &common.TelemetryConfig{
		RegionName: "us-east1",
	}

	resourceMetadata := assemblePoolMetadata(pool, config)

	// Verify all fields are properly set
	assert.Equal(t, "test-pool-uuid-zonal", derefString(resourceMetadata.ResourceUUID))
	assert.Equal(t, metadata.VolumePool, resourceMetadata.ResourceType) // Should be regular VolumePool
	assert.Equal(t, int64(3000000), derefInt64(resourceMetadata.SizeInBytes))
	assert.Equal(t, "us-east1", derefString(resourceMetadata.RegionName))
	assert.Equal(t, "TestZonalPool", derefString(resourceMetadata.ResourceName))
	assert.Equal(t, "TestZonalPool", derefString(resourceMetadata.ResourceDisplayName))
	assert.Equal(t, "TestZonalAccount", derefString(resourceMetadata.AccountName))
	assert.Equal(t, "test-deployment-zonal", derefString(resourceMetadata.DeploymentName))
	assert.Equal(t, float64(100), derefFloat64(resourceMetadata.Throughput))
	assert.Equal(t, int64(456), derefInt64(resourceMetadata.ResourceID))
}

// Test_AssemblePoolMetadata_ThroughputEdgeCases tests throughput handling edge cases
func Test_AssemblePoolMetadata_ThroughputEdgeCases(t *testing.T) {
	testCases := []struct {
		name            string
		throughputMibps int64
		expectedValue   float64
	}{
		{"Zero Throughput", 0, 0.0},
		{"Small Throughput", 10, 10.0},
		{"Large Throughput", 10000, 10000.0},
		{"Negative Throughput", -50, -50.0}, // Edge case that might occur
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pool := &database.PoolMetricsData{
				ID:             1,
				UUID:           "test-pool-uuid",
				Name:           "TestPool",
				SizeInBytes:    1000000,
				DeploymentName: "test-deployment",
				PoolAttributes: &datamodel.PoolAttributes{
					ThroughputMibps: tc.throughputMibps,
					IsRegionalHA:    false,
					AccountName:     "TestAccount",
				},
			}

			config := &common.TelemetryConfig{RegionName: "us-west1"}

			resourceMetadata := assemblePoolMetadata(pool, config)

			assert.Equal(t, tc.expectedValue, derefFloat64(resourceMetadata.Throughput),
				"Throughput should be correctly converted from int64 to float64")
		})
	}
}

// Test_GetPoolMetrics_SkipsDisabledAccounts tests that pools with HYPERSCALERDISABLED accounts are skipped
func Test_GetPoolMetrics_SkipsDisabledAccounts(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 100,
				Iops:            1000,
				AccountName:     "DisabledAccount",
			},
			QuotaInBytes: 500,
		},
		{
			ID:             2,
			UUID:           "pool-uuid-2",
			Name:           "Pool2",
			SizeInBytes:    2000,
			DeploymentName: "gcp-deployment-2",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 200,
				Iops:            2000,
				AccountName:     "EnabledAccount",
			},
			QuotaInBytes: 1000,
		},
	}

	accounts := []*database.AccountTelemetryData{
		{
			ID:    1,
			Name:  "DisabledAccount",
			State: datamodel.AccountStateHyperscalerDisabled,
		},
		{
			ID:    2,
			Name:  "EnabledAccount",
			State: "ENABLED",
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.MatchedBy(func(p *utils.Pagination) bool {
		return p != nil && p.Offset == 0 && p.Limit == 1000
	})).Return(accounts, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should only have metrics for Pool2 (EnabledAccount), not Pool1 (DisabledAccount)
	// Each pool has 4 metrics: PoolAllocatedSize, AllocatedUsed, ThroughputMibps, Iops
	assert.Len(t, result.HydratedMetrics, 4, "Should have 4 metrics for enabled account pool only")
	assert.Len(t, result.HydratedMetricsDataModel, 4, "Should have 4 hydrated metrics for enabled account pool only")
	assert.Len(t, result.PoolMetadataMap, 1, "Should have metadata for one pool only")

	// Verify all metrics belong to Pool2
	for _, metric := range result.HydratedMetrics {
		assert.Equal(t, "Pool2", derefString(metric.Metadata.ResourceName))
		assert.Equal(t, "EnabledAccount", derefString(metric.Metadata.AccountName))
	}

	m.AssertExpectations(t)
}

// Test_GetPoolMetrics_AccountFetchFailure tests graceful degradation when account fetch fails
func Test_GetPoolMetrics_AccountFetchFailure(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 100,
				Iops:            1000,
				AccountName:     "Account1",
			},
			QuotaInBytes: 500,
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return(nil, assert.AnError)

	// Should still process pools even if account fetch fails (graceful degradation)
	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should have metrics for all pools since account filtering failed
	assert.Len(t, result.HydratedMetrics, 4)
	assert.Len(t, result.HydratedMetricsDataModel, 4)

	m.AssertExpectations(t)
}

// Test_GetPoolMetrics_AccountPagination tests account fetching with pagination
func Test_GetPoolMetrics_AccountPagination(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 100,
				Iops:            1000,
				AccountName:     "Account1",
			},
			QuotaInBytes: 500,
		},
	}

	// First page of accounts - return exactly 1000 to trigger second page call
	accountsPage1 := make([]*database.AccountTelemetryData, 1000)
	for i := 0; i < 1000; i++ {
		if i == 0 {
			accountsPage1[i] = &database.AccountTelemetryData{
				ID:    1,
				Name:  "Account1",
				State: "ENABLED",
			}
		} else {
			accountsPage1[i] = &database.AccountTelemetryData{
				ID:    int64(i + 1),
				Name:  fmt.Sprintf("Account%d", i+1),
				State: "ENABLED",
			}
		}
	}

	// Second page of accounts (empty, indicating end)
	accountsPage2 := []*database.AccountTelemetryData{}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.MatchedBy(func(p *utils.Pagination) bool {
		return p != nil && p.Offset == 0 && p.Limit == 1000
	})).Return(accountsPage1, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.MatchedBy(func(p *utils.Pagination) bool {
		return p != nil && p.Offset == 1000 && p.Limit == 1000
	})).Return(accountsPage2, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should have metrics since Account1 is enabled
	assert.Len(t, result.HydratedMetrics, 4)
	assert.Len(t, result.HydratedMetricsDataModel, 4)

	m.AssertExpectations(t)
}

// Test_GetPoolMetrics_AccountNotInMap tests that pools with accounts not in the map are still processed
func Test_GetPoolMetrics_AccountNotInMap(t *testing.T) {
	m := new(mockStorage)
	ctx := context.Background()
	config := &common.TelemetryConfig{RegionName: "us-east-1"}

	pools := []*database.PoolMetricsData{
		{
			ID:             1,
			UUID:           "pool-uuid-1",
			Name:           "Pool1",
			SizeInBytes:    1000,
			DeploymentName: "gcp-deployment-1",
			PoolAttributes: &datamodel.PoolAttributes{
				ThroughputMibps: 100,
				Iops:            1000,
				AccountName:     "UnknownAccount",
			},
			QuotaInBytes: 500,
		},
	}

	accounts := []*database.AccountTelemetryData{
		{
			ID:    1,
			Name:  "OtherAccount",
			State: "ENABLED",
		},
	}

	m.On("ListPoolsForMetrics", mock.Anything).Return(pools, nil)
	m.On("ListAccountsForTelemetry", mock.Anything, mock.Anything).Return(accounts, nil)

	result, err := GetPoolMetrics(ctx, m, config, time.Now())
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Should still process pools even if account is not in the map (unknown accounts are allowed)
	assert.Len(t, result.HydratedMetrics, 4)
	assert.Len(t, result.HydratedMetricsDataModel, 4)

	m.AssertExpectations(t)
}

// Test_setupPoolMetricForBillingOnly tests that setupPoolMetricForBillingOnly only appends to billing slice
func Test_setupPoolMetricForBillingOnly(t *testing.T) {
	// Setup test data
	timestamp := time.Now()
	poolMetadata := metadata.ResourceMetadata{}
	resourceName := "test-pool-billing-only"
	regionName := "us-west-2"
	deploymentName := "test-deployment"
	accountName := "test-account"

	poolMetadata.SetResourceName(resourceName)
	poolMetadata.SetRegionName(regionName)
	poolMetadata.SetDeploymentName(deploymentName)
	poolMetadata.SetResourceType(metadata.VolumePool)

	// Initialize slices
	var performanceMetrics []entity.HydratedMetric
	var billingMetrics []datamodel2.HydratedMetrics

	// Call setupPoolMetricForBillingOnly
	setupPoolMetricForBillingOnly(&billingMetrics, timestamp, poolMetadata,
		metadata.PoolHotTierProvisionedSize, 5368709120.0, accountName)

	// Verify that performance metrics slice is still empty
	assert.Len(t, performanceMetrics, 0, "Performance metrics should not be populated by setupPoolMetricForBillingOnly")

	// Verify that billing metrics slice has one entry
	assert.Len(t, billingMetrics, 1, "Billing metrics should have exactly one entry")

	// Verify the billing metric properties
	billingMetric := billingMetrics[0]
	assert.Equal(t, string(metadata.PoolHotTierProvisionedSize), billingMetric.MeasuredType)
	assert.Equal(t, string(metadata.VolumePool), billingMetric.ResourceType)
	assert.Equal(t, accountName, billingMetric.ConsumerID)
	assert.Equal(t, resourceName, billingMetric.ResourceName)
	assert.Equal(t, regionName, billingMetric.Location)
	assert.Equal(t, 5368709120.0, billingMetric.Quantity)
}

// Test_setupPoolMetricForBillingOnly_NilMetadata tests that setupPoolMetricForBillingOnly handles nil metadata correctly
func Test_setupPoolMetricForBillingOnly_NilMetadata(t *testing.T) {
	timestamp := time.Now()
	poolMetadata := metadata.ResourceMetadata{}
	// Only set RegionName, leave ResourceName and DeploymentName nil
	regionName := "us-west-2"
	poolMetadata.SetRegionName(regionName)
	poolMetadata.SetResourceType(metadata.VolumePool)

	var billingMetrics []datamodel2.HydratedMetrics

	// Call setupPoolMetricForBillingOnly with incomplete metadata
	setupPoolMetricForBillingOnly(&billingMetrics, timestamp, poolMetadata,
		metadata.PoolHotTierProvisionedSize, 1000.0, "test-account")

	// Verify that no metric was added due to validation failure
	assert.Len(t, billingMetrics, 0, "Billing metrics should be empty when metadata validation fails")
}
