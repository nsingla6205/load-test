package activities

import (
	"context"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

type SnapshotDeleteActivity struct {
	SE database.Storage
}

func (a *SnapshotDeleteActivity) DeleteSnapshotInONTAP(ctx context.Context, snapshot *datamodel.Snapshot, node *models.Node) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Initializing snapshot deletion")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Deleting snapshot from ONTAP")
	err = provider.DeleteSnapshot(snapshot.GetExternalUUID(), snapshot.Volume.VolumeAttributes.ExternalUUID)
	if err != nil {
		if strings.Contains(err.Error(), "snapshot is in use") {
			return vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Snapshot deleted successfully from ONTAP")
	logger.Infof("Snapshot %s deleted successfully from the vsa cluster", snapshot.Name)
	return nil
}

func (a *SnapshotDeleteActivity) DeleteSnapshot(ctx context.Context, snapshot *datamodel.Snapshot) error {
	logger := util.GetLogger(ctx)
	se := a.SE
	activity.RecordHeartbeat(ctx, "Marking snapshot as deleted in database")

	_, err := se.DeleteSnapshot(ctx, snapshot.UUID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Infof("Snapshot %s not found, assuming it is already deleted", snapshot.Name)
			return nil
		}
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Snapshot marked as deleted successfully")
	logger.Debugf("Snapshot:%s marked deleted successfully in the db", snapshot.Name)

	return nil
}

func (a *SnapshotDeleteActivity) UpdateDeleteSnapshotDetails(ctx context.Context, snapshot *datamodel.Snapshot) error {
	se := a.SE
	activity.RecordHeartbeat(ctx, "Updating snapshot details after deletion")
	_, err := se.UpdateSnapshot(ctx, snapshot)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Snapshot details updated successfully")
	return nil
}
