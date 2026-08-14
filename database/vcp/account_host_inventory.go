package database

import (
	"context"
	"fmt"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
)

// HostCredential is the resolved connection identity for a pinned account host.
// It carries the joined host row plus ONTAP service-user credential metadata
// consumed by the dedicated-capacity ONTAP proxy / private CLI flow.
type HostCredential struct {
	Host          *datamodel.Host
	Username      string
	SecretId      string
	CertificateID string
	Password      string
	AuthType      int
	CaURI         string
	AccountUUID   string
}

// ManagementIP returns the management IP address to reach the host, falling back
// to the host name when no explicit IP address is recorded.
func (hc *HostCredential) ManagementIP() string {
	if hc == nil {
		return ""
	}
	return hostManagementIP(hc.Host)
}

// ListPinnedHostCredentials returns credentials for project inventory only.
// It reads account_hosts (pinned rows with provisioned service_credentials) and
// hosts for connection metadata. Username comes from account_hosts.service_credentials.
func (d *DataStoreRepository) ListPinnedHostCredentials(ctx context.Context, accountName string) ([]*HostCredential, error) {
	return d.listPinnedHostCredentials(ctx, accountName)
}

// GetDedicatedPinnedHostCredential resolves credentials for the dedicated-capacity
// ONTAP proxy / private CLI flow for a single cluster. It reads pinned account_hosts
// with provisioned service credentials and the joined hosts row matched by external
// cluster name. Username comes from account_hosts.service_credentials (not hosts.username or admin).
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
		if HostExternalName(hc.Host) == clusterExternalName {
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
		if !ah.HasDedicatedProxyCredentials() {
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
		if !ah.HasDedicatedProxyCredentials() {
			continue
		}
		host, ok := hostByUUID[strings.TrimSpace(ah.HostID)]
		if !ok || host == nil {
			continue
		}

		hc, err := hostCredentialFromAccountHost(host, ah, account.UUID)
		if err != nil {
			return nil, err
		}
		creds = append(creds, hc)
	}

	return creds, nil
}

func hostCredentialFromAccountHost(host *datamodel.Host, ah *datamodel.AccountHost, accountUUID string) (*HostCredential, error) {
	cred := ah.OntapModeCredentials
	password := cred.Password
	if cred.AuthType == env.USERNAME_PWD && strings.TrimSpace(password) != "" {
		decrypted, err := utils.DecryptServiceCredentialPassword(password)
		if err != nil {
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrDecryptingServiceAccountPassword,
				fmt.Errorf("decrypt account host service password: %w", err))
		}
		password = decrypted
	}
	hc := &HostCredential{
		Host:          host,
		Username:      strings.TrimSpace(ah.GetServiceUsername()),
		SecretId:      strings.TrimSpace(cred.SecretID),
		CertificateID: strings.TrimSpace(cred.CertificateID),
		Password:      password,
		AuthType:      cred.AuthType,
		AccountUUID:   accountUUID,
	}
	if cred.AuthType == env.USER_CERTIFICATE {
		hc.CaURI = hostCaURIFromHost(host)
	}
	return hc, nil
}

func hostCaURIFromHost(host *datamodel.Host) string {
	if host == nil || host.HostCredentials == nil {
		return ""
	}
	return host.HostCredentials.GetCaURIWithFallback()
}

// HostExternalName returns the cluster-facing host name (ExternalName, else Name).
func HostExternalName(host *datamodel.Host) string {
	if host == nil {
		return ""
	}
	if name := strings.TrimSpace(host.ExternalName); name != "" {
		return name
	}
	return strings.TrimSpace(host.Name)
}

func hostManagementIP(host *datamodel.Host) string {
	if host == nil {
		return ""
	}
	if ip := strings.TrimSpace(host.IPAddress); ip != "" {
		return ip
	}
	return strings.TrimSpace(host.Name)
}
