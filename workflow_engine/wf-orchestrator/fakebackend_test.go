package orchestrator

import (
	"time"
)

// fakeBackend is a configurable orchestrator.Backend used to exercise the
// engine-agnostic logic in context.go, execute.go, and poll.go without pulling
// in a real Temporal or go-workflows engine. Each method records the call and
// returns the corresponding configured value.
type fakeBackend struct {
	// Recorded inputs from the most recent calls.
	execActivityFn    interface{}
	execActivityArg   []interface{}
	execChildFn       interface{}
	execChildArgs     []interface{}
	sleptFor          time.Duration
	valueKey          any
	continueAsNewFn   interface{}
	continueAsNewArgs []interface{}

	// Configurable return values / behaviours.
	activityFuture   Future
	childFuture      Future
	now              time.Time
	info             WorkflowInfo
	sleepErr         error
	value            any
	doneCh           chan struct{}
	err              error
	continueAsNewErr error
}

func newFakeBackend() *fakeBackend {
	return &fakeBackend{
		activityFuture: &readyFuture{},
		childFuture:    &readyFuture{},
		doneCh:         make(chan struct{}),
	}
}

// newFakeContext returns a Context wired to a fresh fakeBackend, plus the
// backend so the test can inspect/adjust it.
func newFakeContext() (*Context, *fakeBackend) {
	b := newFakeBackend()
	return NewContextWithBackend(b, "backend-ctx"), b
}

func (b *fakeBackend) ExecuteActivity(_ *Context, activityFn interface{}, args ...interface{}) Future {
	b.execActivityFn = activityFn
	b.execActivityArg = args
	return b.activityFuture
}

func (b *fakeBackend) ExecuteChildWorkflow(_ *Context, workflowFn interface{}, args ...interface{}) Future {
	b.execChildFn = workflowFn
	b.execChildArgs = args
	return b.childFuture
}

func (b *fakeBackend) WithValue(ctx *Context, key, val any) *Context {
	newCtx := ctx.ShallowCopy()
	merged := make(map[any]any, len(ctx.Values())+1)
	for k, v := range ctx.Values() {
		merged[k] = v
	}
	merged[key] = val
	newCtx.SetValues(merged)
	return newCtx
}

func (b *fakeBackend) Now(_ *Context) time.Time { return b.now }

func (b *fakeBackend) GetInfo(_ *Context) WorkflowInfo { return b.info }

func (b *fakeBackend) Sleep(_ *Context, d time.Duration) error {
	b.sleptFor = d
	return b.sleepErr
}

func (b *fakeBackend) WithActivityOptions(ctx *Context, opts ActivityOptions) *Context {
	newCtx := ctx.ShallowCopy()
	o := opts
	newCtx.SetActivityOpts(&o)
	return newCtx
}

func (b *fakeBackend) WithChildWorkflowOptions(ctx *Context, opts ChildWorkflowOptions) *Context {
	newCtx := ctx.ShallowCopy()
	o := opts
	newCtx.SetChildOpts(&o)
	return newCtx
}

func (b *fakeBackend) Value(ctx *Context, key any) any {
	b.valueKey = key
	if v, ok := ctx.Values()[key]; ok {
		return v
	}
	return b.value
}

func (b *fakeBackend) Done(_ *Context) <-chan struct{} { return b.doneCh }

func (b *fakeBackend) Err(_ *Context) error { return b.err }

func (b *fakeBackend) ContinueAsNew(_ *Context, wf interface{}, args ...interface{}) error {
	b.continueAsNewFn = wf
	b.continueAsNewArgs = args
	return b.continueAsNewErr
}

func (b *fakeBackend) NewDisconnectedContext(ctx *Context) *Context {
	return ctx.ShallowCopy()
}
