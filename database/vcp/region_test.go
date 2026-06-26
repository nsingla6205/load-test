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

func setupRegionTestRepo(t *testing.T) *DataStoreRepository {
	t.Helper()
	db, err := SetupTestDB()
	require.NoError(t, err)
	repo := NewDataStoreRepository(gormwrapper.New(db))
	require.NoError(t, ClearInMemoryDB(repo.db.GORM()))
	return repo
}

func TestCreateRegion_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	created, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)
	assert.NotEmpty(t, created.UUID)
	assert.Equal(t, "us-west1", created.Name)
}

func TestCreateRegion_DuplicateName(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	_, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	_, err = repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.Error(t, err)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestGetRegionByUUID_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	created, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	got, err := repo.GetRegionByUUID(ctx, created.UUID)
	require.NoError(t, err)
	assert.Equal(t, created.UUID, got.UUID)
	assert.Equal(t, "us-west1", got.Name)
}

func TestGetRegionByUUID_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	got, err := repo.GetRegionByUUID(ctx, "missing-region-uuid")
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestGetRegionByName_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	_, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	got, err := repo.GetRegionByName(ctx, "us-west1")
	require.NoError(t, err)
	assert.Equal(t, "us-west1", got.Name)
}

func TestGetRegionByName_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	got, err := repo.GetRegionByName(ctx, "missing-region")
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestListRegions_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	_, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)
	_, err = repo.CreateRegion(ctx, &datamodel.Region{Name: "us-east1"})
	require.NoError(t, err)

	regions, err := repo.ListRegions(ctx)
	require.NoError(t, err)
	assert.Len(t, regions, 2)
}

func TestListRegions_ExcludesDeleted(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	created, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)
	require.NoError(t, repo.db.GORM().Where("uuid = ?", created.UUID).Delete(&datamodel.Region{}).Error)

	regions, err := repo.ListRegions(ctx)
	require.NoError(t, err)
	assert.Empty(t, regions)
}

func TestUpdateRegion_Success(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	created, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	updated, err := repo.UpdateRegion(ctx, &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west2",
	})
	require.NoError(t, err)
	assert.Equal(t, "us-west2", updated.Name)
}

func TestUpdateRegion_NotFound(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	updated, err := repo.UpdateRegion(ctx, &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: "missing-region-uuid"},
		Name:      "us-west2",
	})
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataNotFoundError))
}

func TestUpdateRegion_ConflictDuplicateName(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	_, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)
	region2, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-east1"})
	require.NoError(t, err)

	require.NoError(t, repo.db.GORM().Exec(
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_regions_name_active ON regions(name) WHERE deleted_at IS NULL",
	).Error)

	updated, err := repo.UpdateRegion(ctx, &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: region2.UUID},
		Name:      "us-west1",
	})
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
}

func TestGetRegionByUUID_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	got, err := repo.GetRegionByUUID(ctx, "any-uuid")
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestUpdateRegion_SaveDatabaseError(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	created, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	require.NoError(t, repo.db.GORM().Exec("ALTER TABLE regions RENAME COLUMN name TO region_name").Error)

	updated, err := repo.UpdateRegion(ctx, &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west2",
	})
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataUpdateError))
}

func TestUpdateRegion_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	created, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.NoError(t, err)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	updated, err := repo.UpdateRegion(ctx, &datamodel.Region{
		BaseModel: datamodel.BaseModel{UUID: created.UUID},
		Name:      "us-west2",
	})
	require.Error(t, err)
	assert.Nil(t, updated)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestListRegions_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	regions, err := repo.ListRegions(ctx)
	require.Error(t, err)
	assert.Nil(t, regions)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}

func TestCreateRegion_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	// Break a column used by insert but not by the duplicate-name count check.
	require.NoError(t, repo.db.GORM().Exec("ALTER TABLE regions RENAME COLUMN uuid TO region_uuid").Error)

	created, err := repo.CreateRegion(ctx, &datamodel.Region{Name: "us-west1"})
	require.Error(t, err)
	assert.Nil(t, created)

	var customErr *vsaerrors.CustomError
	require.True(t, vsaerrors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataInsertError))
}

func TestGetRegionByName_DatabaseError(t *testing.T) {
	ctx := context.Background()
	repo := setupRegionTestRepo(t)

	sqldb, err := repo.db.GORM().DB()
	require.NoError(t, err)
	require.NoError(t, sqldb.Close())

	got, err := repo.GetRegionByName(ctx, "us-west1")
	require.Error(t, err)
	assert.Nil(t, got)

	var customErr *vsaerrors.CustomError
	require.True(t, errors.As(err, &customErr))
	assert.True(t, customErr.IsError(vsaerrors.ErrDatabaseDataReadError))
}
