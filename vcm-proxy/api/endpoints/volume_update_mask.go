package api

import (
	"strings"

	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	vcmserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcm-proxy/api/vcm-servergen"
)

// volumeUpdateMaskAllowedFields is the set of mutable VolumeUpdate_v1 field names allowed in updateMask.
var volumeUpdateMaskAllowedFields = map[string]struct{}{
	"sizeInBytes":       {},
	"snapReserve":       {},
	"snapshotDirectory": {},
	"snapshotPolicy":    {},
	"exportPolicy":      {},
	"blockDevices":      {},
	"shareName":         {},
	"unixPermissions":   {},
}

// parseVolumeUpdateMask parses a comma-separated updateMask into a set of root field names.
func parseVolumeUpdateMask(updateMask string) (map[string]bool, error) {
	fields := make(map[string]bool)
	for _, part := range strings.Split(updateMask, ",") {
		path := strings.TrimSpace(part)
		if path == "" {
			continue
		}

		root := strings.SplitN(path, ".", 2)[0]
		field, err := validateVolumeUpdateMaskField(root)
		if err != nil {
			return nil, err
		}
		fields[field] = true
	}

	if len(fields) == 0 {
		return nil, utilErrors.NewUserInputValidationErr("updateMask must include at least one field")
	}

	return fields, nil
}

// validateVolumeUpdateMaskField returns field if it is a supported, non-uuid updateMask entry.
func validateVolumeUpdateMaskField(field string) (string, error) {
	trimmed := strings.TrimSpace(field)
	if trimmed == "" {
		return "", utilErrors.NewUserInputValidationErr("updateMask contains an empty field path")
	}

	if strings.EqualFold(trimmed, "uuid") {
		return "", utilErrors.NewUserInputValidationErr("updateMask must not include uuid")
	}

	if _, ok := volumeUpdateMaskAllowedFields[trimmed]; ok {
		return trimmed, nil
	}

	return "", utilErrors.NewUserInputValidationErr("updateMask contains unsupported field: " + trimmed)
}

// applyVolumeUpdateMask returns req with only updateMask-allowed fields copied from the body.
func applyVolumeUpdateMask(req *vcmserver.VolumeUpdateV1, updateMask string) (*vcmserver.VolumeUpdateV1, error) {
	fields, err := parseVolumeUpdateMask(updateMask)
	if err != nil {
		return nil, err
	}

	masked := &vcmserver.VolumeUpdateV1{}

	if fields["sizeInBytes"] && req.SizeInBytes.IsSet() {
		masked.SizeInBytes = req.SizeInBytes
	}
	if fields["snapReserve"] && req.SnapReserve.IsSet() {
		masked.SnapReserve = req.SnapReserve
	}
	if fields["snapshotDirectory"] && req.SnapshotDirectory.IsSet() {
		masked.SnapshotDirectory = req.SnapshotDirectory
	}
	if fields["snapshotPolicy"] && req.SnapshotPolicy.IsSet() {
		masked.SnapshotPolicy = req.SnapshotPolicy
	}
	if fields["exportPolicy"] && req.ExportPolicy.IsSet() {
		masked.ExportPolicy = req.ExportPolicy
	}
	if fields["blockDevices"] && len(req.BlockDevices) > 0 {
		masked.BlockDevices = req.BlockDevices
	}
	if fields["shareName"] && req.ShareName.IsSet() {
		masked.ShareName = req.ShareName
	}
	if fields["unixPermissions"] && req.UnixPermissions.IsSet() {
		masked.UnixPermissions = req.UnixPermissions
	}

	return masked, nil
}
