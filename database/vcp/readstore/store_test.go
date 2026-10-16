package readstore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"gorm.io/gorm"
)

func newTestStore(t *testing.T) (ReadStore, *gorm.DB) {
	t.Helper()
	db, err := database.SetupTestDB()
	require.NoError(t, err)
	return New(db), db
}

func deletedAt(ts time.Time) *gorm.DeletedAt {
	d := gorm.DeletedAt{Time: ts, Valid: true}
	return &d
}

type fixture struct {
	account      *datamodel.Account
	otherAccount *datamodel.Account
	pool         *datamodel.Pool
	deletedPool  *datamodel.Pool
	volumes      []*datamodel.Volume
}

func seed(t *testing.T, db *gorm.DB) fixture {
	t.Helper()
	f := fixture{
		account:      &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-1"}, Name: "111"},
		otherAccount: &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-2"}, Name: "222"},
	}
	require.NoError(t, db.Create(f.account).Error)
	require.NoError(t, db.Create(f.otherAccount).Error)

	f.pool = &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-1"}, Name: "p1", DeploymentName: "d1", AccountID: f.account.ID, State: "READY"}
	f.deletedPool = &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-2", DeletedAt: deletedAt(time.Now())}, Name: "p2", DeploymentName: "d2", AccountID: f.account.ID, State: "DELETED",
		PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-east4-b"}, PoolCredentials: &datamodel.PoolCredentials{Password: "secret"}}
	require.NoError(t, db.Create(f.pool).Error)
	require.NoError(t, db.Create(f.deletedPool).Error)

	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	for i, spec := range []struct {
		uuid, name, state string
		poolID            int64
		deleted           *gorm.DeletedAt
		vault             string
	}{
		{"vol-1", "v1", "READY", f.pool.ID, nil, "vault-1"},
		{"vol-2", "v2", "CREATING", f.pool.ID, nil, ""},
		{"vol-3", "v3", "ERROR", f.deletedPool.ID, nil, "vault-1"},
		{"vol-4", "v1", "DELETED", f.pool.ID, deletedAt(base.Add(time.Hour)), ""},
		{"vol-5", "v1", "DELETED", f.pool.ID, deletedAt(base.Add(2 * time.Hour)), ""},
	} {
		v := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: spec.uuid, CreatedAt: base.Add(time.Duration(i) * 24 * time.Hour), DeletedAt: spec.deleted},
			Name:      spec.name, State: spec.state, AccountID: f.account.ID, PoolID: spec.poolID,
			DataProtection: &datamodel.DataProtection{BackupVaultID: spec.vault},
		}
		require.NoError(t, db.Create(v).Error)
		f.volumes = append(f.volumes, v)
	}
	return f
}

func TestGetByUUID(t *testing.T) {
	s, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()

	pool, err := s.GetPoolByUUID(ctx, "pool-2")
	require.NoError(t, err)
	assert.Equal(t, "p2", pool.Name)
	require.NotNil(t, pool.DeletedAt)

	vol, err := s.GetVolumeByUUID(ctx, "vol-3")
	require.NoError(t, err)
	require.NotNil(t, vol.Pool, "deleted parent pool must still preload")
	assert.Equal(t, "pool-2", vol.Pool.UUID)
	require.NotNil(t, vol.Pool.PoolAttributes, "pool attributes carry the zone")
	assert.Equal(t, "us-east4-b", vol.Pool.PoolAttributes.PrimaryZone)
	assert.Nil(t, vol.Pool.PoolCredentials, "pool credentials are not loaded with a volume")
	require.NotNil(t, vol.Account)
	assert.Equal(t, f.account.Name, vol.Account.Name)

	_, err = s.GetVolumeByUUID(ctx, "missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

func uuids[T any](rows []T, get func(T) string) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, get(r))
	}
	return out
}

func volUUID(v datamodel.Volume) string { return v.UUID }

func TestGetAccount(t *testing.T) {
	s, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()

	acct, err := s.GetAccountByName(ctx, "111")
	require.NoError(t, err)
	assert.Equal(t, f.account.ID, acct.ID)

	require.NoError(t, db.Delete(f.otherAccount).Error)
	acct, err = s.GetAccountByName(ctx, "222")
	require.NoError(t, err, "a soft-deleted project still resolves")
	assert.Equal(t, f.otherAccount.ID, acct.ID)

	accounts, err := s.GetAccountsByIDs(ctx, []int64{f.account.ID, f.otherAccount.ID, 999})
	require.NoError(t, err, "one query for every id, deleted accounts included")
	require.Len(t, accounts, 2)
	assert.Equal(t, "111", accounts[f.account.ID].Name)
	assert.Equal(t, "222", accounts[f.otherAccount.ID].Name)

	accounts, err = s.GetAccountsByIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, accounts)

	_, err = s.GetAccountByName(ctx, "999")
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestStoreReadsEveryType(t *testing.T) {
	s, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()
	acct := []Eq{{ColAccountID, f.account.ID}}
	page := ListOptions{Eq: acct, Limit: 10}

	vault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "vault-1"}, Name: "bv", AccountID: f.account.ID, LifeCycleState: "READY"}
	require.NoError(t, db.Create(vault).Error)
	require.NoError(t, db.Create(&datamodel.Backup{BaseModel: datamodel.BaseModel{UUID: "bk-1"}, Name: "b", BackupVaultID: vault.ID, VolumeUUID: "vol-1", State: "READY"}).Error)
	require.NoError(t, db.Create(&datamodel.BackupPolicy{BaseModel: datamodel.BaseModel{UUID: "bp-1"}, Name: "bp", AccountID: f.account.ID}).Error)
	require.NoError(t, db.Create(&datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "snap-1"}, Name: "s", AccountID: f.account.ID, VolumeID: f.volumes[0].ID}).Error)
	require.NoError(t, db.Create(&datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: "rep-1"}, Name: "r", AccountID: f.account.ID, VolumeID: f.volumes[0].ID,
		ReplicationAttributes: &datamodel.ReplicationDetails{SourceVolumeUUID: "remote-vol"}}).Error)
	require.NoError(t, db.Create(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-1"}, AccountID: f.account.ID}).Error)
	require.NoError(t, db.Create(&datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{UUID: "ad-1"}, AdName: "ad", AccountId: f.account.ID}).Error)
	require.NoError(t, db.Create(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-1"}, CorrelationID: "corr-1", State: "NEW"}).Error)

	backup, err := s.GetBackupByUUID(ctx, "bk-1")
	require.NoError(t, err)
	require.NotNil(t, backup.BackupVault)
	require.NotNil(t, backup.BackupVault.Account, "backup owner project comes from its vault")
	assert.Equal(t, "111", backup.BackupVault.Account.Name)

	backups, err := s.ListBackups(ctx, ListOptions{Eq: []Eq{{ColBackupVaultID, vault.ID}, {ColVolumeUUID, "vol-1"}}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, backups, 1)

	_, err = s.GetBackupVaultByName(ctx, NameQuery{Name: "bv", Eq: acct})
	require.NoError(t, err)
	_, err = s.GetBackupPolicyByName(ctx, NameQuery{Name: "bp", Eq: acct})
	require.NoError(t, err)
	_, err = s.GetSnapshotByName(ctx, NameQuery{Name: "s", Eq: append(acct, Eq{ColVolumeID, f.volumes[0].ID})})
	require.NoError(t, err)
	_, err = s.GetReplicationByName(ctx, NameQuery{Name: "r", Eq: append(acct, Eq{ColVolumeID, f.volumes[0].ID})})
	require.NoError(t, err)
	_, err = s.GetActiveDirectoryByName(ctx, NameQuery{Name: "ad", Eq: acct})
	require.NoError(t, err)
	_, err = s.GetKmsConfigByUUID(ctx, "kms-1")
	require.NoError(t, err)
	_, err = s.GetJobByUUID(ctx, "job-1")
	require.NoError(t, err)
	_, err = s.GetPoolByName(ctx, NameQuery{Name: "p1", Eq: acct})
	require.NoError(t, err)

	reps, err := s.ListReplications(ctx, ListOptions{Eq: []Eq{{ColReplicationSourceVolumeUUID, "remote-vol"}}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, reps, 1)
	jobs, err := s.ListJobs(ctx, ListOptions{Eq: []Eq{{ColCorrelationID, "corr-1"}}, Limit: 10})
	require.NoError(t, err)
	assert.Len(t, jobs, 1)

	for name, fn := range map[string]func() (int, error){
		"pools":       func() (int, error) { r, err := s.ListPools(ctx, page); return len(r), err },
		"snapshots":   func() (int, error) { r, err := s.ListSnapshots(ctx, page); return len(r), err },
		"vaults":      func() (int, error) { r, err := s.ListBackupVaults(ctx, page); return len(r), err },
		"policies":    func() (int, error) { r, err := s.ListBackupPolicies(ctx, page); return len(r), err },
		"kms":         func() (int, error) { r, err := s.ListKmsConfigs(ctx, page); return len(r), err },
		"directories": func() (int, error) { r, err := s.ListActiveDirectories(ctx, page); return len(r), err },
	} {
		n, err := fn()
		require.NoError(t, err, name)
		assert.Equal(t, 1, n, name)
	}
}

func TestGetKmsConfigByUUIDPreloadsServiceAccount(t *testing.T) {
	s, db := newTestStore(t)
	f := seed(t, db)
	ctx := context.Background()

	sa := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-1", DeletedAt: deletedAt(time.Now())}, Name: "sa", AccountID: f.account.ID,
		ServiceAccountEmail: "sa@example.com", ServiceAccountPasswordLocation: "encrypted-key",
		ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{Keys: []datamodel.ServiceAccountKey{{KeyID: "k1", IsPrimary: true, IsActive: true}}},
	}
	require.NoError(t, db.Create(sa).Error)
	require.NoError(t, db.Create(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-1"}, AccountID: f.account.ID, ServiceAccountID: &sa.ID}).Error)
	require.NoError(t, db.Create(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-2"}, AccountID: f.account.ID}).Error)

	t.Run("WhenServiceAccountIsLinked", func(t *testing.T) {
		kms, err := s.GetKmsConfigByUUID(ctx, "kms-1")
		require.NoError(t, err)
		require.NotNil(t, kms.ServiceAccount, "a deleted service account still preloads")
		assert.Equal(t, "sa@example.com", kms.ServiceAccount.ServiceAccountEmail)
		require.NotNil(t, kms.ServiceAccount.ServiceAccountAttributes)
		assert.Equal(t, "k1", kms.ServiceAccount.ServiceAccountAttributes.Keys[0].KeyID)
		assert.Empty(t, kms.ServiceAccount.ServiceAccountPasswordLocation, "the encrypted key column is not loaded")
	})

	t.Run("WhenNoServiceAccount", func(t *testing.T) {
		kms, err := s.GetKmsConfigByUUID(ctx, "kms-2")
		require.NoError(t, err)
		assert.Nil(t, kms.ServiceAccount)
	})

	t.Run("WhenListing", func(t *testing.T) {
		rows, err := s.ListKmsConfigs(ctx, ListOptions{Eq: []Eq{{ColAccountID, f.account.ID}}, Limit: 10})
		require.NoError(t, err)
		require.Len(t, rows, 2)
		require.NotNil(t, rows[0].ServiceAccount)
		assert.Empty(t, rows[0].ServiceAccount.ServiceAccountPasswordLocation)
	})
}
