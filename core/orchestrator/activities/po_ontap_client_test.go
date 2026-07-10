package activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

type stubPOProvider struct {
	vsa.Provider
	version string
}

func (s *stubPOProvider) GetONTAPVersion() (*string, error) {
	return &s.version, nil
}

func TestDefaultPOClusterClient_UsesHostFromDB(t *testing.T) {
	t.Cleanup(func() {
		poGetProviderForHost = vsa.GetProviderForHost
	})

	host := &datamodel.Host{
		BaseModel: datamodel.BaseModel{UUID: "host-uuid-1"},
		IPAddress: "10.0.0.1",
		Username:  "cluster-admin",
		Password:  "encrypted",
	}

	se := database.NewMockStorage(t)
	se.On("GetHost", mock.Anything, "host-uuid-1").Return(host, nil)

	var providerHost *datamodel.Host
	poGetProviderForHost = func(_ context.Context, h *datamodel.Host) (vsa.Provider, error) {
		providerHost = h
		return &stubPOProvider{version: "9.17.1"}, nil
	}

	act := &POOntapActivity{SE: se}
	client, err := act.defaultPOClusterClient(context.Background(), "host-uuid-1")
	require.NoError(t, err)
	require.NotNil(t, client)
	require.NotNil(t, providerHost)
	assert.Equal(t, host.UUID, providerHost.UUID)
	se.AssertExpectations(t)
}

func TestDefaultPOClusterClient_RequiresHostUUID(t *testing.T) {
	act := &POOntapActivity{SE: database.NewMockStorage(t)}
	_, err := act.defaultPOClusterClient(context.Background(), "")
	require.Error(t, err)
}

func TestDefaultPOClusterClient_RequiresStorage(t *testing.T) {
	act := &POOntapActivity{}
	_, err := act.defaultPOClusterClient(context.Background(), "host-uuid-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "storage is not configured")
}

func TestDefaultPOClusterClient_HostLookupError(t *testing.T) {
	se := database.NewMockStorage(t)
	se.On("GetHost", mock.Anything, "host-uuid-1").
		Return(nil, errors.New("db unavailable"))

	act := &POOntapActivity{SE: se}
	_, err := act.defaultPOClusterClient(context.Background(), "host-uuid-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "load PO host")
	se.AssertExpectations(t)
}

func TestIsAlreadyExistsErr(t *testing.T) {
	assert.False(t, isAlreadyExistsErr(nil))
	assert.True(t, isAlreadyExistsErr(errors.New("resource already exists")))
	assert.True(t, isAlreadyExistsErr(errors.New("has already been created")))
	assert.True(t, isAlreadyExistsErr(errors.New("duplicate entry")))
	assert.True(t, isAlreadyExistsErr(errors.New("409 conflict")))
	assert.False(t, isAlreadyExistsErr(errors.New("connection refused")))
}

type poClusterClientFixture struct {
	client  *poClusterClient
	provider *vsa.MockProvider
	rest    *ontapRest.MockRESTClient
	net     *ontapRest.MockNetworkingClient
	svm     *ontapRest.MockSVMClient
}

func newPOClusterClientFixture(t *testing.T) *poClusterClientFixture {
	t.Helper()
	provider := vsa.NewMockProvider(t)
	rest := ontapRest.NewMockRESTClient(t)
	net := ontapRest.NewMockNetworkingClient(t)
	svmClient := ontapRest.NewMockSVMClient(t)

	provider.On("CreateRESTClient").Return(rest, nil).Maybe()
	rest.On("Networking").Return(net).Maybe()
	rest.On("SVM").Return(svmClient).Maybe()

	return &poClusterClientFixture{
		client:   &poClusterClient{provider: provider},
		provider: provider,
		rest:     rest,
		net:      net,
		svm:      svmClient,
	}
}

func TestPOClusterClient_GetNodeNames(t *testing.T) {
	f := newPOClusterClientFixture(t)
	f.provider.On("GetNodes").Return([]*vsa.Node{
		{Name: "node-1"},
		nil,
		{Name: ""},
		{Name: "node-2"},
	}, nil)

	nodes, err := f.client.GetNodeNames()
	require.NoError(t, err)
	assert.Equal(t, []string{"node-1", "node-2"}, nodes)
}

func TestPOClusterClient_EnsureIPSpace(t *testing.T) {
	t.Run("existing ipspace", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("IpspaceExists", "ipspace-1").Return(true, nil)
		require.NoError(t, f.client.EnsureIPSpace("ipspace-1"))
	})

	t.Run("creates ipspace", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("IpspaceExists", "ipspace-1").Return(false, nil)
		f.net.On("IpspaceCreate", "ipspace-1").Return(nil)
		require.NoError(t, f.client.EnsureIPSpace("ipspace-1"))
	})
}

func TestPOClusterClient_EnsureBroadcastDomain(t *testing.T) {
	t.Run("existing domain", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetBroadcastDomainGet", mock.Anything).Return(&ontapRest.BroadcastDomain{}, nil)
		require.NoError(t, f.client.EnsureBroadcastDomain("Default", "bdomain-1"))
	})

	t.Run("creates domain", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetBroadcastDomainGet", mock.Anything).Return(nil, utilErrors.NewNotFoundErr("broadcast domain", nil))
		f.net.On("NetworkEthernetBroadcastDomainCreate", mock.Anything).Return(nil)
		require.NoError(t, f.client.EnsureBroadcastDomain("ipspace-1", "bdomain-1"))
	})

	t.Run("propagates lookup errors", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetBroadcastDomainGet", mock.Anything).Return(nil, errors.New("cluster unreachable"))
		err := f.client.EnsureBroadcastDomain("ipspace-1", "bdomain-1")
		require.Error(t, err)
	})
}

func TestPOClusterClient_EnsureVlanNetPorts(t *testing.T) {
	t.Run("skips configured nodes and creates missing", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		nodeName := "node-1"
		f.net.On("NetworkEthernetPortsGet", mock.Anything).Return([]*ontapRest.NetworkPort{
			{Port: models.Port{Node: &models.PortInlineNode{Name: &nodeName}}},
		}, nil)
		f.net.On("NetworkEthernetPortCreateVLAN", mock.Anything).Return(nil).Once()
		require.NoError(t, f.client.EnsureVlanNetPorts("Default", "bdomain-1", "a0a", 100, []string{"node-1", "node-2"}))
	})

	t.Run("tolerates already exists", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetPortsGet", mock.Anything).Return(nil, nil)
		f.net.On("NetworkEthernetPortCreateVLAN", mock.Anything).Return(errors.New("vlan already exists")).Once()
		require.NoError(t, f.client.EnsureVlanNetPorts("Default", "bdomain-1", "a0a", 100, []string{"node-1"}))
	})

	t.Run("propagates other errors", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkEthernetPortsGet", mock.Anything).Return(nil, nil)
		f.net.On("NetworkEthernetPortCreateVLAN", mock.Anything).Return(errors.New("permission denied")).Once()
		err := f.client.EnsureVlanNetPorts("Default", "bdomain-1", "a0a", 100, []string{"node-1"})
		require.Error(t, err)
	})
}

func TestPOClusterClient_CreateDefaultRoute(t *testing.T) {
	f := newPOClusterClientFixture(t)
	f.provider.On("CreateNetworkIpRoute", vsa.CreateNetworkIPRouteParams{
		SvmName: "svm-1",
		Gateway: "10.1.1.1",
		IPSpace: "ipspace-1",
	}).Return(nil)
	require.NoError(t, f.client.CreateDefaultRoute("svm-1", "10.1.1.1", "ipspace-1"))

	err := f.client.CreateDefaultRoute("svm-1", "10.1.1.1", "")
	require.Error(t, err)
}

func TestPOClusterClient_EnsureSvm(t *testing.T) {
	t.Run("reuses existing svm", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		uuid := "svm-uuid-1"
		f.svm.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{Svm: models.Svm{UUID: &uuid}}, nil)
		got, err := f.client.EnsureSvm(CreatePOSvmInput{SvmName: "po-svm-01", IPSpace: "ipspace-1"})
		require.NoError(t, err)
		assert.Equal(t, "svm-uuid-1", got)
	})

	t.Run("creates svm", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.svm.On("SvmGet", mock.Anything).Return(nil, utilErrors.NewNotFoundErr("svm", nil))
		f.provider.On("CreateSVM", mock.Anything).Return(&vsa.ProviderResponse{ExternalUUID: "new-svm-uuid"}, nil)
		got, err := f.client.EnsureSvm(CreatePOSvmInput{SvmName: "po-svm-01", EnableNfs: true, IPSpace: "ipspace-1"})
		require.NoError(t, err)
		assert.Equal(t, "new-svm-uuid", got)
	})

	t.Run("requires ipspace", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		_, err := f.client.EnsureSvm(CreatePOSvmInput{SvmName: "po-svm-01"})
		require.Error(t, err)
	})

	t.Run("empty create response", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.svm.On("SvmGet", mock.Anything).Return(nil, utilErrors.NewNotFoundErr("svm", nil))
		f.provider.On("CreateSVM", mock.Anything).Return(nil, nil)
		_, err := f.client.EnsureSvm(CreatePOSvmInput{SvmName: "po-svm-01", IPSpace: "ipspace-1"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty response creating SVM")
	})
}

func TestPOClusterClient_DeleteSvmByName(t *testing.T) {
	t.Run("not found is success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.svm.On("SvmGet", mock.Anything).Return(nil, utilErrors.NewNotFoundErr("svm", nil))
		require.NoError(t, f.client.DeleteSvmByName("po-svm-01"))
	})

	t.Run("lookup error propagates", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.svm.On("SvmGet", mock.Anything).Return(nil, errors.New("cluster unreachable"))
		err := f.client.DeleteSvmByName("po-svm-01")
		require.Error(t, err)
	})

	t.Run("sync delete", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		uuid := "svm-uuid-1"
		f.svm.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{Svm: models.Svm{UUID: &uuid}}, nil)
		f.svm.On("SvmDelete", "svm-uuid-1").Return(true, nil, nil)
		require.NoError(t, f.client.DeleteSvmByName("po-svm-01"))
	})

	t.Run("async delete polls job", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		uuid := "svm-uuid-1"
		f.svm.On("SvmGet", mock.Anything).Return(&ontapRest.Svm{Svm: models.Svm{UUID: &uuid}}, nil)
		f.svm.On("SvmDelete", "svm-uuid-1").Return(false, &ontapRest.JobAccepted{JobUUID: "job-1"}, nil)
		f.rest.On("Poll", "job-1").Return(nil)
		require.NoError(t, f.client.DeleteSvmByName("po-svm-01"))
	})
}

func TestPOClusterClient_EnsureDataLif(t *testing.T) {
	t.Run("uses provided home node", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).Return(nil).Once()
		f.provider.On("CreateDataLIF", vsa.CreateLifParams{
			Name:          "lif-1",
			SvmName:       "svm-1",
			IpAddress:     "10.1.1.10",
			NodeName:      "node-1",
			HomePort:      "vlan-100",
			Netmask:       "255.255.255.0",
			ServicePolicy: "default-data-files",
		}).Return(&vsa.Lif{}, nil)
		require.NoError(t, f.client.EnsureDataLif(PODataLifSpec{
			SvmName: "svm-1", Name: "lif-1", IPAddress: "10.1.1.10",
			HomeNode: "node-1", HomePort: "vlan-100", Netmask: "255.255.255.0",
			ServicePolicy: "default-data-files",
		}))
	})

	t.Run("defaults home node from cluster", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).Return(nil).Once()
		f.provider.On("GetNodes").Return([]*vsa.Node{{Name: "node-1"}}, nil)
		f.provider.On("CreateDataLIF", mock.MatchedBy(func(p vsa.CreateLifParams) bool {
			return p.NodeName == "node-1"
		})).Return(&vsa.Lif{}, nil)
		require.NoError(t, f.client.EnsureDataLif(PODataLifSpec{
			SvmName: "svm-1", Name: "lif-1", IPAddress: "10.1.1.10",
		}))
	})

	t.Run("no nodes available", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.provider.On("GetNodes").Return(nil, nil)
		err := f.client.EnsureDataLif(PODataLifSpec{SvmName: "svm-1", Name: "lif-1", IPAddress: "10.1.1.10"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no nodes available")
	})

	t.Run("existing lif skips create", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			cb := args.Get(1).(ontapRest.UserCallbackFunc[[]*ontapRest.IPInterface])
			require.NoError(t, cb([]*ontapRest.IPInterface{{}}))
		}).Return(nil).Once()
		require.NoError(t, f.client.EnsureDataLif(PODataLifSpec{
			SvmName: "svm-1", Name: "lif-1", IPAddress: "10.1.1.10", HomeNode: "node-1",
		}))
	})

	t.Run("create conflict treated as success", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.net.On("NetworkIPInterfacesGet", mock.Anything, mock.Anything).Return(nil).Once()
		f.provider.On("CreateDataLIF", mock.Anything).Return(nil, errors.New("409 conflict"))
		require.NoError(t, f.client.EnsureDataLif(PODataLifSpec{
			SvmName: "svm-1", Name: "lif-1", IPAddress: "10.1.1.10", HomeNode: "node-1",
		}))
	})
}

func TestPOClusterClient_ApplyQoSPolicy(t *testing.T) {
	t.Run("existing policy", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.provider.On("FindQoSGroupPolicy", mock.Anything).Return(&vsa.QoSGroupPolicyResponse{}, nil)
		f.provider.On("ModifySVMWithQoSPolicy", mock.Anything).Return(nil)
		require.NoError(t, f.client.ApplyQoSPolicy(ApplyQoSClientParams{
			SvmName: "svm-1", SvmUUID: "svm-uuid-1", PolicyName: "extreme_auto", MaxThroughput: 128,
		}))
	})

	t.Run("creates policy then modifies svm", func(t *testing.T) {
		f := newPOClusterClientFixture(t)
		f.provider.On("FindQoSGroupPolicy", mock.Anything).Return(nil, errors.New("not found"))
		f.provider.On("CreateQoSGroupPolicy", mock.Anything).Return(&vsa.QoSGroupPolicyResponse{}, nil)
		f.provider.On("ModifySVMWithQoSPolicy", mock.Anything).Return(nil)
		require.NoError(t, f.client.ApplyQoSPolicy(ApplyQoSClientParams{
			SvmName: "svm-1", SvmUUID: "svm-uuid-1", PolicyName: "extreme_auto", MaxThroughput: 128,
		}))
	})
}

func TestPOClusterClient_ConfigureKmsOnSvm(t *testing.T) {
	f := newPOClusterClientFixture(t)
	in := ConfigureKmsOnSvmInput{SvmName: "svm-1", KeyName: "key-1"}
	f.provider.On("CreateKmsConfig", mock.Anything).Return(&vsa.CreateKmsConfigResponse{}, nil)
	require.NoError(t, f.client.ConfigureKmsOnSvm(in))
}
