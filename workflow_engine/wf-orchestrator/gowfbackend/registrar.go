package gowfbackend

import (
	"github.com/cschleiden/go-workflows/registry"
	"github.com/cschleiden/go-workflows/worker"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// GowfRegistrar implements orchestrator.WorkflowRegistrar by delegating to a
// cschleiden worker.Worker. The registrar wraps every workflow and activity
// to bridge orchestrator's APIs to cschleiden's typed runtime:
//
//   - Workflows: first parameter is swapped from *orchestrator.Context to
//     cschleidenwf.Context and the return value is JSON-encoded into a
//     json.RawMessage so the backend's typed ExecuteActivity/CreateSubWorkflow
//     calls do not need to be specialised per workflow.
//   - Activities: identical RawMessage normalisation; orchestrator's
//     non-retryable error markers are translated into permanent errors.
//
// Heartbeat: cschleiden has no analog of Temporal's RecordHeartbeat, so the
// orchestrator-level RecordHeartbeat stays a no-op when this backend is
// active. Workflows that rely on heartbeat-driven liveness detection are
// out of scope for this backend (documented at package level).
type GowfRegistrar struct {
	Worker *worker.Worker
}

var _ orchestrator.WorkflowRegistrar = (*GowfRegistrar)(nil)

// NewRegistrar creates a GowfRegistrar bound to the given worker. Unlike
// temporalbackend.NewRegistrar this does NOT install a heartbeat hook —
// cschleiden has no per-activity heartbeat primitive to forward to.
func NewRegistrar(w *worker.Worker) *GowfRegistrar {
	return &GowfRegistrar{Worker: w}
}

// RegisterWorkflow wraps and registers a workflow function with an
// auto-derived name (the Go function's fully-qualified name). Panics on
// invalid signatures so misuse is loud.
func (r *GowfRegistrar) RegisterWorkflow(fn interface{}) {
	WrapAndRegister(r.Worker, fn)
}

// RegisterWorkflowWithName wraps and registers a workflow function under an
// explicit name.
func (r *GowfRegistrar) RegisterWorkflowWithName(name string, fn interface{}) {
	WrapAndRegisterWithName(r.Worker, name, fn)
}

// RegisterActivity wraps and registers an activity function under its
// auto-derived name. The wrapper:
//   - normalises the return signature to (json.RawMessage, error) so the
//     backend's typed ExecuteActivity[json.RawMessage] dispatch works
//     uniformly across activities returning arbitrary concrete types
//   - translates orchestrator's NonRetryable markers into cschleiden's
//     permanent-error sentinel
func (r *GowfRegistrar) RegisterActivity(fn interface{}) {
	wrapped := WrapActivity(fn)
	name := FuncName(fn)
	if err := r.Worker.RegisterActivity(wrapped, registry.WithName(name)); err != nil {
		panic("gowfbackend: register activity " + name + ": " + err.Error())
	}
	// See wrapWorkflow's short-name alias rationale in register.go: paired
	// with backend.go's dispatchName so dispatch-by-fn-value resolves
	// uniformly across interface-receiver and concrete-receiver method
	// values. Skipped when the alias equals `name`; on a genuine collision
	// claimShortAlias keeps the first owner and logs a warning.
	if short := ShortFuncName(fn); short != "" && short != name {
		if claimShortAlias(r.Worker, "activity", short, FuncName(fn)) {
			if err := r.Worker.RegisterActivity(wrapped, registry.WithName(short)); err != nil {
				panic("gowfbackend: register activity short alias " + short + ": " + err.Error())
			}
		}
	}
}
