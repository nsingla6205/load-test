package database

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupAccountHostStore(t *testing.T) *DataStoreRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&datamodel.AccountHost{},
		&datamodel.Job{},
	))
	return NewDataStoreRepository(gormwrapper.New(db))
}

func TestCreateAccountHost_Success(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	created, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{
		AccountID: 42,
		HostID:    "9760acf5-4638-11e7-9bdb-020073ca3333",
		Pinned:    false,
		OntapModeCredentials: &datamodel.ExpertModeCredential{
			AuthType: env.USERNAME_PWD_SEC_MGR,
			Username: "svc_gadmin",
			SecretID: "projects/p/secrets/s/versions/1",
		},
	})
	require.NoError(t, err)
	assert.NotEmpty(t, created.UUID)
	assert.False(t, created.Pinned)
	assert.True(t, created.HasProvisionedCredentials())
}

func TestCreateAccountHost_ConflictOnSameAccountAndHost(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	_, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{
		AccountID: 42,
		HostID:    "host-1",
		Pinned:    false,
	})
	require.NoError(t, err)

	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{
		AccountID: 42,
		HostID:    "host-1",
		Pinned:    false,
	})
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

// TestCreateAccountHost_ConcurrentSameAccountAndHost locks in the race-loser contract:
// concurrent inserts for the same (account_id, host_id) must surface
// ErrResourceStateConflictError (HTTP 409), never a generic insert error (HTTP 500).
func TestCreateAccountHost_ConcurrentSameAccountAndHost(t *testing.T) {
	db, fileName, err := SetupTestFileDB()
	require.NoError(t, err)
	defer cleanupTestDBFile(db, fileName)

	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_account_hosts_account_host_active
		ON account_hosts (account_id, host_id)
		WHERE deleted_at IS NULL
	`).Error)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	const accountID int64 = 42
	const hostID = "9760acf5-4638-11e7-9bdb-020073ca3333"
	const numParallel = 8

	var wg sync.WaitGroup
	errs := make([]error, numParallel)
	results := make([]*datamodel.AccountHost, numParallel)
	start := make(chan struct{})

	for i := 0; i < numParallel; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			row, e := store.CreateAccountHost(context.Background(), &datamodel.AccountHost{
				AccountID: accountID,
				HostID:    hostID,
				Pinned:    false,
			})
			results[idx] = row
			errs[idx] = e
		}(i)
	}
	close(start)
	wg.Wait()

	var successes, conflicts int
	for i, e := range errs {
		switch {
		case e == nil:
			successes++
			require.NotNil(t, results[i], "goroutine %d returned nil row on success", i)
		default:
			var customErr *vsaerrors.CustomError
			require.True(t, vsaerrors.As(e, &customErr), "goroutine %d error type %T: %v", i, e, e)
			if customErr.IsError(vsaerrors.ErrResourceStateConflictError) {
				conflicts++
			} else {
				t.Errorf("goroutine %d returned unexpected VCP error: %v", i, e)
			}
		}
	}
	assert.Equal(t, 1, successes, "exactly one goroutine should succeed")
	assert.Equal(t, numParallel-1, conflicts, "losers should get ErrResourceStateConflictError")

	var count int64
	require.NoError(t, store.db.GORM().Model(&datamodel.AccountHost{}).
		Where("account_id = ? AND host_id = ? AND deleted_at IS NULL", accountID, hostID).
		Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestSetAccountHostPinned_RetainsCredentials(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	created, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{
		AccountID: 42,
		HostID:    "host-1",
		Pinned:    true,
		OntapModeCredentials: &datamodel.ExpertModeCredential{
			AuthType: env.USERNAME_PWD_SEC_MGR,
			Username: "svc_gadmin",
			SecretID: "projects/p/secrets/s/versions/1",
		},
	})
	require.NoError(t, err)

	require.NoError(t, store.SetAccountHostPinned(ctx, created.UUID, false))

	got, err := store.GetAccountHost(ctx, created.UUID)
	require.NoError(t, err)
	assert.False(t, got.Pinned)
	assert.True(t, got.HasProvisionedCredentials())
}

func TestListPinnedAccountHostsByAccount(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	_, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-1", Pinned: true})
	require.NoError(t, err)
	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-2", Pinned: false})
	require.NoError(t, err)
	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 99, HostID: "host-3", Pinned: true})
	require.NoError(t, err)

	pinned, err := store.ListPinnedAccountHostsByAccount(ctx, 42)
	require.NoError(t, err)
	require.Len(t, pinned, 1)
	assert.Equal(t, "host-1", pinned[0].HostID)
}

func TestListAccountHostsByAccount(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	_, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-1", Pinned: true})
	require.NoError(t, err)
	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-2", Pinned: false})
	require.NoError(t, err)
	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 99, HostID: "host-3", Pinned: true})
	require.NoError(t, err)

	rows, err := store.ListAccountHostsByAccount(ctx, 42)
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

func TestGetAccountHost_SuccessAndNotFound(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	created, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-1", Pinned: false})
	require.NoError(t, err)

	got, err := store.GetAccountHost(ctx, created.UUID)
	require.NoError(t, err)
	assert.Equal(t, created.UUID, got.UUID)

	_, err = store.GetAccountHost(ctx, "00000000-0000-0000-0000-000000000000")
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetAccountHostByAccountAndHost(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	created, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-1", Pinned: false})
	require.NoError(t, err)

	got, err := store.GetAccountHostByAccountAndHost(ctx, 42, "  host-1  ")
	require.NoError(t, err)
	assert.Equal(t, created.UUID, got.UUID)

	_, err = store.GetAccountHostByAccountAndHost(ctx, 42, "missing-host")
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetAccountHostsByAccountAndHostUUIDs(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	_, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-1", Pinned: false})
	require.NoError(t, err)
	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-2", Pinned: false})
	require.NoError(t, err)

	empty, err := store.GetAccountHostsByAccountAndHostUUIDs(ctx, 42, nil)
	require.NoError(t, err)
	assert.Empty(t, empty)

	got, err := store.GetAccountHostsByAccountAndHostUUIDs(ctx, 42, []string{"host-1", "host-2", "host-missing"})
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestCreateAccountHost_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	_, err := store.CreateAccountHost(ctx, nil)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataInsertError))

	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{HostID: "host-1"})
	require.Error(t, err)
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataInsertError))

	_, err = store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "  "})
	require.Error(t, err)
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataInsertError))
}

func TestSetAccountHostPinned_NotFound(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	err := store.SetAccountHostPinned(ctx, "00000000-0000-0000-0000-000000000000", true)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestUpdateAccountHostServiceCredentials(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	created, err := store.CreateAccountHost(ctx, &datamodel.AccountHost{AccountID: 42, HostID: "host-1", Pinned: false})
	require.NoError(t, err)

	cred := &datamodel.ExpertModeCredential{
		AuthType: env.USERNAME_PWD_SEC_MGR,
		Username: "svc_user",
		SecretID: "projects/p/secrets/s/versions/1",
	}
	require.NoError(t, store.UpdateAccountHostServiceCredentials(ctx, created.UUID, cred))

	got, err := store.GetAccountHost(ctx, created.UUID)
	require.NoError(t, err)
	require.NotNil(t, got.OntapModeCredentials)
	assert.Equal(t, "svc_user", got.OntapModeCredentials.Username)

	err = store.UpdateAccountHostServiceCredentials(ctx, "00000000-0000-0000-0000-000000000000", cred)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetActivePinAccountHostsJobByAccount_NotFound(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	_, err := store.GetActivePinAccountHostsJobByAccount(ctx, 42)
	require.Error(t, err)
	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetActivePinAccountHostsJobByAccount(t *testing.T) {
	ctx := context.Background()
	store := setupAccountHostStore(t)

	err := store.db.GORM().WithContext(ctx).Create(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "job-1"},
		Type:      string(datamodel.JobTypePinAccountHosts),
		State:     string(datamodel.JobsStatePROCESSING),
		AccountID: sql.NullInt64{Int64: 42, Valid: true},
	}).Error
	require.NoError(t, err)

	job, err := store.GetActivePinAccountHostsJobByAccount(ctx, 42)
	require.NoError(t, err)
	assert.Equal(t, "job-1", job.UUID)
}
