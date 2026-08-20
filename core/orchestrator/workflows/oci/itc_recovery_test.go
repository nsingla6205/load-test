package oci

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
)

func TestVlmFailureIndicatesITCRecovery_Table(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "generic Go error", err: errors.New("generic failure"), want: false},
		{name: "VLM 500 without cause", err: vlmClientError(500, "INTERNAL_ERROR", "An internal system error occurred", nil), want: false},
		{name: "VLM 429 unrelated", err: vlmClientError(429, "RATE_LIMITED", "slow down", nil), want: false},
		{name: "capacity in message only not cause", err: vlmClientError(503, "OUT_OF_CAPACITY", "out of capacity", nil), want: false},
		{name: "unrelated message and empty cause", err: vlmClientError(429, "OUT_OF_CAPACITY", "shape unavailable", nil), want: false},
		{name: "capacity-like text in message field only", err: vlmClientError(400, "LimitExceeded", "Out Of Capacity for shape", nil), want: false},
		{name: "substring match in cause", err: vlmClientError(409, "CONFLICT", "conflict", []string{"host is out of capacity"}), want: true},
		{name: "case insensitive cause OUT OF CAPACITY", err: vlmClientError(500, "INTERNAL_ERROR", "wrapper", []string{"ScaleAndReplaceNodeWorkflow: Out Of Capacity in AD-1"}), want: true},
		{name: "exact cause match HTTP 500", err: vlmClientError(500, "INTERNAL_ERROR", "An internal system error occurred", []string{ociVlmITCRecoveryCapacityErrorMessage}), want: true},
		{name: "nested cause substring HTTP 500", err: vlmClientError(500, "INTERNAL_ERROR", "wrapper", []string{"child workflow: failed to create new VM: out of capacity in zone"}), want: true},
		{name: "HTTP 409 with exact cause", err: vlmClientError(409, "CONFLICT", "conflict", []string{ociVlmITCRecoveryCapacityErrorMessage}), want: true},
		{name: "HTTP 429 with exact cause in cause array", err: vlmClientError(429, "OUT_OF_CAPACITY", "shape unavailable", []string{ociVlmITCRecoveryCapacityErrorMessage}), want: true},
		{name: "match second cause element", err: vlmClientError(500, "INTERNAL_ERROR", "x", []string{"unrelated", "cluster out of capacity now"}), want: true},
		{name: "wrong substring in cause", err: vlmClientError(500, "INTERNAL_ERROR", "x", []string{"host is full"}), want: false},
		{name: "wrapped temporal application error", err: fmt.Errorf("child workflow execution error: %w", vlmClientError(500, "INTERNAL_ERROR", "x", []string{ociVlmITCRecoveryCapacityErrorMessage})), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, vlmFailureIndicatesITCRecovery(tt.err))
		})
	}
}

func TestBatchFailureIndicatesITCRecovery_Table(t *testing.T) {
	partialErr := vsaerrors.NewVCPError(
		vsaerrors.ErrIncorrectVSAClusterState,
		fmt.Errorf("%w: partial update failure", errOCIPartialVLMUpdateBatchFailure),
	)
	wrappedPartial := fmt.Errorf("update pool batch failed: %w", partialErr)

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "VLM capacity cause", err: vlmClientError(500, "INTERNAL_ERROR", "err", []string{ociVlmITCRecoveryCapacityErrorMessage}), want: true},
		{name: "partial UpdateStatus sentinel", err: partialErr, want: true},
		{name: "wrapped partial UpdateStatus sentinel", err: wrappedPartial, want: true},
		{name: "generic VLM call failure", err: errors.New("simulated VLM call failure"), want: false},
		{name: "VCP error without partial sentinel", err: vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, errors.New("other")), want: false},
		{name: "nil", err: nil, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, batchFailureIndicatesITCRecovery(tt.err))
		})
	}
}

func TestVlmErrorMatchesITCRecoveryCause_Table(t *testing.T) {
	tests := []struct {
		name   string
		causes []string
		want   bool
	}{
		{name: "empty causes", causes: nil, want: false},
		{name: "exact lowercase", causes: []string{"out of capacity"}, want: true},
		{name: "mixed case", causes: []string{"OUT OF CAPACITY in zone"}, want: true},
		{name: "title case embedded", causes: []string{"failed: Out Of Capacity for VM.Standard.E4.Flex"}, want: true},
		{name: "no match", causes: []string{"quota exceeded elsewhere"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := vlmErrorMatchesITCRecoveryCause(vlm.VLMClientError{Cause: tt.causes})
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractVLMRecoveryStatus_Table(t *testing.T) {
	longCause := "child workflow: out of capacity in availability domain"
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "prefers first cause over code",
			err:  vlmClientError(500, "INTERNAL_ERROR", "An internal system error occurred", []string{longCause}),
			want: longCause,
		},
		{
			name: "falls back to code when cause empty",
			err:  vlmClientError(429, "OUT_OF_CAPACITY", "shape unavailable", nil),
			want: "OUT_OF_CAPACITY",
		},
		{
			name: "falls back to message when cause and code empty",
			err:  vlmClientError(500, "", "shape unavailable", nil),
			want: "shape unavailable",
		},
		{
			name: "generic error uses Error()",
			err:  errors.New("plain failure"),
			want: "plain failure",
		},
		{
			name: "nil returns empty",
			err:  nil,
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractVLMRecoveryStatus(tt.err))
		})
	}
}

func vlmClientError(httpCode int, code, message string, cause []string) error {
	return temporal.NewApplicationError(message, vlm.ErrorTypeVLMClientError, vlm.VLMClientError{
		HttpCode: httpCode,
		Code:     code,
		Message:  message,
		Cause:    cause,
	})
}
