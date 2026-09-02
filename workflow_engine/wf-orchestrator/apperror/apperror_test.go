package apperror

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

func TestWrapAsAppError_NonCustomError_ReturnedUnchanged(t *testing.T) {
	// A plain error is not a vsaerrors.CustomError, so it must pass through
	// untouched (and must not be turned into an orchestrator ApplicationError).
	plain := errors.New("boom")

	got := WrapAsAppError(plain, true)

	require.Equal(t, plain, got, "non-custom errors must be returned unchanged")
	assert.True(t, errors.Is(got, plain), "the original error identity must be preserved")
	_, ok := orchestrator.AsApplicationError(got)
	assert.False(t, ok, "a plain error must not be wrapped as an application error")
}

func TestWrapAsAppError_NilError_ReturnedUnchanged(t *testing.T) {
	assert.NoError(t, WrapAsAppError(nil, true))
	assert.NoError(t, WrapAsAppError(nil, false))
}

func TestWrapAsAppError_CustomError_NonRetryable(t *testing.T) {
	custom := vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("bad input"))

	got := WrapAsAppError(custom, true)

	appErr, ok := orchestrator.AsApplicationError(got)
	require.True(t, ok, "a custom error must be wrapped as an orchestrator application error")
	assert.True(t, appErr.NonRetryable(), "nonRetryable=true must produce a non-retryable application error")
	assert.Equal(t, vsaerrors.CustomErrorType, appErr.Type())
	assert.Equal(t, custom.Error(), appErr.Error())

	var tid int
	require.NoError(t, appErr.ExtractDetails(&tid), "the tracking id must be carried as details")
	assert.Equal(t, custom.TrackingID, tid)
}

func TestWrapAsAppError_CustomError_Retryable(t *testing.T) {
	custom := vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, errors.New("transient fetch failure"))

	got := WrapAsAppError(custom, false)

	appErr, ok := orchestrator.AsApplicationError(got)
	require.True(t, ok, "a custom error must be wrapped as an orchestrator application error")
	assert.False(t, appErr.NonRetryable(), "nonRetryable=false must produce a retryable application error")
	assert.Equal(t, vsaerrors.CustomErrorType, appErr.Type())
	assert.Equal(t, custom.Error(), appErr.Error())
	require.ErrorIs(t, appErr.Unwrap(), custom, "retryable wrappers must preserve the CustomError cause")

	var tid int
	require.NoError(t, appErr.ExtractDetails(&tid), "the tracking id must be carried as details")
	assert.Equal(t, custom.TrackingID, tid)
}

func TestExtractJobFailureDetails_RetryableViaApperrorWrapAsAppError(t *testing.T) {
	originalErr := errors.New("secret manager fetch failed")
	customErr := vsaerrors.NewVCPError(vsaerrors.ErrGCPResourceFetchError, originalErr)
	appErr := WrapAsAppError(customErr, false)

	trackingID, retriable, msg := vsaerrors.ExtractJobFailureDetails(appErr)

	require.Equal(t, vsaerrors.ErrGCPResourceFetchError, trackingID)
	assert.True(t, retriable, "expected retryable failure from catalog entry")
	assert.Equal(t, originalErr.Error(), msg)
	assert.NotEqual(t, customErr.Message, msg, "expected raw provider text, not catalog message")
}

func TestWrapAsAppError_WrappedCustomError_IsDetected(t *testing.T) {
	// errors.As must see through the wrapping chain, so a fmt-wrapped custom
	// error is still converted.
	custom := vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, errors.New("cause"))
	wrapped := &wrapErr{msg: "context", cause: custom}

	got := WrapAsAppError(wrapped, true)

	appErr, ok := orchestrator.AsApplicationError(got)
	require.True(t, ok)
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, wrapped.Error(), appErr.Error())
}

type wrapErr struct {
	msg   string
	cause error
}

func (w *wrapErr) Error() string { return w.msg + ": " + w.cause.Error() }
func (w *wrapErr) Unwrap() error { return w.cause }
