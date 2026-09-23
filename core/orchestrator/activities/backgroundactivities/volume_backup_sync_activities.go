package backgroundactivities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

var getGcbdrBackpropLookback = func() time.Duration {
	hours := env.GetInt("GCBDR_GC_BACKPROP_LOOKBACK_HOURS", 24)
	return time.Duration(hours) * time.Hour
}

// VolumeBackupSyncActivity handles activities for syncing volume and backup logical sizes
type VolumeBackupSyncActivity struct {
	SE database.Storage
}

// GetVolumeLatestBackupMapActivity retrieves volumes with their associated latest backups
func (a *VolumeBackupSyncActivity) GetVolumeLatestBackupMapActivity(ctx context.Context) (map[int64]*datamodel.VolumeLatestBackup, error) {
	logger := util.GetLogger(ctx)

	safeRecordHeartbeat(ctx, "getting volume latest backup map from database")
	volumeBackupMap, err := a.SE.GetVolumeLatestBackupMap(ctx)
	if err != nil {
		logger.Errorf("Failed to get volume latest backup map: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Retrieved %d volume-backup mappings", len(volumeBackupMap))
	return volumeBackupMap, nil
}

// volumeBackupName returns a display name for logging, regardless of whether the entry is
// a regular volume or an expert mode volume.
func volumeBackupName(volumeBackup *datamodel.VolumeLatestBackup) string {
	if volumeBackup.Volume != nil {
		return volumeBackup.Volume.Name
	}
	if volumeBackup.ExpertModeVolume != nil {
		return volumeBackup.ExpertModeVolume.Name
	}
	return "unknown"
}

// getObjectStoreEndpointInfo gets object store endpoint info using the provided node and UUIDs.
// It supports both regular volumes (volumeBackup.Volume) and expert mode volumes
// (volumeBackup.ExpertModeVolume).
func (a *VolumeBackupSyncActivity) getObjectStoreEndpointInfo(ctx context.Context, objectStoreUUID, endpointUUID string, volumeBackup *datamodel.VolumeLatestBackup) (*vsa.SmObjectStoreEndpointt, error) {
	var poolID int64
	var pool *datamodel.Pool
	if volumeBackup.ExpertModeVolume != nil {
		poolID = volumeBackup.ExpertModeVolume.PoolID
		pool = volumeBackup.ExpertModeVolume.Pool
	} else {
		poolID = volumeBackup.Volume.PoolID
		pool = volumeBackup.Volume.Pool
	}

	safeRecordHeartbeat(ctx, fmt.Sprintf("getting nodes for pool %d from database", poolID))
	dbNodes, err := a.SE.GetNodesByPoolID(ctx, poolID)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if len(dbNodes) == 0 {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrUnexpectedNodeCountForPool, errors.New("Node not found for the pool")))
	}

	// Prepare node provider input
	if pool == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool not found for pool %d", poolID)))
	}
	if pool.PoolCredentials == nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("pool credentials not found for pool %d", poolID)))
	}
	nodeProviderInput := vsa.NodeProviderInput{
		Nodes:            dbNodes,
		DeploymentName:   pool.DeploymentName,
		OntapCredentials: pool.PoolCredentials,
	}

	node := vsa.CreateNodeForProvider(nodeProviderInput)

	// Get the provider and fetch object store endpoint info
	safeRecordHeartbeat(ctx, fmt.Sprintf("getting ONTAP provider for pool %d", poolID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, err
	}

	safeRecordHeartbeat(ctx, fmt.Sprintf("getting object store endpoint info from ONTAP (object store %s, endpoint %s)", objectStoreUUID, endpointUUID))
	objStoreEndpointInfo, err := provider.ObjectStoreEndpointInfoGet(objectStoreUUID, endpointUUID)
	if err != nil {
		return nil, err
	}

	return objStoreEndpointInfo, nil
}

// GetObjectStoreEndpointInfoActivity gets object store endpoint info for a volume
func (a *VolumeBackupSyncActivity) GetObjectStoreEndpointInfoActivity(ctx context.Context, volumeBackup *datamodel.VolumeLatestBackup) (*vsa.SmObjectStoreEndpointt, error) {
	logger := util.GetLogger(ctx)

	if volumeBackup.LatestBackup == nil {
		logger.Infof("No latest backup found for volume %s, skipping", volumeBackupName(volumeBackup))
		return nil, nil
	}

	// Check if backup has required attributes
	if volumeBackup.LatestBackup.Attributes == nil ||
		volumeBackup.LatestBackup.Attributes.ObjectStoreUUID == "" ||
		volumeBackup.LatestBackup.Attributes.EndpointUUID == "" {
		logger.Infof("Backup %s missing required attributes (ObjectStoreUUID or EndpointUUID), skipping", volumeBackup.LatestBackup.Name)
		return nil, nil
	}

	// Get object store endpoint info to fetch logical size
	objStoreEndpointInfo, err := a.getObjectStoreEndpointInfo(ctx,
		volumeBackup.LatestBackup.Attributes.ObjectStoreUUID,
		volumeBackup.LatestBackup.Attributes.EndpointUUID, volumeBackup)
	if err != nil {
		logger.Errorf("Failed to get object store endpoint info for backup %s: %v", volumeBackup.LatestBackup.Name, err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	return objStoreEndpointInfo, nil
}

// UpdateBackupAndVolumeActivity updates backup and volume with logical size information
func (a *VolumeBackupSyncActivity) UpdateBackupAndVolumeActivity(ctx context.Context, volumeBackup *datamodel.VolumeLatestBackup, logicalSize int64) error {
	logger := util.GetLogger(ctx)

	if volumeBackup.LatestBackup == nil {
		logger.Infof("No latest backup found for volume %s, skipping update", volumeBackupName(volumeBackup))
		return nil
	}

	// Update backup with latest logical size using UpdateBackupFields
	backupUpdates := map[string]interface{}{
		"latest_logical_backup_size": logicalSize,
	}
	safeRecordHeartbeat(ctx, fmt.Sprintf("updating backup %s logical size in database", volumeBackup.LatestBackup.UUID))
	err := a.SE.UpdateBackupFields(ctx, volumeBackup.LatestBackup.UUID, backupUpdates)
	if err != nil {
		logger.Errorf("Failed to update backup %s with logical size: %v", volumeBackup.LatestBackup.Name, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	// Update volume's latest logical backup size (sum across all the volume's chains, not just
	// this one) via RecalculateAndUpdateVolumeBackupChainBytesActivity, which handles both regular
	// and expert mode volumes. Branch on which volume pointer is populated rather than on the
	// backup attribute flag so that a missing or inconsistent pointer produces a clear error
	// instead of a nil panic.
	backupActivity := activities.BackupActivity{SE: a.SE}
	switch {
	case volumeBackup.ExpertModeVolume != nil:
		safeRecordHeartbeat(ctx, fmt.Sprintf("recalculating backup chain bytes for expert mode volume %s in database", volumeBackup.ExpertModeVolume.ExternalUUID))
		if err = backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity(ctx, volumeBackup.ExpertModeVolume.ExternalUUID); err != nil {
			logger.Errorf("Failed to recalculate backup chain bytes for expert mode volume %s: %v", volumeBackupName(volumeBackup), err)
			return err
		}
	case volumeBackup.Volume != nil:
		safeRecordHeartbeat(ctx, fmt.Sprintf("recalculating backup chain bytes for volume %s in database", volumeBackup.Volume.UUID))
		if err = backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity(ctx, volumeBackup.Volume.UUID); err != nil {
			logger.Errorf("Failed to recalculate backup chain bytes for volume %s: %v", volumeBackup.Volume.Name, err)
			return err
		}
	default:
		return vsaerrors.WrapAsTemporalApplicationError(
			fmt.Errorf("volume backup entry (backup %s) has neither Volume nor ExpertModeVolume populated", volumeBackup.LatestBackup.Name),
		)
	}

	// Derive the resource_uuid for the chain history row.
	// - Regular volumes:      volume.UUID
	// - Expert mode volumes:  expertModeVolume.ExternalUUID (= resource_uuid in backup_chain_histories)
	var chainHistoryUUID string
	if volumeBackup.ExpertModeVolume != nil {
		chainHistoryUUID = volumeBackup.ExpertModeVolume.ExternalUUID
	} else {
		chainHistoryUUID = volumeBackup.Volume.UUID
	}
	endpointUUID := ""
	if volumeBackup.LatestBackup.Attributes != nil {
		endpointUUID = volumeBackup.LatestBackup.Attributes.EndpointUUID
	}
	safeRecordHeartbeat(ctx, fmt.Sprintf("updating backup chain history for resource %s endpoint %s in database", chainHistoryUUID, endpointUUID))
	err = a.SE.UpdateBackupChainHistory(ctx, chainHistoryUUID, endpointUUID, logicalSize)
	if err != nil {
		logger.Warnf("Ledger: Failed to update backup chain history for volume %s endpoint %s: %v", volumeBackupName(volumeBackup), endpointUUID, err)
		// Don't fail the entire operation if history update fails
	}

	logger.Infof("Ledger: Successfully updated logical size %d for volume %s and backup %s",
		logicalSize, volumeBackupName(volumeBackup), volumeBackup.LatestBackup.Name)

	return nil
}

func (a *VolumeBackupSyncActivity) BackPropagateGcbdrBackupChainSizesActivity(ctx context.Context, volumeBackup *datamodel.VolumeLatestBackup, newLogicalSize int64) error {
	logger := util.GetLogger(ctx)

	if volumeBackup == nil || volumeBackup.LatestBackup == nil {
		return nil
	}
	latest := volumeBackup.LatestBackup

	// Gate to GCBDR/CrossProject vaults only.
	if latest.BackupVault == nil || latest.BackupVault.ServiceType != datamodel.ServiceTypeCrossProject {
		return nil
	}

	// Only a drop (delta > 0) indicates a garbage-collected deletion to back-propagate.
	delta := latest.LatestLogicalBackupSize - newLogicalSize
	if delta <= 0 {
		return nil
	}

	if latest.Attributes == nil || latest.Attributes.EndpointUUID == "" {
		logger.Infof("Backup %s missing endpoint_uuid, skipping GCBDR back-propagation", latest.Name)
		return nil
	}
	volumeUUID := latest.VolumeUUID
	endpointUUID := latest.Attributes.EndpointUUID

	// Identify the pivot: the most-recently soft-deleted backup for this endpoint within the
	// lookback window. Without a recent deletion we cannot attribute the drop, so we skip.
	since := time.Now().Add(-getGcbdrBackpropLookback())
	safeRecordHeartbeat(ctx, fmt.Sprintf("looking up most recently deleted backup for volume %s endpoint %s in database", volumeUUID, endpointUUID))
	pivot, err := a.SE.GetMostRecentlyDeletedBackupForEndpoint(ctx, volumeUUID, endpointUUID, since)
	if err != nil {
		logger.Errorf("Failed to look up deleted pivot backup for volume %s endpoint %s: %v", volumeUUID, endpointUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if pivot == nil {
		logger.Infof("No recently deleted backup found for volume %s endpoint %s within lookback; skipping GCBDR back-propagation", volumeUUID, endpointUUID)
		return nil
	}

	safeRecordHeartbeat(ctx, fmt.Sprintf("decrementing intermediate backup chain sizes for volume %s endpoint %s in database", volumeUUID, endpointUUID))
	updated, err := a.SE.DecrementIntermediateBackupChainSizes(ctx, volumeUUID, endpointUUID, pivot.ID, latest.ID, delta)
	if err != nil {
		logger.Errorf("Failed to back-propagate chain size for volume %s endpoint %s: %v", volumeUUID, endpointUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("GCBDR back-propagation: reduced %d intermediate backup(s) by %d for volume %s endpoint %s (pivot backup id %d, latest backup id %d)",
		updated, delta, volumeUUID, endpointUUID, pivot.ID, latest.ID)
	return nil
}
