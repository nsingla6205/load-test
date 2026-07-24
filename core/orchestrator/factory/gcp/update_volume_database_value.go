package gcp

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
)

// UpdateVolumeDatabaseValue partially patches raw volume DB columns (state, stateDetails,
// deletedAt, volumeAttributes) for the given volume UUID. Only fields present in patch are
// written; everything else on the row is left unchanged.
func (o *GCPOrchestrator) UpdateVolumeDatabaseValue(ctx context.Context, volumeUUID string, patch *common.UpdateVolumeDatabaseValueParams) (*models.Volume, error) {
	dbVolume, err := o.storage.GetVolume(ctx, volumeUUID)
	if err != nil {
		return nil, err
	}

	updates := common.BuildVolumeDatabaseValueUpdates(dbVolume, patch)
	if len(updates) > 0 {
		if err := o.storage.UpdateVolumeFields(ctx, volumeUUID, updates); err != nil {
			return nil, err
		}
	}

	updatedVolume, err := o.storage.GetVolume(ctx, volumeUUID)
	if err != nil {
		return nil, err
	}
	return _convertDatastoreVolumeToModel(updatedVolume, nil), nil
}
