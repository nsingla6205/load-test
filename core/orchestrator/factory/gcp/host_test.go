package gcp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontaprest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	utilserrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func validCreateHostParams() *commonparams.CreateHostParams {
	protocol := datamodel.ExternalClusterProtocolInsecureHTTPS
	ip := "10.0.0.1"
	return &commonparams.CreateHostParams{
		Name:      "host-01",
		Username:  "admin",
		Password:  log.Secret("secret"),
		IPAddress: &ip,
		Protocol:  &protocol,
	}
}

func stubOntapProviderForHost(
	t *testing.T,
	setup func(mockProvider *vsa.MockProvider, mockREST *ontaprest.MockRESTClient, mockCluster *ontaprest.MockClusterClient, mockStorage *ontaprest.MockStorageClient),
) {
	t.Helper()

	original := getOntapProviderForHost
	t.Cleanup(func() { getOntapProviderForHost = original })

	mockProvider := vsa.NewMockProvider(t)
	mockREST := ontaprest.NewMockRESTClient(t)
	mockCluster := ontaprest.NewMockClusterClient(t)
	mockStorage := ontaprest.NewMockStorageClient(t)

	setup(mockProvider, mockREST, mockCluster, mockStorage)

	getOntapProviderForHost = func(context.Context, vsa.ProviderDetails) vsa.Provider {
		return mockProvider
	}
}

func mockClusterGetResponse() *ontaprest.Cluster {
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

func mockNodeAggregates(aggregates map[string]int64) []*ontaprest.Aggregate {
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

func setupSuccessfulOntapDiscovery(
	mockProvider *vsa.MockProvider,
	mockREST *ontaprest.MockRESTClient,
	mockCluster *ontaprest.MockClusterClient,
	mockStorage *ontaprest.MockStorageClient,
) {
	mockProvider.On("CreateRESTClient").Return(mockREST, nil)
	mockREST.On("Cluster").Return(mockCluster)
	mockREST.On("Storage").Return(mockStorage)
	mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(mockClusterGetResponse(), nil)
	mockProvider.On("GetNodesWithClient", mockREST).Return([]*vsa.Node{
		{Name: "node-1"},
		{Name: "node-2"},
	}, nil)
	mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
		_ = callback(mockNodeAggregates(map[string]int64{
			"node_1_nfsaas_aggr": 34158833664,
			"node_2_nfsaas_aggr": 34158833664,
		}))
	}).Return(nil)
}

func TestCreateHost_ValidationErrors(t *testing.T) {
	orch := &GCPOrchestrator{storage: database.NewMockStorage(t)}

	tests := []struct {
		name   string
		params *commonparams.CreateHostParams
	}{
		{name: "nil params", params: nil},
		{
			name: "missing name",
			params: &commonparams.CreateHostParams{
				Username: "admin",
				Password: log.Secret("secret"),
			},
		},
		{
			name: "missing username",
			params: &commonparams.CreateHostParams{
				Name:     "host-01",
				Password: log.Secret("secret"),
			},
		},
		{
			name: "missing password",
			params: &commonparams.CreateHostParams{
				Name:     "host-01",
				Username: "admin",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := orch.CreateOntapHost(context.Background(), tc.params)
			require.Error(t, err)
			assert.True(t, utilserrors.IsBadRequestErr(err))
		})
	}
}

func TestCreateHost_InvalidProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	protocol := "NFS"
	params := validCreateHostParams()
	params.Protocol = &protocol

	_, err := orch.CreateOntapHost(context.Background(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	mockStorage.AssertNotCalled(t, "CountHostsWithIPAddress", mock.Anything, mock.Anything, mock.Anything)
}

func TestCreateHost_EncryptPasswordError(t *testing.T) {
	originalEncrypt := utils.EncryptPassword
	t.Cleanup(func() { utils.EncryptPassword = originalEncrypt })
	utils.EncryptPassword = func(log.Secret) (*string, error) {
		return nil, errors.New("encrypt failed")
	}

	orch := &GCPOrchestrator{storage: database.NewMockStorage(t)}
	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt failed")
}

func TestCreateHost_CountHostsWithIPAddressError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), errors.New("db read failed"))

	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed checking host ip uniqueness")
}

func TestCreateHost_IPAlreadyAssigned(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(1), nil)

	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
	assert.Contains(t, err.Error(), "IP address already assigned")
}

func TestCreateHost_UsesNameAsIPAddressWhenUnset(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	params := validCreateHostParams()
	params.IPAddress = nil
	params.Name = "10.0.0.50"

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.50", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapDiscovery)
	mockStorage.On("CreateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		return host.IPAddress == "10.0.0.50"
	})).Return(&datamodel.Host{Name: "10.0.0.50"}, nil)

	_, err := orch.CreateOntapHost(context.Background(), params)
	require.NoError(t, err)
}

func TestCreateHost_CreateRESTClientError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, func(mockProvider *vsa.MockProvider, _ *ontaprest.MockRESTClient, _ *ontaprest.MockClusterClient, _ *ontaprest.MockStorageClient) {
		mockProvider.On("CreateRESTClient").Return(nil, errors.New("connection refused"))
	})

	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unable to connect to host")
}

func TestCreateHost_ClusterGetError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, func(mockProvider *vsa.MockProvider, mockREST *ontaprest.MockRESTClient, mockCluster *ontaprest.MockClusterClient, _ *ontaprest.MockStorageClient) {
		mockProvider.On("CreateRESTClient").Return(mockREST, nil)
		mockREST.On("Cluster").Return(mockCluster)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(nil, errors.New("cluster get failed"))
	})

	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unable to fetch cluster details")
}

func TestCreateHost_MissingNodeAggregate(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, func(mockProvider *vsa.MockProvider, mockREST *ontaprest.MockRESTClient, mockCluster *ontaprest.MockClusterClient, mockStorageClient *ontaprest.MockStorageClient) {
		mockProvider.On("CreateRESTClient").Return(mockREST, nil)
		mockREST.On("Cluster").Return(mockCluster)
		mockREST.On("Storage").Return(mockStorageClient)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(mockClusterGetResponse(), nil)
		mockProvider.On("GetNodesWithClient", mockREST).Return([]*vsa.Node{{Name: "node-1"}}, nil)
		mockStorageClient.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
			_ = callback(nil)
		}).Return(nil)
	})

	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unable to validate host nodes")
}

func TestCreateHost_StorageCreateError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapDiscovery)
	mockStorage.On("CreateHost", mock.Anything, mock.Anything).Return(nil, errors.New("db insert failed"))

	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create host")
}

func TestCreateHost_DuplicateIPOnInsert(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapDiscovery)
	mockStorage.On("CreateHost", mock.Anything, mock.Anything).Return(nil, vsaerrors.NewVCPError(
		vsaerrors.ErrResourceStateConflictError,
		utilserrors.NewConflictErr("IP address already assigned to host"),
	))

	_, err := orch.CreateOntapHost(context.Background(), validCreateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsConflictErr(err))
}

func TestCreateHost_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}
	params := validCreateHostParams()
	label := "type=VSIM"
	params.Label = &label
	maintenanceMode := true
	params.MaintenanceMode = &maintenanceMode

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapDiscovery)
	mockStorage.On("CreateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		if host.Name != "host-01" || host.Username != "admin" || host.IPAddress != "10.0.0.1" {
			return false
		}
		if host.State != datamodel.LifeCycleStateCreated || host.StateDetails != datamodel.LifeCycleStateCreatedDetails {
			return false
		}
		if host.ExternalUUID != "11111111-1111-1111-1111-111111111111" || host.ExternalName != "vsim-cluster" {
			return false
		}
		if host.Capacity != 68317667328 {
			return false
		}
		if host.Attributes == nil || !host.Attributes.MaintenanceMode || host.Attributes.Version != "9.15.1" {
			return false
		}
		if host.Password == "" || host.Password == "secret" {
			return false
		}
		decrypted, err := utils.DecryptPassword(log.Secret(host.Password))
		return err == nil && decrypted != nil && *decrypted == "secret"
	})).Return(&datamodel.Host{Name: "host-01"}, nil)

	got, err := orch.CreateOntapHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, int64(68317667328), got.Capacity)
}

func TestUpdateHostWithSystemInfo(t *testing.T) {
	t.Run("returns error when cluster get fails", func(t *testing.T) {
		mockREST := ontaprest.NewMockRESTClient(t)
		mockCluster := ontaprest.NewMockClusterClient(t)
		mockREST.On("Cluster").Return(mockCluster)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(nil, errors.New("cluster get failed"))

		host := &datamodel.Host{}
		err := updateHostWithSystemInfo(context.Background(), host, mockREST)
		require.Error(t, err)
	})

	t.Run("populates cluster metadata and version tuple", func(t *testing.T) {
		mockREST := ontaprest.NewMockRESTClient(t)
		mockCluster := ontaprest.NewMockClusterClient(t)
		mockREST.On("Cluster").Return(mockCluster)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(mockClusterGetResponse(), nil)

		host := &datamodel.Host{}
		err := updateHostWithSystemInfo(context.Background(), host, mockREST)
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

func TestValidateAggregatesForNewHost(t *testing.T) {
	t.Run("returns error when node lookup fails", func(t *testing.T) {
		mockProvider := vsa.NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockProvider.On("GetNodesWithClient", mockREST).Return(nil, errors.New("nodes failed"))

		host := &datamodel.Host{}
		err := validateAggregatesForNewHost(mockProvider, host, mockREST)
		require.Error(t, err)
	})

	t.Run("returns error when node aggregate is missing", func(t *testing.T) {
		mockProvider := vsa.NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockStorage := ontaprest.NewMockStorageClient(t)
		mockREST.On("Storage").Return(mockStorage)

		mockProvider.On("GetNodesWithClient", mockREST).Return([]*vsa.Node{{Name: "node-1"}}, nil)
		mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
			_ = callback(nil)
		}).Return(nil)

		host := &datamodel.Host{}
		err := validateAggregatesForNewHost(mockProvider, host, mockREST)
		require.Error(t, err)
		assert.Contains(t, err.Error(), `aggregate for node "node-1" not found`)
	})

	t.Run("sets total capacity from node aggregates", func(t *testing.T) {
		mockProvider := vsa.NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockStorage := ontaprest.NewMockStorageClient(t)
		mockREST.On("Storage").Return(mockStorage)

		mockProvider.On("GetNodesWithClient", mockREST).Return([]*vsa.Node{
			{Name: "node-1"},
			{Name: "node-2"},
		}, nil)
		mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
			_ = callback(mockNodeAggregates(map[string]int64{
				"node_1_nfsaas_aggr": 100,
				"node_2_nfsaas_aggr": 200,
			}))
		}).Return(nil)

		host := &datamodel.Host{}
		err := validateAggregatesForNewHost(mockProvider, host, mockREST)
		require.NoError(t, err)
		assert.Equal(t, int64(300), host.Capacity)
	})
}

func TestGetNodeAggregateName(t *testing.T) {
	assert.Equal(t, "node_1_nfsaas_aggr", getNodeAggregateName("node-1"))
	assert.Equal(t, "node_a_nfsaas_aggr", getNodeAggregateName("node-a"))
}

func TestAggregateBlockStorageSize(t *testing.T) {
	assert.Equal(t, int64(0), aggregateBlockStorageSize(nil))

	size := int64(1024)
	aggregate := &ontaprest.Aggregate{
		Aggregate: models.Aggregate{
			Space: &models.AggregateInlineSpace{
				BlockStorage: &models.AggregateInlineSpaceInlineBlockStorage{
					Size: &size,
				},
			},
		},
	}
	assert.Equal(t, int64(1024), aggregateBlockStorageSize(aggregate))
}

func TestProviderForHost_InsecureSkipVerify(t *testing.T) {
	original := getOntapProviderForHost
	t.Cleanup(func() { getOntapProviderForHost = original })

	var captured vsa.ProviderDetails
	getOntapProviderForHost = func(_ context.Context, details vsa.ProviderDetails) vsa.Provider {
		captured = details
		return vsa.NewMockProvider(t)
	}

	params := &commonparams.CreateHostParams{
		Username: "admin",
		Password: log.Secret("secret"),
	}
	_ = providerForHost(context.Background(), params, "10.0.0.1", datamodel.ExternalClusterProtocolInsecureHTTPS)

	assert.True(t, captured.InsecureSkipVerify)
	assert.Equal(t, "10.0.0.1", captured.IPAddress)
	assert.Equal(t, "secret", captured.Password)
}

func TestAggregatesByNameWithClient(t *testing.T) {
	mockREST := ontaprest.NewMockRESTClient(t)
	mockStorage := ontaprest.NewMockStorageClient(t)
	mockREST.On("Storage").Return(mockStorage)
	mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		callback := args.Get(1).(ontaprest.UserCallbackFunc[[]*ontaprest.Aggregate])
		_ = callback(mockNodeAggregates(map[string]int64{"aggr1": 1}))
	}).Return(nil)

	byName, err := aggregatesByNameWithClient(mockREST)
	require.NoError(t, err)
	assert.Contains(t, byName, "aggr1")
}

func TestAggregatesByNameWithClient_Error(t *testing.T) {
	mockREST := ontaprest.NewMockRESTClient(t)
	mockStorage := ontaprest.NewMockStorageClient(t)
	mockREST.On("Storage").Return(mockStorage)
	mockStorage.On("AggregateCollectionGet", mock.Anything, mock.Anything).
		Return(fmt.Errorf("aggregate collection failed"))

	_, err := aggregatesByNameWithClient(mockREST)
	require.Error(t, err)
}
