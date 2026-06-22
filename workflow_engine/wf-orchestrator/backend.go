package orchestrator

import "time"

// Backend abstracts the execution engine. The orchestrator delegates all
// execution primitives through this interface so the core package has zero
// Temporal SDK imports.
type Backend interface {
	ExecuteActivity(ctx *Context, activityFn interface{}, args ...interface{}) Future
	ExecuteChildWorkflow(ctx *Context, workflowFn interface{}, args ...interface{}) Future
	WithValue(ctx *Context, key, val any) *Context
	Now(ctx *Context) time.Time
	GetInfo(ctx *Context) WorkflowInfo
	Sleep(ctx *Context, d time.Duration) error
	WithActivityOptions(ctx *Context, opts ActivityOptions) *Context
	WithChildWorkflowOptions(ctx *Context, opts ChildWorkflowOptions) *Context
	Value(ctx *Context, key any) any
	Done(ctx *Context) <-chan struct{}
	Err(ctx *Context) error
	ContinueAsNew(ctx *Context, wf interface{}, args ...interface{}) error
}
