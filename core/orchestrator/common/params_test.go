package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUpdateExternalClusterParams_HasUpdates(t *testing.T) {
	tests := []struct {
		name   string
		params *UpdateExternalClusterParams
		want   bool
	}{
		{name: "nil params", params: nil, want: false},
		{name: "empty params", params: &UpdateExternalClusterParams{}, want: false},
		{name: "description set", params: &UpdateExternalClusterParams{Description: ptrString("x")}, want: true},
		{name: "label set", params: &UpdateExternalClusterParams{Label: ptrString("x")}, want: true},
		{name: "management IP set", params: &UpdateExternalClusterParams{ManagementIP: ptrString("10.0.0.1")}, want: true},
		{name: "protocol set", params: &UpdateExternalClusterParams{Protocol: ptrString("HTTPS")}, want: true},
		{name: "port set", params: &UpdateExternalClusterParams{Port: ptrInt(443)}, want: true},
		{name: "username set", params: &UpdateExternalClusterParams{Username: ptrString("admin")}, want: true},
		{name: "password set", params: &UpdateExternalClusterParams{Password: ptrString("secret")}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.params.HasUpdates())
		})
	}
}

func ptrString(s string) *string { return &s }

func ptrInt(n int) *int { return &n }

func TestMaintenanceOperationType_IsValid(t *testing.T) {
	tests := []struct {
		op   MaintenanceOperationType
		want bool
	}{
		{op: MaintenanceOperationRebootMigrate, want: true},
		{op: MaintenanceOperationITC, want: true},
		{op: MaintenanceOperationType("bogus"), want: false},
		{op: MaintenanceOperationType(""), want: false},
	}
	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			assert.Equal(t, tt.want, tt.op.IsValid())
		})
	}
}

func TestVolumeFetchOptionsFromFields(t *testing.T) {
	t.Run("NilFieldSet", func(tt *testing.T) {
		opts := VolumeFetchOptionsFromFields(nil)
		assert.Equal(tt, VolumeFetchOptions{}, opts)
	})

	t.Run("DerivesRequestedOptions", func(tt *testing.T) {
		opts := VolumeFetchOptionsFromFields(map[string]bool{
			"activeDirectoryConfigId": true,
			"kmsConfigResourceId":     true,
			"throughputMibps":         true,
			"mountPoints":             true,
			"inReplication":           true,
		})
		assert.True(tt, opts.NeedActiveDirectory)
		assert.True(tt, opts.NeedKmsConfig)
		assert.True(tt, opts.NeedVolumePerformanceGroup)
		assert.True(tt, opts.NeedIPAddresses)
		assert.True(tt, opts.NeedInReplication)
	})
}
