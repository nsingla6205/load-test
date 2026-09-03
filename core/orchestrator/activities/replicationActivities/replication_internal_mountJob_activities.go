package replicationActivities

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
)

var (
	failedStates        = []string{datamodel.SnapmirrorRelationshipFailed, datamodel.SnapmirrorRelationshipAborted, datamodel.SnapmirrorRelationshipHardAborted}
	mountJobRetryWindow = time.Duration(env.GetInt("ONTAP_TRANSIENT_ERROR_RETRY_MINUTES", 90)) * time.Minute
)

type MountJobActivity struct {
	SE database.Storage
}

func (j *MountJobActivity) CheckMountJob(ctx context.Context, dbReplication *datamodel.VolumeReplication, node *models.Node, accountName string, checkMountStart time.Time) error {
	logger := util.GetLogger(ctx)
	provider, err := activitiesGetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	replication := convertToSnapmirrorGetParams(dbReplication, accountName)
	snapmirror, err := provider.GetVolumeReplication(replication)
	if err != nil {
		logger.Errorf("Failed to get replication details from Ontap for replication %s: %v", dbReplication.UUID, err)
		return utilErrors.NewNonRetryableErr(err.Error())
	}
	if strings.Contains(snapmirror.UnhealthyReason, "Scheduled update failed") || strings.Contains(snapmirror.UnhealthyReason, "Failed to create snapshot") {
		logger.Infof("Replication %s failed due to scheduled update failure or snapshot creation error. UnhealthyReason: %s", dbReplication.UUID, snapmirror.UnhealthyReason)
		if time.Since(checkMountStart) <= mountJobRetryWindow {
			return temporal.NewApplicationError("Retrying mount job due to scheduled update failure or snapshot creation error", "", nil)
		}
		return utilErrors.NewNonRetryableErr(snapmirror.UnhealthyReason)
	}
	if strings.Contains(snapmirror.UnhealthyReason, "Transfer aborted") && snapmirror.CurrentTransferType == "" {
		logger.Infof("Transfer aborted, No data transfer is in progress for replication %s", dbReplication.UUID)
		return nil
	}
	if snapmirror.MirrorState == datamodel.OntapSnapmirrored {
		logger.Infof("Status is snapmirrored. External_UUID: %s", replication.ExternalUUID)
		return nil
	} else {
		if slices.Contains(failedStates, snapmirror.RelationshipStatus) {
			logger.Infof("Replication %s is in a failed state: %s", dbReplication.UUID, snapmirror.RelationshipStatus)
			err = errors.New("replication is in a failed state: " + snapmirror.RelationshipStatus)
			return utilErrors.NewNonRetryableErr(err.Error())
		}

		logger.Info("Status is not snapmirrored yet.", "External_UUID", replication.ExternalUUID, "state", snapmirror.MirrorState)
		return errors.New("replication is not in snapmirrored state yet")
	}
}

func (j *MountJobActivity) GetReplicationFromOntap(ctx context.Context, dbReplication *datamodel.VolumeReplication, node *models.Node, accountName string) (*datamodel.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	provider, err := activitiesGetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	replicationParams := convertToSnapmirrorGetParams(dbReplication, accountName)
	ontapRep, err := provider.GetReplicationDetails(ctx, replicationParams)
	if err != nil {
		logger.Errorf("Failed to get replication details from Ontap for replication %s: %v", dbReplication.UUID, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrFailedToGetSnapmirrorDetailsFromOntapMountJob, err)
	}
	replication := addVsaModelReplicationDetailsToDatamodelReplication(ontapRep, dbReplication)
	return replication, nil
}

func (j *MountJobActivity) UpdateReplicationInDB(ctx context.Context, replication *datamodel.VolumeReplication, lunDetails []*vsa.LunResponse) error {
	logger := util.GetLogger(ctx)
	// Validate LUN details
	if (replication.Volume.VolumeAttributes.Protocols != nil && replication.Volume.VolumeAttributes.Protocols[0] == "ISCSI") && (lunDetails == nil || len(lunDetails) != 1) {
		originalErr := errors.New("zero or multiple LUNs found on source volume")
		replication.State = datamodel.LifeCycleStateError
		replication.StateDetails = originalErr.Error()

		// Update database with error state before returning
		if err := j.SE.UpdateVolumeReplication(ctx, replication); err != nil {
			logger.Errorf("Failed to update replication %s error state in DB: %v", replication.UUID, err)
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		}

		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrFailedToGetLunDetailsFromOntap, originalErr))
	}

	// Update database with transfer stats when validation passes
	err := j.SE.UpdateVolumeReplicationTransferStats(ctx, replication)
	if err != nil {
		logger.Errorf("Failed to update replication %s in DB: %v", replication.UUID, err)
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return nil
}

func (j *MountJobActivity) GetReplication(ctx context.Context, uuid string) (*datamodel.VolumeReplication, error) {
	se := j.SE

	replication, err := se.GetVolumeReplication(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return replication, nil
}

func (j *MountJobActivity) GetLunDetailsFromOntap(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) ([]*vsa.LunResponse, error) {
	logger := util.GetLogger(ctx)
	provider, err := activitiesGetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	lunParams := vsa.LunGetParams{
		SvmName:    replication.ReplicationAttributes.DestinationSvmName,
		VolumeName: replication.ReplicationAttributes.DestinationVolumeName,
	}
	lunDetails, err := provider.LunList(lunParams)
	if err != nil {
		logger.Errorf("Failed to get LUN details from Ontap %v", err)
		originalErr := err
		if customErr, ok := err.(*vsaerrors.CustomError); ok && customErr.OriginalErr != nil {
			originalErr = customErr.OriginalErr
			if utilErrors.IsNotFoundErr(originalErr) {
				logger.Infof("LUN not found for replication %s. Attempting to abort and break replication.", replication.UUID)
				return nil, nil
			}
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrFailedToGetLunDetailsFromOntap, originalErr)
	}

	return lunDetails, nil
}

func (j *MountJobActivity) AbortVolumeReplicationForMount(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) error {
	stopActivity := &InternalStopVolumeReplicationActivity{SE: j.SE}
	if _, err := stopActivity.AbortVolumeReplication(ctx, replication, node, true); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *MountJobActivity) BreakVolumeReplicationForMount(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) error {
	stopActivity := &InternalStopVolumeReplicationActivity{SE: j.SE}
	if _, err := stopActivity.BreakVolumeReplication(ctx, replication, node, true); err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	return nil
}

func (j *MountJobActivity) MountVolume(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) error {
	logger := util.GetLogger(ctx)
	provider, err := activitiesGetProviderByNode(ctx, node)
	if err != nil {
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	junctionPath := common.CreateJunctionPath(replication.Volume.VolumeAttributes.CreationToken)
	mountParams := vsa.MountVolumeParams{
		UUID:         replication.Volume.VolumeAttributes.ExternalUUID,
		JunctionPath: junctionPath,
	}
	_, err = provider.MountVolume(mountParams)
	if err != nil {
		logger.Errorf("Failed to mount volume %s to junction path %s: %v", replication.Volume.Name, junctionPath, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Debugf("Junction path updated successfully for volume %s in ONTAP", replication.Volume.Name)

	// If volume is SMB, create CIFS share for the junction path
	if replication.Volume.VolumeAttributes != nil &&
		replication.Volume.VolumeAttributes.Protocols != nil &&
		utils.IsSMBProtocols(replication.Volume.VolumeAttributes.Protocols) {
		logger.Infof("Volume %s is SMB, creating CIFS share for junction path %s", replication.Volume.Name, junctionPath)

		svmName := replication.ReplicationAttributes.DestinationSvmName
		var smbShareProperties []string
		if replication.Volume.VolumeAttributes.FileProperties != nil {
			smbShareProperties = replication.Volume.VolumeAttributes.FileProperties.SMBShareSettings
		}

		activeDirectoryActivity := active_directory_activities.ActiveDirectoryActivity{}
		err = activeDirectoryActivity.CreateJunctionPathForCifsShare(ctx, node, svmName, junctionPath, smbShareProperties)
		if err != nil {
			logger.Errorf("Failed to create CIFS share for volume %s: %v", replication.Volume.Name, err)
			return vsaerrors.WrapAsTemporalApplicationError(err)
		}

		logger.Infof("Successfully created CIFS share for volume %s", replication.Volume.Name)
	}

	return nil
}

func (j *MountJobActivity) UpdateVolumeDetailsInDB(ctx context.Context, replication *datamodel.VolumeReplication, lunDetails []*vsa.LunResponse) error {
	se := j.SE
	updates := make(map[string]interface{})
	if replication.Volume.VolumeAttributes.Protocols != nil && replication.Volume.VolumeAttributes.Protocols[0] == "ISCSI" {
		blockDevices := make([]datamodel.BlockDevice, 1)

		lunPath := strings.Split(lunDetails[0].Name, "/")
		lunName := lunPath[len(lunPath)-1]
		blockDevices[0].Name = lunName
		blockDevices[0].Size = lunDetails[0].Size
		blockDevices[0].LunUUID = lunDetails[0].ExternalUUID
		blockDevices[0].Identifier = lunDetails[0].SerialNumber
		blockDevices[0].OSType = strings.ToUpper(lunDetails[0].OSType)

		replication.Volume.VolumeAttributes.BlockDevices = &blockDevices
	}

	// Set mount to true
	if replication.Volume.VolumeAttributes != nil {
		replication.Volume.VolumeAttributes.Mounted = true
	}

	updates["volume_attributes"] = replication.Volume.VolumeAttributes
	err := se.UpdateVolumeFields(ctx, replication.ReplicationAttributes.DestinationVolumeUUID, updates)
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}
