package activities

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func TestUsePrivateCLIVolumeCreate_OnlySixHAPairActiveActive(t *testing.T) {
	defaultCount := int32(common.SixHAPairActiveActiveDefaultConstituentCount())
	customCount := int32(8)
	tests := []struct {
		name     string
		volume   *datamodel.Volume
		expected bool
	}{
		{
			name: "six HA pair active-active default constituent count",
			volume: volumeWithConstituentCount(&datamodel.Pool{
				LargeCapacity: true,
				PoolAttributes: &datamodel.PoolAttributes{
					IsTrueZonal: true,
					LvHAPairs:   common.LvHAPairsNodeCount12,
				},
			}, &defaultCount),
			expected: true,
		},
		{
			name: "six HA pair active-active custom constituent count",
			volume: volumeWithConstituentCount(&datamodel.Pool{
				LargeCapacity: true,
				PoolAttributes: &datamodel.PoolAttributes{
					IsTrueZonal: true,
					LvHAPairs:   common.LvHAPairsNodeCount12,
				},
			}, &customCount),
		},
		{
			name: "twelve HA pair active-active",
			volume: volumeWithPool(&datamodel.Pool{
				LargeCapacity: true,
				PoolAttributes: &datamodel.PoolAttributes{
					IsTrueZonal: true,
					LvHAPairs:   common.LvHAPairsNodeCount24,
				},
			}),
		},
		{
			name: "one HA pair active-active",
			volume: volumeWithPool(&datamodel.Pool{
				LargeCapacity: true,
				PoolAttributes: &datamodel.PoolAttributes{
					IsTrueZonal: true,
					LvHAPairs:   1,
				},
			}),
		},
		{
			name: "six HA pair non true-zonal",
			volume: volumeWithPool(&datamodel.Pool{
				LargeCapacity:  true,
				PoolAttributes: &datamodel.PoolAttributes{LvHAPairs: common.LvHAPairsNodeCount12},
			}),
		},
		{
			name:   "nil volume",
			volume: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, usePrivateCLIVolumeCreate(tt.volume))
		})
	}
}

func volumeWithPool(pool *datamodel.Pool) *datamodel.Volume {
	return volumeWithConstituentCount(pool, nil)
}

func volumeWithConstituentCount(pool *datamodel.Pool, constituentCount *int32) *datamodel.Volume {
	return &datamodel.Volume{
		Pool: pool,
		LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
			LargeCapacity:               true,
			LargeVolumeConstituentCount: constituentCount,
		},
	}
}
