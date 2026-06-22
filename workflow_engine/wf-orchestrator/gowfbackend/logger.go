package gowfbackend

import (
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func init() {
	orchestrator.ChainWorkflowLoggerFactory(GowfWorkflowLoggerFactory)
}
