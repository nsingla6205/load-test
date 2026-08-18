package database

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	generateRandomNodeGroup = _generateRandomNodeGroup
	portStart               = env.GetInt("HARVEST_PORT_START", 13001)
	portEnd                 = env.GetInt("HARVEST_PORT_END", 13500)
	vsaNodeUserName         = env.GetString("VSA_NODE_USERNAME", "admin")
)

// maxHarvestPortInsertRetries limits remap attempts when the unique (node_group_id, port) index rejects an insert.
const maxHarvestPortInsertRetries = 8

// CreateNodeNodeGroupMap creates a new node to nodegroup mapping
func (d *DataStoreRepository) CreateNodeNodeGroupMap(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) (*datamodel.NodeNodeGroupMap, error) {
	tx := d.db.GORM().WithContext(ctx)
	mapping.CreatedAt = time.Now()
	mapping.UpdatedAt = mapping.CreatedAt
	err := tx.Create(mapping).Error
	if err != nil {
		// Check if this is a duplicate key error for node_id
		if strings.Contains(err.Error(), "idx_node_node_group_maps_node_id_unique") ||
			strings.Contains(err.Error(), "duplicate key") ||
			strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, errors.New("node is already assigned to a group"))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}
	return mapping, nil
}

// GetNodeNodeGroupMap retrieves a node to nodegroup mapping by ID, including soft-deleted rows.
// Unscoped is required so callers that mark retry state after soft-delete (e.g. unregister's
// MarkDeleteRetryState) can still read and merge the persisted row.
func (d *DataStoreRepository) GetNodeNodeGroupMap(ctx context.Context, id int64) (*datamodel.NodeNodeGroupMap, error) {
	var mapping datamodel.NodeNodeGroupMap
	err := d.db.GORM().Unscoped().WithContext(ctx).Preload("NodeGroup").First(&mapping, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "node_node_group_map", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &mapping, nil
}

// GetNodeNodeGroupMapByNodeID retrieves nodegroup map by NodeID
func (d *DataStoreRepository) GetNodeNodeGroupMapByNodeID(ctx context.Context, nodeID int64) (*datamodel.NodeNodeGroupMap, error) {
	var mapping datamodel.NodeNodeGroupMap
	err := d.db.GORM().Unscoped().WithContext(ctx).Preload("NodeGroup").Where("node_id = ?", nodeID).Order("id desc").First(&mapping).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "node_node_group_map", nil))
	}
	return &mapping, nil
}

// GetActiveNodeNodeGroupMapByNodeID returns the latest non-soft-deleted node_node_group_map for the node.
// When tx is nil, uses the repository default connection; when non-nil, uses tx.GORM() so callers inside WithTransaction see their writes.
func (d *DataStoreRepository) GetActiveNodeNodeGroupMapByNodeID(ctx context.Context, nodeID int64, tx dbutils.Transaction) (*datamodel.NodeNodeGroupMap, error) {
	db := d.db.GORM().WithContext(ctx)
	if tx != nil {
		db = tx.GORM().WithContext(ctx)
	}
	var mapping datamodel.NodeNodeGroupMap
	err := db.Preload("NodeGroup").Where("node_id = ? AND deleted_at IS NULL", nodeID).Order("id desc").First(&mapping).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "node_node_group_map", nil))
	}
	return &mapping, nil
}

// GetActiveNodeNodeGroupMapsByNodeIDs retrieves active node-group mappings for
// multiple nodes in one query. On success, every unique requested node ID is
// present in the returned map; missing active mappings return an error and no
// partial result. The returned map is keyed by node ID.
func (d *DataStoreRepository) GetActiveNodeNodeGroupMapsByNodeIDs(
	ctx context.Context,
	nodeIDs []int64,
	tx dbutils.Transaction,
) (map[int64]*datamodel.NodeNodeGroupMap, error) {
	mappingsByNodeID := make(map[int64]*datamodel.NodeNodeGroupMap, len(nodeIDs))
	uniqueNodeIDs := make([]int64, 0, len(nodeIDs))
	seenNodeIDs := make(map[int64]struct{}, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		if _, seen := seenNodeIDs[nodeID]; seen {
			continue
		}
		seenNodeIDs[nodeID] = struct{}{}
		uniqueNodeIDs = append(uniqueNodeIDs, nodeID)
	}
	if len(uniqueNodeIDs) == 0 {
		return mappingsByNodeID, nil
	}

	db := d.db.GORM().WithContext(ctx)
	if tx != nil {
		db = tx.GORM().WithContext(ctx)
	}

	var mappings []datamodel.NodeNodeGroupMap
	query := db.Where("deleted_at IS NULL").Order("id DESC")
	if db.Dialector.Name() == "postgres" {
		query = query.Where("node_id = ANY(?)", pq.Array(uniqueNodeIDs))
	} else {
		query = query.Where("node_id IN ?", uniqueNodeIDs)
	}
	if err := query.Find(&mappings).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	for i := range mappings {
		mapping := &mappings[i]
		if _, found := mappingsByNodeID[mapping.NodeID]; found {
			continue
		}
		mappingsByNodeID[mapping.NodeID] = mapping
	}
	for _, nodeID := range uniqueNodeIDs {
		if _, found := mappingsByNodeID[nodeID]; !found {
			return nil, vsaerrors.NewVCPError(
				vsaerrors.ErrDatabaseDataReadError,
				fmt.Errorf("active node_node_group_map not found for node %d", nodeID),
			)
		}
	}
	return mappingsByNodeID, nil
}

// UpdateNodeNodeGroupMap updates an existing node to nodegroup mapping
func (d *DataStoreRepository) UpdateNodeNodeGroupMap(ctx context.Context, mapping *datamodel.NodeNodeGroupMap) (*datamodel.NodeNodeGroupMap, error) {
	tx := d.db.GORM().WithContext(ctx)
	mapping.UpdatedAt = time.Now()
	err := tx.Save(mapping).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return mapping, nil
}

// UpdateNodeNodeGroupMapRetryState reads the map row under row lock (PostgreSQL SELECT FOR UPDATE),
// applies update against the locked harvest_retry_state, and persists the result in one transaction.
// Soft-deleted rows are included (Unscoped) so unregister can mark delete retry after DeleteNodeGroupMapping.
func (d *DataStoreRepository) UpdateNodeNodeGroupMapRetryState(ctx context.Context, id int64, update HarvestRetryStateMutator) error {
	if id == 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, gorm.ErrRecordNotFound)
	}
	if update == nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, errors.New("harvest retry state update function is required"))
	}

	logger := util.GetLogger(ctx)
	var err error
	tx, err := startTransaction(d.db.GORM().WithContext(ctx))
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(logger, tx, &err)

	var mapping datamodel.NodeNodeGroupMap
	q := tx.Unscoped().Where("id = ?", id)
	if isPostgresDialect(tx) {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if lookupErr := q.First(&mapping).Error; lookupErr != nil {
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, lookupErr)
			return err
		}
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, lookupErr)
		return err
	}

	next, mutateErr := update(mapping.RetryState)
	if mutateErr != nil {
		err = mutateErr
		return err
	}

	result := tx.Unscoped().Model(&datamodel.NodeNodeGroupMap{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"harvest_retry_state": next,
			"updated_at":          time.Now(),
		})
	if result.Error != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
		return err
	}
	if result.RowsAffected == 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, gorm.ErrRecordNotFound)
		return err
	}
	return nil
}

func (d *DataStoreRepository) ListNodeNodeGroupMap(
	ctx context.Context,
	includeDeleted bool,
	pagination *dbutils.Pagination) ([]*datamodel.NodeNodeGroupMap, error) {
	db := d.db.GORM().WithContext(ctx)
	var nodesGroupMap []*datamodel.NodeNodeGroupMap
	// Include soft-deleted records if specified
	if includeDeleted {
		db = d.db.Unscoped().GORM().WithContext(ctx)
	}
	// Apply pagination if provided
	if pagination != nil {
		db = db.Scopes(dbutils.Paginate(pagination))
	}
	err := db.Preload("NodeGroup").Find(&nodesGroupMap).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return nodesGroupMap, nil
}

// ListNodeNodeGroupMapAfterID returns records with id > afterID, ordered by id ascending, with limit.
// Used for keyset (cursor) pagination so that soft-deletes during iteration do not cause rows to be skipped.
func (d *DataStoreRepository) ListNodeNodeGroupMapAfterID(
	ctx context.Context,
	includeDeleted bool,
	afterID int64,
	limit int,
) ([]*datamodel.NodeNodeGroupMap, error) {
	db := d.db.GORM().WithContext(ctx)
	if includeDeleted {
		db = d.db.Unscoped().GORM().WithContext(ctx)
	}
	var nodesGroupMap []*datamodel.NodeNodeGroupMap
	err := db.Where("id > ?", afterID).Order("id ASC").Limit(limit).Preload("NodeGroup").Find(&nodesGroupMap).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return nodesGroupMap, nil
}

// DeleteNodeNodeGroupMap deletes a node to nodegroup mapping by ID
func (d *DataStoreRepository) DeleteNodeNodeGroupMap(ctx context.Context, id int64) error {
	tx := d.db.GORM().WithContext(ctx)
	// Soft delete: update DeletedAt field instead of hard delete
	var mapping datamodel.NodeNodeGroupMap
	err := tx.First(&mapping, id).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, err)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, err)
	}
	mapping.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	err = tx.Save(&mapping).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, err)
	}
	return nil
}

// AssignTwoNodesToTwoGroups assigns two nodes to two different node groups, ensuring no group exceeds maxNodesPerGroup nodes
// Assumes that node1 and node2 are pre-created and have valid IDs
// This function is idempotent - if nodes are already assigned to groups, it returns the existing mappings
//
// All reads and writes run in a single database transaction. On PostgreSQL, non-full node_groups rows are locked
// (ORDER BY id FOR UPDATE) before capacity is re-checked, preventing concurrent overfill and port races; a unique
// index on (node_group_id, harvest port) is a safety net with bounded insert retries.
//
// Cross-pool safety: node groups are global; concurrent pool registrations serialize on overlapping node_groups row
// locks on PostgreSQL. SQLite unit tests use a file-backed DB plus one transaction per assign; production uses Postgres.
//
// Poller / rebalance: rebalance commit locks target node_groups (FOR UPDATE) before Save. Upload picks a port via
// GetFirstAvailablePort (single locked read on Postgres). A concurrent pool assign can still commit between upload
// and commit so Save hits the unique (node_group_id, port) index—rebalance workflows should retry on that failure.
func (d *DataStoreRepository) AssignTwoNodesToTwoGroups(ctx context.Context, params datamodel.NodeGroupAssignmentParams) ([]*datamodel.NodeNodeGroupMap, error) {
	logger := util.GetLogger(ctx)
	if params.Node1 == nil || params.Node2 == nil {
		logger.Errorf("AssignTwoNodesToTwoGroups: node1 or node2 is nil")
		return nil, errors.New("node1 or node2 is nil")
	}
	if params.Node1.ID == params.Node2.ID {
		logger.Errorf("AssignTwoNodesToTwoGroups: node1 and node2 must be different nodes (node1.ID=%d)", params.Node1.ID)
		return nil, errors.New("node1 and node2 must be different nodes")
	}
	if params.MaxNodesPerGroup <= 0 {
		logger.Errorf("AssignTwoNodesToTwoGroups: maxNodesPerGroup must be greater than zero (got %d)", params.MaxNodesPerGroup)
		return nil, errors.New("maxNodesPerGroup must be greater than zero")
	}

	tx, err := startTransaction(d.db.GORM().WithContext(ctx))
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(logger, tx, &err)

	ctxTx := utils.WithTx(ctx, tx)
	logger.Debugf("AssignTwoNodesToTwoGroups: node1.ID=%d, node2.ID=%d, maxNodesPerGroup=%d", params.Node1.ID, params.Node2.ID, params.MaxNodesPerGroup)

	var mappings []*datamodel.NodeNodeGroupMap
	mappings, err = d.assignTwoNodesToTwoGroupsTx(ctxTx, tx, params)
	return mappings, err
}

func (d *DataStoreRepository) assignTwoNodesToTwoGroupsTx(
	ctx context.Context,
	tx *gorm.DB,
	params datamodel.NodeGroupAssignmentParams,
) ([]*datamodel.NodeNodeGroupMap, error) {
	logger := util.GetLogger(ctx)
	var existingMapping1, existingMapping2 datamodel.NodeNodeGroupMap
	err1 := tx.WithContext(ctx).Preload("NodeGroup").Where("node_id = ?", params.Node1.ID).First(&existingMapping1).Error
	err2 := tx.WithContext(ctx).Preload("NodeGroup").Where("node_id = ?", params.Node2.ID).First(&existingMapping2).Error

	if err1 == nil && err2 == nil {
		logger.Infof("Both nodes are already assigned: node1 (ID: %d) -> group %d, node2 (ID: %d) -> group %d",
			params.Node1.ID, existingMapping1.NodeGroupID, params.Node2.ID, existingMapping2.NodeGroupID)
		return []*datamodel.NodeNodeGroupMap{&existingMapping1, &existingMapping2}, nil
	}
	if err1 != nil && !errors.Is(err1, gorm.ErrRecordNotFound) {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err1)
	}
	if err2 != nil && !errors.Is(err2, gorm.ErrRecordNotFound) {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err2)
	}

	lockedIDs, err := lockNonFullNodeGroupIDs(tx, params.MaxNodesPerGroup)
	if err != nil {
		logger.Errorf("lockNonFullNodeGroupIDs: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	counts, err := countsActiveMapsByGroup(tx, lockedIDs)
	if err != nil {
		logger.Errorf("countsActiveMapsByGroup: %v", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	candidates := append([]int64(nil), lockedIDs...)

	excludeForNode1 := int64(0)
	if err2 == nil {
		excludeForNode1 = existingMapping2.NodeGroupID
	}

	var group1 datamodel.NodeGroup
	if err1 == nil {
		logger.Infof("Node1 (ID: %d) is already assigned to group %d", params.Node1.ID, existingMapping1.NodeGroupID)
		if err := tx.WithContext(ctx).First(&group1, existingMapping1.NodeGroupID).Error; err != nil {
			logger.Errorf("Failed to fetch group for node1.ID=%d, groupID=%d: %v", params.Node1.ID, existingMapping1.NodeGroupID, err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
	} else {
		logger.Debugf("Node1 (ID: %d) not assigned, picking group (maxNodesPerGroup=%d)", params.Node1.ID, params.MaxNodesPerGroup)
		gid := pickGroupIDWithCapacity(candidates, counts, excludeForNode1, params.MaxNodesPerGroup)
		if gid == 0 {
			logger.Infof("No available group found for node1.ID=%d, creating new group", params.Node1.ID)
			group1Ptr, err := generateRandomNodeGroup(ctx, d, datamodel.NodeGroup{})
			if err != nil {
				logger.Errorf("Failed to create new group for node1.ID=%d: %v", params.Node1.ID, err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
			}
			group1 = *group1Ptr
			candidates = appendSortedUniqueIDs(candidates, group1.ID)
			counts[group1.ID] = 0
		} else {
			if err := tx.WithContext(ctx).First(&group1, gid).Error; err != nil {
				logger.Errorf("Failed to fetch group id=%d for node1: %v", gid, err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
			}
		}
	}

	var group2 datamodel.NodeGroup
	if err2 == nil {
		logger.Infof("Node2 (ID: %d) is already assigned to group %d", params.Node2.ID, existingMapping2.NodeGroupID)
		if err := tx.WithContext(ctx).First(&group2, existingMapping2.NodeGroupID).Error; err != nil {
			logger.Errorf("Failed to fetch group for node2.ID=%d, groupID=%d: %v", params.Node2.ID, existingMapping2.NodeGroupID, err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
	} else {
		logger.Debugf("Node2 (ID: %d) not assigned, picking group (maxNodesPerGroup=%d, exclude group1.ID=%d)", params.Node2.ID, params.MaxNodesPerGroup, group1.ID)
		gid := pickGroupIDWithCapacity(candidates, counts, group1.ID, params.MaxNodesPerGroup)
		if gid == 0 {
			logger.Infof("No available group found for node2.ID=%d, creating new group", params.Node2.ID)
			group2Ptr, err := generateRandomNodeGroup(ctx, d, datamodel.NodeGroup{})
			if err != nil {
				logger.Errorf("Failed to create new group for node2.ID=%d: %v", params.Node2.ID, err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
			}
			group2 = *group2Ptr
		} else {
			if err := tx.WithContext(ctx).First(&group2, gid).Error; err != nil {
				logger.Errorf("Failed to fetch group id=%d for node2: %v", gid, err)
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
			}
		}
	}

	var mappings []*datamodel.NodeNodeGroupMap

	if errors.Is(err1, gorm.ErrRecordNotFound) {
		m1, err := d.createNodeNodeGroupMapWithPortRetries(ctx, tx, params.Node1, &group1, params)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, m1)
	} else {
		logger.Debugf("Mapping for node1.ID=%d already exists", params.Node1.ID)
		mappings = append(mappings, &existingMapping1)
	}

	if errors.Is(err2, gorm.ErrRecordNotFound) {
		m2, err := d.createNodeNodeGroupMapWithPortRetries(ctx, tx, params.Node2, &group2, params)
		if err != nil {
			return nil, err
		}
		mappings = append(mappings, m2)
	} else {
		logger.Debugf("Mapping for node2.ID=%d already exists", params.Node2.ID)
		mappings = append(mappings, &existingMapping2)
	}

	return mappings, nil
}

func lockNonFullNodeGroupIDs(tx *gorm.DB, maxNodesPerGroup int) ([]int64, error) {
	var ids []int64
	// PostgreSQL: pessimistic row locks on candidate groups in deterministic order (wiki Approach A).
	// SQLite (unit tests): same predicate without FOR UPDATE — assignment still runs atomically in one transaction.
	q := `
SELECT g.id
FROM node_groups g
WHERE g.deleted_at IS NULL
  AND (
        SELECT COUNT(*) FROM node_node_group_maps m
        WHERE m.node_group_id = g.id AND m.deleted_at IS NULL
      ) < ?
ORDER BY g.id ASC`
	if tx.Dialector.Name() == "postgres" {
		q += `
FOR UPDATE OF g`
	}
	err := tx.Raw(q, maxNodesPerGroup).Scan(&ids).Error
	return ids, err
}

type nodeGroupCountRow struct {
	NodeGroupID int64 `gorm:"column:node_group_id"`
	N           int64 `gorm:"column:n"`
}

func countsActiveMapsByGroup(tx *gorm.DB, groupIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64)
	if len(groupIDs) == 0 {
		return out, nil
	}
	var rows []nodeGroupCountRow
	err := tx.Raw(`
SELECT node_group_id, COUNT(*) AS n
FROM node_node_group_maps
WHERE node_group_id IN ? AND deleted_at IS NULL
GROUP BY node_group_id`, groupIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for i := range rows {
		out[rows[i].NodeGroupID] = rows[i].N
	}
	return out, nil
}

func pickGroupIDWithCapacity(orderedGroupIDs []int64, counts map[int64]int64, excludeID int64, maxNodesPerGroup int) int64 {
	maxN := int64(maxNodesPerGroup)
	for _, id := range orderedGroupIDs {
		if excludeID != 0 && id == excludeID {
			continue
		}
		n := counts[id]
		if n < maxN {
			return id
		}
	}
	return 0
}

func appendSortedUniqueIDs(ids []int64, id int64) []int64 {
	for _, x := range ids {
		if x == id {
			return ids
		}
	}
	ids = append(ids, id)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

// IsHarvestNodeGroupPortUniqueViolation reports whether err is a unique violation on
// (node_group_id, harvest_config->>'PORT') from idx_node_node_group_maps_group_port_active_uq.
func IsHarvestNodeGroupPortUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	s := err.Error()
	if strings.Contains(s, "idx_node_node_group_maps_group_port_active_uq") {
		return true
	}
	if strings.Contains(s, "UNIQUE constraint failed") {
		return true
	}
	return strings.Contains(s, "duplicate key") && strings.Contains(s, "node_node_group_maps")
}

func (d *DataStoreRepository) createNodeNodeGroupMapWithPortRetries(
	ctx context.Context,
	tx *gorm.DB,
	node *datamodel.Node,
	group *datamodel.NodeGroup,
	params datamodel.NodeGroupAssignmentParams,
) (*datamodel.NodeNodeGroupMap, error) {
	logger := util.GetLogger(ctx)
	var lastErr error
	for attempt := 0; attempt < maxHarvestPortInsertRetries; attempt++ {
		port, err := GetFirstAvailablePort(tx, group.ID)
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		mapping := &datamodel.NodeNodeGroupMap{
			BaseModel:     datamodel.BaseModel{UUID: uuid.New().String()},
			NodeID:        node.ID,
			NodeGroupID:   group.ID,
			HarvestConfig: renderHarvestConfig(*node, port, group.LeaseName, params),
			NodeGroup:     group,
		}
		logger.Infof("Creating new mapping for node.ID=%d to group.ID=%d (port attempt %d)", node.ID, group.ID, attempt+1)
		if err := tx.WithContext(ctx).Create(mapping).Error; err != nil {
			lastErr = err
			if IsHarvestNodeGroupPortUniqueViolation(err) && attempt < maxHarvestPortInsertRetries-1 {
				logger.Warnf("Port collision on insert for node.ID=%d group.ID=%d, retrying: %v", node.ID, group.ID, err)
				continue
			}
			logger.Errorf("Failed to create mapping for node.ID=%d: %v", node.ID, err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		return mapping, nil
	}
	return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, lastErr)
}

func renderHarvestConfig(node datamodel.Node, port string, leaseName string, params datamodel.NodeGroupAssignmentParams) *datamodel.HarvestConfig {
	return &datamodel.HarvestConfig{
		PORT:                port,
		SERVICE_CONTROL_URL: env.GetString("SERVICE_CONTROL_URL", "https://servicecontrol.googleapis.com"),
		SERVICE_NAME:        env.GetString("SERVICE_NAME", "autopush-netapp.sandbox.googleapis.com"),
		POLLER_NAME:         "cluster" + strconv.FormatInt(node.PoolID, 10) + "-" + node.Name,
		DATACENTER:          env.GetString("LOCAL_REGION", ""),
		NODE_IP:             node.EndpointAddress,
		AUTH_STYLE:          "basic",
		USERNAME:            vsaNodeUserName,
		PASSWORD:            "", // Password info shouldn't be updated in DataBase
		PROJECT:             params.CustomerProject,
		LEASE_NAME:          leaseName,
		FILE_NAME:           fmt.Sprintf("harvest-%d.yaml", node.ID),
		TENANT_PROJECT:      params.TenantProject,
		DEPLOYMENT_NAME:     params.DeploymentName,
		POOL_NAME:           params.PoolName,
		IS_REGIONAL_HA:      params.IsRegionalHA,
	}
}

func _generateRandomNodeGroup(ctx context.Context, d *DataStoreRepository, group1 datamodel.NodeGroup) (*datamodel.NodeGroup, error) {
	group1 = datamodel.NodeGroup{
		BaseModel: datamodel.BaseModel{UUID: uuid.New().String()},
		Name:      "lease-" + utils.RandomHex10(),
	}
	group1Ptr, err := d.CreateNodeGroup(ctx, &group1)
	return group1Ptr, err
}

// portRow is a single assigned poller port for a node group (harvest_config.PORT).
type portRow struct {
	Port string `gorm:"column:port"`
}

// GetFirstAvailablePort returns the lowest free TCP port in [portStart, portEnd] for harvest_config on
// active mappings in the given node_group_id.
//
// On PostgreSQL, port discovery runs in the same SQL statement as a FOR UPDATE on the parent node_groups
// row (CTE locked), so the lock is held for the entire port scan even when tx is in autocommit mode.
// When tx is part of a larger transaction, that lock integrates with other row locks in that transaction.
// SQLite uses a plain scan (no row lock); concurrent writers there rely on the unique (node_group_id, port)
// index and insert-time retries where applicable.
func GetFirstAvailablePort(tx *gorm.DB, groupID int64) (string, error) {
	port, err := GetFirstAvailablePortWithReserved(tx, groupID, nil)
	if err != nil {
		return "", err
	}
	return strconv.Itoa(port), nil
}

// GetFirstAvailablePortWithReserved returns the lowest free Harvest port for groupID.
// reserved treats ports as already assigned (e.g. picked earlier in the same rebalance upload batch).
func GetFirstAvailablePortWithReserved(tx *gorm.DB, groupID int64, reserved map[int]struct{}) (int, error) {
	if tx != nil && tx.Dialector.Name() == "postgres" {
		return getFirstAvailablePortPostgres(tx, groupID, reserved)
	}
	return getFirstAvailablePortDefaultScan(tx, groupID, reserved)
}

func getFirstAvailablePortPostgres(tx *gorm.DB, groupID int64, reserved map[int]struct{}) (int, error) {
	var rows []portRow
	err := tx.Raw(`
WITH locked AS (
	SELECT id FROM node_groups WHERE id = ? AND deleted_at IS NULL FOR UPDATE
)
SELECT m.harvest_config->>'PORT' AS port
FROM node_node_group_maps m
WHERE m.node_group_id = (SELECT id FROM locked)
  AND m.deleted_at IS NULL
  AND m.harvest_config->>'PORT' IS NOT NULL
  AND m.harvest_config->>'PORT' <> ''
`, groupID).Scan(&rows).Error
	if err != nil {
		return 0, fmt.Errorf("failed to query assigned ports: %w", err)
	}
	return pickLowestFreePortFromUsed(rows, groupID, reserved)
}

func getFirstAvailablePortDefaultScan(tx *gorm.DB, groupID int64, reserved map[int]struct{}) (int, error) {
	var rows []portRow
	err := tx.Model(&datamodel.NodeNodeGroupMap{}).
		Select("harvest_config->>'PORT' as port").
		Where("node_group_id = ? AND harvest_config->>'PORT' IS NOT NULL AND harvest_config->>'PORT' != ''", groupID).
		Scan(&rows).Error
	if err != nil {
		return 0, fmt.Errorf("failed to query assigned ports: %w", err)
	}
	return pickLowestFreePortFromUsed(rows, groupID, reserved)
}

func pickLowestFreePortFromUsed(rows []portRow, groupID int64, reserved map[int]struct{}) (int, error) {
	assigned, err := buildAssignedPortSet(rows, reserved)
	if err != nil {
		return 0, err
	}
	return pickLowestFreePortFromAssigned(assigned, groupID)
}

func buildAssignedPortSet(rows []portRow, reserved map[int]struct{}) (map[int]struct{}, error) {
	assigned := make(map[int]struct{})
	for _, r := range rows {
		var p int
		if _, err := fmt.Sscanf(r.Port, "%d", &p); err != nil {
			return nil, fmt.Errorf("failed to parse port '%s': %w", r.Port, err)
		}
		assigned[p] = struct{}{}
	}
	for p := range reserved {
		assigned[p] = struct{}{}
	}
	return assigned, nil
}

func pickLowestFreePortFromAssigned(assigned map[int]struct{}, groupID int64) (int, error) {
	for port := portStart; port <= portEnd; port++ {
		if _, used := assigned[port]; !used {
			return port, nil
		}
	}
	return 0, fmt.Errorf("no available port found in the range %d-%d for group %d", portStart, portEnd, groupID)
}

func (d *DataStoreRepository) DeleteNodeGroupMap(ctx context.Context, nodeGroupMap *datamodel.NodeNodeGroupMap) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	nodeGroupMap.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	err = tx.Updates(nodeGroupMap).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// GetNodeGroupMapNodeCount returns the number of pollers associated with respect to leaseID
func (d *DataStoreRepository) GetNodeGroupMapNodeCount(ctx context.Context, nodeGroupID int64) (int64, error) {
	db := d.db.GORM().WithContext(ctx)
	var count int64
	err := db.Model(&datamodel.NodeNodeGroupMap{}).Where("node_group_id = ?", nodeGroupID).Count(&count).Error
	if err != nil {
		return 0, err
	}
	return count, nil
}

// ListNodeGroupsWithPollerCounts returns all non-deleted node groups with active poller counts, ordered by count ascending.
func (d *DataStoreRepository) ListNodeGroupsWithPollerCounts(ctx context.Context) ([]datamodel.NodeGroupPollerCount, error) {
	db := d.db.GORM().WithContext(ctx)
	var rows []datamodel.NodeGroupPollerCount
	err := db.Raw(`
		SELECT ng.id AS node_group_id, ng.lease_name, COALESCE(COUNT(m.id), 0)::bigint AS cnt
		FROM node_groups ng
		LEFT JOIN node_node_group_maps m ON m.node_group_id = ng.id AND m.deleted_at IS NULL
		WHERE ng.deleted_at IS NULL
		GROUP BY ng.id, ng.lease_name
		ORDER BY cnt ASC, ng.id ASC
	`).Scan(&rows).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return rows, nil
}

// ListNodeNodeGroupMapsByNodeGroupID returns active node-group maps for a group.
// NodeGroup relation is intentionally not preloaded to keep planner queries lightweight.
func (d *DataStoreRepository) ListNodeNodeGroupMapsByNodeGroupID(ctx context.Context, nodeGroupID int64) ([]*datamodel.NodeNodeGroupMap, error) {
	var maps []*datamodel.NodeNodeGroupMap
	err := d.db.GORM().WithContext(ctx).
		Where("node_group_id = ?", nodeGroupID).
		Find(&maps).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return maps, nil
}

// GetHarvestHaSiblingNodeID returns the paired pool sibling node id (adjacent in pool node order), or 0 if none.
func (d *DataStoreRepository) GetHarvestHaSiblingNodeID(ctx context.Context, nodeID int64) (int64, error) {
	n, err := d.GetNodeByID(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	nodes, err := getNodesByPoolID(d.db.GORM().Unscoped().WithContext(ctx), n.PoolID)
	if err != nil {
		return 0, err
	}
	var siblingID int64
	for i := range nodes {
		if nodes[i].ID != nodeID {
			continue
		}
		if i%2 == 0 && i+1 < len(nodes) {
			siblingID = nodes[i+1].ID
		} else if i%2 == 1 {
			siblingID = nodes[i-1].ID
		}
		break
	}
	return siblingID, nil
}

// GetHarvestHaSiblingNodeGroupID returns the node_group_id of this node's HA sibling (paired by pool node id order),
// or 0 if there is no paired sibling or the sibling has no active harvest mapping.
func (d *DataStoreRepository) GetHarvestHaSiblingNodeGroupID(ctx context.Context, nodeID int64) (int64, error) {
	siblingID, err := d.GetHarvestHaSiblingNodeID(ctx, nodeID)
	if err != nil {
		return 0, err
	}
	if siblingID == 0 {
		return 0, nil
	}
	var sibMap datamodel.NodeNodeGroupMap
	err = d.db.GORM().WithContext(ctx).Where("node_id = ?", siblingID).First(&sibMap).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return sibMap.NodeGroupID, nil
}
