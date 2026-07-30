package datamodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostEfficiencyFactorOrDefault(t *testing.T) {
	t.Run("returns default when nil", func(t *testing.T) {
		assert.Equal(t, DefaultHostEfficiencyFactor, HostEfficiencyFactorOrDefault(nil))
	})

	t.Run("returns provided value", func(t *testing.T) {
		value := 2.5
		assert.Equal(t, 2.5, HostEfficiencyFactorOrDefault(&value))
	})
}

func TestHostProvisioningModelOrDefault(t *testing.T) {
	empty := ProvisioningModel("")
	thick := ProvisioningModelThick
	thin := ProvisioningModelThin
	unknown := ProvisioningModel("UNKNOWN")

	tests := []struct {
		name      string
		value     *ProvisioningModel
		want      ProvisioningModel
		wantError bool
	}{
		{name: "nil defaults to thick", want: ProvisioningModelThick},
		{name: "empty defaults to thick", value: &empty, want: ProvisioningModelThick},
		{name: "thick is accepted", value: &thick, want: ProvisioningModelThick},
		{name: "thin is accepted", value: &thin, want: ProvisioningModelThin},
		{name: "unknown returns error", value: &unknown, wantError: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HostProvisioningModelOrDefault(tc.value)
			if tc.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestEffectiveHostProvisioningModel(t *testing.T) {
	t.Run("nil host returns error", func(t *testing.T) {
		_, err := EffectiveHostProvisioningModel(nil)
		require.Error(t, err)
	})

	t.Run("legacy host defaults to thick", func(t *testing.T) {
		got, err := EffectiveHostProvisioningModel(&Host{})
		require.NoError(t, err)
		assert.Equal(t, ProvisioningModelThick, got)
	})

	t.Run("unknown persisted value returns error", func(t *testing.T) {
		unknown := ProvisioningModel("UNKNOWN")
		_, err := EffectiveHostProvisioningModel(&Host{ProvisioningModel: &unknown})
		require.Error(t, err)
	})
}

func TestHostAttributesValueAndScan(t *testing.T) {
	t.Run("ValueAndScanRoundTrip", func(t *testing.T) {
		original := HostAttributes{
			MaintenanceMode:   true,
			Version:           "9.14.1",
			VersionGeneration: 3,
			VersionMajor:      9,
			VersionMinor:      14,
			PassphraseVersion: "CVS_PASSPHRASE_V1",
			Capabilities:      "NFS,SMB",
			PublicAccess:      true,
		}

		val, err := original.Value()
		require.NoError(t, err)

		var scanned HostAttributes
		require.NoError(t, scanned.Scan(val))
		require.Equal(t, original, scanned)
	})

	t.Run("ValuePersistsMaintenanceModeFalse", func(t *testing.T) {
		original := HostAttributes{
			MaintenanceMode: false,
			PublicAccess:    false,
		}

		val, err := original.Value()
		require.NoError(t, err)
		require.Contains(t, string(val.([]byte)), `"maintenance_mode":false`)

		var scanned HostAttributes
		require.NoError(t, scanned.Scan(val))
		require.False(t, scanned.MaintenanceMode)
	})

	t.Run("ScanNilValueResetsToZeroValue", func(t *testing.T) {
		attr := HostAttributes{
			MaintenanceMode:   true,
			Version:           "9.12.0",
			VersionGeneration: 2,
			VersionMajor:      9,
			VersionMinor:      12,
			PassphraseVersion: "VCP_PASSPHRASE_V0",
			Capabilities:      "NFS",
			PublicAccess:      true,
		}

		require.NoError(t, attr.Scan(nil))
		require.Equal(t, HostAttributes{}, attr)
	})

	t.Run("ScanInvalidTypeReturnsError", func(t *testing.T) {
		var attr HostAttributes
		err := attr.Scan("not-bytes")
		require.Error(t, err)
		require.Contains(t, err.Error(), "type assertion to []byte failed")
	})

	t.Run("ScanInvalidJSONReturnsError", func(t *testing.T) {
		var attr HostAttributes
		err := attr.Scan([]byte("{invalid-json"))
		require.Error(t, err)
	})
}
