package orchestrator

import "errors"

// ContinueAsNew restarts the current workflow execution with new arguments
// using the active backend. It is engine-agnostic: the call is dispatched
// through Backend.ContinueAsNew so neither backend package needs to import the
// other (a Temporal-only binary never links go-workflows and vice versa).
func ContinueAsNew(ctx *Context, wf interface{}, args ...interface{}) error {
	if ctx == nil {
		return errors.New("orchestrator.ContinueAsNew: ctx is nil")
	}
	return ctx.backend.ContinueAsNew(ctx, wf, args...)
}
