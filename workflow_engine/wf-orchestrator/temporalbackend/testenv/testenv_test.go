package testenv

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/temporalbackend"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func newEnv() *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	return ts.NewTestWorkflowEnvironment()
}

// testEnvRegistrarActivity / Workflow are package-scoped so their runtime names
// stay stable across the registration and execute-by-name calls.
func testEnvRegistrarActivity(_ context.Context, n int) (int, error) { return n + 1, nil }

func testEnvRegistrarWorkflow(ctx *orchestrator.Context, n int) (int, error) {
	actCtx := ctx.WithActivityOptions(orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 5 * time.Second,
		RetryPolicy:            &orchestrator.RetryPolicy{MaximumAttempts: 1},
	})
	var out int
	if err := orchestrator.ExecuteActivity(actCtx, testEnvRegistrarActivity, n).Get(actCtx, &out); err != nil {
		return 0, err
	}
	return out, nil
}

func aliasedWorkflow(_ *orchestrator.Context) error { return nil }

func TestTestEnvRegistrar_RegistersAndRuns(t *testing.T) {
	env := newEnv()
	reg := NewTestEnvRegistrar(env)
	require.NotNil(t, reg)

	reg.RegisterActivity(testEnvRegistrarActivity)
	reg.RegisterWorkflow(testEnvRegistrarWorkflow)
	reg.RegisterWorkflowWithName("vlm.Aliased", aliasedWorkflow)

	// The explicit alias must resolve through OrchWorkflowName afterwards.
	assert.Equal(t, "vlm.Aliased", temporalbackend.OrchWorkflowName(aliasedWorkflow))

	env.ExecuteWorkflow(temporalbackend.OrchWorkflowName(testEnvRegistrarWorkflow), 10)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out int
	require.NoError(t, env.GetWorkflowResult(&out))
	assert.Equal(t, 11, out)
}

func TestRegisterOrchWorkflow_IsIdempotent(t *testing.T) {
	env := newEnv()
	assert.NotPanics(t, func() {
		RegisterOrchWorkflow(env, aliasedWorkflow)
		RegisterOrchWorkflow(env, aliasedWorkflow)
	})
}

func TestWrapOrchWorkflow_ReturnsWrappedFunc(t *testing.T) {
	wrapped := WrapOrchWorkflow(func(_ *orchestrator.Context) (string, error) { return "ok", nil })
	assert.NotNil(t, wrapped)
}

// ---------------------------------------------------------------------------
// PatchOrchFunc (gomonkey-backed). Targets are //go:noinline so the patch is
// observable even when the test binary is built with inlining enabled.
// ---------------------------------------------------------------------------

//go:noinline
func patchStaticTarget(_ *orchestrator.Context, n int) (int, error) { return n, nil }

//go:noinline
func patchBehaviorTarget(_ *orchestrator.Context, n int) (int, error) { return n, nil }

//go:noinline
func patchAdapterTarget(_ *orchestrator.Context, n int) (int, error) { return n, nil }

func TestPatchOrchFunc_StaticReturns(t *testing.T) {
	patches := PatchOrchFunc(patchStaticTarget, 99, nil)
	defer patches.Reset()

	got, err := patchStaticTarget(nil, 5)
	require.NoError(t, err)
	assert.Equal(t, 99, got)
}

func TestPatchOrchFunc_BehaviorFunctionOverride(t *testing.T) {
	patches := PatchOrchFunc(patchBehaviorTarget, func(_ *orchestrator.Context, n int) (int, error) {
		return n * 10, nil
	})
	defer patches.Reset()

	got, err := patchBehaviorTarget(nil, 4)
	require.NoError(t, err)
	assert.Equal(t, 40, got)
}

func TestPatchOrchFunc_AdapterFirstParamMismatch(t *testing.T) {
	// Replacement's first param is workflow.Context while the target takes
	// *orchestrator.Context: PatchOrchFunc must build an adapter.
	patches := PatchOrchFunc(patchAdapterTarget, func(_ workflow.Context, n int) (int, error) {
		return n + 1, nil
	})
	defer patches.Reset()

	got, err := patchAdapterTarget(nil, 7)
	require.NoError(t, err)
	assert.Equal(t, 8, got)
}

func TestPatchOrchFunc_PanicsOnNonFunction(t *testing.T) {
	assert.PanicsWithValue(t, "PatchOrchFunc: fn must be a function", func() {
		PatchOrchFunc(42)
	})
}
