package temporalbackend

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func TestTemporalLogger_BasicMethods(t *testing.T) {
	tLogger := NewTemporalLoggerForTest(log.NewLogger(), orchestrator.Fields{})
	assert.NotNil(t, tLogger)
	tLogger.Info("hello", "k", "v")
}

func TestTemporalLogger_FieldManagement(t *testing.T) {
	tLogger := NewTemporalLoggerForTest(log.NewLogger(), orchestrator.Fields{"a": "b"})
	with := tLogger.WithFields(orchestrator.Fields{"c": "d"})
	assert.NotNil(t, with)
}
