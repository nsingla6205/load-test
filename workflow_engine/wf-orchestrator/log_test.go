package orchestrator

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestFieldsType(t *testing.T) {
	fields := Fields{"key1": "value1", "key2": 123, "key3": true}
	assert.Equal(t, "value1", fields["key1"])
	assert.Equal(t, 123, fields["key2"])
	assert.Equal(t, true, fields["key3"])
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "x-correlation-id", CorrelationIDKey)
	assert.Equal(t, "x-deployment-id", DeploymentIDKey)
	assert.Equal(t, "requestCorrelationID", RequestCorrelationID)
	assert.Equal(t, "deploymentID", RequestDeploymentID)
}

func TestInfof(t *testing.T) {
	orig := std
	defer func() { std = orig }()

	m := log.NewMockLogger(t)
	m.EXPECT().Infof("hello %s %d", "world", 7).Return()
	std = m

	Infof("hello %s %d", "world", 7)
}

// withSavedFactory runs fn with a clean factory chain, restoring the global
// afterwards so tests don't leak registration state.
func withSavedFactory(fn func()) {
	prev := workflowLoggerFactory
	defer func() { workflowLoggerFactory = prev }()
	workflowLoggerFactory = nil
	fn()
}

func TestWorkflowLoggerFromContext_NoFactory(t *testing.T) {
	withSavedFactory(func() {
		logger, err := WorkflowLoggerFromContext("anything")
		assert.Nil(t, logger)
		assert.Error(t, err)
	})
}

// stubLogger satisfies UnifiedLogger via the embedded interface so factory-chain
// tests can use a sentinel without depending on a backend implementation.
type stubLogger struct {
	UnifiedLogger
	name string
}

func TestChainWorkflowLoggerFactory_FirstMatchWins(t *testing.T) {
	withSavedFactory(func() {
		sentinel := &stubLogger{name: "second"}

		// Registered first, becomes the fallback.
		ChainWorkflowLoggerFactory(func(ctx interface{}) (UnifiedLogger, error) {
			if ctx == "second" {
				return sentinel, nil
			}
			return nil, errors.New("not mine")
		})

		// Registered second, takes precedence.
		ChainWorkflowLoggerFactory(func(ctx interface{}) (UnifiedLogger, error) {
			if ctx == "first" {
				return &stubLogger{name: "first"}, nil
			}
			return nil, errors.New("not mine")
		})

		// Second factory matches.
		got, err := WorkflowLoggerFromContext("first")
		assert.NoError(t, err)
		assert.NotNil(t, got)

		// Falls through to the first factory.
		got, err = WorkflowLoggerFromContext("second")
		assert.NoError(t, err)
		assert.Equal(t, sentinel, got)

		// Neither matches.
		got, err = WorkflowLoggerFromContext("none")
		assert.Nil(t, got)
		assert.Error(t, err)
	})
}
