package temporalbackend

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/workflow"
)

// WithWorkflowID returns a copy of orchCtx with the given workflow ID set on the
// underlying Temporal context (affects child workflow ID generation).
func WithWorkflowID(orchCtx *orchestrator.Context, workflowID string) *orchestrator.Context {
	newCtx := orchCtx.ShallowCopy()
	newCtx.SetBackendCtx(workflow.WithWorkflowID(WorkflowCtx(orchCtx), workflowID))
	return newCtx
}
