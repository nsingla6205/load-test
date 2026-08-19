package database

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetInterclusterLifs returns active intercluster LIF rows, optionally filtered by
// accountUUID and/or ipRangeUUID. Related UUIDs/names are enriched on return.
func (d *DataStoreRepository) GetInterclusterLifs(ctx context.Context, accountUUID, ipRangeUUID *string) ([]*datamodel.Lif, error) {
	db := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Lif{}).
		Where("type = ?", LifTypeInterclusterLIF)

	if accountUUID != nil && strings.TrimSpace(*accountUUID) != "" {
		account, err := d.GetAccountByUUID(ctx, strings.TrimSpace(*accountUUID))
		if err != nil {
			return nil, err
		}
		db = db.Where("account_id = ?", account.ID)
	}

	if ipRangeUUID != nil && strings.TrimSpace(*ipRangeUUID) != "" {
		ipRange, err := d.GetIPRangeByUUID(ctx, strings.TrimSpace(*ipRangeUUID))
		if err != nil {
			return nil, err
		}
		db = db.Where("ip_range_id = ?", ipRange.ID)
	}

	var results []*datamodel.Lif
	if err := db.Find(&results).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if err := d.enrichInterclusterLifs(ctx, results); err != nil {
		return nil, err
	}
	return results, nil
}

// GetInterclusterLifsForAccountHost returns active intercluster LIF rows for an
// account whose node belongs to the given host (nodes.node_attributes.host_id).
func (d *DataStoreRepository) GetInterclusterLifsForAccountHost(ctx context.Context, accountID, hostID int64) ([]*datamodel.Lif, error) {
	var results []*datamodel.Lif
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Lif{}).
		Joins("JOIN nodes ON nodes.id = lifs.node_id").
		Where("lifs.deleted_at IS NULL").
		Where("nodes.deleted_at IS NULL").
		Where("lifs.account_id = ?", accountID).
		Where("lifs.type = ?", LifTypeInterclusterLIF).
		Where("(nodes.node_attributes->>'host_id')::bigint = ?", hostID).
		Order("lifs.id ASC").
		Find(&results).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if err := d.enrichInterclusterLifs(ctx, results); err != nil {
		return nil, err
	}
	return results, nil
}

// GetInterclusterLifIPRanges returns active interclusterLIF ip_ranges for the
// account and network (VendorSubNetID), ordered by created_at then UUID.
// Non-empty accountUUID / network are enforced by the caller
// (POICVNActivity.EnsurePOICNetwork.validateInput).
func (d *DataStoreRepository) GetInterclusterLifIPRanges(ctx context.Context, accountUUID, network string) ([]*datamodel.IPRange, error) {
	account, err := d.GetAccountByUUID(ctx, accountUUID)
	if err != nil {
		return nil, err
	}

	networkVal := network
	var results []*datamodel.IPRange
	err = d.db.GORM().WithContext(ctx).
		Model(&datamodel.IPRange{}).
		Where(&datamodel.IPRange{
			LifType:   LifTypeInterclusterLIF,
			AccountID: sql.NullInt64{Int64: account.ID, Valid: true},
			Network:   &networkVal,
		}).
		Order("created_at ASC, uuid ASC").
		Find(&results).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if len(results) == 0 {
		return results, nil
	}

	region, err := d.getRegionByID(ctx, results[0].RegionID.Int64)
	if err != nil {
		return nil, err
	}
	for _, ipRange := range results {
		ipRange.AccountUUID = account.UUID
		ipRange.AccountName = account.Name
		ipRange.RegionUUID = region.UUID
		ipRange.RegionName = region.Name
	}
	return results, nil
}

// AllocateInterclusterLifIPs allocates one IC LIF IP per reservation in a single
// transaction. Lif.Type is forced to interclusterLIF and SvmID is left null.
// Input validation (non-empty reservations, positive AccountID/NodeID, etc.)
// is enforced by the caller (POICLifActivity.AllocatePOInterclusterLifIPs).
func (d *DataStoreRepository) AllocateInterclusterLifIPs(ctx context.Context, ipRangeUUID string, reservations []*datamodel.Lif) ([]*datamodel.Lif, error) {
	ipRange, err := d.GetIPRangeByUUID(ctx, ipRangeUUID)
	if err != nil {
		return nil, err
	}
	if ipRange.LifType != LifTypeInterclusterLIF {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			fmt.Errorf("ip range %s has lif_type %q, expected %q", ipRange.UUID, ipRange.LifType, LifTypeInterclusterLIF))
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var lockedRange datamodel.IPRange
	lockQuery := tx.Where("uuid = ? AND deleted_at IS NULL", ipRange.UUID)
	// SELECT ... FOR UPDATE guards concurrent IC LIF allocations that target
	// the same ip_range from different hosts. The parent Temporal child
	// workflow (POICLifEnsureWorkflow) serializes only per (account, host),
	// but ip_ranges are scoped per (account, network=VendorSubNetID) and can
	// be shared by multiple hosts under one account. Without this lock, two
	// hosts allocating in parallel would compute the same next-free IPs and
	// one would fail on the idx_lifs_ip_range_ip_active unique index. The
	// isPostgresDialect gate mirrors node_nodegroup_map.go and
	// volume_performance_group.go because SQLite (used by unit tests) does
	// not support FOR UPDATE.
	if isPostgresDialect(tx) {
		lockQuery = lockQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	if err = lockQuery.First(&lockedRange).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	allocatedIPs, err := queryAllocatedIPsInIPRange(ctx, tx, lockedRange.ID)
	if err != nil {
		return nil, err
	}

	addresses, err := commonparams.AllocateIPAddressesFromRange(
		lockedRange.StartIP, lockedRange.EndIP, lockedRange.Gateway, lockedRange.Netmask, len(reservations), allocatedIPs)
	if err != nil {
		if customerrors.IsUnavailableErr(err) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError, err)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
	}

	now := time.Now().UTC()
	created := make([]*datamodel.Lif, 0, len(reservations))
	for i, reservation := range reservations {
		lif := &datamodel.Lif{
			BaseModel: datamodel.BaseModel{
				UUID:      utils.RandomUUID(),
				CreatedAt: now,
				UpdatedAt: now,
			},
			Name:       reservation.Name,
			AccountID:  reservation.AccountID,
			Type:       LifTypeInterclusterLIF,
			IPAddress:  addresses[i],
			NodeID:     reservation.NodeID,
			SubnetMask: lockedRange.Netmask,
			IPRangeID:  sql.NullInt64{Int64: lockedRange.ID, Valid: true},
			SvmID:      sql.NullInt64{},
			LifDetails: &datamodel.LifDetails{},
		}
		if err = tx.Create(lif).Error; err != nil {
			if isDataLifIPRangeUniqueViolation(err) {
				return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
					customerrors.NewConflictErr("IP address has already been allocated"))
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		created = append(created, lif)
	}
	return created, nil
}

// UpdateInterclusterLifExternalUUID persists the ONTAP LIF UUID onto lif_details.
func (d *DataStoreRepository) UpdateInterclusterLifExternalUUID(ctx context.Context, lifUUID, externalUUID string) error {
	lifUUID = strings.TrimSpace(lifUUID)
	externalUUID = strings.TrimSpace(externalUUID)
	if lifUUID == "" || externalUUID == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("lifUUID and externalUUID are required"))
	}

	var lif datamodel.Lif
	err := d.db.GORM().WithContext(ctx).
		Where("uuid = ? AND type = ? AND deleted_at IS NULL", lifUUID, LifTypeInterclusterLIF).
		First(&lif).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, err)
		}
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	if lif.LifDetails == nil {
		lif.LifDetails = &datamodel.LifDetails{}
	}
	lif.LifDetails.ExternalUUID = externalUUID
	lif.UpdatedAt = time.Now().UTC()
	if err := d.db.GORM().WithContext(ctx).Model(&lif).
		Updates(map[string]interface{}{
			"lif_details": lif.LifDetails,
			"updated_at":  lif.UpdatedAt,
		}).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// DeleteInterclusterLifsForAccountHost bulk soft-deletes active IC LIF rows
// for the (accountID, hostID) pair, matching GetInterclusterLifsForAccountHost's
// filter set. Postgres-only (relies on the ->> JSON operator on node_attributes).
// The node_id IN (SELECT ...) subquery form is required because GORM v2 silently
// drops .Joins() on Update statements.
func (d *DataStoreRepository) DeleteInterclusterLifsForAccountHost(ctx context.Context, accountID, hostID int64) error {
	db := d.db.GORM().WithContext(ctx)
	nodeIDs := db.Model(&datamodel.Node{}).
		Select("id").
		Where("(node_attributes->>'host_id')::bigint = ?", hostID)
	result := db.Model(&datamodel.Lif{}).
		Where("account_id = ?", accountID).
		Where("type = ?", LifTypeInterclusterLIF).
		Where("node_id IN (?)", nodeIDs).
		UpdateColumn("deleted_at", time.Now().UTC())
	if result.Error != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}
	util.GetLogger(ctx).Debugf(
		"DeleteInterclusterLifsForAccountHost: soft-deleted %d IC LIF row(s) for account_id=%d host_id=%d",
		result.RowsAffected, accountID, hostID,
	)
	return nil
}

func (d *DataStoreRepository) enrichInterclusterLifs(ctx context.Context, lifs []*datamodel.Lif) error {
	if len(lifs) == 0 {
		return nil
	}

	accountIDs := make(map[int64]struct{})
	nodeIDs := make(map[int64]struct{})
	ipRangeIDs := make(map[int64]struct{})
	for _, lif := range lifs {
		if lif == nil {
			continue
		}
		if lif.AccountID > 0 {
			accountIDs[lif.AccountID] = struct{}{}
		}
		if lif.NodeID > 0 {
			nodeIDs[lif.NodeID] = struct{}{}
		}
		if lif.IPRangeID.Valid && lif.IPRangeID.Int64 > 0 {
			ipRangeIDs[lif.IPRangeID.Int64] = struct{}{}
		}
	}

	accountsByID, err := d.getAccountsByIDs(ctx, mapKeys(accountIDs))
	if err != nil {
		return err
	}
	nodesByID, err := d.getNodesByIDs(ctx, mapKeys(nodeIDs))
	if err != nil {
		return err
	}
	ipRangesByID, err := d.getIPRangesByIDs(ctx, mapKeys(ipRangeIDs))
	if err != nil {
		return err
	}

	hostIDs := make(map[int64]struct{})
	for _, node := range nodesByID {
		if node != nil && node.NodeAttributes != nil && node.NodeAttributes.HostID > 0 {
			hostIDs[node.NodeAttributes.HostID] = struct{}{}
		}
	}
	hostsByID, err := d.getHostsByIDs(ctx, mapKeys(hostIDs))
	if err != nil {
		return err
	}

	for _, lif := range lifs {
		if lif == nil {
			continue
		}
		if account, ok := accountsByID[lif.AccountID]; ok && account != nil {
			lif.AccountUUID = account.UUID
			lif.AccountName = account.Name
		}
		if node, ok := nodesByID[lif.NodeID]; ok && node != nil {
			lif.NodeUUID = node.UUID
			if node.NodeAttributes != nil && node.NodeAttributes.HostID > 0 {
				if host, ok := hostsByID[node.NodeAttributes.HostID]; ok && host != nil {
					lif.HostUUID = host.UUID
				}
			}
		}
		if lif.IPRangeID.Valid {
			if ipRange, ok := ipRangesByID[lif.IPRangeID.Int64]; ok && ipRange != nil {
				lif.IPRangeUUID = ipRange.UUID
			}
		}
	}
	return nil
}

func mapKeys[T comparable](m map[T]struct{}) []T {
	keys := make([]T, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (d *DataStoreRepository) getAccountsByIDs(ctx context.Context, ids []int64) (map[int64]*datamodel.Account, error) {
	out := make(map[int64]*datamodel.Account, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var accounts []*datamodel.Account
	if err := d.db.GORM().WithContext(ctx).Where("id IN ?", ids).Find(&accounts).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	for _, account := range accounts {
		if account != nil {
			out[account.ID] = account
		}
	}
	return out, nil
}

func (d *DataStoreRepository) getNodesByIDs(ctx context.Context, ids []int64) (map[int64]*datamodel.Node, error) {
	out := make(map[int64]*datamodel.Node, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var nodes []*datamodel.Node
	if err := d.db.GORM().WithContext(ctx).Where("id IN ?", ids).Find(&nodes).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	for _, node := range nodes {
		if node != nil {
			out[node.ID] = node
		}
	}
	return out, nil
}

func (d *DataStoreRepository) getIPRangesByIDs(ctx context.Context, ids []int64) (map[int64]*datamodel.IPRange, error) {
	out := make(map[int64]*datamodel.IPRange, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var ipRanges []*datamodel.IPRange
	if err := d.db.GORM().WithContext(ctx).Where("id IN ?", ids).Find(&ipRanges).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	for _, ipRange := range ipRanges {
		if ipRange != nil {
			out[ipRange.ID] = ipRange
		}
	}
	return out, nil
}

func (d *DataStoreRepository) getHostsByIDs(ctx context.Context, ids []int64) (map[int64]*datamodel.Host, error) {
	out := make(map[int64]*datamodel.Host, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var hosts []*datamodel.Host
	if err := d.db.GORM().WithContext(ctx).Where("id IN ?", ids).Find(&hosts).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	for _, host := range hosts {
		if host != nil {
			out[host.ID] = host
		}
	}
	return out, nil
}
