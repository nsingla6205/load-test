package vcm_workflows

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
)

func TestVolumeCreateOperationResponsePayload_BlockVolume(t *testing.T) {
	createdAt := time.Date(2016, 11, 30, 20, 32, 50, 459000000, time.UTC)
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "9760acf5-4638-11e7-9bdb-020073ca0001", Name: "vol-1"},
		Size:                           107374182400,
		UsedBytes:                      1409024,
		SnapReserve:                    20,
		SnapshotDirectoryAccessEnabled: false,
		SecurityStyle:                  "unix",
		UnixPermissions:                "0755",
		Created:                        createdAt,
	}

	payload, err := volumeOperationResponsePayload(ontapVol, &activities.VolumeOperationEnrichment{
		BlockDevices: []map[string]any{{
			"osType":  "LINUX",
			"igroups": []string{"hg-db-linux"},
		}},
	})
	require.NoError(t, err)
	require.NotNil(t, payload)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.Equal(t, "9760acf5-4638-11e7-9bdb-020073ca0001", volumeBody["uuid"])
	assert.Equal(t, "vol-1", volumeBody["name"])
	assert.Equal(t, float64(107374182400), volumeBody["sizeInBytes"])
	assert.Equal(t, float64(1409024), volumeBody["usedBytes"])
	assert.Equal(t, float64(20), volumeBody["snapReserve"])
	assert.Equal(t, createdAt.UTC().Format(time.RFC3339Nano), volumeBody["created"])
	assert.NotContains(t, volumeBody, "shareName")
	assert.NotContains(t, volumeBody, "securityStyle")
	assert.NotContains(t, volumeBody, "unixPermissions")
	assert.NotContains(t, volumeBody, "snapshotDirectory")
	assert.NotContains(t, volumeBody, "protocols")
	assert.NotContains(t, volumeBody, "exportPolicy")

	blockDevices, ok := volumeBody["blockDevices"].([]any)
	require.True(t, ok)
	require.Len(t, blockDevices, 1)
	assert.NotContains(t, volumeBody, "snapshotPolicy")
}

func TestVolumeCreateOperationResponsePayload_BlockVolumeWithSnapshotPolicy(t *testing.T) {
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{ExternalUUID: "9b5cf03b-b268-11f1-961b-794adbfdaef7", Name: "voltest_iscsi01"},
		Size:             29327355904,
		UsedBytes:        417792,
		SnapReserve:      10,
	}

	payload, err := volumeOperationResponsePayload(ontapVol, &activities.VolumeOperationEnrichment{
		BlockDevices: []map[string]any{{
			"osType":  "LINUX",
			"igroups": []string{"hgtest_linux01", "hgtest_linux03"},
		}},
		SnapshotPolicy: map[string]any{
			"enabled": true,
			"hourlySchedule": map[string]any{
				"snapshotsToKeep": float64(4),
				"minute":          float64(0),
			},
		},
	})
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.Equal(t, float64(10), volumeBody["snapReserve"])
	snapshotPolicy, ok := volumeBody["snapshotPolicy"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, snapshotPolicy["enabled"])
	assert.NotContains(t, volumeBody, "shareName")
	assert.NotContains(t, volumeBody, "exportPolicy")
}

func TestVolumeCreateOperationResponsePayload_NASVolume(t *testing.T) {
	createdAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "9760acf5-4638-11e7-9bdb-020073ca0001", Name: "nfs-vol"},
		Size:                           107374182400,
		UsedBytes:                      1409024,
		SnapReserve:                    20,
		SnapshotDirectoryAccessEnabled: true,
		SecurityStyle:                  "unix",
		UnixPermissions:                "0755",
		JunctionPath:                   "/nfs-vol",
		ExportPolicyName:               "nfs-vol",
		Created:                        createdAt,
	}

	payload, err := volumeOperationResponsePayload(ontapVol, &activities.VolumeOperationEnrichment{
		ExportPolicy: map[string]any{
			"rules": []map[string]any{{
				"allowedClients": "10.0.0.0/8",
				"accessType":     "READ_WRITE",
				"nfsv3":          true,
				"nfsv4":          false,
				"hasRootAccess":  "true",
			}},
		},
	})
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.Equal(t, "nfs-vol", volumeBody["shareName"])
	assert.Equal(t, "UNIX", volumeBody["securityStyle"])
	assert.Equal(t, float64(20), volumeBody["snapReserve"])
	assert.Equal(t, "0755", volumeBody["unixPermissions"])
	assert.Equal(t, createdAt.UTC().Format(time.RFC3339Nano), volumeBody["created"])
	assert.Contains(t, volumeBody, "exportPolicy")
	assert.NotContains(t, volumeBody, "blockDevices")
	assert.NotContains(t, volumeBody, "mountPoints")
	assert.NotContains(t, volumeBody, "snapshotPolicy")
}

func TestVolumeCreateOperationResponsePayload_NASVolumeWithSnapshotPolicy(t *testing.T) {
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "vol-uuid", Name: "voltest_nfs01"},
		Size:                           16106127360,
		UsedBytes:                      368640,
		SnapReserve:                    10,
		SnapshotDirectoryAccessEnabled: false,
		SecurityStyle:                  "unix",
		UnixPermissions:                "0750",
		JunctionPath:                   "/voltest_nfs01_renamed",
	}

	payload, err := volumeOperationResponsePayload(ontapVol, &activities.VolumeOperationEnrichment{
		SnapshotPolicy: map[string]any{
			"enabled": true,
			"hourlySchedule": map[string]any{
				"snapshotsToKeep": float64(4),
				"minute":          float64(0),
			},
		},
	})
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.Equal(t, "voltest_nfs01_renamed", volumeBody["shareName"])
	snapshotPolicy, ok := volumeBody["snapshotPolicy"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, snapshotPolicy["enabled"])
	assert.Contains(t, snapshotPolicy, "hourlySchedule")
}

func TestVolumeCreateOperationResponsePayload_NilOntapVolume(t *testing.T) {
	_, err := volumeOperationResponsePayload(nil, nil)
	require.Error(t, err)
}

func TestVolumeCreateOperationResponsePayload_SnapReserveZero(t *testing.T) {
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "vol-uuid-1", Name: "vol-1"},
		Size:                           107374182400,
		SnapReserve:                    0,
		SnapshotDirectoryAccessEnabled: false,
	}

	payload, err := volumeOperationResponsePayload(ontapVol, nil)
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.NotContains(t, volumeBody, "snapReserve")
}

func TestVolumeCreateOperationResponsePayload_CreatedZero(t *testing.T) {
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "vol-uuid-1", Name: "vol-1"},
		Size:                           107374182400,
		SnapshotDirectoryAccessEnabled: true,
	}

	payload, err := volumeOperationResponsePayload(ontapVol, nil)
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.NotContains(t, volumeBody, "created")
}

func TestVolumeCreateOperationResponsePayload_UsedBytesZero(t *testing.T) {
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "vol-uuid-1", Name: "vol-1"},
		Size:                           107374182400,
		SnapshotDirectoryAccessEnabled: true,
	}

	payload, err := volumeOperationResponsePayload(ontapVol, nil)
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.NotContains(t, volumeBody, "usedBytes")
}

func TestVolumeCreateOperationResponsePayload_NASVolumeWithoutProtocols(t *testing.T) {
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "9760acf5-4638-11e7-9bdb-020073ca0001", Name: "nfs-vol"},
		Size:                           107374182400,
		SnapReserve:                    20,
		SnapshotDirectoryAccessEnabled: true,
		SecurityStyle:                  "unix",
		UnixPermissions:                "0755",
		JunctionPath:                   "/nfs-vol",
	}

	payload, err := volumeOperationResponsePayload(ontapVol, nil)
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.Equal(t, "nfs-vol", volumeBody["shareName"])
	assert.Equal(t, "UNIX", volumeBody["securityStyle"])
	assert.Equal(t, "0755", volumeBody["unixPermissions"])
}

func TestVolumeCreateOperationResponsePayload_NASFieldsOnlyFromOntap(t *testing.T) {
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "vol-uuid", Name: "volut16"},
		Size:                           10737418240,
		SnapshotDirectoryAccessEnabled: true,
		JunctionPath:                   "/volut16",
	}

	payload, err := volumeOperationResponsePayload(ontapVol, nil)
	require.NoError(t, err)

	var volumeBody map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &volumeBody))
	assert.Equal(t, "volut16", volumeBody["shareName"])
	assert.NotContains(t, volumeBody, "securityStyle")
	assert.NotContains(t, volumeBody, "unixPermissions")
}

func TestMapOntapSecurityStyle(t *testing.T) {
	assert.Equal(t, "UNIX", mapOntapSecurityStyle("unix"))
	assert.Equal(t, "NTFS", mapOntapSecurityStyle("ntfs"))
	assert.Equal(t, "", mapOntapSecurityStyle("unified"))
	assert.Equal(t, "", mapOntapSecurityStyle("mixed"))
}
