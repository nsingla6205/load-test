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

func createStoredHost(t *testing.T, store *DataStoreRepository, ipAddress string) *datamodel.Host {
	t.Helper()

	created, err := store.CreateHost(context.Background(), sampleHost(ipAddress))
	require.NoError(t, err)
	require.NotNil(t, created)
	return created
}

func TestListHosts_Success(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.80")
	created2 := createStoredHost(t, store, "10.0.0.81")

	got, err := store.ListHosts(ctx)
	require.NoError(t, err)
	require.Len(t, got, 2)

	uuids := []string{got[0].UUID, got[1].UUID}
	assert.Contains(t, uuids, created.UUID)
	assert.Contains(t, uuids, created2.UUID)
}

func TestListHosts_ExcludesDeleted(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.82")
	_, err := store.DeleteHost(ctx, created)
	require.NoError(t, err)

	got, err := store.ListHosts(ctx)
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListHosts_DatabaseError(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)
	createStoredHost(t, store, "10.0.0.84")

	sqlDB, err := store.db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = store.ListHosts(ctx)
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestGetHost_Success(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.80")

	got, err := store.GetHost(ctx, created.UUID)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, created.UUID, got.UUID)
	assert.Equal(t, "host-01", got.Name)
	assert.Equal(t, "10.0.0.80", got.IPAddress)
	assert.Equal(t, int64(68317667328), got.Capacity)
	require.NotNil(t, got.Attributes)
	assert.Equal(t, "9.15.1", got.Attributes.Version)
}

func TestGetHost_NotFound(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	got, err := store.GetHost(ctx, "missing-host-uuid")
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetHost_ExcludesSoftDeleted(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.81")
	_, err := store.DeleteHost(ctx, created)
	require.NoError(t, err)

	got, err := store.GetHost(ctx, created.UUID)
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetHost_ReadError(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.82")

	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = store.GetHost(ctx, created.UUID)
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestUpdateHost_Success(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.90")
	created.Name = "host-updated"
	created.Label = "type=UPDATED"
	created.Capacity = 12345
	require.NotNil(t, created.Attributes)
	created.Attributes.MaintenanceMode = true

	updated, err := store.UpdateHost(ctx, created)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "host-updated", updated.Name)
	assert.Equal(t, "type=UPDATED", updated.Label)
	assert.Equal(t, int64(12345), updated.Capacity)
	require.NotNil(t, updated.Attributes)
	assert.True(t, updated.Attributes.MaintenanceMode)
	assert.False(t, updated.UpdatedAt.IsZero())

	got, err := store.GetHost(ctx, created.UUID)
	require.NoError(t, err)
	assert.Equal(t, "host-updated", got.Name)
	assert.Equal(t, "type=UPDATED", got.Label)
}

func TestUpdateHost_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	tests := []struct {
		name string
		host *datamodel.Host
	}{
		{name: "nil host", host: nil},
		{name: "missing uuid", host: &datamodel.Host{Name: "host-01"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updated, err := store.UpdateHost(ctx, tc.host)
			require.Error(t, err)
			assert.Nil(t, updated)

			var customErr *vsaerrors.CustomError
			require.True(t, vsaerrors.As(err, &customErr))
			assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataUpdateError))
			assert.Contains(t, customErr.GetDetailMessage(), "host uuid is required")
		})
	}
}

func TestUpdateHost_DuplicateIPAddress(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	_, err := store.CreateHost(ctx, sampleHost("10.0.0.91"))
	require.NoError(t, err)

	second := createStoredHost(t, store, "10.0.0.92")
	second.IPAddress = "10.0.0.91"

	updated, err := store.UpdateHost(ctx, second)
	require.Error(t, err)
	assert.Nil(t, updated)
	assert.True(t, customerrors.IsConflictErr(err))

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestUpdateHost_UpdateError(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.93")

	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	created.Name = "host-updated"
	updated, err := store.UpdateHost(ctx, created)
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataUpdateError))
}

func TestDeleteHost_Success(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.100")

	deleted, err := store.DeleteHost(ctx, created)
	require.NoError(t, err)
	require.NotNil(t, deleted)
	assert.Equal(t, datamodel.LifeCycleStateDeleted, deleted.State)
	assert.Equal(t, datamodel.LifeCycleStateDeletedDetails, deleted.StateDetails)
	require.NotNil(t, deleted.DeletedAt)
	assert.True(t, deleted.DeletedAt.Valid)
	assert.False(t, deleted.UpdatedAt.IsZero())

	_, err = store.GetHost(ctx, created.UUID)
	require.Error(t, err)

	count, err := store.CountHostsWithIPAddress(ctx, "10.0.0.100", false)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	count, err = store.CountHostsWithIPAddress(ctx, "10.0.0.100", true)
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}

func TestDeleteHost_ValidationErrors(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	tests := []struct {
		name string
		host *datamodel.Host
	}{
		{name: "nil host", host: nil},
		{name: "missing uuid", host: &datamodel.Host{Name: "host-01"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deleted, err := store.DeleteHost(ctx, tc.host)
			require.Error(t, err)
			assert.Nil(t, deleted)

			var customErr *vsaerrors.CustomError
			require.True(t, vsaerrors.As(err, &customErr))
			assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataDeleteError))
			assert.Contains(t, customErr.GetDetailMessage(), "host uuid is required")
		})
	}
}

func TestDeleteHost_DeleteError(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	created := createStoredHost(t, store, "10.0.0.101")

	sqlDB, err := store.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	deleted, err := store.DeleteHost(ctx, created)
	require.Error(t, err)
	assert.Nil(t, deleted)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataDeleteError))
}

func TestCreateHost_PreservesProvidedPassphraseVersion(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	host := sampleHost("10.0.0.61")
	host.Attributes.PassphraseVersion = "custom-version"

	created, err := store.CreateHost(ctx, host)
	require.NoError(t, err)
	require.NotNil(t, created.Attributes)
	assert.Equal(t, "custom-version", created.Attributes.PassphraseVersion)
}

func TestGetHostsByUUIDs_ReturnsActiveHosts(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	host1 := createStoredHost(t, store, "10.0.0.111")
	host2 := createStoredHost(t, store, "10.0.0.112")

	got, err := store.GetHostsByUUIDs(ctx, []string{host1.UUID, host2.UUID})
	require.NoError(t, err)
	assert.Len(t, got, 2)
}

func TestGetHostsByUUIDs_ExcludesDeleted(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	host1 := createStoredHost(t, store, "10.0.0.113")
	host2 := createStoredHost(t, store, "10.0.0.114")

	_, err := store.DeleteHost(ctx, host2)
	require.NoError(t, err)

	got, err := store.GetHostsByUUIDs(ctx, []string{host1.UUID, host2.UUID})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, host1.UUID, got[0].UUID)
}

func TestGetHostsByUUIDs_EmptyInput(t *testing.T) {
	ctx := context.Background()
	store := setupHostStore(t)

	got, err := store.GetHostsByUUIDs(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
}
