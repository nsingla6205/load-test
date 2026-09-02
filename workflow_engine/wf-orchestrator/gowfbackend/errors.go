// Package gowfbackend implements orchestrator.Backend on top of the
// cschleiden/go-workflows engine. This file maps orchestrator errors to
// cschleiden's permanent-error sentinel so the engine's WithRetries loop
// honours orchestrator's NonRetryable semantics.
package gowfbackend

import (
	"errors"

	gowf "github.com/cschleiden/go-workflows/workflow"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// typedApplicationError matches structured application errors from activity
// layers (including temporal.ApplicationError from reused VCP activities)
// without importing engine SDKs into gowfbackend.
type typedApplicationError interface {
	Type() string
	NonRetryable() bool
}

// ToGowfError translates orchestrator's non-retryable error markers into
// cschleiden's permanent-error wrapper so go-workflows aborts retries.
//
// Behaviour:
//   - nil  -> nil (passthrough)
//   - *orchestrator.NonRetryableError                       -> permanent
//   - *orchestrator.ApplicationError with NonRetryable=true -> permanent
//   - typed application error with Type=CustomError and NonRetryable=true -> permanent
//   - any other error                                       -> returned unchanged (retryable)
//
// This mirrors how Temporal's SDK natively understands
// temporal.ApplicationError{NonRetryable=true}.
func ToGowfError(err error) error {
	if err == nil {
		return nil
	}

	var nre *orchestrator.NonRetryableError
	if errors.As(err, &nre) {
		return gowf.NewPermanentError(err)
	}

	if appErr, ok := orchestrator.AsApplicationError(err); ok && appErr.NonRetryable() {
		return gowf.NewPermanentError(err)
	}

	var typedAppErr typedApplicationError
	if errors.As(err, &typedAppErr) &&
		typedAppErr.Type() == vsaerrors.CustomErrorType &&
		typedAppErr.NonRetryable() {
		return gowf.NewPermanentError(err)
	}

	return err
}
