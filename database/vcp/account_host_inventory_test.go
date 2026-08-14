package database

import (
	"context"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupHostCredStore(t *testing.T) *DataStoreRepository {
	t.Helper()
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))
	return store
}

func seedAccount(t *testing.T, store *DataStoreRepository, uuid, name string) *datamodel.Account {
	t.Helper()
	acc := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: uuid}, Name: name}
	require.NoError(t, store.db.GORM().Create(acc).Error)
	return acc
}

func setupPinnedHostCredStore(t *testing.T) *DataStoreRepository {
	t.Helper()
	store := setupHostCredStore(t)
	require.NoError(t, store.db.GORM().AutoMigrate(&datamodel.AccountHost{}))
	return store
}

func seedHost(t *testing.T, store *DataStoreRepository, uuid, externalName, username, ip string) *datamodel.Host {
	t.Helper()
	host := &datamodel.Host{
		BaseModel:    datamodel.BaseModel{UUID: uuid},
		Name:         externalName,
		ExternalName: externalName,
		IPAddress:    ip,
		Username:     username,
	}
	require.NoError(t, store.db.GORM().Create(host).Error)
	return host
}

func seedAccountHost(t *testing.T, store *DataStoreRepository, uuid string, accountID int64, hostID string, pinned bool, cred *datamodel.ExpertModeCredential) {
	t.Helper()
	ah := &datamodel.AccountHost{
		BaseModel:            datamodel.BaseModel{UUID: uuid},
		AccountID:            accountID,
		HostID:               hostID,
		Pinned:               pinned,
		OntapModeCredentials: cred,
	}
	require.NoError(t, store.db.GORM().Create(ah).Error)
}

func pinnedSecretCred(secretID, username string) *datamodel.ExpertModeCredential {
	return &datamodel.ExpertModeCredential{
		AuthType: env.USERNAME_PWD_SEC_MGR,
		SecretID: secretID,
		Username: username,
	}
}

func pinnedCertCred(certificateID, secretID, username string) *datamodel.ExpertModeCredential {
	return &datamodel.ExpertModeCredential{
		AuthType:      env.USER_CERTIFICATE,
		CertificateID: certificateID,
		SecretID:      secretID,
		Username:      username,
	}
}

func seedHostWithCredentials(t *testing.T, store *DataStoreRepository, uuid, externalName, username, ip, caURI string) *datamodel.Host {
	t.Helper()
	host := &datamodel.Host{
		BaseModel:    datamodel.BaseModel{UUID: uuid},
		Name:         externalName,
		ExternalName: externalName,
		IPAddress:    ip,
		Username:     username,
	}
	if caURI != "" {
		host.HostCredentials = &datamodel.PoolCredentials{CaURI: caURI}
	}
	require.NoError(t, store.db.GORM().Create(host).Error)
	return host
}

func TestListPinnedHostCredentials(t *testing.T) {
	t.Run("WhenAccountHasNoPinnedHosts_ReturnsNil", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		seedAccount(tt, store, "acc-uuid-1", "proj-1")

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-1")

		require.NoError(tt, err)
		assert.Nil(tt, creds)
	})

	t.Run("WhenAccountNameDoesNotExist_ReturnsError", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)

		creds, err := store.ListPinnedHostCredentials(context.Background(), "nonexistent-project")

		assert.Nil(tt, creds)
		require.Error(tt, err)
	})

	t.Run("WhenPinnedHostHasSecretID_UsesServiceUsername", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-2", "proj-2")
		host := seedHost(tt, store, "host-uuid-1", "cluster-a", "host-user", "10.0.0.1")
		seedAccountHost(tt, store, "ah-uuid-1", acc.ID, host.UUID, true, pinnedSecretCred("secret-a", "cred-user"))

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-2")

		require.NoError(tt, err)
		require.Len(tt, creds, 1)
		assert.Equal(tt, "secret-a", creds[0].SecretId)
		assert.Equal(tt, "cred-user", creds[0].Username, "username should come from account_hosts service_credentials")
		require.NotNil(tt, creds[0].Host)
		assert.Equal(tt, "cluster-a", creds[0].Host.ExternalName)
		assert.Equal(tt, acc.UUID, creds[0].AccountUUID)
	})

	t.Run("WhenPinnedHostHasSecretID_UsesServiceUsername_IgnoresHostUsername", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-3", "proj-3")
		host := seedHost(tt, store, "host-uuid-2", "cluster-b", "host-user-b", "10.0.0.2")
		seedAccountHost(tt, store, "ah-uuid-2", acc.ID, host.UUID, true, pinnedSecretCred("secret-b", "cred-user-b"))

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-3")

		require.NoError(tt, err)
		require.Len(tt, creds, 1)
		assert.Equal(tt, "cred-user-b", creds[0].Username)
	})

	t.Run("WhenPinnedWithInlinePassword_ReturnsCredential", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-4", "proj-4")
		host := seedHost(tt, store, "host-uuid-3", "cluster-c", "host-user-c", "10.0.0.3")
		seedAccountHost(tt, store, "ah-uuid-3", acc.ID, host.UUID, true, &datamodel.ExpertModeCredential{
			AuthType: env.USERNAME_PWD,
			Username: "user-c",
			Password: "pass",
		})

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-4")

		require.NoError(tt, err)
		require.Len(tt, creds, 1)
		assert.Equal(tt, "user-c", creds[0].Username)
		assert.Equal(tt, "pass", creds[0].Password)
		assert.Equal(tt, env.USERNAME_PWD, creds[0].AuthType)
		assert.Empty(tt, creds[0].SecretId)
	})

	t.Run("WhenUnpinnedWithSecretID_SkipsRow", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-5", "proj-5")
		host := seedHost(tt, store, "host-uuid-4", "cluster-d", "host-user-d", "10.0.0.4")
		seedAccountHost(tt, store, "ah-uuid-4", acc.ID, host.UUID, false, pinnedSecretCred("secret-d", "cred-user-d"))

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-5")

		require.NoError(tt, err)
		assert.Nil(tt, creds)
	})

	t.Run("WhenHostMissing_SkipsOrphanedAccountHost", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-6", "proj-6")
		seedAccountHost(tt, store, "ah-uuid-5", acc.ID, "missing-host-uuid", true, pinnedSecretCred("secret-e", "cred-user-e"))

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-6")

		require.NoError(tt, err)
		assert.Empty(tt, creds)
	})

	t.Run("WhenMultiplePinnedHosts_ReturnsAllWithSecretID", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-7", "proj-7")
		host1 := seedHost(tt, store, "host-uuid-5", "cluster-e", "user-e", "10.0.0.5")
		host2 := seedHost(tt, store, "host-uuid-6", "cluster-f", "user-f", "10.0.0.6")
		seedAccountHost(tt, store, "ah-uuid-6a", acc.ID, host1.UUID, true, pinnedSecretCred("secret-e", "cred-e"))
		seedAccountHost(tt, store, "ah-uuid-6b", acc.ID, host2.UUID, true, pinnedSecretCred("secret-f", "cred-f"))

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-7")

		require.NoError(tt, err)
		require.Len(tt, creds, 2)
		bySecret := map[string]*HostCredential{}
		for _, c := range creds {
			bySecret[c.SecretId] = c
		}
		assert.NotNil(tt, bySecret["secret-e"])
		assert.NotNil(tt, bySecret["secret-f"])
	})

	t.Run("WhenPinnedHostMatchesCluster_ReturnsAccountHostUsername", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-8", "proj-8")
		host := seedHost(tt, store, "host-uuid-7", "cluster-g", "host-user-g", "10.0.0.7")
		seedAccountHost(tt, store, "ah-uuid-7", acc.ID, host.UUID, true, pinnedSecretCred("secret-g", "account-host-user-g"))

		hc, err := store.GetDedicatedPinnedHostCredential(context.Background(), "proj-8", "cluster-g")

		require.NoError(tt, err)
		require.NotNil(tt, hc)
		assert.Equal(tt, "account-host-user-g", hc.Username, "dedicated flow uses account_hosts username")
		assert.Equal(tt, "secret-g", hc.SecretId)
		assert.Equal(tt, env.USERNAME_PWD_SEC_MGR, hc.AuthType)
		require.NotNil(tt, hc.Host)
		assert.Equal(tt, "cluster-g", hc.Host.ExternalName)
	})

	t.Run("WhenPinnedHostHasCertificateCredentials_ReturnsCertMetadata", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-cert", "proj-cert")
		host := seedHostWithCredentials(tt, store, "host-uuid-cert", "cluster-cert", "host-user-cert", "10.0.0.20", "proj/pool/ca")
		seedAccountHost(tt, store, "ah-uuid-cert", acc.ID, host.UUID, true,
			pinnedCertCred("account-host-cert-uuid", "account-host-secret-uuid", "gcnv_u_cert"))

		hc, err := store.GetDedicatedPinnedHostCredential(context.Background(), "proj-cert", "cluster-cert")

		require.NoError(tt, err)
		require.NotNil(tt, hc)
		assert.Equal(tt, "gcnv_u_cert", hc.Username)
		assert.Equal(tt, "account-host-secret-uuid", hc.SecretId)
		assert.Equal(tt, "account-host-cert-uuid", hc.CertificateID)
		assert.Equal(tt, env.USER_CERTIFICATE, hc.AuthType)
		assert.Equal(tt, "proj/pool/ca", hc.CaURI)
	})

	t.Run("WhenPinnedHostHasInlinePassword_ReturnsDecryptedPassword", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-pwd", "proj-pwd")
		host := seedHost(tt, store, "host-uuid-pwd", "cluster-pwd", "host-user-pwd", "10.0.0.21")
		encryptedPassword, err := utils.EncryptPassword(log.Secret("inline-password"))
		require.NoError(tt, err)
		seedAccountHost(tt, store, "ah-uuid-pwd", acc.ID, host.UUID, true, &datamodel.ExpertModeCredential{
			AuthType: env.USERNAME_PWD,
			Username: "gcnv_u_pwd",
			Password: *encryptedPassword,
		})

		hc, err := store.GetDedicatedPinnedHostCredential(context.Background(), "proj-pwd", "cluster-pwd")

		require.NoError(tt, err)
		require.NotNil(tt, hc)
		assert.Equal(tt, "gcnv_u_pwd", hc.Username)
		assert.Equal(tt, "inline-password", hc.Password)
		assert.Equal(tt, env.USERNAME_PWD, hc.AuthType)
		assert.Empty(tt, hc.SecretId)
	})

	t.Run("WhenClusterNameDoesNotMatch_ReturnsNil", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-9", "proj-9")
		host := seedHost(tt, store, "host-uuid-8", "cluster-h", "host-user-h", "10.0.0.8")
		seedAccountHost(tt, store, "ah-uuid-8", acc.ID, host.UUID, true, pinnedSecretCred("secret-h", "account-host-user-h"))

		hc, err := store.GetDedicatedPinnedHostCredential(context.Background(), "proj-9", "other-cluster")

		require.NoError(tt, err)
		assert.Nil(tt, hc)
	})

	t.Run("WhenClusterNameEmpty_ReturnsNil", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)

		hc, err := store.GetDedicatedPinnedHostCredential(context.Background(), "proj-any", "   ")

		require.NoError(tt, err)
		assert.Nil(tt, hc)
	})

	t.Run("WhenAccountMissing_ReturnsError", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)

		hc, err := store.GetDedicatedPinnedHostCredential(context.Background(), "nonexistent-project", "cluster-x")

		require.Error(tt, err)
		assert.Nil(tt, hc)
	})

	t.Run("WhenMixedPinnedRows_ReturnsAllProvisionedCredentials", func(tt *testing.T) {
		store := setupPinnedHostCredStore(tt)
		acc := seedAccount(tt, store, "acc-uuid-mixed", "proj-mixed")
		validHost := seedHost(tt, store, "host-uuid-valid", "cluster-valid", "user-valid", "10.0.0.10")
		inlineHost := seedHost(tt, store, "host-uuid-nosecret", "cluster-nosecret", "user-nosecret", "10.0.0.11")
		seedAccountHost(tt, store, "ah-valid", acc.ID, validHost.UUID, true, pinnedSecretCred("secret-valid", "cred-valid"))
		seedAccountHost(tt, store, "ah-nosecret", acc.ID, inlineHost.UUID, true, &datamodel.ExpertModeCredential{
			AuthType: env.USERNAME_PWD,
			Username: "user-nosecret",
			Password: "pass",
		})

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-mixed")

		require.NoError(tt, err)
		require.Len(tt, creds, 2)
		bySecret := map[string]*HostCredential{}
		var inline *HostCredential
		for _, c := range creds {
			if c.SecretId != "" {
				bySecret[c.SecretId] = c
			}
			if c.AuthType == env.USERNAME_PWD {
				inline = c
			}
		}
		assert.NotNil(tt, bySecret["secret-valid"])
		require.NotNil(tt, inline)
		assert.Equal(tt, "user-nosecret", inline.Username)
		assert.Equal(tt, "pass", inline.Password)
	})

	t.Run("WhenAccountHostsFetchFails_ReturnsVCPError", func(tt *testing.T) {
		dbSQL, mock, sqlErr := sqlmock.New()
		require.NoError(tt, sqlErr)
		gdb, sqlErr := gorm.Open(postgres.New(postgres.Config{Conn: dbSQL}), &gorm.Config{})
		require.NoError(tt, sqlErr)
		store := NewDataStoreRepository(gormwrapper.New(gdb))

		accountRows := sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "uuid", "name", "state",
		}).AddRow(int64(1), nil, nil, nil, "acc-uuid-mock", "proj-mock", datamodel.AccountStateEnabled)
		mock.ExpectQuery(`SELECT`).WillReturnRows(accountRows)

		// The account_hosts lookup itself fails.
		mock.ExpectQuery(`SELECT`).WillReturnError(fmt.Errorf("account_hosts table offline"))

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-mock")

		assert.Nil(tt, creds)
		require.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		require.True(tt, vsaerrors.As(err, &vcpErr))
		assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpErr.TrackingID)
	})

	t.Run("WhenHostFetchFails_ReturnsVCPError", func(tt *testing.T) {
		dbSQL, mock, sqlErr := sqlmock.New()
		require.NoError(tt, sqlErr)
		gdb, sqlErr := gorm.Open(postgres.New(postgres.Config{Conn: dbSQL}), &gorm.Config{})
		require.NoError(tt, sqlErr)
		store := NewDataStoreRepository(gormwrapper.New(gdb))

		accountRows := sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "uuid", "name", "state",
		}).AddRow(int64(1), nil, nil, nil, "acc-uuid-mock", "proj-mock", datamodel.AccountStateEnabled)
		mock.ExpectQuery(`SELECT`).WillReturnRows(accountRows)

		accountHostRows := sqlmock.NewRows([]string{
			"id", "created_at", "updated_at", "deleted_at", "uuid",
			"account_id", "host_id", "pinned", "service_credentials",
		}).AddRow(int64(1), nil, nil, nil, "ah-uuid-mock", int64(1), "host-uuid-mock", true, []byte(`{"auth_type":1,"secret_id":"secret-x","username":"user-x"}`))
		mock.ExpectQuery(`SELECT`).WillReturnRows(accountHostRows)

		mock.ExpectQuery(`SELECT`).WillReturnError(fmt.Errorf("hosts table offline"))

		creds, err := store.ListPinnedHostCredentials(context.Background(), "proj-mock")

		assert.Nil(tt, creds)
		require.Error(tt, err)
		var vcpErr *vsaerrors.CustomError
		require.True(tt, vsaerrors.As(err, &vcpErr))
		assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpErr.TrackingID)
	})
}

func TestHostCredential_ManagementIP(t *testing.T) {
	t.Run("NilReceiver_ReturnsEmpty", func(tt *testing.T) {
		var hc *HostCredential
		assert.Equal(tt, "", hc.ManagementIP())
	})

	t.Run("NilHost_ReturnsEmpty", func(tt *testing.T) {
		hc := &HostCredential{Host: nil}
		assert.Equal(tt, "", hc.ManagementIP())
	})

	t.Run("IPAddressSet_ReturnsIPAddress", func(tt *testing.T) {
		hc := &HostCredential{Host: &datamodel.Host{IPAddress: "10.0.0.5", Name: "host-name"}}
		assert.Equal(tt, "10.0.0.5", hc.ManagementIP())
	})

	t.Run("IPAddressEmpty_FallsBackToName", func(tt *testing.T) {
		hc := &HostCredential{Host: &datamodel.Host{IPAddress: "  ", Name: "host-name"}}
		assert.Equal(tt, "host-name", hc.ManagementIP())
	})
}

func TestHostExternalName(t *testing.T) {
	tests := []struct {
		name string
		host *datamodel.Host
		want string
	}{
		{"NilHost", nil, ""},
		{"ExternalNameSet", &datamodel.Host{ExternalName: "cluster-a", Name: "host-a"}, "cluster-a"},
		{"ExternalNameEmpty_FallsBackToName", &datamodel.Host{ExternalName: "  ", Name: "host-a"}, "host-a"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			assert.Equal(tt, tc.want, HostExternalName(tc.host))
		})
	}
}

func TestHostManagementIP(t *testing.T) {
	tests := []struct {
		name string
		host *datamodel.Host
		want string
	}{
		{"NilHost", nil, ""},
		{"IPAddressSet", &datamodel.Host{IPAddress: "10.0.0.9", Name: "host-b"}, "10.0.0.9"},
		{"IPAddressEmpty_FallsBackToName", &datamodel.Host{IPAddress: "", Name: "host-b"}, "host-b"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(tt *testing.T) {
			assert.Equal(tt, tc.want, hostManagementIP(tc.host))
		})
	}
}
