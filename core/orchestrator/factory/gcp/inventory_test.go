package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontapmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// ---- helpers ----------------------------------------------------------------

func activeAccount(name string) *datamodel.Account {
	return &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "acct-uuid", ID: 42},
		Name:      name,
		State:     datamodel.AccountStateEnabled,
	}
}

func makeTestHost(hostname, hostUUID, secretID, username string) *datamodel.Host {
	return &datamodel.Host{
		BaseModel:    datamodel.BaseModel{UUID: hostUUID},
		Name:         hostname,
		ExternalName: hostname,
		IPAddress:    "10.0.0.1",
		Username:     username,
		Password:     "encrypted-password",
	}
}

func makeTestHostCred(hostname, hostUUID, secretID, username string) *database.HostCredential {
	return &database.HostCredential{
		Host:        makeTestHost(hostname, hostUUID, secretID, username),
		Username:    username,
		SecretId:    secretID,
		AccountUUID: "acct-uuid",
	}
}

func stubGetProviderForHost(t *testing.T, fn func(context.Context, *datamodel.Host) (vsa.Provider, error)) {
	t.Helper()
	orig := vsa.GetProviderForHost
	t.Cleanup(func() { vsa.GetProviderForHost = orig })
	vsa.GetProviderForHost = fn
}

func stubProvider(t *testing.T, provider vsa.Provider) {
	t.Helper()
	stubGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return provider, nil
	})
}

// stubGetAccountWithName replaces the package-level getAccountWithName var.
func stubGetAccountWithName(t *testing.T, fn func(context.Context, database.Storage, string) (*datamodel.Account, error)) {
	t.Helper()
	orig := getAccountWithName
	t.Cleanup(func() { getAccountWithName = orig })
	getAccountWithName = fn
}

// stubCreateAccount replaces the package-level createAccount var.
func stubCreateAccount(t *testing.T, fn func(context.Context, database.Storage, string) (*datamodel.Account, error)) {
	t.Helper()
	orig := createAccount
	t.Cleanup(func() { createAccount = orig })
	createAccount = fn
}

// ---- GetProjectInventory: account lifecycle --------------------------------

func TestGetProjectInventory_AccountNotFound_CreatesAndReturnsEmpty(t *testing.T) {
	created := false
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return nil, customerrors.NewNotFoundErr("account", nil)
	})
	stubCreateAccount(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		created = true
		return activeAccount("proj-1"), nil
	})

	o := &GCPOrchestrator{storage: database.NewMockStorage(t)}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err)
	assert.True(t, created, "createAccount must be called for a new project")
	assert.Empty(t, result.Clusters, "brand-new account has no clusters yet")
}

func TestGetProjectInventory_AccountCreateRace_ReturnsEmpty(t *testing.T) {
	// getAccountWithName says not-found; createAccount fails (concurrent create).
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return nil, customerrors.NewNotFoundErr("account", nil)
	})
	stubCreateAccount(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return nil, errors.New("duplicate key value")
	})

	o := &GCPOrchestrator{storage: database.NewMockStorage(t)}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-race"})

	require.NoError(t, err, "concurrent create must not return an error to the caller")
	assert.Empty(t, result.Clusters)
}

func TestGetProjectInventory_AccountLookupError_ReturnsError(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return nil, errors.New("db unreachable")
	})

	o := &GCPOrchestrator{storage: database.NewMockStorage(t)}
	_, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db unreachable")
}

func TestGetProjectInventory_AccountDisabled_ReturnsError(t *testing.T) {
	disabled := activeAccount("proj-1")
	disabled.State = datamodel.AccountStateDisabled

	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return disabled, nil
	})

	o := &GCPOrchestrator{storage: database.NewMockStorage(t)}
	_, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.Error(t, err)
	var vcpErr *vsaerrors.CustomError
	require.ErrorAs(t, err, &vcpErr)
	assert.True(t, vcpErr.IsError(vsaerrors.ErrAccountNotFound))
}

// ---- GetProjectInventory: host credentials ---------------------------------

func TestGetProjectInventory_ListPinnedHostCredentialsError_ReturnsError(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return(nil, errors.New("db error"))

	o := &GCPOrchestrator{storage: mockStorage}
	_, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

func TestGetProjectInventory_NoHostCredentials_ReturnsEmpty(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return(nil, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err)
	assert.Empty(t, result.Clusters)
}

// ---- GetProjectInventory: per-cluster failures are non-fatal ---------------

func TestGetProjectInventory_VSAProviderCreationFails_ClusterSkipped(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})
	stubGetProviderForHost(t, func(context.Context, *datamodel.Host) (vsa.Provider, error) {
		return nil, errors.New("connection refused")
	})

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{makeTestHostCred("cluster-a", "uuid-a", "secret-a", "admin")}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err)
	assert.Empty(t, result.Clusters)
}

func TestGetProjectInventory_ListAggregatesFails_ClusterSkipped(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})
	provider := vsa.NewMockProvider(t)
	provider.EXPECT().GetAggregates().Return(nil, errors.New("ONTAP unreachable"))
	stubProvider(t, provider)

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{makeTestHostCred("cluster-a", "uuid-a", "secret-a", "admin")}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err)
	assert.Empty(t, result.Clusters)
}

func TestGetProjectInventory_ListVolumesFails_ClusterSkipped(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})
	provider := vsa.NewMockProvider(t)
	provider.EXPECT().GetAggregates().Return([]*vsa.Aggregate{{Name: "aggr1"}}, nil)
	provider.EXPECT().GetVolumesForInventory(mock.Anything).Return(nil, errors.New("volumes error"))
	stubProvider(t, provider)

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{makeTestHostCred("cluster-a", "uuid-a", "secret-a", "admin")}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err)
	assert.Empty(t, result.Clusters)
}

// ---- GetProjectInventory: showVolumes flag ---------------------------------

func TestGetProjectInventory_ShowVolumesFalse_ListSVMsNotCalled(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})
	provider := vsa.NewMockProvider(t)
	provider.EXPECT().GetAggregates().Return([]*vsa.Aggregate{{Name: "aggr1"}}, nil)
	provider.EXPECT().GetVolumesForInventory("svm_acctuuid_*").Return(nil, nil)
	stubProvider(t, provider)

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{makeTestHostCred("cluster-a", "uuid-a", "secret-a", "admin")}, nil)
	mockStorage.EXPECT().
		SumPoolSizesByClusterName(mock.Anything, "cluster-a").
		Return(int64(0), nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{
		ProjectNumber: "proj-1",
		ShowVolumes:   false,
	})

	require.NoError(t, err)
	require.Len(t, result.Clusters, 1)
	assert.Empty(t, result.Clusters[0].SVMs, "SVMs must not be present when showVolumes=false")
}

func TestGetProjectInventory_ShowVolumesTrue_ListSVMsErrorSkipsCluster(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})
	provider := vsa.NewMockProvider(t)
	provider.EXPECT().GetAggregates().Return([]*vsa.Aggregate{{Name: "aggr1"}}, nil)
	provider.EXPECT().GetVolumesForInventory(mock.Anything).Return(nil, nil)
	provider.EXPECT().ListSVMs(mock.Anything).Return(nil, errors.New("SVM query failed"))
	stubProvider(t, provider)

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{makeTestHostCred("cluster-a", "uuid-a", "secret-a", "admin")}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{
		ProjectNumber: "proj-1",
		ShowVolumes:   true,
	})

	require.NoError(t, err)
	assert.Empty(t, result.Clusters, "cluster must be skipped when ListSVMs fails")
}

// ---- GetProjectInventory: pool size is non-fatal ---------------------------

func TestGetProjectInventory_PoolSizeFails_DefaultsToZeroNonFatal(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})
	provider := vsa.NewMockProvider(t)
	provider.EXPECT().GetAggregates().Return([]*vsa.Aggregate{{Name: "aggr1", Size: 1000}}, nil)
	provider.EXPECT().GetVolumesForInventory(mock.Anything).Return(nil, nil)
	stubProvider(t, provider)

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{makeTestHostCred("cluster-a", "uuid-a", "secret-a", "admin")}, nil)
	mockStorage.EXPECT().
		SumPoolSizesByClusterName(mock.Anything, "cluster-a").
		Return(int64(0), errors.New("pool query failed"))

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err, "pool size error must be non-fatal")
	require.Len(t, result.Clusters, 1)
	assert.Equal(t, int64(0), result.Clusters[0].UsedSize, "pool size error defaults to 0")
}

// ---- GetProjectInventory: partial failure ----------------------------------

func TestGetProjectInventory_PartialClusterFailure_SuccessfulClustersReturned(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})

	provider := vsa.NewMockProvider(t)
	provider.EXPECT().GetAggregates().Return([]*vsa.Aggregate{{Name: "aggr1"}}, nil)
	provider.EXPECT().GetVolumesForInventory(mock.Anything).Return(nil, nil)
	stubGetProviderForHost(t, func(_ context.Context, host *datamodel.Host) (vsa.Provider, error) {
		if host.UUID == "uuid-fail" {
			return nil, errors.New("connection refused")
		}
		return provider, nil
	})

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{
			makeTestHostCred("cluster-ok", "uuid-ok", "secret-ok", "admin"),
			makeTestHostCred("cluster-fail", "uuid-fail", "secret-fail", "admin"),
		}, nil)
	mockStorage.EXPECT().
		SumPoolSizesByClusterName(mock.Anything, "cluster-ok").
		Return(int64(0), nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err)
	require.Len(t, result.Clusters, 1)
	assert.Equal(t, "cluster-ok", result.Clusters[0].Name)
}

// ---- fetchClusterInventory: missing host -----------------------------------

func TestGetProjectInventory_MissingHostOnCredential_ClusterSkipped(t *testing.T) {
	stubGetAccountWithName(t, func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return activeAccount("proj-1"), nil
	})

	mockStorage := database.NewMockStorage(t)
	mockStorage.EXPECT().
		ListPinnedHostCredentials(mock.Anything, "proj-1").
		Return([]*database.HostCredential{{
			Host:        nil,
			Username:    "admin",
			SecretId:    "secret-a",
			AccountUUID: "acct-uuid",
		}}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetProjectInventory(context.Background(), &commonparams.GetProjectInventoryParams{ProjectNumber: "proj-1"})

	require.NoError(t, err)
	assert.Empty(t, result.Clusters, "credential with nil Host must be skipped")
}

// ---- normalize helpers -----------------------------------------------------

func TestNormalizeInventoryAggregates(t *testing.T) {
	result := normalizeInventoryAggregates([]*vsa.Aggregate{
		nil,
		{Name: "aggr1", UUID: "uuid-1", State: "online", VolumeCount: 3, Size: 1000},
		nil,
		{Name: "aggr2", UUID: "uuid-2", State: "offline", VolumeCount: 0, Size: 500},
	})

	require.Len(t, result, 2)
	assert.Equal(t, inventoryAggregate{name: "aggr1", uuid: "uuid-1", state: "online", volumeCount: 3, size: 1000}, result[0])
	assert.Equal(t, inventoryAggregate{name: "aggr2", uuid: "uuid-2", state: "offline", volumeCount: 0, size: 500}, result[1])
}

func TestNormalizeInventoryVolumes(t *testing.T) {
	volName := "vol-a"
	svmName := "svm1"
	aggrName := "aggr1"
	emptyAggrName := ""
	physicalUsed := int64(4096)

	result := normalizeInventoryVolumes([]*vsa.Volume{
		nil,
		{
			ExternalUUID: "vol-uuid-1",
			Volume: ontapmodels.Volume{
				Name: &volName,
				Svm:  &ontapmodels.VolumeInlineSvm{Name: &svmName},
				Space: &ontapmodels.VolumeInlineSpace{PhysicalUsed: &physicalUsed},
				VolumeInlineAggregates: []*ontapmodels.VolumeInlineAggregatesInlineArrayItem{
					nil,
					{Name: &emptyAggrName},
					{Name: &aggrName},
				},
			},
		},
		{
			ExternalUUID: "vol-uuid-2",
			Volume:       ontapmodels.Volume{}, // no name/svm/space/aggregates
		},
	})

	require.Len(t, result, 2)
	assert.Equal(t, "vol-uuid-1", result[0].uuid)
	assert.Equal(t, "vol-a", result[0].name)
	assert.Equal(t, "svm1", result[0].svmName)
	assert.Equal(t, int64(4096), result[0].size)
	assert.Equal(t, []string{"aggr1"}, result[0].aggregateNames)

	assert.Equal(t, "vol-uuid-2", result[1].uuid)
	assert.Empty(t, result[1].name)
	assert.Empty(t, result[1].svmName)
	assert.Equal(t, int64(0), result[1].size)
	assert.Empty(t, result[1].aggregateNames)
}

func TestNormalizeInventorySVMs(t *testing.T) {
	svmName := "svm1"
	result := normalizeInventorySVMs([]*ontapRest.Svm{
		nil,
		{Svm: ontapmodels.Svm{Name: &svmName}},
		nil,
		{Svm: ontapmodels.Svm{}}, // nil Name → empty string
	})

	require.Len(t, result, 2)
	assert.Equal(t, inventorySVM{name: "svm1"}, result[0])
	assert.Equal(t, inventorySVM{name: ""}, result[1])
}

// ---- buildClusterInventory -------------------------------------------------

func TestEffectiveAggregateSizeMultiplier(t *testing.T) {
	tests := []struct {
		name                 string
		hostEfficiencyFactor float64
		expected             float64
	}{
		{
			name:                 "uses host efficiency factor",
			hostEfficiencyFactor: 2.5,
			expected:             2.5,
		},
		{
			name:     "uses default when host efficiency factor is zero",
			expected: defaultStorageEfficiencyFactor,
		},
		{
			name:                 "uses default when host efficiency factor is negative",
			hostEfficiencyFactor: -1,
			expected:             defaultStorageEfficiencyFactor,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, effectiveAggregateSizeMultiplier(tt.hostEfficiencyFactor))
		})
	}
}

func TestBuildClusterInventory_Empty(t *testing.T) {
	result := buildClusterInventory("cluster-x", "THICK", nil, nil, nil, 0, defaultStorageEfficiencyFactor)

	assert.Equal(t, "cluster-x", result.Name)
	assert.Equal(t, "THICK", result.ProvisioningModel)
	assert.Empty(t, result.Aggregates)
	assert.Empty(t, result.SVMs)
	assert.Equal(t, int64(0), result.Size)
}

func TestBuildClusterInventory_AggregateSizeMath(t *testing.T) {
	// size = blockStorage.size * efficiency factor; used = sum of vol sizes on aggr; available = size - used
	const efficiencyFactor = 2.5
	aggregates := []inventoryAggregate{
		{
			name:        "aggr1",
			state:       "online",
			volumeCount: 2,
			size:        1000,
		},
	}
	volumes := []inventoryVolume{
		{
			uuid:           "vol-uuid-1",
			name:           "vol-a",
			aggregateNames: []string{"aggr1"},
			size:           300,
		},
		{
			uuid:           "vol-uuid-2",
			name:           "vol-b",
			aggregateNames: []string{"aggr1"},
			size:           100,
		},
	}

	result := buildClusterInventory("cluster-x", "THICK", aggregates, volumes, nil, 0, efficiencyFactor)

	require.Len(t, result.Aggregates, 1)
	a := result.Aggregates[0]
	assert.Equal(t, "aggr1", a.Name)
	assert.Equal(t, "online", a.State)
	assert.Equal(t, int64(2), a.VolumeCount)

	expectedSize := int64(float64(1000) * efficiencyFactor)
	assert.Equal(t, expectedSize, a.Size)
	assert.Equal(t, int64(400), a.UsedSize) // 300 + 100
	assert.Equal(t, expectedSize-400, a.AvailableSize)
}

func TestBuildClusterInventory_ClusterSizeMath(t *testing.T) {
	// cluster.size = sum of aggr sizes; used = poolUsedSize
	aggregates := []inventoryAggregate{
		{name: "aggr1", size: 1000},
		{name: "aggr2", size: 500},
	}
	poolUsedSize := int64(800)

	result := buildClusterInventory("cluster-x", "THICK", aggregates, nil, nil, poolUsedSize, defaultStorageEfficiencyFactor)

	expectedClusterSize := int64(float64(1000)*defaultStorageEfficiencyFactor) + int64(float64(500)*defaultStorageEfficiencyFactor)
	assert.Equal(t, expectedClusterSize, result.Size)
	assert.Equal(t, poolUsedSize, result.UsedSize)
}

func TestBuildClusterInventory_ZeroAggregateSize_NotIncludedInClusterSize(t *testing.T) {
	// An aggregate with Size=0 contributes 0 to cluster size.
	aggregates := []inventoryAggregate{
		{name: "aggr-zero"},
	}

	result := buildClusterInventory("cluster-x", "THICK", aggregates, nil, nil, 0, defaultStorageEfficiencyFactor)

	assert.Equal(t, int64(0), result.Size)
	require.Len(t, result.Aggregates, 1)
	assert.Equal(t, int64(0), result.Aggregates[0].Size)
}

func TestBuildClusterInventory_SVMsGroupedWithVolumes(t *testing.T) {
	svms := []inventorySVM{{name: "svm1"}, {name: "svm2"}}
	volumes := []inventoryVolume{
		{uuid: "uuid-1", name: "vol-a", svmName: "svm1"},
		{uuid: "uuid-2", name: "vol-b", svmName: "svm1"},
		{uuid: "uuid-3", name: "vol-c", svmName: "svm2"},
	}

	result := buildClusterInventory("cluster-x", "THICK", nil, volumes, svms, 0, defaultStorageEfficiencyFactor)

	require.Len(t, result.SVMs, 2)
	svmByName := make(map[string]commonparams.InventorySVM)
	for _, s := range result.SVMs {
		svmByName[s.Name] = s
	}

	assert.Len(t, svmByName["svm1"].Volumes, 2)
	assert.Len(t, svmByName["svm2"].Volumes, 1)
	assert.Equal(t, "vol-c", svmByName["svm2"].Volumes[0].Name)
}

func TestBuildClusterInventory_VolumeWithEmptyUUID_Skipped(t *testing.T) {
	svms := []inventorySVM{{name: "svm1"}}
	volumes := []inventoryVolume{
		{name: "vol-no-uuid", svmName: "svm1"},
		{uuid: "valid-uuid", name: "vol-ok", svmName: "svm1"},
	}

	result := buildClusterInventory("cluster-x", "THICK", nil, volumes, svms, 0, defaultStorageEfficiencyFactor)

	require.Len(t, result.SVMs, 1)
	assert.Len(t, result.SVMs[0].Volumes, 1, "volume with empty UUID must be skipped")
	assert.Equal(t, "vol-ok", result.SVMs[0].Volumes[0].Name)
}

func TestBuildClusterInventory_VolumeForUnknownSVM_NotAddedToSVMList(t *testing.T) {
	svms := []inventorySVM{{name: "svm1"}}
	volumes := []inventoryVolume{
		{uuid: "uuid-1", name: "orphan-vol", svmName: "ghost-svm"},
	}

	result := buildClusterInventory("cluster-x", "THICK", nil, volumes, svms, 0, defaultStorageEfficiencyFactor)

	require.Len(t, result.SVMs, 1)
	assert.Equal(t, "svm1", result.SVMs[0].Name)
	assert.Empty(t, result.SVMs[0].Volumes, "volumes for unknown SVMs must not appear in any SVM")
}

func TestBuildClusterInventory_VolumeAggregateContribution_MultipleAggregates(t *testing.T) {
	// A FlexGroup volume can span multiple aggregates; its size is split across them.
	aggregates := []inventoryAggregate{
		{name: "aggr1", size: 2000},
		{name: "aggr2", size: 2000},
	}
	volumes := []inventoryVolume{
		{
			uuid:           "flexgroup-uuid",
			name:           "fg-vol",
			aggregateNames: []string{"aggr1", "aggr2"},
			size:           200,
		},
	}

	result := buildClusterInventory("cluster-x", "THICK", aggregates, volumes, nil, 0, defaultStorageEfficiencyFactor)

	require.Len(t, result.Aggregates, 2)
	var totalUsed int64
	for _, a := range result.Aggregates {
		assert.Equal(t, int64(100), a.UsedSize,
			"FlexGroup volume size must be split evenly across backing aggregates")
		totalUsed += a.UsedSize
	}
	assert.Equal(t, int64(200), totalUsed, "sum of per-aggregate used must equal volume size")
}

func TestBuildClusterInventory_VolumeAggregateContribution_MultipleAggregates_OddSize(t *testing.T) {
	aggregates := []inventoryAggregate{
		{name: "aggr1", size: 2000},
		{name: "aggr2", size: 2000},
		{name: "aggr3", size: 2000},
	}
	volumes := []inventoryVolume{
		{
			uuid:           "flexgroup-uuid",
			name:           "fg-vol",
			aggregateNames: []string{"aggr1", "aggr2", "aggr3"},
			size:           100,
		},
	}

	result := buildClusterInventory("cluster-x", "THICK", aggregates, volumes, nil, 0, defaultStorageEfficiencyFactor)

	require.Len(t, result.Aggregates, 3)
	usedByName := map[string]int64{}
	var totalUsed int64
	for _, a := range result.Aggregates {
		usedByName[a.Name] = a.UsedSize
		totalUsed += a.UsedSize
	}
	assert.Equal(t, int64(100), totalUsed)
	assert.Equal(t, int64(34), usedByName["aggr1"])
	assert.Equal(t, int64(33), usedByName["aggr2"])
	assert.Equal(t, int64(33), usedByName["aggr3"])
}
