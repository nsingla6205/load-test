package vcm_workflows

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

// volumeCreateOperationResponsePayload marshals the created volume as {"volume": Volume_v1} for the durable job operation response.
func volumeCreateOperationResponsePayload(volume *datamodel.Volume, ontapVol *vsa.VolumeResponse) (*string, error) {
	volumeBody, err := buildVolumeOperationResponseBody(volume, ontapVol)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(map[string]any{"volume": volumeBody})
	if err != nil {
		return nil, fmt.Errorf("marshal volume operation response: %w", err)
	}
	result := string(payload)
	return &result, nil
}

func buildVolumeOperationResponseBody(volume *datamodel.Volume, ontapVol *vsa.VolumeResponse) (map[string]any, error) {
	if ontapVol == nil {
		return nil, fmt.Errorf("ONTAP volume response is required for operation response")
	}

	volumeBody := map[string]any{
		"uuid":              ontapVol.ExternalUUID,
		"name":              ontapVol.Name,
		"sizeInBytes":       float64(ontapVol.Size),
		"snapshotDirectory": ontapVol.SnapshotDirectoryAccessEnabled,
	}
	if ontapVol.UsedBytes != 0 {
		volumeBody["usedBytes"] = float64(ontapVol.UsedBytes)
	}
	if ontapVol.SnapReserve != 0 {
		volumeBody["snapReserve"] = float64(ontapVol.SnapReserve)
	}

	if volume != nil && volume.VolumeAttributes != nil && utils.IsNasProtocols(volume.VolumeAttributes.Protocols) {
		if style := mapOntapSecurityStyle(ontapVol.SecurityStyle); style != "" {
			volumeBody["securityStyle"] = style
		}
		if ontapVol.UnixPermissions != "" {
			volumeBody["unixPermissions"] = ontapVol.UnixPermissions
		}
		if ontapVol.JunctionPath != "" {
			volumeBody["shareName"] = ontapVol.JunctionPath
		}
	}

	return volumeBody, nil
}

func mapOntapSecurityStyle(style string) string {
	switch strings.ToLower(style) {
	case "unix":
		return "UNIX"
	case "ntfs":
		return "NTFS"
	default:
		return ""
	}
}
