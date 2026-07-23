package database

import (
	"context"
	"strconv"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	coreerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"gorm.io/gorm"
)

const (
	KmsSaStateDisabled      = "disable"
	KmsSaStateEnable        = "enable"
	GcpKmsConfigHealthError = "specified key <key_name> in <key_ring> does not exist or service permissions are incorrect"
	maxKmsConfigLimitReachedForAccountError = "Maximum CMEK policy limit reached for this account."

	// Postgres advisory lock class for concurrent first-time KMS service account create (key: account_id).
	gcpKmsSACreateLockClass = 0x4B4D // "KM"
)

var (
	getKmsConfig              = _getKmsConfig
	listKmsConfigByAccountID  = _listKmsConfigByAccountID
	getTimeNow                = utils.GetTimeNow
	updateKmsConfigAttributes = _updateKmsConfigAttributes
	updateKmsConfigDetails    = _updateKmsConfigDetails
	getKmsConfigByUUID        = _getKmsConfigByUUID
	isKmsConfigInUse          = _isKmsConfigInUse
)

func (d *DataStoreRepository) GetKmsConfig(ctx context.Context, kmsConfigUUID string) (*datamodel.KmsConfig, error) {
	db := d.db.GORM().WithContext(ctx)
	return getKmsConfig(db, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID}})
}

func (d *DataStoreRepository) UpdateKmsConfigState(ctx context.Context, kmsConfigUUID string, state string, stateDetails string) (*datamodel.KmsConfig, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	kmsConfig, err := _getKmsConfig(tx, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID}})
	if err != nil {
		return nil, err
	}

	kmsConfig.State = state
	kmsConfig.StateDetails = stateDetails
	err = tx.Updates(kmsConfig).Error
	if err != nil {
		return nil, err
	}

	return kmsConfig, nil
}

func (d *DataStoreRepository) UpdateKmsConfigStateForHandleResource(ctx context.Context, kmsConfigUUID string, stateDetails string, event string) (*datamodel.KmsConfig, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	kmsConfig, err := _getKmsConfig(tx, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID}})
	if err != nil {
		return nil, err
	}

	newState := ""
	newStateDetails := ""
	currState := kmsConfig.State

	if err = isStateReady(currState); err != nil {
		return nil, err
	}

	switch event {
	case datamodel.StateOff:
		newState = datamodel.LifeCycleStateDisabled
		newStateDetails = datamodel.LifeCycleStateDisabledDetails
	case datamodel.StateOn:
		if currState != datamodel.LifeCycleStateDisabled {
			err = errors.New("Cannot Enable gcpKmsConfig which is not in disabled state")
			return nil, err
		}
		// ON event will mark the state as "in use" if the gcpKmsConfig is being used by any svm, else it will be marked as "created".
		// gcpKmsConfig check endpoint should be invoked to move gcpKmsConfig from "created" to "ready" state.
		newState, err = func() (string, error) {
			inUse, err := isKmsConfigInUse(tx, kmsConfig)
			if err != nil {
				return "", err
			}
			if inUse {
				return datamodel.LifeCycleStateInUse, nil
			}
			return datamodel.LifeCycleStateCreated, nil
		}()
		if err != nil {
			return nil, err
		}

		// Set appropriate state details based on the final state
		switch newState {
		case datamodel.LifeCycleStateInUse:
			newStateDetails = datamodel.LifeCycleStateInUseDetails
		case datamodel.LifeCycleStateCreated:
			newStateDetails = datamodel.LifeCycleStateCreatedDetails
		default:
			err = errors.NewNotSupportedErrWithMessage("Invalid state")
			return nil, err
		}
	default:
		err = errors.NewNotSupportedErrWithMessage("Invalid event")
		return nil, err
	}

	kmsConfig.State = newState
	kmsConfig.StateDetails = newStateDetails
	err = tx.Updates(kmsConfig).Error
	if err != nil {
		return nil, err
	}

	return kmsConfig, nil
}

// GetMultipleKmsConfigs retrieves multiple KMS configurations based on the provided conditions
func (d *DataStoreRepository) GetMultipleKmsConfigs(ctx context.Context, conditions [][]interface{}) ([]*datamodel.KmsConfig, error) {
	return getMultipleKmsConfigs(d.db.ApplyFilter(conditions).GORM().WithContext(ctx))
}

// GetKmsConfigsByUUIDs retrieves KMS configs by UUID without preloading any relations. All currently
// supported batch KMS fields are served from the base KMS config row and its KmsAttributes JSON column,
// so no eager loading is needed.
func (d *DataStoreRepository) GetKmsConfigsByUUIDs(
	ctx context.Context,
	kmsConfigUUIDs []string,
) ([]*datamodel.KmsConfig, error) {
	var kmsConfigs []*datamodel.KmsConfig
	db := d.db.GORM().WithContext(ctx).Where("uuid in ?", kmsConfigUUIDs)
	if err := db.Find(&kmsConfigs).Error; err != nil {
		return nil, err
	}
	return kmsConfigs, nil
}

func getMultipleKmsConfigs(db *gorm.DB) ([]*datamodel.KmsConfig, error) {
	var kmsConfigs []*datamodel.KmsConfig
	err := db.Preload("ServiceAccount").Find(&kmsConfigs).Error
	if err != nil {
		return nil, err
	}
	return kmsConfigs, nil
}

// CreateKmsConfig creates the KMS configuration along with the service account.
// Multiple KMS configs are allowed per account as long as each one points to a distinct key path, when EnableMultipleCmek is true.
func (d *DataStoreRepository) CreateKmsConfig(ctx context.Context, kmsConfig *datamodel.KmsConfig) (*datamodel.KmsConfig, error) {
	db := d.db.GORM().WithContext(ctx)
	dbKmsConfigs, err := listKmsConfigByAccountID(db, kmsConfig.AccountID)
	if err != nil {
		return nil, err
	}
	for _, dbKmsConfig := range dbKmsConfigs {
		if utils.EnableMultipleCmek && !sameKeyPath(dbKmsConfig, kmsConfig) {
			continue
		}
		switch dbKmsConfig.State {
		case datamodel.LifeCycleStateCreating:
			return nil, errors.NewConflictErr("another config create operation is in progress for this region and project")
		case datamodel.LifeCycleStateDeleting:
			return nil, errors.NewConflictErr("another config delete operation is in progress for this region and project")
		case datamodel.LifeCycleStateAvailable:
			return dbKmsConfig, nil
		}
	}
	if utils.EnableMultipleCmek && len(dbKmsConfigs) >= utils.MaxNumberOfKmsPerAccount {
		return nil, errors.NewConflictErr(maxKmsConfigLimitReachedForAccountError)
	}
	kmsConfig.ServiceAccountID = nil
	err = db.Save(kmsConfig).Error
	if err != nil {
		return nil, err
	}

	err = db.Preload("ServiceAccount").Preload("Account").Where("uuid = ?", kmsConfig.UUID).First(kmsConfig).Error
	return kmsConfig, err
}

// sameKeyPath returns true when two KMS config rows reference the same
// (KeyProjectID, KeyRingLocation, KeyRing, KeyName) tuple.
func sameKeyPath(a, b *datamodel.KmsConfig) bool {
	return a.KeyProjectID == b.KeyProjectID &&
		a.KeyRingLocation == b.KeyRingLocation &&
		a.KeyRing == b.KeyRing &&
		a.KeyName == b.KeyName
}

func (d *DataStoreRepository) CreateKmsServiceAccount(ctx context.Context, serviceAccount *datamodel.ServiceAccount) (_ *datamodel.ServiceAccount, err error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// Serialize concurrent first-time creates so two callers can't both miss the
	// First() lookup and Create() duplicate rows. Released when the transaction ends.
	if err = tx.Exec("SELECT pg_advisory_xact_lock(?, hashtext(?))",
		gcpKmsSACreateLockClass, strconv.FormatInt(serviceAccount.AccountID, 10)).Error; err != nil {
		return nil, err
	}

	err = tx.First(serviceAccount, &datamodel.ServiceAccount{AccountID: serviceAccount.AccountID,
		State: KmsSaStateEnable}).Error
	if err != nil {
		if err.Error() != "record not found" {
			return nil, err
		}
		serviceAccount.UUID = utils.RandomUUID()
		serviceAccount.CreatedAt = getTimeNow()
		serviceAccount.UpdatedAt = getTimeNow()
		encKey, encErr := utils.EncryptPassword(log.Secret(serviceAccount.ServiceAccountPasswordLocation))
		if encErr != nil {
			err = encErr
			return nil, err
		}
		serviceAccount.ServiceAccountPasswordLocation = *encKey
		if err = tx.Create(serviceAccount).Error; err != nil {
			return nil, err
		}
		err = tx.First(serviceAccount).Error
		return serviceAccount, err
	}
	return serviceAccount, err
}

// ListKmsConfigByAccountID retrieves all KMS configurations for a given account ID
func (d *DataStoreRepository) ListKmsConfigByAccountID(ctx context.Context, accountID int64) ([]*datamodel.KmsConfig, error) {
	return listKmsConfigByAccountID(d.db.GORM().WithContext(ctx), accountID)
}

func _listKmsConfigByAccountID(db *gorm.DB, accountID int64) ([]*datamodel.KmsConfig, error) {
	var kmsConfigs []*datamodel.KmsConfig
	err := db.Preload("ServiceAccount").Preload("Account").Where("account_id = ?", accountID).Find(&kmsConfigs).Error
	if err != nil {
		return nil, err
	}
	return kmsConfigs, nil
}

// GetKmsConfigByUUID retrieves a KMS configuration by its UUID
func (d *DataStoreRepository) GetKmsConfigByUUID(ctx context.Context, uuid string) (*datamodel.KmsConfig, error) {
	return getKmsConfigByUUID(d.db.GORM().WithContext(ctx), uuid)
}

func _getKmsConfigByUUID(db *gorm.DB, uuid string) (*datamodel.KmsConfig, error) {
	kmsConfig := &datamodel.KmsConfig{}
	err := db.Preload("ServiceAccount").Preload("Account").First(kmsConfig, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: uuid}}).Error
	if err != nil {
		if coreerrors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.NewNotFoundErr("KMS Configuration", nil)
		}
		return nil, err
	}
	return kmsConfig, nil
}

func _getKmsConfig(db *gorm.DB, query *datamodel.KmsConfig) (*datamodel.KmsConfig, error) {
	kmsConfig := &datamodel.KmsConfig{}
	err := db.Preload("Account").Preload("ServiceAccount").First(&kmsConfig, query).Error
	if err != nil {
		return nil, errors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "KMS Configuration", nil)
	}
	return kmsConfig, nil
}

// UpdateKmsConfigAttributes updates the attributes of a KMS configuration in the database
func (d *DataStoreRepository) UpdateKmsConfigAttributes(ctx context.Context, uuid string, attributes *datamodel.KmsAttributes) (*datamodel.KmsConfig, error) {
	var updatedKmsConfig *datamodel.KmsConfig
	err := d.db.GORM().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error

		updatedKmsConfig, err = updateKmsConfigAttributes(tx, uuid, attributes)
		return err
	})
	if err != nil {
		return nil, err
	}
	return updatedKmsConfig, nil
}

func _updateKmsConfigAttributes(db *gorm.DB, uuid string, attributes *datamodel.KmsAttributes) (*datamodel.KmsConfig, error) {
	dbKmsConfig := &datamodel.KmsConfig{}
	err := db.Preload("ServiceAccount").Preload("Account").Where("uuid = ?", uuid).First(dbKmsConfig).Error
	if err != nil {
		return nil, err
	}
	dbKmsConfig.UpdatedAt = time.Now()
	dbKmsConfig.KmsAttributes = attributes
	err = db.Where("uuid = ?", uuid).Updates(dbKmsConfig).Error
	if err != nil {
		return nil, err
	}
	return dbKmsConfig, nil
}

// UpdateKmsConfigDetails updates the KMS configuration details such as key full path and resource ID
func (d *DataStoreRepository) UpdateKmsConfigDetails(ctx context.Context, uuid string, keyFullPath string, resourceID string) (*datamodel.KmsConfig, error) {
	var updatedKmsConfig *datamodel.KmsConfig
	err := d.db.GORM().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error

		updatedKmsConfig, err = updateKmsConfigDetails(tx, uuid, keyFullPath, resourceID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return updatedKmsConfig, nil
}

func _updateKmsConfigDetails(db *gorm.DB, uuid string, keyFullPath string, resourceID string) (*datamodel.KmsConfig, error) {
	parsedKeyFullPath, err := utils.ParseKeyFullPathResource(keyFullPath)
	if err != nil {
		return nil, err
	}
	dbKmsConfig := &datamodel.KmsConfig{}
	err = db.Preload("ServiceAccount").Preload("Account").Where("uuid = ?", uuid).First(dbKmsConfig).Error
	if err != nil {
		return nil, err
	}
	dbKmsConfig.UpdatedAt = time.Now()
	dbKmsConfig.KeyRingLocation = parsedKeyFullPath.Location
	dbKmsConfig.KeyRing = parsedKeyFullPath.KeyRing
	dbKmsConfig.KeyName = parsedKeyFullPath.CryptoKey
	dbKmsConfig.KeyProjectID = parsedKeyFullPath.ProjectID
	dbKmsConfig.KmsGrant = parsedKeyFullPath.Grant
	dbKmsConfig.ResourceID = resourceID
	err = db.Where("uuid = ?", uuid).Updates(dbKmsConfig).Error
	if err != nil {
		return nil, err
	}
	return dbKmsConfig, nil
}

// GetKmsConfigByKeyFullPath retrieves a KMS configuration by its full key path
func (d *DataStoreRepository) GetKmsConfigByKeyFullPath(ctx context.Context, keyFullPath string, accountID int64) (*datamodel.KmsConfig, error) {
	parsedKeyFullPath, err := utils.ParseKeyFullPathResource(keyFullPath)
	if err != nil {
		return nil, err
	}
	kmsConfig := &datamodel.KmsConfig{}
	err = d.db.GORM().WithContext(ctx).Preload("ServiceAccount").Preload("Account").First(kmsConfig, &datamodel.KmsConfig{
		KeyRingLocation: parsedKeyFullPath.Location,
		KeyRing:         parsedKeyFullPath.KeyRing,
		KeyName:         parsedKeyFullPath.CryptoKey,
		KeyProjectID:    parsedKeyFullPath.ProjectID,
		AccountID:       accountID,
	}).Error
	if err != nil {
		return nil, errors.ConvertToNotFoundErrIfContainsMessage(err, "record not found", "KMS Configuration", nil)
	}
	return kmsConfig, nil
}

// DeleteKmsConfig deletes kms config based on UUID
func (d *DataStoreRepository) DeleteKmsConfig(ctx context.Context, kmsConfigUUID, state, stateDetails string) (*datamodel.KmsConfig, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	defer commitOrRollbackOnError(util.GetLogger(ctx), tx, &err)

	kmsConfig, err := getKmsConfig(tx, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: kmsConfigUUID}})
	if err != nil {
		return nil, err
	}
	kmsConfig.DeletedAt = &gorm.DeletedAt{Time: time.Now(), Valid: true}
	kmsConfig.State = state
	kmsConfig.StateDetails = stateDetails
	err = tx.Save(kmsConfig).Error
	if err != nil {
		return nil, err
	}

	return kmsConfig, nil
}

func (d *DataStoreRepository) UpdateKmsConfig(ctx context.Context, kmsUUID string, updates map[string]interface{}) error {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	// Verify the record exists first
	_, err = _getKmsConfig(tx, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: kmsUUID}})
	if err != nil {
		return err
	}

	// Add updated_at timestamp
	updates["updated_at"] = time.Now()

	// Update directly on the table to avoid conflicts with loaded model
	err = tx.Model(&datamodel.KmsConfig{}).Where("uuid = ?", kmsUUID).Updates(updates).Error
	if err != nil {
		return err
	}
	return nil
}

func (d *DataStoreRepository) IsKmsConfigInUse(ctx context.Context, kmsConfigUUID string) (bool, error) {
	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return false, err
	}
	kmsConfig, err := getKmsConfigByUUID(tx, kmsConfigUUID)
	if err != nil {
		return false, err
	}
	return isKmsConfigInUse(tx, kmsConfig)
}

func _isKmsConfigInUse(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
	pools, err := getPoolsByKmsConfigID(db, kmsConfig.ID)
	if err != nil && !errors.IsNotFoundErr(err) {
		return false, err
	}
	if len(pools) > 0 {
		return true, nil
	}
	return false, nil
}

func isStateReady(currState string) error {
	switch currState {
	case datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeleted:
		return errors.NewNotFoundErr("Config does not exist", nil)
	case datamodel.LifeCycleStateError, datamodel.LifeCycleStateCreating:
		return errors.NewUserInputValidationErr("Can not update a KmsConfig which is in creating or error state")
	case datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateMigrating:
		return errors.NewUserInputValidationErr("GCP KMS configuration is already transitioning between states")
	}
	return nil
}
