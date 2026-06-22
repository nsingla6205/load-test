package temporalbackend

import (
	"errors"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/temporal"
)

// init registers a Temporal-aware adapter so orchestrator.AsApplicationError
// can translate *temporal.ApplicationError values without the core package
// importing the Temporal SDK. This runs whenever the temporalbackend package is
// linked (i.e. only in binaries that actually use the Temporal engine).
func init() {
	orchestrator.RegisterApplicationErrorAdapter(func(err error) (*orchestrator.ApplicationError, bool) {
		var te *temporal.ApplicationError
		if !errors.As(err, &te) {
			return nil, false
		}
		extractor := func(dest interface{}) error {
			if te.HasDetails() {
				return te.Details(dest)
			}
			return errors.New("no details")
		}
		return orchestrator.NewApplicationErrorFromSource(te.Error(), te.Type(), te.NonRetryable(), extractor), true
	})
}

// ToTemporalError converts an orchestrator.ApplicationError to a
// temporal.ApplicationError. Non-ApplicationError values are returned unchanged.
func ToTemporalError(err error) error {
	if err == nil {
		return nil
	}
	if appErr, ok := orchestrator.AsApplicationError(err); ok {
		if appErr.NonRetryable() {
			return temporal.NewNonRetryableApplicationError(appErr.Error(), appErr.Type(), appErr.Unwrap(), appErr.Details())
		}
		return temporal.NewApplicationError(appErr.Error(), appErr.Type(), appErr.Details())
	}
	return err
}
