package opsinventory

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp/readstore"
)

var activeDirectoryRow = datamodel.ActiveDirectory{
	BaseModel: datamodel.BaseModel{ID: 91, UUID: "ad-1"},
	AdName:    "ad1", AccountId: 1, State: "IN_USE", Username: "admin", Domain: "corp.example", DNS: "10.0.0.2", NetBIOS: "CORP",
	CredentialPath: sentinel,
	ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
		Site: "site-a", OrganizationalUnit: "OU=x", KdcIP: "10.0.0.3", PrimaryAD: true, LdapSigning: true, SvmIds: []string{"svm-1"},
		ServerRootCaCertificate: sentinel, AdUsers: map[string][]string{"backup": {sentinel}},
	},
}

func TestNewActiveDirectoryReader_Spec(t *testing.T) {
	spec := NewActiveDirectoryReader(nil).Spec()
	assert.Equal(t, ResourceTypeActiveDirectory, spec.Type)
	assert.True(t, spec.GetByName)
	assert.True(t, spec.NameFilter)
	assert.Empty(t, spec.ListParents)
}

func TestActiveDirectoryReaderGet(t *testing.T) {
	ctx := context.Background()
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "1111"}

	t.Run("WhenByUUIDItLooksUpTheProject", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetActiveDirectoryByUUID(mock.Anything, "ad-1").Return(&activeDirectoryRow, nil)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{1}).Return(map[int64]*datamodel.Account{1: account}, nil)
		res, err := NewActiveDirectoryReader(store).Get(ctx, GetRequest{UUID: "ad-1"})
		require.NoError(t, err)
		assert.Equal(t, "1111", encoded(t, res.Value)["projectNumber"])
	})

	t.Run("WhenByNameItReusesTheRequestProject", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		scope := []readstore.Eq{{Column: readstore.ColAccountID, Value: int64(1)}}
		store.EXPECT().GetActiveDirectoryByName(mock.Anything, readstore.NameQuery{Name: "ad1", Eq: scope}).Return(&activeDirectoryRow, nil)
		res, err := NewActiveDirectoryReader(store).Get(ctx, GetRequest{Name: "ad1", NameScope: scope, Account: account})
		require.NoError(t, err)
		assert.Equal(t, "1111", encoded(t, res.Value)["projectNumber"])
	})

	t.Run("WhenProjectIsMissing", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetActiveDirectoryByUUID(mock.Anything, "ad-1").Return(&activeDirectoryRow, nil)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{1}).Return(map[int64]*datamodel.Account{}, nil)
		res, err := NewActiveDirectoryReader(store).Get(ctx, GetRequest{UUID: "ad-1"})
		require.NoError(t, err)
		assert.NotContains(t, encoded(t, res.Value), "projectNumber")
	})

	t.Run("WhenProjectLookupFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		boom := errors.New("db down")
		store.EXPECT().GetActiveDirectoryByUUID(mock.Anything, "ad-1").Return(&activeDirectoryRow, nil)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{1}).Return(nil, boom)
		_, err := NewActiveDirectoryReader(store).Get(ctx, GetRequest{UUID: "ad-1"})
		assert.ErrorIs(t, err, boom)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().GetActiveDirectoryByUUID(mock.Anything, "x").Return(nil, readstore.ErrNotFound)
		_, err := NewActiveDirectoryReader(store).Get(ctx, GetRequest{UUID: "x"})
		assert.ErrorIs(t, err, readstore.ErrNotFound)
	})
}

func TestActiveDirectoryReaderList(t *testing.T) {
	ctx := context.Background()
	opts := readstore.ListOptions{Limit: 3}
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "1111"}

	t.Run("WhenRowsShareTheRequestProjectNoAccountQuery", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListActiveDirectories(mock.Anything, opts).Return([]datamodel.ActiveDirectory{activeDirectoryRow, activeDirectoryRow}, nil)
		rows, err := NewActiveDirectoryReader(store).List(ctx, ListRequest{Account: account, Options: opts})
		require.NoError(t, err)
		assert.Len(t, rows, 2)
	})

	t.Run("WhenRowsHaveSeveralProjectsOneAccountQuery", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		other := activeDirectoryRow
		other.AccountId = 2
		store.EXPECT().ListActiveDirectories(mock.Anything, opts).Return([]datamodel.ActiveDirectory{activeDirectoryRow, other, other}, nil)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{1, 2}).
			Return(map[int64]*datamodel.Account{1: {Name: "1111"}, 2: {Name: "2222"}}, nil).Once()
		rows, err := NewActiveDirectoryReader(store).List(ctx, ListRequest{Options: opts})
		require.NoError(t, err)
		require.Len(t, rows, 3)
		assert.Equal(t, "2222", encoded(t, rows[2].Value)["projectNumber"])
	})

	t.Run("WhenProjectLookupFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		boom := errors.New("db down")
		store.EXPECT().ListActiveDirectories(mock.Anything, opts).Return([]datamodel.ActiveDirectory{activeDirectoryRow}, nil)
		store.EXPECT().GetAccountsByIDs(mock.Anything, []int64{1}).Return(nil, boom)
		_, err := NewActiveDirectoryReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, boom)
	})

	t.Run("WhenStoreFails", func(t *testing.T) {
		store := readstore.NewMockReadStore(t)
		store.EXPECT().ListActiveDirectories(mock.Anything, opts).Return(nil, readstore.ErrInvalidQuery)
		_, err := NewActiveDirectoryReader(store).List(ctx, ListRequest{Options: opts})
		assert.ErrorIs(t, err, readstore.ErrInvalidQuery)
	})
}

func TestActiveDirectoryResource(t *testing.T) {
	res := activeDirectoryResource(&activeDirectoryRow, map[int64]*datamodel.Account{1: {Name: "1111"}})
	got := encoded(t, res.Value)
	assert.Equal(t, "activeDirectory", got["resourceType"])
	assert.Equal(t, "ad1", got["name"])
	assert.Equal(t, "admin", got["username"])
	assert.Equal(t, "site-a", got["site"])
	assert.Equal(t, true, got["primaryAd"])
	assert.Equal(t, []any{"svm-1"}, got["svmIds"])
	assert.Len(t, got["serverRootCaCertificateSha256"], 64)
	for _, key := range []string{"password", "credentialPath", "serverRootCaCertificate", "adUsers"} {
		assert.NotContains(t, got, key)
	}
	assertNoSecrets(t, res.Value)
}

func TestCertificateFingerprint(t *testing.T) {
	t.Run("WhenEmpty", func(t *testing.T) {
		assert.False(t, certificateFingerprint("").IsSet())
	})

	t.Run("WhenPEM", func(t *testing.T) {
		pem := "-----BEGIN CERTIFICATE-----\nAAEC\n-----END CERTIFICATE-----\n"
		// SHA-256 of the DER bytes 0x00 0x01 0x02.
		assert.Equal(t, "ae4b3280e56e2faf83f414a6e3dabe9d5fbe18976544c05fed121accb85b53fc", certificateFingerprint(pem).Or(""))
	})

	t.Run("WhenNotPEM", func(t *testing.T) {
		assert.Len(t, certificateFingerprint("not pem").Or(""), 64)
	})
}
