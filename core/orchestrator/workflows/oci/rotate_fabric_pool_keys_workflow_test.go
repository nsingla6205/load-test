package oci

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newRotateFabricPoolKeysTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	return env
}

// installMockVlmForRotate swaps the package-level GetNewVSAClientWorkflowManager
// factory so the workflow uses a mockery-generated VlmWorkflowClient mock
// instead of the real VLM child workflow dispatcher. Cleaned up on test exit.
func installMockVlmForRotate(t *testing.T) *vlm.MockVlmWorkflowClient {
	t.Helper()
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	t.Cleanup(func() { workflows.GetNewVSAClientWorkflowManager = orig })
	return mockVlm
}

func defaultRotateParams() *common.RotateFabricPoolKeysParams {
	return &common.RotateFabricPoolKeysParams{
		AccountName:   "ocid1.compartment..x",
		PoolOCID:      "ocid1.pool.oc1..y",
		NewSecretOCID: "ocid1.vaultsecret..new",
	}
}

func defaultRotatePool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "pool-uuid"},
		Name:            "test-pool",
		VLMConfig:       `{"deployment":{"ociconfig":{"fabric_pool_config":{"secret_ocid":"ocid1.vaultsecret..old"}}}}`,
		PoolCredentials: &datamodel.PoolCredentials{Password: "ontap-pw"},
		BuildInfo:       &datamodel.PoolBuildInfo{OntapVersion: "9.19.1"},
	}
}

// ---------------------------------------------------------------------------
// arg/Setup validation
// ---------------------------------------------------------------------------

func TestOCIRotateFabricPoolKeysWorkflow_NilParams(t *testing.T) {
	env := newRotateFabricPoolKeysTestEnv(t)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, (*common.RotateFabricPoolKeysParams)(nil), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "invalid params")
}

func TestOCIRotateFabricPoolKeysWorkflow_NilPool(t *testing.T) {
	env := newRotateFabricPoolKeysTestEnv(t)

	// Pool arg is consumed inside Run, after Setup succeeds. The extractor
	// rejects nil pool with a typed "must not be nil" error.
	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), (*datamodel.Pool)(nil))

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "must not be nil")
}

// ---------------------------------------------------------------------------
// activity failure paths
// ---------------------------------------------------------------------------

func TestOCIRotateFabricPoolKeysWorkflow_ParseVLMConfigFails(t *testing.T) {
	env := newRotateFabricPoolKeysTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return((*vlm.VLMConfig)(nil), assert.AnError)
	// Rollback MUST fire (ErroredPool) when ParseVlmConfig fails.
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIRotateFabricPoolKeysWorkflow_GetOnTapCredsFails(t *testing.T) {
	env := newRotateFabricPoolKeysTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return((*vlm.OntapCredentials)(nil), assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIRotateFabricPoolKeysWorkflow_ValidateSecretFails(t *testing.T) {
	env := newRotateFabricPoolKeysTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	// Caller-supplied secret OCID is unreadable / wrong shape / etc.
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return((*vlm.FabricPoolConfig)(nil), assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// VLM client contract
// ---------------------------------------------------------------------------

// vlmRotateResponse builds a canonical happy-path response: VLM echoes the
// post-rotation VLMConfig with the new SecretOcid programmed in.
func vlmRotateResponse(newSecretOCID string) *vlm.RotateFabricPoolKeysResponse {
	resp := &vlm.RotateFabricPoolKeysResponse{}
	resp.VLMConfig.Deployment.ProviderConfig = vlm.ProviderConfigWrapper{
		ProviderConfig: vlm.OCIConfig{
			FabricPoolConfig: vlm.FabricPoolConfig{SecretOcid: newSecretOCID},
		},
	}
	return resp
}

func TestOCIRotateFabricPoolKeysWorkflow_VLMClientFails(t *testing.T) {
	mockVlm := installMockVlmForRotate(t)
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("vlm down"))

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	// VLM failure MUST roll the pool to ERROR and MUST NOT persist the new OCID.
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIRotateFabricPoolKeysWorkflow_VLMNilResponse covers the case where
// VLM returns (nil, nil): we MUST treat that as failure and roll back, never
// silently succeed without a confirmed post-rotation VLMConfig.
func TestOCIRotateFabricPoolKeysWorkflow_VLMNilResponse(t *testing.T) {
	mockVlm := installMockVlmForRotate(t)
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.Anything, mock.Anything).
		Return((*vlm.RotateFabricPoolKeysResponse)(nil), nil)

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIRotateFabricPoolKeysWorkflow_VLMRequestShape asserts the exact
// RotateFabricPoolKeysRequest payload handed to the VLM client: the parsed
// current VLMConfig from ParseVlmConfig, the new secret OCID from params,
// and the ONTAP credentials from GetOnTapCredentialsForOCI.
func TestOCIRotateFabricPoolKeysWorkflow_VLMRequestShape(t *testing.T) {
	mockVlm := installMockVlmForRotate(t)
	parsedVlmConfig := vlm.VLMConfig{
		Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-xyz"},
	}
	ontapCreds := vlm.OntapCredentials{AdminPassword: "vault-pw"}
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.MatchedBy(func(req *vlm.RotateFabricPoolKeysRequest) bool {
		return req != nil &&
			req.NewSecretOcid == "ocid1.vaultsecret..new" &&
			req.NewSecretVersion == nil && // params omit the version -> nil (VLM fetches current)
			req.VLMConfig.Deployment.DeploymentID == "deployment-xyz" &&
			req.OntapCredentials.AdminPassword == "vault-pw"
	}), "9.19.1").Return(vlmRotateResponse("ocid1.vaultsecret..new"), nil)

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&parsedVlmConfig, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&ontapCreds, nil)
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}}, nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIRotateFabricPoolKeysWorkflow_ForwardsSecretVersion asserts that when
// the caller pins a specific vault secret version, the workflow forwards that
// exact version (by value) to the VLM client rather than dropping it.
func TestOCIRotateFabricPoolKeysWorkflow_ForwardsSecretVersion(t *testing.T) {
	mockVlm := installMockVlmForRotate(t)
	wantVersion := int64(7)
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.MatchedBy(func(req *vlm.RotateFabricPoolKeysRequest) bool {
		return req != nil &&
			req.NewSecretVersion != nil &&
			*req.NewSecretVersion == wantVersion
	}), mock.Anything).Return(vlmRotateResponse("ocid1.vaultsecret..new"), nil)

	params := defaultRotateParams()
	params.NewSecretVersion = &wantVersion

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	// The pinned version must also reach the validation activity so it validates
	// the same version VLM will rotate to.
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new",
		mock.MatchedBy(func(v *int64) bool { return v != nil && *v == wantVersion })).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}}, nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, params, defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIRotateFabricPoolKeysWorkflow_PersistReceivesVLMConfig asserts the
// workflow forwards VLM's returned VLMConfig (NOT the pre-rotation snapshot)
// to CreatedPool as the new source of truth.
func TestOCIRotateFabricPoolKeysWorkflow_PersistReceivesVLMConfig(t *testing.T) {
	mockVlm := installMockVlmForRotate(t)
	vlmReturned := &vlm.RotateFabricPoolKeysResponse{}
	vlmReturned.VLMConfig.Deployment.ProviderConfig = vlm.ProviderConfigWrapper{
		ProviderConfig: vlm.OCIConfig{
			CompartmentID: "ocid1.compartment..vlm-says",
			FabricPoolConfig: vlm.FabricPoolConfig{
				SecretOcid: "ocid1.vaultsecret..new",
				BucketName: "tier-bucket",
			},
		},
	}
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.Anything, mock.Anything).Return(vlmReturned, nil)

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	env.OnActivity(
		"CreatedPool",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(cfg *vlm.VLMConfig) bool {
			if cfg == nil {
				return false
			}
			oci, err := cfg.Deployment.ProviderConfig.AsOCI()
			return err == nil &&
				oci.CompartmentID == "ocid1.compartment..vlm-says" &&
				oci.FabricPoolConfig.SecretOcid == "ocid1.vaultsecret..new" &&
				oci.FabricPoolConfig.BucketName == "tier-bucket"
		}),
	).Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}}, nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// happy path
// ---------------------------------------------------------------------------

func TestOCIRotateFabricPoolKeysWorkflow_HappyPath(t *testing.T) {
	mockVlm := installMockVlmForRotate(t)
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.Anything, mock.Anything).
		Return(vlmRotateResponse("ocid1.vaultsecret..new"), nil)

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}}, nil)
	// Happy path: ErroredPool MUST NOT fire.

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIRotateFabricPoolKeysWorkflow_PersistFails_RollsBackToError(t *testing.T) {
	mockVlm := installMockVlmForRotate(t)
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.Anything, mock.Anything).
		Return(vlmRotateResponse("ocid1.vaultsecret..new"), nil)

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), assert.AnError)
	// DB-write-after-VLM-apply failure leaves the cluster mid-state; the
	// rollback flips the pool to ERROR so the operator can drive a manual
	// reconcile.
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), defaultRotatePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIRotateFabricPoolKeysWorkflow_StopsWhenBuildInfoOntapVersionMissing(t *testing.T) {
	withOCIOntapVersionDetails(t, "")
	mockVlm := installMockVlmForRotate(t)
	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	pool := defaultRotatePool()
	pool.BuildInfo = nil

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), pool)

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "buildInfo=")
	mockVlm.AssertNotCalled(t, "RotateFabricPoolKeys", mock.Anything, mock.Anything, mock.Anything)
}

func TestOCIRotateFabricPoolKeysWorkflow_StoredBuildInfoRoutesToExpectedQueue(t *testing.T) {
	withHostileVLMRoutingEnv(t)
	mockVlm := installMockVlmForRotate(t)

	var capturedVersion string
	mockVlm.EXPECT().RotateFabricPoolKeys(mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ workflow.Context, _ *vlm.RotateFabricPoolKeysRequest, ontapVersion string) {
			capturedVersion = ontapVersion
		}).
		Return(vlmRotateResponse("ocid1.vaultsecret..new"), nil)

	env := newRotateFabricPoolKeysTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ValidateOCIFabricPoolSecret", mock.Anything, "ocid1.vaultsecret..new", mock.Anything).
		Return(&vlm.FabricPoolConfig{SecretOcid: "ocid1.vaultsecret..new"}, nil)
	env.OnActivity("CreatedPool", mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid"}}, nil)

	pool := defaultRotatePool()
	pool.BuildInfo = storedOCIRoutingBuildInfo(t)

	env.ExecuteWorkflow(OCIRotateFabricPoolKeysWorkflow, defaultRotateParams(), pool)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	assert.Equal(t, ociStoredOntapVersionQueue, vlm.GetVLMWorkerQueueForVersion(capturedVersion))
	env.AssertExpectations(t)
}
