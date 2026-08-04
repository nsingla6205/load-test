package replicationActivities

import (
	"context"
	"fmt"
	"time"

	googleproxyclient "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/google-proxy-client"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/replication"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type UpdateVolumeReplicationAttributesActivity struct {
	SE database.Storage
}

// GetSnapmirrorDetailsFromOntap gets snapmirror details from ONTAP using source and destination paths
func (a *UpdateVolumeReplicationAttributesActivity) GetSnapmirrorDetailsFromOntap(ctx context.Context, result *replication.UpdateVolumeReplicationAttributesResult) (*replication.UpdateVolumeReplicationAttributesResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debug("Getting snapmirror details from ONTAP using source and destination paths")

	// Get the volume replication from the database first
	se := a.SE
	volumeReplicationId := result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationId

	safeRecordHeartbeat(ctx, fmt.Sprintf("getting volume replication %s from database", volumeReplicationId))
	volReplication, err := se.GetVolumeReplication(ctx, volumeReplicationId)
	if err != nil {
		logger.Error("Failed to get volume replication from database", "error", err, "replicationId", volumeReplicationId)
		return nil, errors.NewVCPError(errors.ErrDatabaseDataReadError, err)
	}

	result.DbVolReplication = volReplication

	// Get source cluster node information to connect to ONTAP
	safeRecordHeartbeat(ctx, fmt.Sprintf("getting nodes for source pool %d from database", volReplication.Volume.PoolID))
	nodes, err := se.GetNodesByPoolID(ctx, volReplication.Volume.PoolID)
	if err != nil || len(nodes) == 0 {
		logger.Error("Failed to get nodes for source pool", "error", err, "poolId", volReplication.Volume.PoolID)
		return nil, errors.NewVCPError(errors.ErrVSAClusterNodeNotFound, err)
	}

	node := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
		Nodes:            nodes,
		DeploymentName:   volReplication.Volume.Pool.DeploymentName,
		OntapCredentials: volReplication.Volume.Pool.PoolCredentials,
	})

	// Get provider for the source node
	safeRecordHeartbeat(ctx, fmt.Sprintf("getting ONTAP provider for source pool %d", volReplication.Volume.PoolID))
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		logger.Error("Failed to get provider for source node", "error", err)
		return nil, errors.NewVCPError(errors.ErrVSAClusterCreateError, err)
	}

	// Create VSA replication parameters using source and destination paths
	vsaReplicationParams := &vsa.VolumeReplication{
		SourceSVMName:         volReplication.ReplicationAttributes.DestinationSvmName,
		SourceVolumeName:      volReplication.ReplicationAttributes.DestinationVolumeName,
		DestinationSVMName:    volReplication.ReplicationAttributes.SourceSvmName,
		DestinationVolumeName: volReplication.ReplicationAttributes.SourceVolumeName,
	}

	// Make call to ONTAP to fetch replication details using GetVolumeReplicationFromSrcAndDstPath
	safeRecordHeartbeat(ctx, fmt.Sprintf("fetching snapmirror details from ONTAP for volume replication %s", volumeReplicationId))
	replicationDetails, err := provider.GetVolumeReplicationFromSrcAndDstPath(vsaReplicationParams)
	if err != nil {
		logger.Error("Failed to get replication details from ONTAP using src and dst paths")
		return nil, errors.NewVCPError(errors.ErrOntapRestAPIError, err)
	}

	logger.Info("Successfully fetched snapmirror details from ONTAP cluster")

	// Store the replication details in the result for use by subsequent activities
	result.ReplicationDetails = replicationDetails

	return result, nil
}

// UpdateReplicationAttributes updates replication table entries with snapmirror details and attribute values from params
func (a *UpdateVolumeReplicationAttributesActivity) UpdateDstVolumeReplication(ctx context.Context, result *replication.UpdateVolumeReplicationAttributesResult) (*replication.UpdateVolumeReplicationAttributesResult, error) {
	logger := util.GetLogger(ctx)
	logger.Debug("Updating replication table entries with snapmirror details and attribute values")

	se := a.SE

	// Get the VolumeReplicationInternal object from parameters (now properly typed)
	replicationInternal := result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationInternal
	if replicationInternal == nil {
		logger.Error("VolumeReplicationInternal is nil")
		return nil, errors.NewVCPError(errors.ErrInputValidationError, nil)
	}

	// Update database replication with snapmirror details from ONTAP
	safeRecordHeartbeat(ctx, fmt.Sprintf("getting volume replication %s from database", result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationId))
	dbReplication, err := se.GetVolumeReplication(ctx, result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationId)
	if err != nil {
		logger.Error("Failed to get volume replication from database", "error", err, "replicationId", result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationId)
		return nil, errors.NewVCPError(errors.ErrDatabaseDataReadError, err)
	}

	dbReplication = createReplicationObjectForUpdate(dbReplication, result, replicationInternal)

	logger.Info("Updating volume replication in database")
	// Update the replication in database
	safeRecordHeartbeat(ctx, fmt.Sprintf("updating volume replication %s in database", result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationId))
	err = se.UpdateVolumeReplication(ctx, dbReplication)
	if err != nil {
		logger.Error("Failed to update volume replication in database", "error", err)
		return nil, errors.NewVCPError(errors.ErrDatabaseDataUpdateError, err)
	}

	logger.Info("Successfully updated replication table entries",
		"volumeReplicationId", result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationId)

	return result, nil
}

func createReplicationObjectForUpdate(dbReplication *datamodel.VolumeReplication, result *replication.UpdateVolumeReplicationAttributesResult, replicationInternal *gcpserver.VolumeReplicationInternalV1beta) *datamodel.VolumeReplication {
	// Update with snapmirror details from ONTAP
	if result.ReplicationDetails != nil {
		if result.ReplicationDetails.MirrorState != "" {
			dbReplication.MirrorState = &result.ReplicationDetails.MirrorState
		}

		if result.ReplicationDetails.RelationshipStatus != "" {
			dbReplication.RelationshipStatus = &result.ReplicationDetails.RelationshipStatus
		}

		if result.ReplicationDetails.ReplicationSchedule != "" {
			dbReplication.ReplicationAttributes.ReplicationSchedule = result.ReplicationDetails.ReplicationSchedule
		}

		dbReplication.LagTime = result.ReplicationDetails.LagTime
		dbReplication.TotalProgress = result.ReplicationDetails.TotalProgress
		dbReplication.TotalTransferBytes = result.ReplicationDetails.TotalTransferBytes
		dbReplication.TotalTransferTimeSecs = result.ReplicationDetails.TotalTransferTimeSecs
		dbReplication.LastTransferSize = result.ReplicationDetails.LastTransferSize
		dbReplication.LastTransferDuration = result.ReplicationDetails.LastTransferDuration
		dbReplication.LastTransferError = result.ReplicationDetails.LastTransferError
		dbReplication.LastTransferEndTime = result.ReplicationDetails.LastTransferEndTime
		dbReplication.ProgressLastUpdated = result.ReplicationDetails.ProgressLastUpdated
		dbReplication.Healthy = result.ReplicationDetails.Healthy
	}

	// Update with attribute values received in params
	if dbReplication.ReplicationAttributes == nil {
		dbReplication.ReplicationAttributes = &datamodel.ReplicationDetails{}
	}

	// Update endpoint type
	dbReplication.ReplicationAttributes.EndpointType = string(replicationInternal.EndpointType)

	// Update source attributes
	dbReplication.ReplicationAttributes.SourceHostName = replicationInternal.SourceHostName
	dbReplication.ReplicationAttributes.SourceSvmName = replicationInternal.SourceServerName
	dbReplication.ReplicationAttributes.SourceVolumeName = replicationInternal.SourceVolumeName
	dbReplication.ReplicationAttributes.SourceLocation = result.DbVolReplication.ReplicationAttributes.DestinationLocation
	dbReplication.ReplicationAttributes.SourceReplicationUUID = result.DbVolReplication.ReplicationAttributes.DestinationReplicationUUID

	if replicationInternal.SourceVolumeUuid.IsSet() {
		dbReplication.ReplicationAttributes.SourceVolumeUUID = replicationInternal.SourceVolumeUuid.Value
	}
	if replicationInternal.SourcePoolUuid.IsSet() {
		dbReplication.ReplicationAttributes.SourcePoolUUID = replicationInternal.SourcePoolUuid.Value
	}

	// Update destination attributes
	dbReplication.ReplicationAttributes.DestinationHostName = replicationInternal.DestinationHostName
	dbReplication.ReplicationAttributes.DestinationSvmName = replicationInternal.DestinationServerName
	dbReplication.ReplicationAttributes.DestinationVolumeName = replicationInternal.DestinationVolumeName
	dbReplication.ReplicationAttributes.DestinationLocation = result.DbVolReplication.ReplicationAttributes.SourceLocation
	dbReplication.ReplicationAttributes.DestinationReplicationUUID = result.DbVolReplication.ReplicationAttributes.SourceReplicationUUID

	if replicationInternal.DestinationVolumeUuid.IsSet() {
		dbReplication.ReplicationAttributes.DestinationVolumeUUID = replicationInternal.DestinationVolumeUuid.Value
	}
	if replicationInternal.DestinationPoolUuid.IsSet() {
		dbReplication.ReplicationAttributes.DestinationPoolUUID = replicationInternal.DestinationPoolUuid.Value
	}

	dbReplication.ReplicationAttributes.ExternalUUID = result.ReplicationDetails.RelationshipID

	dbReplication.State = datamodel.LifeCycleStateAvailable
	dbReplication.StateDetails = datamodel.LifeCycleStateAvailableDetails

	return dbReplication
}

func (a *UpdateVolumeReplicationAttributesActivity) UpdateVolumeTypeActivity(ctx context.Context, result *replication.UpdateVolumeReplicationAttributesResult) error {
	se := a.SE
	logger := util.GetLogger(ctx)
	logger.Infof("Updating volume type for volume %s", result.DbVolReplication.Volume.Name)

	safeRecordHeartbeat(ctx, fmt.Sprintf("getting volume %s from database", result.DbVolReplication.Volume.UUID))
	volume, err := se.GetVolume(ctx, result.DbVolReplication.Volume.UUID)
	if err != nil {
		return err
	}

	updates := make(map[string]interface{})
	if volume.VolumeAttributes != nil && result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationInternal.EndpointType == "src" {
		volume.VolumeAttributes.IsDataProtection = false
	} else {
		volume.VolumeAttributes.IsDataProtection = true
	}
	updates["volume_attributes"] = volume.VolumeAttributes
	safeRecordHeartbeat(ctx, fmt.Sprintf("updating volume %s fields in database", volume.UUID))
	err = se.UpdateVolumeFields(ctx, volume.UUID, updates)
	return err
}

func (a *UpdateVolumeReplicationAttributesActivity) UpdateSrcVolumeReplication(ctx context.Context, result *replication.UpdateVolumeReplicationAttributesResult) (*replication.UpdateVolumeReplicationAttributesResult, error) {
	logger := util.GetLogger(ctx)

	// Get the volume replication from the database first
	se := a.SE
	volumeReplicationId := result.Event.UpdateVolumeReplicationAttributesParams.VolumeReplicationId

	safeRecordHeartbeat(ctx, fmt.Sprintf("getting volume replication %s from database", volumeReplicationId))
	volReplication, err := se.GetVolumeReplication(ctx, volumeReplicationId)
	if err != nil {
		logger.Error("Failed to get volume replication from database", "error", err, "replicationId", volumeReplicationId)
		return nil, errors.NewVCPError(errors.ErrDatabaseDataReadError, err)
	}

	result.DbVolReplication = volReplication
	logger.Infof("Updating volume replication %s details in the database after reversal", volReplication.Name)

	replicationAttributes := volReplication.ReplicationAttributes
	// Swap all source and destination details after reversal
	if replicationAttributes != nil {
		oldReplicationAttributes := *replicationAttributes

		// Swap source and destination fields
		replicationAttributes.SourcePoolUUID = oldReplicationAttributes.DestinationPoolUUID
		replicationAttributes.SourceVolumeUUID = oldReplicationAttributes.DestinationVolumeUUID
		replicationAttributes.SourceLocation = oldReplicationAttributes.DestinationLocation
		replicationAttributes.SourceHostName = oldReplicationAttributes.DestinationHostName
		replicationAttributes.SourceReplicationUUID = oldReplicationAttributes.DestinationReplicationUUID
		replicationAttributes.SourceSvmName = oldReplicationAttributes.DestinationSvmName
		replicationAttributes.SourceVolumeName = oldReplicationAttributes.DestinationVolumeName

		replicationAttributes.DestinationPoolUUID = oldReplicationAttributes.SourcePoolUUID
		replicationAttributes.DestinationVolumeUUID = oldReplicationAttributes.SourceVolumeUUID
		replicationAttributes.DestinationLocation = oldReplicationAttributes.SourceLocation
		replicationAttributes.DestinationHostName = oldReplicationAttributes.SourceHostName
		replicationAttributes.DestinationReplicationUUID = oldReplicationAttributes.SourceReplicationUUID
		replicationAttributes.DestinationSvmName = oldReplicationAttributes.SourceSvmName
		replicationAttributes.DestinationVolumeName = oldReplicationAttributes.SourceVolumeName

		replicationAttributes.EndpointType = string(googleproxyclient.VolumeReplicationInternalV1betaEndpointTypeSrc)
		replicationAttributes.ExternalUUID = ""
	}

	updates := make(map[string]interface{})
	updates["mirror_state"] = nillable.GetStringPtr("")
	updates["total_transfer_bytes"] = 0
	updates["total_transfer_time_secs"] = 0
	updates["last_transfer_size"] = int64(0)
	updates["last_transfer_error"] = ""
	updates["last_transfer_duration"] = 0
	updates["last_transfer_end_time"] = nil
	updates["lag_time"] = 0
	updates["last_updated_from_ontap"] = time.Now()
	updates["progress_last_updated"] = time.Now()

	updates["replication_attributes"] = volReplication.ReplicationAttributes
	updates["state"] = datamodel.LifeCycleStateREADY
	updates["state_details"] = datamodel.LifeCycleStateAvailableDetails

	// Update the volume replication in the database
	safeRecordHeartbeat(ctx, fmt.Sprintf("updating volume replication %s fields in database", volReplication.UUID))
	err = a.SE.UpdateVolumeReplicationFields(ctx, volReplication.UUID, updates)
	if err != nil {
		logger.Errorf("Failed to update volume replication %s in the database: %v", volReplication.Name, err)
		return nil, err
	}

	logger.Debugf("Volume Replication %s updated successfully in the database", volReplication.Name)
	return result, nil
}
