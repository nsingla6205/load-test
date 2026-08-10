package util

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// GetLogger must never panic on inputs that are neither context.Context nor
// workflow.Context. Before hardening, an unrecognized type left the internal
// handler nil and dereferencing it panicked.
func TestGetLogger_UnrecognizedContextType_ReturnsLoggerNoPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		logger := GetLogger(42)
		assert.NotNil(t, logger)
	})
}

func TestGetLogger_NilInput_ReturnsLogger(t *testing.T) {
	assert.NotPanics(t, func() {
		logger := GetLogger(nil)
		assert.NotNil(t, logger)
	})
}

func TestGetLogger_PlainContextWithoutLogger_ReturnsFreshLogger(t *testing.T) {
	logger := GetLogger(context.Background())
	assert.NotNil(t, logger)
}
