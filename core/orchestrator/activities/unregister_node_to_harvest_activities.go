package activities

import (
	"context"
	stderrors "errors"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/metrics"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/temporal"
)

const (
	UnRegisterNodesInfoNotAvailable    = "no Available Nodes to unregister"
	UnRegisterNodeGroupMapNotAvailable = "node group map not available"
)

var (
	harvestEndPoint     = env.GetString("HARVEST_HOST", "localhost:8080")
	harvestRestProtocol = env.GetString("HARVEST_REST_PROTOCOL", "http")

	deleteHarvestPollerFromFarm = deleteHarvestPollerFromFarmClient
	// Aliases for shared harvest poller YAML delete helpers in poller_rebalance_activities.go.
	deleteHarvestPollerYAML    = deleteUploadedRebalanceTarget
	deleteHarvestPollerFromPod = deleteRebalanceTargetHarvestPoller

	deleteKubernetesLease                           = utils.DeleteKubernetesLease
	UnRegisterNodeFromHarvestFarmNonRetryableErrors = []string{
		UnRegisterNodesInfoNotAvailable,
		UnRegisterNodeGroupMapNotAvailable,
	}
)

type UnRegisterNodeFromHarvestActivityParams struct {
	PoolID            int64
	CustomerProjectID string
	TenantProjectID   string
	Nodes             []*datamodel.Node
	NodeGroupsMap     []*datamodel.NodeNodeGroupMap
}

type UnRegisterNodeFromHarvestActivity struct {
	SE database.Storage
}

// deleteHarvestPollerFromFarmClient removes a poller YAML from the configured destination
// (GCS bucket when POLLER_REBALANCE_BUCKET_NAME is set, otherwise the harvest pod PVC).
func deleteHarvestPollerFromFarmClient(ctx context.Context, leaseName string, nodeID int64) error {
	if isRebalanceBucketModeEnabled() {
		return deleteHarvestPollerYAML(ctx, "gs://"+strings.TrimSpace(pollerRebalanceBucketName), leaseName, nodeID)
	}
	return deleteHarvestPollerFromPod(ctx, leaseName, nodeID)
}

// ValidateAndGetNodes retrieves nodes for the given pool ID.
// Note: dbHbCtx is the context with DB heartbeat timeout configured from the workflow
func (unRegisterNodeToHarvest *UnRegisterNodeFromHarvestActivity) ValidateAndGetNodes(
	dbHbCtx context.Context,
	activityParams *UnRegisterNodeFromHarvestActivityParams,
) ([]*datamodel.Node, error) {
	logger := util.GetLogger(dbHbCtx)
	activity.RecordHeartbeat(dbHbCtx, "ValidateAndGetNodes started")

	se := unRegisterNodeToHarvest.SE
	poolID := activityParams.PoolID
	nodes, err := se.GetNodesByPoolID(dbHbCtx, poolID)
	if err != nil {
		if errors.IsNotFoundErr(err) {
			logger.Errorf("no nodes found for pool ID %d", poolID)
			return nil, temporal.NewNonRetryableApplicationError(err.Error(), UnRegisterNodesInfoNotAvailable, err)
		}
		logger.Warnf("failed to retrieve nodes information from pool ID %d: %w", poolID, err)
		return nil, err
	}
	return nodes, nil
}

// GetNodeGroupMapping retrieves node group mappings for the given nodes.
// Note: dbHbCtx is the context with DB heartbeat timeout configured from the workflow
func (unRegisterNodeToHarvest *UnRegisterNodeFromHarvestActivity) GetNodeGroupMapping(
	dbHbCtx context.Context,
	activityParams *UnRegisterNodeFromHarvestActivityParams,
) ([]*datamodel.NodeNodeGroupMap, error) {
	se := unRegisterNodeToHarvest.SE
	logger := util.GetLogger(dbHbCtx)
	activity.RecordHeartbeat(dbHbCtx, "GetNodeGroupMapping started")

	var nodeGroupMappings []*datamodel.NodeNodeGroupMap
	for i, node := range activityParams.Nodes {
		activity.RecordHeartbeat(dbHbCtx, fmt.Sprintf("Getting node group mapping %d/%d", i+1, len(activityParams.Nodes)))
		nodeGroupMap, err := se.GetNodeNodeGroupMapByNodeID(dbHbCtx, node.ID)
		if err != nil {
			if errors.IsNotFoundErr(err) {
				// A node may have no mapping if it was never registered (e.g. multi-HA pair
				// registration was disabled at onboard time, or the flag was toggled during the
				// pool's lifetime). Always skip the missing mapping and continue, independent of the
				// current flag value, so offboarding cleans up every mapping that actually exists.
				logger.Warnf("no nodegroupmap info found for node ID %d, skipping", node.ID)
				continue
			}
			logger.Warnf("failed to retrieve nodegroupmap information from node ID %d: %v", node.ID, err)
			return nil, err
		}
		nodeGroupMappings = append(nodeGroupMappings, nodeGroupMap)
	}
	return nodeGroupMappings, nil
}

// DeleteNodeGroupMapping deletes node group mappings from the database.
// Note: dbHbCtx is the context with DB heartbeat timeout configured from the workflow
func (unRegisterNodeToHarvest *UnRegisterNodeFromHarvestActivity) DeleteNodeGroupMapping(
	dbHbCtx context.Context, activityParams *UnRegisterNodeFromHarvestActivityParams,
) error {
	logger := util.GetLogger(dbHbCtx)
	activity.RecordHeartbeat(dbHbCtx, "DeleteNodeGroupMapping started")

	se := unRegisterNodeToHarvest.SE
	var errs []error
	for i, nodeGroupMapping := range activityParams.NodeGroupsMap {
		activity.RecordHeartbeat(dbHbCtx, fmt.Sprintf("Deleting node group mapping %d/%d", i+1, len(activityParams.NodeGroupsMap)))
		err := se.DeleteNodeNodeGroupMap(dbHbCtx, nodeGroupMapping.ID)
		if err != nil {
			// An already-absent mapping (never created, or soft-deleted by a prior attempt) is an
			// idempotent no-op. Skipping it lets a whole-activity retry make progress instead of
			// re-failing on maps a previous attempt already removed.
			if isNodeGroupMapAlreadyGone(err) {
				logger.Warnf("node group map %d (node %d) already deleted, skipping", nodeGroupMapping.ID, nodeGroupMapping.NodeID)
				continue
			}
			// Accumulate the error and keep going so a single stuck mapping does not block cleanup
			// of the remaining nodes in the pool.
			logger.Warnf("failed to delete nodeGroupMap %d for node %d: %v", nodeGroupMapping.ID, nodeGroupMapping.NodeID, err)
			errs = append(errs, fmt.Errorf("node group map %d (node %d): %w", nodeGroupMapping.ID, nodeGroupMapping.NodeID, err))
			continue
		}
	}
	return stderrors.Join(errs...)
}

// isNodeGroupMapAlreadyGone reports whether err indicates the node group map row is already
// absent (never created, or already soft-deleted). Such errors are idempotent no-ops for
// offboarding, so a whole-activity retry must not keep failing on maps a prior attempt removed.
func isNodeGroupMapAlreadyGone(err error) bool {
	if err == nil {
		return false
	}
	if ce := vsaerrors.ExtractCustomError(err); ce != nil && ce.IsError(vsaerrors.ErrDatabaseDataNotFoundError) {
		return true
	}
	return errors.IsNotFoundErr(err)
}

func (unRegisterNodeToHarvest *UnRegisterNodeFromHarvestActivity) DeletePollersFromHarvestFarm(
	ctx context.Context, activityParams *UnRegisterNodeFromHarvestActivityParams) error {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "DeletePollersFromHarvestFarm started")

	var errs []error
	for i, nodeMap := range activityParams.NodeGroupsMap {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Deleting poller %d/%d", i+1, len(activityParams.NodeGroupsMap)))
		if nodeMap.NodeGroup == nil {
			logger.Warnf("nil NodeGroup for node group map %d (node %d), skipping poller delete", nodeMap.ID, nodeMap.NodeID)
			continue
		}
		leaseName := nodeMap.NodeGroup.LeaseName
		if len(leaseName) == 0 {
			logger.Warnf("no leaseName exists for nodeGroupMap Name:%s", nodeMap.NodeGroup.Name)
			continue
		}
		if err := deleteHarvestPollerFromFarm(ctx, leaseName, nodeMap.NodeID); err != nil {
			// Accumulate the error and keep going so a single stuck poller does not block deletion
			// of the remaining pollers. The harvest delete treats a missing YAML (404) as success,
			// so a whole-activity retry re-processes the failed nodes idempotently.
			logger.Warnf("Failed to delete YAML template for node mapping %d: %v", nodeMap.NodeID, err)
			errs = append(errs, fmt.Errorf("node %d lease %s: %w", nodeMap.NodeID, leaseName, err))
			continue
		}
		logger.Infof("Successfully deleted poller %s from harvest farm lease %s", nodeMap.UUID, leaseName)
	}
	return stderrors.Join(errs...)
}

// ValidateAndReleaseLease validates and releases Kubernetes leases for node groups.
// Note: dbHbCtx is the context with DB heartbeat timeout configured from the workflow
func (unRegisterNodeToHarvest *UnRegisterNodeFromHarvestActivity) ValidateAndReleaseLease(
	dbHbCtx context.Context,
	activityParams *UnRegisterNodeFromHarvestActivityParams) error {
	logger := util.GetLogger(dbHbCtx)
	activity.RecordHeartbeat(dbHbCtx, "ValidateAndReleaseLease started")

	se := unRegisterNodeToHarvest.SE
	for i, nodeGroupMap := range activityParams.NodeGroupsMap {
		activity.RecordHeartbeat(dbHbCtx, fmt.Sprintf("Validating and releasing lease %d/%d", i+1, len(activityParams.NodeGroupsMap)))
		nodesCount, err := se.GetNodeGroupMapNodeCount(dbHbCtx, nodeGroupMap.NodeGroupID)
		if err != nil {
			return err
		}
		if nodesCount == 0 {
			if nodeGroupMap.NodeGroup != nil {
				logger.Infof("Releasing kubernetes lease:%s for node group %d", nodeGroupMap.NodeGroup.LeaseName, nodeGroupMap.NodeGroupID)
				err := deleteKubernetesLease(dbHbCtx, vcpLeaseNameSpace, nodeGroupMap.NodeGroup.LeaseName)
				if err != nil {
					if strings.Contains(err.Error(), "not found") {
						logger.Warnf("kubernetes lease not found for node group %d: %v", nodeGroupMap.NodeGroupID, err)
						continue
					}
					logger.Errorf("Failed to delete kubernetes lease for node group %d: %v", nodeGroupMap.NodeGroupID, err)
					return err
				}
				// Delete lease table
				err = se.DeleteNodeGroup(dbHbCtx, nodeGroupMap.NodeGroupID)
				if err != nil {
					logger.Errorf("Failed to delete node group %d: %v", nodeGroupMap.NodeGroupID, err)
					return err
				}
			} else {
				logger.Warnf("NodeGroup is nil for NodeGroupID: %d", nodeGroupMap.NodeGroupID)
			}
		}
	}
	return nil
}

func (unRegisterNodeToHarvest *UnRegisterNodeFromHarvestActivity) AlertHarvestUnRegisterFailure(ctx context.Context, errorDetails string) error {
	metrics.IncJobStatusCounter(ctx, errorDetails, "failed to un-register nodes from harvest farm")
	return nil
}

// ListAllMapsWithDeletedNodesParams optionally configures page size for the internal keyset loop. Zero = default.
type ListAllMapsWithDeletedNodesParams struct {
	PageSize int // limit per internal page; default 100
}

// ListAllMapsWithDeletedNodesResult returns the full list of node group maps whose node is DELETED.
type ListAllMapsWithDeletedNodesResult struct {
	MapsToReconcile []*datamodel.NodeNodeGroupMap
}

const defaultListAllPageSize = 100
const maxListAllPageIterations = 10000

// ListAllMapsWithDeletedNodes runs the keyset pagination loop inside the activity: fetches all pages
// of non-deleted node group maps, filters to those whose node is in DELETED state, and returns
// the entire list. Use with ReconcileNodeGroupMapsBatch so the workflow has no loop.
func (a *UnRegisterNodeFromHarvestActivity) ListAllMapsWithDeletedNodes(
	ctx context.Context,
	params *ListAllMapsWithDeletedNodesParams,
) (*ListAllMapsWithDeletedNodesResult, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "ListAllMapsWithDeletedNodes started")

	pageSize := defaultListAllPageSize
	if params != nil && params.PageSize > 0 {
		pageSize = params.PageSize
	}

	se := a.SE
	var all []*datamodel.NodeNodeGroupMap
	afterID := int64(0)
	iterations := 0

	for iterations < maxListAllPageIterations {
		iterations++
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Listing page after ID %d", afterID))

		maps, err := se.ListNodeNodeGroupMapAfterID(ctx, false, afterID, pageSize)
		if err != nil {
			logger.Warnf("failed to list node group maps: %v", err)
			return nil, err
		}
		if len(maps) == 0 {
			break
		}
		lastID := maps[len(maps)-1].ID

		for i, m := range maps {
			activity.RecordHeartbeat(ctx, fmt.Sprintf("Checking node %d/%d (page)", i+1, len(maps)))

			node, err := se.GetNodeByID(ctx, m.NodeID)
			if err != nil {
				if errors.IsNotFoundErr(err) {
					logger.Warnf("node ID %d for node group map %d not found, skipping", m.NodeID, m.ID)
					continue
				}
				logger.Warnf("GetNodeByID failed for node %d (node group map %d): %v", m.NodeID, m.ID, err)
				return nil, err
			}
			if node.State != datamodel.LifeCycleStateDeleted {
				continue
			}
			all = append(all, m)
		}

		if len(maps) < pageSize {
			break
		}
		afterID = lastID
	}

	logger.Infof("ListAllMapsWithDeletedNodes collected %d maps to reconcile", len(all))
	return &ListAllMapsWithDeletedNodesResult{MapsToReconcile: all}, nil
}

// ReconcileNodeGroupMapsBatchParams holds the list of node group maps to reconcile (Harvest delete + DB soft-delete).
type ReconcileNodeGroupMapsBatchParams struct {
	Maps []*datamodel.NodeNodeGroupMap
}

// ReconcileNodeGroupMapsBatchResult holds the number of records successfully reconciled.
type ReconcileNodeGroupMapsBatchResult struct {
	Reconciled int
}

// ReconcileNodeGroupMapsBatch issues Harvest delete and DB soft-delete for each given node group map.
// Call after ListAllMapsWithDeletedNodes; input maps are expected to have NodeGroup preloaded.
func (a *UnRegisterNodeFromHarvestActivity) ReconcileNodeGroupMapsBatch(
	ctx context.Context,
	params *ReconcileNodeGroupMapsBatchParams,
) (*ReconcileNodeGroupMapsBatchResult, error) {
	logger := util.GetLogger(ctx)
	activity.RecordHeartbeat(ctx, "ReconcileNodeGroupMapsBatch started")

	if params == nil || len(params.Maps) == 0 {
		return &ReconcileNodeGroupMapsBatchResult{}, nil
	}

	se := a.SE
	var reconciled int
	for i, m := range params.Maps {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("Reconciling %d/%d", i+1, len(params.Maps)))

		if m.NodeGroup != nil && len(m.NodeGroup.LeaseName) > 0 {
			if err := deleteHarvestPollerFromFarm(ctx, m.NodeGroup.LeaseName, m.NodeID); err != nil {
				logger.Warnf("failed to delete poller from harvest for node group map %d (node %d): %v", m.ID, m.NodeID, err)
				continue
			}
		}

		if err := se.DeleteNodeNodeGroupMap(ctx, m.ID); err != nil {
			logger.Warnf("failed to mark node group map %d as deleted: %v", m.ID, err)
			continue
		}
		reconciled++
		logger.Infof("reconciled node group map %d (node %d deleted): removed from Harvest and marked deleted", m.ID, m.NodeID)
	}

	return &ReconcileNodeGroupMapsBatchResult{Reconciled: reconciled}, nil
}
