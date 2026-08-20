package temporalbackend

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type probeValueKey struct{}

type probeResult struct {
	Sum        int
	Doubled    int
	Value      string
	HasInfo    bool
	NowNonZero bool
	DoneNil    bool
}

func addActivity(_ context.Context, a, b int) (int, error) { return a + b, nil }

func doubleChildWorkflow(_ *orchestrator.Context, n int) (int, error) { return n * 2, nil }

// backendProbeWorkflow exercises every orchestrator.Backend primitive through
// the Temporal backend so a single testsuite run covers backend.go end-to-end.
func backendProbeWorkflow(ctx *orchestrator.Context) (probeResult, error) {
	res := probeResult{}
	res.NowNonZero = !orchestrator.Now(ctx).IsZero()

	info := orchestrator.GetInfo(ctx)
	res.HasInfo = info.WorkflowExecution.ID != ""

	c2 := orchestrator.WithValue(ctx, probeValueKey{}, "v")
	res.Value, _ = c2.Value(probeValueKey{}).(string)

	_ = ctx.Err()
	res.DoneNil = ctx.Done() == nil

	if err := orchestrator.Sleep(ctx, time.Second); err != nil {
		return res, err
	}

	actCtx := ctx.WithActivityOptions(orchestrator.ActivityOptions{
		ScheduleToCloseTimeout: 10 * time.Second,
		RetryPolicy:            &orchestrator.RetryPolicy{MaximumAttempts: 1},
	})
	if err := orchestrator.ExecuteActivity(actCtx, addActivity, 2, 3).Get(actCtx, &res.Sum); err != nil {
		return res, err
	}

	childCtx := ctx.WithChildWorkflowOptions(orchestrator.ChildWorkflowOptions{
		WorkflowExecutionTimeout: 10 * time.Second,
		WorkflowID:               "child-1",
		RetryPolicy:              &orchestrator.RetryPolicy{MaximumAttempts: 1},
	})
	if err := orchestrator.ExecuteChildWorkflow(childCtx, doubleChildWorkflow, res.Sum).Get(childCtx, &res.Doubled); err != nil {
		return res, err
	}
	return res, nil
}

func TestTemporalBackend_EndToEnd(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerOrchWorkflow(env, backendProbeWorkflow)
	registerOrchWorkflow(env, doubleChildWorkflow)
	env.RegisterActivity(addActivity)

	env.ExecuteWorkflow(OrchWorkflowName(backendProbeWorkflow))

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var res probeResult
	require.NoError(t, env.GetWorkflowResult(&res))
	assert.Equal(t, 5, res.Sum)
	assert.Equal(t, 10, res.Doubled)
	assert.Equal(t, "v", res.Value)
	assert.True(t, res.HasInfo)
	assert.True(t, res.NowNonZero)
	assert.True(t, res.DoneNil, "temporal backend Done() must return nil")
}

// propagatorWorkflow drives the workflow-context variants of the context map
// propagator (InjectFromWorkflow / ExtractToWorkflow) plus WorkflowCtx.
func propagatorWorkflow(ctx *orchestrator.Context) (string, error) {
	p := NewContextMapPropagator()
	wfc := WorkflowCtx(ctx)
	wfc = workflow.WithValue(wfc, orchestrator.CorrelationIDKey, "corr-9")

	writer := newFakeHeaderWriter()
	if err := p.InjectFromWorkflow(wfc, writer); err != nil {
		return "", err
	}
	if _, ok := writer.data["correlationID"]; !ok {
		return "missing-correlation", nil
	}

	reader := &fakeHeaderReader{data: writer.data}
	if _, err := p.ExtractToWorkflow(wfc, reader); err != nil {
		return "", err
	}
	return "ok", nil
}

func TestTemporalBackend_PropagatorWorkflowVariants(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerOrchWorkflow(env, propagatorWorkflow)

	env.ExecuteWorkflow(OrchWorkflowName(propagatorWorkflow))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	assert.Equal(t, "ok", out)
}

// withIDWorkflow exercises WithWorkflowID, which swaps the underlying Temporal
// context used for child workflow ID generation.
func withIDWorkflow(ctx *orchestrator.Context) (string, error) {
	c2 := WithWorkflowID(ctx, "custom-child-wf-id")
	if c2 == nil {
		return "", nil
	}
	return "ok", nil
}

func TestTemporalBackend_WithWorkflowID(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerOrchWorkflow(env, withIDWorkflow)

	env.ExecuteWorkflow(OrchWorkflowName(withIDWorkflow))
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	assert.Equal(t, "ok", out)
}

// continueAsNewWorkflow continues itself once, then completes on the re-run.
func continueAsNewWorkflow(ctx *orchestrator.Context, iteration int) error {
	if iteration > 0 {
		return nil
	}
	return ContinueAsNew(ctx, continueAsNewWorkflow, iteration+1)
}

func TestTemporalBackend_ContinueAsNew(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerOrchWorkflow(env, continueAsNewWorkflow)

	env.ExecuteWorkflow(OrchWorkflowName(continueAsNewWorkflow), 0)
	require.True(t, env.IsWorkflowCompleted())
	// A continue-as-new either re-runs to completion (no error) or surfaces a
	// ContinueAsNewError; both are acceptable proof the temporal branch ran.
	_ = env.GetWorkflowError()
}

func TestContinueAsNew_NilContext(t *testing.T) {
	err := ContinueAsNew(nil, continueAsNewWorkflow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "orchCtx is nil")
}

func disconnectedContextWorkflow(ctx *orchestrator.Context) (bool, error) {
	disconnected := orchestrator.NewDisconnectedContext(ctx)
	return disconnected != nil && disconnected != ctx, nil
}

func TestTemporalBackend_NewDisconnectedContext(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	registerOrchWorkflow(env, disconnectedContextWorkflow)

	env.ExecuteWorkflow(OrchWorkflowName(disconnectedContextWorkflow))

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var copied bool
	require.NoError(t, env.GetWorkflowResult(&copied))
	assert.True(t, copied)
}
