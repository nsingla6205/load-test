package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	lifTypeInterclusterDB       = "intercluster"
	interclusterLIFIPsPerHAPair = 2
	// hostRouteNetmask matches data LIF rows (vsa.DefaultNetmask): a /32 host
	// route on the individual LIF IP, not the carved block's prefix mask.
	hostRouteNetmask = "255.255.255.255"
)

var (
	getLifWithDetails          = _getLifWithDetails
	getLifsWithProtocolDetails = _getLifsWithProtocolDetails
)

// GetLifByNodeID retrieves a LIF by its corresponding Node ID
func (d *DataStoreRepository) GetLifByNodeID(ctx context.Context, nodeID int64, accountID int64) (*datamodel.Lif, error) {
	lif, err := getLifWithDetails(d.db.GORM().Unscoped().WithContext(ctx), &datamodel.Lif{NodeID: nodeID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	return lif, nil
}

// GetLifsForNodesWithProtocol retrieves LIFs for multiple nodes with the specified protocol
func (d *DataStoreRepository) GetLifsForNodesWithProtocol(ctx context.Context, nodeIDs []int64, accountID int64, protocol string) ([]*datamodel.Lif, error) {
	if len(nodeIDs) == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, customerrors.NewUserInputValidationErr("nodeIDs cannot be empty"))
	}

	db := d.db.GORM().WithContext(ctx)

	// Diagnosis LIFs are temporary and must never be returned as operational
	// LIFs for volume mount or validation paths.
	dbQuery := db.Where(
		"node_id IN ? AND account_id = ? AND is_directory_diagnosis = ?",
		nodeIDs,
		accountID,
		false,
	)

	return getLifsWithProtocolDetails(dbQuery, protocol)
}

// CreateLif creates a new LIF in the database
func (d *DataStoreRepository) CreateLif(ctx context.Context, lif *datamodel.Lif) (*datamodel.Lif, error) {
	var dbLif datamodel.Lif
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	err1 := tx.Where("name = ? and node_id = ? and account_id = ?", lif.Name, lif.NodeID, lif.AccountID).First(&dbLif).Error
	if errors.Is(err1, gorm.ErrRecordNotFound) {
		lif.UUID = utils.RandomUUID()
		lif.CreatedAt = time.Now()
		lif.UpdatedAt = lif.CreatedAt
		err = tx.Create(lif).Error
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		err = tx.Where("name = ? and account_id = ?", lif.Name, lif.AccountID).First(&dbLif).Error
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		return &dbLif, nil
	} else if err1 != nil {
		logger.Errorf("Error while checking if lif exists: %v", err1)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err1)
	}
	return nil, vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, customerrors.NewConflictErr("lif already exists"))
}

// DeleteLif deletes a LIF from the database
func (d *DataStoreRepository) DeleteLif(ctx context.Context, lif *datamodel.Lif) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	lif.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	err = tx.Updates(lif).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

func (d *DataStoreRepository) GetLifForNode(ctx context.Context, nodeID int64, accountID int64) (*datamodel.Lif, error) {
	db := d.db.GORM().WithContext(ctx).Where("is_directory_diagnosis = ?", false)
	lif, err := getLifWithDetails(db, &datamodel.Lif{NodeID: nodeID, AccountID: accountID})
	if err != nil {
		return nil, err
	}
	return lif, nil
}

func _getLifWithDetails(db *gorm.DB, query *datamodel.Lif) (*datamodel.Lif, error) {
	lif := &datamodel.Lif{}
	err := db.First(&lif, query).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "lif", nil))
	}
	return lif, nil
}

// _getLifsWithProtocolDetails retrieves a LIF with protocol details from the database
func _getLifsWithProtocolDetails(dbQuery *gorm.DB, protocol string) ([]*datamodel.Lif, error) {
	lifs := []*datamodel.Lif{}

	if protocol != "" {
		dbQuery = dbQuery.Where("lif_details->>'protocol_type' = ?", protocol)
	}

	err := dbQuery.Find(&lifs).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "lif", nil))
	}
	return lifs, nil
}

// ReserveInterclusterLifIPs reserves count IC LIF IPs from the carved ip_ranges block for a pool.
func (d *DataStoreRepository) ReserveInterclusterLifIPs(ctx context.Context, poolID, accountID, ipRangeID int64, count int) ([]string, error) {
	if poolID == 0 || ipRangeID == 0 || count <= 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
			customerrors.NewUserInputValidationErr("poolID, ipRangeID, and positive count are required"))
	}
	var reserved []string
	err := d.db.GORM().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ir datamodel.IPRange
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND deleted_at IS NULL", ipRangeID).
			First(&ir).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				ipRangeIDStr := strconv.FormatInt(ipRangeID, 10)
				return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("IPRange", &ipRangeIDStr))
			}
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}

		var existingForRange []*datamodel.Lif
		if err := tx.Where("pool_id = ? AND ip_range_id = ? AND type = ? AND deleted_at IS NULL",
			poolID, ipRangeID, lifTypeInterclusterDB).
			Find(&existingForRange).Error; err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		if len(existingForRange) > 0 {
			if len(existingForRange) != count {
				return vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
					fmt.Errorf("partial IC LIF reservation for pool_id=%d ip_range_id=%d: have %d, need %d",
						poolID, ipRangeID, len(existingForRange), count))
			}
			for _, lif := range existingForRange {
				reserved = append(reserved, lif.IPAddress)
			}
			sortIPAddressesNumerically(reserved)
			return nil
		}

		var staleForPool []*datamodel.Lif
		if err := tx.Where("pool_id = ? AND type = ? AND deleted_at IS NULL AND (ip_range_id IS NULL OR ip_range_id != ?)",
			poolID, lifTypeInterclusterDB, ipRangeID).
			Find(&staleForPool).Error; err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		if len(staleForPool) > 0 {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("stale IC LIF rows for pool_id=%d under a different ip_range_id (requested=%d)", poolID, ipRangeID))
		}

		used := make(map[string]struct{})
		var usedRows []*datamodel.Lif
		if err := tx.Where("ip_range_id = ? AND type = ? AND deleted_at IS NULL", ipRangeID, lifTypeInterclusterDB).
			Find(&usedRows).Error; err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		for _, lif := range usedRows {
			used[lif.IPAddress] = struct{}{}
		}

		candidates, err := hostIPsInRange(ir.StartIP, ir.EndIP)
		if err != nil {
			return vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError, err)
		}
		picked := make([]string, 0, count)
		for _, ip := range candidates {
			if len(picked) >= count {
				break
			}
			if _, ok := used[ip]; ok {
				continue
			}
			picked = append(picked, ip)
		}
		if len(picked) < count {
			return vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				fmt.Errorf("insufficient free IC LIF IPs in range id=%d: need %d, found %d", ipRangeID, count, len(picked)))
		}

		now := time.Now().UTC()
		for i, ip := range picked {
			lif := &datamodel.Lif{
				BaseModel: datamodel.BaseModel{
					UUID:      utils.RandomUUID(),
					CreatedAt: now,
					UpdatedAt: now,
				},
				Name:       fmt.Sprintf("ic-lif-pool-%d-%d", poolID, i),
				AccountID:  accountID,
				Type:       lifTypeInterclusterDB,
				IPAddress:  ip,
				NodeID:     0,
				SubnetMask: hostRouteNetmask,
				PoolID:     sql.NullInt64{Int64: poolID, Valid: true},
				IPRangeID:  sql.NullInt64{Int64: ipRangeID, Valid: true},
				LifDetails: &datamodel.LifDetails{
					ProtocolType: lifTypeInterclusterDB,
					HAPairIndex:  i / interclusterLIFIPsPerHAPair,
				},
			}
			if err := tx.Create(lif).Error; err != nil {
				return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
			}
		}
		reserved = picked
		return nil
	})
	if err != nil {
		return nil, err
	}
	return reserved, nil
}

// ListInterclusterLifsByPoolID returns active reserved intercluster lif rows for a pool in IP order.
func (d *DataStoreRepository) ListInterclusterLifsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Lif, error) {
	if poolID == 0 {
		return nil, nil
	}
	var lifs []*datamodel.Lif
	err := d.db.GORM().WithContext(ctx).
		Where("pool_id = ? AND type = ? AND deleted_at IS NULL", poolID, lifTypeInterclusterDB).
		Find(&lifs).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	sortLifsByIPAddressNumerically(lifs)
	return lifs, nil
}

func sortIPAddressesNumerically(ips []string) {
	sort.Slice(ips, func(i, j int) bool {
		return compareIPAddressesNumerically(ips[i], ips[j]) < 0
	})
}

func sortLifsByIPAddressNumerically(lifs []*datamodel.Lif) {
	sort.Slice(lifs, func(i, j int) bool {
		return compareIPAddressesNumerically(lifs[i].IPAddress, lifs[j].IPAddress) < 0
	})
}

func compareIPAddressesNumerically(left, right string) int {
	leftIP := net.ParseIP(strings.TrimSpace(left))
	rightIP := net.ParseIP(strings.TrimSpace(right))
	if leftIP == nil || rightIP == nil {
		return strings.Compare(left, right)
	}
	leftV4 := leftIP.To4()
	rightV4 := rightIP.To4()
	if leftV4 == nil || rightV4 == nil {
		return strings.Compare(left, right)
	}
	leftN := ipToUint32(leftV4)
	rightN := ipToUint32(rightV4)
	switch {
	case leftN < rightN:
		return -1
	case leftN > rightN:
		return 1
	default:
		return 0
	}
}

// hostIPsInRange returns candidate host IPs between startIP and endIP inclusive.
// When the span is a CIDR-sized block (at least 4 addresses), the network and
// broadcast addresses are excluded to match the design usable-host assumption
// (e.g. /29 => 6 usable IPs). Smaller spans keep inclusive bounds.
func hostIPsInRange(startIP, endIP string) ([]string, error) {
	start := net.ParseIP(strings.TrimSpace(startIP)).To4()
	end := net.ParseIP(strings.TrimSpace(endIP)).To4()
	if start == nil || end == nil {
		return nil, fmt.Errorf("invalid IP range bounds %q-%q", startIP, endIP)
	}
	startN := ipToUint32(start)
	endN := ipToUint32(end)
	if endN < startN {
		return nil, fmt.Errorf("invalid IP range bounds %q-%q", startIP, endIP)
	}
	span := endN - startN + 1
	first, last := startN, endN
	if span >= 4 {
		first++
		last--
	}
	out := make([]string, 0, int(last-first+1))
	for n := first; n <= last; n++ {
		out = append(out, uint32ToIP(n).String())
	}
	return out, nil
}

func ipToUint32(ip net.IP) uint32 {
	v4 := ip.To4()
	if v4 == nil {
		return 0
	}
	return uint32(v4[0])<<24 | uint32(v4[1])<<16 | uint32(v4[2])<<8 | uint32(v4[3])
}

func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}

// BindInterclusterLifToNode links a reserved intercluster lif row to its node after deploy.
func (d *DataStoreRepository) BindInterclusterLifToNode(ctx context.Context, poolID int64, ipAddress string, nodeID int64) error {
	if poolID == 0 || nodeID == 0 || strings.TrimSpace(ipAddress) == "" {
		return nil
	}
	db := d.db.GORM().WithContext(ctx)
	var lif datamodel.Lif
	err := db.Where(
		"pool_id = ? AND type = ? AND ip_address = ? AND deleted_at IS NULL",
		poolID, lifTypeInterclusterDB, ipAddress,
	).First(&lif).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	lif.NodeID = nodeID
	if lif.LifDetails == nil {
		lif.LifDetails = &datamodel.LifDetails{ProtocolType: lifTypeInterclusterDB}
	}
	lif.UpdatedAt = time.Now().UTC()
	if err := db.Save(&lif).Error; err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// DeleteInterclusterLifsByPoolID soft-deletes reserved intercluster lifs for a pool.
func (d *DataStoreRepository) DeleteInterclusterLifsByPoolID(ctx context.Context, poolID int64) error {
	if poolID == 0 {
		return nil
	}
	now := time.Now().UTC()
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Lif{}).
		Where("pool_id = ? AND type = ? AND deleted_at IS NULL", poolID, lifTypeInterclusterDB).
		Update("deleted_at", now).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}
