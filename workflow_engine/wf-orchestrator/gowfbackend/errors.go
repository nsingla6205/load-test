// Package gowfbackend implements orchestrator.Backend on top of the
// cschleiden/go-workflows engine. This file maps orchestrator errors to
// cschleiden's permanent-error sentinel so the engine's WithRetries loop
// honours orchestrator's NonRetryable semantics.
package gowfbackend

import (
	"errors"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// ToGowfError translates orchestrator's non-retryable error markers into
// cschleiden's permanent-error wrapper so go-workflows aborts retries.
//
// Behaviour:
//   - nil  -> nil (passthrough)
//   - *orchestrator.NonRetryableError                       -> permanent
//   - *orchestrator.ApplicationError with NonRetryable=true -> permanent
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

	return err
}
