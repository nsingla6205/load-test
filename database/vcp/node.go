package database

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// nodeEndpointAddressColumn matches the column name declared on datamodel.Node.
// AutoMigrate created it as a quoted, mixed-case identifier.
const nodeEndpointAddressColumn = "endpoint_Address"

// GetNodesByPoolID retrieves nodes by their corresponding pool ID
func (d *DataStoreRepository) GetNodesByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Node, error) {
	return getNodesByPoolID(d.db.GORM().Unscoped().WithContext(ctx), poolID)
}

// GetNodesByPoolIDs retrieves nodes for multiple pool IDs in a single query.
func (d *DataStoreRepository) GetNodesByPoolIDs(ctx context.Context, poolIDs []int64) ([]*datamodel.Node, error) {
	if len(poolIDs) == 0 {
		return nil, nil
	}
	return getNodesByPoolIDs(d.db.GORM().WithContext(ctx), poolIDs)
}

// UpdateNodeHostDNSNames updates persisted TLS host names for nodes in one pool.
func (d *DataStoreRepository) UpdateNodeHostDNSNames(
	ctx context.Context,
	poolID int64,
	hostNamesByEndpoint map[string]string,
) (err error) {
	if len(hostNamesByEndpoint) == 0 {
		return nil
	}
	tx, err := startTransaction(d.db.GORM().WithContext(ctx))
	if err != nil {
		return err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	for endpoint, hostName := range hostNamesByEndpoint {
		// The nodes column is the mixed-case identifier "endpoint_Address",
		// so it must be quoted by GORM rather than inlined into raw SQL.
		result := tx.Model(&datamodel.Node{}).
			Where("pool_id = ?", poolID).
			Where(clause.Eq{Column: clause.Column{Name: nodeEndpointAddressColumn}, Value: endpoint}).
			Update("host_dns_name", hostName)
		if result.Error != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
		}
		if result.RowsAffected != 1 {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				fmt.Errorf("node endpoint %q not found for pool %d", endpoint, poolID))
		}
	}
	return nil
}

// GetNodeByID retrieves a node by ID. Uses Unscoped to include soft-deleted nodes so callers can check State.
func (d *DataStoreRepository) GetNodeByID(ctx context.Context, nodeID int64) (*datamodel.Node, error) {
	var node datamodel.Node
	err := d.db.GORM().Unscoped().WithContext(ctx).First(&node, nodeID).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "node", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &node, nil
}

func getNodesByPoolID(db *gorm.DB, poolID int64) ([]*datamodel.Node, error) {
	return getNodesByPoolIDs(db, []int64{poolID})
}

func getNodesByPoolIDs(db *gorm.DB, poolIDs []int64) ([]*datamodel.Node, error) {
	var nodes []*datamodel.Node
	err := db.Where("pool_id IN ?", poolIDs).Order("pool_id ASC, id ASC").Find(&nodes).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "node", nil))
	}
	return nodes, nil
}

type faultDomainCountRow struct {
	ZoneName    string `gorm:"column:zone_name"`
	FaultDomain string `gorm:"column:fault_domain"`
	N           int64  `gorm:"column:n"`
}

// GetFaultDomainCountsByZone returns the number of active (non-deleted) nodes per
// (availability domain, fault domain), shaped as zone -> faultDomain -> count.
// Nodes without a fault domain assigned are excluded. The map enables fast lookups
// when selecting the least-loaded fault domain for a given availability domain.
func (d *DataStoreRepository) GetFaultDomainCountsByZone(ctx context.Context) (map[string]map[string]int64, error) {
	db := d.db.GORM().WithContext(ctx)
	var rows []faultDomainCountRow
	err := db.Raw(`
			SELECT zone_name, fault_domain, COUNT(*) AS n
			FROM nodes
			WHERE fault_domain <> '' AND deleted_at IS NULL
			GROUP BY zone_name, fault_domain`).Scan(&rows).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	out := make(map[string]map[string]int64, len(rows))
	for i := range rows {
		zone := rows[i].ZoneName
		if out[zone] == nil {
			out[zone] = make(map[string]int64)
		}
		out[zone][rows[i].FaultDomain] = rows[i].N
	}
	return out, nil
}

// GetNodesByHostIDAndNames resolves ONTAP node names to nodes table IDs for a hardware host.
// Returns an error when any requested name is missing.
func (d *DataStoreRepository) GetNodesByHostIDAndNames(ctx context.Context, hostID int64, names []string) (map[string]int64, error) {
	if hostID <= 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("hostID must be positive"))
	}
	if len(names) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("names cannot be empty"))
	}

	var nodes []*datamodel.Node
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Node{}).
		Where(datatypes.JSONQuery("node_attributes").Equals(hostID, "host_id")).
		Where(map[string]interface{}{"name": names}).
		Find(&nodes).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	out := make(map[string]int64, len(nodes))
	for _, node := range nodes {
		if node != nil {
			out[node.Name] = node.ID
		}
	}
	for _, name := range names {
		if _, ok := out[name]; !ok {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				fmt.Errorf("node %q not found for host id %d", name, hostID))
		}
	}
	return out, nil
}

// CreateNode creates a new Node entry in the database
func (d *DataStoreRepository) CreateNode(ctx context.Context, node *datamodel.Node) (*datamodel.Node, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	var dbNode datamodel.Node
	err1 := tx.Where("name = ? and account_id = ?", node.Name, node.AccountID).First(&dbNode).Error
	if errors.Is(err1, gorm.ErrRecordNotFound) {
		node.UUID = utils.RandomUUID()
		node.State = datamodel.LifeCycleStateREADY
		node.StateDetails = datamodel.LifeCycleStateAvailableDetails
		node.CreatedAt = time.Now()
		node.UpdatedAt = node.CreatedAt
		err = tx.Create(node).Error
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		err = tx.Where("name = ? and account_id = ?", node.Name, node.AccountID).First(&dbNode).Error
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		return &dbNode, nil
	} else if err1 != nil {
		logger.Errorf("Error while checking if node exists: %v", err1)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err1)
	}

	return nil, vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, customerrors.NewConflictErr("node already exists"))
}

// EnsureHostNode creates or updates a host-scoped node row keyed by (name, account_id).
// When a matching row already exists, it refreshes node_attributes.host_id if unset or unchanged.
// Rows already linked to a different host_id return a conflict.
func (d *DataStoreRepository) EnsureHostNode(ctx context.Context, node *datamodel.Node) (*datamodel.Node, error) {
	if node == nil || strings.TrimSpace(node.Name) == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("node name is required"))
	}
	if node.NodeAttributes == nil || node.NodeAttributes.HostID <= 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("host_id is required in node_attributes"))
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var existing datamodel.Node
	err = tx.First(&existing, &datamodel.Node{Name: node.Name, AccountID: node.AccountID}).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		now := time.Now().UTC()
		node.UUID = utils.RandomUUID()
		node.State = datamodel.LifeCycleStateREADY
		node.StateDetails = datamodel.LifeCycleStateAvailableDetails
		node.CreatedAt = now
		node.UpdatedAt = now
		if err = tx.Create(node).Error; err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		return node, nil
	}
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	existingHostID := int64(0)
	if existing.NodeAttributes != nil {
		existingHostID = existing.NodeAttributes.HostID
	}
	requestedHostID := node.NodeAttributes.HostID
	if existingHostID != 0 && existingHostID != requestedHostID {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			customerrors.NewConflictErr(fmt.Sprintf("node %q is already associated with host id %d", node.Name, existingHostID)))
	}

	if existing.NodeAttributes == nil {
		existing.NodeAttributes = &datamodel.NodeDetails{}
	}
	existing.NodeAttributes.HostID = requestedHostID
	if node.NodeAttributes.ExternalUUID != "" {
		existing.NodeAttributes.ExternalUUID = node.NodeAttributes.ExternalUUID
	}
	if node.EndpointAddress != "" {
		existing.EndpointAddress = node.EndpointAddress
	}
	existing.UpdatedAt = time.Now().UTC()
	if err = tx.Save(&existing).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return &existing, nil
}

// DeleteNode deletes a Node from the database
func (d *DataStoreRepository) DeleteNode(ctx context.Context, node *datamodel.Node) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	node.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	node.State = datamodel.LifeCycleStateDeleted
	node.StateDetails = datamodel.LifeCycleStateDeletedDetails
	err = tx.Updates(node).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// ErroredNode marks a Node state to error in the database
func (d *DataStoreRepository) ErroredNode(ctx context.Context, node *datamodel.Node, errMsg string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	node.UpdatedAt = time.Now()
	node.State = datamodel.LifeCycleStateError
	node.StateDetails = errMsg
	err = tx.Updates(node).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// DeletingNode updates the node entry to deleting state
func (d *DataStoreRepository) DeletingNode(ctx context.Context, node *datamodel.Node) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	node.State = datamodel.LifeCycleStateDeleting
	node.StateDetails = datamodel.LifeCycleStateDeletingDetails
	err = tx.Updates(node).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// UpdateNodesInstanceType updates the instance type for all nodes in a pool
func (d *DataStoreRepository) UpdateNodesInstanceType(ctx context.Context, poolID int64, newInstanceType string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// Get all nodes for the pool
	nodes, err := getNodesByPoolID(tx, poolID)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		logger.Warnf("No nodes found for pool ID %d", poolID)
		return nil
	}

	// Update instance type for each node
	for _, node := range nodes {
		if node.NodeAttributes == nil {
			node.NodeAttributes = &datamodel.NodeDetails{}
		}

		// Update instance type
		node.NodeAttributes.InstanceType = newInstanceType
		node.UpdatedAt = time.Now()

		err = tx.Model(&datamodel.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
			"node_attributes": node.NodeAttributes,
			"updated_at":      node.UpdatedAt,
		}).Error
		if err != nil {
			logger.Errorf("Failed to update instance type for node %s: %v", node.UUID, err)
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		}
	}

	logger.Infof("Successfully updated instance type to %s for %d nodes in pool %d", newInstanceType, len(nodes), poolID)
	return nil
}

func (d *DataStoreRepository) UpdateNodesSizeAndInstanceType(ctx context.Context, poolID int64, updatesByNodeName map[string]datamodel.NodeDetails) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	nodes, err := getNodesByPoolID(tx, poolID)
	if err != nil {
		return err
	}

	if len(nodes) == 0 {
		logger.Warnf("No nodes found for pool ID %d", poolID)
		return nil
	}

	updatedCount := 0
	for _, node := range nodes {
		update, ok := updatesByNodeName[node.Name]
		if !ok {
			continue
		}
		if node.NodeAttributes == nil {
			node.NodeAttributes = &datamodel.NodeDetails{}
		}
		node.NodeAttributes.InstanceType = update.InstanceType
		node.NodeAttributes.SizeInGiB = update.SizeInGiB
		node.UpdatedAt = time.Now()

		err = tx.Model(&datamodel.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
			"node_attributes": node.NodeAttributes,
			"updated_at":      node.UpdatedAt,
		}).Error
		if err != nil {
			logger.Errorf("Failed to update size and instance type for node %s: %v", node.UUID, err)
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		}
		updatedCount++
	}

	logger.Infof("Successfully updated size and instance type for %d nodes in pool %d", updatedCount, poolID)
	return nil
}
