package temporalbackend

import (
	"time"

	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/workflow"
)

// temporalBackend implements orchestrator.Backend by delegating to the
// Temporal SDK. The opaque backendCtx stored in orchestrator.Context is
// always a workflow.Context when this backend is active.
type temporalBackend struct{}

var _ orchestrator.Backend = (*temporalBackend)(nil)

func wfCtx(ctx *orchestrator.Context) workflow.Context {
	wf, ok := ctx.BackendCtx().(workflow.Context)
	if !ok {
		panic("temporalbackend: orchestrator.Context is not backed by a Temporal workflow.Context")
	}
	return wf
}

// NewTemporalContext wraps a Temporal workflow.Context into an
// orchestrator.Context backed by the Temporal execution engine.
func NewTemporalContext(wfCtx workflow.Context) *orchestrator.Context {
	return orchestrator.NewContextWithBackend(&temporalBackend{}, wfCtx)
}

func (b *temporalBackend) ExecuteActivity(ctx *orchestrator.Context, activityFn interface{}, args ...interface{}) orchestrator.Future {
	tArgs := make([]interface{}, len(args))
	copy(tArgs, args)
	return &temporalFuture{
		inner: workflow.ExecuteActivity(wfCtx(ctx), activityFn, tArgs...),
	}
}

func (b *temporalBackend) ExecuteChildWorkflow(ctx *orchestrator.Context, workflowFn interface{}, args ...interface{}) orchestrator.Future {
	tArgs := make([]interface{}, len(args))
	copy(tArgs, args)
	name, ok := workflowFn.(string)
	if !ok {
		name = OrchWorkflowName(workflowFn)
	}
	return &temporalFuture{
		inner: workflow.ExecuteChildWorkflow(wfCtx(ctx), name, tArgs...),
	}
}

func (b *temporalBackend) WithValue(ctx *orchestrator.Context, key, val any) *orchestrator.Context {
	newCtx := ctx.ShallowCopy()
	newCtx.SetBackendCtx(workflow.WithValue(wfCtx(ctx), key, val))
	return newCtx
}

func (b *temporalBackend) Now(ctx *orchestrator.Context) time.Time {
	return workflow.Now(wfCtx(ctx))
}

func (b *temporalBackend) GetInfo(ctx *orchestrator.Context) orchestrator.WorkflowInfo {
	info := workflow.GetInfo(wfCtx(ctx))
	return orchestrator.WorkflowInfo{
		WorkflowExecutionTimeout: info.WorkflowExecutionTimeout,
		WorkflowStartTime:        info.WorkflowStartTime,
		WorkflowExecution: orchestrator.WorkflowExecution{
			ID:    info.WorkflowExecution.ID,
			RunID: info.WorkflowExecution.RunID,
		},
		Attempt: info.Attempt,
	}
}

func (b *temporalBackend) Sleep(ctx *orchestrator.Context, d time.Duration) error {
	return workflow.Sleep(wfCtx(ctx), d)
}

func (b *temporalBackend) WithActivityOptions(ctx *orchestrator.Context, opts orchestrator.ActivityOptions) *orchestrator.Context {
	newCtx := ctx.ShallowCopy()
	newCtx.SetBackendCtx(workflow.WithActivityOptions(wfCtx(ctx), toTemporalActivityOptions(opts)))
	newCtx.SetActivityOpts(&opts)
	return newCtx
}

func (b *temporalBackend) WithChildWorkflowOptions(ctx *orchestrator.Context, opts orchestrator.ChildWorkflowOptions) *orchestrator.Context {
	newCtx := ctx.ShallowCopy()
	newCtx.SetBackendCtx(workflow.WithChildOptions(wfCtx(ctx), toTemporalChildOptions(opts)))
	newCtx.SetChildOpts(&opts)
	return newCtx
}

func (b *temporalBackend) Value(ctx *orchestrator.Context, key any) any {
	return wfCtx(ctx).Value(key)
}

func (b *temporalBackend) Done(ctx *orchestrator.Context) <-chan struct{} {
	// Temporal's cancellation signal is workflow.Context.Done() (a workflow.Channel).
	// It cannot be safely bridged to a native Go channel from workflow code without
	// breaking determinism. Returning nil avoids accidental non-deterministic use;
	// callers should use Temporal primitives directly when running under this backend.
	return nil
}

func (b *temporalBackend) Err(ctx *orchestrator.Context) error {
	return wfCtx(ctx).Err()
}

func (b *temporalBackend) NewDisconnectedContext(orchCtx *orchestrator.Context) *orchestrator.Context {
	disconnectedWfCtx, _ := workflow.NewDisconnectedContext(wfCtx(orchCtx))
	newCtx := orchCtx.ShallowCopy()
	newCtx.SetBackendCtx(disconnectedWfCtx)
	return newCtx
}
