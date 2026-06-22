package gowfbackend

import (
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/cschleiden/go-workflows/registry"
	"github.com/cschleiden/go-workflows/worker"
	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// orchestratorCtxType matches the temporalbackend equivalent so workflow
// signatures written for either backend are interchangeable.
var orchestratorCtxType = reflect.TypeOf((*orchestrator.Context)(nil))

// gowfContextType holds reflect.Type for cschleidenwf.Context (sync.Context).
// Used as the swapped-in first parameter type when wrapping workflows.
var gowfContextType = reflect.TypeOf((*gowf.Context)(nil)).Elem()

// WrapAndRegister wraps a workflow function whose first parameter is
// *orchestrator.Context into one whose first parameter is cschleidenwf.Context,
// then registers it with the cschleiden worker under the original Go function
// name. The registered wrapper returns (json.RawMessage, error) regardless of
// the workflow's declared return type, mirroring the activity-side strategy.
//
// This handles arbitrary parameter counts and return types via reflection,
// so callers don't need signature-specific generic wrappers.
//
// Panics on misuse so that registration errors surface at process start
// rather than at first invocation.
func WrapAndRegister(w *worker.Worker, fn interface{}) {
	wrapWorkflow(w, fn, FuncName(fn))
}

// WrapAndRegisterWithName is the explicit-name analogue of WrapAndRegister.
// Used by GowfRegistrar.RegisterWorkflowWithName when the workflow ID
// differs from the Go function symbol.
func WrapAndRegisterWithName(w *worker.Worker, name string, fn interface{}) {
	wrapWorkflow(w, fn, name)
}

// BuildWorkflowWrapper exposes the reflective workflow wrapper that
// WrapAndRegister uses internally. The returned interface{} value is a
// function whose first parameter is cschleidenwf.Context and whose return
// signature has been normalised to (json.RawMessage, error).
//
// Useful when callers need to register the wrapped workflow on a different
// surface than the cschleiden *worker.Worker — for example, on a
// worker.WorkflowOrchestrator that doesn't expose its embedded worker. The
// returned wrapper is suitable for passing to any registration call that
// accepts a workflow.Workflow (cschleiden's interface{} alias).
//
// Panics on misuse so registration errors surface at process start, in line
// with the rest of this package.
func BuildWorkflowWrapper(fn interface{}) interface{} {
	return buildWorkflowWrapper(fn)
}

func wrapWorkflow(w *worker.Worker, fn interface{}, name string) {
	wrapped := buildWorkflowWrapper(fn)
	if err := w.RegisterWorkflow(wrapped, registry.WithName(name)); err != nil {
		panic("gowfbackend: register workflow " + name + ": " + err.Error())
	}
	// Also register a short-name alias keyed by the trailing identifier of
	// the function's runtime FuncName. This pairs with backend.go's
	// dispatchName, which resolves dispatch references to the same short
	// identifier so that callers can pass either a concrete-receiver or
	// interface-receiver method value at the call site (their full
	// FuncNames differ — concrete carries "(*ConcreteType)" and interface
	// carries the interface type — but the trailing identifier is shared).
	// The alias is skipped when it equals `name`. claimShortAlias fails fast
	// if a *different* function already owns this short name on the worker,
	// instead of silently dropping the alias (which would make dispatch by
	// function value resolve to the wrong workflow).
	if short := ShortFuncName(fn); short != "" && short != name {
		if claimShortAlias(w, "workflow", short, FuncName(fn)) {
			if err := w.RegisterWorkflow(wrapped, registry.WithName(short)); err != nil {
				panic("gowfbackend: register workflow short alias " + short + ": " + err.Error())
			}
		}
	}
}

// shortAliasKey identifies a short-name alias registration on a specific worker.
// Collision detection is scoped per worker because dispatch resolves names
// within a worker's own registry; the same short name on two different workers
// (e.g. one cloud-specific manager per worker) is legitimate and independent.
type shortAliasKey struct {
	w     *worker.Worker
	kind  string // "workflow" or "activity"
	short string
}

// shortAliasOwners records which fully-qualified function currently owns a given
// short-name alias on a worker, so a second *distinct* function claiming the
// same short name fails fast rather than being silently dropped by the registry
// (which keeps the first registration).
var shortAliasOwners sync.Map // map[shortAliasKey]string (owner FuncName)

// claimShortAlias reports whether the short alias should be registered now.
// It returns false when the same function already owns the alias on this worker
// (idempotent — e.g. a function registered under two explicit names). It panics
// on a genuine collision: a different function already owns this short name on
// this worker, which would make dispatch-by-function-value silently ambiguous.
func claimShortAlias(w *worker.Worker, kind, short, ownerFuncName string) bool {
	key := shortAliasKey{w: w, kind: kind, short: short}
	prev, loaded := shortAliasOwners.LoadOrStore(key, ownerFuncName)
	if !loaded {
		return true
	}
	if prev.(string) == ownerFuncName {
		return false
	}
	panic(fmt.Sprintf(
		"gowfbackend: %s short-name alias %q is claimed by both %q and %q on the same worker; "+
			"dispatch by function value would be ambiguous — register one with an explicit, unique name",
		kind, short, prev.(string), ownerFuncName))
}

// buildWorkflowWrapper takes a function whose first parameter is
// *orchestrator.Context and returns a function with the same parameter list
// (modulo first-param swap) and a normalised (json.RawMessage, error) return
// signature.
func buildWorkflowWrapper(fn interface{}) interface{} {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()

	if fnType.Kind() != reflect.Func {
		panic("gowfbackend.WrapAndRegister: argument must be a function")
	}
	if fnType.NumIn() < 1 || fnType.In(0) != orchestratorCtxType {
		panic("gowfbackend.WrapAndRegister: first parameter must be *orchestrator.Context")
	}
	if fnType.NumOut() == 0 || !fnType.Out(fnType.NumOut()-1).Implements(errorType) {
		panic("gowfbackend.WrapAndRegister: function must return error as last return value")
	}
	if fnType.NumOut() > 2 {
		panic("gowfbackend.WrapAndRegister: function must return at most (result, error)")
	}

	inTypes := make([]reflect.Type, fnType.NumIn())
	inTypes[0] = gowfContextType
	for i := 1; i < fnType.NumIn(); i++ {
		inTypes[i] = fnType.In(i)
	}
	outTypes := []reflect.Type{rawMessageType, errorType}

	newFnType := reflect.FuncOf(inTypes, outTypes, fnType.IsVariadic())
	wrapped := reflect.MakeFunc(newFnType, func(args []reflect.Value) []reflect.Value {
		wf := args[0].Interface().(gowf.Context)
		orchCtx := NewGoWorkflowsContext(wf)

		newArgs := make([]reflect.Value, len(args))
		newArgs[0] = reflect.ValueOf(orchCtx)
		copy(newArgs[1:], args[1:])

		var callOut []reflect.Value
		if fnType.IsVariadic() {
			callOut = fnVal.CallSlice(newArgs)
		} else {
			callOut = fnVal.Call(newArgs)
		}

		errIdx := len(callOut) - 1
		errVal, _ := callOut[errIdx].Interface().(error)
		if errVal != nil {
			// ContinueAsNew must reach cschleiden's executor unchanged.
			if isContinueAsNewError(errVal) {
				return []reflect.Value{
					reflect.Zero(rawMessageType),
					reflect.ValueOf(errVal).Convert(errorType),
				}
			}
			return []reflect.Value{
				reflect.Zero(rawMessageType),
				reflect.ValueOf(ToGowfError(errVal)).Convert(errorType),
			}
		}

		var resultIface interface{}
		if len(callOut) == 2 && callOut[0].IsValid() && callOut[0].CanInterface() {
			resultIface = callOut[0].Interface()
		}

		raw, marshalErr := marshalResult(resultIface)
		if marshalErr != nil {
			return []reflect.Value{
				reflect.Zero(rawMessageType),
				reflect.ValueOf(marshalErr).Convert(errorType),
			}
		}
		return []reflect.Value{reflect.ValueOf(raw), reflect.Zero(errorType)}
	})

	return wrapped.Interface()
}

// FuncName returns the fully-qualified name of a function value, matching
// what cschleiden's fn.Name(...) extractor would produce. Used to keep
// workflow-side dispatch (by name) consistent with worker-side registration.
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
	return strings.TrimSuffix(f.Name(), "-fm")
}

// ShortFuncName returns the trailing dot-segment of FuncName(fn): the bare
// identifier without any package or receiver-type prefix. For top-level
// functions and method values alike, the trailing segment is the bare
// method/function name (e.g. "CreateDisksWorkflow"). For method values
// bound to interface-typed and concrete-typed receivers of the same
// underlying method, the full FuncName diverges (one carries the
// interface type, the other the "(*ConcreteType)" prefix) but the
// trailing identifier is identical — which is what makes this name
// stable across both binding sites.
//
// Returns the empty string for nil or non-function values.
func ShortFuncName(fn interface{}) string {
	full := FuncName(fn)
	if full == "" {
		return ""
	}
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

// activityIdentifier resolves an orchestrator activity reference (either a
// function value or a string name) to the canonical name under which the
// activity was registered. Returns the empty string for nil/unknown inputs
// so callers can short-circuit with a meaningful error.
func activityIdentifier(activityFn interface{}) string {
	if activityFn == nil {
		return ""
	}
	if name, ok := activityFn.(string); ok {
		return name
	}
	return FuncName(activityFn)
}
