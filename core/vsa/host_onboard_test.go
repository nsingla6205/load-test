package vsa

import (
	"context"
	"errors"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
)

func mockHostOnboardCluster() *ontaprest.Cluster {
	clusterUUID := strfmt.UUID("11111111-1111-1111-1111-111111111111")
	clusterName := "vsim-cluster"
	clusterVersion := "9.15.1"
	generation := int64(9)
	major := int64(15)
	minor := int64(1)
	return &ontaprest.Cluster{
		Cluster: models.Cluster{
			UUID: &clusterUUID,
			Name: &clusterName,
			Version: &models.ClusterInlineVersion{
				Full:       &clusterVersion,
				Generation: &generation,
				Major:      &major,
				Minor:      &minor,
			},
		},
	}
}

func mockHostOnboardAggregates(aggregates map[string]int64) []*ontaprest.Aggregate {
	result := make([]*ontaprest.Aggregate, 0, len(aggregates))
	for name, size := range aggregates {
		aggregateName := name
		aggregateSize := size
		result = append(result, &ontaprest.Aggregate{
			Aggregate: models.Aggregate{
				Name: &aggregateName,
				Space: &models.AggregateInlineSpace{
					BlockStorage: &models.AggregateInlineSpaceInlineBlockStorage{
						Size: &aggregateSize,
					},
				},
			},
		})
	}
	return result
}

func TestHostNodeAggregateName(t *testing.T) {
	assert.Equal(t, "node_1_nfsaas_aggr", hostNodeAggregateName("node-1"))
	assert.Equal(t, "node_a_nfsaas_aggr", hostNodeAggregateName("node-a"))
}

func TestDiscoverHostSystemInfo(t *testing.T) {
	t.Run("returns cluster get error", func(t *testing.T) {
		mockREST := ontaprest.NewMockRESTClient(t)
		mockCluster := ontaprest.NewMockClusterClient(t)
		mockREST.On("Cluster").Return(mockCluster)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).
			Return(nil, errors.New("cluster get failed"))

		host := &datamodel.Host{}
		err := DiscoverHostSystemInfo(context.Background(), host, mockREST)
		require.Error(t, err)
	})

	t.Run("populates cluster metadata", func(t *testing.T) {
		mockREST := ontaprest.NewMockRESTClient(t)
		mockCluster := ontaprest.NewMockClusterClient(t)
		mockREST.On("Cluster").Return(mockCluster)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(mockHostOnboardCluster(), nil)

		host := &datamodel.Host{}
		err := DiscoverHostSystemInfo(context.Background(), host, mockREST)
		require.NoError(t, err)
		assert.Equal(t, "11111111-1111-1111-1111-111111111111", host.ExternalUUID)
		assert.Equal(t, "vsim-cluster", host.ExternalName)
		require.NotNil(t, host.Attributes)
		assert.Equal(t, "9.15.1", host.Attributes.Version)
		assert.Equal(t, 9, host.Attributes.VersionGeneration)
		assert.Equal(t, 15, host.Attributes.VersionMajor)
		assert.Equal(t, 1, host.Attributes.VersionMinor)
		assert.Empty(t, host.Attributes.Capabilities)
	})
}

func TestValidateHostAggregates(t *testing.T) {
	t.Run("returns error when node lookup fails", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockProvider.On("GetNodesWithClient", mockREST).Return(nil, errors.New("nodes failed"))

		host := &datamodel.Host{}
		_, err := ValidateHostAggregates(mockProvider, host, mockREST)
		require.Error(t, err)
	})

	t.Run("returns error when node aggregate is missing", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockStorage := ontaprest.NewMockStorageClient(t)
		mockREST.On("Storage").Return(mockStorage)

		mockProvider.On("GetNodesWithClient", mockREST).Return([]*Node{{Name: "node-1"}}, nil)
		mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
			_ = callback(nil)
		}).Return(nil)

		host := &datamodel.Host{}
		_, err := ValidateHostAggregates(mockProvider, host, mockREST)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `aggregate for node "node-1" not found`)
	})

	t.Run("sets total capacity from node aggregates", func(t *testing.T) {
		mockProvider := NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockStorage := ontaprest.NewMockStorageClient(t)
		mockREST.On("Storage").Return(mockStorage)

		mockProvider.On("GetNodesWithClient", mockREST).Return([]*Node{
			{Name: "node-1"},
			{Name: "node-2"},
			{Name: ""},
		}, nil)
		mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
			_ = callback(mockHostOnboardAggregates(map[string]int64{
				"node_1_nfsaas_aggr": 100,
				"node_2_nfsaas_aggr": 200,
			}))
		}).Return(nil)

		host := &datamodel.Host{}
		nodes, err := ValidateHostAggregates(mockProvider, host, mockREST)
		require.NoError(t, err)
		assert.Equal(t, int64(300), host.Capacity)
		assert.Len(t, nodes, 3)
	})
}
