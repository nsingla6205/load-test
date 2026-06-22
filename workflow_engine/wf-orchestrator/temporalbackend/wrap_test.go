package temporalbackend

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func newEnv() *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	return ts.NewTestWorkflowEnvironment()
}

func TestWrapWorkflow0(t *testing.T) {
	env := newEnv()
	wf := WrapWorkflow0(func(_ *orchestrator.Context) (string, error) { return "zero", nil })
	env.RegisterWorkflowWithOptions(wf, workflow.RegisterOptions{Name: "wrap0"})

	env.ExecuteWorkflow("wrap0")
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out string
	require.NoError(t, env.GetWorkflowResult(&out))
	assert.Equal(t, "zero", out)
}

func TestWrapWorkflow0Err(t *testing.T) {
	env := newEnv()
	wantErr := errors.New("boom0")
	wf := WrapWorkflow0Err(func(_ *orchestrator.Context) error { return wantErr })
	env.RegisterWorkflowWithOptions(wf, workflow.RegisterOptions{Name: "wrap0err"})

	env.ExecuteWorkflow("wrap0err")
	require.True(t, env.IsWorkflowCompleted())
	require.Error(t, env.GetWorkflowError())
}

func TestWrapWorkflow1(t *testing.T) {
	env := newEnv()
	wf := WrapWorkflow1(func(_ *orchestrator.Context, n int) (int, error) { return n + 1, nil })
	env.RegisterWorkflowWithOptions(wf, workflow.RegisterOptions{Name: "wrap1"})

	env.ExecuteWorkflow("wrap1", 41)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out int
	require.NoError(t, env.GetWorkflowResult(&out))
	assert.Equal(t, 42, out)
}

func TestWrapWorkflow1Err(t *testing.T) {
	env := newEnv()
	wf := WrapWorkflow1Err(func(_ *orchestrator.Context, _ string) error { return nil })
	env.RegisterWorkflowWithOptions(wf, workflow.RegisterOptions{Name: "wrap1err"})

	env.ExecuteWorkflow("wrap1err", "input")
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestWrapWorkflow2(t *testing.T) {
	env := newEnv()
	wf := WrapWorkflow2(func(_ *orchestrator.Context, a, b int) (int, error) { return a * b, nil })
	env.RegisterWorkflowWithOptions(wf, workflow.RegisterOptions{Name: "wrap2"})

	env.ExecuteWorkflow("wrap2", 6, 7)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	var out int
	require.NoError(t, env.GetWorkflowResult(&out))
	assert.Equal(t, 42, out)
}

func TestWrapWorkflow2Err(t *testing.T) {
	env := newEnv()
	wf := WrapWorkflow2Err(func(_ *orchestrator.Context, _ string, _ int) error { return nil })
	env.RegisterWorkflowWithOptions(wf, workflow.RegisterOptions{Name: "wrap2err"})

	env.ExecuteWorkflow("wrap2err", "k", 1)
	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
