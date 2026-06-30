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

// GetHostByUUID retrieves a host by its UUID.
func (d *DataStoreRepository) GetHostByUUID(ctx context.Context, uuid string) (*datamodel.Host, error) {
	var host datamodel.Host
	err := d.db.GORM().WithContext(ctx).
		First(&host, &datamodel.Host{BaseModel: datamodel.BaseModel{UUID: uuid}}).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataNotFoundError, customerrors.NewNotFoundErr("Host", &uuid))
		}
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError, err)
	}
	return &host, nil
}
