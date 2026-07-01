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
	"gorm.io/gorm"
)

// ListAccountHostsByAccount returns all active account-host rows for an account.
func (d *DataStoreRepository) ListAccountHostsByAccount(ctx context.Context, accountID int64) ([]*datamodel.AccountHost, error) {
	var rows []*datamodel.AccountHost
	err := d.db.GORM().WithContext(ctx).
		Where("account_id = ? AND deleted_at IS NULL", accountID).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if rows == nil {
		return []*datamodel.AccountHost{}, nil
	}
	return rows, nil
}

// ListPinnedAccountHostsByAccount returns pinned account-host rows for an account.
func (d *DataStoreRepository) ListPinnedAccountHostsByAccount(ctx context.Context, accountID int64) ([]*datamodel.AccountHost, error) {
	var rows []*datamodel.AccountHost
	err := d.db.GORM().WithContext(ctx).
		Where("account_id = ? AND pinned = ? AND deleted_at IS NULL", accountID, true).
		Order("created_at ASC").
		Find(&rows).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if rows == nil {
		return []*datamodel.AccountHost{}, nil
	}
	return rows, nil
}

// GetAccountHost retrieves a single account-host row by UUID.
func (d *DataStoreRepository) GetAccountHost(ctx context.Context, accountHostUUID string) (*datamodel.AccountHost, error) {
	var row datamodel.AccountHost
	err := d.db.GORM().WithContext(ctx).
		Where("uuid = ? AND deleted_at IS NULL", accountHostUUID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				customerrors.NewNotFoundErr("AccountHost", &accountHostUUID))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &row, nil
}

// GetAccountHostByAccountAndHost returns the active row for an account/host pair.
func (d *DataStoreRepository) GetAccountHostByAccountAndHost(ctx context.Context, accountID int64, hostUUID string) (*datamodel.AccountHost, error) {
	hostUUID = strings.TrimSpace(hostUUID)
	var row datamodel.AccountHost
	err := d.db.GORM().WithContext(ctx).
		Where("account_id = ? AND host_id = ? AND deleted_at IS NULL", accountID, hostUUID).
		First(&row).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			key := fmt.Sprintf("%d/%s", accountID, hostUUID)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				customerrors.NewNotFoundErr("AccountHost", &key))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &row, nil
}

// GetAccountHostsByAccountAndHostUUIDs returns active rows for the given host UUIDs.
func (d *DataStoreRepository) GetAccountHostsByAccountAndHostUUIDs(ctx context.Context, accountID int64, hostUUIDs []string) ([]*datamodel.AccountHost, error) {
	if len(hostUUIDs) == 0 {
		return []*datamodel.AccountHost{}, nil
	}
	var rows []*datamodel.AccountHost
	err := d.db.GORM().WithContext(ctx).
		Where("account_id = ? AND host_id IN ? AND deleted_at IS NULL", accountID, hostUUIDs).
		Find(&rows).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	if rows == nil {
		return []*datamodel.AccountHost{}, nil
	}
	return rows, nil
}

// CreateAccountHost inserts a new account-host row.
func (d *DataStoreRepository) CreateAccountHost(ctx context.Context, row *datamodel.AccountHost) (*datamodel.AccountHost, error) {
	if row == nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError,
			fmt.Errorf("account host is required"))
	}
	if row.AccountID == 0 {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError,
			fmt.Errorf("account_id is required"))
	}
	if strings.TrimSpace(row.HostID) == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError,
			fmt.Errorf("host_id is required"))
	}

	db := d.db.GORM().WithContext(ctx)
	tx, err := startTransaction(db)
	if err != nil {
		return nil, err
	}
	logger := util.GetLogger(ctx)
	defer commitOrRollbackOnError(logger, tx, &err)

	var count int64
	if err = tx.Model(&datamodel.AccountHost{}).
		Where("account_id = ? AND host_id = ? AND deleted_at IS NULL", row.AccountID, row.HostID).
		Count(&count).Error; err != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
		return nil, err
	}
	if count > 0 {
		err = vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
			fmt.Errorf("account host mapping for account %d and host %q already exists", row.AccountID, row.HostID))
		return nil, err
	}

	if row.UUID == "" {
		row.UUID = utils.RandomUUID()
	}
	now := time.Now().UTC()
	row.CreatedAt = now
	row.UpdatedAt = now

	if err = tx.Create(row).Error; err != nil {
		err = vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
		return nil, err
	}
	return row, nil
}

// SetAccountHostPinned updates only the pinned flag for an account-host row.
func (d *DataStoreRepository) SetAccountHostPinned(ctx context.Context, accountHostUUID string, pinned bool) error {
	result := d.db.GORM().WithContext(ctx).
		Model(&datamodel.AccountHost{}).
		Where("uuid = ? AND deleted_at IS NULL", accountHostUUID).
		Updates(map[string]interface{}{
			"pinned":     pinned,
			"updated_at": time.Now().UTC(),
		})
	if result.Error != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}
	if result.RowsAffected == 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
			customerrors.NewNotFoundErr("AccountHost", &accountHostUUID))
	}
	return nil
}

// UpdateAccountHostServiceCredentials persists service user credentials for an account-host row.
func (d *DataStoreRepository) UpdateAccountHostServiceCredentials(
	ctx context.Context,
	accountHostUUID string,
	cred *datamodel.ExpertModeCredential,
) error {
	result := d.db.GORM().WithContext(ctx).
		Model(&datamodel.AccountHost{}).
		Where("uuid = ? AND deleted_at IS NULL", accountHostUUID).
		Updates(map[string]interface{}{
			"service_credentials": cred,
			"updated_at":          time.Now().UTC(),
		})
	if result.Error != nil {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, result.Error)
	}
	if result.RowsAffected == 0 {
		return vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
			customerrors.NewNotFoundErr("AccountHost", &accountHostUUID))
	}
	return nil
}

// GetActivePinAccountHostsJobByAccount returns a non-terminal pin-account-hosts job for the account, if any.
func (d *DataStoreRepository) GetActivePinAccountHostsJobByAccount(ctx context.Context, accountID int64) (*datamodel.Job, error) {
	var job datamodel.Job
	err := d.db.GORM().WithContext(ctx).Where(
		"account_id = ? AND type = ? AND state IN ?",
		accountID,
		datamodel.JobTypePinAccountHosts,
		[]string{
			string(datamodel.JobsStateNEW),
			string(datamodel.JobsStatePROCESSING),
		},
	).Order("created_at DESC").First(&job).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("job", nil))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &job, nil
}
