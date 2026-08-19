package errors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactProviderError(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "no resource paths",
			input: "operation failed: quota exceeded",
			want:  "operation failed: quota exceeded",
		},
		{
			name:  "project number and network",
			input: "Consumer network projects/987654321012/networks/tenant-vpc not found",
			want:  "Consumer network [REDACTED]/[REDACTED] not found",
		},
		{
			name:  "project id and subnetwork",
			input: "projects/my-customer-proj/subnetworks/subnet-a is in use",
			want:  "[REDACTED]/[REDACTED] is in use",
		},
		{
			name:  "global network path",
			input: "peer projects/123456789012/global/networks/vpc-ap-tst failed",
			want:  "peer [REDACTED]/[REDACTED] failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, RedactProviderError(tt.input))
		})
	}
}
