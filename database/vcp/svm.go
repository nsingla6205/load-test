package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// isSvmExternalIdentifierUniqueViolation reports whether err is a unique-index
// violation from the partial unique index on svm_external_identifier.
func isSvmExternalIdentifierUniqueViolation(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint")
}

var (
	getSvmsByKmsConfigID    = _getSvmsByKmsConfigID
	listSvmsWithAccountId   = _listSvmsWithAccountId
	listHardwareClusterSVMs = _listHardwareClusterSVMs
)

const (
	serviceLevelExtreme = "EXTREME"
	serviceLevelPremium = "PREMIUM"
)

type lifIPRangeResult struct {
	SvmID       int64  `gorm:"column:svm_id"`
	IPAddress   string `gorm:"column:ip_address"`
	IPRangeUUID string `gorm:"column:ip_range_uuid"`
}

// GetSvmsByPoolID retrieves SVMs by its corresponding pool ID
func (d *DataStoreRepository) GetSvmsByPoolID(ctx context.Context, poolID int64) ([]*datamodel.Svm, error) {
	var svms []*datamodel.Svm
	err := d.db.GORM().Unscoped().WithContext(ctx).Where("pool_id = ?", poolID).Find(&svms).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return svms, nil
}

func (d *DataStoreRepository) ActiveSvmExistsByPoolID(ctx context.Context, poolID int64) (bool, error) {
	var svm datamodel.Svm
	err := d.db.GORM().WithContext(ctx).
		Select("id").
		Where("pool_id = ? AND state <> ?", poolID, datamodel.LifeCycleStateDeleted).
		Limit(1).
		First(&svm).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return true, nil
}

// GetNextSVMIndexByPoolID retrieves the next SVM index (count + 1) by pool ID
func (d *DataStoreRepository) GetNextSVMIndexByPoolID(ctx context.Context, poolID int64) (int64, error) {
	var count int64
	err := d.db.GORM().Unscoped().WithContext(ctx).Model(&datamodel.Svm{}).Where("pool_id = ?", poolID).Count(&count).Error
	if err != nil {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return count + 1, nil
}

// GetSvmsByKmsConfigID retrieves SVMs by kms config id
func (d *DataStoreRepository) GetSvmsByKmsConfigID(ctx context.Context, kmsConfigID int64) ([]*datamodel.Svm, error) {
	return getSvmsByKmsConfigID(d.db.GORM().WithContext(ctx), kmsConfigID)
}

func _getSvmsByKmsConfigID(db *gorm.DB, kmsConfigID int64) ([]*datamodel.Svm, error) {
	var svms []*datamodel.Svm
	err := db.Where("kms_config_id = ?", kmsConfigID).Find(&svms).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return svms, nil
}

// CreateSVM creates a new SVM in the database, or finalizes an SVM row that was pre-allocated in CREATING state.
func (d *DataStoreRepository) CreateSVM(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	var dbSvm datamodel.Svm
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	lookup := func(into *datamodel.Svm) error {
		if svm.SvmExternalIdentifier != "" {
			return tx.Unscoped().Where("account_id = ? AND svm_external_identifier = ?", svm.AccountID, svm.SvmExternalIdentifier).First(into).Error
		}
		return tx.Where("account_id = ?", svm.AccountID).Where("name = ?", svm.Name).Where("pool_id = ?", svm.PoolID).First(into).Error
	}

	err1 := lookup(&dbSvm)
	if errors.Is(err1, gorm.ErrRecordNotFound) {
		svm.UUID = utils.RandomUUID()
		svm.CreatedAt = time.Now()
		svm.UpdatedAt = svm.CreatedAt
		svm.State = datamodel.LifeCycleStateREADY
		svm.StateDetails = datamodel.LifeCycleStateAvailableDetails

		err = tx.Create(svm).Error
		if err != nil {
			if svm.SvmExternalIdentifier != "" && isSvmExternalIdentifierUniqueViolation(err) {
				return nil, customerrors.NewConflictErr("svm already exists")
			}
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		}
		err = lookup(&dbSvm)
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		}
		return &dbSvm, nil
	} else if err1 != nil {
		logger.Errorf("Error while checking if svm exists: %v", err1)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err1)
	}

	switch dbSvm.State {
	case datamodel.LifeCycleStateCreating:
		// Row was pre-allocated by CreateSvmInCreatingState; finalize it now that
		// VLMConfig-derived fields are available.
		dbSvm.SvmDetails = svm.SvmDetails
		if svm.SvmExternalIdentifier != "" {
			dbSvm.SvmExternalIdentifier = svm.SvmExternalIdentifier
		}
		dbSvm.UpdatedAt = time.Now()
		dbSvm.State = datamodel.LifeCycleStateREADY
		dbSvm.StateDetails = datamodel.LifeCycleStateAvailableDetails
		if err = tx.Save(&dbSvm).Error; err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
		}
		return &dbSvm, nil
	case datamodel.LifeCycleStateREADY:
		// Idempotent retry: row already finalized.
		return &dbSvm, nil
	default:
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrIncorrectVSAClusterState, customerrors.NewConflictErr("svm already exists"))
	}
}

// CreateSvmInCreatingState pre-allocates an SVM row in CREATING state.
func (d *DataStoreRepository) CreateSvmInCreatingState(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	if svm == nil {
		return nil, customerrors.NewBadRequestErr("svm must not be nil")
	}
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	if svm.SvmExternalIdentifier != "" {
		var existing datamodel.Svm
		res := tx.Unscoped().
			Where("account_id = ? AND svm_external_identifier = ?", svm.AccountID, svm.SvmExternalIdentifier).
			Limit(1).
			Find(&existing)
		if res.Error != nil {
			logger.Errorf("Error while checking if svm exists by external identifier: %v", res.Error)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, res.Error)
		}
		if res.RowsAffected > 0 {
			if isIdempotentCreatingRetry(&existing, svm) {
				return &existing, nil
			}
			return nil, customerrors.NewConflictErr("svm already exists")
		}
	} else {
		var dbSvm datamodel.Svm
		res := tx.Where("account_id = ?", svm.AccountID).
			Where("name = ?", svm.Name).
			Where("pool_id = ?", svm.PoolID).
			Limit(1).
			Find(&dbSvm)
		if res.Error != nil {
			logger.Errorf("Error while checking if svm exists: %v", res.Error)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, res.Error)
		}
		if res.RowsAffected > 0 {
			if dbSvm.State == datamodel.LifeCycleStateCreating &&
				dbSvm.SvmExternalIdentifier == svm.SvmExternalIdentifier {
				return &dbSvm, nil
			}
			return nil, customerrors.NewConflictErr("svm with same name already exists in this pool")
		}
	}

	svm.UUID = utils.RandomUUID()
	svm.CreatedAt = time.Now()
	svm.UpdatedAt = svm.CreatedAt
	svm.State = datamodel.LifeCycleStateCreating
	svm.StateDetails = datamodel.LifeCycleStateCreatingDetails

	if err = tx.Create(svm).Error; err != nil {
		if svm.SvmExternalIdentifier != "" && isSvmExternalIdentifierUniqueViolation(err) {
			return nil, customerrors.NewConflictErr("svm already exists")
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}

	var fresh datamodel.Svm
	if err = tx.Where("id = ?", svm.ID).First(&fresh).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &fresh, nil
}

// isIdempotentCreatingRetry reports whether `existing` represents the same
// logical insert as `incoming` so a retry can return the existing row
func isIdempotentCreatingRetry(existing, incoming *datamodel.Svm) bool {
	if existing.DeletedAt != nil && existing.DeletedAt.Valid {
		return false
	}
	return existing.State == datamodel.LifeCycleStateCreating &&
		existing.Name == incoming.Name &&
		existing.PoolID == incoming.PoolID
}

func (d *DataStoreRepository) GetSvmForPoolID(ctx context.Context, poolID int64) (*datamodel.Svm, error) {
	return getSvmWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Svm{PoolID: poolID})
}

func getSvmWithDetails(db *gorm.DB, query *datamodel.Svm) (*datamodel.Svm, error) {
	svm := &datamodel.Svm{}
	err := db.Preload("Account").First(&svm, query).Error
	if err != nil {
		return nil, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "svm", nil)
	}
	return svm, nil
}

// DeleteSVM deletes an SVM from the database
func (d *DataStoreRepository) DeleteSVM(ctx context.Context, svm *datamodel.Svm) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	svm.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	svm.State = datamodel.LifeCycleStateDeleted
	svm.StateDetails = datamodel.LifeCycleStateDeletedDetails
	err = tx.Updates(svm).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// ErroredSVM marks an SVM with error state the database
func (d *DataStoreRepository) ErroredSVM(ctx context.Context, svm *datamodel.Svm, errMsg string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	svm.UpdatedAt = time.Now()
	svm.State = datamodel.LifeCycleStateError
	svm.StateDetails = errMsg
	err = tx.Updates(svm).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// DeletingSVM deletes an SVM from the database
func (d *DataStoreRepository) DeletingSVM(ctx context.Context, svm *datamodel.Svm) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)
	svm.State = datamodel.LifeCycleStateDeleting
	svm.StateDetails = datamodel.LifeCycleStateDeletingDetails
	err = tx.Updates(svm).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return nil
}

// TransitionSvmToDeleting atomically flips an SVM's state to DELETING
func (d *DataStoreRepository) TransitionSvmToDeleting(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	if svm == nil {
		return nil, customerrors.NewBadRequestErr("svm must not be nil")
	}
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// NOTE: ERROR is a deletable source state per the orchestrator's
	// validateSvmDeletionState contract (only DELETED, DELETING, CREATING are
	// rejected)
	var updated datamodel.Svm
	res := tx.Model(&updated).
		Clauses(clause.Returning{}).
		Where("id = ?", svm.ID).
		Where("state NOT IN ?", []string{
			datamodel.LifeCycleStateDeleted,
			datamodel.LifeCycleStateDeleting,
			datamodel.LifeCycleStateCreating,
			// datamodel.LifeCycleStateError,(currently allowing error svm to delete)
		}).
		Updates(map[string]interface{}{
			"state":         datamodel.LifeCycleStateDeleting,
			"state_details": datamodel.LifeCycleStateDeletingDetails,
			"workflow_id":   svm.WorkflowID,
			"updated_at":    time.Now(),
		})
	if res.Error != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, res.Error)
	}
	if res.RowsAffected == 1 {
		return &updated, nil
	}

	var current datamodel.Svm
	if ferr := tx.Where("id = ?", svm.ID).First(&current).Error; ferr != nil {
		if errors.Is(ferr, gorm.ErrRecordNotFound) {
			return nil, customerrors.NewNotFoundErr("svm not found", nil)
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, ferr)
	}
	switch current.State {
	case datamodel.LifeCycleStateDeleted:
		return nil, customerrors.NewNotFoundErr("svm deleted already", nil)
	case datamodel.LifeCycleStateDeleting:
		return nil, customerrors.NewConflictErr("SVM delete is already in progress")
	case datamodel.LifeCycleStateCreating:
		return nil, customerrors.NewConflictErr("SVM cannot be deleted while creation is in progress")
	default:
		return nil, customerrors.NewConflictErr("svm not in a state that allows delete")
	}
}

// UpdateSvmState updates state, stateDetails, and updatedAt in a single transaction.
// Pass optional WHERE conditions (same []interface{} format as GORM Where) for CAS-style
// guarded transitions. When no row is affected, it returns NotFoundErr (row missing) or
// ConflictErr (conditions not met). If svm.WorkflowID is non-empty, workflow_id is
// persisted alongside the state change.
func (d *DataStoreRepository) UpdateSvmState(ctx context.Context, svm *datamodel.Svm, state string, stateDetails string, conditions ...[]interface{}) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	updates := map[string]interface{}{
		"state":         state,
		"state_details": stateDetails,
		"updated_at":    time.Now(),
	}
	if svm.WorkflowID != "" {
		updates["workflow_id"] = svm.WorkflowID
	}

	query := tx.Model(&datamodel.Svm{}).Where("id = ?", svm.ID)
	for _, c := range conditions {
		if len(c) > 0 {
			query = query.Where(c[0], c[1:]...)
		}
	}

	res := query.Updates(updates)
	if res.Error != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, res.Error)
	}

	if res.RowsAffected == 0 {
		if len(conditions) == 0 {
			return customerrors.NewNotFoundErr("svm not found", nil)
		}
		var currentState string
		if ferr := tx.Model(&datamodel.Svm{}).Select("state").Where("id = ?", svm.ID).Row().Scan(&currentState); ferr != nil {
			if errors.Is(ferr, gorm.ErrRecordNotFound) || errors.Is(ferr, sql.ErrNoRows) {
				return customerrors.NewNotFoundErr("svm not found", nil)
			}
			return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, ferr)
		}
		return customerrors.NewConflictErr(
			fmt.Sprintf("SVM state transition to %s rejected: current state is %s", state, currentState))
	}

	return nil
}

func (d *DataStoreRepository) UpdateSvmWithKmsConfigIDs(ctx context.Context, svm *datamodel.Svm, gcpKmsConfigUUID, externalKmsConfigUUID string) (*datamodel.Svm, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}

	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	kmsConfig, err := d.GetKmsConfig(ctx, gcpKmsConfigUUID)
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	svm.KmsConfigID = sql.NullInt64{Int64: kmsConfig.ID, Valid: true}
	svm.KmsConfig = kmsConfig
	svm.UpdatedAt = time.Now()
	svm.SvmDetails.ExternalKmsConfigUUID = externalKmsConfigUUID

	err = tx.Save(svm).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return svm, nil
}

func (d *DataStoreRepository) UpdateSvmActiveDirectoryID(ctx context.Context, svm *datamodel.Svm, activeDirectoryID int64) (*datamodel.Svm, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}

	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	svm.ActiveDirectoryID = sql.NullInt64{Int64: activeDirectoryID, Valid: true}
	svm.UpdatedAt = time.Now()

	err = tx.Save(svm).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return svm, nil
}

func (d *DataStoreRepository) UnsetSvmActiveDirectoryID(ctx context.Context, svm *datamodel.Svm) (*datamodel.Svm, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}

	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	svm.ActiveDirectoryID = sql.NullInt64{Valid: false}
	svm.UpdatedAt = time.Now()

	err = tx.Save(svm).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return svm, nil
}

// UpdateSvmCurrentKmsKeyID updates the current KMS key ID in SvmDetails
// This tracks which service account key the SVM is currently using during rotation
func (d *DataStoreRepository) UpdateSvmCurrentKmsKeyID(ctx context.Context, svmUUID string, keyID string) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}

	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	svm := &datamodel.Svm{}
	err = tx.Where("uuid = ?", svmUUID).First(svm).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	// Initialize SvmDetails if nil
	if svm.SvmDetails == nil {
		svm.SvmDetails = &datamodel.SvmDetails{}
	}

	svm.SvmDetails.CurrentKmsKeyID = keyID
	svm.UpdatedAt = time.Now()

	err = tx.Save(svm).Error
	if err != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}

	return nil
}

func (d *DataStoreRepository) ListSvmsWithAccountId(ctx context.Context, accountId int64) ([]*datamodel.Svm, error) {
	return listSvmsWithAccountId(d.db.GORM().WithContext(ctx), accountId)
}

// ListSvmsForHardwareClusters returns SVMs belonging to EXTREME or PREMIUM service-level pools with related
// account, cluster, vendor subnet, and data LIF ip range metadata.
func (d *DataStoreRepository) ListSvmsForHardwareClusters(ctx context.Context) ([]*datamodel.SvmView, error) {
	return listHardwareClusterSVMs(d.db.GORM().WithContext(ctx))
}

func _listHardwareClusterSVMs(db *gorm.DB) ([]*datamodel.SvmView, error) {
	var svms []*datamodel.Svm
	err := db.Model(&datamodel.Svm{}).
		Joins("JOIN pools ON pools.id = svms.pool_id AND pools.deleted_at IS NULL AND pools.service_level IN ?", []string{serviceLevelExtreme, serviceLevelPremium}).
		Preload("Account").
		Preload("Pool").
		Order("svms.id ASC").
		Find(&svms).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if len(svms) == 0 {
		return []*datamodel.SvmView{}, nil
	}

	svmIDs := make([]int64, len(svms))
	for i, svm := range svms {
		svmIDs[i] = svm.ID
	}

	var lifResults []lifIPRangeResult
	err = db.Model(&datamodel.Lif{}).
		Select("lifs.svm_id, lifs.ip_address, ip_ranges.uuid AS ip_range_uuid").
		Joins("LEFT JOIN ip_ranges ON ip_ranges.id = lifs.ip_range_id").
		Where("lifs.svm_id IN ? AND lifs.type = ?", svmIDs, LifTypeDataLIF).
		Order("lifs.svm_id ASC, lifs.id ASC").
		Scan(&lifResults).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	lifDataBySvmID := make(map[int64]*lifIPRangeResult, len(lifResults))
	for i := range lifResults {
		if _, exists := lifDataBySvmID[lifResults[i].SvmID]; !exists {
			lifDataBySvmID[lifResults[i].SvmID] = &lifResults[i]
		}
	}

	svmViews := make([]*datamodel.SvmView, 0, len(svms))
	for _, svm := range svms {
		svmView := &datamodel.SvmView{Svm: *svm}
		if svm.Pool != nil {
			svmView.ClusterUUID = svm.Pool.HostUUID
			svmView.VendorSubnetID = svm.Pool.Network
		}
		if svm.Account != nil {
			svmView.AccountUUID = svm.Account.UUID
			svmView.AccountName = svm.Account.Name
		}
		if lifData, ok := lifDataBySvmID[svm.ID]; ok {
			svmView.IPRangeUUID = lifData.IPRangeUUID
		}
		svmViews = append(svmViews, svmView)
	}

	return svmViews, nil
}

func _listSvmsWithAccountId(db *gorm.DB, accountId int64) ([]*datamodel.Svm, error) {
	var svms []*datamodel.Svm
	err := db.Where("account_id = ?", accountId).Find(&svms).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return svms, nil
}

// GetSvmByNameAndPoolID retrieves an SVM by name and pool ID
func (d *DataStoreRepository) GetSvmByNameAndPoolID(ctx context.Context, name string, poolID int64) (*datamodel.Svm, error) {
	return getSvmWithDetails(d.db.GORM().WithContext(ctx), &datamodel.Svm{Name: name, PoolID: poolID})
}

// GetSvmByExternalUUID retrieves an SVM by external UUID from svm_details JSONB field and validates pool ownership
func (d *DataStoreRepository) GetSvmByExternalUUID(ctx context.Context, externalUUID string, poolID int64) (*datamodel.Svm, error) {
	db := d.db.GORM().WithContext(ctx)
	svm := &datamodel.Svm{}
	err := db.Where("pool_id = ? AND svm_details ->> 'external_uuid' = ?", poolID, externalUUID).
		First(&svm).Error
	if err != nil {
		return nil, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "svm", nil)
	}
	return svm, nil
}

// GetSvmByExternalIdentifier retrieves an SVM by its top-level external identifier (e.g. SVM OCID),
// scoped to the given account for tenant isolation. Returns the row regardless of lifecycle state
func (d *DataStoreRepository) GetSvmByExternalIdentifier(ctx context.Context, externalIdentifier string, accountID int64) (*datamodel.Svm, error) {
	db := d.db.GORM().WithContext(ctx)
	svm := &datamodel.Svm{}
	err := db.Unscoped().Where("account_id = ? AND svm_external_identifier = ?", accountID, externalIdentifier).
		First(&svm).Error
	if err != nil {
		return nil, customerrors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "svm", nil)
	}
	return svm, nil
}

// SvmExistsByExternalIdentifier reports whether any SVM with the given external identifier exists for the account.
func (d *DataStoreRepository) SvmExistsByExternalIdentifier(ctx context.Context, externalIdentifier string, accountID int64) (bool, error) {
	if externalIdentifier == "" || accountID == 0 {
		return false, nil
	}
	var count int64
	err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Svm{}).
		Unscoped().
		Where("account_id = ? AND svm_external_identifier = ?", accountID, externalIdentifier).
		Limit(1).
		Count(&count).Error
	if err != nil {
		return false, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return count > 0, nil
}
