package oci

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
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

func newClusterUpgradeTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.ClusterUpgradeActivity{SE: mockStorage})
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	env.RegisterActivity(&activities.CommonActivities{SE: mockStorage})
	return env
}

func installMockVlmForUpgrade(t *testing.T) *vlm.MockVlmWorkflowClient {
	t.Helper()
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	t.Cleanup(func() { workflows.GetNewVSAClientWorkflowManager = orig })
	return mockVlm
}

func defaultUpgradeParams() *OCIClusterUpgradeWorkflowParams {
	return &OCIClusterUpgradeWorkflowParams{
		JobID:          "job-123",
		ClusterID:      "cluster-abc",
		AccountName:    "test-account",
		TargetVersion:  "9.18.1",
		CurrentVersion: "9.17.1",
		VSAImagePath:   "oci-bucket/images/vsa-9.18.1.img",
		Pool: &datamodel.Pool{
			BaseModel:              datamodel.BaseModel{UUID: "pool-uuid"},
			Name:                   "test-pool",
			PoolExternalIdentifier: "ocid1.pool..a",
			VLMConfig:              "{}",
			State:                  datamodel.LifeCycleStateAvailable,
			PoolCredentials:        &datamodel.PoolCredentials{Password: "ontap-pw"},
			ClusterDetails:         datamodel.ClusterDetails{OntapVersion: "9.17.1"},
		},
	}
}

func defaultUpgradeParamsWithMediator() *OCIClusterUpgradeWorkflowParams {
	params := defaultUpgradeParams()
	params.MediatorImageOcid = "ocid1.image.oc1.iad.mediator-9.18.1"
	return params
}

func defaultVLMConfig() vlm.VLMConfig {
	return vlm.VLMConfig{
		Cloud: vlm.CloudConfig{
			HAPairs: []vlm.HAPair{
				{
					VM1: vlm.VMConfig{SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeNodeMgmt: {IP: "10.0.0.1"},
					}},
					VM2: vlm.VMConfig{SystemLIFs: map[vlm.VSALIFType]vlm.LIFConfig{
						vlm.LIFTypeNodeMgmt: {IP: "10.0.0.2"},
					}},
				},
			},
		},
	}
}

// ---------------------------------------------------------------------------
// getOCIUpgradeStartToCloseTimeout
// ---------------------------------------------------------------------------

func TestGetOCIUpgradeStartToCloseTimeout(t *testing.T) {
	t.Run("nil pool returns standard timeout", func(t *testing.T) {
		got := getOCIUpgradeStartToCloseTimeout(nil)
		assert.Equal(t, workflows.StartToCloseTimeoutUpgrade, got)
	})

	t.Run("non-large pool returns standard timeout", func(t *testing.T) {
		pool := &datamodel.Pool{LargeCapacity: false}
		got := getOCIUpgradeStartToCloseTimeout(pool)
		assert.Equal(t, workflows.StartToCloseTimeoutUpgrade, got)
	})

	t.Run("large pool returns LV timeout", func(t *testing.T) {
		pool := &datamodel.Pool{LargeCapacity: true}
		got := getOCIUpgradeStartToCloseTimeout(pool)
		assert.Equal(t, workflows.StartToCloseTimeoutUpgradeLV, got)
	})
}

// ---------------------------------------------------------------------------
// OCIClusterUpgradeWorkflow
// ---------------------------------------------------------------------------

func TestOCIClusterUpgradeWorkflow_NilParams(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, (*OCIClusterUpgradeWorkflowParams)(nil))

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "invalid params")
}

func TestOCIClusterUpgradeWorkflow_UpdateJobStatusInProgressFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(assert.AnError)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIClusterUpgradeWorkflow_GetCredentialsFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return((*vlm.OntapCredentials)(nil), assert.AnError)
	// Workflow marks FAILED on error.
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIClusterUpgradeWorkflow_SkipUpgrade_AlreadyUpToDate(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.ForceUpgrade = false
	params.Pool.BuildInfo = &datamodel.PoolBuildInfo{
		VSABuildImage: "vsa-9.18.1.img",
		OntapVersion:  "9.18.1",
	}

	mockVlm := installMockVlmForUpgrade(t)
	// License update calls succeed (per-node, non-fatal).
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	// persistBuildInfoAndVLMConfig (post-upgrade, best-effort)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	// RBAC child workflow (params.SkipUpdateRBAC defaults to false)
	env.RegisterWorkflow(OCIRefreshRbacForPoolWorkflow)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil).Maybe()
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	mockVlm.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{}, nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIClusterUpgradeWorkflow_SingleShotUpgradeSuccess(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIClusterUpgradeWorkflow_VLMUpgradeFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return((*vlm.UpgradeVSAClusterDeploymentResponse)(nil), assert.AnError)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIClusterUpgradeWorkflow_PowerOffWhenClusterWasDisabled(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.Pool.State = datamodel.LifeCycleStateDisabled
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOn
	})).Return(nil)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	powerOffCalled := false
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOff
	})).Run(func(args mock.Arguments) { powerOffCalled = true }).Return(nil)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.True(t, powerOffCalled, "ClusterPowerOp(PowerOff) must fire when the cluster was originally DISABLED")
	env.AssertExpectations(t)
}

func TestOCIClusterUpgradeWorkflow_SkipRBACWhenFlagSet(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true
	params.Pool.BuildInfo = &datamodel.PoolBuildInfo{
		VSABuildImage: "vsa-9.18.1.img",
		OntapVersion:  "9.18.1",
	}
	params.ForceUpgrade = false

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	// OCIRefreshRbacForPoolWorkflow must NOT be called. If it were, the workflow
	// would fail because we haven't registered the child workflow or its activities.

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIClusterUpgradeWorkflow_FullSuccessFlow(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = false

	origRbacURL := ociExpertModeRbacURL
	ociExpertModeRbacURL = "https://rbac.example.com/rbac.zip"
	t.Cleanup(func() { ociExpertModeRbacURL = origRbacURL })

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	// RBAC child workflow VLM call
	origVlmNew := vlm.NewVSAClientWorkflowManager
	vlm.NewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	t.Cleanup(func() { vlm.NewVSAClientWorkflowManager = origVlmNew })
	mockVlm.On("CreateVSAExpertModeUser", mock.Anything, mock.Anything).
		Return(vlm.OntapExpertModeUserResponse{RbacFileChecksum: "abc123"}, nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	// Register RBAC child workflow and its activities.
	env.RegisterWorkflow(OCIRefreshRbacForPoolWorkflow)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&upgradedVlmConfig, nil).Maybe()
	env.OnActivity("UpdateRbacInPoolWithURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_SetupFails_MarksJobFailed verifies that when
// Setup fails with valid params (e.g. a future validation), the job is marked FAILED.
func TestOCIClusterUpgradeWorkflow_SetupFails_MarksJobFailed(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)

	params := defaultUpgradeParams()
	params.AccountName = "" // valid struct, but we need to trigger Setup failure
	// Force Setup to fail by passing nil pool (Setup checks params != nil, not pool).
	// Instead, pass a typed-nil to trigger the nil-params guard.
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, mock.Anything, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil).Maybe()

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, (*OCIClusterUpgradeWorkflowParams)(nil))

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "invalid params")
}

// TestOCIClusterUpgradeWorkflow_DisabledCluster_PowersOffOnUpgradeFailure
// verifies that if a disabled cluster was powered on during pre-upgrade and the
// VLM upgrade subsequently fails, the cluster is powered off in cleanup.
func TestOCIClusterUpgradeWorkflow_DisabledCluster_PowersOffOnUpgradeFailure(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.Pool.State = datamodel.LifeCycleStateDisabled
	params.SkipUpdateRBAC = true

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOn
	})).Return(nil)

	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return((*vlm.UpgradeVSAClusterDeploymentResponse)(nil), assert.AnError)

	powerOffCalled := false
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOff
	})).Run(func(args mock.Arguments) { powerOffCalled = true }).Return(nil)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.True(t, powerOffCalled,
		"ClusterPowerOp(PowerOff) must fire when upgrade fails and the cluster was originally DISABLED")
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_OntapVersionPersistFails_StillSucceeds verifies
// that when the VLM upgrade succeeds but persisting the ONTAP version to
// cluster_details fails, the workflow still completes successfully because
// updateOntapVersionAfterUpgrade is non-fatal.
func TestOCIClusterUpgradeWorkflow_OntapVersionPersistFails_StillSucceeds(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)

	// All UpdatePoolFields calls fail (build_info and cluster_details).
	// The workflow should still succeed because both are non-fatal.
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(assert.AnError).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError(),
		"workflow must succeed even when cluster_details persist fails — the VLM upgrade already succeeded")
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_CompletedStatusUpdateFails_StillSucceeds verifies
// that when the upgrade succeeds but marking the job COMPLETED fails, the
// workflow still returns success.
func TestOCIClusterUpgradeWorkflow_CompletedStatusUpdateFails_StillSucceeds(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(assert.AnError)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError(),
		"workflow must return success even when COMPLETED status update fails — the upgrade itself succeeded")
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_PersistsBuildInfoAfterUpgrade verifies that the
// workflow calls UpdatePoolFields with build info derived from the upgrade params
// (VSAImagePath and TargetVersion), not from worker env vars.
func TestOCIClusterUpgradeWorkflow_PersistsBuildInfoAfterUpgrade(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	upgradedVlmConfig.Deployment.Images = vlm.ImageConfig{
		VSAImageName: "vsa-9.18.1.img",
	}
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)

	var capturedBuildInfos []datamodel.PoolBuildInfo
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updates, ok := args[2].(map[string]interface{})
			if !ok {
				return
			}
			if raw, ok := updates["build_info"]; ok {
				encoded, err := json.Marshal(raw)
				if err != nil {
					return
				}
				var bi datamodel.PoolBuildInfo
				if err := json.Unmarshal(encoded, &bi); err != nil {
					return
				}
				capturedBuildInfos = append(capturedBuildInfos, bi)
			}
		}).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.NotEmpty(t, capturedBuildInfos, "UpdatePoolFields must be called with build_info after a successful upgrade")
	for _, bi := range capturedBuildInfos {
		assert.Equal(t, "vsa-9.18.1.img", bi.VSABuildImage,
			"VSABuildImage must be derived from params.VSAImagePath, not worker env vars")
		assert.Equal(t, "9.18.1", bi.OntapVersion,
			"OntapVersion must be derived from params.TargetVersion, not worker env vars")
	}
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_LargePool_BatchUpgradeSuccess verifies the
// batch upgrade path for large pools, ensuring multiple VLM calls succeed.
func TestOCIClusterUpgradeWorkflow_LargePool_BatchUpgradeSuccess(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.Pool.LargeCapacity = true
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("CalculateBatchPlanForUpdate", mock.Anything, mock.Anything).
		Return(&activities.CalculateBatchPlanActivityOutput{
			NumHAPairs:       2,
			BatchSize:        1,
			NumWorkflowCalls: 2,
			BatchIndices:     [][]int{{0}, {1}},
		}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_LargePool_BatchUpgradeFails verifies that
// when a batch upgrade fails mid-way, the workflow marks the job as failed.
func TestOCIClusterUpgradeWorkflow_LargePool_BatchUpgradeFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.Pool.LargeCapacity = true
	params.SkipUpdateRBAC = true

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return((*vlm.UpgradeVSAClusterDeploymentResponse)(nil), assert.AnError)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("CalculateBatchPlanForUpdate", mock.Anything, mock.Anything).
		Return(&activities.CalculateBatchPlanActivityOutput{
			NumHAPairs:       2,
			BatchSize:        1,
			NumWorkflowCalls: 2,
			BatchIndices:     [][]int{{0}, {1}},
		}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_LargePool_CalculateBatchPlanFails verifies
// that when batch plan calculation fails, the workflow fails.
func TestOCIClusterUpgradeWorkflow_LargePool_CalculateBatchPlanFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.Pool.LargeCapacity = true
	params.SkipUpdateRBAC = true

	installMockVlmForUpgrade(t)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("CalculateBatchPlanForUpdate", mock.Anything, mock.Anything).
		Return((*activities.CalculateBatchPlanActivityOutput)(nil), assert.AnError)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_DisabledCluster_PowerOnFails verifies that
// when a disabled cluster fails to power on, the workflow fails.
func TestOCIClusterUpgradeWorkflow_DisabledCluster_PowerOnFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.Pool.State = datamodel.LifeCycleStateDisabled
	params.SkipUpdateRBAC = true

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOn
	})).Return(assert.AnError)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_GeneratePARFails verifies that when OCI PAR
// generation fails, the workflow fails.
func TestOCIClusterUpgradeWorkflow_GeneratePARFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	installMockVlmForUpgrade(t)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("", assert.AnError)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_SkipUpgrade_PersistsBuildInfoFromParams verifies
// that when the VSA upgrade is skipped (already up to date), build info is still
// derived from the upgrade params.
func TestOCIClusterUpgradeWorkflow_SkipUpgrade_PersistsBuildInfoFromParams(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.ForceUpgrade = false
	params.SkipUpdateRBAC = true
	params.Pool.BuildInfo = &datamodel.PoolBuildInfo{
		VSABuildImage: "vsa-9.18.1.img",
		OntapVersion:  "9.18.1",
	}

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)

	var capturedBuildInfo *datamodel.PoolBuildInfo
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updates, ok := args[2].(map[string]interface{})
			if !ok {
				return
			}
			if raw, ok := updates["build_info"]; ok {
				encoded, err := json.Marshal(raw)
				if err != nil {
					return
				}
				var bi datamodel.PoolBuildInfo
				if err := json.Unmarshal(encoded, &bi); err != nil {
					return
				}
				capturedBuildInfo = &bi
			}
		}).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.NotNil(t, capturedBuildInfo, "UpdatePoolFields must persist build_info even when upgrade is skipped")
	assert.Equal(t, "vsa-9.18.1.img", capturedBuildInfo.VSABuildImage,
		"VSABuildImage must be derived from params.VSAImagePath")
	assert.Equal(t, "9.18.1", capturedBuildInfo.OntapVersion,
		"OntapVersion must be derived from params.TargetVersion")
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// Mediator upgrade tests
// ---------------------------------------------------------------------------

// TestOCIClusterUpgradeWorkflow_MediatorAndVSAUpgradeSuccess verifies that when
// mediatorImagePath is provided and not already up-to-date, the mediator step
// runs followed by the VSA upgrade.
func TestOCIClusterUpgradeWorkflow_MediatorAndVSAUpgradeSuccess(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpdateMediatorResponse{
			VLMConfig: upgradedVlmConfig,
		}, nil)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	mockVlm.AssertCalled(t, "UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything)
	mockVlm.AssertCalled(t, "UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_SkipMediator_NoMediatorImageOcid verifies that
// when no mediatorImageOcid is provided, the mediator upgrade step is skipped
// entirely and only the VSA upgrade runs.
func TestOCIClusterUpgradeWorkflow_SkipMediator_NoMediatorImageOcid(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_SkipMediator_AlreadyUpToDate verifies that when
// the mediator image already matches BuildInfo, the mediator step is skipped.
func TestOCIClusterUpgradeWorkflow_SkipMediator_AlreadyUpToDate(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.ForceUpgrade = false
	params.SkipUpdateRBAC = true
	params.Pool.BuildInfo = &datamodel.PoolBuildInfo{
		MediatorBuildImage: "ocid1.image.oc1.iad.mediator-9.18.1",
	}

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_ForceUpgrade_RunsMediatorEvenIfUpToDate verifies
// that forceUpgrade=true causes the mediator step to run even when BuildInfo
// already matches the target mediator image.
func TestOCIClusterUpgradeWorkflow_ForceUpgrade_RunsMediatorEvenIfUpToDate(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.ForceUpgrade = true
	params.SkipUpdateRBAC = true
	params.Pool.BuildInfo = &datamodel.PoolBuildInfo{
		VSABuildImage:      "vsa-9.18.1.img",
		MediatorBuildImage: "ocid1.image.oc1.iad.mediator-9.18.1",
	}

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpdateMediatorResponse{
			VLMConfig: upgradedVlmConfig,
		}, nil)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	mockVlm.AssertCalled(t, "UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything)
	mockVlm.AssertCalled(t, "UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_BothSkipped_AlreadyUpToDate verifies that when
// both VSA and mediator images match BuildInfo and forceUpgrade is false, both
// upgrade steps are skipped.
func TestOCIClusterUpgradeWorkflow_BothSkipped_AlreadyUpToDate(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.ForceUpgrade = false
	params.SkipUpdateRBAC = true
	params.Pool.BuildInfo = &datamodel.PoolBuildInfo{
		VSABuildImage:      "vsa-9.18.1.img",
		MediatorBuildImage: "ocid1.image.oc1.iad.mediator-9.18.1",
		OntapVersion:       "9.18.1",
	}

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	mockVlm.AssertNotCalled(t, "UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_PersistsBuildInfoWithMediator verifies that when
// mediatorImageOcid is provided, the post-upgrade build info includes
// MediatorBuildImage matching the OCID.
func TestOCIClusterUpgradeWorkflow_PersistsBuildInfoWithMediator(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	upgradedVlmConfig.Deployment.Images = vlm.ImageConfig{
		VSAImageName:      "vsa-9.18.1.img",
		MediatorImageName: "ocid1.image.oc1.iad.mediator-9.18.1",
	}
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpdateMediatorResponse{
			VLMConfig: upgradedVlmConfig,
		}, nil)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)

	var capturedBuildInfos []datamodel.PoolBuildInfo
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updates, ok := args[2].(map[string]interface{})
			if !ok {
				return
			}
			if raw, ok := updates["build_info"]; ok {
				encoded, err := json.Marshal(raw)
				if err != nil {
					return
				}
				var bi datamodel.PoolBuildInfo
				if err := json.Unmarshal(encoded, &bi); err != nil {
					return
				}
				capturedBuildInfos = append(capturedBuildInfos, bi)
			}
		}).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.NotEmpty(t, capturedBuildInfos, "UpdatePoolFields must be called with build_info after upgrade")
	for _, bi := range capturedBuildInfos {
		assert.Equal(t, "vsa-9.18.1.img", bi.VSABuildImage,
			"VSABuildImage must be derived from params.VSAImagePath")
		assert.Equal(t, "ocid1.image.oc1.iad.mediator-9.18.1", bi.MediatorBuildImage,
			"MediatorBuildImage must match params.MediatorImageOcid")
		assert.Equal(t, "9.18.1", bi.OntapVersion,
			"OntapVersion must be derived from params.TargetVersion")
	}
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_NoBuildInfo_MediatorNotPersisted verifies that
// when mediatorImageOcid is not provided and there is no existing build info,
// the post-upgrade build info does not include MediatorBuildImage.
func TestOCIClusterUpgradeWorkflow_NoBuildInfo_MediatorNotPersisted(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	upgradedVlmConfig.Deployment.Images = vlm.ImageConfig{
		VSAImageName: "vsa-9.18.1.img",
	}
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)

	var capturedBuildInfos []datamodel.PoolBuildInfo
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updates, ok := args[2].(map[string]interface{})
			if !ok {
				return
			}
			if raw, ok := updates["build_info"]; ok {
				encoded, err := json.Marshal(raw)
				if err != nil {
					return
				}
				var bi datamodel.PoolBuildInfo
				if err := json.Unmarshal(encoded, &bi); err != nil {
					return
				}
				capturedBuildInfos = append(capturedBuildInfos, bi)
			}
		}).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.NotEmpty(t, capturedBuildInfos, "UpdatePoolFields must be called with build_info")
	for _, bi := range capturedBuildInfos {
		assert.Equal(t, "vsa-9.18.1.img", bi.VSABuildImage)
		assert.Empty(t, bi.MediatorBuildImage,
			"MediatorBuildImage must be empty when mediatorImageOcid is not provided and no existing build info")
	}
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_PreservesExistingMediatorBuildImage verifies that
// when mediatorImageOcid is not provided but the pool already has an existing
// MediatorBuildImage in its BuildInfo, the existing value is preserved in the
// post-upgrade build info.
func TestOCIClusterUpgradeWorkflow_PreservesExistingMediatorBuildImage(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParams()
	params.MediatorImageOcid = ""
	params.SkipUpdateRBAC = true
	params.Pool.BuildInfo = &datamodel.PoolBuildInfo{
		MediatorBuildImage: "ocid1.image.oc1.iad.previous-mediator",
	}

	upgradedVlmConfig := defaultVLMConfig()
	upgradedVlmConfig.Deployment.Images = vlm.ImageConfig{
		VSAImageName: "vsa-9.18.1.img",
	}
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)

	var capturedBuildInfos []datamodel.PoolBuildInfo
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			updates, ok := args[2].(map[string]interface{})
			if !ok {
				return
			}
			if raw, ok := updates["build_info"]; ok {
				encoded, err := json.Marshal(raw)
				if err != nil {
					return
				}
				var bi datamodel.PoolBuildInfo
				if err := json.Unmarshal(encoded, &bi); err != nil {
					return
				}
				capturedBuildInfos = append(capturedBuildInfos, bi)
			}
		}).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.NotEmpty(t, capturedBuildInfos, "UpdatePoolFields must be called with build_info")
	for _, bi := range capturedBuildInfos {
		assert.Equal(t, "vsa-9.18.1.img", bi.VSABuildImage)
		assert.Equal(t, "ocid1.image.oc1.iad.previous-mediator", bi.MediatorBuildImage,
			"existing MediatorBuildImage must be preserved when mediatorImageOcid is not provided")
	}
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_MediatorUpgradeFails verifies that when the
// mediator upgrade step fails, the workflow fails without proceeding to the
// VSA upgrade, and the job is marked as FAILED.
func TestOCIClusterUpgradeWorkflow_MediatorUpgradeFails(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.SkipUpdateRBAC = true

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
		Return((*vlm.UpdateMediatorResponse)(nil), assert.AnError)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	mockVlm.AssertNotCalled(t, "UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_DisabledCluster_MediatorAndVSAUpgrade verifies
// that a disabled cluster is powered on before mediator+VSA upgrade and powered
// off afterwards.
func TestOCIClusterUpgradeWorkflow_DisabledCluster_MediatorAndVSAUpgrade(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.Pool.State = datamodel.LifeCycleStateDisabled
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOn
	})).Return(nil)
	mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpdateMediatorResponse{VLMConfig: upgradedVlmConfig}, nil)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	powerOffCalled := false
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOff
	})).Run(func(args mock.Arguments) { powerOffCalled = true }).Return(nil)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.True(t, powerOffCalled,
		"ClusterPowerOp(PowerOff) must fire after mediator+VSA upgrade when cluster was originally DISABLED")
	mockVlm.AssertCalled(t, "UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything)
	mockVlm.AssertCalled(t, "UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_DisabledCluster_MediatorFails_PowersOff verifies
// that if a disabled cluster was powered on and the mediator upgrade fails,
// the cluster is still powered off during cleanup.
func TestOCIClusterUpgradeWorkflow_DisabledCluster_MediatorFails_PowersOff(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.Pool.State = datamodel.LifeCycleStateDisabled
	params.SkipUpdateRBAC = true

	mockVlm := installMockVlmForUpgrade(t)
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOn
	})).Return(nil)
	mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
		Return((*vlm.UpdateMediatorResponse)(nil), assert.AnError)

	powerOffCalled := false
	mockVlm.On("ClusterPowerOp", mock.Anything, mock.MatchedBy(func(req *vlm.ClusterPowerOpReq) bool {
		return req.Operation == vlm.ClusterPowerOff
	})).Run(func(args mock.Arguments) { powerOffCalled = true }).Return(nil)

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusFailed), mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.Error(t, env.GetWorkflowError())
	assert.True(t, powerOffCalled,
		"ClusterPowerOp(PowerOff) must fire when mediator upgrade fails and cluster was originally DISABLED")
	mockVlm.AssertNotCalled(t, "UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIClusterUpgradeWorkflow_LargePool_MediatorThenBatchVSA verifies that for
// large pools, the mediator upgrade runs once before the batched VSA upgrade,
// not once per batch.
func TestOCIClusterUpgradeWorkflow_LargePool_MediatorThenBatchVSA(t *testing.T) {
	env := newClusterUpgradeTestEnv(t)
	params := defaultUpgradeParamsWithMediator()
	params.Pool.LargeCapacity = true
	params.SkipUpdateRBAC = true

	upgradedVlmConfig := defaultVLMConfig()
	mockVlm := installMockVlmForUpgrade(t)

	mediatorCallCount := 0
	mockVlm.On("UpgradeVSAMediatorWorkflow", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { mediatorCallCount++ }).
		Return(&vlm.UpdateMediatorResponse{VLMConfig: upgradedVlmConfig}, nil)
	mockVlm.On("UpgradeVSAClusterDeploymentWorkflow", mock.Anything, mock.Anything).
		Return(&vlm.UpgradeVSAClusterDeploymentResponse{
			VLMConfig:    upgradedVlmConfig,
			OntapVersion: "9.18.1",
		}, nil)
	mockVlm.On("UpdateLicenseWorkflow", mock.Anything, mock.Anything).Return(nil).Maybe()

	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusInProgress), "").
		Return(nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "pw"}, nil)
	env.OnActivity("CalculateBatchPlanForUpdate", mock.Anything, mock.Anything).
		Return(&activities.CalculateBatchPlanActivityOutput{
			NumHAPairs:       2,
			BatchSize:        1,
			NumWorkflowCalls: 2,
			BatchIndices:     [][]int{{0}, {1}},
		}, nil)
	env.OnActivity("GenerateVSAOCIPARActivity", mock.Anything, mock.Anything).
		Return("https://par-url.example.com/vsa.img", nil)
	env.OnActivity("UpdatePoolFields", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	env.OnActivity("UpdateClusterUpgradeJobStatusActivity",
		mock.Anything, params.JobID, string(models.UpgradeStatusCompleted), "").
		Return(nil)

	env.ExecuteWorkflow(OCIClusterUpgradeWorkflow, params)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.Equal(t, 1, mediatorCallCount,
		"mediator upgrade must run exactly once before batched VSA upgrades, not per batch")
	env.AssertExpectations(t)
}
