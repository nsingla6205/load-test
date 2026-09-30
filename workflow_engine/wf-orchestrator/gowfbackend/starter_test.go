package gowfbackend

import (
	"context"
	"testing"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func TestWorkflowStarter_FromRunner(t *testing.T) {
	runner, err := NewRunner(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runner.Stop())
	})

	starter := runner.WorkflowStarter()
	require.NotNil(t, starter)
	assert.NotNil(t, starter.client)
}

func TestNewWorkflowStarter_NilClientStartErrors(t *testing.T) {
	s := NewWorkflowStarter(nil)
	require.NotNil(t, s)

	_, err := s.Start(context.Background(), "id-1", func() {}, "arg")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WorkflowStarter not configured")
}

func TestWorkflowStarter_NonFunctionWorkflowErrors(t *testing.T) {
	runner, err := NewRunner(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runner.Stop())
	})

	starter := runner.WorkflowStarter()

	for _, workflowFn := range []interface{}{nil, "not-a-function", 42} {
		_, err := starter.Start(context.Background(), "id-1", workflowFn)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "workflowFn must be a registered workflow function")
	}
}

func TestWorkflowStarter_StartBridgesMiddlewareCorrelation(t *testing.T) {
	runner, err := NewRunner(context.Background())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, runner.Stop())
	})

	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-1")
	instance, err := runner.WorkflowStarter().Start(ctx, "instance-bridge", func() error { return nil })
	require.NoError(t, err)
	require.NotNil(t, instance)
	assert.Equal(t, "instance-bridge", instance.InstanceID)
}

func TestWorkflowStarter_NilReceiverStartErrors(t *testing.T) {
	var s *WorkflowStarter
	_, err := s.Start(context.Background(), "id-1", func() {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WorkflowStarter not configured")
}

// CreateWorkflowInstance runs the registered propagators against the context it
// is given. The HTTP logging middleware writes the correlation id under a typed
// key the propagator does not read, so without the bridge in Start the id never
// reaches the workflow and every downstream log line loses it.
func TestWorkflowStarter_BridgesMiddlewareCorrelationForPropagator(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.CorrelationContextKey, "corr-1")
	propagator := NewContextMapPropagator()

	unbridged := gowf.Metadata{}
	require.NoError(t, propagator.Inject(ctx, &unbridged))
	assert.Empty(t, unbridged.Get("correlationID"), "middleware key alone is invisible to the propagator")

	bridged := gowf.Metadata{}
	require.NoError(t, propagator.Inject(orchestrator.WithRequestContext(ctx), &bridged))
	assert.Equal(t, "corr-1", bridged.Get("correlationID"))
}
