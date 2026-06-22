package temporalbackend

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// registerOrchWorkflow is the internal-test analogue of
// testenv.RegisterOrchWorkflow. It is kept here (rather than imported from the
// testenv package) because testenv imports this package, so an internal test
// importing testenv would form an import cycle. It reuses the production
// WrapOrchWorkflow so no reflection logic is duplicated.
func registerOrchWorkflow(env *testsuite.TestWorkflowEnvironment, fn interface{}) interface{} {
	wrapped := WrapOrchWorkflow(fn)
	name := OrchWorkflowName(fn)
	func() {
		defer func() {
			if r := recover(); r != nil &&
				!strings.Contains(strings.ToLower(fmt.Sprint(r)), "already registered") {
				panic(r)
			}
		}()
		env.RegisterWorkflowWithOptions(wrapped, workflow.RegisterOptions{Name: name})
	}()
	return wrapped
}

func TestWrapOrchWorkflow_PanicsOnNonFunction(t *testing.T) {
	assert.PanicsWithValue(t, "WrapOrchWorkflow: argument must be a function", func() {
		WrapOrchWorkflow(42)
	})
}

func TestWrapOrchWorkflow_PanicsOnWrongFirstParam(t *testing.T) {
	assert.PanicsWithValue(t, "WrapOrchWorkflow: first parameter must be *orchestrator.Context", func() {
		WrapOrchWorkflow(func(_ context.Context) error { return nil })
	})
}

func TestWrapOrchWorkflow_ReturnsWrappedFunc(t *testing.T) {
	wrapped := WrapOrchWorkflow(func(_ *orchestrator.Context) (string, error) { return "ok", nil })
	assert.NotNil(t, wrapped)
}
