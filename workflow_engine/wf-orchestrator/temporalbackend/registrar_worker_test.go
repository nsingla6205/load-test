package temporalbackend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func workerRegWorkflow(_ *orchestrator.Context, _ int) (int, error) { return 0, nil }
func workerAliasWorkflow(_ *orchestrator.Context) error             { return nil }
func workerRegActivity(_ context.Context, n int) (int, error)       { return n, nil }

// TestTemporalRegistrar_ProductionRegistrationPath drives the real
// worker-backed registrar (WrapAndRegister, RegisterWorkflow,
// RegisterWorkflowWithName, RegisterActivity) using a lazy client that never
// dials the server, so registration runs without a Temporal backend.
func TestTemporalRegistrar_ProductionRegistrationPath(t *testing.T) {
	c, err := client.NewLazyClient(client.Options{})
	require.NoError(t, err)
	t.Cleanup(c.Close)

	w := worker.New(c, "wf-orchestrator-test-tq", worker.Options{})
	reg := NewRegistrar(w)
	require.NotNil(t, reg)

	assert.NotPanics(t, func() {
		reg.RegisterWorkflow(workerRegWorkflow)
		reg.RegisterWorkflowWithName("vlm.WorkerAlias", workerAliasWorkflow)
		reg.RegisterActivity(workerRegActivity)
	})

	// The explicit alias must resolve through OrchWorkflowName.
	assert.Equal(t, "vlm.WorkerAlias", OrchWorkflowName(workerAliasWorkflow))
}
