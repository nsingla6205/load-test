package active_directory_activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	ontaprestmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/ontap-rest/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	ontapRest "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/ontap-rest"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"go.temporal.io/sdk/testsuite"
)

// vlm.VLMConfig has custom JSON (un)marshalling that requires an active cloud
// provider to be registered. The diagnose activities pass *vlm.VLMConfig across
// the Temporal test activity boundary (which serializes/deserializes args), so
// register a provider once for the whole package's tests.
func init() {
	vlm.SetActiveProvider(vlm.GCPCloud)
}

// -----------------------------------------------------------------------------
// Test helpers / stubs
// -----------------------------------------------------------------------------

// stubCifsDeleter embeds vsa.Provider so it satisfies the full Provider
// interface (returned by vsa.GetProviderByNode) while overriding only
// DeleteCIFSServer, which is the narrow surface DeleteDiagnosisCifsServer
// type-asserts to (cifsServerDeleter).
type stubCifsDeleter struct {
	vsa.Provider
	deleteCIFSServerFn func(externalSVMUUID, adUsername, adPassword string) error
}

func (s *stubCifsDeleter) DeleteCIFSServer(externalSVMUUID, adUsername, adPassword string) error {
	return s.deleteCIFSServerFn(externalSVMUUID, adUsername, adPassword)
}

// stubKerberosCleaner embeds vsa.Provider and overrides the two methods
// DeleteDiagnosisKerberosConfig type-asserts to (kerberosDiagnosisCleaner).
type stubKerberosCleaner struct {
	vsa.Provider
	getInterfacesFn func(svmUUID, svmName string) ([]*ontapRest.KerberosInterface, error)
	disableFn       func(params vsa.KerberosInterfaceModifyParams) error
}

func (s *stubKerberosCleaner) GetKerberosInterfacesForSVM(svmUUID, svmName string) ([]*ontapRest.KerberosInterface, error) {
	return s.getInterfacesFn(svmUUID, svmName)
}

func (s *stubKerberosCleaner) DisableKerberosOnInterface(params vsa.KerberosInterfaceModifyParams) error {
	return s.disableFn(params)
}

// withProviderByNode monkey-patches vsa.GetProviderByNode to return the given
// provider/error and returns a restore func.
func withProviderByNode(p vsa.Provider, err error) func() {
	orig := vsa.GetProviderByNode
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return p, err
	}
	return func() { vsa.GetProviderByNode = orig }
}

// withRealOntapProviderByNode wires vsa.GetProviderByNode to a real
// *vsa.OntapRestProvider whose ONTAP client is the supplied mock (via the vsa
// test hooks). Required for functions that type-assert to *vsa.OntapRestProvider
// (e.g. SetIlbNasLifServicePolicy).
func withRealOntapProviderByNode(mockClient ontapRest.RESTClient) func() {
	origGet := vsa.GetProviderByNode
	provider := &vsa.OntapRestProvider{}
	vsa.GetProviderByNode = func(context.Context, *models.Node) (vsa.Provider, error) {
		return provider, nil
	}
	restoreHooks := vsa.SetTestHooks(vsa.TestHooks{
		GetOntapClient: func(ontapRest.RESTClientParams) (ontapRest.RESTClient, error) {
			return mockClient, nil
		},
	})
	return func() {
		restoreHooks()
		vsa.GetProviderByNode = origGet
	}
}

// withPassword monkey-patches vsa.GetPasswordFromCacheOrSecretManager.
func withPassword(pw string, err error) func() {
	orig := vsa.GetPasswordFromCacheOrSecretManager
	vsa.GetPasswordFromCacheOrSecretManager = func(context.Context, string) (string, error) {
		return pw, err
	}
	return func() { vsa.GetPasswordFromCacheOrSecretManager = orig }
}

// withCreateNodeForProvider monkey-patches vsa.CreateNodeForProvider.
func withCreateNodeForProvider(node *models.Node) func() {
	orig := vsa.CreateNodeForProvider
	vsa.CreateNodeForProvider = func(vsa.NodeProviderInput) *models.Node {
		return node
	}
	return func() { vsa.CreateNodeForProvider = orig }
}

func kerberosInterface(enabled *bool, name, uuid *string) *ontapRest.KerberosInterface {
	ki := &ontapRest.KerberosInterface{}
	ki.Enabled = enabled
	if name != nil || uuid != nil {
		ki.Interface = &ontaprestmodels.KerberosInterfaceInlineInterface{Name: name, UUID: uuid}
	}
	return ki
}

// -----------------------------------------------------------------------------
// Pure functions
// -----------------------------------------------------------------------------

func TestBuildDiagnosisSvmName(t *testing.T) {
	tests := []struct {
		name     string
		poolUUID string
		expected string
	}{
		{"short uuid", "abc", "diag-abc"},
		{"exactly 8 chars", "abcdefgh", "diag-abcdefgh"},
		{"long uuid truncated to 8", "abcdefghijkl", "diag-abcdefgh"},
		{"hyphens stripped", "ab-cd-ef-gh-ij", "diag-abcdefgh"},
		{"uppercase lowercased", "ABCDEF12", "diag-abcdef12"},
		{"empty uuid", "", "diag-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(tt2 *testing.T) {
			assert.Equal(tt2, tt.expected, BuildDiagnosisSvmName(tt.poolUUID))
		})
	}
}

func TestIlbNasLifHomeNodeName(t *testing.T) {
	t.Run("nil config", func(tt *testing.T) {
		assert.Equal(tt, "", ilbNasLifHomeNodeName("svm", nil))
	})
	t.Run("svm not present", func(tt *testing.T) {
		cfg := &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{"other": {}}}
		assert.Equal(tt, "", ilbNasLifHomeNodeName("svm", cfg))
	})
	t.Run("no ilb nas lifs", func(tt *testing.T) {
		cfg := &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{"svm": {SVMLIFs: vlm.SvmLIFConfigs{}}}}
		assert.Equal(tt, "", ilbNasLifHomeNodeName("svm", cfg))
	})
	t.Run("returns home node", func(tt *testing.T) {
		cfg := &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{
			"svm": {SVMLIFs: vlm.SvmLIFConfigs{vlm.LIFTypeIlbNas: []vlm.LIFConfig{{HomeNode: "node-2"}}}},
		}}
		assert.Equal(tt, "node-2", ilbNasLifHomeNodeName("svm", cfg))
	})
}

func TestHasNonDiagnosisSVM(t *testing.T) {
	t.Run("empty list", func(tt *testing.T) {
		assert.False(tt, hasNonDiagnosisSVM(nil))
	})
	t.Run("nil entries skipped", func(tt *testing.T) {
		assert.False(tt, hasNonDiagnosisSVM([]*datamodel.Svm{nil}))
	})
	t.Run("all diagnosis svms", func(tt *testing.T) {
		svms := []*datamodel.Svm{
			{SvmDetails: &datamodel.SvmDetails{IsDirectoryDiagnosis: true}},
			nil,
		}
		assert.False(tt, hasNonDiagnosisSVM(svms))
	})
	t.Run("has non-diagnosis svm (nil details)", func(tt *testing.T) {
		svms := []*datamodel.Svm{
			{SvmDetails: &datamodel.SvmDetails{IsDirectoryDiagnosis: true}},
			{SvmDetails: nil},
		}
		assert.True(tt, hasNonDiagnosisSVM(svms))
	})
	t.Run("has non-diagnosis svm (details not diagnosis)", func(tt *testing.T) {
		svms := []*datamodel.Svm{
			{SvmDetails: &datamodel.SvmDetails{IsDirectoryDiagnosis: false}},
		}
		assert.True(tt, hasNonDiagnosisSVM(svms))
	})
}

// -----------------------------------------------------------------------------
// LoadPoolForDiagnosis
// -----------------------------------------------------------------------------

func TestLoadPoolForDiagnosis(t *testing.T) {
	poolUUID := "pool-uuid-1"

	t.Run("empty uuid errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.LoadPoolForDiagnosis)

		_, err := env.ExecuteActivity(a.LoadPoolForDiagnosis, "")
		require.Error(tt, err)
	})

	t.Run("not found errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.LoadPoolForDiagnosis)

		mockStorage.On("GetPoolByUUID", mock.Anything, poolUUID).
			Return((*datamodel.Pool)(nil), utilerrors.NewNotFoundErr("Pool", &poolUUID))

		_, err := env.ExecuteActivity(a.LoadPoolForDiagnosis, poolUUID)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("generic db error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.LoadPoolForDiagnosis)

		mockStorage.On("GetPoolByUUID", mock.Anything, poolUUID).
			Return((*datamodel.Pool)(nil), errors.New("db down"))

		_, err := env.ExecuteActivity(a.LoadPoolForDiagnosis, poolUUID)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("nil pool without error errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.LoadPoolForDiagnosis)

		mockStorage.On("GetPoolByUUID", mock.Anything, poolUUID).
			Return((*datamodel.Pool)(nil), nil)

		_, err := env.ExecuteActivity(a.LoadPoolForDiagnosis, poolUUID)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.LoadPoolForDiagnosis)

		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 5, UUID: poolUUID}}
		mockStorage.On("GetPoolByUUID", mock.Anything, poolUUID).Return(pool, nil)

		val, err := env.ExecuteActivity(a.LoadPoolForDiagnosis, poolUUID)
		require.NoError(tt, err)
		var got *datamodel.Pool
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, poolUUID, got.UUID)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// GetActiveDirectoryRecordForPool
// -----------------------------------------------------------------------------

func TestGetActiveDirectoryRecordForPool(t *testing.T) {
	poolID := int64(11)

	t.Run("db error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetActiveDirectoryRecordForPool)

		mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, poolID).
			Return((*datamodel.ActiveDirectory)(nil), errors.New("db error"))

		_, err := env.ExecuteActivity(a.GetActiveDirectoryRecordForPool, poolID)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("no ad attached errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetActiveDirectoryRecordForPool)

		mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, poolID).
			Return((*datamodel.ActiveDirectory)(nil), nil)

		_, err := env.ExecuteActivity(a.GetActiveDirectoryRecordForPool, poolID)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetActiveDirectoryRecordForPool)

		ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 3, UUID: "ad-uuid"}}
		mockStorage.On("GetActiveDirectoryForPoolByPoolID", mock.Anything, poolID).Return(ad, nil)

		val, err := env.ExecuteActivity(a.GetActiveDirectoryRecordForPool, poolID)
		require.NoError(tt, err)
		var got *datamodel.ActiveDirectory
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, "ad-uuid", got.UUID)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// GetNodeForDiagnosis
// -----------------------------------------------------------------------------

func TestGetNodeForDiagnosis(t *testing.T) {
	t.Run("nil pool errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetNodeForDiagnosis)

		_, err := env.ExecuteActivity(a.GetNodeForDiagnosis, (*datamodel.Pool)(nil))
		require.Error(tt, err)
	})

	t.Run("db error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetNodeForDiagnosis)

		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "p"}}
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).
			Return(([]*datamodel.Node)(nil), errors.New("db error"))

		_, err := env.ExecuteActivity(a.GetNodeForDiagnosis, pool)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("no nodes errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetNodeForDiagnosis)

		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "p"}}
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).
			Return([]*datamodel.Node{}, nil)

		_, err := env.ExecuteActivity(a.GetNodeForDiagnosis, pool)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetNodeForDiagnosis)

		restore := withCreateNodeForProvider(&models.Node{Name: "node-a"})
		defer restore()

		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "p"}}
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).
			Return([]*datamodel.Node{{Name: "node-a"}}, nil)

		val, err := env.ExecuteActivity(a.GetNodeForDiagnosis, pool)
		require.NoError(tt, err)
		var got *models.Node
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, "node-a", got.Name)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// GetIlbNasLifHomeNodeForDiagnosis
// -----------------------------------------------------------------------------

func TestGetIlbNasLifHomeNodeForDiagnosis(t *testing.T) {
	svmName := "diag-svm"
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "p"}}
	fallback := &models.Node{Name: "fallback-node"}

	homeNodeConfig := func(homeNode string) *vlm.VLMConfig {
		return &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{
			svmName: {SVMLIFs: vlm.SvmLIFConfigs{vlm.LIFTypeIlbNas: []vlm.LIFConfig{{HomeNode: homeNode}}}},
		}}
	}

	t.Run("no home node falls back", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetIlbNasLifHomeNodeForDiagnosis)

		val, err := env.ExecuteActivity(a.GetIlbNasLifHomeNodeForDiagnosis, pool, svmName, (*vlm.VLMConfig)(nil), fallback)
		require.NoError(tt, err)
		var got *models.Node
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, "fallback-node", got.Name)
	})

	t.Run("db error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetIlbNasLifHomeNodeForDiagnosis)

		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).
			Return(([]*datamodel.Node)(nil), errors.New("db error"))

		_, err := env.ExecuteActivity(a.GetIlbNasLifHomeNodeForDiagnosis, pool, svmName, homeNodeConfig("node-2"), fallback)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("home node matches db node", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetIlbNasLifHomeNodeForDiagnosis)

		restore := withCreateNodeForProvider(&models.Node{Name: "pinned-node"})
		defer restore()

		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).
			Return([]*datamodel.Node{{Name: "node-2"}}, nil)

		val, err := env.ExecuteActivity(a.GetIlbNasLifHomeNodeForDiagnosis, pool, svmName, homeNodeConfig("node-2"), fallback)
		require.NoError(tt, err)
		var got *models.Node
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, "pinned-node", got.Name)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("home node not among db nodes falls back", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.GetIlbNasLifHomeNodeForDiagnosis)

		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).
			Return([]*datamodel.Node{{Name: "node-9"}}, nil)

		val, err := env.ExecuteActivity(a.GetIlbNasLifHomeNodeForDiagnosis, pool, svmName, homeNodeConfig("node-2"), fallback)
		require.NoError(tt, err)
		var got *models.Node
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, "fallback-node", got.Name)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// PrepareCreateDiagnosisSVMRequest
// -----------------------------------------------------------------------------

func TestDiagnosisAggrNamesForReducedLif(t *testing.T) {
	tests := []struct {
		name    string
		config  *vlm.VLMConfig
		want    []string
		wantErr bool
	}{
		{
			name: "returns first named data aggregate",
			config: &vlm.VLMConfig{
				DataAggr: []vlm.DataAggrConfig{{Name: "aggr-a"}, {Name: "aggr-b"}},
			},
			want: []string{"aggr-a"},
		},
		{
			name: "skips blank aggregate names",
			config: &vlm.VLMConfig{
				DataAggr: []vlm.DataAggrConfig{{Name: "  "}, {Name: "aggr-b"}},
			},
			want: []string{"aggr-b"},
		},
		{
			name:    "no named data aggregates errors",
			config:  &vlm.VLMConfig{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := diagnosisAggrNamesForReducedLif(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			assert.Len(t, got, 1)
		})
	}
}

func TestPrepareCreateDiagnosisSVMRequest(t *testing.T) {
	newActivity := func(tt *testing.T) (ActiveDirectoryDiagnoseActivity, *testsuite.TestActivityEnvironment) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.PrepareCreateDiagnosisSVMRequest)
		return a, env
	}

	validPool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid-abc123"}}
	validAD := &datamodel.ActiveDirectory{DNS: "10.0.0.1", Domain: "example.com"}
	validCfg := &vlm.VLMConfig{
		DataAggr: []vlm.DataAggrConfig{{Name: "aggr1", HomeNode: "node-1"}},
	}
	validCreds := &vlm.OntapCredentials{AdminPassword: "admin-pw"}

	t.Run("nil pool errors", func(tt *testing.T) {
		a, env := newActivity(tt)
		_, err := env.ExecuteActivity(a.PrepareCreateDiagnosisSVMRequest, (*datamodel.Pool)(nil), validAD, validCfg, validCreds)
		require.Error(tt, err)
	})

	t.Run("nil ad errors", func(tt *testing.T) {
		a, env := newActivity(tt)
		_, err := env.ExecuteActivity(a.PrepareCreateDiagnosisSVMRequest, validPool, (*datamodel.ActiveDirectory)(nil), validCfg, validCreds)
		require.Error(tt, err)
	})

	t.Run("nil vlmConfig errors", func(tt *testing.T) {
		a, env := newActivity(tt)
		_, err := env.ExecuteActivity(a.PrepareCreateDiagnosisSVMRequest, validPool, validAD, (*vlm.VLMConfig)(nil), validCreds)
		require.Error(tt, err)
	})

	t.Run("nil credentials errors", func(tt *testing.T) {
		a, env := newActivity(tt)
		_, err := env.ExecuteActivity(a.PrepareCreateDiagnosisSVMRequest, validPool, validAD, validCfg, (*vlm.OntapCredentials)(nil))
		require.Error(tt, err)
	})

	t.Run("empty dns or domain errors", func(tt *testing.T) {
		a, env := newActivity(tt)
		badAD := &datamodel.ActiveDirectory{DNS: "", Domain: "example.com"}
		_, err := env.ExecuteActivity(a.PrepareCreateDiagnosisSVMRequest, validPool, badAD, validCfg, validCreds)
		require.Error(tt, err)
	})

	t.Run("missing data aggregates errors", func(tt *testing.T) {
		a, env := newActivity(tt)
		emptyAggrCfg := &vlm.VLMConfig{}
		_, err := env.ExecuteActivity(a.PrepareCreateDiagnosisSVMRequest, validPool, validAD, emptyAggrCfg, validCreds)
		require.Error(tt, err)
	})

	t.Run("success builds request with forced ilb + reduced nas lif", func(tt *testing.T) {
		a, env := newActivity(tt)
		val, err := env.ExecuteActivity(a.PrepareCreateDiagnosisSVMRequest, validPool, validAD, validCfg, validCreds)
		require.NoError(tt, err)
		var req *vlm.CreateSVMRequest
		require.NoError(tt, val.Get(&req))
		require.NotNil(tt, req)
		assert.Equal(tt, BuildDiagnosisSvmName(validPool.UUID), req.Name)
		assert.Equal(tt, "example.com", req.DnsDomains)
		assert.Equal(tt, "10.0.0.1", req.NameServers)
		assert.True(tt, req.NasLif)
		assert.False(tt, req.SanLif)
		assert.Equal(tt, []string{"aggr1"}, req.AggrNames)
		assert.True(tt, req.VLMConfig.Deployment.DevFlags.EnableIlbSupport)
	})
}

// -----------------------------------------------------------------------------
// SaveDiagnosisSVMToDB
// -----------------------------------------------------------------------------

func TestSaveDiagnosisSVMToDB(t *testing.T) {
	svmName := "diag-svm"
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 7}, AccountID: 42}
	ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 9}}
	cfgWithSvm := &vlm.VLMConfig{
		VsaCluster: vlm.VsaClusterConfig{CustIPSpace: "ipspace1"},
		Svm: map[string]vlm.SvmConfig{
			svmName: {Svmname: svmName, Svmuuid: "svm-ext-uuid"},
		},
	}

	t.Run("nil pool errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, (*datamodel.Pool)(nil), ad, svmName, cfgWithSvm)
		require.Error(tt, err)
	})

	t.Run("empty svmName errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, "", cfgWithSvm)
		require.Error(tt, err)
	})

	t.Run("svm missing from vlm config errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{}})
		require.Error(tt, err)
	})

	// expectDetailsPersist stubs the unconditional SvmDetails rewrite, asserting
	// the blob carries the VLM UUID and the diagnosis flag, and returns the row
	// with those details applied (as the real store does).
	expectDetailsPersist := func(mockStorage *database.MockStorage, row *datamodel.Svm) *datamodel.Svm {
		persisted := *row
		mockStorage.On("UpdateSvmDetails", mock.Anything, mock.Anything, mock.MatchedBy(func(d *datamodel.SvmDetails) bool {
			return d != nil && d.ExternalUUID == "svm-ext-uuid" && d.IPSpace == "ipspace1" && d.IsDirectoryDiagnosis
		})).Run(func(args mock.Arguments) {
			persisted.SvmDetails = args.Get(2).(*datamodel.SvmDetails)
		}).Return(&persisted, nil)
		return &persisted
	}

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		created := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 100, UUID: "svm-row-uuid"}, Name: svmName}
		// No State is asserted: CreateSVM owns the lifecycle state (always READY),
		// so the activity leaves it unset and flags the row via SvmDetails.
		mockStorage.On("CreateSVM", mock.Anything, mock.MatchedBy(func(s *datamodel.Svm) bool {
			return s.Name == svmName &&
				s.State == "" &&
				s.StateDetails == "" &&
				s.SvmDetails != nil &&
				s.SvmDetails.IsDirectoryDiagnosis &&
				s.SvmDetails.ExternalUUID == "svm-ext-uuid"
		})).Return(created, nil)
		expectDetailsPersist(mockStorage, created)
		// No SVMLIFs in cfgWithSvm, so only node lookup happens; no CreateLif.
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(7)).Return([]*datamodel.Node{}, nil)

		val, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithSvm)
		require.NoError(tt, err)
		var got *datamodel.Svm
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, "svm-row-uuid", got.UUID)
		require.NotNil(tt, got.SvmDetails)
		assert.Equal(tt, "svm-ext-uuid", got.SvmDetails.ExternalUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("conflict returns existing row", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		existing := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 55, UUID: "existing-uuid"}, Name: svmName}
		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return((*datamodel.Svm)(nil), utilerrors.NewConflictErr("exists"))
		mockStorage.On("GetSvmByNameAndPoolID", mock.Anything, svmName, int64(7)).Return(existing, nil)
		expectDetailsPersist(mockStorage, existing)
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(7)).Return([]*datamodel.Node{}, nil)

		val, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithSvm)
		require.NoError(tt, err)
		var got *datamodel.Svm
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		assert.Equal(tt, "existing-uuid", got.UUID)
		mockStorage.AssertExpectations(tt)
	})

	// A previous run's soft-delete is best-effort, so its row can survive and be
	// handed back by the CreateSVM already-READY short-circuit carrying that
	// run's (now deleted) SVM UUID. The rewrite must replace it.
	t.Run("stale row keeps the UUID VLM just reported", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		stale := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{ID: 55, UUID: "existing-uuid"},
			Name:       svmName,
			SvmDetails: &datamodel.SvmDetails{ExternalUUID: "previous-run-uuid", IsDirectoryDiagnosis: true},
		}
		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(stale, nil)
		expectDetailsPersist(mockStorage, stale)
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(7)).Return([]*datamodel.Node{}, nil)

		val, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithSvm)
		require.NoError(tt, err)
		var got *datamodel.Svm
		require.NoError(tt, val.Get(&got))
		require.NotNil(tt, got)
		require.NotNil(tt, got.SvmDetails)
		assert.Equal(tt, "svm-ext-uuid", got.SvmDetails.ExternalUUID)
		assert.True(tt, got.SvmDetails.IsDirectoryDiagnosis)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("empty UUID in vlm config errors before any write", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		cfgWithoutUUID := &vlm.VLMConfig{
			VsaCluster: vlm.VsaClusterConfig{CustIPSpace: "ipspace1"},
			Svm:        map[string]vlm.SvmConfig{svmName: {Svmname: svmName}},
		}

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithoutUUID)
		require.Error(tt, err)
		mockStorage.AssertNotCalled(tt, "CreateSVM", mock.Anything, mock.Anything)
	})

	t.Run("details persist failure errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		created := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 100, UUID: "svm-row-uuid"}, Name: svmName}
		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(created, nil)
		mockStorage.On("UpdateSvmDetails", mock.Anything, mock.Anything, mock.Anything).
			Return((*datamodel.Svm)(nil), errors.New("db down"))

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithSvm)
		require.Error(tt, err)
		mockStorage.AssertNotCalled(tt, "CreateLif", mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("row without details after persist errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		created := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 100, UUID: "svm-row-uuid"}, Name: svmName}
		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(created, nil)
		mockStorage.On("UpdateSvmDetails", mock.Anything, mock.Anything, mock.Anything).Return(created, nil)

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithSvm)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("conflict then lookup error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return((*datamodel.Svm)(nil), utilerrors.NewConflictErr("exists"))
		mockStorage.On("GetSvmByNameAndPoolID", mock.Anything, svmName, int64(7)).Return((*datamodel.Svm)(nil), errors.New("lookup failed"))

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithSvm)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("generic create error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return((*datamodel.Svm)(nil), errors.New("db down"))

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithSvm)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	cfgWithLif := &vlm.VLMConfig{
		VsaCluster: vlm.VsaClusterConfig{CustIPSpace: "ipspace1"},
		Svm: map[string]vlm.SvmConfig{
			svmName: {
				Svmname: svmName,
				Svmuuid: "svm-ext-uuid",
				SVMLIFs: vlm.SvmLIFConfigs{
					vlm.LIFTypeIlbNas: {
						{Name: "diag-ilb-nas", IP: "10.0.0.5/24", Uuid: "lif-ext-uuid", HomeNode: "node-a"},
					},
				},
			},
		},
	}

	t.Run("persists diagnosis LIF flagged and linked to svm", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		created := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 100, UUID: "svm-row-uuid"}, Name: svmName}
		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(created, nil)
		expectDetailsPersist(mockStorage, created)
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(7)).Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 11}, Name: "node-a"},
		}, nil)
		mockStorage.On("CreateLif", mock.Anything, mock.MatchedBy(func(l *datamodel.Lif) bool {
			return l.Name == "diag-ilb-nas" &&
				l.NodeID == 11 &&
				l.SvmID.Valid && l.SvmID.Int64 == 100 &&
				l.AccountID == 42 &&
				l.IPAddress == "10.0.0.5" &&
				l.Type == database.LifTypeDataLIF &&
				l.IsDirectoryDiagnosis &&
				l.LifDetails != nil &&
				l.LifDetails.ProtocolType == string(vlm.LIFTypeNas) &&
				l.LifDetails.ExternalUUID == "lif-ext-uuid"
		})).Return(&datamodel.Lif{}, nil)

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithLif)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("CreateLif conflict is idempotent", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		created := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 100, UUID: "svm-row-uuid"}, Name: svmName}
		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(created, nil)
		expectDetailsPersist(mockStorage, created)
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(7)).Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 11}, Name: "node-a"},
		}, nil)
		mockStorage.On("CreateLif", mock.Anything, mock.Anything).Return((*datamodel.Lif)(nil), utilerrors.NewConflictErr("lif exists"))

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithLif)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("LIF with unknown home node errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SaveDiagnosisSVMToDB)

		created := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 100, UUID: "svm-row-uuid"}, Name: svmName}
		mockStorage.On("CreateSVM", mock.Anything, mock.Anything).Return(created, nil)
		expectDetailsPersist(mockStorage, created)
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(7)).Return([]*datamodel.Node{
			{BaseModel: datamodel.BaseModel{ID: 11}, Name: "other-node"},
		}, nil)

		_, err := env.ExecuteActivity(a.SaveDiagnosisSVMToDB, pool, ad, svmName, cfgWithLif)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// SoftDeleteDiagnosisSVMFromDB
// -----------------------------------------------------------------------------

func TestSoftDeleteDiagnosisSVMFromDB(t *testing.T) {
	t.Run("nil svm is a no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SoftDeleteDiagnosisSVMFromDB)

		_, err := env.ExecuteActivity(a.SoftDeleteDiagnosisSVMFromDB, (*datamodel.Svm)(nil), "")
		require.NoError(tt, err)
	})

	t.Run("delete error is swallowed", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SoftDeleteDiagnosisSVMFromDB)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "diag-svm"}
		mockStorage.On("DeleteLifsBySvmID", mock.Anything, int64(1)).Return(nil)
		mockStorage.On("DeleteSVM", mock.Anything, svm).Return(errors.New("delete failed"))

		_, err := env.ExecuteActivity(a.SoftDeleteDiagnosisSVMFromDB, svm, "err msg")
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SoftDeleteDiagnosisSVMFromDB)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 1}, Name: "diag-svm"}
		mockStorage.On("DeleteLifsBySvmID", mock.Anything, int64(1)).Return(nil)
		mockStorage.On("DeleteSVM", mock.Anything, svm).Return(nil)

		_, err := env.ExecuteActivity(a.SoftDeleteDiagnosisSVMFromDB, svm, "")
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("lif delete error is swallowed, svm still deleted", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.SoftDeleteDiagnosisSVMFromDB)

		svm := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 2}, Name: "diag-svm"}
		mockStorage.On("DeleteLifsBySvmID", mock.Anything, int64(2)).Return(errors.New("lif delete failed"))
		mockStorage.On("DeleteSVM", mock.Anything, svm).Return(nil)

		_, err := env.ExecuteActivity(a.SoftDeleteDiagnosisSVMFromDB, svm, "")
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// CleanupStaleDiagnosisSVMFromDB
// -----------------------------------------------------------------------------

func TestCleanupStaleDiagnosisSVMFromDB(t *testing.T) {
	svmName := "diag-svm"
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 3}}

	t.Run("nil pool no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.CleanupStaleDiagnosisSVMFromDB)

		_, err := env.ExecuteActivity(a.CleanupStaleDiagnosisSVMFromDB, (*datamodel.Pool)(nil), svmName)
		require.NoError(tt, err)
	})

	t.Run("not found no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.CleanupStaleDiagnosisSVMFromDB)

		mockStorage.On("GetSvmByNameAndPoolID", mock.Anything, svmName, int64(3)).
			Return((*datamodel.Svm)(nil), utilerrors.NewNotFoundErr("svm", &svmName))

		_, err := env.ExecuteActivity(a.CleanupStaleDiagnosisSVMFromDB, pool, svmName)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("lookup error swallowed", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.CleanupStaleDiagnosisSVMFromDB)

		mockStorage.On("GetSvmByNameAndPoolID", mock.Anything, svmName, int64(3)).
			Return((*datamodel.Svm)(nil), errors.New("db error"))

		_, err := env.ExecuteActivity(a.CleanupStaleDiagnosisSVMFromDB, pool, svmName)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("nil existing no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.CleanupStaleDiagnosisSVMFromDB)

		mockStorage.On("GetSvmByNameAndPoolID", mock.Anything, svmName, int64(3)).
			Return((*datamodel.Svm)(nil), nil)

		_, err := env.ExecuteActivity(a.CleanupStaleDiagnosisSVMFromDB, pool, svmName)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("delete error swallowed", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.CleanupStaleDiagnosisSVMFromDB)

		existing := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 8}, Name: svmName}
		mockStorage.On("GetSvmByNameAndPoolID", mock.Anything, svmName, int64(3)).Return(existing, nil)
		mockStorage.On("DeleteSVM", mock.Anything, existing).Return(errors.New("delete failed"))

		_, err := env.ExecuteActivity(a.CleanupStaleDiagnosisSVMFromDB, pool, svmName)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.CleanupStaleDiagnosisSVMFromDB)

		existing := &datamodel.Svm{BaseModel: datamodel.BaseModel{ID: 8}, Name: svmName}
		mockStorage.On("GetSvmByNameAndPoolID", mock.Anything, svmName, int64(3)).Return(existing, nil)
		mockStorage.On("DeleteSVM", mock.Anything, existing).Return(nil)

		_, err := env.ExecuteActivity(a.CleanupStaleDiagnosisSVMFromDB, pool, svmName)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// RestoreADStateFromSVMUsage
// -----------------------------------------------------------------------------

func TestRestoreADStateFromSVMUsage(t *testing.T) {
	adUUID := "ad-uuid-1"

	t.Run("empty uuid no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.RestoreADStateFromSVMUsage)

		_, err := env.ExecuteActivity(a.RestoreADStateFromSVMUsage, "")
		require.NoError(tt, err)
	})

	t.Run("get ad error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.RestoreADStateFromSVMUsage)

		mockStorage.On("GetActiveDirectoryByUUID", mock.Anything, adUUID).
			Return((*datamodel.ActiveDirectory)(nil), errors.New("db error"))

		_, err := env.ExecuteActivity(a.RestoreADStateFromSVMUsage, adUUID)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("nil ad no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.RestoreADStateFromSVMUsage)

		mockStorage.On("GetActiveDirectoryByUUID", mock.Anything, adUUID).
			Return((*datamodel.ActiveDirectory)(nil), nil)

		_, err := env.ExecuteActivity(a.RestoreADStateFromSVMUsage, adUUID)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("svm lookup error defaults to READY", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.RestoreADStateFromSVMUsage)

		ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 4, UUID: adUUID}}
		mockStorage.On("GetActiveDirectoryByUUID", mock.Anything, adUUID).Return(ad, nil)
		mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(4)).
			Return(([]*datamodel.Svm)(nil), errors.New("db error"))
		mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
			return a.State == datamodel.LifeCycleStateREADY
		})).Return(ad, nil)

		_, err := env.ExecuteActivity(a.RestoreADStateFromSVMUsage, adUUID)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("in use when non-diagnosis svm present", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.RestoreADStateFromSVMUsage)

		ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 4, UUID: adUUID}}
		mockStorage.On("GetActiveDirectoryByUUID", mock.Anything, adUUID).Return(ad, nil)
		mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(4)).
			Return([]*datamodel.Svm{{BaseModel: datamodel.BaseModel{ID: 1}}}, nil)
		mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
			return a.State == datamodel.LifeCycleStateInUse
		})).Return(ad, nil)

		_, err := env.ExecuteActivity(a.RestoreADStateFromSVMUsage, adUUID)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("ready when only diagnosis svm present", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.RestoreADStateFromSVMUsage)

		ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 4, UUID: adUUID}}
		mockStorage.On("GetActiveDirectoryByUUID", mock.Anything, adUUID).Return(ad, nil)
		mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(4)).
			Return([]*datamodel.Svm{{SvmDetails: &datamodel.SvmDetails{IsDirectoryDiagnosis: true}}}, nil)
		mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.MatchedBy(func(a *datamodel.ActiveDirectory) bool {
			return a.State == datamodel.LifeCycleStateREADY
		})).Return(ad, nil)

		_, err := env.ExecuteActivity(a.RestoreADStateFromSVMUsage, adUUID)
		require.NoError(tt, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("update error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		mockStorage := database.NewMockStorage(tt)
		a := ActiveDirectoryDiagnoseActivity{SE: mockStorage}
		env.RegisterActivity(a.RestoreADStateFromSVMUsage)

		ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 4, UUID: adUUID}}
		mockStorage.On("GetActiveDirectoryByUUID", mock.Anything, adUUID).Return(ad, nil)
		mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(4)).Return([]*datamodel.Svm{}, nil)
		mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.Anything).
			Return((*datamodel.ActiveDirectory)(nil), errors.New("update failed"))

		_, err := env.ExecuteActivity(a.RestoreADStateFromSVMUsage, adUUID)
		require.Error(tt, err)
		mockStorage.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// SetIlbNasLifServicePolicy
// -----------------------------------------------------------------------------

func TestSetIlbNasLifServicePolicy(t *testing.T) {
	svmName := "diag-svm"
	cfgWithLif := func(uuid string) *vlm.VLMConfig {
		return &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{
			svmName: {SVMLIFs: vlm.SvmLIFConfigs{vlm.LIFTypeIlbNas: []vlm.LIFConfig{{Name: "diag-svm-ilbnas", Uuid: uuid}}}},
		}}
	}

	t.Run("nil vlmConfig errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, (*vlm.VLMConfig)(nil))
		require.Error(tt, err)
	})

	t.Run("svm not in config errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{}})
		require.Error(tt, err)
	})

	t.Run("no ilb nas lif errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		cfg := &vlm.VLMConfig{Svm: map[string]vlm.SvmConfig{svmName: {SVMLIFs: vlm.SvmLIFConfigs{}}}}
		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, cfg)
		require.Error(tt, err)
	})

	t.Run("empty lif uuid errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, cfgWithLif(""))
		require.Error(tt, err)
	})

	t.Run("provider by node error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		restore := withProviderByNode(nil, errors.New("provider error"))
		defer restore()

		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, cfgWithLif("lif-uuid"))
		require.Error(tt, err)
	})

	t.Run("provider not ontap rest errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		restore := withProviderByNode(&vsa.MockProvider{}, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, cfgWithLif("lif-uuid"))
		require.Error(tt, err)
	})

	t.Run("modify error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		mockClient := new(ontapRest.MockRESTClient)
		mockNet := new(ontapRest.MockNetworkingClient)
		mockClient.On("Networking").Return(mockNet).Once()
		mockNet.On("NetworkIPInterfaceModify", mock.Anything).Return(errors.New("modify failed")).Once()

		restore := withRealOntapProviderByNode(mockClient)
		defer restore()

		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, cfgWithLif("lif-uuid"))
		require.Error(tt, err)
		mockNet.AssertExpectations(tt)
	})

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.SetIlbNasLifServicePolicy)

		mockClient := new(ontapRest.MockRESTClient)
		mockNet := new(ontapRest.MockNetworkingClient)
		mockClient.On("Networking").Return(mockNet).Once()
		mockNet.On("NetworkIPInterfaceModify", mock.MatchedBy(func(p *ontapRest.NetworkIPInterfaceModifyParams) bool {
			return p.UUID != nil && *p.UUID == "lif-uuid" &&
				p.ServicePolicyName != nil && *p.ServicePolicyName == ilbNasServicePolicy
		})).Return(nil).Once()

		restore := withRealOntapProviderByNode(mockClient)
		defer restore()

		_, err := env.ExecuteActivity(a.SetIlbNasLifServicePolicy, &models.Node{}, svmName, cfgWithLif("lif-uuid"))
		require.NoError(tt, err)
		mockNet.AssertExpectations(tt)
	})
}

// -----------------------------------------------------------------------------
// DeleteDiagnosisCifsServer
// -----------------------------------------------------------------------------

func TestDeleteDiagnosisCifsServer(t *testing.T) {
	adWithCreds := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: "ad-uuid"},
		Username:       "admin",
		CredentialPath: "secret-path",
	}

	t.Run("nil node no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, (*models.Node)(nil), adWithCreds, "svm-uuid", "")
		require.NoError(tt, err)
	})

	t.Run("empty svm uuid no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, adWithCreds, "", "")
		require.NoError(tt, err)
	})

	t.Run("empty credential path no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{UUID: "ad-uuid"}, CredentialPath: ""}
		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, ad, "svm-uuid", "")
		require.NoError(tt, err)
	})

	t.Run("password fetch error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		restore := withPassword("", errors.New("secret error"))
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, adWithCreds, "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("provider by node error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		restore := withProviderByNode(nil, errors.New("provider error"))
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, adWithCreds, "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("provider does not support cifs delete", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		restore := withProviderByNode(&vsa.MockProvider{}, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, adWithCreds, "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("cifs server already gone", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		notFound := "cifs"
		stub := &stubCifsDeleter{deleteCIFSServerFn: func(string, string, string) error {
			return utilerrors.NewNotFoundErr("cifs", &notFound)
		}}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, adWithCreds, "svm-uuid", "")
		require.NoError(tt, err)
	})

	t.Run("cifs delete error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		stub := &stubCifsDeleter{deleteCIFSServerFn: func(string, string, string) error {
			return errors.New("delete failed")
		}}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, adWithCreds, "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisCifsServer)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		called := false
		stub := &stubCifsDeleter{deleteCIFSServerFn: func(svmUUID, user, pass string) error {
			called = true
			assert.Equal(tt, "svm-uuid", svmUUID)
			assert.Equal(tt, "admin", user)
			assert.Equal(tt, "pw", pass)
			return nil
		}}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisCifsServer, &models.Node{}, adWithCreds, "svm-uuid", "")
		require.NoError(tt, err)
		assert.True(tt, called)
	})
}

// -----------------------------------------------------------------------------
// DeleteDiagnosisKerberosConfig
// -----------------------------------------------------------------------------

func TestDeleteDiagnosisKerberosConfig(t *testing.T) {
	adWithCreds := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: "ad-uuid"},
		Username:       "admin",
		CredentialPath: "secret-path",
	}

	t.Run("nil node no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, (*models.Node)(nil), adWithCreds, "svm", "svm-uuid", "")
		require.NoError(tt, err)
	})

	t.Run("empty credential path no-op", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		ad := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{UUID: "ad-uuid"}}
		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, ad, "svm", "svm-uuid", "")
		require.NoError(tt, err)
	})

	t.Run("password fetch error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restore := withPassword("", errors.New("secret error"))
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, adWithCreds, "svm", "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("provider by node error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		restore := withProviderByNode(nil, errors.New("provider error"))
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, adWithCreds, "svm", "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("provider does not support kerberos teardown", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		restore := withProviderByNode(&vsa.MockProvider{}, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, adWithCreds, "svm", "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("svm already gone", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		notFound := "svm"
		stub := &stubKerberosCleaner{
			getInterfacesFn: func(string, string) ([]*ontapRest.KerberosInterface, error) {
				return nil, utilerrors.NewNotFoundErr("svm", &notFound)
			},
		}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, adWithCreds, "svm", "svm-uuid", "")
		require.NoError(tt, err)
	})

	t.Run("list interfaces error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restorePw := withPassword("pw", nil)
		defer restorePw()
		stub := &stubKerberosCleaner{
			getInterfacesFn: func(string, string) ([]*ontapRest.KerberosInterface, error) {
				return nil, errors.New("list failed")
			},
		}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, adWithCreds, "svm", "svm-uuid", "")
		require.Error(tt, err)
	})

	t.Run("disables only enabled interfaces with uuid", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restorePw := withPassword("pw", nil)
		defer restorePw()

		enabledName, enabledUUID := "lif-enabled", "uuid-1"
		disabledName := "lif-disabled"
		noUUIDName := "lif-nouuid"
		interfaces := []*ontapRest.KerberosInterface{
			nil,
			kerberosInterface(boolPtr(false), &disabledName, strPtr("uuid-x")),
			kerberosInterface(boolPtr(true), &noUUIDName, nil),
			kerberosInterface(boolPtr(true), &enabledName, &enabledUUID),
		}
		disableCount := 0
		stub := &stubKerberosCleaner{
			getInterfacesFn: func(string, string) ([]*ontapRest.KerberosInterface, error) {
				return interfaces, nil
			},
			disableFn: func(params vsa.KerberosInterfaceModifyParams) error {
				disableCount++
				assert.Equal(tt, "svm-uuid", params.SvmUUID)
				assert.Equal(tt, enabledUUID, params.InterfaceUUID)
				return nil
			},
		}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, adWithCreds, "svm", "svm-uuid", "")
		require.NoError(tt, err)
		assert.Equal(tt, 1, disableCount)
	})

	t.Run("uses organizational unit from AD attributes", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restorePw := withPassword("pw", nil)
		defer restorePw()

		ad := adWithCreds
		ad.ActiveDirectoryAttributes = &datamodel.ActiveDirectoryAttributes{OrganizationalUnit: "OU=diag"}
		enabledName, enabledUUID := "lif-enabled", "uuid-1"
		stub := &stubKerberosCleaner{
			getInterfacesFn: func(string, string) ([]*ontapRest.KerberosInterface, error) {
				return []*ontapRest.KerberosInterface{
					kerberosInterface(boolPtr(true), &enabledName, &enabledUUID),
				}, nil
			},
			disableFn: func(params vsa.KerberosInterfaceModifyParams) error {
				assert.Equal(tt, "OU=diag", params.OU)
				return nil
			},
		}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, ad, "svm", "svm-uuid", "")
		require.NoError(tt, err)
	})

	t.Run("disable error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryDiagnoseActivity{SE: database.NewMockStorage(tt)}
		env.RegisterActivity(a.DeleteDiagnosisKerberosConfig)

		restorePw := withPassword("pw", nil)
		defer restorePw()

		name, uuid := "lif-enabled", "uuid-1"
		stub := &stubKerberosCleaner{
			getInterfacesFn: func(string, string) ([]*ontapRest.KerberosInterface, error) {
				return []*ontapRest.KerberosInterface{kerberosInterface(boolPtr(true), &name, &uuid)}, nil
			},
			disableFn: func(vsa.KerberosInterfaceModifyParams) error {
				return errors.New("disable failed")
			},
		}
		restore := withProviderByNode(stub, nil)
		defer restore()

		_, err := env.ExecuteActivity(a.DeleteDiagnosisKerberosConfig, &models.Node{}, adWithCreds, "svm", "svm-uuid", "")
		require.Error(tt, err)
	})
}

// -----------------------------------------------------------------------------
// ConfigureLdapForSVM (active_directory_activity.go)
// -----------------------------------------------------------------------------

func TestConfigureLdapForSVM(t *testing.T) {
	adValid := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		Domain:    "example.com",
	}

	t.Run("nil ad errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryActivity{}
		env.RegisterActivity(a.ConfigureLdapForSVM)

		_, err := env.ExecuteActivity(a.ConfigureLdapForSVM, &models.Node{}, (*datamodel.ActiveDirectory)(nil), "svm", "svm-uuid", []string{})
		require.Error(tt, err)
	})

	t.Run("empty svm uuid errors", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryActivity{}
		env.RegisterActivity(a.ConfigureLdapForSVM)

		_, err := env.ExecuteActivity(a.ConfigureLdapForSVM, &models.Node{}, adValid, "svm", "", []string{})
		require.Error(tt, err)
	})

	t.Run("get provider error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryActivity{}
		env.RegisterActivity(a.ConfigureLdapForSVM)

		orig := getOntapRestProvider
		getOntapRestProvider = func(context.Context, *models.Node) (*vsa.OntapRestProvider, error) {
			return nil, errors.New("provider error")
		}
		defer func() { getOntapRestProvider = orig }()

		_, err := env.ExecuteActivity(a.ConfigureLdapForSVM, &models.Node{}, adValid, "svm", "svm-uuid", []string{})
		require.Error(tt, err)
	})

	t.Run("existing ldap config treated as success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryActivity{}
		env.RegisterActivity(a.ConfigureLdapForSVM)

		mockClient := new(ontapRest.MockRESTClient)
		mockNS := new(ontapRest.MockNameServicesClient)
		mockClient.On("NameServices").Return(mockNS).Once()
		mockNS.On("LdapGet", mock.Anything).Return(&ontapRest.LdapService{}, nil).Once()

		cleanup := setupOntapProvider(tt, context.Background(), mockClient, vsa.TestHooks{})
		defer cleanup()

		_, err := env.ExecuteActivity(a.ConfigureLdapForSVM, &models.Node{}, adValid, "svm", "svm-uuid", []string{})
		require.NoError(tt, err)
		mockNS.AssertExpectations(tt)
	})

	t.Run("ldap conflict treated as success", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryActivity{}
		env.RegisterActivity(a.ConfigureLdapForSVM)

		mockClient := new(ontapRest.MockRESTClient)
		mockNS := new(ontapRest.MockNameServicesClient)
		mockClient.On("NameServices").Return(mockNS).Once()
		mockNS.On("LdapGet", mock.Anything).Return((*ontapRest.LdapService)(nil), utilerrors.NewConflictErr("exists")).Once()

		cleanup := setupOntapProvider(tt, context.Background(), mockClient, vsa.TestHooks{})
		defer cleanup()

		_, err := env.ExecuteActivity(a.ConfigureLdapForSVM, &models.Node{}, adValid, "svm", "svm-uuid", []string{})
		require.NoError(tt, err)
		mockNS.AssertExpectations(tt)
	})

	t.Run("ldap generic error", func(tt *testing.T) {
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		a := ActiveDirectoryActivity{}
		env.RegisterActivity(a.ConfigureLdapForSVM)

		mockClient := new(ontapRest.MockRESTClient)
		mockNS := new(ontapRest.MockNameServicesClient)
		mockClient.On("NameServices").Return(mockNS).Once()
		mockNS.On("LdapGet", mock.Anything).Return((*ontapRest.LdapService)(nil), errors.New("ldap boom")).Once()

		cleanup := setupOntapProvider(tt, context.Background(), mockClient, vsa.TestHooks{})
		defer cleanup()

		_, err := env.ExecuteActivity(a.ConfigureLdapForSVM, &models.Node{}, adValid, "svm", "svm-uuid", []string{})
		require.Error(tt, err)
		mockNS.AssertExpectations(tt)
	})
}
