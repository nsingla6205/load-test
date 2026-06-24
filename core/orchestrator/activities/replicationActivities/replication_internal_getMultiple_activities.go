package replicationActivities

import (
	"context"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	vsamodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

type ReplicationInternalGetMultipleActivity struct {
	SE database.Storage
}

var (
	activitiesGetProviderByNode = vsa.GetProviderByNode
)

// GetReplicationsFromDB retrieves multiple replications from the database based on the ReplicationUUIDs.
func (r *ReplicationInternalGetMultipleActivity) GetReplicationsFromDB(ctx context.Context, params *common.ReplicationInternalGetMultipleParams) (*common.ReplicationInternalGetMultipleParams, error) {
	se := r.SE
	logger := util.GetLogger(ctx)

	account, err := se.GetAccount(ctx, params.AccountName)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	filter := utils.CreateFilterWithConditions(
		utils.NewFilterCondition("account_id", "=", account.ID),
		utils.NewFilterCondition("uuid", "in", params.ReplicationUUIDs))

	replications, err := se.ListVolumeReplications(ctx, *filter, database.QueryDepthZero)
	if err != nil {
		logger.Errorf("Failed to list replications for account %s: %v", params.AccountName, err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if len(replications) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("replication", nil))
	}

	params.ReplicationsFromDB = replications
	return params, nil
}

// GetNodesForPools retrieves nodes for all the pools associated with the params.ReplicationsFromDB.
func (r *ReplicationInternalGetMultipleActivity) GetNodesForPools(ctx context.Context, params *common.ReplicationInternalGetMultipleParams) (*common.ReplicationInternalGetMultipleParams, error) {
	se := r.SE
	logger := util.GetLogger(ctx)
	poolNodeMap := make(map[int64]*datamodel.Node)

	for _, replication := range params.ReplicationsFromDB {
		if _, ok := poolNodeMap[replication.Volume.PoolID]; ok {
			continue // Skip if node for this pool is already fetched
		}

		nodes, err := se.GetNodesByPoolID(ctx, replication.Volume.PoolID)
		if err != nil {
			logger.Errorf("Failed to get nodes for pool %d: %v", replication.Volume.PoolID, err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}

		if len(nodes) == 0 {
			logger.Errorf("No nodes found for pool %d", replication.Volume.PoolID)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, errors.NewNotFoundErr("node", nil))
		}
		poolNodeMap[replication.Volume.PoolID] = nodes[0]
	}

	params.PoolNodeMap = poolNodeMap
	return params, nil
}

// GetReplicationsFromOntap fetches replication details from Ontap for each pool in params.PoolNodeMap.
func (r *ReplicationInternalGetMultipleActivity) GetReplicationsFromOntap(ctx context.Context, params *common.ReplicationInternalGetMultipleParams) (*common.ReplicationInternalGetMultipleParams, error) {
	logger := util.GetLogger(ctx)

	// Create the poolReplicationsMap to group replications by pool ID
	poolReplicationsMap := make(map[int64][]*datamodel.VolumeReplication)
	var updatedReplications []*datamodel.VolumeReplication

	for _, replication := range params.ReplicationsFromDB {
		poolID := replication.Volume.PoolID
		// Check if replication should be refreshed
		// If last updated time is older than the schedule Or if relationship status is transferring
		if shouldRefreshReplication(replication) {
			poolReplicationsMap[poolID] = append(poolReplicationsMap[poolID], replication)
		} else {
			logger.Infof("Skipping replication %s for pool %d as it does not need refresh", replication.UUID, poolID)
			continue
		}
	}

	params.PoolReplicationsMap = poolReplicationsMap

	// Iterate over each pool from poolNodeMap and fetch replication details from Ontap
	for poolID, node := range params.PoolNodeMap {
		replications, ok := params.PoolReplicationsMap[poolID]
		if !ok || len(replications) == 0 {
			logger.Warnf("No replications found for pool %d", poolID)
			continue // Skip if no replications for this pool
		}
		// Prepare node for provider
		nodeModel := vsa.CreateNodeForProvider(vsa.NodeProviderInput{
			Nodes:            []*datamodel.Node{node},
			DeploymentName:   replications[0].Volume.Pool.DeploymentName,
			OntapCredentials: replications[0].Volume.Pool.PoolCredentials,
		})

		// Get Ontap provider
		prov, err := activitiesGetProviderByNode(ctx, nodeModel)
		if err != nil {
			return nil, vsaerrors.WrapAsTemporalApplicationError(err)
		}
		for _, replication := range replications {
			replFromOntap, err := prov.GetReplicationDetails(ctx, convertToSnapmirrorGetParams(replication, params.AccountName))
			if err != nil {
				if errors.IsNotFoundErr(err) {
					logger.Warnf("Replication %s not found in Ontap for pool %d, skipping", replication.UUID, poolID)
					continue // Skip if replication not found in Ontap
				}
				logger.Errorf("Failed to get replication details from Ontap for replication %s: %v", replication.UUID, err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrFailedToGetSnapmirrorDetailsFromOntapGetMultiple, err)
			}

			replication = addVsaModelReplicationDetailsToDatamodelReplication(replFromOntap, replication)
			updatedReplications = append(updatedReplications, replication)
		}
	}

	params.UpdatedReplications = updatedReplications
	return params, nil
}

// UpdateReplicationsInDB updates the replication transfer and health details in the database.
func (r *ReplicationInternalGetMultipleActivity) UpdateReplicationsInDB(ctx context.Context, params *common.ReplicationInternalGetMultipleParams) error {
	se := r.SE
	logger := util.GetLogger(ctx)

	if len(params.UpdatedReplications) == 0 {
		logger.Info("No updated replications to process, skipping database update")
		return nil // No updates to process
	}

	for _, replication := range params.UpdatedReplications {
		replication.LastUpdatedFromOntap = time.Now()

		err := se.UpdateVolumeReplicationTransferStats(ctx, replication)
		if err != nil {
			logger.Errorf("Failed to update replication %s in DB: %v", replication.UUID, err)
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		}
	}

	return nil
}

// convertToSnapmirrorGetParams converts a datamodel.VolumeReplication to vsamodels.VolumeReplication.
func convertToSnapmirrorGetParams(in *datamodel.VolumeReplication, accountName string) *vsamodels.VolumeReplication {
	return &vsamodels.VolumeReplication{
		UUID:                  in.UUID,
		AccountName:           accountName,
		DestinationHostName:   in.ReplicationAttributes.DestinationHostName,
		DestinationVolumeName: in.ReplicationAttributes.DestinationVolumeName,
		DestinationSVMName:    in.ReplicationAttributes.DestinationSvmName,
		ExternalUUID:          in.ReplicationAttributes.ExternalUUID,
	}
}

// addVsaModelReplicationDetailsToDatamodelReplication adds transfer and health details from vsamodels.VolumeReplication to datamodel.VolumeReplication.
func addVsaModelReplicationDetailsToDatamodelReplication(in *vsamodels.VolumeReplication, repl *datamodel.VolumeReplication) *datamodel.VolumeReplication {
	repl.MirrorState = &in.MirrorState
	repl.RelationshipStatus = &in.RelationshipStatus
	repl.TotalProgress = in.TotalProgress
	repl.Healthy = in.Healthy
	repl.UnhealthyReason = in.UnhealthyReason
	repl.TotalTransferBytes = in.TotalTransferBytes
	repl.TotalTransferTimeSecs = in.TotalTransferTimeSecs
	repl.LastTransferSize = in.LastTransferSize
	repl.LastTransferError = in.LastTransferError
	repl.LastTransferEndTime = in.LastTransferEndTime
	repl.ProgressLastUpdated = in.ProgressLastUpdated
	repl.LagTime = in.LagTime
	repl.LastTransferDuration = in.LastTransferDuration
	repl.ReplicationAttributes.ExternalUUID = in.ExternalUUID

	return repl
}

// shouldRefreshReplication checks if the replication should be refreshed based on its last updated time and relationship status.
func shouldRefreshReplication(repl *datamodel.VolumeReplication) bool {
	// Get an update interval from replication schedule
	scheduleDuration := getDurationFromSchedule(repl.ReplicationAttributes.ReplicationSchedule)
	lastUpdated := repl.LastUpdatedFromOntap
	// Check if the replication should be refreshed based on last updated time and relationship status
	if time.Since(lastUpdated) > scheduleDuration || (repl.RelationshipStatus != nil && (*repl.RelationshipStatus == datamodel.SnapmirrorRelationshipTransferring || *repl.RelationshipStatus == datamodel.SnapmirrorRelationshipFinalizing)) {
		return true
	}
	return false
}

// getDurationFromSchedule returns the duration based on the replication schedule.
func getDurationFromSchedule(schedule string) time.Duration {
	switch schedule {
	case "hourly":
		return time.Hour
	case "daily":
		return 24 * time.Hour
	case "10minutely":
		return 10 * time.Minute
	default:
		return 0
	}
}
