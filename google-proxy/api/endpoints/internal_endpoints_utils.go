package api

import (
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

var convertToVolumeReplicationsInternalV1Beta = _convertToVolumeReplicationsInternalV1Beta

func mapReplicationStateToInternalLifeCycleState(state string) gcpgenserver.VolumeReplicationInternalV1betaLifeCycleState {
	switch state {
	case datamodel.LifeCycleStateCreating:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateCreating
	case datamodel.LifeCycleStateAvailable:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateAvailable
	case datamodel.LifeCycleStateDeleting:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateDeleting
	case datamodel.LifeCycleStateDeleted:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateDeleted
	case datamodel.LifeCycleStateError:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateError
	case datamodel.LifeCycleStateDisabled:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateDisabled
	case datamodel.LifeCycleStateUpdating:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateUpdating
	default:
		return gcpgenserver.VolumeReplicationInternalV1betaLifeCycleStateAvailable
	}
}

func mapEndpointTypeToInternal(endpointType string) gcpgenserver.VolumeReplicationInternalV1betaEndpointType {
	switch endpointType {
	case datamodel.SrcEndpoint:
		return gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeSrc
	case datamodel.DstEndpoint:
		return gcpgenserver.VolumeReplicationInternalV1betaEndpointTypeDst
	default:
		return ""
	}
}

func mapMirrorStateToInternal(mirrorState string) gcpgenserver.VolumeReplicationInternalV1betaMirrorState {
	switch mirrorState {
	case datamodel.OntapUninitialized:
		return gcpgenserver.VolumeReplicationInternalV1betaMirrorStatePREPARING
	case datamodel.OntapBrokenOff:
		return gcpgenserver.VolumeReplicationInternalV1betaMirrorStateSTOPPED
	case datamodel.OntapSnapmirrored:
		return gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORED
	default:
		return gcpgenserver.VolumeReplicationInternalV1betaMirrorStateMIRRORSTATEUNSPECIFIED
	}
}

func mapRelationshipStatusToInternal(relationshipStatus string) gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatus {
	switch relationshipStatus {
	case datamodel.SnapmirrorRelationshipIdle, datamodel.SnapmirrorRelationshipSuccess, datamodel.SnapmirrorRelationshipFinalizing:
		return gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusIdle
	case datamodel.SnapmirrorRelationshipTransferring:
		return gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusTransferring
	case datamodel.SnapmirrorRelationshipFailed:
		return gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusFailed
	case datamodel.SnapmirrorRelationshipAborted:
		return gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusAborted
	case datamodel.SnapmirrorRelationshipQueued:
		return gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusQueued
	case datamodel.SnapmirrorRelationshipHardAborted:
		return gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusHardAborted
	default:
		return gcpgenserver.VolumeReplicationInternalV1betaRelationshipStatusIdle
	}
}

func mapReplicationScheduleToInternal(schedule string) gcpgenserver.VolumeReplicationInternalV1betaReplicationSchedule {
	switch schedule {
	case vsa.VolumeReplicationScheduleHourly:
		return gcpgenserver.VolumeReplicationInternalV1betaReplicationScheduleHourly
	case vsa.VolumeReplicationScheduleDaily:
		return gcpgenserver.VolumeReplicationInternalV1betaReplicationScheduleDaily
	case vsa.VolumeReplicationSchedule10Minutely:
		return gcpgenserver.VolumeReplicationInternalV1betaReplicationSchedule10minutely
	default:
		return ""
	}
}

func convertToVolumeReplicationInternalV1Beta(replication *datamodel.VolumeReplication) gcpgenserver.VolumeReplicationInternalV1beta {
	if replication == nil {
		return gcpgenserver.VolumeReplicationInternalV1beta{}
	}

	var lastTransferEndTime time.Time
	var progressLastUpdated time.Time
	if replication.LastTransferEndTime != nil {
		lastTransferEndTime = *replication.LastTransferEndTime
	}
	if replication.ProgressLastUpdated != nil {
		progressLastUpdated = *replication.ProgressLastUpdated
	}

	retObj := gcpgenserver.VolumeReplicationInternalV1beta{
		VolumeReplicationUuid: gcpgenserver.NewOptString(replication.UUID),
		LifeCycleState:        gcpgenserver.NewOptVolumeReplicationInternalV1betaLifeCycleState(mapReplicationStateToInternalLifeCycleState(replication.State)),
		LifeCycleStateDetails: gcpgenserver.NewOptString(replication.StateDetails),
		Name:                  gcpgenserver.NewOptString(replication.Name),
		MirrorState:           gcpgenserver.NewOptVolumeReplicationInternalV1betaMirrorState(mapMirrorStateToInternal(nillable.GetString(replication.MirrorState, ""))),
		RelationshipStatus:    gcpgenserver.NewOptVolumeReplicationInternalV1betaRelationshipStatus(mapRelationshipStatusToInternal(nillable.GetString(replication.RelationshipStatus, ""))),
		TotalProgress:         gcpgenserver.NewOptInt64(replication.TotalProgress),
		Healthy:               gcpgenserver.NewOptBool(replication.Healthy),
		TotalTransferBytes:    gcpgenserver.NewOptInt64(replication.TotalTransferBytes),
		TotalTransferTimeSecs: gcpgenserver.NewOptInt64(replication.TotalTransferTimeSecs),
		LastTransferSize:      gcpgenserver.NewOptInt64(replication.LastTransferSize),
		LastTransferError:     gcpgenserver.NewOptString(replication.LastTransferError),
		LastTransferDuration:  gcpgenserver.NewOptInt64(replication.LastTransferDuration),
		LastTransferEndTime:   gcpgenserver.NewOptDateTime(lastTransferEndTime),
		ProgressLastUpdated:   gcpgenserver.NewOptDateTime(progressLastUpdated),
		LagTime:               gcpgenserver.NewOptInt64(replication.LagTime),
		CreatedAt:             gcpgenserver.NewOptDateTime(replication.CreatedAt),
		UpdatedAt:             gcpgenserver.NewOptDateTime(replication.UpdatedAt),
		Description:           gcpgenserver.NewOptString(replication.Description),
		CcfeUri:               gcpgenserver.NewOptString(replication.Uri),
		CcfeRemoteUri:         gcpgenserver.NewOptString(replication.RemoteUri),
	}

	// Handle ReplicationAttributes fields safely
	if replication.ReplicationAttributes != nil {
		retObj.EndpointType = mapEndpointTypeToInternal(replication.ReplicationAttributes.EndpointType)
		retObj.ReplicationType = gcpgenserver.NewOptVolumeReplicationInternalV1betaReplicationType(gcpgenserver.VolumeReplicationInternalV1betaReplicationType(replication.ReplicationAttributes.ReplicationType))
		retObj.ReplicationPolicy = gcpgenserver.NewOptVolumeReplicationInternalV1betaReplicationPolicy(gcpgenserver.VolumeReplicationInternalV1betaReplicationPolicyMirrorAllSnapshots)
		retObj.ReplicationSchedule = gcpgenserver.NewOptVolumeReplicationInternalV1betaReplicationSchedule(mapReplicationScheduleToInternal(replication.ReplicationAttributes.ReplicationSchedule))
		retObj.SourceHostName = replication.ReplicationAttributes.SourceHostName
		retObj.SourceServerName = replication.ReplicationAttributes.SourceSvmName
		retObj.SourceVolumeName = replication.ReplicationAttributes.SourceVolumeName
		retObj.SourceVolumeUuid = gcpgenserver.NewOptString(replication.ReplicationAttributes.SourceVolumeUUID)
		retObj.DestinationHostName = replication.ReplicationAttributes.DestinationHostName
		retObj.DestinationServerName = replication.ReplicationAttributes.DestinationSvmName
		retObj.DestinationVolumeName = replication.ReplicationAttributes.DestinationVolumeName
		retObj.DestinationVolumeUuid = gcpgenserver.NewOptString(replication.ReplicationAttributes.DestinationVolumeUUID)
		retObj.RemoteRegion = replication.ReplicationAttributes.DestinationLocation
		retObj.Labels = convertJSONBLabelsToOptLabels(replication.ReplicationAttributes.Labels)
	}

	if nillable.GetString(replication.RelationshipStatus, "") == datamodel.SnapmirrorRelationshipTransferring {
		if nillable.GetString(replication.MirrorState, "") == datamodel.OntapUninitialized {
			retObj.MirrorState = gcpgenserver.NewOptVolumeReplicationInternalV1betaMirrorState(gcpgenserver.VolumeReplicationInternalV1betaMirrorStateBASELINETRANSFERRING)
		} else {
			retObj.MirrorState = gcpgenserver.NewOptVolumeReplicationInternalV1betaMirrorState(gcpgenserver.VolumeReplicationInternalV1betaMirrorStateTRANSFERRING)
		}
	}

	return retObj
}

func convertJSONBLabelsToOptLabels(labels *datamodel.JSONB) gcpgenserver.OptVolumeReplicationInternalV1betaLabels {
	if labels == nil {
		return gcpgenserver.OptVolumeReplicationInternalV1betaLabels{}
	}

	result := make(map[string]string)
	for key, value := range *labels {
		if strValue, ok := value.(string); ok {
			result[key] = strValue
		}
	}

	convertedLabels := gcpgenserver.VolumeReplicationInternalV1betaLabels(result)
	return gcpgenserver.NewOptVolumeReplicationInternalV1betaLabels(convertedLabels)
}

func convertToPoolInternalV1Beta(pool *models.Pool) *gcpgenserver.PoolInternalV1beta {
	poolResp := &gcpgenserver.PoolInternalV1beta{
		Network:                  pool.VendorSubNetID,
		PoolId:                   gcpgenserver.NewOptString(pool.UUID),
		ResourceId:               pool.Name,
		ServiceLevel:             gcpgenserver.PoolInternalV1betaServiceLevel(pool.ServiceLevel),
		QosType:                  gcpgenserver.NewOptNilString(pool.QosType),
		SizeInBytes:              float64(pool.SizeInBytes),
		AllocatedBytes:           gcpgenserver.NewOptNilFloat64(pool.PoolAttributes.AllocatedBytes),
		TotalThroughputMibps:     gcpgenserver.NewOptNilFloat64(pool.TotalThroughputMibps),
		AvailableThroughputMibps: gcpgenserver.NewOptNilFloat64(pool.TotalThroughputMibps - pool.UtilizedThroughputMibps),
		NumberOfVolumes:          gcpgenserver.NewOptNilInt32(int32(pool.PoolAttributes.NumberOfVolumes)),
		StoragePoolState: gcpgenserver.NewOptPoolInternalV1betaStoragePoolState(
			gcpgenserver.PoolInternalV1betaStoragePoolState(pool.State),
		),
		StoragePoolStateDetails: gcpgenserver.NewOptString(pool.StateDetails),
		CreatedAt:               gcpgenserver.NewOptDateTime(pool.CreatedAt),
		UpdatedAt:               gcpgenserver.NewOptDateTime(pool.UpdatedAt),
		StateDetails:            gcpgenserver.NewOptString(pool.StateDetails),
		Description:             gcpgenserver.NewOptNilString(pool.Description),
		Zone:                    gcpgenserver.NewOptString(pool.Zone),
		AllowAutoTiering:        gcpgenserver.NewOptNilBool(pool.AllowAutoTiering),
		SatisfiesPzi:            gcpgenserver.NewOptNilBool(pool.SatisfiesPzi),
		SatisfiesPzs:            gcpgenserver.NewOptNilBool(pool.SatisfiesPzs),
		LargeCapacity:           gcpgenserver.NewOptBool(pool.LargeCapacity),
	}
	if pool.PoolAttributes != nil {
		poolResp.SecondaryZone = gcpgenserver.NewOptString(pool.PoolAttributes.SecondaryZone)
		if config, ok := largeCapacityConfigFromLvHAPairs(pool.PoolAttributes.LvHAPairs); ok {
			poolResp.LargeCapacityConfig = gcpgenserver.NewOptNilPoolInternalV1betaLargeCapacityConfig(
				gcpgenserver.PoolInternalV1betaLargeCapacityConfig(config),
			)
		}
	}
	if pool.CustomPerformanceParams != nil {
		poolResp.CustomPerformanceEnabled = gcpgenserver.NewOptBool(pool.CustomPerformanceParams.Enabled)
		poolResp.TotalIops = gcpgenserver.NewOptNilFloat64(float64(pool.CustomPerformanceParams.Iops))
	}
	if pool.ClusterDetails != nil {
		poolResp.InterclusterLifs = pool.ClusterDetails.InterClusterLifs
		poolResp.ClusterName = gcpgenserver.NewOptString(pool.ClusterDetails.ExternalName)
	}
	return poolResp
}

func _convertToVolumeReplicationsInternalV1Beta(in []*datamodel.VolumeReplication) []gcpgenserver.VolumeReplicationInternalV1beta {
	if in == nil {
		return nil
	}
	var out []gcpgenserver.VolumeReplicationInternalV1beta
	for _, replication := range in {
		out = append(out, convertToVolumeReplicationInternalV1Beta(replication))
	}
	return out
}

func convertBackupDataModelToInternalBackupsV1beta(backup *datamodel.Backup, isRestoring bool) gcpgenserver.InternalBackupV1beta {
	var state gcpgenserver.InternalBackupV1betaState
	// Need to convert states as DB models and API models have different states
	switch backup.State {
	case datamodel.LifeCycleStateAvailable:
		state = gcpgenserver.InternalBackupV1betaStateREADY
	case datamodel.LifeCycleStateUpdating:
		state = gcpgenserver.InternalBackupV1betaStateUPDATING
	default:
		state = gcpgenserver.InternalBackupV1betaState(backup.State)
	}
	sourceVolumePath := utils.GetSourceVolumePathFromBackup(backup)
	if backup.Attributes != nil && backup.Attributes.IsExpertModeBackup && backup.Attributes.VolumeName != "" {
		// Keep the full path format but replace the volume name segment with the ONTAP volume UUID.
		if parts := strings.Split(sourceVolumePath, "/"); len(parts) > 0 && parts[len(parts)-1] == backup.Attributes.VolumeName {
			parts[len(parts)-1] = backup.VolumeUUID
			sourceVolumePath = strings.Join(parts, "/")
		}
	}
	sourceSnapshotPath := utils.GetSourceSnapshotPathFromBackup(backup)

	var satisfiesPzi, satisfiesPzs bool
	for _, bucket := range backup.BackupVault.BucketDetails {
		if bucket.BucketName == backup.Attributes.BucketName {
			satisfiesPzi = bucket.SatisfiesPzi
			satisfiesPzs = bucket.SatisfiesPzs
			break
		}
	}

	internalBackupV1 := gcpgenserver.InternalBackupV1beta{
		ResourceId: gcpgenserver.OptString{
			Value: backup.Name,
			Set:   true,
		},
		VolumeId: gcpgenserver.OptString{
			Value: backup.VolumeUUID,
			Set:   true,
		},
		State: gcpgenserver.OptInternalBackupV1betaState{
			Value: state,
			Set:   true,
		},
		Created: gcpgenserver.OptDateTime{
			Value: backup.CreatedAt,
			Set:   true,
		},
		BackupId: gcpgenserver.OptString{
			Value: backup.UUID,
			Set:   true,
		},
		VolumeUsageBytes: gcpgenserver.OptInt64{
			Value: backup.SizeInBytes,
			Set:   true,
		},
		BackupVaultId: gcpgenserver.OptString{
			Value: backup.BackupVault.UUID,
			Set:   true,
		},
		Description: gcpgenserver.OptString{
			Value: backup.Description,
			Set:   true,
		},
		BackupType: gcpgenserver.OptInternalBackupV1betaBackupType{
			Value: gcpgenserver.InternalBackupV1betaBackupType(backup.Type),
			Set:   true,
		},
		SourceSnapshot: gcpgenserver.OptString{
			Value: sourceSnapshotPath,
			Set:   backup.Attributes.UseExistingSnapshot && backup.Attributes.SnapshotName != "",
		},
		SourceVolume: gcpgenserver.OptString{
			Value: sourceVolumePath,
			Set:   true,
		},
		BackupRegion: gcpgenserver.OptString{
			Value: *backup.BackupVault.SourceRegionName,
			Set:   true,
		},
		VolumeRegion: gcpgenserver.OptString{
			Value: *backup.BackupVault.SourceRegionName,
			Set:   true,
		},
		SatisfiesPzi: gcpgenserver.OptBool{
			Value: satisfiesPzi,
			Set:   true,
		},
		SatisfiesPzs: gcpgenserver.OptBool{
			Value: satisfiesPzs,
			Set:   true,
		},
		BackupChainBytes: gcpgenserver.OptInt64{
			Value: backup.LatestLogicalBackupSize,
			Set:   backup.LatestLogicalBackupSize != 0,
		},
		IsRestoring: gcpgenserver.OptBool{
			Value: isRestoring,
			Set:   true,
		},
	}
	if backup.BackupVault.ImmutableAttributes != nil && *backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration > 0 && common.CheckIfBackupIsImmutable(backup) {
		expirationDate := backup.CreatedAt.AddDate(0, 0, int(*backup.BackupVault.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration))
		if !time.Now().After(expirationDate) {
			internalBackupV1.EnforcedRetentionEndTime = gcpgenserver.OptDateTime{
				Value: expirationDate,
				Set:   true,
			}
		}
	}
	if backup.AssetMetadata != nil {
		internalBackupV1.AssetLocationMetadata = gcpgenserver.OptAssetLocationMetadataV2{
			Value: gcpgenserver.AssetLocationMetadataV2{
				ChildAssets: func() []gcpgenserver.ChildAssetV2 {
					var assets []gcpgenserver.ChildAssetV2
					for _, asset := range backup.AssetMetadata.ChildAssets {
						assets = append(assets, gcpgenserver.ChildAssetV2{
							AssetType:  gcpgenserver.OptString{Value: asset.AssetType, Set: true},
							AssetNames: asset.AssetNames,
						})
					}
					return assets
				}(),
			},
			Set: true,
		}
	}
	return internalBackupV1
}
