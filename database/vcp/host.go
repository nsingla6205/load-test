package database

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/gorm"
)

const hostIPAddressConflictMessage = "IP address already assigned to host"

// CreateHost creates a new Host entry in the database.
func (d *DataStoreRepository) CreateHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error) {
	if host.UUID == "" {
		host.UUID = utils.RandomUUID()
	}
	host.CreatedAt = time.Now()
	host.UpdatedAt = host.CreatedAt
	if host.Attributes == nil {
		host.Attributes = &datamodel.HostAttributes{}
	}
	if host.Attributes.PassphraseVersion == "" {
		host.Attributes.PassphraseVersion = datamodel.VCP_PASSPHRASE_V0
	}

	err := d.db.GORM().WithContext(ctx).Create(host).Error
	if err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				customerrors.NewConflictErr(hostIPAddressConflictMessage))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataInsertError, err)
	}
	return host, nil
}

// CountHostsWithIPAddress returns the number of hosts with the given management IP.
func (d *DataStoreRepository) CountHostsWithIPAddress(ctx context.Context, ipAddress string, includeDeleted bool) (int64, error) {
	db := d.db.GORM().WithContext(ctx)
	if includeDeleted {
		db = db.Unscoped()
	}

	var count int64
	err := db.Model(&datamodel.Host{}).Where("ip_address = ?", ipAddress).Count(&count).Error
	if err != nil {
		return 0, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}

	return count, nil
}

// ListHosts returns all active hosts.
func (d *DataStoreRepository) ListHosts(ctx context.Context) ([]*datamodel.Host, error) {
	var hosts []*datamodel.Host
	if err := d.db.GORM().WithContext(ctx).
		Model(&datamodel.Host{}).
		Where("deleted_at IS NULL").
		Find(&hosts).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return hosts, nil
}

// GetHostByName retrieves an active host by its registered name (hosts.name).
func (d *DataStoreRepository) GetHostByName(ctx context.Context, name string) (*datamodel.Host, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
			customerrors.NewBadRequestErr("host name is required"))
	}

	var host datamodel.Host
	err := d.db.GORM().WithContext(ctx).
		Where("name = ? AND deleted_at IS NULL", name).
		First(&host).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				customerrors.NewNotFoundErr("Host", &name))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &host, nil
}

// GetHost retrieves a single host by UUID.
func (d *DataStoreRepository) GetHost(ctx context.Context, hostUUID string) (*datamodel.Host, error) {
	var host datamodel.Host
	err := d.db.GORM().WithContext(ctx).
		Where("uuid = ? AND deleted_at IS NULL", hostUUID).
		First(&host).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError,
				customerrors.NewNotFoundErr("Host", &hostUUID))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &host, nil
}

// GetHostsByUUIDs fetches active hosts whose UUIDs are in the supplied list.
// Missing UUIDs are simply absent from the result; callers must compare lengths to detect partial misses.
func (d *DataStoreRepository) GetHostsByUUIDs(ctx context.Context, hostUUIDs []string) ([]*datamodel.Host, error) {
	if len(hostUUIDs) == 0 {
		return nil, nil
	}
	var hosts []*datamodel.Host
	if err := d.db.GORM().WithContext(ctx).
		Where("uuid IN ? AND deleted_at IS NULL", hostUUIDs).
		Find(&hosts).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return hosts, nil
}

// UpdateHost updates an existing host row.
func (d *DataStoreRepository) UpdateHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error) {
	if host == nil || host.UUID == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError,
			customerrors.NewBadRequestErr("host uuid is required"))
	}

	host.UpdatedAt = time.Now().UTC()
	if err := d.db.GORM().WithContext(ctx).Save(host).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "unique constraint") {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrResourceStateConflictError,
				customerrors.NewConflictErr(hostIPAddressConflictMessage))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, err)
	}
	return host, nil
}

// DeleteHost soft-deletes an existing host row.
func (d *DataStoreRepository) DeleteHost(ctx context.Context, host *datamodel.Host) (*datamodel.Host, error) {
	if host == nil || host.UUID == "" {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError,
			customerrors.NewBadRequestErr("host uuid is required"))
	}

	now := time.Now().UTC()
	host.State = datamodel.LifeCycleStateDeleted
	host.StateDetails = datamodel.LifeCycleStateDeletedDetails
	host.UpdatedAt = now
	deletedAt := gorm.DeletedAt{Time: now, Valid: true}
	host.DeletedAt = &deletedAt

	if err := d.db.GORM().WithContext(ctx).Save(host).Error; err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataDeleteError, err)
	}
	return host, nil
}
