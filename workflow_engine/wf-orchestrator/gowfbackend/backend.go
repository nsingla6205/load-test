package gowfbackend

import (
	"encoding/json"
	"errors"
	"time"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// gowfBackend implements orchestrator.Backend by delegating to the
// cschleiden/go-workflows engine. The opaque backendCtx stored in
// orchestrator.Context is always a cschleidenwf.Context (sync.Context) when
// this backend is active.
//
// Behavioural divergences from temporalbackend (intentional and documented):
//   - HeartbeatTimeout / ActivityID / ParentClosePolicy: dropped, no
//     cschleiden equivalent.
//   - ScheduleToCloseTimeout: mapped to cschleiden RetryTimeout for workflow-
//     level retries AND propagated onto the activity Go context deadline via
//     metadata + WrapActivity so in-process long-poll activities are bounded
//     by the deadline (Temporal parity).
//   - GetInfo.WorkflowExecutionTimeout: returns zero unless an explicit
//     ChildWorkflowOptions has been attached to the context.
//   - GetInfo.WorkflowStartTime: cached lazily on first NewGoWorkflowsContext
//     call via a private context value.
//   - RecordHeartbeat: no-op (cschleiden has no heartbeat primitive).
type gowfBackend struct{}

var _ orchestrator.Backend = (*gowfBackend)(nil)

// NewBackend returns the orchestrator.Backend implementation used inside
// go-workflows workflow and activity code.
func NewBackend() orchestrator.Backend {
	return &gowfBackend{}
}

// startTimeKey is unexported so workflow code cannot accidentally read or
// overwrite the cached workflow-start timestamp.
type startTimeKey struct{}

// wfCtx unwraps the orchestrator context's opaque backendCtx into the
// cschleiden workflow context. Panics on type mismatch — that is always a
// programmer error (using the wrong backend with a backend-specific call site).
func wfCtx(ctx *orchestrator.Context) gowf.Context {
	wf, ok := ctx.BackendCtx().(gowf.Context)
	if !ok {
		panic("gowfbackend: backend context is not a go-workflows workflow.Context")
	}
	return wf
}

// NewGoWorkflowsContext wraps a cschleiden workflow.Context into an
// orchestrator.Context backed by the gowfBackend. The workflow start time is
// captured here and cached on the context so subsequent GetInfo calls return
// a stable value for the lifetime of the workflow execution (cschleiden does
// not surface a start timestamp on its WorkflowInstance).
func NewGoWorkflowsContext(wf gowf.Context) *orchestrator.Context {
	octx := orchestrator.NewContextWithBackend(&gowfBackend{}, wf)
	octx.Values()[startTimeKey{}] = gowf.Now(wf)
	return octx
}

func (b *gowfBackend) ExecuteActivity(ctx *orchestrator.Context, activityFn interface{}, args ...interface{}) orchestrator.Future {
	name := dispatchName(activityFn)
	if name == "" {
		return readyFutureErr(errors.New("gowfbackend: cannot resolve activity name (nil or unknown reference)"))
	}

	opts := gowf.DefaultActivityOptions
	if attached := ctx.ActivityOpts(); attached != nil {
		opts = toGowfActivityOptions(*attached)
	}

	gowfArgs := make([]any, len(args))
	copy(gowfArgs, args)

	wf := wfCtx(ctx)
	if attached := ctx.ActivityOpts(); attached != nil && attached.ScheduleToCloseTimeout > 0 {
		wf = stampScheduleToCloseOnWorkflow(wf, attached.ScheduleToCloseTimeout)
	}

	fut := gowf.ExecuteActivity[json.RawMessage](wf, opts, name, gowfArgs...)
	return &gowfFuture{inner: fut}
}

func (b *gowfBackend) ExecuteChildWorkflow(ctx *orchestrator.Context, workflowFn interface{}, args ...interface{}) orchestrator.Future {
	name := dispatchName(workflowFn)
	if name == "" {
		return readyFutureErr(errors.New("gowfbackend: cannot resolve child workflow name (nil or unknown reference)"))
	}

	opts := gowf.DefaultSubWorkflowOptions
	if attached := ctx.ChildOpts(); attached != nil {
		opts = toGowfSubWorkflowOptions(*attached)
	}

	gowfArgs := make([]any, len(args))
	copy(gowfArgs, args)

	fut := gowf.CreateSubWorkflowInstance[json.RawMessage](wfCtx(ctx), opts, name, gowfArgs...)
	return &gowfFuture{inner: fut}
}

func (b *gowfBackend) WithValue(ctx *orchestrator.Context, key, val any) *orchestrator.Context {
	newCtx := ctx.ShallowCopy()
	newCtx.SetBackendCtx(gowf.WithValue(wfCtx(ctx), key, val))
	// Also mirror the value into the orchestrator-level values map so the
	// subsequent fast-path Value() lookup works without re-entering the
	// workflow runtime (matches temporalbackend, where wfCtx.Value also
	// answers, but our fast-path is helpful for unit tests using ad-hoc
	// keys that the runtime doesn't know about).
	newValues := make(map[any]any, len(ctx.Values())+1)
	for k, v := range ctx.Values() {
		newValues[k] = v
	}
	newValues[key] = val
	newCtx.SetValues(newValues)
	return newCtx
}

func (b *gowfBackend) Now(ctx *orchestrator.Context) time.Time {
	return gowf.Now(wfCtx(ctx))
}

func (b *gowfBackend) GetInfo(ctx *orchestrator.Context) orchestrator.WorkflowInfo {
	wf := wfCtx(ctx)
	inst := gowf.WorkflowInstance(wf)
	info := orchestrator.WorkflowInfo{
		WorkflowExecution: orchestrator.WorkflowExecution{
			ID:    inst.InstanceID,
			RunID: inst.ExecutionID,
		},
	}
	if start, ok := ctx.Values()[startTimeKey{}].(time.Time); ok {
		info.WorkflowStartTime = start
	}
	if child := ctx.ChildOpts(); child != nil {
		info.WorkflowExecutionTimeout = child.WorkflowExecutionTimeout
	}
	return info
}

func (b *gowfBackend) Sleep(ctx *orchestrator.Context, d time.Duration) error {
	return gowf.Sleep(wfCtx(ctx), d)
}

func (b *gowfBackend) WithActivityOptions(ctx *orchestrator.Context, opts orchestrator.ActivityOptions) *orchestrator.Context {
	newCtx := ctx.ShallowCopy()
	newCtx.SetActivityOpts(&opts)
	return newCtx
}

func (b *gowfBackend) WithChildWorkflowOptions(ctx *orchestrator.Context, opts orchestrator.ChildWorkflowOptions) *orchestrator.Context {
	newCtx := ctx.ShallowCopy()
	newCtx.SetChildOpts(&opts)
	return newCtx
}

func (b *gowfBackend) Value(ctx *orchestrator.Context, key any) any {
	if v, ok := ctx.Values()[key]; ok {
		return v
	}
	return wfCtx(ctx).Value(key)
}

func (b *gowfBackend) Done(ctx *orchestrator.Context) <-chan struct{} {
	// go-workflows executes all workflow code on a single deterministic
	// goroutine. cschleiden's Done() returns a workflow Channel that may only
	// be received from within that goroutine; bridging it to a native Go
	// channel requires a separate goroutine calling Receive, which breaks
	// determinism. Returning nil (matching temporalBackend.Done) avoids
	// accidental non-deterministic use; callers should use go-workflows
	// primitives directly when running under this backend.
	return nil
}

func (b *gowfBackend) Err(ctx *orchestrator.Context) error {
	return wfCtx(ctx).Err()
}

// dispatchName resolves the lookup key used by ExecuteChildWorkflow and
// ExecuteActivity. For string references it returns the string verbatim.
// For function values it returns the SHORT name (the trailing identifier
// of FuncName) so that dispatch resolves regardless of whether the caller
// passes a concrete-receiver or interface-receiver method value.
//
// Why short rather than full FuncName: lifecycle-manager registers
// per-cloud workflows with concrete receivers (e.g. *GCPDiskManager) but
// dispatches them via the cloud-agnostic interface stored on
// CloudManager (e.g. cm.DiskManager.CreateDisksWorkflow). Go's runtime
// emits different FuncNames for the two — concrete carries
// "(*GCPDiskManager)" in the prefix, interface carries the interface
// type — but the trailing identifier is identical
// ("CreateDisksWorkflow"). The registration helpers in this package
// (wrapWorkflow, GowfRegistrar.RegisterActivity, and external registrar
// implementations such as vlmgowf) add the short-name alias so the
// short-name lookup always resolves.
func dispatchName(fn interface{}) string {
	if fn == nil {
		return ""
	}
	if name, ok := fn.(string); ok {
		return name
	}
	return ShortFuncName(fn)
}
