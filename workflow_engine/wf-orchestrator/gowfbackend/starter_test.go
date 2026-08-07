package gowfbackend

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestWorkflowStarter_NilReceiverStartErrors(t *testing.T) {
	var s *WorkflowStarter
	_, err := s.Start(context.Background(), "id-1", func() {})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WorkflowStarter not configured")
}
