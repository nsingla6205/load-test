package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newRbacTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	return env
}

// Override OCI_EXPERT_MODE_PASSWORD for one test and restore it on cleanup.
func setOCIExpertModePasswordForRbac(t *testing.T, pw string) {
	t.Helper()
	orig := ociExpertModePassword
	ociExpertModePassword = pw
	t.Cleanup(func() { ociExpertModePassword = orig })
}

func installMockVlmForRbac(t *testing.T) *vlm.MockVlmWorkflowClient {
	t.Helper()
	mockVlmClient := vlm.NewMockVlmWorkflowClient(t)
	orig := vlm.NewVSAClientWorkflowManager
	vlm.NewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlmClient }
	t.Cleanup(func() { vlm.NewVSAClientWorkflowManager = orig })
	return mockVlmClient
}

func defaultRbacParams() *common.RefreshRbacForPoolParams {
	return &common.RefreshRbacForPoolParams{
		PoolOCID:    "ocid1.pool..a",
		AccountName: "test-account",
	}
}

func defaultRbacPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "pool-uuid"},
		Name:            "test-pool",
		VLMConfig:       "{}",
		PoolCredentials: &datamodel.PoolCredentials{Password: "ontap-pw"},
	}
}

// ---------------------------------------------------------------------------
// OCIRefreshRbacForPoolWorkflow
// ---------------------------------------------------------------------------

func TestOCIRefreshRbacForPoolWorkflow_NilParams(t *testing.T) {
	env := newRbacTestEnv(t)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, (*common.RefreshRbacForPoolParams)(nil), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "invalid params")
}

func TestOCIRefreshRbacForPoolWorkflow_NilPool(t *testing.T) {
	env := newRbacTestEnv(t)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), (*datamodel.Pool)(nil))

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "missing or invalid params/pool")
}

func TestOCIRefreshRbacForPoolWorkflow_ParseVlmConfigFails(t *testing.T) {
	env := newRbacTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return((*vlm.VLMConfig)(nil), assert.AnError)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIRefreshRbacForPoolWorkflow_NilPoolCredentials(t *testing.T) {
	env := newRbacTestEnv(t)
	pool := defaultRbacPool()
	pool.PoolCredentials = nil

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), pool)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "pool credentials are required")
	env.AssertExpectations(t)
}

func TestOCIRefreshRbacForPoolWorkflow_EmptyRbacURL(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = ""
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	env := newRbacTestEnv(t)
	params := defaultRbacParams()
	params.RbacFileURL = ""

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, params, defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "rbacFilePath was not provided")
	env.AssertExpectations(t)
}

func TestOCIRefreshRbacForPoolWorkflow_CreateExpertModeUserFails(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })
	setOCIExpertModePasswordForRbac(t, "expert-mode-pw")

	env := newRbacTestEnv(t)
	mockVlmClient := installMockVlmForRbac(t)
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, assert.AnError)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIRefreshRbacForPoolWorkflow_UpdateRbacInPoolFails(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })
	setOCIExpertModePasswordForRbac(t, "expert-mode-pw")

	env := newRbacTestEnv(t)
	mockVlmClient := installMockVlmForRbac(t)
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "checksum-abc"}, nil)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(assert.AnError)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIRefreshRbacForPoolWorkflow_FullSuccess(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })
	setOCIExpertModePasswordForRbac(t, "expert-mode-pw")

	env := newRbacTestEnv(t)
	mockVlmClient := installMockVlmForRbac(t)
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "checksum-abc"}, nil)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// params.RbacFileURL should win over the env var fallback.
func TestOCIRefreshRbacForPoolWorkflow_UsesParamsRbacURL(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://env-fallback.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })
	setOCIExpertModePasswordForRbac(t, "expert-mode-pw")

	env := newRbacTestEnv(t)
	params := defaultRbacParams()
	params.RbacFileURL = "https://params-override.example.com/rbac.zip"

	mockVlmClient := installMockVlmForRbac(t)
	var capturedReq *vlm.OntapExpertModeUserConfig
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything,
		mock.MatchedBy(func(req *vlm.OntapExpertModeUserConfig) bool {
			capturedReq = req
			return true
		})).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "checksum-abc"}, nil)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, params, defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	if assert.NotNil(t, capturedReq) {
		assert.Equal(t, params.RbacFileURL, capturedReq.RbacFileURL,
			"params.RbacFileURL should take precedence over the env var fallback")
	}
	env.AssertExpectations(t)
}

// If we can't resolve the ONTAP admin password (vault unreachable, secret
// missing, etc.) we should bail out before calling VLM.
func TestOCIRefreshRbacForPoolWorkflow_GetOnTapCredentialsFails(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })
	setOCIExpertModePasswordForRbac(t, "expert-mode-pw")

	env := newRbacTestEnv(t)
	installMockVlmForRbac(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return((*vlm.OntapCredentials)(nil), assert.AnError)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// Regression guard for the "failed to validate ONTAP credentials" bug: pin
// where each field in the VLM request comes from. OntapCredentials must come
// from the activity (not pool.PoolCredentials.Password). When
// ENABLE_ADMIN_TEMP_PASSWORD is true the RBAC refresh does not resolve or
// forward an expert-mode user password, so ExpertModeUserCredentials must be
// empty and the RBAC checksum must come from the OCI_EXPERT_MODE_RBAC_FILE_CHECKSUM
// env var.
func TestOCIRefreshRbacForPoolWorkflow_RequestShape_UsesResolvedCredentials(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	origHash := ociExpertModeRbacHash
	ociExpertModeRbacHash = "env-rbac-checksum"
	t.Cleanup(func() { ociExpertModeRbacHash = origHash })

	// Flag on: the expert-mode password is not resolved and not forwarded.
	setEnableAdminTempPassword(t, true)

	env := newRbacTestEnv(t)
	mockVlmClient := installMockVlmForRbac(t)

	var capturedReq *vlm.OntapExpertModeUserConfig
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything,
		mock.MatchedBy(func(req *vlm.OntapExpertModeUserConfig) bool {
			capturedReq = req
			return true
		})).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "checksum-abc"}, nil)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	if assert.NotNil(t, capturedReq) {
		assert.Equal(t, "vault-resolved-pw", capturedReq.OntapCredentials.AdminPassword,
			"ONTAP admin password must come from the activity, not pool.PoolCredentials.Password")
		assert.NotEqual(t, "ontap-pw", capturedReq.OntapCredentials.AdminPassword,
			"regression: must NOT be pool.PoolCredentials.Password")
		assert.Empty(t, capturedReq.ExpertModeUserCredentials.AdminPassword,
			"RBAC refresh must not forward an expert-mode user password")
		assert.Equal(t, ociExpertModeRbacHash, capturedReq.RbacFileChecksum,
			"RBAC checksum must come from OCI_EXPERT_MODE_RBAC_FILE_CHECKSUM")
		assert.Equal(t, ociExpertModeUsername, capturedReq.Username)
	}
	env.AssertExpectations(t)
}

// When ENABLE_ADMIN_TEMP_PASSWORD is true, the RBAC refresh marks the VLM request
// as an update (IsUpdate=true) so VLM treats it as an expert-mode role update
// rather than a first-time create, and it must not forward an expert-mode password.
func TestOCIRefreshRbacForPoolWorkflow_SetsIsUpdateWhenAdminTempPasswordEnabled(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	// Flag on: the request must be marked as an update.
	setEnableAdminTempPassword(t, true)

	env := newRbacTestEnv(t)
	mockVlmClient := installMockVlmForRbac(t)

	var capturedReq *vlm.OntapExpertModeUserConfig
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything,
		mock.MatchedBy(func(req *vlm.OntapExpertModeUserConfig) bool {
			capturedReq = req
			return true
		})).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "checksum-abc"}, nil)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	if assert.NotNil(t, capturedReq) {
		assert.True(t, capturedReq.IsUpdate,
			"IsUpdate must be true when ENABLE_ADMIN_TEMP_PASSWORD is enabled")
		assert.Empty(t, capturedReq.ExpertModeUserCredentials.AdminPassword,
			"expert-mode password must not be forwarded when the flag is enabled")
	}
	env.AssertExpectations(t)
}

// When ENABLE_ADMIN_TEMP_PASSWORD is false, the RBAC refresh resolves the
// expert-mode user password (here from OCI_EXPERT_MODE_PASSWORD) and forwards it
// in ExpertModeUserCredentials, while RbacFileChecksum is still sent.
func TestOCIRefreshRbacForPoolWorkflow_RequestShape_ForwardsExpertPasswordWhenFlagDisabled(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	origHash := ociExpertModeRbacHash
	ociExpertModeRbacHash = "env-rbac-checksum"
	t.Cleanup(func() { ociExpertModeRbacHash = origHash })

	// Flag off: the expert-mode password is resolved and forwarded.
	setEnableAdminTempPassword(t, false)
	setOCIExpertModePasswordForRbac(t, "expert-mode-pw")

	env := newRbacTestEnv(t)
	mockVlmClient := installMockVlmForRbac(t)

	var capturedReq *vlm.OntapExpertModeUserConfig
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything,
		mock.MatchedBy(func(req *vlm.OntapExpertModeUserConfig) bool {
			capturedReq = req
			return true
		})).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "checksum-abc"}, nil)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	if assert.NotNil(t, capturedReq) {
		assert.Equal(t, "expert-mode-pw", capturedReq.ExpertModeUserCredentials.AdminPassword,
			"expert-mode password must be forwarded when ENABLE_ADMIN_TEMP_PASSWORD is false")
		assert.Equal(t, ociExpertModeRbacHash, capturedReq.RbacFileChecksum,
			"RBAC checksum must still be sent")
	}
	env.AssertExpectations(t)
}

// rbacPoolWithExpertSecret returns a pool whose PoolCredentials carry an OCI
// Vault reference for the expert-mode secret so the RBAC refresh resolves the
// expert-mode password via GetExpertModeCredentialsForOCI.
func rbacPoolWithExpertSecret() *datamodel.Pool {
	pool := defaultRbacPool()
	pool.PoolCredentials.ExpertModeSecret = &datamodel.ExternalCredRef{
		ExternalIdentifier: "ocid1.vaultsecret..expert",
		Version:            3,
	}
	return pool
}

// When ENABLE_ADMIN_TEMP_PASSWORD is false and OCI_EXPERT_MODE_PASSWORD is unset,
// the expert-mode password is resolved from the OCI Vault secret referenced by
// PoolCredentials.ExpertModeSecret and forwarded in ExpertModeUserCredentials.
func TestOCIRefreshRbacForPoolWorkflow_ResolvesExpertPasswordFromVault(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	setEnableAdminTempPassword(t, false)
	setOCIExpertModePasswordForRbac(t, "")

	env := newRbacTestEnv(t)
	mockVlmClient := installMockVlmForRbac(t)

	var capturedReq *vlm.OntapExpertModeUserConfig
	mockVlmClient.On("CreateVSAExpertModeUser", mock.Anything,
		mock.MatchedBy(func(req *vlm.OntapExpertModeUserConfig) bool {
			capturedReq = req
			return true
		})).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "checksum-abc"}, nil)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetExpertModeCredentialsForOCI", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-expert-pw"}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-resolved-pw"}, nil)
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), rbacPoolWithExpertSecret())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	if assert.NotNil(t, capturedReq) {
		assert.Equal(t, "vault-expert-pw", capturedReq.ExpertModeUserCredentials.AdminPassword,
			"expert-mode password must come from the OCI Vault secret when the env var is unset")
	}
	env.AssertExpectations(t)
}

// Flag off + env password unset + no ExpertModeSecret must fail before any VLM
// call, since there is no way to resolve the expert-mode password.
func TestOCIRefreshRbacForPoolWorkflow_MissingExpertModeSecret(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	setEnableAdminTempPassword(t, false)
	setOCIExpertModePasswordForRbac(t, "")

	env := newRbacTestEnv(t)
	installMockVlmForRbac(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)

	// ExpertModeSecret left nil on the default pool.
	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), defaultRbacPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "ExpertModeSecret is required")
	env.AssertExpectations(t)
}

// A Vault lookup failure while resolving the expert-mode password must abort the
// RBAC refresh.
func TestOCIRefreshRbacForPoolWorkflow_ExpertModeCredentialsFail(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	setEnableAdminTempPassword(t, false)
	setOCIExpertModePasswordForRbac(t, "")

	env := newRbacTestEnv(t)
	installMockVlmForRbac(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetExpertModeCredentialsForOCI", mock.Anything, mock.Anything, mock.Anything).
		Return((*vlm.OntapCredentials)(nil), assert.AnError)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), rbacPoolWithExpertSecret())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// An empty password returned from the Vault activity must be treated as an
// error rather than silently forwarded.
func TestOCIRefreshRbacForPoolWorkflow_EmptyVaultExpertPassword(t *testing.T) {
	origURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origURL })

	setEnableAdminTempPassword(t, false)
	setOCIExpertModePasswordForRbac(t, "")

	env := newRbacTestEnv(t)
	installMockVlmForRbac(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetExpertModeCredentialsForOCI", mock.Anything, mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: ""}, nil)

	env.ExecuteWorkflow(OCIRefreshRbacForPoolWorkflow, defaultRbacParams(), rbacPoolWithExpertSecret())

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "expert mode password is empty")
	env.AssertExpectations(t)
}
