package temporalbackend

import (
	"errors"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/workflow"
)

// WorkflowCtx returns the underlying Temporal workflow.Context for orchCtx.
// Panics if orchCtx is not backed by the Temporal engine.
func WorkflowCtx(orchCtx *orchestrator.Context) workflow.Context {
	return orchCtx.BackendCtx().(workflow.Context)
}

// ContinueAsNew requests workflow continuation with new arguments. It is kept
// for call-site compatibility but no longer dispatches between engines itself:
// the engine-specific behaviour is selected by orchestrator.Backend, so this
// package no longer imports gowfbackend (a Temporal-only binary no longer links
// go-workflows + sqlite).
func ContinueAsNew(orchCtx *orchestrator.Context, wf interface{}, args ...interface{}) error {
	if orchCtx == nil {
		return errors.New("temporalbackend.ContinueAsNew: orchCtx is nil")
	}
	return orchestrator.ContinueAsNew(orchCtx, wf, args...)
}

// ContinueAsNew implements orchestrator.Backend for the Temporal engine using
// Temporal's NewContinueAsNewError.
//
// When wf is a workflow function registered via WrapAndRegister, the Temporal
// workflow type name is used so argument validation matches the wrapped signature.
func (b *temporalBackend) ContinueAsNew(orchCtx *orchestrator.Context, wf interface{}, args ...interface{}) error {
	name, ok := wf.(string)
	if !ok {
		name = OrchWorkflowName(wf)
	}
	return workflow.NewContinueAsNewError(WorkflowCtx(orchCtx), name, args...)
}
