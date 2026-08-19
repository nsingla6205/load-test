package activities

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"gorm.io/gorm"
)

const (
	harvestDriftReconcileRetryLimit           = 5
	harvestDriftCheckpointAppConfigKey        = "harvest_poller_drift_reconcile_last_successful_run_start_at"
	harvestRetryFlowCreate                    = "create"
	harvestRetryFlowDelete                    = "delete"
	harvestDriftReconcileMapPageSize          = 100
	maxHarvestDriftReconcileMapPageIterations = 10000
)

var fetchOnTapCredentialsForDriftReconcile = fetchOnTapCredentials

type harvestDriftPoolCache struct {
	byNodeID map[int64]*datamodel.Pool
	byID     map[int64]*datamodel.Pool
}

func newHarvestDriftPoolCache() *harvestDriftPoolCache {
	return &harvestDriftPoolCache{
		byNodeID: make(map[int64]*datamodel.Pool),
		byID:     make(map[int64]*datamodel.Pool),
	}
}

type harvestPollerInventory struct {
	FileEntries  []HarvestPollerFileEntry
	InventorySet map[string]struct{}
	EmptyLeases  []string
}

var (
	harvestConfigListPath                           = env.GetString("HARVEST_CONFIG_LIST_PATH", "/config/list")
	driftReconcileLeaseNamespace                    = env.GetString("LEASE_NAMESPACE", "vcp")
	deleteKubernetesLeaseForDriftReconcile          = utils.DeleteKubernetesLease
	deleteHarvestLeaseConfigFolderForDriftReconcile = deleteHarvestLeaseConfigFolder
)

type HarvestPollerDriftReconcileActivity struct {
	SE database.Storage
}

type HarvestPollerFileEntry struct {
	LeaseName string
	FileName  string
	NodeID    int64
}

type HarvestPollerDriftReconcileResult struct {
	DeletedYAMLs          int
	DeletedLeases         int
	SoftDeletedMaps       int
	SoftDeletedNodeGroups int
	CreatedYAMLs          int
	RetryQueued           int
	RetryExhausted        int
	OrphanYAMLHandled     int
}

func (a *HarvestPollerDriftReconcileActivity) ReconcileHarvestPollerDrift(ctx context.Context) (*HarvestPollerDriftReconcileResult, error) {
	logger := util.GetLogger(ctx)
	runStart := time.Now().UTC()
	logger.Infof("harvest drift reconcile run started: runStart=%s", runStart.Format(time.RFC3339Nano))

	windowStart, fullHistoricalScan, err := a.getWindowStart(ctx, runStart)
	if err != nil {
		return nil, err
	}
	if fullHistoricalScan {
		logger.Infof("harvest drift reconcile window: full historical scan to=%s", runStart.Format(time.RFC3339Nano))
	} else {
		logger.Infof("harvest drift reconcile window: from=%s to=%s", windowStart.Format(time.RFC3339Nano), runStart.Format(time.RFC3339Nano))
	}

	inventory, err := listHarvestPollerInventory(ctx)
	if err != nil {
		return nil, err
	}
	inventoryEntries := inventory.FileEntries
	inventorySet := inventory.InventorySet
	logger.Infof(
		"harvest drift reconcile: inventory yaml entries=%d empty leases=%d",
		len(inventoryEntries),
		len(inventory.EmptyLeases),
	)

	result := &HarvestPollerDriftReconcileResult{}
	leaseCleanupCandidates := make(map[string]int64)
	for i, item := range inventoryEntries {
		recordDriftReconcileHeartbeat(ctx, fmt.Sprintf("delete pass: entry %d/%d lease=%s node=%d", i+1, len(inventoryEntries), item.LeaseName, item.NodeID))
		handled, orphanHandled, nodeGroupID, deleteErr := a.handleInventoryDeleteCandidate(ctx, item)
		if deleteErr != nil {
			logger.Warnf("failed handling delete candidate lease=%s node=%d: %v", item.LeaseName, item.NodeID, deleteErr)
			continue
		}
		if handled {
			result.DeletedYAMLs++
			if orphanHandled {
				result.OrphanYAMLHandled++
			}
			delete(inventorySet, pollerInventoryKey(item.LeaseName, item.NodeID))
			recordLeaseCleanupCandidate(leaseCleanupCandidates, item.LeaseName, nodeGroupID)
		}
	}

	softDeletedMaps, extraDeletedYAMLs, mapCleanupErr := a.cleanupMapsForDeletedPools(ctx, inventorySet, leaseCleanupCandidates)
	result.SoftDeletedMaps = softDeletedMaps
	result.DeletedYAMLs += extraDeletedYAMLs
	if mapCleanupErr != nil {
		logger.Warnf("harvest drift reconcile: deleted-pool map cleanup failed: %v", mapCleanupErr)
	}

	deletedLeases, softDeletedNodeGroups, leaseCleanupErr := a.cleanupEmptyLeasesAfterDeletePass(ctx, inventorySet, leaseCleanupCandidates, inventory.EmptyLeases)
	result.DeletedLeases = deletedLeases
	result.SoftDeletedNodeGroups = softDeletedNodeGroups
	if leaseCleanupErr != nil {
		logger.Warnf("harvest drift reconcile: empty lease cleanup pass failed: %v", leaseCleanupErr)
	}

	created, retryQueued, retryExhausted, createErr := a.reconcileReadyPoolsAndRetries(ctx, inventorySet, windowStart, runStart, fullHistoricalScan)
	result.CreatedYAMLs += created
	result.RetryQueued += retryQueued
	result.RetryExhausted += retryExhausted
	if createErr != nil {
		logger.Errorf(
			"harvest drift reconcile create pass failed after partial run: deleted=%d deletedLeases=%d created=%d retryQueued=%d retryExhausted=%d err=%v",
			result.DeletedYAMLs, result.DeletedLeases, result.CreatedYAMLs, result.RetryQueued, result.RetryExhausted, createErr,
		)
		return nil, createErr
	}

	if err := a.SE.UpsertAppConfig(ctx, harvestDriftCheckpointAppConfigKey, runStart.Format(time.RFC3339Nano)); err != nil {
		logger.Errorf(
			"harvest drift reconcile checkpoint persist failed after partial run: deleted=%d deletedLeases=%d created=%d retryQueued=%d retryExhausted=%d err=%v",
			result.DeletedYAMLs, result.DeletedLeases, result.CreatedYAMLs, result.RetryQueued, result.RetryExhausted, err,
		)
		return nil, err
	}

	logger.Infof(
		"Harvest poller drift reconcile completed: deleted=%d deletedLeases=%d softDeletedMaps=%d softDeletedNodeGroups=%d created=%d retryQueued=%d retryExhausted=%d",
		result.DeletedYAMLs, result.DeletedLeases, result.SoftDeletedMaps, result.SoftDeletedNodeGroups, result.CreatedYAMLs, result.RetryQueued, result.RetryExhausted,
	)
	return result, nil
}

func recordDriftReconcileHeartbeat(ctx context.Context, msg string) {
	defer func() { _ = recover() }()
	activity.RecordHeartbeat(ctx, msg)
}

func (a *HarvestPollerDriftReconcileActivity) handleInventoryDeleteCandidate(ctx context.Context, entry HarvestPollerFileEntry) (bool, bool, int64, error) {
	logger := util.GetLogger(ctx)
	if strings.TrimSpace(entry.LeaseName) == "" {
		logger.Warnf("harvest drift reconcile: skip delete candidate node=%d; empty lease name in inventory", entry.NodeID)
		return false, false, 0, nil
	}
	mapRow, mapErr := a.SE.GetNodeNodeGroupMapByNodeID(ctx, entry.NodeID)
	if mapErr != nil && !utilerrors.IsNotFoundErr(mapErr) {
		return false, false, 0, mapErr
	}

	// Case: map exists and is soft-deleted -> direct cleanup when delete retry remains.
	if mapErr == nil && mapRow.DeletedAt != nil && mapRow.DeletedAt.Valid {
		if !shouldProcessDeleteCandidate(mapRow) {
			logger.Infof(
				"harvest drift reconcile: skip delete candidate lease=%s node=%d; delete retry exhausted",
				entry.LeaseName,
				entry.NodeID,
			)
			return false, false, nodeGroupIDFromMap(mapRow), nil
		}
		handled, orphan, err := a.processDeleteCandidate(ctx, entry, mapRow, false)
		return handled, orphan, nodeGroupIDFromMap(mapRow), err
	}

	// Case: map exists and active -> pool state decides cleanup.
	if mapErr == nil {
		pool, poolErr := a.resolvePoolForMap(ctx, mapRow)
		if poolErr != nil {
			return false, false, 0, poolErr
		}
		if pool == nil {
			logger.Infof(
				"harvest drift reconcile: skip delete candidate lease=%s node=%d; pool is unresolved",
				entry.LeaseName,
				entry.NodeID,
			)
			return false, false, 0, nil
		}
		if !isPoolDeleted(pool) {
			if hasFlowRetryRemaining(mapRow, harvestRetryFlowDelete) {
				if err := a.clearStaleDeleteRetry(ctx, mapRow); err != nil {
					return false, false, 0, err
				}
				logger.Infof(
					"harvest drift reconcile: cleared stale delete retry lease=%s node=%d; pool not deleted",
					entry.LeaseName,
					entry.NodeID,
				)
			} else {
				logger.Infof(
					"harvest drift reconcile: skip delete candidate lease=%s node=%d; pool not deleted",
					entry.LeaseName,
					entry.NodeID,
				)
			}
			return false, false, 0, nil
		}
		if !shouldProcessDeleteCandidate(mapRow) {
			logger.Infof(
				"harvest drift reconcile: skip delete candidate lease=%s node=%d; delete retry exhausted",
				entry.LeaseName,
				entry.NodeID,
			)
			return false, false, nodeGroupIDFromMap(mapRow), nil
		}
		handled, orphan, err := a.processDeleteCandidate(ctx, entry, mapRow, false)
		return handled, orphan, nodeGroupIDFromMap(mapRow), err
	}

	// Case: orphan YAML (no map row) => delete stale inventory artifact.
	logger.Infof("harvest drift reconcile: deleting orphan yaml lease=%s node=%d; no node_node_group_map found", entry.LeaseName, entry.NodeID)
	handled, orphan, err := a.deleteCandidateAndPersist(ctx, entry, nil, true)
	return handled, orphan, 0, err
}

func (a *HarvestPollerDriftReconcileActivity) reconcileReadyPoolsAndRetries(ctx context.Context, inventorySet map[string]struct{}, windowStart, windowEnd time.Time, fullHistoricalScan bool) (int, int, int, error) {
	readyPoolIDs, err := a.listReadyPoolIDsInWindow(ctx, windowStart, windowEnd, fullHistoricalScan)
	if err != nil {
		return 0, 0, 0, err
	}
	logger := util.GetLogger(ctx)
	logger.Infof("harvest drift reconcile: ready pools discovered=%d", len(readyPoolIDs))
	readyPools := make(map[int64]*datamodel.Pool, len(readyPoolIDs))
	for _, poolID := range readyPoolIDs {
		pool, poolErr := a.SE.GetPoolByID(ctx, poolID)
		if poolErr != nil {
			return 0, 0, 0, poolErr
		}
		readyPools[poolID] = pool
	}

	allActiveMaps, err := a.listActiveNodeNodeGroupMapsPaginated(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	logger.Infof("harvest drift reconcile: active node_node_group_maps discovered=%d", len(allActiveMaps))

	poolCache := newHarvestDriftPoolCache()
	for id, pool := range readyPools {
		poolCache.byID[id] = pool
	}

	candidateMaps := make(map[int64]*datamodel.NodeNodeGroupMap)
	retryPending := 0
	for _, m := range allActiveMaps {
		if m == nil {
			continue
		}
		if hasFlowRetryRemaining(m, harvestRetryFlowCreate) {
			candidateMaps[m.NodeID] = m
			retryPending++
			continue
		}
		pool, poolErr := a.resolvePoolForMapCached(ctx, m, poolCache)
		if poolErr != nil {
			return 0, 0, 0, poolErr
		}
		if pool == nil || !isPoolEligibleForHarvestCreateDrift(pool) {
			continue
		}
		if _, ok := readyPools[pool.ID]; ok {
			candidateMaps[m.NodeID] = m
		}
	}
	logger.Infof("harvest drift reconcile: retry pending mappings discovered=%d", retryPending)

	candidateNodeIDs := make([]int64, 0, len(candidateMaps))
	for nodeID := range candidateMaps {
		candidateNodeIDs = append(candidateNodeIDs, nodeID)
	}
	sort.Slice(candidateNodeIDs, func(i, j int) bool { return candidateNodeIDs[i] < candidateNodeIDs[j] })

	created := 0
	retryQueued := 0
	retryExhausted := 0

	registerActivity := &RegisterNodeToHarvestFarmActivity{SE: a.SE}

	for i, nodeID := range candidateNodeIDs {
		recordDriftReconcileHeartbeat(ctx, fmt.Sprintf("create pass: candidate %d/%d node=%d", i+1, len(candidateNodeIDs), nodeID))
		logger.Debugf("harvest drift reconcile: processing candidate node id=%d", nodeID)
		mapping := candidateMaps[nodeID]
		if mapping == nil {
			continue
		}
		pool, poolErr := a.resolvePoolForMapCached(ctx, mapping, poolCache)
		if poolErr != nil {
			return created, retryQueued, retryExhausted, poolErr
		}
		if pool == nil {
			logger.Debugf("harvest drift reconcile: no pool resolved for map node=%d, skipping create path", nodeID)
			continue
		}
		if isPoolDeleted(pool) {
			if hasFlowRetryRemaining(mapping, harvestRetryFlowCreate) {
				if err := a.markRetryTerminal(ctx, mapping, "pool is deleted; stopping create retry"); err != nil {
					return created, retryQueued, retryExhausted, err
				}
			}
			continue
		}
		if !isPoolEligibleForHarvestCreateDrift(pool) {
			if hasFlowRetryRemaining(mapping, harvestRetryFlowCreate) {
				if err := a.markRetryTerminal(ctx, mapping, fmt.Sprintf("pool state %q is not eligible for create retry; stopping", pool.State)); err != nil {
					return created, retryQueued, retryExhausted, err
				}
			}
			continue
		}

		if mapping.NodeGroup == nil {
			mapping.NodeGroup, err = a.SE.GetNodeGroup(ctx, mapping.NodeGroupID)
			if err != nil {
				return created, retryQueued, retryExhausted, err
			}
		}

		key := pollerInventoryKey(mapping.NodeGroup.LeaseName, mapping.NodeID)
		_, exists := inventorySet[key]
		shouldRetry := hasFlowRetryRemaining(mapping, harvestRetryFlowCreate)
		if exists && !shouldRetry {
			logger.Debugf("harvest drift reconcile: yaml present and no retry pending lease=%s node=%d, skipping create", mapping.NodeGroup.LeaseName, mapping.NodeID)
			continue
		}

		stillActive, activeErr := a.nodeNodeGroupMapStillActiveForUpload(ctx, mapping)
		if activeErr != nil {
			return created, retryQueued, retryExhausted, activeErr
		}
		if !stillActive {
			leaseName := ""
			if mapping.NodeGroup != nil {
				leaseName = mapping.NodeGroup.LeaseName
			}
			logger.Infof(
				"harvest drift reconcile: skip upload lease=%s node=%d map=%d; node_node_group_map no longer active",
				leaseName,
				mapping.NodeID,
				mapping.ID,
			)
			continue
		}

		updatedMappings, err := registerActivity.ValidateAndCreateKubernetesLease(ctx, []*datamodel.NodeNodeGroupMap{mapping})
		if err != nil {
			queued, exhausted, stateErr := a.markRetryFailure(ctx, mapping, fmt.Sprintf("lease validation failed: %v", err))
			retryQueued += queued
			retryExhausted += exhausted
			if stateErr != nil {
				return created, retryQueued, retryExhausted, stateErr
			}
			continue
		}
		if len(updatedMappings) > 0 {
			mapping = updatedMappings[0]
		}

		var credentials *vlm.OntapCredentials
		if !smHarvestAuthEnabled {
			credentials, err = fetchOnTapCredentialsForDriftReconcile(ctx, pool)
			if err != nil {
				queued, exhausted, stateErr := a.markRetryFailure(ctx, mapping, fmt.Sprintf("credential fetch failed: %v", err))
				retryQueued += queued
				retryExhausted += exhausted
				if stateErr != nil {
					return created, retryQueued, retryExhausted, stateErr
				}
				continue
			}
		}

		uploadPool, ready, readyErr := a.readyForHarvestCreateUpload(ctx, mapping)
		if readyErr != nil {
			return created, retryQueued, retryExhausted, readyErr
		}
		if !ready {
			continue
		}

		if err := uploadHarvestNodeMapping(ctx, a.SE, mapping, harvestConfigUploadURL(), uploadPool, credentials, nil, true); err != nil {
			logger.Warnf("harvest drift reconcile: upload failed lease=%s node=%d err=%v", mapping.NodeGroup.LeaseName, mapping.NodeID, err)
			queued, exhausted, stateErr := a.markRetryFailure(ctx, mapping, err.Error())
			retryQueued += queued
			retryExhausted += exhausted
			if stateErr != nil {
				return created, retryQueued, retryExhausted, stateErr
			}
			continue
		}

		if err := a.markRetrySuccess(ctx, mapping); err != nil {
			return created, retryQueued, retryExhausted, err
		}
		logger.Infof("harvest drift reconcile: created/backfilled yaml lease=%s node=%d", mapping.NodeGroup.LeaseName, mapping.NodeID)
		created++
		inventorySet[key] = struct{}{}
	}

	return created, retryQueued, retryExhausted, nil
}

func (a *HarvestPollerDriftReconcileActivity) nodeNodeGroupMapStillActiveForUpload(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) (bool, error) {
	if mapping == nil {
		return false, nil
	}
	activeMap, err := a.SE.GetActiveNodeNodeGroupMapByNodeID(ctx, mapping.NodeID, nil)
	if err != nil {
		if utilerrors.IsNotFoundErr(err) {
			return false, nil
		}
		return false, err
	}
	return activeMap != nil && activeMap.ID == mapping.ID, nil
}

// readyForHarvestCreateUpload re-validates mapping liveness and current pool eligibility
// immediately before an external YAML upload. Unregister can soft-delete the map and remove
// YAML while lease validation or credential fetch runs earlier in the loop.
func (a *HarvestPollerDriftReconcileActivity) readyForHarvestCreateUpload(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) (*datamodel.Pool, bool, error) {
	logger := util.GetLogger(ctx)

	stillActive, activeErr := a.nodeNodeGroupMapStillActiveForUpload(ctx, mapping)
	if activeErr != nil {
		return nil, false, activeErr
	}
	if !stillActive {
		leaseName := ""
		if mapping != nil && mapping.NodeGroup != nil {
			leaseName = mapping.NodeGroup.LeaseName
		}
		nodeID := int64(0)
		mapID := int64(0)
		if mapping != nil {
			nodeID = mapping.NodeID
			mapID = mapping.ID
		}
		logger.Infof(
			"harvest drift reconcile: skip upload lease=%s node=%d map=%d; node_node_group_map no longer active before upload",
			leaseName,
			nodeID,
			mapID,
		)
		return nil, false, nil
	}

	pool, poolErr := a.resolvePoolForMap(ctx, mapping)
	if poolErr != nil {
		return nil, false, poolErr
	}
	if pool == nil || isPoolDeleted(pool) {
		if hasFlowRetryRemaining(mapping, harvestRetryFlowCreate) {
			if err := a.markRetryTerminal(ctx, mapping, "pool is deleted; stopping create retry"); err != nil {
				return nil, false, err
			}
		}
		logger.Infof("harvest drift reconcile: skip upload node=%d; pool deleted before upload", mapping.NodeID)
		return nil, false, nil
	}
	if !isPoolEligibleForHarvestCreateDrift(pool) {
		if hasFlowRetryRemaining(mapping, harvestRetryFlowCreate) {
			if err := a.markRetryTerminal(ctx, mapping, fmt.Sprintf("pool state %q is not eligible for create retry; stopping", pool.State)); err != nil {
				return nil, false, err
			}
		}
		logger.Infof(
			"harvest drift reconcile: skip upload node=%d; pool state %q not eligible before upload",
			mapping.NodeID,
			pool.State,
		)
		return nil, false, nil
	}
	return pool, true, nil
}

func (a *HarvestPollerDriftReconcileActivity) markRetryFailure(ctx context.Context, mapping *datamodel.NodeNodeGroupMap, msg string) (int, int, error) {
	if err := setHarvestNodeMapCreateRetryState(ctx, a.SE, mapping, false, msg); err != nil {
		return 0, 0, err
	}
	state := flowRetryState(mapping, harvestRetryFlowCreate)
	if state != nil && state.ShouldRetry {
		return 1, 0, nil
	}
	return 0, 1, nil
}

func (a *HarvestPollerDriftReconcileActivity) markRetrySuccess(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) error {
	return setHarvestNodeMapCreateRetryState(ctx, a.SE, mapping, true, "success")
}

func (a *HarvestPollerDriftReconcileActivity) markRetryTerminal(ctx context.Context, mapping *datamodel.NodeNodeGroupMap, msg string) error {
	return setHarvestNodeMapFlowRetryTerminal(ctx, a.SE, mapping, harvestRetryFlowCreate, msg)
}

// setHarvestNodeMapCreateRetryState persists harvest_retry_state.create on a node_node_group_map
// row. Used by RegisterNodeToHarvestFarmActivity so drift reconcile can pick up nodes whose
// register-to-harvest pipeline failed after Temporal's own activity retries were exhausted.
func setHarvestNodeMapCreateRetryState(ctx context.Context, se database.Storage, mapping *datamodel.NodeNodeGroupMap, success bool, msg string) error {
	sourceRunID := harvestSourceRunID(ctx)
	return updateHarvestNodeMapRetryStateLocked(ctx, se, mapping, func(current *datamodel.HarvestRetryState) (*datamodel.HarvestRetryState, error) {
		next := cloneHarvestRetryState(current)
		state := flowRetryStateFromRetryState(next, harvestRetryFlowCreate)
		if state == nil {
			state = &datamodel.HarvestFlowRetryState{}
		} else {
			state = cloneHarvestFlowRetryState(state)
		}
		applyHarvestFlowRetryOutcome(state, success, msg, sourceRunID)
		setFlowRetryStateOnRetryState(next, harvestRetryFlowCreate, state)
		return next, nil
	})
}

// setHarvestNodeMapDeleteRetryState persists harvest_retry_state.delete on a node_node_group_map
// row. Used by UnRegisterNodeFromHarvestActivity; by the time this runs the map row is already
// soft-deleted (DeleteNodeGroupMapping precedes DeletePollersFromHarvestFarm), so the update goes
// through the unscoped UpdateNodeNodeGroupMapRetryState path instead of UpdateNodeNodeGroupMap.
func setHarvestNodeMapDeleteRetryState(ctx context.Context, se database.Storage, mapping *datamodel.NodeNodeGroupMap, success bool, msg string) error {
	sourceRunID := harvestSourceRunID(ctx)
	return updateHarvestNodeMapRetryStateLocked(ctx, se, mapping, func(current *datamodel.HarvestRetryState) (*datamodel.HarvestRetryState, error) {
		next := cloneHarvestRetryState(current)
		state := flowRetryStateFromRetryState(next, harvestRetryFlowDelete)
		if state == nil {
			state = &datamodel.HarvestFlowRetryState{}
		} else {
			state = cloneHarvestFlowRetryState(state)
		}
		applyHarvestFlowRetryOutcome(state, success, msg, sourceRunID)
		setFlowRetryStateOnRetryState(next, harvestRetryFlowDelete, state)
		return next, nil
	})
}

// setHarvestNodeMapFlowRetryTerminal clears should_retry for one flow without incrementing retries.
func setHarvestNodeMapFlowRetryTerminal(ctx context.Context, se database.Storage, mapping *datamodel.NodeNodeGroupMap, flow, msg string) error {
	return updateHarvestNodeMapRetryStateLocked(ctx, se, mapping, func(current *datamodel.HarvestRetryState) (*datamodel.HarvestRetryState, error) {
		next := cloneHarvestRetryState(current)
		state := flowRetryStateFromRetryState(next, flow)
		if state == nil {
			state = &datamodel.HarvestFlowRetryState{}
		} else {
			state = cloneHarvestFlowRetryState(state)
		}
		state.LastRunMsg = msg
		state.LastAttemptAt = time.Now().UTC()
		state.ShouldRetry = false
		setFlowRetryStateOnRetryState(next, flow, state)
		return next, nil
	})
}

func updateHarvestNodeMapRetryStateLocked(
	ctx context.Context,
	se database.Storage,
	mapping *datamodel.NodeNodeGroupMap,
	mutate database.HarvestRetryStateMutator,
) error {
	if mapping == nil {
		return nil
	}
	mapID, err := nodeNodeGroupMapIDForRetryUpdate(ctx, se, mapping)
	if err != nil {
		return err
	}
	return se.UpdateNodeNodeGroupMapRetryState(ctx, mapID, func(current *datamodel.HarvestRetryState) (*datamodel.HarvestRetryState, error) {
		next, mutateErr := mutate(current)
		if mutateErr != nil {
			return nil, mutateErr
		}
		mapping.RetryState = cloneHarvestRetryState(next)
		return next, nil
	})
}

func nodeNodeGroupMapIDForRetryUpdate(ctx context.Context, se database.Storage, mapping *datamodel.NodeNodeGroupMap) (int64, error) {
	if mapping == nil {
		return 0, nil
	}
	if mapping.ID != 0 {
		return mapping.ID, nil
	}
	row, err := fetchNodeNodeGroupMapForRetryMerge(ctx, se, mapping)
	if err != nil {
		return 0, err
	}
	if row == nil {
		return 0, nil
	}
	mapping.ID = row.ID
	return row.ID, nil
}

func (a *HarvestPollerDriftReconcileActivity) markDeleteRetryFailure(ctx context.Context, mapping *datamodel.NodeNodeGroupMap, msg string) error {
	return setHarvestNodeMapDeleteRetryState(ctx, a.SE, mapping, false, msg)
}

func (a *HarvestPollerDriftReconcileActivity) markDeleteRetrySuccess(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) error {
	return setHarvestNodeMapDeleteRetryState(ctx, a.SE, mapping, true, "success")
}

func (a *HarvestPollerDriftReconcileActivity) clearStaleDeleteRetry(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) error {
	return setHarvestNodeMapFlowRetryTerminal(ctx, a.SE, mapping, harvestRetryFlowDelete, "pool not deleted; clearing stale delete retry")
}

func (a *HarvestPollerDriftReconcileActivity) listActiveNodeNodeGroupMapsPaginated(ctx context.Context) ([]*datamodel.NodeNodeGroupMap, error) {
	all := make([]*datamodel.NodeNodeGroupMap, 0)
	afterID := int64(0)
	for pageNum := 0; pageNum < maxHarvestDriftReconcileMapPageIterations; pageNum++ {
		recordDriftReconcileHeartbeat(ctx, fmt.Sprintf("listing node_node_group_maps page=%d after_id=%d", pageNum+1, afterID))
		page, err := a.SE.ListNodeNodeGroupMapAfterID(ctx, false, afterID, harvestDriftReconcileMapPageSize)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		all = append(all, page...)
		afterID = page[len(page)-1].ID
		if len(page) < harvestDriftReconcileMapPageSize {
			break
		}
	}
	return all, nil
}

func (a *HarvestPollerDriftReconcileActivity) resolvePoolForMapCached(
	ctx context.Context,
	mapRow *datamodel.NodeNodeGroupMap,
	cache *harvestDriftPoolCache,
) (*datamodel.Pool, error) {
	if mapRow == nil || mapRow.NodeID == 0 {
		return nil, nil
	}
	if cache != nil {
		if pool, ok := cache.byNodeID[mapRow.NodeID]; ok {
			return pool, nil
		}
	}
	pool, err := a.resolvePoolForMap(ctx, mapRow)
	if err != nil {
		return nil, err
	}
	if cache != nil && pool != nil {
		cache.byNodeID[mapRow.NodeID] = pool
		cache.byID[pool.ID] = pool
	}
	return pool, nil
}

func (a *HarvestPollerDriftReconcileActivity) deleteCandidateAndPersist(
	ctx context.Context,
	entry HarvestPollerFileEntry,
	mapRow *datamodel.NodeNodeGroupMap,
	isOrphan bool,
) (bool, bool, error) {
	if err := deleteHarvestPollerFromFarm(ctx, entry.LeaseName, entry.NodeID); err != nil {
		return false, false, err
	}
	if mapRow != nil && (mapRow.DeletedAt == nil || !mapRow.DeletedAt.Valid) {
		if err := a.SE.DeleteNodeNodeGroupMap(ctx, mapRow.ID); err != nil {
			return false, false, err
		}
	}
	return true, isOrphan, nil
}

func (a *HarvestPollerDriftReconcileActivity) processDeleteCandidate(
	ctx context.Context,
	entry HarvestPollerFileEntry,
	mapRow *datamodel.NodeNodeGroupMap,
	isOrphan bool,
) (bool, bool, error) {
	handled, orphanHandled, err := a.deleteCandidateAndPersist(ctx, entry, mapRow, isOrphan)
	if err != nil {
		if mapRow != nil {
			if stateErr := a.markDeleteRetryFailure(ctx, mapRow, err.Error()); stateErr != nil {
				return false, false, stateErr
			}
		}
		return false, false, err
	}
	// Persist delete retry success after YAML cleanup (and map soft-delete when applicable).
	if mapRow != nil {
		if stateErr := a.markDeleteRetrySuccess(ctx, mapRow); stateErr != nil {
			return false, false, stateErr
		}
	}
	return handled, orphanHandled, nil
}

func nodeGroupIDFromMap(mapRow *datamodel.NodeNodeGroupMap) int64 {
	if mapRow == nil {
		return 0
	}
	return mapRow.NodeGroupID
}

func recordLeaseCleanupCandidate(candidates map[string]int64, leaseName string, nodeGroupID int64) {
	leaseName = strings.TrimSpace(leaseName)
	if leaseName == "" {
		return
	}
	if existing, ok := candidates[leaseName]; !ok || (existing == 0 && nodeGroupID != 0) {
		if nodeGroupID != 0 {
			candidates[leaseName] = nodeGroupID
			return
		}
		if !ok {
			candidates[leaseName] = 0
		}
	}
}

func countInventoryEntriesForLease(inventorySet map[string]struct{}, leaseName string) int {
	leaseName = strings.TrimSpace(leaseName)
	if leaseName == "" {
		return 0
	}
	prefix := leaseName + "|"
	count := 0
	for key := range inventorySet {
		if strings.HasPrefix(key, prefix) {
			count++
		}
	}
	return count
}

// nodeGroupIDsByLeaseName indexes all rows from a single ListNodeGroupsWithPollerCounts
// call so bucket-reported empty leases can reuse resolved node group IDs without N+1 lookups.
func nodeGroupIDsByLeaseName(rows []datamodel.NodeGroupPollerCount) map[string]int64 {
	out := make(map[string]int64, len(rows))
	for _, row := range rows {
		lease := strings.TrimSpace(row.LeaseName)
		if lease == "" {
			continue
		}
		if existing, ok := out[lease]; !ok || (existing == 0 && row.NodeGroupID != 0) {
			out[lease] = row.NodeGroupID
		}
	}
	return out
}

// cleanupMapsForDeletedPools soft-deletes active node_node_group_maps whose pool is deleted,
// regardless of YAML inventory presence. Deletes YAML first when still listed in inventorySet.
func (a *HarvestPollerDriftReconcileActivity) cleanupMapsForDeletedPools(
	ctx context.Context,
	inventorySet map[string]struct{},
	leaseCleanupCandidates map[string]int64,
) (softDeletedMaps int, deletedYAMLs int, err error) {
	logger := util.GetLogger(ctx)
	maps, err := a.listActiveNodeNodeGroupMapsPaginated(ctx)
	if err != nil {
		return 0, 0, err
	}
	poolCache := newHarvestDriftPoolCache()
	for i, mapping := range maps {
		if mapping == nil {
			continue
		}
		recordDriftReconcileHeartbeat(ctx, fmt.Sprintf("deleted-pool map cleanup: %d/%d node=%d", i+1, len(maps), mapping.NodeID))
		pool, poolErr := a.resolvePoolForMapCached(ctx, mapping, poolCache)
		if poolErr != nil {
			return softDeletedMaps, deletedYAMLs, poolErr
		}
		if !isPoolDeleted(pool) {
			continue
		}
		yamlDeleted, cleanupErr := a.softDeleteMapForDeletedPool(ctx, mapping, inventorySet, leaseCleanupCandidates)
		if cleanupErr != nil {
			logger.Warnf("harvest drift reconcile: deleted-pool map cleanup failed node=%d: %v", mapping.NodeID, cleanupErr)
			continue
		}
		softDeletedMaps++
		if yamlDeleted {
			deletedYAMLs++
		}
	}
	if softDeletedMaps > 0 {
		logger.Infof("harvest drift reconcile: soft-deleted %d map(s) for deleted pools", softDeletedMaps)
	}
	return softDeletedMaps, deletedYAMLs, nil
}

func (a *HarvestPollerDriftReconcileActivity) ensureMapLeaseName(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) (string, error) {
	if mapping == nil {
		return "", nil
	}
	if mapping.NodeGroup != nil {
		if lease := strings.TrimSpace(mapping.NodeGroup.LeaseName); lease != "" {
			return lease, nil
		}
	}
	if mapping.NodeGroupID == 0 {
		return "", nil
	}
	group, err := a.SE.GetNodeGroup(ctx, mapping.NodeGroupID)
	if err != nil {
		return "", err
	}
	if group == nil {
		return "", nil
	}
	return strings.TrimSpace(group.LeaseName), nil
}

func (a *HarvestPollerDriftReconcileActivity) softDeleteMapForDeletedPool(
	ctx context.Context,
	mapping *datamodel.NodeNodeGroupMap,
	inventorySet map[string]struct{},
	leaseCleanupCandidates map[string]int64,
) (yamlDeleted bool, err error) {
	if mapping == nil {
		return false, nil
	}
	leaseName, err := a.ensureMapLeaseName(ctx, mapping)
	if err != nil {
		return false, err
	}
	if leaseName == "" {
		return false, fmt.Errorf("lease name missing for map id=%d node=%d", mapping.ID, mapping.NodeID)
	}
	key := pollerInventoryKey(leaseName, mapping.NodeID)
	if _, inInventory := inventorySet[key]; inInventory {
		if err := deleteHarvestPollerFromFarm(ctx, leaseName, mapping.NodeID); err != nil {
			return false, err
		}
		delete(inventorySet, key)
		yamlDeleted = true
	}
	if mapping.DeletedAt == nil || !mapping.DeletedAt.Valid {
		if err := a.SE.DeleteNodeNodeGroupMap(ctx, mapping.ID); err != nil {
			return yamlDeleted, err
		}
	}
	recordLeaseCleanupCandidate(leaseCleanupCandidates, leaseName, mapping.NodeGroupID)
	return yamlDeleted, nil
}

// reconcileDeletedPoolMapsOnNodeGroup soft-deletes maps on a node group only when every active map
// belongs to a deleted pool. Returns false when any live-pool map remains.
func (a *HarvestPollerDriftReconcileActivity) reconcileDeletedPoolMapsOnNodeGroup(
	ctx context.Context,
	nodeGroupID int64,
	inventorySet map[string]struct{},
	leaseCleanupCandidates map[string]int64,
) (bool, error) {
	if nodeGroupID == 0 {
		return true, nil
	}
	maps, err := a.SE.ListNodeNodeGroupMapsByNodeGroupID(ctx, nodeGroupID)
	if err != nil {
		return false, err
	}
	poolCache := newHarvestDriftPoolCache()
	for _, mapping := range maps {
		if mapping == nil || (mapping.DeletedAt != nil && mapping.DeletedAt.Valid) {
			continue
		}
		pool, poolErr := a.resolvePoolForMapCached(ctx, mapping, poolCache)
		if poolErr != nil {
			return false, poolErr
		}
		if !isPoolDeleted(pool) {
			return false, nil
		}
	}
	for _, mapping := range maps {
		if mapping == nil || (mapping.DeletedAt != nil && mapping.DeletedAt.Valid) {
			continue
		}
		pool, poolErr := a.resolvePoolForMapCached(ctx, mapping, poolCache)
		if poolErr != nil {
			return false, poolErr
		}
		if !isPoolDeleted(pool) {
			continue
		}
		if _, err := a.softDeleteMapForDeletedPool(ctx, mapping, inventorySet, leaseCleanupCandidates); err != nil {
			return false, err
		}
	}
	return true, nil
}

func (a *HarvestPollerDriftReconcileActivity) softDeleteNodeGroupIfEmpty(ctx context.Context, nodeGroupID int64, leaseName string) (bool, error) {
	if nodeGroupID == 0 {
		return false, nil
	}
	leaseName = strings.TrimSpace(leaseName)
	activePollers, err := a.SE.GetNodeGroupMapNodeCount(ctx, nodeGroupID)
	if err != nil {
		return false, err
	}
	if activePollers > 0 {
		return false, nil
	}
	group, err := a.SE.GetNodeGroup(ctx, nodeGroupID)
	if err != nil {
		return false, err
	}
	if group == nil {
		return false, nil
	}
	if leaseName != "" && strings.TrimSpace(group.LeaseName) != leaseName {
		return false, nil
	}
	if err := a.SE.DeleteNodeGroup(ctx, nodeGroupID); err != nil {
		if utilerrors.IsNotFoundErr(err) {
			return false, nil
		}
		return false, err
	}
	util.GetLogger(ctx).Infof("harvest drift reconcile: soft-deleted node group %d lease=%s", nodeGroupID, leaseName)
	return true, nil
}

func (a *HarvestPollerDriftReconcileActivity) cleanupEmptyLeasesAfterDeletePass(
	ctx context.Context,
	inventorySet map[string]struct{},
	candidates map[string]int64,
	bucketEmptyLeases []string,
) (deletedLeases int, softDeletedNodeGroups int, err error) {
	logger := util.GetLogger(ctx)
	rows, err := a.SE.ListNodeGroupsWithPollerCounts(ctx)
	if err != nil {
		return 0, 0, err
	}
	leaseToNodeGroupID := nodeGroupIDsByLeaseName(rows)
	for _, row := range rows {
		if row.Count != 0 {
			continue
		}
		lease := strings.TrimSpace(row.LeaseName)
		if lease == "" {
			continue
		}
		if countInventoryEntriesForLease(inventorySet, lease) > 0 {
			continue
		}
		recordLeaseCleanupCandidate(candidates, lease, row.NodeGroupID)
	}
	for _, lease := range bucketEmptyLeases {
		lease = strings.TrimSpace(lease)
		if lease == "" {
			continue
		}
		if countInventoryEntriesForLease(inventorySet, lease) > 0 {
			continue
		}
		recordLeaseCleanupCandidate(candidates, lease, leaseToNodeGroupID[lease])
	}

	deletedLeases = 0
	leaseIdx := 0
	for leaseName, nodeGroupID := range candidates {
		leaseIdx++
		recordDriftReconcileHeartbeat(ctx, fmt.Sprintf("lease cleanup: candidate %d/%d lease=%s", leaseIdx, len(candidates), leaseName))
		if countInventoryEntriesForLease(inventorySet, leaseName) > 0 {
			continue
		}
		cleaned, nodeGroupDeleted, cleanupErr := a.tryCleanupEmptyLease(ctx, leaseName, nodeGroupID, inventorySet, candidates, leaseToNodeGroupID)
		if cleanupErr != nil {
			logger.Warnf("harvest drift reconcile: empty lease cleanup failed lease=%s: %v", leaseName, cleanupErr)
			continue
		}
		if cleaned {
			deletedLeases++
		}
		if nodeGroupDeleted {
			softDeletedNodeGroups++
		}
	}
	return deletedLeases, softDeletedNodeGroups, nil
}

func (a *HarvestPollerDriftReconcileActivity) tryCleanupEmptyLease(
	ctx context.Context,
	leaseName string,
	nodeGroupID int64,
	inventorySet map[string]struct{},
	leaseCleanupCandidates map[string]int64,
	leaseToNodeGroupID map[string]int64,
) (cleaned bool, nodeGroupDeleted bool, err error) {
	leaseName = strings.TrimSpace(leaseName)
	if leaseName == "" {
		return false, false, nil
	}
	logger := util.GetLogger(ctx)

	if nodeGroupID == 0 {
		nodeGroupID = nodeGroupIDForLease(leaseName, leaseToNodeGroupID)
	}
	if nodeGroupID != 0 {
		activePollers, countErr := a.SE.GetNodeGroupMapNodeCount(ctx, nodeGroupID)
		if countErr != nil {
			return false, false, countErr
		}
		if activePollers > 0 {
			allDeletedPool, reconcileErr := a.reconcileDeletedPoolMapsOnNodeGroup(ctx, nodeGroupID, inventorySet, leaseCleanupCandidates)
			if reconcileErr != nil {
				return false, false, reconcileErr
			}
			if !allDeletedPool {
				logger.Infof(
					"harvest drift reconcile: skip empty lease cleanup lease=%s; node group %d still has live-pool poller(s)",
					leaseName, nodeGroupID,
				)
				return false, false, nil
			}
			activePollers, countErr = a.SE.GetNodeGroupMapNodeCount(ctx, nodeGroupID)
			if countErr != nil {
				return false, false, countErr
			}
			if activePollers > 0 {
				logger.Infof(
					"harvest drift reconcile: skip empty lease cleanup lease=%s; node group %d still has %d active poller(s) after deleted-pool map cleanup",
					leaseName, nodeGroupID, activePollers,
				)
				return false, false, nil
			}
		}
	}

	if err := deleteHarvestLeaseConfigFolderForDriftReconcile(ctx, leaseName); err != nil {
		if strings.Contains(err.Error(), "409") || strings.Contains(strings.ToLower(err.Error()), "not empty") {
			logger.Infof("harvest drift reconcile: skip k8s lease cleanup lease=%s; harvest lease folder not empty", leaseName)
			return false, false, nil
		}
		return false, false, err
	}

	leaseNS := strings.TrimSpace(driftReconcileLeaseNamespace)
	if leaseNS == "" {
		leaseNS = "vcp"
	}
	if err := deleteKubernetesLeaseForDriftReconcile(ctx, leaseNS, leaseName); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not found") {
			logger.Warnf("harvest drift reconcile: k8s lease already gone %s/%s: %v", leaseNS, leaseName, err)
			return false, false, nil
		}
		return false, false, err
	}
	logger.Infof("harvest drift reconcile: deleted empty k8s lease %s/%s", leaseNS, leaseName)

	if nodeGroupID != 0 {
		nodeGroupDeleted, err = a.softDeleteNodeGroupIfEmpty(ctx, nodeGroupID, leaseName)
		if err != nil {
			return true, false, err
		}
	}
	return true, nodeGroupDeleted, nil
}

// nodeGroupIDForLease looks up a node group ID from a prebuilt lease index (see nodeGroupIDsByLeaseName).
func nodeGroupIDForLease(leaseName string, leaseToNodeGroupID map[string]int64) int64 {
	leaseName = strings.TrimSpace(leaseName)
	if leaseName == "" || len(leaseToNodeGroupID) == 0 {
		return 0
	}
	return leaseToNodeGroupID[leaseName]
}

func (a *HarvestPollerDriftReconcileActivity) resolveNodeGroupIDForLease(ctx context.Context, leaseName string) int64 {
	if strings.TrimSpace(leaseName) == "" {
		return 0
	}
	rows, err := a.SE.ListNodeGroupsWithPollerCounts(ctx)
	if err != nil {
		return 0
	}
	return nodeGroupIDForLease(leaseName, nodeGroupIDsByLeaseName(rows))
}

func (a *HarvestPollerDriftReconcileActivity) getWindowStart(ctx context.Context, runStart time.Time) (time.Time, bool, error) {
	logger := util.GetLogger(ctx)
	cfg, err := a.SE.GetAppConfig(ctx, harvestDriftCheckpointAppConfigKey)
	if err != nil {
		if isCheckpointNotFoundErr(err) {
			logger.Info("harvest drift reconcile checkpoint missing; using full historical scan")
			return time.Time{}, true, nil
		}
		return time.Time{}, false, err
	}
	windowStart, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(cfg.Value))
	if parseErr != nil {
		logger.Warnf("harvest drift reconcile checkpoint parse failed, using full historical scan err=%v", parseErr)
		return time.Time{}, true, nil
	}
	return windowStart.UTC(), false, nil
}

func isCheckpointNotFoundErr(err error) bool {
	if utilerrors.IsNotFoundErr(err) {
		return true
	}
	var customErr *vsaerrors.CustomError
	if vsaerrors.As(err, &customErr) && customErr.IsError(vsaerrors.ErrResourceNotFound) {
		return true
	}
	return false
}

func (a *HarvestPollerDriftReconcileActivity) listReadyPoolIDsInWindow(ctx context.Context, windowStart, windowEnd time.Time, fullHistoricalScan bool) ([]int64, error) {
	type poolIDRow struct {
		ID int64 `gorm:"column:id"`
	}
	rows := make([]poolIDRow, 0)
	query := a.SE.DB().WithContext(ctx).
		Model(&datamodel.Pool{}).
		Select("id").
		Where("state IN ? AND deleted_at IS NULL", []string{datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateUpdating})
	if !fullHistoricalScan {
		query = query.Where(
			"(created_at >= ? AND created_at <= ?) OR (updated_at >= ? AND updated_at <= ?)",
			windowStart, windowEnd, windowStart, windowEnd,
		)
	}
	err := query.Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.ID)
	}
	return out, nil
}

// harvestSourceRunID returns the Temporal workflow run ID for the activity's parent workflow.
// Outside an activity context it returns an empty string and callers fall back to always incrementing.
func harvestSourceRunID(ctx context.Context) (runID string) {
	defer func() {
		if recover() != nil {
			runID = ""
		}
	}()
	return activity.GetInfo(ctx).WorkflowExecution.RunID
}

// applyHarvestFlowRetryOutcome updates flow retry metadata. Failure increments Retries at most once
// per sourceRunID so Temporal activity retries do not burn drift reconcile retry budget.
func applyHarvestFlowRetryOutcome(state *datamodel.HarvestFlowRetryState, success bool, msg, sourceRunID string) {
	if state == nil {
		return
	}
	state.LastAttemptAt = time.Now().UTC()
	if success {
		state.LastRunMsg = "success"
		state.ShouldRetry = false
		return
	}
	if sourceRunID != "" && state.LastSourceRunID == sourceRunID {
		state.LastRunMsg = msg
		return
	}
	state.Retries++
	state.LastSourceRunID = sourceRunID
	state.LastRunMsg = msg
	state.ShouldRetry = state.Retries < harvestDriftReconcileRetryLimit
}

// mergePersistedFlowRetryState overlays one flow's retry JSON from a separate DB read. Production
// writers use UpdateNodeNodeGroupMapRetryState (SELECT FOR UPDATE + mutator) instead; this helper
// remains for unit tests of overlay semantics.
func mergePersistedFlowRetryState(ctx context.Context, se database.Storage, mapping *datamodel.NodeNodeGroupMap, flow string) error {
	persisted, err := fetchNodeNodeGroupMapForRetryMerge(ctx, se, mapping)
	if err != nil || persisted == nil {
		return err
	}
	persistedFlow := flowRetryState(persisted, flow)
	if persistedFlow == nil {
		return nil
	}
	setFlowRetryState(mapping, flow, cloneHarvestFlowRetryState(persistedFlow))
	return nil
}

// fetchNodeNodeGroupMapForRetryMerge loads a map row when callers only have NodeID (no map ID yet).
// GetNodeNodeGroupMap is Unscoped, so this sees the row even after unregister soft-deletes it.
func fetchNodeNodeGroupMapForRetryMerge(ctx context.Context, se database.Storage, mapping *datamodel.NodeNodeGroupMap) (*datamodel.NodeNodeGroupMap, error) {
	if mapping == nil {
		return nil, nil
	}
	if mapping.ID != 0 {
		row, err := se.GetNodeNodeGroupMap(ctx, mapping.ID)
		if err != nil {
			if utilerrors.IsNotFoundErr(err) {
				return nil, nil
			}
			return nil, err
		}
		return row, nil
	}
	if mapping.NodeID == 0 {
		return nil, nil
	}
	row, err := se.GetNodeNodeGroupMapByNodeID(ctx, mapping.NodeID)
	if err != nil {
		if utilerrors.IsNotFoundErr(err) {
			return nil, nil
		}
		return nil, err
	}
	return row, nil
}

func cloneHarvestFlowRetryState(state *datamodel.HarvestFlowRetryState) *datamodel.HarvestFlowRetryState {
	if state == nil {
		return nil
	}
	copy := *state
	return &copy
}

func cloneHarvestRetryState(state *datamodel.HarvestRetryState) *datamodel.HarvestRetryState {
	if state == nil {
		return &datamodel.HarvestRetryState{}
	}
	out := &datamodel.HarvestRetryState{}
	if state.Create != nil {
		out.Create = cloneHarvestFlowRetryState(state.Create)
	}
	if state.Delete != nil {
		out.Delete = cloneHarvestFlowRetryState(state.Delete)
	}
	return out
}

func flowRetryStateFromRetryState(retryState *datamodel.HarvestRetryState, flow string) *datamodel.HarvestFlowRetryState {
	if retryState == nil {
		return nil
	}
	switch flow {
	case harvestRetryFlowDelete:
		return retryState.Delete
	case harvestRetryFlowCreate:
		return retryState.Create
	default:
		return nil
	}
}

func setFlowRetryStateOnRetryState(retryState *datamodel.HarvestRetryState, flow string, state *datamodel.HarvestFlowRetryState) {
	if retryState == nil {
		return
	}
	switch flow {
	case harvestRetryFlowDelete:
		retryState.Delete = state
	case harvestRetryFlowCreate:
		retryState.Create = state
	}
}

func hasFlowRetryRemaining(mapping *datamodel.NodeNodeGroupMap, flow string) bool {
	state := flowRetryState(mapping, flow)
	return state != nil && state.ShouldRetry && state.Retries < harvestDriftReconcileRetryLimit
}

// shouldProcessDeleteCandidate returns true when drift reconcile should attempt YAML delete for a
// mapped node listed in inventory. Rows with no delete retry state are processed (legacy cleanup).
// Recorded delete success does not suppress cleanup: inventory evidence overrides stale success.
// Only genuinely exhausted delete failures are skipped.
func shouldProcessDeleteCandidate(mapping *datamodel.NodeNodeGroupMap) bool {
	if mapping == nil {
		return true
	}
	state := flowRetryState(mapping, harvestRetryFlowDelete)
	if state == nil {
		return true
	}
	if hasFlowRetryRemaining(mapping, harvestRetryFlowDelete) {
		return true
	}
	if state.LastRunMsg == "success" {
		return true
	}
	if !state.ShouldRetry && state.Retries >= harvestDriftReconcileRetryLimit {
		return false
	}
	return true
}

func flowRetryState(mapping *datamodel.NodeNodeGroupMap, flow string) *datamodel.HarvestFlowRetryState {
	if mapping == nil || mapping.RetryState == nil {
		return nil
	}
	switch flow {
	case harvestRetryFlowDelete:
		return mapping.RetryState.Delete
	case harvestRetryFlowCreate:
		return mapping.RetryState.Create
	default:
		return nil
	}
}

func setFlowRetryState(mapping *datamodel.NodeNodeGroupMap, flow string, state *datamodel.HarvestFlowRetryState) {
	if mapping.RetryState == nil {
		mapping.RetryState = &datamodel.HarvestRetryState{}
	}
	switch flow {
	case harvestRetryFlowDelete:
		mapping.RetryState.Delete = state
	case harvestRetryFlowCreate:
		mapping.RetryState.Create = state
	}
}

func listHarvestPollerInventory(ctx context.Context) (harvestPollerInventory, error) {
	entries, emptyLeases, err := listHarvestPollerFiles(ctx)
	if err != nil {
		return harvestPollerInventory{}, err
	}
	out := make(map[string]struct{}, len(entries))
	filtered := make([]HarvestPollerFileEntry, 0, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.LeaseName) == "" || e.NodeID == 0 {
			continue
		}
		filtered = append(filtered, e)
		out[pollerInventoryKey(e.LeaseName, e.NodeID)] = struct{}{}
	}
	return harvestPollerInventory{
		FileEntries:  filtered,
		InventorySet: out,
		EmptyLeases:  emptyLeases,
	}, nil
}

func harvestConfigUploadURL() string {
	return fmt.Sprintf("%s://%s/config/upload", harvestRestProtocol, harvestEndPoint)
}

func listHarvestPollerFiles(ctx context.Context) ([]HarvestPollerFileEntry, []string, error) {
	url := fmt.Sprintf("%s://%s%s", harvestRestProtocol, harvestEndPoint, harvestConfigListPath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := doHarvestHTTP(ctx, req, nil)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
		return nil, nil, fmt.Errorf("list harvest config files failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, nil, err
	}
	return parseHarvestInventory(body)
}

func parseHarvestInventory(body []byte) ([]HarvestPollerFileEntry, []string, error) {
	body = bytesTrimSpace(body)
	if len(body) == 0 {
		return nil, nil, nil
	}
	var rawEntries []map[string]interface{}
	if err := json.Unmarshal(body, &rawEntries); err == nil {
		files, emptyLeases := flattenInventoryObjectList(rawEntries)
		return files, emptyLeases, nil
	}

	var wrapped struct {
		Items []map[string]interface{} `json:"items"`
		Files []map[string]interface{} `json:"files"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		combined := append(make([]map[string]interface{}, 0, len(wrapped.Items)+len(wrapped.Files)), wrapped.Items...)
		combined = append(combined, wrapped.Files...)
		files, emptyLeases := flattenInventoryObjectList(combined)
		return files, emptyLeases, nil
	}

	var fileNames []string
	if err := json.Unmarshal(body, &fileNames); err == nil {
		out := make([]HarvestPollerFileEntry, 0, len(fileNames))
		for _, fileName := range fileNames {
			nodeID, ok := nodeIDFromFileName(fileName)
			if !ok {
				continue
			}
			out = append(out, HarvestPollerFileEntry{FileName: fileName, NodeID: nodeID})
		}
		return out, nil, nil
	}

	return nil, nil, errors.New("unable to parse harvest inventory response")
}

func flattenInventoryObjectList(entries []map[string]interface{}) ([]HarvestPollerFileEntry, []string) {
	out := make([]HarvestPollerFileEntry, 0, len(entries))
	emptyLeases := make([]string, 0)
	for _, item := range entries {
		if isEmptyLeaseInventoryItem(item) {
			leaseName := getStringFromMap(item, "leaseName", "lease_name", "lease")
			if leaseName != "" {
				emptyLeases = append(emptyLeases, leaseName)
			}
			continue
		}
		leaseName := getStringFromMap(item, "leaseName", "lease_name", "lease")
		fileName := getStringFromMap(item, "fileName", "file_name", "file")
		nodeID, ok := getInt64FromMap(item, "nodeID", "node_id")
		if !ok && fileName != "" {
			nodeID, ok = nodeIDFromFileName(fileName)
		}
		if !ok || nodeID == 0 {
			continue
		}
		out = append(out, HarvestPollerFileEntry{
			LeaseName: leaseName,
			FileName:  fileName,
			NodeID:    nodeID,
		})
	}
	return out, emptyLeases
}

func isEmptyLeaseInventoryItem(item map[string]interface{}) bool {
	raw, ok := item["empty"]
	if !ok || raw == nil {
		return false
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func getStringFromMap(item map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		raw, ok := item[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func getInt64FromMap(item map[string]interface{}, keys ...string) (int64, bool) {
	for _, key := range keys {
		raw, ok := item[key]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return int64(v), true
		case int64:
			return v, true
		case int:
			return int64(v), true
		case string:
			parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err == nil {
				return parsed, true
			}
		}
	}
	return 0, false
}

func nodeIDFromFileName(fileName string) (int64, bool) {
	trimmed := strings.TrimSpace(fileName)
	trimmed = strings.TrimSuffix(trimmed, ".yaml")
	if !strings.HasPrefix(trimmed, leasePrefix) {
		return 0, false
	}
	idText := strings.TrimPrefix(trimmed, leasePrefix)
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return 0, false
	}
	return id, true
}

func bytesTrimSpace(in []byte) []byte {
	return []byte(strings.TrimSpace(string(in)))
}

func isPoolDeleted(pool *datamodel.Pool) bool {
	if pool == nil {
		return false
	}
	return pool.DeletedAt != nil && pool.DeletedAt.Valid
}

// isPoolEligibleForHarvestCreateDrift reports whether the pool's current state allows the
// drift reconcile create pass to (re)upload a poller YAML for it. READY pools are the normal
// case; UPDATING pools are also eligible because in-place ops (e.g. HA-pair add) can leave
// register/unregister mappings needing a poller create while the pool is mid-update.
func isPoolEligibleForHarvestCreateDrift(pool *datamodel.Pool) bool {
	if pool == nil || isPoolDeleted(pool) {
		return false
	}
	return pool.State == datamodel.LifeCycleStateREADY || pool.State == datamodel.LifeCycleStateUpdating
}

func (a *HarvestPollerDriftReconcileActivity) resolvePoolForMap(ctx context.Context, mapRow *datamodel.NodeNodeGroupMap) (*datamodel.Pool, error) {
	if mapRow == nil || mapRow.NodeID == 0 {
		return nil, nil
	}
	node, err := a.SE.GetNodeByID(ctx, mapRow.NodeID)
	if err != nil {
		if utilerrors.IsNotFoundErr(err) {
			return nil, nil
		}
		return nil, err
	}
	if node == nil || node.PoolID == 0 {
		return nil, nil
	}
	return a.resolvePoolByID(ctx, node.PoolID)
}

func (a *HarvestPollerDriftReconcileActivity) resolvePoolByID(ctx context.Context, poolID int64) (*datamodel.Pool, error) {
	pool, err := a.SE.GetPoolByID(ctx, poolID)
	if err == nil {
		return pool, nil
	}
	if !utilerrors.IsNotFoundErr(err) {
		return nil, err
	}

	// Fallback for soft-deleted pools.
	var deletedPool datamodel.Pool
	dbErr := a.SE.DB().WithContext(ctx).Unscoped().Where("id = ?", poolID).First(&deletedPool).Error
	if dbErr != nil {
		if errors.Is(dbErr, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, dbErr
	}
	return &deletedPool, nil
}

func pollerInventoryKey(leaseName string, nodeID int64) string {
	return fmt.Sprintf("%s|%d", strings.TrimSpace(leaseName), nodeID)
}
