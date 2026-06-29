package datamodel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

