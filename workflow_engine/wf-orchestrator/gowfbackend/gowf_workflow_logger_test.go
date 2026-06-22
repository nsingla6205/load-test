package gowfbackend

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func TestGowfWorkflowLoggerFactory_WrongType(t *testing.T) {
	logger, err := GowfWorkflowLoggerFactory("not-a-gowf-context")
	assert.Nil(t, logger)
	assert.Error(t, err)
}

func newTestGowfLogger() *GowfWorkflowLogger {
	return &GowfWorkflowLogger{logger: log.NewLogger(), fields: orchestrator.Fields{}}
}

func TestGowfWorkflowLogger_WithErrorAttachesError(t *testing.T) {
	logger := newTestGowfLogger().WithError(errors.New("disk full")).(*GowfWorkflowLogger)
	assert.Equal(t, "disk full", logger.fields[orchestrator.ErrorField])
}

func TestGowfWorkflowLogger_WithErrorNilIsNoOp(t *testing.T) {
	logger := newTestGowfLogger().WithError(nil).(*GowfWorkflowLogger)
	_, ok := logger.fields[orchestrator.ErrorField]
	assert.False(t, ok)
}

func TestGowfWorkflowLogger_WithTimeAttachesTime(t *testing.T) {
	ts := time.Date(2026, 6, 19, 10, 0, 0, 0, time.UTC)
	logger := newTestGowfLogger().WithTime(ts).(*GowfWorkflowLogger)
	assert.Equal(t, ts, logger.fields[orchestrator.TimeField])
}
