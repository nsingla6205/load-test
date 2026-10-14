package oci

import (
	"testing"
	"time"

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

// createdAtIs matches the RecordTempPasswordState createdAt argument against want.
func createdAtIs(want time.Time) any {
	return mock.MatchedBy(func(createdAt *time.Time) bool {
		return createdAt != nil && createdAt.Equal(want)
	})
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
	// Record must not fire when the set itself failed.

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "RecordTempPasswordState", mock.Anything, mock.Anything, mock.Anything)
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
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything, mock.Anything).
		Return(assert.AnError)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

// TestOCIResetOciAdminPasswordWorkflow_SetRetried_CreatedAtCapturedBeforeSet pins
// that createdAt is the workflow time from before the set step: the set's retry
// backoff advances the workflow clock, yet the record step still receives the
// original start time.
func TestOCIResetOciAdminPasswordWorkflow_SetRetried_CreatedAtCapturedBeforeSet(t *testing.T) {
	env := newResetPasswordTestEnv(t)
	start := time.Date(2026, time.September, 24, 10, 0, 0, 0, time.UTC)
	env.SetStartTime(start)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, mock.Anything).
		Return(assert.AnError).Once()
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, mock.Anything).
		Return(nil).Once()
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything, createdAtIs(start)).
		Return(nil).Once()

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.True(t, env.Now().After(start), "the retry backoff must have advanced the workflow clock")
	env.AssertExpectations(t)
}

// TestOCIResetOciAdminPasswordWorkflow_RecordRetried_KeepsCreatedAt pins that the
// createdAt passed to the record step is the workflow time captured before the
// set, and that it does not move forward when the record step is retried.
func TestOCIResetOciAdminPasswordWorkflow_RecordRetried_KeepsCreatedAt(t *testing.T) {
	env := newResetPasswordTestEnv(t)
	start := time.Date(2026, time.September, 24, 10, 0, 0, 0, time.UTC)
	env.SetStartTime(start)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything, createdAtIs(start)).
		Return(assert.AnError).Once()
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything, createdAtIs(start)).
		Return(nil).Once()

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.True(t, env.Now().After(start), "the retry backoff must have advanced the workflow clock")
	env.AssertNumberOfCalls(t, "RecordTempPasswordState", 2)
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// happy path
// ---------------------------------------------------------------------------

func TestOCIResetOciAdminPasswordWorkflow_HappyPath(t *testing.T) {
	env := newResetPasswordTestEnv(t)
	start := time.Date(2026, time.September, 24, 10, 0, 0, 0, time.UTC)
	env.SetStartTime(start)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	// The set activity must receive the resolved cluster name and the temp
	// password from params; the temp-credential state is recorded afterward
	// with the workflow time captured before the set.
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, "Netapp1234").
		Return(nil)
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything, createdAtIs(start)).
		Return(nil)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "ExpireOciAdminPasswordOnVSA", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}

// ---------------------------------------------------------------------------
// pre-version histories (replay of the original set -> expire -> record graph)
// ---------------------------------------------------------------------------

func onLegacySkipExpireVersion(env *testsuite.TestWorkflowEnvironment) {
	env.OnGetVersion(ociResetPasswordSkipExpireChangeID,
		workflow.DefaultVersion, ociResetPasswordSkipExpireVersion).
		Return(workflow.DefaultVersion)
}

// TestOCIResetOciAdminPasswordWorkflow_LegacyVersion_ExpiresBetweenSetAndRecord
// pins that histories started before the skip-expire change still schedule
// set, expire and record in that order.
func TestOCIResetOciAdminPasswordWorkflow_LegacyVersion_ExpiresBetweenSetAndRecord(t *testing.T) {
	env := newResetPasswordTestEnv(t)
	onLegacySkipExpireVersion(env)

	var calls []string
	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, "Netapp1234").
		Run(func(mock.Arguments) { calls = append(calls, "set") }).
		Return(nil).Once()
	env.OnActivity("ExpireOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything).
		Run(func(mock.Arguments) { calls = append(calls, "expire") }).
		Return(nil).Once()
	env.OnActivity("RecordTempPasswordState", mock.Anything, mock.Anything, mock.Anything).
		Run(func(mock.Arguments) { calls = append(calls, "record") }).
		Return(nil).Once()

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
	assert.Equal(t, []string{"set", "expire", "record"}, calls)
	env.AssertExpectations(t)
}

// TestOCIResetOciAdminPasswordWorkflow_LegacyVersion_ExpireFails pins that on
// pre-version histories a failed expire fails the workflow and does NOT record
// the temp-credential state.
func TestOCIResetOciAdminPasswordWorkflow_LegacyVersion_ExpireFails(t *testing.T) {
	env := newResetPasswordTestEnv(t)
	onLegacySkipExpireVersion(env)

	env.OnActivity("ParseVlmConfig", mock.Anything, mock.Anything).
		Return(vlmConfigWithCluster("cluster-1"), nil)
	env.OnActivity("SetOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything, mock.Anything).
		Return(nil)
	env.OnActivity("ExpireOciAdminPasswordOnVSA", mock.Anything, mock.Anything, "cluster-1", mock.Anything).
		Return(assert.AnError)

	env.ExecuteWorkflow(OCIResetOciAdminPasswordWorkflow, defaultResetPasswordParams(), defaultResetPasswordPool())

	assert.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
	env.AssertNotCalled(t, "RecordTempPasswordState", mock.Anything, mock.Anything, mock.Anything)
	env.AssertExpectations(t)
}
