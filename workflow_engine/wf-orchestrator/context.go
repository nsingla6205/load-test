package orchestrator

import "time"

// Context wraps an engine-specific execution context (Temporal workflow.Context,
// go-workflows workflow context, etc.) behind orchestrator.Backend.
type Context struct {
	backend              Backend
	backendCtx           any
	info                 *WorkflowInfo
	actOpts              *ActivityOptions
	childOpts            *ChildWorkflowOptions
	values               map[any]any
	operationBudgetCheck func() bool
}

// NewContextWithBackend creates a Context backed by a custom backend.
// The backendCtx is opaque and only interpreted by the backend itself.
func NewContextWithBackend(b Backend, backendCtx any) *Context {
	return &Context{
		backend:    b,
		backendCtx: backendCtx,
		values:     make(map[any]any),
	}
}

// BackendCtx returns the opaque backend context (used by backend implementations).
func (ctx *Context) BackendCtx() any {
	return ctx.backendCtx
}

// Backend returns the execution backend associated with this context.
func (ctx *Context) Backend() Backend {
	return ctx.backend
}

// Value retrieves a value from the context, delegating to the backend.
func (ctx *Context) Value(key any) any {
	return ctx.backend.Value(ctx, key)
}

// Done returns a channel that is closed when the context is cancelled.
func (ctx *Context) Done() <-chan struct{} {
	return ctx.backend.Done(ctx)
}

// Err returns the error from the context, if cancelled.
func (ctx *Context) Err() error {
	return ctx.backend.Err(ctx)
}

// WithValue returns a shallow copy of ctx with the key-value pair added.
func WithValue(ctx *Context, key, val any) *Context {
	return ctx.backend.WithValue(ctx, key, val)
}

// WithActivityOptions returns a shallow copy of ctx with activity options attached.
func (ctx *Context) WithActivityOptions(opts ActivityOptions) *Context {
	return ctx.backend.WithActivityOptions(ctx, opts)
}

// WithChildWorkflowOptions returns a shallow copy of ctx with child workflow options attached.
func (ctx *Context) WithChildWorkflowOptions(opts ChildWorkflowOptions) *Context {
	return ctx.backend.WithChildWorkflowOptions(ctx, opts)
}

// Now returns the current time, using the backend's clock.
func Now(ctx *Context) time.Time {
	return ctx.backend.Now(ctx)
}

// GetInfo returns workflow execution metadata.
func GetInfo(ctx *Context) WorkflowInfo {
	return ctx.backend.GetInfo(ctx)
}

// Sleep pauses execution for the given duration.
func Sleep(ctx *Context, d time.Duration) error {
	return ctx.backend.Sleep(ctx, d)
}

// ShallowCopy returns a shallow copy of the context, preserving the backend,
// backendCtx, and all other fields. Used by backend implementations.
func (ctx *Context) ShallowCopy() *Context {
	newCtx := *ctx
	return &newCtx
}

// SetBackendCtx updates the opaque backend context. Used by backend implementations.
func (ctx *Context) SetBackendCtx(backendCtx any) {
	ctx.backendCtx = backendCtx
}

// SetActivityOpts updates the activity options on the context.
func (ctx *Context) SetActivityOpts(opts *ActivityOptions) {
	ctx.actOpts = opts
}

// SetChildOpts updates the child workflow options on the context.
func (ctx *Context) SetChildOpts(opts *ChildWorkflowOptions) {
	ctx.childOpts = opts
}

// ActivityOpts returns the activity options attached to this context.
func (ctx *Context) ActivityOpts() *ActivityOptions {
	return ctx.actOpts
}

// ChildOpts returns the child workflow options attached to this context.
func (ctx *Context) ChildOpts() *ChildWorkflowOptions {
	return ctx.childOpts
}

// Values returns the local values map.
func (ctx *Context) Values() map[any]any {
	return ctx.values
}

// SetValues sets the local values map.
func (ctx *Context) SetValues(m map[any]any) {
	ctx.values = m
}

// Info returns the workflow info, or nil if not set.
func (ctx *Context) Info() *WorkflowInfo {
	return ctx.info
}

// WithOperationBudgetCheck returns a shallow copy of ctx with the operation-budget predicate set.
// RunActivity checks this predicate after every successful activity dispatch.
func (ctx *Context) WithOperationBudgetCheck(fn func() bool) *Context {
	newCtx := ctx.ShallowCopy()
	newCtx.operationBudgetCheck = fn
	return newCtx
}

// ClearOperationBudgetCheck returns a shallow copy of ctx with no operation-budget gate.
func (ctx *Context) ClearOperationBudgetCheck() *Context {
	newCtx := ctx.ShallowCopy()
	newCtx.operationBudgetCheck = nil
	return newCtx
}

// OperationBudgetExceeded returns true if an operation-budget check is set on ctx and it fires.
func (ctx *Context) OperationBudgetExceeded() bool {
	return ctx.operationBudgetCheck != nil && ctx.operationBudgetCheck()
}

// NewDisconnectedContext returns a copy of ctx whose cancellation is detached
// from the parent workflow context.
func NewDisconnectedContext(ctx *Context) *Context {
	return ctx.backend.NewDisconnectedContext(ctx)
}

// WorkflowStartTime returns the time at which this workflow execution started.
func (ctx *Context) WorkflowStartTime() time.Time {
	return GetInfo(ctx).WorkflowStartTime
}
