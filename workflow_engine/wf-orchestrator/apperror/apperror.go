// Package apperror bridges VCP custom errors (lib/errors) into engine-agnostic
// orchestrator application errors. It lives outside the core orchestrator
// package so that package stays free of any lib/errors dependency, while giving
// the workflow/activity/client layers a single shared conversion helper.
package apperror

import (
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// WrapAsAppError converts a vsaerrors.CustomError into an engine-agnostic
// orchestrator application error, preserving the tracking ID and retryability.
// Errors that are not a vsaerrors.CustomError are returned unchanged.
//
// VCM runs on go-workflows, whose retry loop only honours
// orchestrator.ApplicationError / NonRetryableError (via gowfbackend.ToGowfError).
// A temporal.ApplicationError (what vsaerrors.WrapAs*TemporalApplicationError
// produces) carries no retry meaning there unless the Temporal SDK adapter is
// linked, so a "non-retryable" temporal error would be silently retried.
// The orchestrator error is translated natively by both backends
// (gowfbackend -> permanent error, temporalbackend -> temporal.ApplicationError).
func WrapAsAppError(err error, nonRetryable bool) error {
	var customErr *vsaerrors.CustomError
	if !vsaerrors.As(err, &customErr) {
		return err
	}
	if nonRetryable {
		return orchestrator.NewNonRetryableApplicationError(
			err.Error(), vsaerrors.CustomErrorType, customErr, customErr.TrackingID)
	}
	return orchestrator.NewApplicationErrorWithCause(
		err.Error(), vsaerrors.CustomErrorType, customErr, customErr.TrackingID)
}
