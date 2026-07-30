package oci

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

func newResetPasswordTestEnv(t *testing.T) *testsuite.TestWorkflowEnvironment {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})

	mockStorage := database.NewMockStorage(t)
	env.RegisterActivity(&activities.PoolActivity{SE: mockStorage})
	return env
}

func defaultResetPasswordParams() *common.ResetOciAdminPasswordParams {
	return &common.ResetOciAdminPasswordParams{
		AccountName:  "ocid1.tenancy.oc1..x",
		PoolOCID:     "ocid1.pool.oc1..y",
		TempPassword: "Netapp1234",
	}
}

func defaultResetPasswordPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:       datamodel.BaseModel{UUID: "pool-uuid"},
		Name:            "test-pool",
		PoolCredentials: &datamodel.PoolCredentials{Password: "ontap-pw"},
	}
}

func vlmConfigWithCluster(name string) *vlm.VLMConfig {
	cfg := &vlm.VLMConfig{}
	cfg.VsaCluster.ClusterName = name
	return cfg
}

// ---------------------------------------------------------------------------
// arg / Setup validation
// ---------------------------------------------------------------------------

func TestOCIResetOciAdminPasswordWorkflow_NilParams(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, (*common.ResetOciAdminPasswordParams)(nil), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "invalid params")
}

func TestOCIResetOciAdminPasswordWorkflow_NilPool(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), (*datamodel.Pool)(nil))

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "missing or invalid params/pool")
}

func TestOCIResetOciAdminPasswordWorkflow_EmptyTempPassword(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	params := defaultResetPasswordParams()
	params.TempPassword = ""
	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, params, defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "temporary password is required")
}

func TestOCIResetOciAdminPasswordWorkflow_NilPoolCredentials(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	pool := defaultResetPasswordPool()
	pool.PoolCredentials = nil
	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), pool)

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "pool credentials are required")
}

// ---------------------------------------------------------------------------
// activity failure paths
// ---------------------------------------------------------------------------

func TestOCIResetOciAdminPasswordWorkflow_ParseVLMConfigFails(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return((*vlm.VLMConfig)(nil), assert.AnError)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestOCIResetOciAdminPasswordWorkflow_EmptyClusterName(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	// VLM config parsed but has no cluster name — the workflow must abort
	// before attempting the ONTAP reset.
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster(""), nil)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	assert.Contains(t, env.GetWorkflowError().Error(), "cluster name is empty")
	env.AssertExpectations(t)
}

func TestOCIResetOciAdminPasswordWorkflow_SetActivityFails(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, mock.Anything).
		Return(assert.AnError)
	// Neither expire nor record must fire when the set itself failed.

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "ExpireOciAdminPasswordOnVSA", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	env.AssertNotCalled(t, "RecordTempPasswordState", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIResetOciAdminPasswordWorkflow_ExpireActivityFails pins that when the set
// succeeds but the expire fails, the workflow fails and does NOT record the
// temp-credential state (so the reconciler contract is never falsely satisfied).
func TestOCIResetOciAdminPasswordWorkflow_ExpireActivityFails(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity("ExpireOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything).
		Return(assert.AnError)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "RecordTempPasswordState", mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// TestOCIResetOciAdminPasswordWorkflow_RecordFails pins that a failure to
// persist the temp-credential state fails the workflow (so the reconciler
// contract is never silently skipped) even though the ONTAP reset succeeded.
func TestOCIResetOciAdminPasswordWorkflow_RecordFails(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity("ExpireOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything).
		Return(nil)
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything).
		Return(assert.AnError)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// happy path
// ---------------------------------------------------------------------------

func TestOCIResetOciAdminPasswordWorkflow_HappyPath(t *testing.T) {
	env := newResetPasswordTestEnv(t)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	// The set activity must receive the resolved cluster name and the temp
	// password from params; expire runs afterward with the same cluster/user.
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, "Netapp1234").
		Return(nil)
	env.OnActivity("ExpireOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything).
		Return(nil)
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything).
		Return(nil)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}
