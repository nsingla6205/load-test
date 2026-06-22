package temporalbackend

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func TestTemporalWorkflowLoggerFactory_WrongType(t *testing.T) {
	logger, err := TemporalWorkflowLoggerFactory("not-a-temporal-context")
	assert.Nil(t, logger)
	assert.Error(t, err)
}

func TestNewTemporalWorkflowLoggerForTest(t *testing.T) {
	logger := NewTemporalWorkflowLoggerForTest(log.NewLogger(), nil)
	assert.NotNil(t, logger)

	withFields := logger.WithFields(orchestrator.Fields{"a": "b"})
	assert.NotNil(t, withFields)
}

func TestTemporalWorkflowLogger_WithErrorAttachesError(t *testing.T) {
	logger := NewTemporalWorkflowLoggerForTest(log.NewLogger(), nil).
		WithError(errors.New("disk full")).(*TemporalWorkflowLogger)
	assert.Equal(t, "disk full", logger.fields[orchestrator.ErrorField])
}

func TestTemporalWorkflowLogger_WithErrorNilIsNoOp(t *testing.T) {
	logger := NewTemporalWorkflowLoggerForTest(log.NewLogger(), nil).
		WithError(nil).(*TemporalWorkflowLogger)
	_, ok := logger.fields[orchestrator.ErrorField]
	assert.False(t, ok)
}

func TestTemporalWorkflowLogger_WithTimeAttachesTime(t *testing.T) {
	ts := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	logger := NewTemporalWorkflowLoggerForTest(log.NewLogger(), nil).
		WithTime(ts).(*TemporalWorkflowLogger)
	assert.Equal(t, ts, logger.fields[orchestrator.TimeField])
}
