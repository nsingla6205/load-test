package temporalbackend

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func init() {
	orchestrator.ChainWorkflowLoggerFactory(TemporalWorkflowLoggerFactory)
}

// NewTemporalLoggerForTest builds a UnifiedLogger for tests in this package.
func NewTemporalLoggerForTest(l log.Logger, fields orchestrator.Fields) orchestrator.UnifiedLogger {
	return NewTemporalWorkflowLoggerForTest(l, fields)
}
