package gowfbackend

import (
	"errors"
	"reflect"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// IsGoWorkflowsBackend reports whether b is the cschleiden go-workflows backend.
func IsGoWorkflowsBackend(b orchestrator.Backend) bool {
	_, ok := b.(*gowfBackend)
	return ok
}

// ContinueAsNew restarts the current workflow execution with new arguments.
// cschleiden only supports continuing the in-flight workflow (not jumping to
// a different workflow type by name). lifecycle-manager call sites pass the
// same workflow function they are running when batching via ContinueAsNew.
//
// The wf parameter is accepted for API parity with temporalbackend.ContinueAsNew
// but is not used to select a target workflow under go-workflows.
func ContinueAsNew(orchCtx *orchestrator.Context, _ interface{}, args ...interface{}) error {
	if orchCtx == nil {
		return errors.New("gowfbackend.ContinueAsNew: orchCtx is nil")
	}
	gowfArgs := make([]any, len(args))
	copy(gowfArgs, args)
	return gowf.ContinueAsNew(wfCtx(orchCtx), gowfArgs...)
}

// ContinueAsNew implements orchestrator.Backend for the go-workflows engine by
// delegating to the package-level ContinueAsNew.
func (b *gowfBackend) ContinueAsNew(orchCtx *orchestrator.Context, wf interface{}, args ...interface{}) error {
	return ContinueAsNew(orchCtx, wf, args...)
}

func isContinueAsNewError(err error) bool {
	if err == nil {
		return false
	}
	// cschleiden's continueasnew.Error lives in an internal package; detect by
	// type name instead of importing it.
	rt := reflect.TypeOf(err)
	if rt == nil {
		return false
	}
	return rt.PkgPath() == "github.com/cschleiden/go-workflows/internal/continueasnew" && rt.Name() == "Error"
}
