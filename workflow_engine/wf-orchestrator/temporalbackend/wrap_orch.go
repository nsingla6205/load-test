package temporalbackend

import (
	"reflect"

	"go.temporal.io/sdk/workflow"
)

// WrapOrchWorkflow wraps a workflow function whose first parameter is
// *orchestrator.Context into one whose first parameter is workflow.Context, so
// it can be registered with the Temporal SDK (worker or test environment).
//
// It lives in the production package (rather than the testenv helper package)
// because it pulls in no test-only dependencies — only reflect and the Temporal
// workflow package — and is shared by both the test helpers and the package's
// own tests.
func WrapOrchWorkflow(fn interface{}) interface{} {
	return wrapOrchWorkflowReflect(fn)
}

func wrapOrchWorkflowReflect(fn interface{}) interface{} {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		panic("WrapOrchWorkflow: argument must be a function")
	}
	if fnType.NumIn() < 1 || fnType.In(0) != orchestratorCtxType {
		panic("WrapOrchWorkflow: first parameter must be *orchestrator.Context")
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

	return wrapped.Interface()
}
