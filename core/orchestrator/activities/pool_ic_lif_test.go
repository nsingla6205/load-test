package activities

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
)

func Test_interclusterLIFReservationCount_singleHAPair(t *testing.T) {
	cfg := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 1},
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{VM1: vlm.VMConfig{Name: "vm1"}, VM2: vlm.VMConfig{Name: "vm2"}}},
		},
	}
	assert.Equal(t, 2, interclusterLIFReservationCount(cfg))
}

func Test_interclusterLIFReservationCount_multipleHAPairs(t *testing.T) {
	cfg := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 3},
	}
	assert.Equal(t, 6, interclusterLIFReservationCount(cfg))
}

func Test_assignInterclusterLifIPsToVLMConfig_bothVMs(t *testing.T) {
	t.Run("pre-sized ha_pair", func(t *testing.T) {
		cfg := &vlm.VLMConfig{
			Cloud: vlm.CloudConfig{
				HAPairs: []vlm.HAPair{
					{VM1: vlm.VMConfig{Name: "vm1"}, VM2: vlm.VMConfig{Name: "vm2"}},
				},
			},
		}
		assignInterclusterLifIPsToVLMConfig(cfg, []string{"10.44.44.2", "10.44.44.3"})

		vm1Lif := cfg.Cloud.HAPairs[0].VM1.SystemLIFs[vlm.LIFTypeInterCluster]
		vm2Lif := cfg.Cloud.HAPairs[0].VM2.SystemLIFs[vlm.LIFTypeInterCluster]
		assert.Equal(t, "10.44.44.2", vm1Lif.IP)
		assert.Equal(t, "10.44.44.3", vm2Lif.IP)
	})
	t.Run("empty ha_pair after ensure", func(t *testing.T) {
		cfg := &vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{NumHAPair: 1},
			Cloud:      vlm.CloudConfig{HAPairs: []vlm.HAPair{}},
		}
		ensureHAPairStructInVLMConfig(cfg, interclusterHAPairCount(cfg))
		assignInterclusterLifIPsToVLMConfig(cfg, []string{"10.44.44.2", "10.44.44.3"})

		require.Len(t, cfg.Cloud.HAPairs, 1)
		assert.Equal(t, "10.44.44.2", cfg.Cloud.HAPairs[0].VM1.SystemLIFs[vlm.LIFTypeInterCluster].IP)
		assert.Equal(t, "10.44.44.3", cfg.Cloud.HAPairs[0].VM2.SystemLIFs[vlm.LIFTypeInterCluster].IP)
	})
}

func Test_ensureHAPairStructInVLMConfig_emptySlice(t *testing.T) {
	cfg := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 1},
		Cloud:      vlm.CloudConfig{HAPairs: []vlm.HAPair{}},
	}
	ensureHAPairStructInVLMConfig(cfg, interclusterHAPairCount(cfg))
	require.Len(t, cfg.Cloud.HAPairs, 1)
}

func Test_ensureHAPairStructInVLMConfig_largeCapacity(t *testing.T) {
	cfg := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 3},
		Cloud:      vlm.CloudConfig{HAPairs: []vlm.HAPair{}},
	}
	ensureHAPairStructInVLMConfig(cfg, interclusterHAPairCount(cfg))
	require.Len(t, cfg.Cloud.HAPairs, 3)
}

func Test_ensureHAPairStructInVLMConfig_idempotent(t *testing.T) {
	cfg := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 2},
		Cloud:      vlm.CloudConfig{HAPairs: []vlm.HAPair{}},
	}
	ensureHAPairStructInVLMConfig(cfg, 2)
	ensureHAPairStructInVLMConfig(cfg, 2)
	require.Len(t, cfg.Cloud.HAPairs, 2)
}

func Test_applyICManagedInterclusterNetConfig(t *testing.T) {
	t.Run("sets flags on existing intercluster entry", func(t *testing.T) {
		cfg := &vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{
				NetConfig: map[vlm.VSALIFType]vlm.NetworkConfig{
					vlm.LIFTypeInterCluster: {
						VPC:    "data-vpc",
						Subnet: "data-subnet",
						GCPNetworkConfig: vlm.GCPNetworkConfig{
							SubnetProjectID: "host-project",
						},
					},
					vlm.LIFTypeNodeMgmt: {
						VPC:    "mgmt-vpc",
						Subnet: "mgmt-subnet",
						GCPNetworkConfig: vlm.GCPNetworkConfig{
							SubnetProjectID: "tenant-project",
						},
					},
				},
			},
		}

		applyICManagedInterclusterNetConfig(cfg)

		ic := cfg.Deployment.NetConfig[vlm.LIFTypeInterCluster]
		assert.True(t, ic.GCPNetworkConfig.IsSecondaryIP)
		assert.True(t, ic.GCPNetworkConfig.SkipIPReservation)
		assert.Equal(t, "host-project", ic.GCPNetworkConfig.SubnetProjectID)
		assert.Equal(t, "data-vpc", ic.VPC)

		mgmt := cfg.Deployment.NetConfig[vlm.LIFTypeNodeMgmt]
		assert.False(t, mgmt.GCPNetworkConfig.IsSecondaryIP)
		assert.False(t, mgmt.GCPNetworkConfig.SkipIPReservation)
	})

	t.Run("no-op when NetConfig nil", func(t *testing.T) {
		cfg := &vlm.VLMConfig{Deployment: vlm.DeploymentConfig{}}
		applyICManagedInterclusterNetConfig(cfg)
		assert.Nil(t, cfg.Deployment.NetConfig)
	})

	t.Run("no-op when intercluster entry missing", func(t *testing.T) {
		cfg := &vlm.VLMConfig{
			Deployment: vlm.DeploymentConfig{
				NetConfig: map[vlm.VSALIFType]vlm.NetworkConfig{
					vlm.LIFTypeNodeMgmt: {VPC: "mgmt-vpc"},
				},
			},
		}
		applyICManagedInterclusterNetConfig(cfg)
		_, ok := cfg.Deployment.NetConfig[vlm.LIFTypeInterCluster]
		assert.False(t, ok)
		assert.Equal(t, "mgmt-vpc", cfg.Deployment.NetConfig[vlm.LIFTypeNodeMgmt].VPC)
	})

	t.Run("no-op on nil config", func(t *testing.T) {
		applyICManagedInterclusterNetConfig(nil)
	})
}

func Test_assignInterclusterLifIPsToVLMConfig_jsonShape(t *testing.T) {
	cfg := &vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 1},
		Cloud:      vlm.CloudConfig{HAPairs: []vlm.HAPair{}},
	}
	ensureHAPairStructInVLMConfig(cfg, interclusterHAPairCount(cfg))
	assignInterclusterLifIPsToVLMConfig(cfg, []string{"192.168.64.36", "192.168.66.17"})

	raw, err := json.Marshal(cfg)
	require.NoError(t, err)

	var doc map[string]any
	require.NoError(t, json.Unmarshal(raw, &doc))

	cloud, ok := doc["cloud"].(map[string]any)
	require.True(t, ok)
	haPairs, ok := cloud["ha_pair"].([]any)
	require.True(t, ok)
	require.Len(t, haPairs, 1)

	pair0, ok := haPairs[0].(map[string]any)
	require.True(t, ok)
	vm1, ok := pair0["vm1"].(map[string]any)
	require.True(t, ok)
	vm1Lifs, ok := vm1["lifs"].(map[string]any)
	require.True(t, ok)
	vm1IC, ok := vm1Lifs["intercluster"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "192.168.64.36", vm1IC["ip"])

	vm2, ok := pair0["vm2"].(map[string]any)
	require.True(t, ok)
	vm2Lifs, ok := vm2["lifs"].(map[string]any)
	require.True(t, ok)
	vm2IC, ok := vm2Lifs["intercluster"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "192.168.66.17", vm2IC["ip"])
}

func Test_bindInterclusterLifsToNodes_usesReservedDBIPs(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	ctx := context.Background()
	poolID := int64(41)
	vlmConfig := &vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{
					Name:     "vm1",
					HostName: "gcnv-test-01",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeInterCluster: {IP: "10.43.28.210"},
					},
				},
				VM2: vlm.VMConfig{
					Name:     "vm2",
					HostName: "gcnv-test-02",
					SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeInterCluster: {IP: "10.43.28.211"},
					},
				},
			}},
		},
	}
	mockStorage.On("ListInterclusterLifsByPoolID", mock.Anything, poolID).Return([]*datamodel.Lif{
		{IPAddress: "10.44.44.2"},
		{IPAddress: "10.44.44.3"},
	}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, poolID).Return([]*datamodel.Node{
		{BaseModel: datamodel.BaseModel{ID: 101}, Name: "gcnv-test-01"},
		{BaseModel: datamodel.BaseModel{ID: 102}, Name: "gcnv-test-02"},
	}, nil)
	mockStorage.On("BindInterclusterLifToNode", mock.Anything, poolID, "10.44.44.2", int64(101)).Return(nil)
	mockStorage.On("BindInterclusterLifToNode", mock.Anything, poolID, "10.44.44.3", int64(102)).Return(nil)

	err := bindInterclusterLifsToNodes(ctx, mockStorage, poolID, vlmConfig)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func Test_nodeIDForVM(t *testing.T) {
	nodeByName := map[string]int64{"gcnv-test-01": 101}
	id, ok := nodeIDForVM(nodeByName, vlm.VMConfig{HostName: "gcnv-test-01"})
	assert.True(t, ok)
	assert.Equal(t, int64(101), id)

	id, ok = nodeIDForVM(nodeByName, vlm.VMConfig{Name: "gcnv-test-01"})
	assert.True(t, ok)
	assert.Equal(t, int64(101), id)
}

func Test_shouldReleaseSharedDataSubnet(t *testing.T) {
	t.Run("single pool", func(t *testing.T) {
		pools := []*datamodel.PoolView{{Pool: datamodel.Pool{State: datamodel.LifeCycleStateREADY}}}
		assert.True(t, shouldReleaseSharedDataSubnet(pools))
	})
	t.Run("multiple pools one ready", func(t *testing.T) {
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}},
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateREADY}},
		}
		assert.False(t, shouldReleaseSharedDataSubnet(pools))
	})
	t.Run("all pools deleting", func(t *testing.T) {
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}},
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}},
		}
		assert.True(t, shouldReleaseSharedDataSubnet(pools))
	})
	t.Run("no pools", func(t *testing.T) {
		assert.False(t, shouldReleaseSharedDataSubnet(nil))
	})
	// The cases below cover rollback of a pool that reused an existing subnet, where the
	// rolling-back pool is itself still in CREATING.
	t.Run("single creating pool", func(t *testing.T) {
		pools := []*datamodel.PoolView{{Pool: datamodel.Pool{State: datamodel.LifeCycleStateCreating}}}
		assert.True(t, shouldReleaseSharedDataSubnet(pools))
	})
	t.Run("creating pool alongside ready pool", func(t *testing.T) {
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateCreating}},
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateREADY}},
		}
		assert.False(t, shouldReleaseSharedDataSubnet(pools))
	})
	t.Run("concurrent creating pools", func(t *testing.T) {
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateCreating}},
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateCreating}},
		}
		assert.False(t, shouldReleaseSharedDataSubnet(pools))
	})
	t.Run("errored pool alongside ready pool", func(t *testing.T) {
		pools := []*datamodel.PoolView{
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateError}},
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateREADY}},
		}
		assert.False(t, shouldReleaseSharedDataSubnet(pools))
	})
}

func stubICLifDeleteHooks(t *testing.T, getPools func(context.Context, database.Storage, string, string, string) ([]*datamodel.PoolView, error)) {
	t.Helper()

	origFlag := icLifAddressSpaceEnabled
	origGetPools := getPoolsBySubnetwork
	origSubnetExists := icLifDataSubnetStillExists
	t.Cleanup(func() {
		icLifAddressSpaceEnabled = origFlag
		getPoolsBySubnetwork = origGetPools
		icLifDataSubnetStillExists = origSubnetExists
	})
	icLifAddressSpaceEnabled = func() bool { return true }
	getPoolsBySubnetwork = getPools
	icLifDataSubnetStillExists = func(context.Context, string, string) (bool, error) {
		return false, nil
	}
}

func TestDeleteInterclusterIPRangeForSubnet_skipsWhenOtherPoolsOnSubnet(t *testing.T) {
	stubICLifDeleteHooks(t, func(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) ([]*datamodel.PoolView, error) {
		return []*datamodel.PoolView{
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}},
			{Pool: datamodel.Pool{State: datamodel.LifeCycleStateREADY}},
		}, nil
	})

	mockStorage := database.NewMockStorage(t)
	pa := &PoolActivity{SE: mockStorage}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 40},
		AccountID: 1,
		Name:      "testpool1",
		Network:   "vpc-1",
		Account:   &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"},
		ClusterDetails: datamodel.ClusterDetails{
			SubnetNames: []string{"vsa-123-456"},
		},
	}

	err := pa.DeleteInterclusterIPRangeForSubnet(context.Background(), pool)
	assert.NoError(t, err)
}

func TestDeleteInterclusterIPRangeForSubnet_skipsWhenSubnetStillExistsInGCP(t *testing.T) {
	stubICLifDeleteHooks(t, func(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) ([]*datamodel.PoolView, error) {
		return []*datamodel.PoolView{{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}}}, nil
	})
	icLifDataSubnetStillExists = func(context.Context, string, string) (bool, error) {
		return true, nil
	}

	mockStorage := database.NewMockStorage(t)
	pa := &PoolActivity{SE: mockStorage}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 40},
		AccountID: 1,
		Name:      "testpool1",
		Network:   "vpc-1",
		Account:   &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"},
		ClusterDetails: datamodel.ClusterDetails{
			SubnetNames:   []string{"vsa-123-456"},
			SnHostProject: "host-project",
		},
	}

	err := pa.DeleteInterclusterIPRangeForSubnet(context.Background(), pool)
	assert.NoError(t, err)
}

func TestDeleteInterclusterIPRangeForSubnet_deletesWhenLastPoolOnSubnet(t *testing.T) {
	stubICLifDeleteHooks(t, func(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) ([]*datamodel.PoolView, error) {
		return []*datamodel.PoolView{{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}}}, nil
	})
	origRegion := Region
	t.Cleanup(func() { Region = origRegion })
	Region = "us-central1"

	mockStorage := database.NewMockStorage(t)
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"}, Name: "test-account"}
	subnet := "vsa-123-456"
	snHost := "host-project"
	network := interclusterSubnetNetwork("123", Region, subnet)
	mockStorage.On("GetAccount", mock.Anything, account.Name).Return(account, nil)
	mockStorage.On("GetIPRanges", mock.Anything, &account.UUID, &network).Return([]*datamodel.IPRange{{
		BaseModel: datamodel.BaseModel{ID: 99, UUID: "ip-range-uuid"},
		LifType:   database.LifTypeInterclusterLIF,
	}}, nil)
	mockStorage.On("DeleteIPRangeByID", mock.Anything, int64(99)).Return(&datamodel.IPRange{}, nil)

	pa := &PoolActivity{SE: mockStorage}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 40},
		AccountID: 1,
		Name:      "testpool1",
		Network:   "projects/123/global/networks/vpc-1",
		Account:   account,
		ClusterDetails: datamodel.ClusterDetails{
			SubnetNames:   []string{subnet},
			SnHostProject: snHost,
		},
	}

	err := pa.DeleteInterclusterIPRangeForSubnet(context.Background(), pool)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteInterclusterIPRangeForSubnet_releasesParentAddressRange(t *testing.T) {
	stubICLifDeleteHooks(t, func(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) ([]*datamodel.PoolView, error) {
		return []*datamodel.PoolView{{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}}}, nil
	})
	origRegion := Region
	t.Cleanup(func() { Region = origRegion })
	Region = "us-central1"

	mockStorage := database.NewMockStorage(t)
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"}, Name: "test-account"}
	subnet := "vsa-123-456"
	snHost := "host-project"
	network := interclusterSubnetNetwork("123", Region, subnet)
	mockStorage.On("GetAccount", mock.Anything, account.Name).Return(account, nil)
	mockStorage.On("GetIPRanges", mock.Anything, &account.UUID, &network).Return([]*datamodel.IPRange{{
		BaseModel: datamodel.BaseModel{ID: 99, UUID: "ip-range-uuid"},
		LifType:   database.LifTypeInterclusterLIF,
	}}, nil)
	mockStorage.On("DeleteIPRangeByID", mock.Anything, int64(99)).Return(&datamodel.IPRange{
		AddressRangeID: sql.NullInt64{Int64: 11, Valid: true},
	}, nil)
	mockStorage.On("ReleaseInterclusterAddressRangeIfUnused", mock.Anything, int64(11)).Return(true, nil)

	pa := &PoolActivity{SE: mockStorage}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 40},
		AccountID: 1,
		Name:      "testpool1",
		Network:   "projects/123/global/networks/vpc-1",
		Account:   account,
		ClusterDetails: datamodel.ClusterDetails{
			SubnetNames:   []string{subnet},
			SnHostProject: snHost,
		},
	}

	err := pa.DeleteInterclusterIPRangeForSubnet(context.Background(), pool)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteInterclusterIPRangeForSubnet_releaseFailureDoesNotFailDelete(t *testing.T) {
	stubICLifDeleteHooks(t, func(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) ([]*datamodel.PoolView, error) {
		return []*datamodel.PoolView{{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}}}, nil
	})
	origRegion := Region
	t.Cleanup(func() { Region = origRegion })
	Region = "us-central1"

	mockStorage := database.NewMockStorage(t)
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"}, Name: "test-account"}
	subnet := "vsa-123-456"
	snHost := "host-project"
	network := interclusterSubnetNetwork("123", Region, subnet)
	mockStorage.On("GetAccount", mock.Anything, account.Name).Return(account, nil)
	mockStorage.On("GetIPRanges", mock.Anything, &account.UUID, &network).Return([]*datamodel.IPRange{{
		BaseModel: datamodel.BaseModel{ID: 99, UUID: "ip-range-uuid"},
		LifType:   database.LifTypeInterclusterLIF,
	}}, nil)
	mockStorage.On("DeleteIPRangeByID", mock.Anything, int64(99)).Return(&datamodel.IPRange{
		AddressRangeID: sql.NullInt64{Int64: 11, Valid: true},
	}, nil)
	mockStorage.On("ReleaseInterclusterAddressRangeIfUnused", mock.Anything, int64(11)).
		Return(false, errors.New("db unavailable"))

	// A stuck IN_USE parent is recoverable; failing pool deletion here is not.
	pa := &PoolActivity{SE: mockStorage}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 40},
		AccountID: 1,
		Name:      "testpool1",
		Network:   "projects/123/global/networks/vpc-1",
		Account:   account,
		ClusterDetails: datamodel.ClusterDetails{
			SubnetNames:   []string{subnet},
			SnHostProject: snHost,
		},
	}

	err := pa.DeleteInterclusterIPRangeForSubnet(context.Background(), pool)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestDeleteInterclusterIPRangeForSubnet_invalidVendorNetworkFails(t *testing.T) {
	stubICLifDeleteHooks(t, func(ctx context.Context, se database.Storage, accountID, subnetworkName, poolNetwork string) ([]*datamodel.PoolView, error) {
		return []*datamodel.PoolView{{Pool: datamodel.Pool{State: datamodel.LifeCycleStateDeleting}}}, nil
	})
	origRegion := Region
	t.Cleanup(func() { Region = origRegion })
	Region = "us-central1"

	mockStorage := database.NewMockStorage(t)
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "acct-uuid"}, Name: "test-account"}
	mockStorage.On("GetAccount", mock.Anything, account.Name).Return(account, nil)

	pa := &PoolActivity{SE: mockStorage}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 40},
		AccountID: 1,
		Name:      "testpool1",
		Network:   "not-a-vendor-network",
		Account:   account,
		ClusterDetails: datamodel.ClusterDetails{
			SubnetNames:   []string{"vsa-123-456"},
			SnHostProject: "host-project",
		},
	}

	err := pa.DeleteInterclusterIPRangeForSubnet(context.Background(), pool)
	require.Error(t, err)
	var appErr *temporal.ApplicationError
	require.ErrorAs(t, err, &appErr)
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, vsaerrors.CustomErrorType, appErr.Type())
	var trackingID int
	var originalMsg string
	require.NoError(t, appErr.Details(&trackingID, &originalMsg))
	assert.Equal(t, vsaerrors.ErrInputValidationError, trackingID)
	assert.Contains(t, originalMsg, "invalid vendor network")
	mockStorage.AssertNotCalled(t, "GetIPRanges", mock.Anything, mock.Anything, mock.Anything)
	mockStorage.AssertNotCalled(t, "DeleteIPRangeByID", mock.Anything, mock.Anything)
}

func Test_interclusterHAPairCount_fallbacks(t *testing.T) {
	assert.Equal(t, 0, interclusterHAPairCount(nil))
	assert.Equal(t, 3, interclusterHAPairCount(&vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{NumHAPair: 3},
	}))
	assert.Equal(t, 2, interclusterHAPairCount(&vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{}, {}},
		},
	}))
	assert.Equal(t, 1, interclusterHAPairCount(&vlm.VLMConfig{}))
}

func Test_assignInterclusterLifIPsToVLMConfig_noops(t *testing.T) {
	assignInterclusterLifIPsToVLMConfig(nil, []string{"10.0.0.1"})
	cfg := &vlm.VLMConfig{}
	assignInterclusterLifIPsToVLMConfig(cfg, nil)
	assert.Empty(t, cfg.Cloud.HAPairs)
}

func Test_bindInterclusterLifsToNodes_shortReservedCount(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("ListInterclusterLifsByPoolID", mock.Anything, int64(9)).
		Return([]*datamodel.Lif{{IPAddress: "10.44.44.2"}}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, int64(9)).
		Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 1}, Name: "node-1"},
			{BaseModel: datamodel.BaseModel{ID: 2}, Name: "node-2"},
		}, nil)
	mockStorage.On("BindInterclusterLifToNode", mock.Anything, int64(9), "10.44.44.2", int64(1)).
		Return(nil)

	vlmConfig := &vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{Name: "node-1"},
				VM2: vlm.VMConfig{Name: "node-2"},
			}},
		},
	}
	err := bindInterclusterLifsToNodes(context.Background(), mockStorage, 9, vlmConfig)
	assert.NoError(t, err)
}

func Test_bindInterclusterLifsToNodes_skipsUnmatchedNode(t *testing.T) {
	mockStorage := database.NewMockStorage(t)
	mockStorage.On("ListInterclusterLifsByPoolID", mock.Anything, int64(10)).
		Return([]*datamodel.Lif{
			{IPAddress: "10.44.44.2"},
			{IPAddress: "10.44.44.3"},
		}, nil)
	mockStorage.On("GetNodesByPoolID", mock.Anything, int64(10)).
		Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 1}, Name: "known-node"},
		}, nil)
	mockStorage.On("BindInterclusterLifToNode", mock.Anything, int64(10), "10.44.44.3", int64(1)).
		Return(nil)

	vlmConfig := &vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{{
				VM1: vlm.VMConfig{Name: "unknown-node"},
				VM2: vlm.VMConfig{Name: "known-node"},
			}},
		},
	}
	err := bindInterclusterLifsToNodes(context.Background(), mockStorage, 10, vlmConfig)
	assert.NoError(t, err)
}
