package activities_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
)

// mockPOClusterClient is a hand-written testify mock for activities.POClusterClient.
type mockPOClusterClient struct {
	mock.Mock
}

func (m *mockPOClusterClient) GetNodeNames() ([]string, error) {
	args := m.Called()
	var names []string
	if v := args.Get(0); v != nil {
		names = v.([]string)
	}
	return names, args.Error(1)
}

func (m *mockPOClusterClient) EnsureSvm(in activities.CreatePOSvmInput) (string, error) {
	args := m.Called(in)
	return args.String(0), args.Error(1)
}

func (m *mockPOClusterClient) EnsureMgmtPolicies(svmUUID string) error {
	return m.Called(svmUUID).Error(0)
}

func (m *mockPOClusterClient) DeleteSvmByName(svmName string) error {
	return m.Called(svmName).Error(0)
}

func (m *mockPOClusterClient) EnsureIPSpace(name string) error {
	return m.Called(name).Error(0)
}

func (m *mockPOClusterClient) EnsureBroadcastDomain(ipSpace, name string) error {
	return m.Called(ipSpace, name).Error(0)
}

func (m *mockPOClusterClient) EnsureVlanNetPorts(ipSpace, broadcastDomain, basePort string, vlanTag int, nodeNames []string) error {
	return m.Called(ipSpace, broadcastDomain, basePort, vlanTag, nodeNames).Error(0)
}

func (m *mockPOClusterClient) EnsureDataLif(in activities.PODataLifSpec) error {
	return m.Called(in).Error(0)
}

func (m *mockPOClusterClient) CreateDefaultRoute(svmName, gateway, ipSpace string) error {
	return m.Called(svmName, gateway, ipSpace).Error(0)
}

func (m *mockPOClusterClient) ApplyQoSPolicy(in activities.ApplyQoSClientParams) error {
	return m.Called(in).Error(0)
}

func (m *mockPOClusterClient) ConfigureKmsOnSvm(in activities.ConfigureKmsOnSvmInput) error {
	return m.Called(in).Error(0)
}

func (m *mockPOClusterClient) CleanupPOAccountClusterNetwork(accountUUID, ipSpace string) error {
	return m.Called(accountUUID, ipSpace).Error(0)
}

func (m *mockPOClusterClient) HasSVMsInIPSpace(ipSpace string) (bool, error) {
	args := m.Called(ipSpace)
	return args.Bool(0), args.Error(1)
}

func (m *mockPOClusterClient) GetInterclusterLifs(servicePolicy string) ([]*vsa.InterclusterLif, error) {
	args := m.Called(servicePolicy)
	var lifs []*vsa.InterclusterLif
	if v := args.Get(0); v != nil {
		lifs = v.([]*vsa.InterclusterLif)
	}
	return lifs, args.Error(1)
}

func (m *mockPOClusterClient) CreateInterclusterLif(in activities.POInterclusterLifSpec) (*activities.POInterclusterLifResult, error) {
	args := m.Called(in)
	var res *activities.POInterclusterLifResult
	if v := args.Get(0); v != nil {
		res = v.(*activities.POInterclusterLifResult)
	}
	return res, args.Error(1)
}

func (m *mockPOClusterClient) DeleteInterclusterLif(externalUUID string) error {
	return m.Called(externalUUID).Error(0)
}

func (m *mockPOClusterClient) DeleteIPSpaceDefaultRoute(ipSpace, gateway string) error {
	return m.Called(ipSpace, gateway).Error(0)
}

func (m *mockPOClusterClient) DeleteVlanNetPorts(ipSpace, broadcastDomain, basePort string, vlanTag int, nodeNames []string) error {
	return m.Called(ipSpace, broadcastDomain, basePort, vlanTag, nodeNames).Error(0)
}

func (m *mockPOClusterClient) DeleteBroadcastDomain(ipSpace, name string) error {
	return m.Called(ipSpace, name).Error(0)
}

func (m *mockPOClusterClient) DeleteIPSpace(name string) error {
	return m.Called(name).Error(0)
}

const poTestHostUUID = "host-uuid-1"

func newPOActivityWithClient(client activities.POClusterClient) *activities.POOntapActivity {
	return &activities.POOntapActivity{
		ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
			return client, nil
		},
	}
}

func TestCreatePOSvm(t *testing.T) {
	t.Run("success returns uuid", func(t *testing.T) {
		in := activities.CreatePOSvmInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01", EnableNfs: true, IPSpace: "ipspace-1"}
		client := &mockPOClusterClient{}
		client.On("EnsureSvm", in).Return("svm-uuid-1", nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePOSvm)

		val, err := env.ExecuteActivity(act.CreatePOSvm, in)
		require.NoError(t, err)
		var out activities.CreatePOSvmOutput
		require.NoError(t, val.Get(&out))
		assert.Equal(t, "po-svm-01", out.SvmName)
		assert.Equal(t, "svm-uuid-1", out.SvmUUID)
		client.AssertExpectations(t)
	})

	t.Run("empty name rejected", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePOSvm)

		_, err := env.ExecuteActivity(act.CreatePOSvm, activities.CreatePOSvmInput{HostUUID: poTestHostUUID})
		require.Error(t, err)
		client.AssertNotCalled(t, "EnsureSvm", mock.Anything)
	})

	t.Run("client error", func(t *testing.T) {
		in := activities.CreatePOSvmInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01", IPSpace: "ipspace-1"}
		client := &mockPOClusterClient{}
		client.On("EnsureSvm", in).Return("", errors.New("ontap error"))

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePOSvm)

		_, err := env.ExecuteActivity(act.CreatePOSvm, in)
		require.Error(t, err)
	})
}

func TestCreatePODataLifs(t *testing.T) {
	t.Run("creates each lif", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("EnsureDataLif", mock.Anything).Return(nil).Twice()

		in := activities.CreatePODataLifsInput{
			HostUUID: poTestHostUUID,
			SvmName:  "po-svm-01",
			Lifs: []activities.PODataLifSpec{
				{Name: "lif-1", IPAddress: "10.1.1.10"},
				{Name: "lif-2", IPAddress: "10.1.1.11"},
			},
		}

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODataLifs)

		_, err := env.ExecuteActivity(act.CreatePODataLifs, in)
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("empty lifs rejected", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODataLifs)

		_, err := env.ExecuteActivity(act.CreatePODataLifs, activities.CreatePODataLifsInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01"})
		require.Error(t, err)
	})

	t.Run("lif creation error", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("EnsureDataLif", mock.Anything).Return(errors.New("lif error")).Once()

		in := activities.CreatePODataLifsInput{
			HostUUID: poTestHostUUID,
			SvmName:  "po-svm-01",
			Lifs:     []activities.PODataLifSpec{{Name: "lif-1", IPAddress: "10.1.1.10"}},
		}

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODataLifs)

		_, err := env.ExecuteActivity(act.CreatePODataLifs, in)
		require.Error(t, err)
	})
}

func TestCreatePOInterclusterLif(t *testing.T) {
	lif := activities.POInterclusterLifSpec{
		Name:          "ic-lif-node-1",
		IPAddress:     "10.10.0.10",
		Netmask:       "255.255.255.240",
		HomeNode:      "node-1",
		HomePort:      "a0a-4001",
		ServicePolicy: commonparams.POInterclusterServicePolicy,
		IPSpace:       "ipspace_acct_4001",
	}

	t.Run("creates cluster-scoped lif", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("CreateInterclusterLif", lif).Return(&activities.POInterclusterLifResult{
			Name: "ic-lif-node-1", ExternalUUID: "ext-uuid-1",
		}, nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePOInterclusterLif)

		val, err := env.ExecuteActivity(act.CreatePOInterclusterLif, activities.CreatePOInterclusterLifInput{
			HostUUID: poTestHostUUID,
			Lif:      lif,
		})
		require.NoError(t, err)
		var got activities.POInterclusterLifResult
		require.NoError(t, val.Get(&got))
		assert.Equal(t, "ext-uuid-1", got.ExternalUUID)
		client.AssertExpectations(t)
	})

	t.Run("rejects empty ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePOInterclusterLif)

		empty := lif
		empty.IPSpace = ""
		_, err := env.ExecuteActivity(act.CreatePOInterclusterLif, activities.CreatePOInterclusterLifInput{
			HostUUID: poTestHostUUID,
			Lif:      empty,
		})
		require.Error(t, err)
		client.AssertNotCalled(t, "CreateInterclusterLif", mock.Anything)
	})
}

func TestEnsurePOIPSpace(t *testing.T) {
	t.Run("creates ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("EnsureIPSpace", "ipspace_acct_100").Return(nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOIPSpace)

		_, err := env.ExecuteActivity(act.EnsurePOIPSpace, activities.EnsurePOIPSpaceInput{
			HostUUID: poTestHostUUID,
			Name:     "ipspace_acct_100",
		})
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("rejects empty name", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOIPSpace)

		_, err := env.ExecuteActivity(act.EnsurePOIPSpace, activities.EnsurePOIPSpaceInput{HostUUID: poTestHostUUID})
		require.Error(t, err)
		client.AssertNotCalled(t, "EnsureIPSpace", mock.Anything)
	})
}

func TestEnsurePOVlanPorts(t *testing.T) {
	t.Run("creates per-node vlan ports", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("EnsureVlanNetPorts", "ipspace_acct_100", "bdomain_acct_100", "a0a", 100, []string{"node-1", "node-2"}).Return(nil).Once()

		in := activities.EnsurePOVlanPortsInput{
			HostUUID:        poTestHostUUID,
			IPSpace:         "ipspace_acct_100",
			BroadcastDomain: "bdomain_acct_100",
			BasePort:        "a0a",
			VlanTag:         100,
			NodeNames:       []string{"node-1", "node-2"},
		}

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOVlanPorts)

		_, err := env.ExecuteActivity(act.EnsurePOVlanPorts, in)
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("no-op when no vlan/nodes", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOVlanPorts)

		_, err := env.ExecuteActivity(act.EnsurePOVlanPorts, activities.EnsurePOVlanPortsInput{HostUUID: poTestHostUUID, BroadcastDomain: "bdomain_acct_100", VlanTag: 0})
		require.NoError(t, err)
		client.AssertNotCalled(t, "EnsureVlanNetPorts", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("propagates client error", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("EnsureVlanNetPorts", "Default", "bdomain_acct_100", "a0a", 100, []string{"node-1"}).Return(errors.New("vlan error")).Once()

		in := activities.EnsurePOVlanPortsInput{
			HostUUID:        poTestHostUUID,
			BroadcastDomain: "bdomain_acct_100",
			BasePort:        "a0a",
			VlanTag:         100,
			NodeNames:       []string{"node-1"},
		}

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOVlanPorts)

		_, err := env.ExecuteActivity(act.EnsurePOVlanPorts, in)
		require.Error(t, err)
	})
}

func TestApplyQoSPolicy(t *testing.T) {
	t.Run("extreme tier derives ceiling from offering x size", func(t *testing.T) {
		// 1 TiB at the default EXTREME multiplier (128 MiBps/TiB) → 128 MiBps.
		in := activities.ApplyQoSPolicyInput{
			HostUUID:     poTestHostUUID,
			SvmName:      "po-svm-01",
			SvmUUID:      "svm-uuid-1",
			ServiceLevel: commonparams.ServiceLevelExtreme,
			SizeInBytes:  uint64(commonparams.BytesPerTiB),
		}
		client := &mockPOClusterClient{}
		client.On("ApplyQoSPolicy", activities.ApplyQoSClientParams{
			SvmName:       "po-svm-01",
			SvmUUID:       "svm-uuid-1",
			PolicyName:    commonparams.POExtremeQoSPolicyName,
			MaxThroughput: 128,
			MaxIOPS:       0,
		}).Return(nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.ApplyQoSPolicy)

		_, err := env.ExecuteActivity(act.ApplyQoSPolicy, in)
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("premium tier uses premium policy", func(t *testing.T) {
		// 1 TiB at the default PREMIUM multiplier (64 MiBps/TiB) → 64 MiBps.
		in := activities.ApplyQoSPolicyInput{
			HostUUID:     poTestHostUUID,
			SvmName:      "po-svm-01",
			SvmUUID:      "svm-uuid-1",
			ServiceLevel: commonparams.ServiceLevelPremium,
			SizeInBytes:  uint64(commonparams.BytesPerTiB),
		}
		client := &mockPOClusterClient{}
		client.On("ApplyQoSPolicy", activities.ApplyQoSClientParams{
			SvmName:       "po-svm-01",
			SvmUUID:       "svm-uuid-1",
			PolicyName:    commonparams.POPremiumQoSPolicyName,
			MaxThroughput: 64,
			MaxIOPS:       0,
		}).Return(nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.ApplyQoSPolicy)

		_, err := env.ExecuteActivity(act.ApplyQoSPolicy, in)
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("empty svm uuid rejected", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.ApplyQoSPolicy)

		_, err := env.ExecuteActivity(act.ApplyQoSPolicy, activities.ApplyQoSPolicyInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01"})
		require.Error(t, err)
	})

	t.Run("unsupported service level rejected", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.ApplyQoSPolicy)

		_, err := env.ExecuteActivity(act.ApplyQoSPolicy, activities.ApplyQoSPolicyInput{
			HostUUID: poTestHostUUID, SvmName: "po-svm-01", SvmUUID: "svm-uuid-1", ServiceLevel: "STANDARD",
		})
		require.Error(t, err)
	})
}

func TestConfigureKmsOnSvm(t *testing.T) {
	t.Run("no-op when kms config id empty", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.ConfigureKmsOnSvm)

		_, err := env.ExecuteActivity(act.ConfigureKmsOnSvm, activities.ConfigureKmsOnSvmInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01"})
		require.NoError(t, err)
		client.AssertNotCalled(t, "ConfigureKmsOnSvm", mock.Anything)
	})

	t.Run("configures when requested", func(t *testing.T) {
		in := activities.ConfigureKmsOnSvmInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01", KmsConfigId: "kms-1", KeyName: "k"}
		client := &mockPOClusterClient{}
		client.On("ConfigureKmsOnSvm", in).Return(nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.ConfigureKmsOnSvm)

		_, err := env.ExecuteActivity(act.ConfigureKmsOnSvm, in)
		require.NoError(t, err)
		client.AssertExpectations(t)
	})
}

func TestDeletePOSvm(t *testing.T) {
	t.Run("no-op when name empty", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.DeletePOSvm)

		_, err := env.ExecuteActivity(act.DeletePOSvm, activities.DeletePOSvmInput{HostUUID: poTestHostUUID})
		require.NoError(t, err)
		client.AssertNotCalled(t, "DeleteSvmByName", mock.Anything)
	})

	t.Run("deletes by name", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("DeleteSvmByName", "po-svm-01").Return(nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.DeletePOSvm)

		_, err := env.ExecuteActivity(act.DeletePOSvm, activities.DeletePOSvmInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01"})
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("delete error", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("DeleteSvmByName", "po-svm-01").Return(errors.New("delete failed"))

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.DeletePOSvm)

		_, err := env.ExecuteActivity(act.DeletePOSvm, activities.DeletePOSvmInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01"})
		require.Error(t, err)
	})
}

func TestGetPONodeNames(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("GetNodeNames").Return([]string{"node-1"}, nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.GetPONodeNames)

		val, err := env.ExecuteActivity(act.GetPONodeNames, activities.GetPONodeNamesInput{HostUUID: poTestHostUUID})
		require.NoError(t, err)
		var nodes []string
		require.NoError(t, val.Get(&nodes))
		assert.Equal(t, []string{"node-1"}, nodes)
	})

	t.Run("empty nodes rejected", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("GetNodeNames").Return([]string{}, nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.GetPONodeNames)

		_, err := env.ExecuteActivity(act.GetPONodeNames, activities.GetPONodeNamesInput{HostUUID: poTestHostUUID})
		require.Error(t, err)
	})
}

func TestEnsurePOBroadcastDomain(t *testing.T) {
	t.Run("creates broadcast domain", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("EnsureBroadcastDomain", "ipspace-1", "bdomain_acct_100").Return(nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOBroadcastDomain)

		_, err := env.ExecuteActivity(act.EnsurePOBroadcastDomain, activities.EnsurePOBroadcastDomainInput{
			HostUUID: poTestHostUUID,
			IPSpace:  "ipspace-1",
			Name:     "bdomain_acct_100",
		})
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("rejects empty ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOBroadcastDomain)

		_, err := env.ExecuteActivity(act.EnsurePOBroadcastDomain, activities.EnsurePOBroadcastDomainInput{
			HostUUID: poTestHostUUID,
			Name:     "bdomain_acct_100",
		})
		require.Error(t, err)
		client.AssertNotCalled(t, "EnsureBroadcastDomain", mock.Anything, mock.Anything)
	})

	t.Run("no-op when name empty", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.EnsurePOBroadcastDomain)

		_, err := env.ExecuteActivity(act.EnsurePOBroadcastDomain, activities.EnsurePOBroadcastDomainInput{HostUUID: poTestHostUUID})
		require.NoError(t, err)
		client.AssertNotCalled(t, "EnsureBroadcastDomain", mock.Anything, mock.Anything)
	})
}

func TestCreatePODefaultRoute(t *testing.T) {
	t.Run("creates route", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("CreateDefaultRoute", "po-svm-01", "10.1.1.1", "ipspace-1").Return(nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODefaultRoute)

		_, err := env.ExecuteActivity(act.CreatePODefaultRoute, activities.CreatePODefaultRouteInput{
			HostUUID: poTestHostUUID,
			SvmName:  "po-svm-01",
			Gateway:  "10.1.1.1",
			IPSpace:  "ipspace-1",
		})
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("already exists is tolerated by client", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("CreateDefaultRoute", "", "10.0.0.1", "ipspace-ic").Return(nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODefaultRoute)

		_, err := env.ExecuteActivity(act.CreatePODefaultRoute, activities.CreatePODefaultRouteInput{
			HostUUID: poTestHostUUID,
			SvmName:  "",
			Gateway:  "10.0.0.1",
			IPSpace:  "ipspace-ic",
		})
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("propagates client error", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("CreateDefaultRoute", "po-svm-01", "10.1.1.1", "ipspace-1").Return(errors.New("cluster off")).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODefaultRoute)

		_, err := env.ExecuteActivity(act.CreatePODefaultRoute, activities.CreatePODefaultRouteInput{
			HostUUID: poTestHostUUID,
			SvmName:  "po-svm-01",
			Gateway:  "10.1.1.1",
			IPSpace:  "ipspace-1",
		})
		require.Error(t, err)
		client.AssertExpectations(t)
	})

	t.Run("no-op when gateway empty", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODefaultRoute)

		_, err := env.ExecuteActivity(act.CreatePODefaultRoute, activities.CreatePODefaultRouteInput{HostUUID: poTestHostUUID, SvmName: "po-svm-01"})
		require.NoError(t, err)
		client.AssertNotCalled(t, "CreateDefaultRoute", mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestIsLastActiveSVMOnIPSpace(t *testing.T) {
	ctx := context.Background()
	const ipSpace = "ipspace_acctuuid_100"

	t.Run("last svm when count is zero", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("CountActiveSVMsByIPSpace", ctx, ipSpace).Return(int64(0), nil)
		isLast, err := activities.IsLastActiveSVMOnIPSpace(ctx, mockSE, ipSpace)
		require.NoError(t, err)
		assert.True(t, isLast)
	})

	t.Run("not last when siblings remain", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("CountActiveSVMsByIPSpace", ctx, ipSpace).Return(int64(1), nil)
		isLast, err := activities.IsLastActiveSVMOnIPSpace(ctx, mockSE, ipSpace)
		require.NoError(t, err)
		assert.False(t, isLast)
	})

	t.Run("empty ipspace", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		_, err := activities.IsLastActiveSVMOnIPSpace(ctx, mockSE, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ipspace must not be empty")
	})

	t.Run("nil storage", func(t *testing.T) {
		_, err := activities.IsLastActiveSVMOnIPSpace(ctx, nil, ipSpace)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "storage is not configured")
	})

	t.Run("count error", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		mockSE.On("CountActiveSVMsByIPSpace", ctx, ipSpace).Return(int64(0), errors.New("db error"))
		_, err := activities.IsLastActiveSVMOnIPSpace(ctx, mockSE, ipSpace)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "db error")
	})
}

func TestCleanupPOAccountClusterNetwork(t *testing.T) {
	const ipSpace = "ipspace_acctuuid_100"

	t.Run("success", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("CleanupPOAccountClusterNetwork", "acct-uuid", ipSpace).Return(nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CleanupPOAccountClusterNetwork)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetwork, activities.CleanupClusterNetworkInput{
			HostUUID:    poTestHostUUID,
			AccountUUID: "acct-uuid",
			IPSpace:     ipSpace,
		})
		require.NoError(t, err)
		client.AssertExpectations(t)
	})

	t.Run("empty account uuid", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CleanupPOAccountClusterNetwork)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetwork, activities.CleanupClusterNetworkInput{
			HostUUID: poTestHostUUID,
			IPSpace:  ipSpace,
		})
		require.Error(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
	})

	t.Run("empty ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CleanupPOAccountClusterNetwork)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetwork, activities.CleanupClusterNetworkInput{
			HostUUID:    poTestHostUUID,
			AccountUUID: "acct-uuid",
		})
		require.Error(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
	})

	t.Run("ipspace account mismatch", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CleanupPOAccountClusterNetwork)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetwork, activities.CleanupClusterNetworkInput{
			HostUUID:    poTestHostUUID,
			AccountUUID: "acct-uuid",
			IPSpace:     "ipspace_other_100",
		})
		require.Error(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
	})

	t.Run("client error", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("CleanupPOAccountClusterNetwork", "acct-uuid", ipSpace).Return(errors.New("cleanup failed"))

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CleanupPOAccountClusterNetwork)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetwork, activities.CleanupClusterNetworkInput{
			HostUUID:    poTestHostUUID,
			AccountUUID: "acct-uuid",
			IPSpace:     ipSpace,
		})
		require.Error(t, err)
		client.AssertExpectations(t)
	})
}

func TestCleanupPOAccountClusterNetworkIfIPRangeUnused(t *testing.T) {
	const ipSpace = "ipspace_acctuuid_100"
	in := activities.CleanupClusterNetworkIfIPSpaceUnusedInput{
		HostUUID:    poTestHostUUID,
		AccountUUID: "acct-uuid",
		IPSpace:     ipSpace,
	}

	t.Run("cleans network when last svm on ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("HasSVMsInIPSpace", ipSpace).Return(false, nil)
		client.On("CleanupPOAccountClusterNetwork", "acct-uuid", ipSpace).Return(nil)
		se := database.NewMockStorage(t)
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).
			Return(int64(0), nil).Twice()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, in)
		require.NoError(t, err)
		client.AssertExpectations(t)
		se.AssertExpectations(t)
	})

	t.Run("cleans network when global ip range still in use but ipspace empty", func(t *testing.T) {
		// Regression: po-e2e-b delete must not be blocked by po-e2e-c LIFs on a
		// different ipspace sharing the same ip_ranges row (see TestCountActiveSVMsByIPSpace).
		client := &mockPOClusterClient{}
		client.On("HasSVMsInIPSpace", ipSpace).Return(false, nil)
		client.On("CleanupPOAccountClusterNetwork", "acct-uuid", ipSpace).Return(nil)
		se := database.NewMockStorage(t)
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).
			Return(int64(0), nil).Twice()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, in)
		require.NoError(t, err)
		client.AssertCalled(t, "CleanupPOAccountClusterNetwork", "acct-uuid", ipSpace)
	})

	t.Run("rollback entry point cleans network when last svm on ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("HasSVMsInIPSpace", ipSpace).Return(false, nil)
		client.On("CleanupPOAccountClusterNetwork", "acct-uuid", ipSpace).Return(nil)
		se := database.NewMockStorage(t)
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).
			Return(int64(0), nil).Twice()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnusedRollback)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnusedRollback, in, "rollback error")
		require.NoError(t, err)
		client.AssertExpectations(t)
		se.AssertExpectations(t)
	})

	t.Run("skips when ONTAP still has svms on ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("HasSVMsInIPSpace", ipSpace).Return(true, nil)
		se := database.NewMockStorage(t)
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).
			Return(int64(0), nil).Twice()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, in)
		require.NoError(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
	})

	t.Run("ontap list error fails activity", func(t *testing.T) {
		client := &mockPOClusterClient{}
		client.On("HasSVMsInIPSpace", ipSpace).Return(false, errors.New("ontap list failed"))
		se := database.NewMockStorage(t)
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).
			Return(int64(0), nil).Twice()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, in)
		require.Error(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
	})

	t.Run("skips when ipspace input empty", func(t *testing.T) {
		client := &mockPOClusterClient{}
		se := database.NewMockStorage(t)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		emptyIn := in
		emptyIn.IPSpace = ""
		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, emptyIn)
		require.NoError(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
		se.AssertNotCalled(t, "CountActiveSVMsByIPSpace", mock.Anything, mock.Anything)
	})

	t.Run("nil storage", func(t *testing.T) {
		client := &mockPOClusterClient{}
		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, in)
		require.Error(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
	})

	t.Run("skips cleanup when other svms remain on ipspace", func(t *testing.T) {
		client := &mockPOClusterClient{}
		se := database.NewMockStorage(t)
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).Return(int64(1), nil)

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, in)
		require.NoError(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
	})

	t.Run("aborts cleanup when new svm appears before destructive delete", func(t *testing.T) {
		client := &mockPOClusterClient{}
		se := database.NewMockStorage(t)
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).
			Return(int64(0), nil).Once()
		se.On("CountActiveSVMsByIPSpace", mock.Anything, ipSpace).
			Return(int64(1), nil).Once()

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := &activities.POOntapActivity{
			SE: se,
			ClientFactory: func(_ context.Context, _ string) (activities.POClusterClient, error) {
				return client, nil
			},
		}
		env.RegisterActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused)

		_, err := env.ExecuteActivity(act.CleanupPOAccountClusterNetworkIfIPRangeUnused, in)
		require.NoError(t, err)
		client.AssertNotCalled(t, "CleanupPOAccountClusterNetwork", mock.Anything, mock.Anything)
		se.AssertExpectations(t)
	})
}
