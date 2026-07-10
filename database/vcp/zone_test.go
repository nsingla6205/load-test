package database

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
)

func setupZoneTestRepo(t *testing.T) (*DataStoreRepository, *datamodel.Region) {
	t.Helper()
	db, err := SetupTestDB()
	require.NoError(t, err)
	repo := NewDataStoreRepository(gormwrapper.New(db))
	require.NoError(t, ClearInMemoryDB(repo.db.GORM()))

	region, err := repo.CreateRegion(context.Background(), &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)
	return repo, region
}

func TestCreateZone_Success(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)
	assert.NotEmpty(t, created.UUID)
	assert.Equal(t, "us-west1-a", created.Name)
	assert.Equal(t, "America/Los_Angeles", created.Timezone)
	assert.Equal(t, region.ID, created.RegionID.Int64)
}

func TestCreateZone_RegionNotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, "missing-region-uuid")
	require.Error(t, err)
	assert.Nil(t, created)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestCreateZone_DuplicateName(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	_, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	_, err = repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestListZones_Success(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	_, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	zones, err := repo.ListZones(ctx)
	require.NoError(t, err)
	require.Len(t, zones, 1)
	assert.Equal(t, "us-west1-a", zones[0].Name)
	assert.Equal(t, region.ID, zones[0].RegionID.Int64)
}

func TestUpdateZone_Success(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	updated, err := repo.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west1-b",
		Timezone:  "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)
	assert.Equal(t, "us-west1-b", updated.Name)
}

func TestUpdateZone_NoChangeFastPath(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	updated, err := repo.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west1-a",
		Timezone:  "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)
	assert.Equal(t, created.UUID, updated.UUID)
	assert.Equal(t, "us-west1-a", updated.Name)
}

func TestUpdateZone_NotFound(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	updated, err := repo.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: "missing-zone-uuid"},
		Name:      "us-west1-b",
		Timezone:  "America/Los_Angeles",
	}, region.UUID)
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestUpdateZone_RegionNotFound(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	updated, err := repo.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west1-b",
		Timezone:  "America/Los_Angeles",
	}, "missing-region-uuid")
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestUpdateZone_ConflictDuplicateName(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	_, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	zone2, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-b",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	updated, err := repo.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: zone2.UUID},
		Name:      "us-west1-a",
		Timezone:  "America/Los_Angeles",
	}, region.UUID)
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestListZones_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupZoneTestRepo(t)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	zones, err := repo.ListZones(ctx)
	require.Error(t, err)
	assert.Nil(t, zones)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestCreateZone_InsertDatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	// Break a column used by insert but not by the duplicate-name count check.
	require.NoError(t, repo.db.GORM().Exec("ALTER TABLE zones RENAME COLUMN timezone TO tz_name").Error)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.Error(t, err)
	assert.Nil(t, created)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataInsertError))
}

func TestCreateZone_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.Error(t, err)
	assert.Nil(t, created)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestUpdateZone_UpdateDatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	// Break a column used by update but not by the duplicate-name count check.
	require.NoError(t, repo.db.GORM().Exec("ALTER TABLE zones RENAME COLUMN timezone TO tz_name").Error)

	updated, err := repo.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west1-b",
		Timezone:  "America/Los_Angeles",
	}, region.UUID)
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataUpdateError))
}

func TestUpdateZone_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	updated, err := repo.UpdateZone(ctx, &datamodel.Zone{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west1-b",
		Timezone:  "America/Los_Angeles",
	}, region.UUID)
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestGetZoneByRegionName_Success(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	got, err := repo.GetZoneByRegionName(ctx, "us-west1")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, created.UUID, got.UUID)
	assert.Equal(t, "us-west1-a", got.Name)
}

func TestGetZoneByRegionName_RegionNotFound(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupZoneTestRepo(t)

	got, err := repo.GetZoneByRegionName(ctx, "missing-region")
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetZoneByRegionName_NoZonesInRegion(t *testing.T) {
	ctx := context.Background()
	repo, _ := setupZoneTestRepo(t)

	got, err := repo.GetZoneByRegionName(ctx, "us-west1")
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetZoneByUUID_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo, region := setupZoneTestRepo(t)

	created, err := repo.CreateZone(ctx, &datamodel.Zone{
		Name:     "us-west1-a",
		Timezone: "America/Los_Angeles",
	}, region.UUID)
	require.NoError(t, err)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	got, err := repo.getZoneByUUID(ctx, created.UUID)
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}
