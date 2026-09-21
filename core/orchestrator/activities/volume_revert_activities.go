package activities

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/hydrationActivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/scheduler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

type VolumeRevertActivity struct {
	SE        database.Storage
	Scheduler *scheduler.TemporalScheduler
}

func (a VolumeRevertActivity) RevertVolume(ctx context.Context, volume *datamodel.Volume, snapshot *datamodel.Snapshot, node *models.Node, params vsa.RevertVolumeParams) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Initializing volume revert")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	revertVolumeParams := vsa.RevertVolumeParams{
		VolumeID:        volume.VolumeAttributes.ExternalUUID,
		SnapshotID:      snapshot.GetExternalUUID(),
		SnapshotName:    snapshot.Name,
		SvmName:         volume.Svm.Name,
		PreRevertVolume: volume,
	}

	activity.RecordHeartbeat(ctx, "Reverting volume in ONTAP")
	err = provider.RevertVolume(revertVolumeParams)
	if err != nil {
		logger.Errorf("Failed to revert volume %s in ontap: %v", params.VolumeID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	logger.Debugf("Volume %s Reverted successfully in ontap", params.VolumeID)
	activity.RecordHeartbeat(ctx, "Volume reverted successfully in ONTAP")

	se := a.SE
	activity.RecordHeartbeat(ctx, "Updating reverted volume in database")
	snapshots, err := se.RevertedVolume(ctx, volume, snapshot)
	if err != nil {
		logger.Errorf("Failed to update the reverted volume %s in DB: %v", params.VolumeID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Hydrate snapshots to CCFE after volume revert
	if len(snapshots) > 0 {
		activity.RecordHeartbeat(ctx, "Hydrating snapshots to CCFE")
		hydrateErr := hydrationActivities.HydrateBatchSnapshotstoCCFE(ctx, nil, snapshots)
		if hydrateErr != nil {
			logger.Errorf("Failed to hydrate snapshots to CCFE after volume revert: %v, snapshots: %+v", hydrateErr, snapshots)
		}
	}

	activity.RecordHeartbeat(ctx, "Volume revert completed successfully")
	return nil
}
