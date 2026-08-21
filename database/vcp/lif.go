package database

import (
	"context"
	"errors"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
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
