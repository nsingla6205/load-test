package activities

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"gorm.io/gorm"
)

const SnapshotTypeBackup = "backup"

type SnapshotCreateActivity struct {
	SE database.Storage
}

func (a *SnapshotCreateActivity) CreateSnapshotInONTAP(ctx context.Context, snapshot *datamodel.Snapshot, node *models.Node) (*vsa.SnapshotProviderResponse, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "Initializing snapshot creation")
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Creating snapshot in ONTAP")
	res, err := provider.CreateSnapshot(vsa.CreateSnapshotParams{
		VolumeUUID: snapshot.Volume.VolumeAttributes.ExternalUUID,
		Name:       snapshot.Name,
		Comment:    snapshot.Description,
	})
	if err != nil {
		return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Snapshot created successfully")
	logger.Debug("CreateSnapshotInONTAP: snapshot created successfully")

	return res, nil
}

func (a *SnapshotCreateActivity) UpdateSnapshotDetails(ctx context.Context, dbSnapshot *datamodel.Snapshot, snapshotCreateResponse *vsa.SnapshotProviderResponse) error {
	se := a.SE
	activity.RecordHeartbeat(ctx, "Updating snapshot details")
	if snapshotCreateResponse == nil {
		dbSnapshot.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
		dbSnapshot.State = datamodel.LifeCycleStateError
		dbSnapshot.StateDetails = datamodel.LifeCycleStateCreationErrorDetails
	} else {
		dbSnapshot.State = datamodel.LifeCycleStateREADY
		dbSnapshot.StateDetails = datamodel.LifeCycleStateAvailableDetails
		dbSnapshot.SetExternalUUID(snapshotCreateResponse.ExternalUUID)
		dbSnapshot.SnapshotAttributes.SizeInBytes = snapshotCreateResponse.SizeInBytes
		dbSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes = snapshotCreateResponse.LogicalSizeInBytes
	}
	activity.RecordHeartbeat(ctx, "Persisting snapshot state to database")
	_, err := se.UpdateSnapshot(ctx, dbSnapshot)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	activity.RecordHeartbeat(ctx, "Snapshot details updated successfully")
	return nil
}
