package vcm_workflows

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

// volumeOperationResponsePayload JSON-encodes the completed volume as the LRO response payload.
func volumeOperationResponsePayload(ontapVol *vsa.VolumeResponse, enrichment *vsa.VolumeOperationEnrichment) (*string, error) {
	volumeBody, err := buildVolumeOperationResponseBody(ontapVol, enrichment)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(volumeBody)
	if err != nil {
		return nil, fmt.Errorf("marshal volume operation response: %w", err)
	}
	result := string(payload)
	return &result, nil
}

func buildVolumeOperationResponseBody(ontapVol *vsa.VolumeResponse, enrichment *vsa.VolumeOperationEnrichment) (map[string]any, error) {
	if ontapVol == nil {
		return nil, fmt.Errorf("ONTAP volume response is required for operation response")
	}

	isNAS := strings.TrimSpace(ontapVol.JunctionPath) != ""
	isSAN := !isNAS && enrichment != nil && len(enrichment.BlockDevices) > 0

	volumeBody := map[string]any{
		"uuid":        ontapVol.ExternalUUID,
		"name":        ontapVol.Name,
		"sizeInBytes": float64(ontapVol.Size),
	}
	if ontapVol.UsedBytes != 0 {
		volumeBody["usedBytes"] = float64(ontapVol.UsedBytes)
	}
	if !ontapVol.Created.IsZero() {
		volumeBody["created"] = ontapVol.Created.UTC()
	}
	if ontapVol.SnapReserve != 0 {
		volumeBody["snapReserve"] = float64(ontapVol.SnapReserve)
	}

	if isNAS {
		volumeBody["snapshotDirectory"] = ontapVol.SnapshotDirectoryAccessEnabled
		if shareName := common.ShareNameFromJunctionPath(ontapVol.JunctionPath); shareName != "" {
			volumeBody["shareName"] = shareName
		}
		if style := mapOntapSecurityStyle(ontapVol.SecurityStyle); style != "" {
			volumeBody["securityStyle"] = style
		}
		if ontapVol.UnixPermissions != "" {
			volumeBody["unixPermissions"] = ontapVol.UnixPermissions
		}
		if enrichment != nil && enrichment.ExportPolicy != nil {
			volumeBody["exportPolicy"] = enrichment.ExportPolicy
		}
	} else if isSAN {
		if enrichment != nil && len(enrichment.BlockDevices) > 0 {
			volumeBody["blockDevices"] = enrichment.BlockDevices
		}
	}

	if enrichment != nil && enrichment.SnapshotPolicy != nil {
		volumeBody["snapshotPolicy"] = enrichment.SnapshotPolicy
	}

	return volumeBody, nil
}

func mapOntapSecurityStyle(style string) string {
	switch strings.ToLower(style) {
	case utils.UnixSecurityStyle:
		return strings.ToUpper(utils.UnixSecurityStyle)
	case utils.NtfsSecurityStyle:
		return strings.ToUpper(utils.NtfsSecurityStyle)
	default:
		return ""
	}
}
