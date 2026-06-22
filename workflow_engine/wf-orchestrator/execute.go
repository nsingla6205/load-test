package orchestrator

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
)

var contextType = reflect.TypeOf((*context.Context)(nil)).Elem()

// ExecuteActivity dispatches to the appropriate backend.
// The ctx must have ActivityOptions attached (via WithActivityOptions) before calling.
func ExecuteActivity(ctx *Context, activityFn interface{}, args ...interface{}) Future {
	// Fail fast on obviously invalid calls so workflows get a normal error instead
	// of a later reflection panic from the backend's activity invocation path.
	if err := validateActivityInvocation(activityFn, args...); err != nil {
		return &readyFuture{result: futureResult{err: err}}
	}
	return ctx.backend.ExecuteActivity(ctx, activityFn, args...)
}

// ExecuteChildWorkflow dispatches to the appropriate backend.
// The ctx must have ChildWorkflowOptions attached (via WithChildWorkflowOptions) before calling.
func ExecuteChildWorkflow(ctx *Context, workflowFn interface{}, args ...interface{}) Future {
	return ctx.backend.ExecuteChildWorkflow(ctx, workflowFn, args...)
}

func validateActivityInvocation(activityFn interface{}, args ...interface{}) error {
	if activityFn == nil {
		return fmt.Errorf("invalid activity invocation: nil activity")
	}
	if _, ok := activityFn.(string); ok {
		// Named activities are registered and resolved by the backend, so we do not
		// have enough type information here to validate their arguments safely.
		return nil
	}

	fnType := reflect.TypeOf(activityFn)
	if fnType.Kind() != reflect.Func {
		return fmt.Errorf("invalid activity invocation for %T: activity must be a function or registered activity name", activityFn)
	}
	if fnType.NumIn() == 0 || !fnType.In(0).Implements(contextType) {
		return fmt.Errorf("invalid activity invocation for %s: first parameter must implement context.Context", runtimeFuncName(activityFn))
	}

	requiredArgs := fnType.NumIn() - 1
	if !fnType.IsVariadic() {
		if len(args) != requiredArgs {
			return fmt.Errorf("invalid activity invocation for %s: got %d args, want %d", runtimeFuncName(activityFn), len(args), requiredArgs)
		}
	} else if len(args) < requiredArgs-1 {
		return fmt.Errorf("invalid activity invocation for %s: got %d args, want at least %d", runtimeFuncName(activityFn), len(args), requiredArgs-1)
	}

	for i, arg := range args {
		paramType := activityParamType(fnType, i)
		if arg == nil {
			if !isNilAssignableType(paramType) {
				return fmt.Errorf("invalid activity invocation for %s: arg %d is nil but parameter type %s is not nil-assignable", runtimeFuncName(activityFn), i+1, paramType)
			}
			continue
		}

		argType := reflect.TypeOf(arg)
		// AssignableTo intentionally requires the exact concrete type (or a directly
		// assignable one). This catches bugs such as passing a package-local "same
		// shape" request type into an activity expecting a different defined type.
		if !argType.AssignableTo(paramType) {
			return fmt.Errorf("invalid activity invocation for %s: arg %d has type %s, activity expects %s", runtimeFuncName(activityFn), i+1, argType, paramType)
		}
	}

	return nil
}

func activityParamType(fnType reflect.Type, argIdx int) reflect.Type {
	paramIdx := argIdx + 1
	if fnType.IsVariadic() && paramIdx >= fnType.NumIn()-1 {
		// Variadic parameters are represented as a slice type; compare each extra
		// argument against the slice element type the function actually accepts.
		return fnType.In(fnType.NumIn() - 1).Elem()
	}
	return fnType.In(paramIdx)
}

func isNilAssignableType(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Ptr, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return true
	default:
		return false
	}
}

func runtimeFuncName(fn interface{}) string {
	return runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
}
