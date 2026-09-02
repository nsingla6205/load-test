package orchestrator

import (
	"encoding/json"
	"errors"
)

// ApplicationError is an engine-agnostic structured error that carries a type
// string and detail payload, mirroring temporal.ApplicationError.
type ApplicationError struct {
	message      string
	errType      string
	details      interface{}
	nonRetryable bool
	cause        error
	// detailsExtractor, when set by a backend adapter, supplies ExtractDetails
	// behaviour for an engine-specific error that carries its own detail payload.
	// Kept as a closure so this core package needs no engine SDK import.
	detailsExtractor func(dest interface{}) error
}

func (e *ApplicationError) Error() string { return e.message }

// Type returns the application error type string.
func (e *ApplicationError) Type() string { return e.errType }

// Details returns the detail payload attached to this error.
func (e *ApplicationError) Details() interface{} { return e.details }

// HasDetails returns true if the error carries detail payload(s).
func (e *ApplicationError) HasDetails() bool { return e.details != nil }

// ExtractDetails deserializes the detail payload into dest via JSON round-trip,
// matching the temporal.ApplicationError.Details(dest) calling convention.
func (e *ApplicationError) ExtractDetails(dest interface{}) error {
	if e.detailsExtractor != nil {
		return e.detailsExtractor(dest)
	}
	if e.details == nil {
		return errors.New("no details")
	}
	data, err := json.Marshal(e.details)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, dest)
}

// NonRetryable returns true if this error was marked non-retryable.
func (e *ApplicationError) NonRetryable() bool { return e.nonRetryable }

// Unwrap returns the wrapped cause, if any.
func (e *ApplicationError) Unwrap() error { return e.cause }

// NewApplicationError creates a structured application error (retryable).
func NewApplicationError(msg, errType string, details interface{}) error {
	return &ApplicationError{message: msg, errType: errType, details: details}
}

// NewApplicationErrorWithCause creates a retryable application error with a cause and details.
func NewApplicationErrorWithCause(msg, errType string, cause error, details interface{}) error {
	return &ApplicationError{message: msg, errType: errType, details: details, cause: cause}
}

// NewNonRetryableApplicationError creates a non-retryable structured error.
// Signature mirrors temporal.NewNonRetryableApplicationError(msg, errType, cause, details...).
func NewNonRetryableApplicationError(msg, errType string, cause error, details ...interface{}) error {
	var det interface{}
	if len(details) > 0 {
		det = details[0]
	}
	return &ApplicationError{
		message:      msg,
		errType:      errType,
		details:      det,
		nonRetryable: true,
		cause:        cause,
	}
}

// ApplicationErrorAdapter translates a backend-specific error into an
// *ApplicationError. It returns false when err is not recognized.
type ApplicationErrorAdapter func(err error) (*ApplicationError, bool)

// applicationErrorAdapters holds backend-registered translators. It is only
// appended to during package init() of backend packages, so no locking is
// required for the read path.
var applicationErrorAdapters []ApplicationErrorAdapter

// RegisterApplicationErrorAdapter registers an adapter consulted by
// AsApplicationError for engine-specific error types. Backends (e.g.
// temporalbackend) register one in init() so this core package needs no engine
// SDK import — keeping go-workflows-only binaries free of the Temporal SDK and
// vice versa.
func RegisterApplicationErrorAdapter(a ApplicationErrorAdapter) {
	if a != nil {
		applicationErrorAdapters = append(applicationErrorAdapters, a)
	}
}

// NewApplicationErrorFromSource builds an *ApplicationError on behalf of a
// backend adapter bridging an engine-specific error. detailsExtractor may be
// nil; when set it backs ExtractDetails.
func NewApplicationErrorFromSource(message, errType string, nonRetryable bool, detailsExtractor func(dest interface{}) error) *ApplicationError {
	return &ApplicationError{
		message:          message,
		errType:          errType,
		nonRetryable:     nonRetryable,
		detailsExtractor: detailsExtractor,
	}
}

// AsApplicationError checks whether err (or any wrapped error) is an
// *ApplicationError and returns it, consulting registered backend adapters for
// engine-specific error types.
func AsApplicationError(err error) (*ApplicationError, bool) {
	if err == nil {
		return nil, false
	}
	var appErr *ApplicationError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	for _, adapt := range applicationErrorAdapters {
		if ae, ok := adapt(err); ok {
			return ae, true
		}
	}
	return nil, false
}

// NonRetryableError wraps an error to signal that retry should not be attempted.
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string { return e.Err.Error() }
func (e *NonRetryableError) Unwrap() error { return e.Err }

// isNonRetryable checks whether an error should not be retried.
func isNonRetryable(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := err.(*NonRetryableError); ok {
		return true
	}
	if appErr, ok := AsApplicationError(err); ok {
		return appErr.NonRetryable()
	}
	return false
}
