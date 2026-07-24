package oci

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
)

// UpdateVolumeDatabaseValue partially patches raw volume DB columns (state, stateDetails,
// deletedAt, volumeAttributes) for the given volume UUID. Only fields present in patch are
// written; everything else on the row is left unchanged.
func (o *OCIOrchestrator) UpdateVolumeDatabaseValue(ctx context.Context, volumeUUID string, patch *commonparams.UpdateVolumeDatabaseValueParams) (*models.Volume, error) {
	dbVolume, err := o.storage.GetVolume(ctx, volumeUUID)
	if err != nil {
		return nil, err
	}

	updates := commonparams.BuildVolumeDatabaseValueUpdates(dbVolume, patch)
	if len(updates) > 0 {
		if err := o.storage.UpdateVolumeFields(ctx, volumeUUID, updates); err != nil {
			return nil, err
		}
	}

	updatedVolume, err := o.storage.GetVolume(ctx, volumeUUID)
	if err != nil {
		return nil, err
	}
	return convertOCIDataStoreVolumeToModel(updatedVolume), nil
}

func convertOCIDataStoreVolumeToModel(volume *datamodel.Volume) *models.Volume {
	if volume == nil {
		return nil
	}

	res := &models.Volume{
		BaseModel: models.BaseModel{
			UUID:      volume.UUID,
			CreatedAt: volume.CreatedAt,
			UpdatedAt: volume.UpdatedAt,
			DeletedAt: utils.DeletedAtOrNil(volume.DeletedAt),
		},
		LifeCycleState:        volume.State,
		LifeCycleStateDetails: volume.StateDetails,
	}

	if volume.VolumeAttributes != nil {
		res.IsDataProtection = volume.VolumeAttributes.IsDataProtection
	}

	return res
}
