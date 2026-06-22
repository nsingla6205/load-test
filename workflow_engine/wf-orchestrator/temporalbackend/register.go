package temporalbackend

import (
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

var orchestratorCtxType = reflect.TypeOf((*orchestrator.Context)(nil))
var workflowCtxType = reflect.TypeOf((*workflow.Context)(nil)).Elem()

// workflowAliasByPtr maps function pointer (as uintptr) → explicit registered
// name passed to RegisterWorkflowWithName. OrchWorkflowName consults this map
// so that orchestrator.ExecuteChildWorkflow(fn, ...) resolves fn to its
// registered alias (e.g. "vlm.CreateVSAClusterWorkflow") rather than the
// Go-derived short name.
var (
	workflowAliasByPtr   sync.Map // map[uintptr]string
	workflowAliasByFuncN sync.Map // map[string]string (full runtime func name → alias)
)

// RememberAlias records that fn was registered under name. Subsequent
// OrchWorkflowName(fn) calls return name instead of the auto-derived short
// name. The mapping is stored both by pointer (handles plain functions and
// method values that share storage) and by runtime function name (handles
// method values where the pointer may differ between captures of the same
// method).
//
// Exported so the separate temporalbackend/testenv package (test helpers) can
// record aliases without re-implementing the alias maps, which remain private
// to this package.
func RememberAlias(fn interface{}, name string) {
	if fn == nil {
		return
	}
	val := reflect.ValueOf(fn)
	if val.Kind() != reflect.Func {
		return
	}
	ptr := val.Pointer()
	if ptr != 0 {
		workflowAliasByPtr.Store(ptr, name)
	}
	if f := runtime.FuncForPC(ptr); f != nil {
		if fnName := f.Name(); fnName != "" {
			workflowAliasByFuncN.Store(fnName, name)
		}
	}
}

// OrchWorkflowName returns the Temporal workflow type name for an orchestrator
// workflow function (e.g. "DeleteIPWorkflow"), matching SDK getFunctionName.
// If fn is already a workflow type name string (e.g. from RegisterWorkflowWithName),
// it is returned unchanged. If fn was registered via RegisterWorkflowWithName,
// the explicit alias is returned.
func OrchWorkflowName(fn interface{}) string {
	if name, ok := fn.(string); ok {
		return name
	}
	val := reflect.ValueOf(fn)
	if val.Kind() == reflect.Func {
		ptr := val.Pointer()
		if v, ok := workflowAliasByPtr.Load(ptr); ok {
			return v.(string)
		}
		fnName := ""
		if f := runtime.FuncForPC(ptr); f != nil {
			fnName = f.Name()
		}
		if v, ok := workflowAliasByFuncN.Load(fnName); ok {
			return v.(string)
		}
		elements := strings.Split(fnName, ".")
		return strings.TrimSuffix(elements[len(elements)-1], "-fm")
	}
	return ""
}

// WrapAndRegister wraps a workflow function that takes *orchestrator.Context
// as its first parameter into one that takes workflow.Context, then registers
// it with the Temporal worker under the original function name.
//
// This handles arbitrary parameter counts and return types via reflection,
// so callers don't need signature-specific generic wrappers.
func WrapAndRegister(w worker.Worker, fn interface{}) {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		panic("WrapAndRegister: argument must be a function")
	}
	if fnType.NumIn() < 1 || fnType.In(0) != orchestratorCtxType {
		panic("WrapAndRegister: first parameter must be *orchestrator.Context")
	}

	name := OrchWorkflowName(fn)

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

	w.RegisterWorkflowWithOptions(wrapped.Interface(), workflow.RegisterOptions{Name: name})
}
