package database

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

func setupHostStore(t *testing.T) *DataStoreRepository {
	t.Helper()

	db, err := SetupTestDB()
	require.NoError(t, err)
	require.NoError(t, ClearInMemoryDB(db))
	require.NoError(t, db.Exec(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_hosts_ip_address_unique
		ON hosts (ip_address)
		WHERE deleted_at IS NULL AND ip_address <> ''
	`).Error)
	return NewDataStoreRepository(gormwrapper.New(db))
}

func sampleHost(ipAddress string) *datamodel.Host {
	port := 443
	return &datamodel.Host{
		Name:         "host-01",
		ExternalUUID: "cluster-uuid-123",
		ExternalName: "vsim-cluster",
		Username:     "admin",
		Password:     "encrypted-password",
		Protocol:     datamodel.ExternalClusterProtocolInsecureHTTPS,
		Port:         &port,
		IPAddress:    ipAddress,
		State:        datamodel.LifeCycleStateCreated,
		StateDetails: datamodel.LifeCycleStateCreatedDetails,
		Label:        "type=VSIM",
		Capacity:     68317667328,
		Attributes: &datamodel.HostAttributes{
			Version:           "9.15.1",
			VersionGeneration: 9,
			VersionMajor:      15,
			VersionMinor:      1,
			PublicAccess:      true,
		},
	}
}

func TestCreateHost_Success(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created, err := store.CreateHost(ctx, sampleHost("10.0.0.1"))
	require.NoError(t, err)
	require.NotNil(t, created)

	assert.NotEmpty(t, created.UUID)
	assert.False(t, created.CreatedAt.IsZero())
	assert.False(t, created.UpdatedAt.IsZero())
	assert.Equal(t, "host-01", created.Name)
	assert.Equal(t, "10.0.0.1", created.IPAddress)
	assert.Equal(t, int64(68317667328), created.Capacity)
	require.NotNil(t, created.Attributes)
	assert.Equal(t, "9.15.1", created.Attributes.Version)
	assert.Equal(t, datamodel.VCP_PASSPHRASE_V0, created.Attributes.PassphraseVersion)

	count, err := store.CountHostsWithIPAddress(ctx, "10.0.0.1", false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCreateHost_DefaultPassphraseVersion(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	t.Run("sets default when attributes nil", func(t *testing.T) {
		host := sampleHost("10.0.0.60")
		host.Attributes = nil

		created, err := store.CreateHost(ctx, host)
		require.NoError(t, err)
		require.NotNil(t, created.Attributes)
		assert.Equal(t, datamodel.VCP_PASSPHRASE_V0, created.Attributes.PassphraseVersion)
	})
}

func TestCreateHost_PreservesProvidedUUID(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	host := sampleHost("10.0.0.2")
	host.UUID = "fixed-host-uuid"
	host.CreatedAt = time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC)
	host.UpdatedAt = host.CreatedAt

	created, err := store.CreateHost(ctx, host)
	require.NoError(t, err)
	assert.Equal(t, "fixed-host-uuid", created.UUID)
	assert.False(t, created.CreatedAt.IsZero())
	assert.False(t, created.UpdatedAt.IsZero())
	assert.Equal(t, created.CreatedAt, created.UpdatedAt)
}

func TestCreateHost_DuplicateIPAddress(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	_, err := store.CreateHost(ctx, sampleHost("10.0.0.70"))
	require.NoError(t, err)

	duplicate := sampleHost("10.0.0.70")
	duplicate.Name = "host-duplicate"
	_, err = store.CreateHost(ctx, duplicate)
	require.Error(t, err)
	assert.True(t, customerrors.IsConflictErr(err))

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestCreateHost_DuplicateIPAddress_AllowsReuseAfterSoftDelete(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created, err := store.CreateHost(ctx, sampleHost("10.0.0.71"))
	require.NoError(t, err)
	require.NoError(t, store.db.GORM().Delete(created).Error)

	replacement := sampleHost("10.0.0.71")
	replacement.Name = "host-replacement"
	_, err = store.CreateHost(ctx, replacement)
	require.NoError(t, err)
}

func TestCreateHost_InsertError(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = store.CreateHost(ctx, sampleHost("10.0.0.1"))
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataInsertError))
}

func TestCountHostsWithIPAddress_ReturnsOneWhenHostExists(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	_, err := store.CreateHost(ctx, sampleHost("10.0.0.10"))
	require.NoError(t, err)

	count, err := store.CountHostsWithIPAddress(ctx, "10.0.0.10", false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCountHostsWithIPAddress_MatchesIPAddressColumnNotName(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	host := sampleHost("10.0.0.20")
	host.Name = "different-host-name"
	_, err := store.CreateHost(ctx, host)
	require.NoError(t, err)

	count, err := store.CountHostsWithIPAddress(ctx, "10.0.0.20", false)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)

	count, err = store.CountHostsWithIPAddress(ctx, "different-host-name", false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestCountHostsWithIPAddress_ReturnsZeroWhenNotFound(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	count, err := store.CountHostsWithIPAddress(ctx, "10.9.9.9", false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestCountHostsWithIPAddress_ExcludesDeletedByDefault(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created, err := store.CreateHost(ctx, sampleHost("10.0.0.30"))
	require.NoError(t, err)

	require.NoError(t, store.db.GORM().Delete(created).Error)

	count, err := store.CountHostsWithIPAddress(ctx, "10.0.0.30", false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)
}

func TestCountHostsWithIPAddress_IncludeDeleted(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created, err := store.CreateHost(ctx, sampleHost("10.0.0.40"))
	require.NoError(t, err)

	require.NoError(t, store.db.GORM().Delete(created).Error)

	count, err := store.CountHostsWithIPAddress(ctx, "10.0.0.40", true)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestCountHostsWithIPAddress_ReadError(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created, err := store.CreateHost(ctx, sampleHost("10.0.0.50"))
	require.NoError(t, err)

	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = store.CountHostsWithIPAddress(ctx, created.IPAddress, false)
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestGetHostByUUID(t *testing.T) {
	t.Run("WhenHostExists", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		store := NewDataStoreRepository(gormwrapper.New(db))
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		host := &datamodel.Host{
			BaseModel: datamodel.BaseModel{UUID: "host-uuid-1"},
			Name:      "host-1",
		}
		require.NoError(t, store.db.GORM().Create(host).Error)

		got, err := store.GetHostByUUID(context.Background(), host.UUID)
		require.NoError(t, err)
		assert.Equal(t, host.UUID, got.UUID)
		assert.Equal(t, host.Name, got.Name)
	})

	t.Run("WhenHostDoesNotExist", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		store := NewDataStoreRepository(gormwrapper.New(db))
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		_, err = store.GetHostByUUID(context.Background(), "missing-host-uuid")
		require.Error(t, err)

		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
	})

	t.Run("WhenDatabaseReadFails", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		store := NewDataStoreRepository(gormwrapper.New(db))

		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())

		_, err = store.GetHostByUUID(context.Background(), "any-host-uuid")
		require.Error(t, err)

		var customErr *vsaerrors.CustomError
		require.True(t, vsaerrors.As(err, &customErr))
		assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
	})
}
