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
			SvmName:       "po-svm-01",
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
			SvmName:       "po-svm-01",
			Lifs:          []activities.PODataLifSpec{{Name: "lif-1", IPAddress: "10.1.1.10"}},
		}

		env := (&testsuite.WorkflowTestSuite{}).NewTestActivityEnvironment()
		act := newPOActivityWithClient(client)
		env.RegisterActivity(act.CreatePODataLifs)

		_, err := env.ExecuteActivity(act.CreatePODataLifs, in)
		require.Error(t, err)
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
			HostUUID: poTestHostUUID,
			SvmName:       "po-svm-01",
			SvmUUID:       "svm-uuid-1",
			ServiceLevel:  commonparams.ServiceLevelExtreme,
			SizeInBytes:   uint64(commonparams.BytesPerTiB),
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
			HostUUID: poTestHostUUID,
			SvmName:       "po-svm-01",
			SvmUUID:       "svm-uuid-1",
			ServiceLevel:  commonparams.ServiceLevelPremium,
			SizeInBytes:   uint64(commonparams.BytesPerTiB),
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
