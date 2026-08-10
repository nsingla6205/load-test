package database

import (
	"context"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
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
