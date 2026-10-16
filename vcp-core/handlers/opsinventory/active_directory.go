package opsinventory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
	oasgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/vcp-core/servergen"
)

var activeDirectoryStates = stateSet(lifecycleStates, resourceEventStates)

type activeDirectoryReader struct {
	store readstore.ReadStore
}

// NewActiveDirectoryReader returns the Active Directory reader. It reads the
// VCP DB only; it never calls the factory GetActiveDirectory, which reads SDE
// when CVP is enabled.
func NewActiveDirectoryReader(store readstore.ReadStore) Reader {
	return &activeDirectoryReader{store: store}
}

func (r *activeDirectoryReader) Spec() Spec {
	return Spec{
		Type:          ResourceTypeActiveDirectory,
		AccountColumn: readstore.ColAccountID,
		GetByName:     true,
		NameFilter:    true,
		StateColumn:   readstore.ColState,
		States:        activeDirectoryStates,
		TimeFields:    baseTimeFields,
	}
}

func (r *activeDirectoryReader) Get(ctx context.Context, req GetRequest) (Resource, error) {
	var (
		ad  *datamodel.ActiveDirectory
		err error
	)
	if req.UUID != "" {
		ad, err = r.store.GetActiveDirectoryByUUID(ctx, req.UUID)
	} else {
		ad, err = r.store.GetActiveDirectoryByName(ctx, readstore.NameQuery{Name: req.Name, Eq: req.NameScope})
	}
	if err != nil {
		return Resource{}, err
	}
	accounts, err := loadAccounts(ctx, r.store, req.Account, []int64{ad.AccountId})
	if err != nil {
		return Resource{}, err
	}
	return activeDirectoryResource(ad, accounts), nil
}

func (r *activeDirectoryReader) List(ctx context.Context, req ListRequest) ([]Resource, error) {
	rows, err := r.store.ListActiveDirectories(ctx, req.Options)
	if err != nil {
		return nil, err
	}
	// The AD model has no Account association, so projects are loaded by id,
	// in one query for the whole page.
	ids := make([]int64, 0, len(rows))
	for _, ad := range rows {
		ids = append(ids, ad.AccountId)
	}
	accounts, err := loadAccounts(ctx, r.store, req.Account, ids)
	if err != nil {
		return nil, err
	}
	out := make([]Resource, 0, len(rows))
	for i := range rows {
		out = append(out, activeDirectoryResource(&rows[i], accounts))
	}
	return out, nil
}

// activeDirectoryResource never maps the password or the credential path.
// The server root CA is returned as a SHA-256 fingerprint only.
func activeDirectoryResource(ad *datamodel.ActiveDirectory, accounts map[int64]*datamodel.Account) Resource {
	details, withheld := stateDetails(ad.StateDetails)
	dto := oasgenserver.OpsActiveDirectoryV1{
		ResourceType:         string(ResourceTypeActiveDirectory),
		Source:               oasgenserver.OpsSourceV1VcpDb,
		UUID:                 ad.UUID,
		Name:                 optStr(ad.AdName),
		State:                optStr(ad.State),
		StateDetails:         details,
		StateDetailsWithheld: withheld,
		ProjectNumber:        accountName(accounts[ad.AccountId]),
		Username:             optStr(ad.Username),
		Domain:               optStr(ad.Domain),
		DNS:                  optStr(ad.DNS),
		NetBios:              optStr(ad.NetBIOS),
		ChangeId:             optStr(ad.ChangeId),
		CreatedAt:            ad.CreatedAt.UTC(),
		UpdatedAt:            ad.UpdatedAt.UTC(),
		DeletedAt:            deletedAt(ad.DeletedAt),
	}
	if a := ad.ActiveDirectoryAttributes; a != nil {
		dto.Description = optStr(a.Description)
		dto.OrganizationalUnit = optStr(a.OrganizationalUnit)
		dto.Site = optStr(a.Site)
		dto.KdcIp = optStr(a.KdcIP)
		dto.KdcHostname = optStr(a.KdcHostname)
		dto.UserDn = optStr(a.UserDN)
		dto.GroupDn = optStr(a.GroupDN)
		dto.GroupMembershipFilter = optStr(a.GroupMembershipFilter)
		dto.PreferredServersForLdapClient = optStr(a.PreferredServersForLdapClient)
		dto.PrimaryAd = oasgenserver.NewOptBool(a.PrimaryAD)
		dto.ManagedAd = oasgenserver.NewOptBool(a.ManagedAD)
		dto.AesEncryption = oasgenserver.NewOptBool(a.AesEncryption)
		dto.EncryptDcConnections = oasgenserver.NewOptBool(a.EncryptDCConnections)
		dto.LdapSigning = oasgenserver.NewOptBool(a.LdapSigning)
		dto.LdapOverTls = oasgenserver.NewOptBool(a.LdapOverTLS)
		dto.AllowLocalNfsUsersWithLdap = oasgenserver.NewOptBool(a.AllowLocalNFSUsersWithLdap)
		dto.SvmIds = a.SvmIds
		dto.ServerRootCaCertificateSha256 = certificateFingerprint(a.ServerRootCaCertificate)
	}
	return Resource{
		ID:             ad.ID,
		OwnerAccountID: ownerID(ad.AccountId),
		Value:          oasgenserver.NewOpsActiveDirectoryV1OpsResourceV1(dto),
	}
}

// certificateFingerprint returns the SHA-256 of the first PEM block's DER
// bytes, or of the raw value when it is not PEM.
func certificateFingerprint(cert string) oasgenserver.OptString {
	if cert == "" {
		return oasgenserver.OptString{}
	}
	data := []byte(cert)
	if block, _ := pem.Decode(data); block != nil {
		data = block.Bytes
	}
	sum := sha256.Sum256(data)
	return oasgenserver.NewOptString(hex.EncodeToString(sum[:]))
}
