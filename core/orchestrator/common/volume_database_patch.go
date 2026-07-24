package common

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"gorm.io/gorm"
)

// BuildVolumeDatabaseValueUpdates applies a partial UpdateVolumeDatabaseValueParams patch onto
// dbVolume (mutating dbVolume.VolumeAttributes in place, creating it if it was nil) and returns
// the GORM column-name update map for only the fields present in the patch. Callers persist the
// returned map via Storage.UpdateVolumeFields; fields omitted from the patch are absent from the
// returned map and are therefore left unchanged in the DB.
func BuildVolumeDatabaseValueUpdates(dbVolume *datamodel.Volume, patch *UpdateVolumeDatabaseValueParams) map[string]interface{} {
	updates := map[string]interface{}{}
	if patch == nil {
		return updates
	}

	if patch.State != nil {
		updates["state"] = *patch.State
	}
	if patch.StateDetails != nil {
		updates["state_details"] = *patch.StateDetails
	}
	if patch.DeletedAtSet {
		if patch.DeletedAt != nil {
			updates["deleted_at"] = &gorm.DeletedAt{Time: *patch.DeletedAt, Valid: true}
		} else {
			updates["deleted_at"] = nil
		}
	}

	if patch.VolumeAttributes != nil {
		if dbVolume.VolumeAttributes == nil {
			dbVolume.VolumeAttributes = &datamodel.VolumeAttributes{}
		}
		applyVolumeAttributesPatch(dbVolume.VolumeAttributes, patch.VolumeAttributes)
		updates["volume_attributes"] = dbVolume.VolumeAttributes
	}

	return updates
}

// applyVolumeAttributesPatch overwrites only the non-nil fields of patch onto attrs.
//
// For now only IsDataProtection is supported (see UpdateVolumeAttributesPatch doc comment).
func applyVolumeAttributesPatch(attrs *datamodel.VolumeAttributes, patch *UpdateVolumeAttributesPatch) {
	if patch.IsDataProtection != nil {
		attrs.IsDataProtection = *patch.IsDataProtection
	}
}
