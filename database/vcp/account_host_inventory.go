package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

// ListPinnedHostCredentials returns credentials for project inventory only.
// It reads account_hosts (pinned rows with provisioned service_credentials) and
// hosts for connection metadata. Username comes from account_hosts.service_credentials.
func (d *DataStoreRepository) ListPinnedHostCredentials(ctx context.Context, accountName string) ([]*HostCredential, error) {
	return d.listPinnedHostCredentials(ctx, accountName)
}

// GetDedicatedPinnedHostCredential resolves credentials for the dedicated-capacity
// ONTAP proxy / private CLI flow for a single cluster. It reads pinned account_hosts
// with a secret id and the joined hosts row matched by external cluster name.
// Username comes from account_hosts.service_credentials (not hosts.username or admin).
func (d *DataStoreRepository) GetDedicatedPinnedHostCredential(ctx context.Context, accountName, clusterExternalName string) (*HostCredential, error) {
	clusterExternalName = strings.TrimSpace(clusterExternalName)
	if clusterExternalName == "" {
		return nil, nil
	}

	creds, err := d.listPinnedHostCredentials(ctx, accountName)
	if err != nil {
		return nil, err
	}

	for _, hc := range creds {
		if hc == nil || hc.Host == nil {
			continue
		}
		if hostExternalName(hc.Host) == clusterExternalName {
			return hc, nil
		}
	}

	return nil, nil
}

func (d *DataStoreRepository) listPinnedHostCredentials(ctx context.Context, accountName string) ([]*HostCredential, error) {
	account, err := d.GetAccount(ctx, accountName)
	if err != nil {
		return nil, fmt.Errorf("list pinned host credentials get account=%s: %w", accountName, err)
	}

	var accountHosts []*datamodel.AccountHost
	err = d.db.GORM().WithContext(ctx).
		Where("account_id = ? AND pinned = ?", account.ID, true).
		Find(&accountHosts).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
			fmt.Errorf("list pinned host credentials account=%s: %w", accountName, err))
	}
	if len(accountHosts) == 0 {
		return nil, nil
	}

	hostIDs := make([]string, 0, len(accountHosts))
	for _, ah := range accountHosts {
		if !hasSecretManagerCredentials(ah) {
			continue
		}
		if id := strings.TrimSpace(ah.HostID); id != "" {
			hostIDs = append(hostIDs, id)
		}
	}
	if len(hostIDs) == 0 {
		return nil, nil
	}

	var hosts []*datamodel.Host
	err = d.db.GORM().WithContext(ctx).
		Where("uuid IN ? AND deleted_at IS NULL", hostIDs).
		Find(&hosts).Error
	if err != nil {
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataReadError,
			fmt.Errorf("list pinned host credentials fetch hosts account=%s: %w", accountName, err))
	}

	hostByUUID := make(map[string]*datamodel.Host, len(hosts))
	for _, h := range hosts {
		if h != nil {
			hostByUUID[h.UUID] = h
		}
	}

	creds := make([]*HostCredential, 0, len(accountHosts))
	for _, ah := range accountHosts {
		if !hasSecretManagerCredentials(ah) {
			continue
		}
		host, ok := hostByUUID[strings.TrimSpace(ah.HostID)]
		if !ok || host == nil {
			continue
		}

		creds = append(creds, &HostCredential{
			Host:        host,
			Username:    strings.TrimSpace(ah.GetServiceUsername()),
			SecretId:    strings.TrimSpace(ah.OntapModeCredentials.SecretID),
			AccountUUID: account.UUID,
		})
	}

	return creds, nil
}

// hasSecretManagerCredentials reports whether ah has provisioned credentials of the
// secret-manager type (USERNAME_PWD_SEC_MGR). This is the only credential type
// supported by the HostCredential model (which carries a SecretId field).
func hasSecretManagerCredentials(ah *datamodel.AccountHost) bool {
	return ah.HasProvisionedCredentials() && strings.TrimSpace(ah.OntapModeCredentials.SecretID) != ""
}
