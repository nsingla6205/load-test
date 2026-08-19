package google

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/api/compute/v1"
)

func Test_firstComputeOperationErrorMessage(t *testing.T) {
	tests := []struct {
		name string
		op   *compute.Operation
		want string
	}{
		{name: "nil operation", op: nil, want: ""},
		{name: "nil error", op: &compute.Operation{}, want: ""},
		{name: "empty errors slice", op: &compute.Operation{Error: &compute.OperationError{}}, want: ""},
		{name: "nil first element", op: &compute.Operation{Error: &compute.OperationError{Errors: []*compute.OperationErrorErrors{nil}}}, want: ""},
		{
			name: "message present",
			op:   &compute.Operation{Error: &compute.OperationError{Errors: []*compute.OperationErrorErrors{{Message: "quota exceeded"}}}},
			want: "quota exceeded",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, firstComputeOperationErrorMessage(tt.op))
		})
	}
}

// Fuzz_firstComputeOperationErrorMessage feeds malformed / minimal Compute LRO
// JSON and asserts deserialization + safe Errors indexing never panics.
func Fuzz_firstComputeOperationErrorMessage(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"error":{}}`))
	f.Add([]byte(`{"error":{"errors":[]}}`))
	f.Add([]byte(`{"error":{"errors":[{"message":"x"}]}}`))
	f.Add([]byte(`{"error":{"errors":[null]}}`))
	f.Add([]byte(`{"name":"op","error":{"errors":[{"code":"CONDITION_NOT_MET","message":"projects/123456789012/networks/tenant-vpc"}]}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		var op compute.Operation
		if err := json.Unmarshal(data, &op); err != nil {
			return
		}
		msg := firstComputeOperationErrorMessage(&op)
		if msg != "" {
			_ = msg
		}
	})
}
