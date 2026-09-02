package orchestrator

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewApplicationError_IsRetryableWithDetails(t *testing.T) {
	details := map[string]any{"key": "value"}
	err := NewApplicationError("boom", "BoomType", details)

	appErr, ok := AsApplicationError(err)
	require.True(t, ok)
	assert.Equal(t, "boom", appErr.Error())
	assert.Equal(t, "BoomType", appErr.Type())
	assert.Equal(t, details, appErr.Details())
	assert.True(t, appErr.HasDetails())
	assert.False(t, appErr.NonRetryable())
	assert.Nil(t, appErr.Unwrap())
}

func TestNewApplicationErrorWithCause_CarriesCauseAndDetails(t *testing.T) {
	cause := errors.New("underlying")
	err := NewApplicationErrorWithCause("boom", "BoomType", cause, 1001)

	appErr, ok := AsApplicationError(err)
	require.True(t, ok)
	assert.Equal(t, "boom", appErr.Error())
	assert.Equal(t, "BoomType", appErr.Type())
	assert.Equal(t, 1001, appErr.Details())
	assert.False(t, appErr.NonRetryable())
	assert.Equal(t, cause, appErr.Unwrap())
	assert.ErrorIs(t, err, cause)
}

func TestNewNonRetryableApplicationError_CarriesCauseAndDetails(t *testing.T) {
	cause := errors.New("underlying")
	err := NewNonRetryableApplicationError("perm", "PermType", cause, "detail-payload")

	appErr, ok := AsApplicationError(err)
	require.True(t, ok)
	assert.Equal(t, "perm", appErr.Error())
	assert.Equal(t, "PermType", appErr.Type())
	assert.Equal(t, "detail-payload", appErr.Details())
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, cause, appErr.Unwrap())
	assert.ErrorIs(t, err, cause)
}

func TestNewNonRetryableApplicationError_NoDetails(t *testing.T) {
	err := NewNonRetryableApplicationError("perm", "PermType", nil)
	appErr, ok := AsApplicationError(err)
	require.True(t, ok)
	assert.Nil(t, appErr.Details())
	assert.False(t, appErr.HasDetails())
	assert.Nil(t, appErr.Unwrap())
}

func TestApplicationError_ExtractDetails_RoundTrip(t *testing.T) {
	type payload struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	in := payload{Name: "abc", Count: 3}
	err := NewApplicationError("msg", "T", in)
	appErr, ok := AsApplicationError(err)
	require.True(t, ok)

	var out payload
	require.NoError(t, appErr.ExtractDetails(&out))
	assert.Equal(t, in, out)
}

func TestApplicationError_ExtractDetails_NoDetails(t *testing.T) {
	appErr := &ApplicationError{message: "m", errType: "T"}
	var dest map[string]any
	err := appErr.ExtractDetails(&dest)
	assert.Error(t, err)
	assert.EqualError(t, err, "no details")
}

func TestApplicationError_ExtractDetails_MarshalError(t *testing.T) {
	// channels cannot be JSON-marshaled, forcing the marshal error branch.
	appErr := &ApplicationError{message: "m", errType: "T", details: make(chan int)}
	var dest any
	err := appErr.ExtractDetails(&dest)
	assert.Error(t, err)
}

func TestAsApplicationError_WrappedAppError(t *testing.T) {
	inner := NewApplicationError("inner", "T", nil)
	wrapped := fmt.Errorf("context: %w", inner)

	appErr, ok := AsApplicationError(wrapped)
	require.True(t, ok)
	assert.Equal(t, "inner", appErr.Error())
}

func TestAsApplicationError_NotAnAppError(t *testing.T) {
	appErr, ok := AsApplicationError(errors.New("plain"))
	assert.False(t, ok)
	assert.Nil(t, appErr)
}

// fakeEngineError stands in for a backend-specific error type; the core package
// must be able to recognize it via a registered adapter without importing any
// engine SDK.
type fakeEngineError struct{ msg string }

func (e *fakeEngineError) Error() string { return e.msg }

func TestRegisterApplicationErrorAdapter_BridgesBackendError(t *testing.T) {
	RegisterApplicationErrorAdapter(func(err error) (*ApplicationError, bool) {
		var fe *fakeEngineError
		if errors.As(err, &fe) {
			return NewApplicationErrorFromSource(fe.msg, "FakeType", true, func(dest interface{}) error {
				return errors.New("no details")
			}), true
		}
		return nil, false
	})

	appErr, ok := AsApplicationError(&fakeEngineError{msg: "engine boom"})
	require.True(t, ok)
	assert.Equal(t, "FakeType", appErr.Type())
	assert.True(t, appErr.NonRetryable())
	assert.Equal(t, "engine boom", appErr.Error())
	assert.EqualError(t, appErr.ExtractDetails(nil), "no details")
}

func TestAsApplicationError_Nil(t *testing.T) {
	appErr, ok := AsApplicationError(nil)
	assert.False(t, ok)
	assert.Nil(t, appErr)
}

func TestNonRetryableError_ErrorAndUnwrap(t *testing.T) {
	inner := errors.New("cause")
	nre := &NonRetryableError{Err: inner}
	assert.Equal(t, "cause", nre.Error())
	assert.Equal(t, inner, nre.Unwrap())
	assert.ErrorIs(t, nre, inner)
}

func TestIsNonRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"plain error", errors.New("x"), false},
		{"NonRetryableError wrapper", &NonRetryableError{Err: errors.New("x")}, true},
		{"non-retryable app error", NewNonRetryableApplicationError("m", "T", nil), true},
		{"retryable app error", NewApplicationError("m", "T", nil), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isNonRetryable(tt.err))
		})
	}
}
