package gowfbackend

import (
	"encoding/json"
	"reflect"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// gowfFuture wraps a cschleiden Future[json.RawMessage] and, on Get,
// JSON-decodes the raw payload into the caller-supplied destination
// pointer. The RawMessage indirection lets a single backend.ExecuteActivity
// path serve activities returning arbitrary concrete types without
// instantiating cschleiden's Future[T] generic per call site.
type gowfFuture struct {
	inner gowf.Future[json.RawMessage]
	// readyErr lets us produce a future that is "already failed" (e.g. when
	// the activity reference cannot be resolved to a name). Get returns this
	// error immediately without consulting inner.
	readyErr error
}

// Get blocks the workflow until the underlying future resolves and copies
// the result into valuePtr.
//
// Semantics match orchestrator.Future.Get:
//   - returns the activity/workflow error verbatim if the call failed
//   - if valuePtr == nil the result is discarded (used for void activities)
//   - if valuePtr != nil and the future succeeded, the JSON payload is
//     unmarshaled into the value pointed to by valuePtr
func (f *gowfFuture) Get(ctx *orchestrator.Context, valuePtr interface{}) error {
	if f.readyErr != nil {
		return f.readyErr
	}

	wf := wfCtx(ctx)
	raw, err := f.inner.Get(wf)
	if err != nil {
		return err
	}
	if valuePtr == nil || len(raw) == 0 {
		return nil
	}

	dst := reflect.ValueOf(valuePtr)
	if dst.Kind() != reflect.Ptr || dst.IsNil() {
		// Defensive: callers should always pass a non-nil pointer, but if
		// they don't there's nothing meaningful to assign into.
		return nil
	}

	// json.Unmarshal expects a pointer; we already validated valuePtr is one.
	return json.Unmarshal(raw, valuePtr)
}

// readyFutureErr is a helper for synthesising a pre-failed future.
func readyFutureErr(err error) *gowfFuture {
	return &gowfFuture{readyErr: err}
}
