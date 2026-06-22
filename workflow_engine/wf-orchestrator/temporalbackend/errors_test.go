package temporalbackend

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/temporal"
)

func TestToTemporalError_Nil(t *testing.T) {
	assert.Nil(t, ToTemporalError(nil))
}

func TestToTemporalError_PlainErrorUnchanged(t *testing.T) {
	in := errors.New("plain")
	assert.Equal(t, in, ToTemporalError(in))
}

func TestToTemporalError_ConvertsApplicationError(t *testing.T) {
	type detail struct {
		Code int `json:"code"`
	}
	appErr := orchestrator.NewApplicationError("boom", "BoomType", detail{Code: 7})

	out := ToTemporalError(appErr)
	require.Error(t, out)

	var tErr *temporal.ApplicationError
	require.ErrorAs(t, out, &tErr)
	assert.Equal(t, "BoomType", tErr.Type())

	var got detail
	require.NoError(t, tErr.Details(&got))
	assert.Equal(t, 7, got.Code)
}

func TestToTemporalError_PreservesNonRetryable(t *testing.T) {
	cause := errors.New("underlying")
	appErr := orchestrator.NewNonRetryableApplicationError("boom", "BoomType", cause, "detail")

	out := ToTemporalError(appErr)
	require.Error(t, out)

	var tErr *temporal.ApplicationError
	require.ErrorAs(t, out, &tErr)
	assert.Equal(t, "BoomType", tErr.Type())
	assert.True(t, tErr.NonRetryable(), "non-retryable flag must survive conversion")
}

func TestToTemporalError_RetryableStaysRetryable(t *testing.T) {
	appErr := orchestrator.NewApplicationError("boom", "BoomType", nil)

	out := ToTemporalError(appErr)
	require.Error(t, out)

	var tErr *temporal.ApplicationError
	require.ErrorAs(t, out, &tErr)
	assert.False(t, tErr.NonRetryable())
}

// TestToTemporalError_DetectsWrappedApplicationError verifies the conversion
// uses AsApplicationError so a wrapped *orchestrator.ApplicationError is still
// recognized rather than passed through unchanged.
func TestToTemporalError_DetectsWrappedApplicationError(t *testing.T) {
	appErr := orchestrator.NewNonRetryableApplicationError("boom", "BoomType", nil)
	wrapped := fmt.Errorf("context: %w", appErr)

	out := ToTemporalError(wrapped)
	require.Error(t, out)

	var tErr *temporal.ApplicationError
	require.ErrorAs(t, out, &tErr)
	assert.Equal(t, "BoomType", tErr.Type())
	assert.True(t, tErr.NonRetryable())
}

// The following tests verify that the init()-registered adapter lets the
// engine-agnostic orchestrator.AsApplicationError recognize Temporal errors.
// They live here (not in the core package) so the core package and its tests
// stay free of the Temporal SDK.

func TestAsApplicationError_FromTemporalApplicationError(t *testing.T) {
	tErr := temporal.NewNonRetryableApplicationError("temporal-msg", "TempType", nil)

	appErr, ok := orchestrator.AsApplicationError(tErr)
	require.True(t, ok)
	assert.Equal(t, "TempType", appErr.Type())
	assert.True(t, appErr.NonRetryable())
	assert.Contains(t, appErr.Error(), "temporal-msg")
}

func TestAsApplicationError_FromTemporalWithDetails(t *testing.T) {
	type payload struct {
		Foo string `json:"foo"`
	}
	tErr := temporal.NewApplicationError("with-details", "T", payload{Foo: "bar"})

	appErr, ok := orchestrator.AsApplicationError(tErr)
	require.True(t, ok)

	// ExtractDetails resolves through the adapter-provided extractor, even
	// though the engine-agnostic details field is not populated.
	var out payload
	require.NoError(t, appErr.ExtractDetails(&out))
	assert.Equal(t, "bar", out.Foo)
}
