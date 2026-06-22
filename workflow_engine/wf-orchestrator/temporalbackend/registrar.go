package temporalbackend

import (
	"reflect"
	"runtime"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// TemporalRegistrar implements orchestrator.WorkflowRegistrar by delegating to
// a Temporal worker.Worker.
type TemporalRegistrar struct {
	Worker worker.Worker
}

var _ orchestrator.WorkflowRegistrar = (*TemporalRegistrar)(nil)

// RegisterWorkflow wraps a workflow function (first param *orchestrator.Context)
// and registers it with the Temporal worker using the auto-derived function name.
func (r *TemporalRegistrar) RegisterWorkflow(fn interface{}) {
	WrapAndRegister(r.Worker, fn)
}

// RegisterWorkflowWithName wraps a workflow function and registers it with an
// explicit name instead of the Go function name.
func (r *TemporalRegistrar) RegisterWorkflowWithName(name string, fn interface{}) {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		panic("RegisterWorkflowWithName: argument must be a function")
	}
	if fnType.NumIn() < 1 || fnType.In(0) != orchestratorCtxType {
		panic("RegisterWorkflowWithName: first parameter must be *orchestrator.Context")
	}

	inTypes := make([]reflect.Type, fnType.NumIn())
	inTypes[0] = workflowCtxType
	for i := 1; i < fnType.NumIn(); i++ {
		inTypes[i] = fnType.In(i)
	}
	outTypes := make([]reflect.Type, fnType.NumOut())
	for i := 0; i < fnType.NumOut(); i++ {
		outTypes[i] = fnType.Out(i)
	}

	newFnType := reflect.FuncOf(inTypes, outTypes, fnType.IsVariadic())
	wrapped := reflect.MakeFunc(newFnType, func(args []reflect.Value) []reflect.Value {
		wfCtx := args[0].Interface().(workflow.Context)
		orchCtx := NewTemporalContext(wfCtx)
		newArgs := make([]reflect.Value, len(args))
		newArgs[0] = reflect.ValueOf(orchCtx)
		copy(newArgs[1:], args[1:])
		return fnVal.Call(newArgs)
	})

	r.Worker.RegisterWorkflowWithOptions(wrapped.Interface(), workflow.RegisterOptions{Name: name})
	// Record the fn → alias mapping so ExecuteChildWorkflow(fn) can resolve
	// the explicit registered name.
	RememberAlias(fn, name)
}

// RegisterActivity registers an activity function directly with the worker.
func (r *TemporalRegistrar) RegisterActivity(fn interface{}) {
	r.Worker.RegisterActivity(fn)
}

// SetupHeartbeat configures the orchestrator heartbeat function to delegate
// to the Temporal SDK's activity.RecordHeartbeat.
func SetupHeartbeat() {
	orchestrator.SetHeartbeatFunc(activity.RecordHeartbeat)
}

// NewRegistrar creates a TemporalRegistrar for the given worker and sets up
// backend hooks (heartbeat, etc.).
func NewRegistrar(w worker.Worker) *TemporalRegistrar {
	SetupHeartbeat()
	return &TemporalRegistrar{Worker: w}
}

// FuncName returns the fully-qualified name of a function value, matching the
// name Temporal would use for auto-registration. Unlike gowfbackend.FuncName it
// does not strip the method-value "-fm" suffix (OrchWorkflowName handles that
// for dispatch). Returns the empty string for nil/non-function values or when
// the runtime cannot resolve the function, mirroring the gowf nil-guarding.
func FuncName(fn interface{}) string {
	v := reflect.ValueOf(fn)
	if v.Kind() != reflect.Func {
		return ""
	}
	pc := v.Pointer()
	if pc == 0 {
		return ""
	}
	f := runtime.FuncForPC(pc)
	if f == nil {
		return ""
	}
	return f.Name()
}
