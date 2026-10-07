package replicationActivities

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
)

// ShouldBreakBeforeDelete reports whether the live ONTAP relationship must be broken
// before it is deleted, so the destination volume is left writable instead of DP.
func ShouldBreakBeforeDelete(ontapReplication *vsa.VolumeReplication) bool {
	if ontapReplication == nil {
		return false
	}
	return ontapReplication.MirrorState == vsa.SnapmirrorStateMirrored ||
		(ontapReplication.MirrorState == vsa.SnapmirrorStateUninitialized &&
			ontapReplication.RelationshipStatus == vsa.SnapMirrorRelationshipStatusTransferring)
}

type InternalVolumeReplicationDeleteActivity struct {
	SE database.Storage
}

// GetVolumeReplicationFromOntap returns the live SnapMirror relationship from ONTAP.
// When the relationship UUID is set it looks up by that UUID. When the UUID is empty it
// looks up by source and destination path, matching the internal stop activity.
// It returns nil when the replication record is missing or ONTAP no longer has the relationship.
func (a *InternalVolumeReplicationDeleteActivity) GetVolumeReplicationFromOntap(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) (*vsa.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	if replication == nil || replication.ReplicationAttributes == nil {
		logger.Info("No replication attributes; skipping ONTAP lookup")
		return nil, nil
	}
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	lookup := buildSnapmirrorLookupParams(replication)
	var ontapReplication *vsa.VolumeReplication
	if lookup.ExternalUUID == "" {
		logger.Info("No SnapMirror relationship UUID on replication; looking up by source and destination path")
		ontapReplication, err = provider.GetVolumeReplicationFromSrcAndDstPath(lookup)
	} else {
		ontapReplication, err = provider.GetVolumeReplication(lookup)
	}
	if err != nil {
		if errors.IsNotFoundErr(err) || strings.Contains(err.Error(), "entry doesn't exist") || strings.Contains(err.Error(), "entry not found") {
			logger.Info("SnapMirror relationship not found on ONTAP", "relationshipUUID", lookup.ExternalUUID)
			return nil, nil
		}
		logger.Error("Failed to get volume replication from ONTAP", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrProviderGetVolumeReplication, err)
	}
	return ontapReplication, nil
}

func (a *InternalVolumeReplicationDeleteActivity) DeleteVolumeReplication(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node, isCleanup bool) (*vsa.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}
	vsaDeleteVolumeReplicationParams := prepareDeleteVolumeReplicationParamsVSA(replication)
	vsaDeleteVolumeReplicationParams.VolumeReplication.IsCleanup = isCleanup
	res, err := provider.DeleteVolumeReplication(vsaDeleteVolumeReplicationParams)
	if err != nil {
		// For hybrid replication, the peer (on-prem/remote) cluster may be unreachable when
		// tearing down the relationship. In that case retry the delete targeting only the
		// destination relationship so the local side can still be cleaned up.
		if isHybridReplication(replication) && isPeerClusterUnreachableError(err) {
			logger.Info("Failed to delete hybrid volume replication due to unreachable peer cluster, retrying with destinationOnly", "error", err)
			destinationOnly := true
			// Assign a fresh pointer: prepareDeleteVolumeReplicationParamsVSA points both
			// DestinationOnly and SourceOnly at the same bool, so mutating in place would
			// set both flags and fail provider validation.
			vsaDeleteVolumeReplicationParams.DestinationOnly = &destinationOnly
			res, err = provider.DeleteVolumeReplication(vsaDeleteVolumeReplicationParams)
		}
		// Re-check err: it may have been cleared by a successful destinationOnly retry.
		if err != nil {
			if errors.IsConflictErr(err) {
				logger.Error("Failed to delete volume replication", "error", err)
				return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrProviderDeleteVolumeReplication, err))
			}
			logger.Error("Failed to delete volume replication", "error", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrProviderDeleteVolumeReplication, err)
		}
	}
	return res, nil
}

// isHybridReplication reports whether the given volume replication represents a
// hybrid replication relationship of a type that involves an external peer
// cluster (on-prem replication or migration).
func isHybridReplication(replication *datamodel.VolumeReplication) bool {
	if replication == nil || replication.HybridReplicationAttributes == nil ||
		replication.HybridReplicationAttributes.HybridReplicationType == nil {
		return false
	}
	replicationType := *replication.HybridReplicationAttributes.HybridReplicationType
	return replicationType == string(datamodel.HybridReplicationParametersReplicationTypeONPREM) ||
		replicationType == string(datamodel.HybridReplicationParametersReplicationTypeMIGRATION)
}

// isPeerClusterUnreachableError reports whether the error indicates the peer
// cluster could not be contacted (e.g. "Failed to contact peer cluster ... RPC:
// Remote system error ..."). For hybrid replication this is recoverable by
// deleting only the destination side.
func isPeerClusterUnreachableError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "failed to contact peer cluster")
}

func (a *InternalVolumeReplicationDeleteActivity) CleanupReplicationAfterReverse(ctx context.Context, replication *datamodel.VolumeReplication, node *models.Node) (*vsa.VolumeReplication, error) {
	logger := util.GetLogger(ctx)
	provider, err := vsa.GetProviderByNode(ctx, node)
	if err != nil {
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	volumeRep := &vsa.VolumeReplication{
		EndpointType:          replication.ReplicationAttributes.EndpointType,
		SourceHostName:        replication.ReplicationAttributes.DestinationHostName,
		SourceSVMName:         replication.ReplicationAttributes.DestinationSvmName,
		SourceVolumeName:      replication.ReplicationAttributes.DestinationVolumeName,
		DestinationHostName:   replication.ReplicationAttributes.SourceHostName,
		DestinationSVMName:    replication.ReplicationAttributes.SourceSvmName,
		ReplicationSchedule:   replication.ReplicationAttributes.ReplicationSchedule,
		DestinationVolumeName: replication.ReplicationAttributes.SourceVolumeName,
		RelationshipID:        replication.ReplicationAttributes.ExternalUUID,
		IsCleanup:             true,
	}
	vsaDeleteVolumeReplicationParams := &vsa.DeleteVolumeReplicationParams{
		VolumeReplication: volumeRep,
	}
	res, err := provider.DeleteVolumeReplication(vsaDeleteVolumeReplicationParams)
	if err != nil {
		if errors.IsConflictErr(err) {
			logger.Error("Failed to cleanup volume replication", "error", err)
			return nil, vsaerrors.WrapAsNonRetryableTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrProviderDeleteVolumeReplication, err))
		}
		logger.Error("Failed to cleanup volume replication", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrProviderDeleteVolumeReplication, err)
	}
	return res, nil
}

func replicationTypeForVSADelete(volumeReplication *datamodel.VolumeReplication) string {
	if volumeReplication.HybridReplicationAttributes != nil &&
		volumeReplication.HybridReplicationAttributes.HybridReplicationType != nil {
		return *volumeReplication.HybridReplicationAttributes.HybridReplicationType
	}
	if volumeReplication.ReplicationAttributes != nil {
		return volumeReplication.ReplicationAttributes.ReplicationType
	}
	return ""
}

func prepareDeleteVolumeReplicationParamsVSA(volumeReplication *datamodel.VolumeReplication) *vsa.DeleteVolumeReplicationParams {
	params := false
	replication := &vsa.VolumeReplication{
		EndpointType:          volumeReplication.ReplicationAttributes.EndpointType,
		SourceHostName:        volumeReplication.ReplicationAttributes.SourceHostName,
		SourceSVMName:         volumeReplication.ReplicationAttributes.SourceSvmName,
		SourceVolumeName:      volumeReplication.ReplicationAttributes.SourceVolumeName,
		DestinationHostName:   volumeReplication.ReplicationAttributes.DestinationHostName,
		DestinationSVMName:    volumeReplication.ReplicationAttributes.DestinationSvmName,
		ReplicationSchedule:   volumeReplication.ReplicationAttributes.ReplicationSchedule,
		DestinationVolumeName: volumeReplication.ReplicationAttributes.DestinationVolumeName,
		RelationshipID:        volumeReplication.ReplicationAttributes.ExternalUUID,
		ReplicationType:       replicationTypeForVSADelete(volumeReplication),
	}
	return &vsa.DeleteVolumeReplicationParams{
		VolumeReplication: replication,
		DestinationOnly:   &params,
		SourceOnly:        &params,
	}
}

func (a *InternalVolumeReplicationDeleteActivity) UpdateReplicationStateInDBForDelete(ctx context.Context, volumeRep *datamodel.VolumeReplication) error {
	se := a.SE
	volumeRep.State = datamodel.LifeCycleStateError
	volumeRep.StateDetails = datamodel.LifeCycleStateDeletionErrorDetails
	if err := se.UpdateVolumeReplicationStates(ctx, volumeRep); err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}
