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
	efficiencyFactor := 1.4
	params.EfficiencyFactor = &efficiencyFactor

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
		if host.EfficiencyFactor != efficiencyFactor {
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

func TestCreateHost_DefaultEfficiencyFactor(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}
	params := validCreateHostParams()

	mockStorage.On("CountHostsWithIPAddress", mock.Anything, "10.0.0.1", false).
		Return(int64(0), nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapDiscovery)
	mockStorage.On("CreateHost", mock.Anything, mock.MatchedBy(func(host *datamodel.Host) bool {
		return host.EfficiencyFactor == datamodel.DefaultHostEfficiencyFactor
	})).Return(&datamodel.Host{Name: "host-01", EfficiencyFactor: datamodel.DefaultHostEfficiencyFactor}, nil)

	got, err := orch.CreateOntapHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, datamodel.DefaultHostEfficiencyFactor, got.EfficiencyFactor)
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

func existingStoredHost() *datamodel.Host {
	protocol := datamodel.ExternalClusterProtocolInsecureHTTPS
	port := 443
	encrypted, err := utils.EncryptPassword(log.Secret("stored-secret"))
	if err != nil {
		panic(err)
	}
	return &datamodel.Host{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "host-uuid-123",
		},
		Name:      "host-01",
		Username:  "admin",
		Password:  *encrypted,
		Protocol:  protocol,
		Port:      &port,
		IPAddress: "10.0.0.1",
		Attributes: &datamodel.HostAttributes{
			MaintenanceMode: false,
			PublicAccess:    false,
		},
	}
}

func validUpdateHostParams() *commonparams.UpdateHostParams {
	return &commonparams.UpdateHostParams{
		HostUUID: "host-uuid-123",
		CreateHostParams: commonparams.CreateHostParams{
			Name:     "host-01",
			Username: "admin",
			Password: log.Secret("new-secret"),
		},
	}
}

func setupSuccessfulOntapClusterGet(
	mockProvider *vsa.MockProvider,
	mockREST *ontaprest.MockRESTClient,
	mockCluster *ontaprest.MockClusterClient,
	_ *ontaprest.MockStorageClient,
) {
	mockProvider.On("CreateRESTClient").Return(mockREST, nil)
	mockREST.On("Cluster").Return(mockCluster)
	mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(mockClusterGetResponse(), nil)
}

func TestListHosts_DelegatesToStorage(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	hosts := []*datamodel.Host{{Name: "host-01"}}
	mockStorage.On("ListHosts", mock.Anything).Return(hosts, nil)

	got, err := orch.ListHosts(context.Background())
	require.NoError(t, err)
	assert.Equal(t, hosts, got)
}

func TestGetHost_DelegatesToStorage(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	got, err := orch.GetHost(context.Background(), "host-uuid-123")
	require.NoError(t, err)
	assert.Equal(t, host, got)
}

func TestGetHost_PropagatesStorageError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("GetHost", mock.Anything, "missing-id").
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.New("not found")))

	_, err := orch.GetHost(context.Background(), "missing-id")
	require.Error(t, err)
}

func TestDeleteHost_Success(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	deleted := *host
	deleted.State = datamodel.LifeCycleStateDeleted

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	mockStorage.On("DeleteHost", mock.Anything, host).Return(&deleted, nil)

	got, err := orch.DeleteHost(context.Background(), "host-uuid-123")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, datamodel.LifeCycleStateDeleted, got.State)
}

func TestDeleteHost_GetHostError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("GetHost", mock.Anything, "missing-id").
		Return(nil, errors.New("not found"))

	_, err := orch.DeleteHost(context.Background(), "missing-id")
	require.Error(t, err)
	mockStorage.AssertNotCalled(t, "DeleteHost", mock.Anything, mock.Anything)
}

func TestDeleteHost_DeleteError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	mockStorage.On("DeleteHost", mock.Anything, host).Return(nil, errors.New("delete failed"))

	_, err := orch.DeleteHost(context.Background(), "host-uuid-123")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "delete failed")
}

func TestUpdateHost_HostNotFound(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").
		Return(nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.New("not found")))

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
}

func TestUpdateHost_InvalidProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	protocol := "NFS"
	params := validUpdateHostParams()
	params.Protocol = &protocol

	_, err := orch.UpdateHost(context.Background(), params)
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
}

func TestUpdateHost_EncryptPasswordError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	originalEncrypt := utils.EncryptPassword
	t.Cleanup(func() { utils.EncryptPassword = originalEncrypt })
	utils.EncryptPassword = func(log.Secret) (*string, error) {
		return nil, errors.New("encrypt failed")
	}

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt failed")
}

func TestUpdateHost_CreateRESTClientError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, func(mockProvider *vsa.MockProvider, _ *ontaprest.MockRESTClient, _ *ontaprest.MockClusterClient, _ *ontaprest.MockStorageClient) {
		mockProvider.On("CreateRESTClient").Return(nil, errors.New("connection refused"))
	})

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unable to connect to host")
}

func TestUpdateHost_ClusterGetError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, func(mockProvider *vsa.MockProvider, mockREST *ontaprest.MockRESTClient, mockCluster *ontaprest.MockClusterClient, _ *ontaprest.MockStorageClient) {
		mockProvider.On("CreateRESTClient").Return(mockREST, nil)
		mockREST.On("Cluster").Return(mockCluster)
		mockCluster.On("ClusterGet", mock.Anything, mock.Anything).Return(nil, errors.New("cluster get failed"))
	})

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.True(t, utilserrors.IsBadRequestErr(err))
	assert.Contains(t, err.Error(), "unable to fetch cluster details")
}

func TestUpdateHost_StorageUpdateError(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapClusterGet)
	mockStorage.On("UpdateHost", mock.Anything, mock.Anything).Return(nil, errors.New("update failed"))

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update failed")
}

func TestUpdateHost_SuccessWithNewPassword(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	storedPassword := host.Password
	params := validUpdateHostParams()
	label := "type=UPDATED"
	vendorID := "vendor-updated"
	maintenanceMode := true
	publicAccess := true
	ip := "10.0.0.2"
	port := 8443
	protocol := datamodel.ExternalClusterProtocolHTTPS
	params.Label = &label
	params.VendorID = &vendorID
	params.MaintenanceMode = &maintenanceMode
	params.PublicAccess = &publicAccess
	params.IPAddress = &ip
	params.Port = &port
	params.Protocol = &protocol
	params.ExternalUUID = "external-uuid-updated"
	params.ExternalName = "cluster-updated"

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapClusterGet)
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(updated *datamodel.Host) bool {
		if updated.Name != "host-01" || updated.Username != "admin" || updated.IPAddress != "10.0.0.2" {
			return false
		}
		if updated.Protocol != protocol || updated.Port == nil || *updated.Port != port {
			return false
		}
		if updated.Label != label || updated.VendorID == nil || *updated.VendorID != vendorID {
			return false
		}
		if updated.Attributes == nil || !updated.Attributes.MaintenanceMode || !updated.Attributes.PublicAccess {
			return false
		}
		if updated.ExternalUUID != "11111111-1111-1111-1111-111111111111" || updated.ExternalName != "vsim-cluster" {
			return false
		}
		if updated.Password == storedPassword || updated.Password == "new-secret" {
			return false
		}
		decrypted, err := utils.DecryptPassword(log.Secret(updated.Password))
		return err == nil && decrypted != nil && *decrypted == "new-secret"
	})).Return(&datamodel.Host{Name: "host-01"}, nil)

	got, err := orch.UpdateHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestUpdateHost_SuccessWithPasswordMask(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	storedPassword := host.Password
	params := validUpdateHostParams()
	params.Password = log.Secret(log.PasswordMask)

	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)
	stubOntapProviderForHost(t, setupSuccessfulOntapClusterGet)
	mockStorage.On("UpdateHost", mock.Anything, mock.MatchedBy(func(updated *datamodel.Host) bool {
		return updated.Password == storedPassword
	})).Return(&datamodel.Host{Name: "host-01", Password: storedPassword}, nil)

	got, err := orch.UpdateHost(context.Background(), params)
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestUpdateHost_UsesInsecureSkipVerifyForInsecureProtocol(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	orch := &GCPOrchestrator{storage: mockStorage}

	host := existingStoredHost()
	mockStorage.On("GetHost", mock.Anything, "host-uuid-123").Return(host, nil)

	var captured vsa.ProviderDetails
	original := getOntapProviderForHost
	t.Cleanup(func() { getOntapProviderForHost = original })
	getOntapProviderForHost = func(_ context.Context, details vsa.ProviderDetails) vsa.Provider {
		captured = details
		mockProvider := vsa.NewMockProvider(t)
		mockREST := ontaprest.NewMockRESTClient(t)
		mockCluster := ontaprest.NewMockClusterClient(t)
		setupSuccessfulOntapClusterGet(mockProvider, mockREST, mockCluster, nil)
		return mockProvider
	}
	mockStorage.On("UpdateHost", mock.Anything, mock.Anything).Return(&datamodel.Host{Name: "host-01"}, nil)

	_, err := orch.UpdateHost(context.Background(), validUpdateHostParams())
	require.NoError(t, err)
	assert.True(t, captured.InsecureSkipVerify)
	assert.Equal(t, "new-secret", captured.Password)
}

func TestApplyUpdateParamsToHost(t *testing.T) {
	t.Run("applies full spec and preserves existing protocol when unset", func(t *testing.T) {
		port := 443
		host := &datamodel.Host{
			Name:      "old-name",
			Username:  "old-user",
			Protocol:  datamodel.ExternalClusterProtocolInsecureHTTPS,
			Port:      &port,
			IPAddress: "10.0.0.1",
			Attributes: &datamodel.HostAttributes{
				MaintenanceMode: false,
				PublicAccess:    false,
			},
		}
		label := "type=VSIM"
		vendorID := "vendor-123"
		maintenanceMode := true
		publicAccess := true
		ip := "10.0.0.2"
		newPort := 8443
		protocol := datamodel.ExternalClusterProtocolHTTPS

		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:            "host-01",
				Username:        "admin",
				Password:        log.Secret("secret"),
				ExternalUUID:    "external-uuid",
				ExternalName:    "external-name",
				VendorID:        &vendorID,
				Label:           &label,
				MaintenanceMode: &maintenanceMode,
				PublicAccess:    &publicAccess,
				IPAddress:       &ip,
				Port:            &newPort,
				Protocol:        &protocol,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "host-01", host.Name)
		assert.Equal(t, "admin", host.Username)
		assert.Equal(t, protocol, host.Protocol)
		require.NotNil(t, host.Port)
		assert.Equal(t, 8443, *host.Port)
		assert.Equal(t, "10.0.0.2", host.IPAddress)
		assert.Equal(t, "external-uuid", host.ExternalUUID)
		assert.Equal(t, "external-name", host.ExternalName)
		assert.Equal(t, label, host.Label)
		require.NotNil(t, host.VendorID)
		assert.Equal(t, vendorID, *host.VendorID)
		require.NotNil(t, host.Attributes)
		assert.True(t, host.Attributes.MaintenanceMode)
		assert.True(t, host.Attributes.PublicAccess)
	})

	t.Run("uses host name when stored and request ip are empty", func(t *testing.T) {
		host := &datamodel.Host{}
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:     "10.0.0.50",
				Username: "admin",
				Password: log.Secret("secret"),
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.50", host.IPAddress)
	})

	t.Run("uses request ip when provided", func(t *testing.T) {
		host := &datamodel.Host{IPAddress: "10.0.0.1"}
		ip := "10.0.0.2"
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:      "host-01",
				Username:  "admin",
				Password:  log.Secret("secret"),
				IPAddress: &ip,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.2", host.IPAddress)
	})

	t.Run("returns bad request for invalid protocol", func(t *testing.T) {
		host := &datamodel.Host{Protocol: datamodel.ExternalClusterProtocolHTTPS}
		protocol := "NFS"
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:     "host-01",
				Username: "admin",
				Password: log.Secret("secret"),
				Protocol: &protocol,
			},
		})
		require.Error(t, err)
		assert.True(t, utilserrors.IsBadRequestErr(err))
	})

	t.Run("updates efficiency factor when set", func(t *testing.T) {
		host := &datamodel.Host{
			EfficiencyFactor: datamodel.DefaultHostEfficiencyFactor,
		}
		efficiencyFactor := 2.5
		err := applyUpdateParamsToHost(host, &commonparams.UpdateHostParams{
			HostUUID: "host-uuid-123",
			CreateHostParams: commonparams.CreateHostParams{
				Name:             "host-01",
				Username:         "admin",
				Password:         log.Secret("secret"),
				EfficiencyFactor: &efficiencyFactor,
			},
		})
		require.NoError(t, err)
		assert.Equal(t, efficiencyFactor, host.EfficiencyFactor)
	})
}
