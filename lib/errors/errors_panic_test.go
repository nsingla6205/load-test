package errors

import (
	"strings"
	"testing"
	_ "unsafe"
)

//go:linkname newTemporalPanicError go.temporal.io/sdk/internal.newPanicError
func newTemporalPanicError(value interface{}, stackTrace string) error

func TestExtractGoWorkflowCustomError_FromTemporalPanicError(t *testing.T) {
	panicErr := newTemporalPanicError("activity panic", "stack trace line 1")

	result := ExtractGoWorkflowCustomError(panicErr)
	if result.TrackingID != ErrInternalServerError {
		t.Errorf("expected tracking ID %d, got %d", ErrInternalServerError, result.TrackingID)
	}
	if result.OriginalErr == nil {
		t.Fatal("expected wrapped panic original error")
	}
	if !strings.Contains(result.OriginalErr.Error(), "activity panic") {
		t.Errorf("expected panic message in original error, got %q", result.OriginalErr.Error())
	}
	if !strings.Contains(result.OriginalErr.Error(), "stack trace line 1") {
		t.Errorf("expected stack trace in original error, got %q", result.OriginalErr.Error())
	}
}

func TestExtractJobFailureDetails_FromTemporalPanicError(t *testing.T) {
	panicErr := newTemporalPanicError("activity panic", "stack trace line 1")

	trackingID, retriable, msg := ExtractJobFailureDetails(panicErr)
	if trackingID != ErrInternalServerError {
		t.Errorf("expected tracking ID %d, got %d", ErrInternalServerError, trackingID)
	}
	if retriable {
		t.Error("expected non-retriable panic failure")
	}
	if !strings.Contains(msg, "activity panic") {
		t.Errorf("expected panic message in job error message, got %q", msg)
	}
	if !strings.Contains(msg, "stack trace line 1") {
		t.Errorf("expected stack trace in job error message, got %q", msg)
	}
}
