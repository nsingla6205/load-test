package api

import (
	"fmt"
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
// Every field named in the mask must be set on the body.
func applyVolumeUpdateMask(req *vcmserver.VolumeUpdateV1, updateMask string) (*vcmserver.VolumeUpdateV1, error) {
	fields, err := parseVolumeUpdateMask(updateMask)
	if err != nil {
		return nil, err
	}

	masked := &vcmserver.VolumeUpdateV1{}
	for field := range fields {
		switch field {
		case "sizeInBytes":
			if !req.SizeInBytes.IsSet() {
				return nil, missingUpdateMaskField(field)
			}
			masked.SizeInBytes = req.SizeInBytes
		case "snapReserve":
			if !req.SnapReserve.IsSet() {
				return nil, missingUpdateMaskField(field)
			}
			masked.SnapReserve = req.SnapReserve
		case "snapshotDirectory":
			if !req.SnapshotDirectory.IsSet() {
				return nil, missingUpdateMaskField(field)
			}
			masked.SnapshotDirectory = req.SnapshotDirectory
		case "snapshotPolicy":
			if !req.SnapshotPolicy.IsSet() {
				return nil, missingUpdateMaskField(field)
			}
			masked.SnapshotPolicy = req.SnapshotPolicy
		case "exportPolicy":
			if !req.ExportPolicy.IsSet() {
				return nil, missingUpdateMaskField(field)
			}
			masked.ExportPolicy = req.ExportPolicy
		case "blockDevices":
			if len(req.BlockDevices) == 0 {
				return nil, missingUpdateMaskField(field)
			}
			masked.BlockDevices = req.BlockDevices
		case "shareName":
			if !req.ShareName.IsSet() {
				return nil, missingUpdateMaskField(field)
			}
			masked.ShareName = req.ShareName
		case "unixPermissions":
			if !req.UnixPermissions.IsSet() {
				return nil, missingUpdateMaskField(field)
			}
			masked.UnixPermissions = req.UnixPermissions
		}
	}

	return masked, nil
}

func missingUpdateMaskField(field string) error {
	return utilErrors.NewUserInputValidationErr(fmt.Sprintf("updateMask names %q but the field is missing from the request body", field))
}
