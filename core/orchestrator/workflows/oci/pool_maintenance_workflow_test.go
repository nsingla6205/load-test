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

func newMaintenanceTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	return env
}

func installMockVlmForMaintenance(t *testing.T) *vlm.MockVlmWorkflowClient {
	t.Helper()
	mockVlm := vlm.NewMockVlmWorkflowClient(t)
	orig := workflows.GetNewVSAClientWorkflowManager
	workflows.GetNewVSAClientWorkflowManager = func() vlm.VlmWorkflowClient { return mockVlm }
	t.Cleanup(func() { workflows.GetNewVSAClientWorkflowManager = orig })
	return mockVlm
}

// defaultMaintenanceParams targets both nodes of HA pair 1 only, so the batch
// plan yields a single batch / single VLM call. Multi-batch behaviour is
// covered explicitly in TestOCIExecutePoolMaintenanceWorkflow_BatchesAcrossHAPairs.
func defaultMaintenanceParams() *common.PoolMaintenanceParams {
	return &common.PoolMaintenanceParams{
		AccountName:   "ocid1.compartment..x",
		PoolOCID:      "ocid1.pool.oc1..y",
		InstanceOCIDs: []string{"ocid1.instance..a", "ocid1.instance..b"},
		OperationType: common.MaintenanceOperationRebootMigrate,
		WorkflowID:    "opc-work-req-1",
	}
}

func defaultMaintenancePool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "pool-uuid"},
		Name:            "test-pool",
		VLMConfig:       `{"deployment":{"deployment_id":"deployment-xyz"}}`,
		PoolCredentials: &datamodel.PoolCredentials{Password: "ontap-pw"},
	}
}

// twoPairVLMConfig returns a VLM config whose HA-pair topology matches the
// default instance OCIDs: pair 1 owns instance ..a (node1), pair 2 owns
// instance ..c (node3).
func twoPairVLMConfig() *vlm.VLMConfig {
	cfg := &vlm.VLMConfig{}
	cfg.Deployment.DeploymentID = "deployment-xyz"
	cfg.Cloud.HAPairs = []vlm.HAPair{
		{
			VM1: vlm.VMConfig{Name: "node1", VMInstanceID: "ocid1.instance..a"},
			VM2: vlm.VMConfig{Name: "node2", VMInstanceID: "ocid1.instance..b"},
		},
		{
			VM1: vlm.VMConfig{Name: "node3", VMInstanceID: "ocid1.instance..c"},
			VM2: vlm.VMConfig{Name: "node4", VMInstanceID: "ocid1.instance..d"},
		},
	}
	return cfg
}

// nonSharedHAVLMConfig returns a single-HA-pair non_shared_ha config. non_shared_ha
// always has exactly one HA pair and triggers mediator maintenance first.
func nonSharedHAVLMConfig() *vlm.VLMConfig {
	cfg := &vlm.VLMConfig{}
	cfg.Deployment.DeploymentID = "deployment-xyz"
	cfg.Deployment.DeploymentType = vlm.DeploymentTypeNonSharedHA
	cfg.Cloud.HAPairs = []vlm.HAPair{
		{
			VM1:      vlm.VMConfig{Name: "node1", VMInstanceID: "ocid1.instance..a"},
			VM2:      vlm.VMConfig{Name: "node2", VMInstanceID: "ocid1.instance..b"},
			Mediator: vlm.VMConfig{Name: "mediator1", VMInstanceID: "ocid1.instance..m"},
		},
	}
	return cfg
}

// ---------------------------------------------------------------------------
// target builder unit coverage
// ---------------------------------------------------------------------------

func TestBuildMaintenanceTargets_GroupsByZeroBasedHAPairIndex(t *testing.T) {
	targets := buildMaintenanceTargets(twoPairVLMConfig(), []string{"ocid1.instance..a", "ocid1.instance..c"})

	require.Len(t, targets, 2)
	assert.Equal(t, 0, targets[0].HaPairIndex)
	assert.Equal(t, []string{"node1"}, targets[0].NodeNames)
	assert.Equal(t, 1, targets[1].HaPairIndex)
	assert.Equal(t, []string{"node3"}, targets[1].NodeNames)
}

func TestBuildMaintenanceTargets_NoMatchesReturnsNil(t *testing.T) {
	assert.Nil(t, buildMaintenanceTargets(twoPairVLMConfig(), []string{"ocid1.instance..unknown"}))
	assert.Nil(t, buildMaintenanceTargets(nil, []string{"ocid1.instance..a"}))
}

func TestBuildMaintenanceTargets_ExcludesMediator(t *testing.T) {
	assert.Nil(t, buildMaintenanceTargets(nonSharedHAVLMConfig(), []string{"ocid1.instance..m"}))
	targets := buildMaintenanceTargets(nonSharedHAVLMConfig(), []string{"ocid1.instance..a", "ocid1.instance..m"})
	require.Len(t, targets, 1)
	assert.Equal(t, []string{"node1"}, targets[0].NodeNames)
}

// ---------------------------------------------------------------------------
// arg/Setup validation
// ---------------------------------------------------------------------------

func TestOCIExecutePoolMaintenanceWorkflow_NilParams(t *testing.T) {
	env := newMaintenanceTestEnv(t)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, (*common.PoolMaintenanceParams)(nil), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "invalid params")
}

func TestOCIExecutePoolMaintenanceWorkflow_NilPool(t *testing.T) {
	env := newMaintenanceTestEnv(t)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), (*datamodel.Pool)(nil))

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "must not be nil")
}

// ---------------------------------------------------------------------------
// activity failure paths
// ---------------------------------------------------------------------------

func TestOCIExecutePoolMaintenanceWorkflow_ParseVLMConfigFails(t *testing.T) {
	env := newMaintenanceTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return((*vlm.VLMConfig)(nil), assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_NoTargetsResolved(t *testing.T) {
	env := newMaintenanceTestEnv(t)

	// Config topology does not contain any of the requested instance OCIDs.
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(&vlm.VLMConfig{}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "no maintenance targets")
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_GetOnTapCredsFails(t *testing.T) {
	env := newMaintenanceTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return((*vlm.OntapCredentials)(nil), assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// VLM client contract
// ---------------------------------------------------------------------------

func TestOCIExecutePoolMaintenanceWorkflow_VLMClientFails(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(nil, errors.New("vlm down"))

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_VLMNilResponse(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return((*vlm.MaintenanceResponse)(nil), nil)

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// A node-level failure in the VLM response must fail the workflow and roll the
// pool back to ERROR; the pool must never be marked READY.
func TestOCIExecutePoolMaintenanceWorkflow_NodeResultFailed(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
			{HaPairIndex: 1, NodeName: "node2", Succeeded: false, Error: "takeover stuck"},
		}}, nil)

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "node2")
	env.AssertExpectations(t)
}

// TestOCIExecutePoolMaintenanceWorkflow_VLMRequestShape asserts the exact
// MaintenanceRequest payload handed to the VLM client for a single-batch run:
// parsed VLMConfig, operation type, ONTAP credentials, and the 0-based
// HA-pair-indexed target carrying both nodes of the pair.
func TestOCIExecutePoolMaintenanceWorkflow_VLMRequestShape(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MaintenanceRequest) bool {
		return req != nil &&
			req.VLMConfig.Deployment.DeploymentID == "deployment-xyz" &&
			req.MaintenanceType == string(common.MaintenanceOperationRebootMigrate) &&
			req.OntapCredentials.AdminPassword == "vault-pw" &&
			len(req.Targets) == 1 &&
			req.Targets[0].HaPairIndex == 0 &&
			len(req.Targets[0].NodeNames) == 2 &&
			req.Targets[0].NodeNames[0] == "node1" &&
			req.Targets[0].NodeNames[1] == "node2"
	})).Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
		{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
		{HaPairIndex: 1, NodeName: "node2", Succeeded: true},
	}, VLMConfig: *twoPairVLMConfig()}, nil)

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return cfg.Deployment.DeploymentID == "deployment-xyz"
	})).Return(nil).Once()

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIExecutePoolMaintenanceWorkflow_BatchesAcrossHAPairs asserts the
// maintenance batching contract: two targeted HA pairs yield batchSize=1 and
// two sequential VLM calls, one per HA pair index ([[1],[2]]).
func TestOCIExecutePoolMaintenanceWorkflow_BatchesAcrossHAPairs(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	// Batch 1: HA pair 1 (node1). Batch 2: HA pair 2 (node3).
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MaintenanceRequest) bool {
		return req != nil && len(req.Targets) == 1 &&
			req.Targets[0].HaPairIndex == 0 && req.Targets[0].NodeNames[0] == "node1"
	})).Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
		{HaPairIndex: 0, NodeName: "node1", Succeeded: true},
	}, VLMConfig: vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-updated-1"}}}, nil).Once()
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MaintenanceRequest) bool {
		return req != nil && len(req.Targets) == 1 &&
			req.Targets[0].HaPairIndex == 1 && req.Targets[0].NodeNames[0] == "node3" &&
			req.VLMConfig.Deployment.DeploymentID == "deployment-updated-1"
	})).Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
		{HaPairIndex: 1, NodeName: "node3", Succeeded: true},
	}, VLMConfig: vlm.VLMConfig{Deployment: vlm.DeploymentConfig{DeploymentID: "deployment-updated-2"}}}, nil).Once()

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..c"}

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_DoesNotPersistPartialBatchSuccess(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MaintenanceRequest) bool {
		return req != nil && req.Targets[0].HaPairIndex == 0
	})).Return(&vlm.MaintenanceResponse{
		Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 0, NodeName: "node1", Succeeded: true},
		},
		VLMConfig: *twoPairVLMConfig(),
	}, nil).Once()
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MaintenanceRequest) bool {
		return req != nil && req.Targets[0].HaPairIndex == 1
	})).Return(&vlm.MaintenanceResponse{
		Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 1, NodeName: "node3", Succeeded: false, Error: "recreate failed"},
		},
		VLMConfig: *twoPairVLMConfig(),
	}, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..c"}
	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// happy path
// ---------------------------------------------------------------------------

func TestOCIExecutePoolMaintenanceWorkflow_HappyPath(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
			{HaPairIndex: 2, NodeName: "node3", Succeeded: true},
		}, VLMConfig: *twoPairVLMConfig()}, nil)

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_NodeVLMConfigPersistFails(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(&vlm.MaintenanceResponse{
			Results: []vlm.PairMaintenanceResult{
				{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
				{HaPairIndex: 1, NodeName: "node2", Succeeded: true},
			},
			VLMConfig: *twoPairVLMConfig(),
		}, nil)

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return cfg.Deployment.DeploymentID == "deployment-xyz"
	})).
		Return(assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// On non_shared_ha, mediator maintenance runs first (with the targeted HA pair
// index) and only then does instance maintenance run; the pool ends up READY.
func TestOCIExecutePoolMaintenanceWorkflow_NonSharedHA_RunsMediatorFirst(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorMaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MediatorMaintenanceRequest) bool {
		return req != nil &&
			req.VLMConfig.Deployment.DeploymentID == "deployment-xyz" &&
			req.OntapCredentials.AdminPassword == "vault-pw" &&
			len(req.HAPairIndices) == 1 && req.HAPairIndices[0] == 1
	})).Return(nonSharedHAVLMConfig(), nil).Once()
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
			{HaPairIndex: 1, NodeName: "node2", Succeeded: true},
		}, VLMConfig: *nonSharedHAVLMConfig()}, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b", "ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_NonSharedHA_RecreateRunsMediatorITCFirst(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorITCWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MediatorMaintenanceRequest) bool {
		return req != nil &&
			req.VLMConfig.Deployment.DeploymentID == "deployment-xyz" &&
			req.OntapCredentials.AdminPassword == "vault-pw" &&
			len(req.HAPairIndices) == 1 && req.HAPairIndices[0] == 1
	})).Return(nonSharedHAVLMConfig(), nil).Once()
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MaintenanceRequest) bool {
		return req != nil && req.MaintenanceType == string(common.MaintenanceOperationRecreate)
	})).Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
		{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
		{HaPairIndex: 1, NodeName: "node2", Succeeded: true},
	}, VLMConfig: *nonSharedHAVLMConfig()}, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	params := defaultMaintenanceParams()
	params.OperationType = common.MaintenanceOperationRecreate
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b", "ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_MediatorOnly_SkipsMaintenanceWorkflow(t *testing.T) {
	updatedCfg := nonSharedHAVLMConfig()
	updatedCfg.Cloud.HAPairs[0].Mediator.VMInstanceID = "ocid1.instance..m-new"

	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorITCWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MediatorMaintenanceRequest) bool {
		return req != nil &&
			len(req.HAPairIndices) == 1 && req.HAPairIndices[0] == 1
	})).Return(updatedCfg, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return cfg.Cloud.HAPairs[0].Mediator.VMInstanceID == "ocid1.instance..m-new"
	})).Return(nil).Once()
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return cfg.Cloud.HAPairs[0].Mediator.VMInstanceID == "ocid1.instance..m-new"
	})).Return(nil).Once()

	params := defaultMaintenanceParams()
	params.OperationType = common.MaintenanceOperationRecreate
	params.InstanceOCIDs = []string{"ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_MediatorOnly_RebootMigrate_SkipsMaintenanceWorkflow(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorMaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_UsesMediatorConfigForNodesAndSavesNodeResult(t *testing.T) {
	mediatorCfg := nonSharedHAVLMConfig()
	mediatorCfg.Deployment.Labels = map[string]string{"result": "mediator"}
	finalCfg := nonSharedHAVLMConfig()
	finalCfg.Deployment.Labels = map[string]string{"result": "nodes"}

	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorITCWorkflow(mock.Anything, mock.Anything).
		Return(mediatorCfg, nil).Once()
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.MatchedBy(func(req *vlm.MaintenanceRequest) bool {
		return req != nil && req.VLMConfig.Deployment.Labels["result"] == "mediator"
	})).Return(&vlm.MaintenanceResponse{
		Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
			{HaPairIndex: 1, NodeName: "node2", Succeeded: true},
		},
		VLMConfig: *finalCfg,
	}, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return cfg.Deployment.Labels["result"] == "mediator"
	})).Return(nil).Once()
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return cfg.Deployment.Labels["result"] == "nodes"
	})).Return(nil).Once()

	params := defaultMaintenanceParams()
	params.OperationType = common.MaintenanceOperationRecreate
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b", "ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_NodeTargetsMissingFromMediatorResultFails(t *testing.T) {
	mediatorCfg := nonSharedHAVLMConfig()
	mediatorCfg.Cloud.HAPairs = nil

	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorITCWorkflow(mock.Anything, mock.Anything).
		Return(mediatorCfg, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return len(cfg.Cloud.HAPairs) == 0
	})).Return(nil).Once()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	params := defaultMaintenanceParams()
	params.OperationType = common.MaintenanceOperationRecreate
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "no longer contains requested node instances")
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_PersistsMediatorConfigWhenNodeBatchFails(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorMaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil).Once()
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(&vlm.MaintenanceResponse{
			Results: []vlm.PairMaintenanceResult{
				{HaPairIndex: 1, NodeName: "node1", Succeeded: false, Error: "maintenance failed"},
			},
			VLMConfig: *nonSharedHAVLMConfig(),
		}, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b", "ocid1.instance..m"}
	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_NonSharedHA_SaveVLMConfigFails(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorMaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("SavePoolVLMConfig", mock.Anything, mock.Anything, mock.MatchedBy(func(cfg vlm.VLMConfig) bool {
		return cfg.Deployment.DeploymentID == "deployment-xyz"
	})).
		Return(assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b", "ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// If mediator maintenance fails on non_shared_ha, the workflow fails and instance
// maintenance is never dispatched (no MaintenanceWorkflow expectation is set, so
// AssertExpectations catches an unexpected call).
func TestOCIExecutePoolMaintenanceWorkflow_NonSharedHA_MediatorFails(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorMaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(nil, errors.New("mediator recovery failed")).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b", "ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// On non_shared_ha, when the request does not target the mediator instance,
// mediator maintenance is skipped and only instance maintenance runs. No
// VSAMediatorMaintenanceWorkflow expectation is set, so AssertExpectations
// catches an unexpected call.
func TestOCIExecutePoolMaintenanceWorkflow_NonSharedHA_SkipsMediatorWhenNotRequested(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 0, NodeName: "node1", Succeeded: true},
			{HaPairIndex: 0, NodeName: "node2", Succeeded: true},
		}, VLMConfig: *nonSharedHAVLMConfig()}, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_PersistFails_RollsBackToError(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().MaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(&vlm.MaintenanceResponse{Results: []vlm.PairMaintenanceResult{
			{HaPairIndex: 1, NodeName: "node1", Succeeded: true},
			{HaPairIndex: 2, NodeName: "node3", Succeeded: true},
		}, VLMConfig: *twoPairVLMConfig()}, nil)

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("CompletePoolMaintenance", mock.Anything, mock.Anything, mock.Anything).
		Return(assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_NonSharedHA_MediatorNilConfig(t *testing.T) {
	mockVlm := installMockVlmForMaintenance(t)
	mockVlm.EXPECT().VSAMediatorMaintenanceWorkflow(mock.Anything, mock.Anything).
		Return(nil, nil).Once()

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(nonSharedHAVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	params := defaultMaintenanceParams()
	params.InstanceOCIDs = []string{"ocid1.instance..a", "ocid1.instance..b", "ocid1.instance..m"}

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, params, defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIExecutePoolMaintenanceWorkflow_BatchPlanFails(t *testing.T) {
	installMockVlmForMaintenance(t)

	env := newMaintenanceTestEnv(t)
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(twoPairVLMConfig(), nil)
	env.OnActivity("GetOnTapCredentialsForOCI", mock.Anything, mock.Anything).
		Return(&vlm.OntapCredentials{AdminPassword: "vault-pw"}, nil)
	env.OnActivity("CalculateBatchPlanForUpdate", mock.Anything, mock.Anything).
		Return((*activities.CalculateBatchPlanActivityOutput)(nil), assert.AnError)
	env.OnActivity("ErroredPool", mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Pool)(nil), nil)

	env.ExecuteWorkflow(OCIExecutePoolMaintenanceWorkflow, defaultMaintenanceParams(), defaultMaintenancePool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestRequestIncludesMediator(t *testing.T) {
	assert.False(t, requestIncludesMediator(nil, []string{"ocid1.instance..m"}))
	assert.False(t, requestIncludesMediator(nonSharedHAVLMConfig(), nil))
	assert.False(t, requestIncludesMediator(nonSharedHAVLMConfig(), []string{"ocid1.instance..a"}))
	assert.True(t, requestIncludesMediator(nonSharedHAVLMConfig(), []string{"ocid1.instance..m"}))
	assert.False(t, requestIncludesMediator(twoPairVLMConfig(), []string{"ocid1.instance..a"}))
}

func TestOneBasedMediatorHAPairIndices(t *testing.T) {
	assert.Equal(t, []int{1}, oneBasedMediatorHAPairIndices(nonSharedHAVLMConfig(), []string{"ocid1.instance..m"}))
	assert.Nil(t, oneBasedMediatorHAPairIndices(nonSharedHAVLMConfig(), []string{"ocid1.instance..a"}))
}

func TestFailedMaintenanceResults(t *testing.T) {
	results := []vlm.PairMaintenanceResult{
		{HaPairIndex: 0, NodeName: "node1", Succeeded: true},
		{HaPairIndex: 1, NodeName: "node3", Succeeded: false, Error: "boom"},
	}
	failures := failedMaintenanceResults(results)
	require.Len(t, failures, 1)
	assert.Contains(t, failures[0], "node3")
	assert.Contains(t, failures[0], "boom")
}
