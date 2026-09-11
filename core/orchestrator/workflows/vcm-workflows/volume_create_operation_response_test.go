package vcm_workflows

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
)

func TestVolumeCreateOperationResponsePayload_BlockVolume(t *testing.T) {
	volume := testBlockVolume()
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "9760acf5-4638-11e7-9bdb-020073ca0001", Name: "vol-1"},
		Size:                           volume.SizeInBytes,
		UsedBytes:                      1409024,
		SnapReserve:                    20,
		SnapshotDirectoryAccessEnabled: false,
	}

	payload, err := volumeCreateOperationResponsePayload(volume, ontapVol)
	require.NoError(t, err)
	require.NotNil(t, payload)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &body))
	volumeBody, ok := body["volume"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "9760acf5-4638-11e7-9bdb-020073ca0001", volumeBody["uuid"])
	assert.Equal(t, "vol-1", volumeBody["name"])
	assert.Equal(t, float64(107374182400), volumeBody["sizeInBytes"])
	assert.Equal(t, float64(1409024), volumeBody["usedBytes"])
	assert.Equal(t, float64(20), volumeBody["snapReserve"])
	assert.Equal(t, false, volumeBody["snapshotDirectory"])
	assert.NotContains(t, volumeBody, "shareName")
	assert.NotContains(t, volumeBody, "securityStyle")
	assert.NotContains(t, volumeBody, "unixPermissions")
	assert.NotContains(t, volumeBody, "protocols")
	assert.NotContains(t, volumeBody, "blockDevices")
	assert.NotContains(t, volumeBody, "exportPolicy")
}

func TestVolumeCreateOperationResponsePayload_NASVolume(t *testing.T) {
	volume := testNFSVolume()
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "9760acf5-4638-11e7-9bdb-020073ca0001", Name: "nfs-vol"},
		Size:                           volume.SizeInBytes,
		UsedBytes:                      1409024,
		SnapReserve:                    20,
		SnapshotDirectoryAccessEnabled: true,
		SecurityStyle:                  "unix",
		UnixPermissions:                "0755",
		JunctionPath:                     "/nfs-vol",
	}

	payload, err := volumeCreateOperationResponsePayload(volume, ontapVol)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &body))
	volumeBody := body["volume"].(map[string]any)
	assert.Equal(t, "/nfs-vol", volumeBody["shareName"])
	assert.Equal(t, "UNIX", volumeBody["securityStyle"])
	assert.Equal(t, float64(20), volumeBody["snapReserve"])
	assert.Equal(t, "0755", volumeBody["unixPermissions"])
	assert.NotContains(t, volumeBody, "blockDevices")
	assert.NotContains(t, volumeBody, "mountPoints")
	assert.NotContains(t, volumeBody, "exportPolicy")
	assert.NotContains(t, volumeBody, "snapshotPolicy")
}

func TestVolumeCreateOperationResponsePayload_NilOntapVolume(t *testing.T) {
	_, err := volumeCreateOperationResponsePayload(testBlockVolume(), nil)
	require.Error(t, err)
}

func TestVolumeCreateOperationResponsePayload_UsedBytesZero(t *testing.T) {
	volume := testBlockVolume()
	ontapVol := &vsa.VolumeResponse{
		ProviderResponse:               vsa.ProviderResponse{ExternalUUID: "vol-uuid-1", Name: "vol-1"},
		Size:                           volume.SizeInBytes,
		SnapshotDirectoryAccessEnabled: true,
	}

	payload, err := volumeCreateOperationResponsePayload(volume, ontapVol)
	require.NoError(t, err)

	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(*payload), &body))
	volumeBody := body["volume"].(map[string]any)
	assert.NotContains(t, volumeBody, "usedBytes")
}

func TestMapOntapSecurityStyle(t *testing.T) {
	assert.Equal(t, "UNIX", mapOntapSecurityStyle("unix"))
	assert.Equal(t, "NTFS", mapOntapSecurityStyle("ntfs"))
	assert.Equal(t, "", mapOntapSecurityStyle("unified"))
	assert.Equal(t, "", mapOntapSecurityStyle("mixed"))
}
