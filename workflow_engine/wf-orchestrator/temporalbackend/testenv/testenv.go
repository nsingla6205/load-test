// Package testenv holds Temporal test-environment helpers (workflow
// registration on testsuite.TestWorkflowEnvironment and gomonkey-based function
// patching). These utilities pull in test-only dependencies — gomonkey (which
// rewrites function pointers at runtime) and go.temporal.io/sdk/testsuite — so
// they are deliberately kept out of the production temporalbackend package to
// avoid linking that baggage into production binaries.
//
// Test code that previously called temporalbackend.PatchOrchFunc /
// RegisterOrchWorkflow / NewTestEnvRegistrar / WrapOrchWorkflow should import
// this package instead. WrapOrchWorkflow itself stays in temporalbackend (no
// test-only deps) and is re-used here.
package testenv

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/agiledragon/gomonkey/v2"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator/temporalbackend"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// WrapOrchWorkflow wraps a workflow function whose first parameter is
// *orchestrator.Context so it can be passed to testsuite.TestWorkflowEnvironment
// RegisterWorkflow / ExecuteWorkflow. It delegates to temporalbackend, which
// owns the reflection wrapper.
func WrapOrchWorkflow(fn interface{}) interface{} {
	return temporalbackend.WrapOrchWorkflow(fn)
}

// RegisterOrchWorkflow wraps fn and registers it on env under the workflow name
// returned by temporalbackend.OrchWorkflowName(fn). Returns the wrapped function
// for use with ExecuteWorkflow / OnWorkflow. Idempotent: silently swallows
// duplicate registrations so callers that pre-register workflows can still call
// this safely.
func RegisterOrchWorkflow(env *testsuite.TestWorkflowEnvironment, fn interface{}) interface{} {
	wrapped := temporalbackend.WrapOrchWorkflow(fn)
	name := temporalbackend.OrchWorkflowName(fn)
	func() {
		defer func() {
			// Only swallow duplicate-registration panics so this stays
			// idempotent for callers that pre-register workflows. Real
			// registration failures (bad options, SDK changes) must surface.
			if r := recover(); r != nil && !isAlreadyRegisteredPanic(r) {
				panic(r)
			}
		}()
		env.RegisterWorkflowWithOptions(wrapped, workflow.RegisterOptions{Name: name})
	}()
	return wrapped
}

// isAlreadyRegisteredPanic reports whether a recovered panic value indicates
// that a workflow with the same name was already registered.
func isAlreadyRegisteredPanic(r interface{}) bool {
	return strings.Contains(strings.ToLower(fmt.Sprint(r)), "already registered")
}

// TestEnvRegistrar implements orchestrator.WorkflowRegistrar for testsuite environments.
type TestEnvRegistrar struct {
	env *testsuite.TestWorkflowEnvironment
}

var _ orchestrator.WorkflowRegistrar = (*TestEnvRegistrar)(nil)

// NewTestEnvRegistrar returns a registrar that registers wrapped workflows on env.
func NewTestEnvRegistrar(env *testsuite.TestWorkflowEnvironment) *TestEnvRegistrar {
	temporalbackend.SetupHeartbeat()
	return &TestEnvRegistrar{env: env}
}

func (r *TestEnvRegistrar) RegisterWorkflow(fn interface{}) {
	RegisterOrchWorkflow(r.env, fn)
}

func (r *TestEnvRegistrar) RegisterWorkflowWithName(name string, fn interface{}) {
	wrapped := temporalbackend.WrapOrchWorkflow(fn)
	r.env.RegisterWorkflowWithOptions(wrapped, workflow.RegisterOptions{Name: name})
	// Record the mapping from fn pointer → registered name so that
	// orchestrator.ExecuteChildWorkflow(fn, ...) can resolve fn to its
	// registered alias even when that alias differs from the Go-derived short
	// name (e.g. "vlm.CreateVSAClusterWorkflow").
	temporalbackend.RememberAlias(fn, name)
}

func (r *TestEnvRegistrar) RegisterActivity(fn interface{}) {
	r.env.RegisterActivity(fn)
}

// PatchOrchFunc replaces fn with a stub that returns the given values whenever
// the function is invoked (directly via Go call or via
// orchestrator.ExecuteChildWorkflow). It mirrors the semantics of
// testsuite.TestWorkflowEnvironment.OnWorkflow(...).Return(...) but works for
// orchestrator workflow functions that the VLM codebase commonly invokes as
// direct Go calls rather than as Temporal child workflows.
//
// Usage:
//
//	defer testenv.PatchOrchFunc(svm.ModifyVSASVMWorkflow, resp, nil).Reset()
//
// If a single function value is passed as returns, it is used as the
// replacement (its signature must be assignable to fn's signature). Otherwise
// the values are treated as static return values, converted to fn's output
// types, and returned on every invocation.
func PatchOrchFunc(fn interface{}, returns ...interface{}) *gomonkey.Patches {
	fnVal := reflect.ValueOf(fn)
	fnType := fnVal.Type()
	if fnType.Kind() != reflect.Func {
		panic("PatchOrchFunc: fn must be a function")
	}

	// Behavior function override: caller passed a single function whose
	// signature is either directly assignable to fn's signature or differs
	// only in the first parameter type (workflow.Context vs *orchestrator.Context).
	if len(returns) == 1 && returns[0] != nil {
		rv := reflect.ValueOf(returns[0])
		if rv.Kind() == reflect.Func {
			rvType := rv.Type()
			if rvType.AssignableTo(fnType) {
				return gomonkey.ApplyFunc(fn, rv.Interface())
			}
			// Adapter: caller's first param may be workflow.Context (because
			// our earlier mock signature normalisation rewrote them) while
			// fn takes *orchestrator.Context. Build an adapter that calls
			// the user's func with the appropriate first-arg value (the
			// user's callbacks ignore the context via `_`).
			if rvType.NumIn() == fnType.NumIn() &&
				rvType.NumOut() == fnType.NumOut() {
				compatible := true
				for i := 1; i < rvType.NumIn(); i++ {
					if rvType.In(i) != fnType.In(i) {
						compatible = false
						break
					}
				}
				for i := 0; i < rvType.NumOut(); i++ {
					if rvType.Out(i) != fnType.Out(i) {
						compatible = false
						break
					}
				}
				if compatible {
					adapter := reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
						newArgs := make([]reflect.Value, len(args))
						newArgs[0] = reflect.Zero(rvType.In(0))
						copy(newArgs[1:], args[1:])
						return rv.Call(newArgs)
					})
					return gomonkey.ApplyFunc(fn, adapter.Interface())
				}
			}
		}
	}

	replacement := reflect.MakeFunc(fnType, func(args []reflect.Value) []reflect.Value {
		outs := make([]reflect.Value, fnType.NumOut())
		for i := 0; i < fnType.NumOut(); i++ {
			outType := fnType.Out(i)
			if i < len(returns) && returns[i] != nil {
				rv := reflect.ValueOf(returns[i])
				if rv.Type().AssignableTo(outType) {
					outs[i] = rv
				} else if rv.Type().ConvertibleTo(outType) {
					outs[i] = rv.Convert(outType)
				} else {
					outs[i] = reflect.Zero(outType)
				}
			} else {
				outs[i] = reflect.Zero(outType)
			}
		}
		return outs
	}).Interface()
	return gomonkey.ApplyFunc(fn, replacement)
}
