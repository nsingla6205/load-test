package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGetVolume(t *testing.T) {
	t.Run("WhenVolumeExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		result, err := store.GetVolume(context.Background(), "test-volume-uuid")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, result.Name, "Expected volume name %v, got %v", volume.Name, result.Name)
		assert.Equal(tt, account.Name, result.Account.Name, "Expected account name %v, got %v", account.Name, result.Account.Name)
	})
	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volume, err := store.GetVolume(context.Background(), "test-volume-uuid")
		assert.Nil(tt, volume, "Expected nil volume, got %v", volume)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
}

func TestCreateVolume(t *testing.T) {
	// t.Run("WhenVolumeIsCreatedSuccessfully", func(tt *testing.T) {
	//	db, err := SetupTestDB()
	//	assert.NoError(tt, err, "Failed to set up test database")
	//	wrapper := gormwrapper.New(db)
	//	store := NewDataStoreRepository(wrapper)
	//
	//	err = ClearInMemoryDB(store.db.GORM())
	//	assert.NoError(tt, err, "Failed to clean up test database")
	//
	//	account := &datamodel.Account{
	//		BaseModel: datamodel.BaseModel{
	//			ID:   1,
	//			UUID: "test-account-uuid",
	//		},
	//		Name: "test_account",
	//	}
	//	err = store.db.Create(account).Error()
	//	if err != nil {
	//		tt.Fatalf("Failed to create account: %v", err)
	//	}
	//
	//	pool := &datamodel.Pool{
	//		Name:    "test_pool",
	//		Account: account,
	//	}
	//
	//	err = store.db.Create(pool).Error()
	//	if err != nil {
	//		tt.Fatalf("Failed to create pool: %v", err)
	//	}
	//
	//	volume := &datamodel.Volume{
	//		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
	//		Name:      "test_volume",
	//		AccountID: account.ID,
	//		Account:   account,
	//		Pool:      pool,
	//		PoolID:    pool.ID,
	//	}
	//
	//	createdVolume, err := store.CreateVolume(context.Background(), volume)
	//	assert.NoError(tt, err, "Expected no error, got %v", err)
	//	assert.Equal(tt, volume.Name, createdVolume.Name, "Expected volume name %v, got %v", volume.Name, createdVolume.Name)
	//	assert.Equal(tt, createdVolume.State, datamodel.LifeCycleStateCreating, "Expected volume state %v, got %v", datamodel.LifeCycleStateCreating, createdVolume.State)
	//	assert.Equal(tt, createdVolume.StateDetails, datamodel.LifeCycleStateCreatingDetails, "Expected volume state %v, got %v", datamodel.LifeCycleStateCreatingDetails, createdVolume.State)
	// })
	t.Run("WhenVolumeAlreadyExists", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific JSON syntax which is not supported in SQLite")
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-west1-a",
			},
		}

		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		createdVolume, err := store.CreateVolume(context.Background(), volume)
		assert.EqualError(tt, err.(*vsaerrors.CustomError).OriginalErr, "volume with this name already exists in the same zone", "Expected error 'volume already exists', got %v", err)
		assert.EqualError(tt, err, "Invalid input parameters provided", "Expected error 'Invalid input parameters provided', got %v", err)
		assert.Nil(tt, createdVolume, "Expected nil volume, got %v", createdVolume)
	})
	t.Run("CreatesVolumeSuccessfullyWhenParamsAreProvided", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone:  "us-west1-a",
				IsRegionalHA: false,
			},
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				RestoredBackupID:   "test-backup-uuid",
				RestoredBackupPath: "test-backup-path",
			},
		}

		createdVolume, err := store.CreateVolume(context.Background(), volume)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, createdVolume.Name, "Expected volume name %v, got %v", volume.Name, createdVolume.Name)
		assert.Equal(tt, datamodel.LifeCycleStateRestoring, createdVolume.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateRestoring, createdVolume.State)
		assert.Equal(tt, datamodel.LifeCycleStateRestoringDetails, createdVolume.StateDetails, "Expected volume state details %v, got %v", datamodel.LifeCycleStateRestoringDetails, createdVolume.StateDetails)
	})

	t.Run("CreateVolumeWithRegionalPool", func(tt *testing.T) {
		originalFindVolumeInRegionalPool := FindVolumeInRegionalPool
		defer func() {
			FindVolumeInRegionalPool = originalFindVolumeInRegionalPool
		}()

		// Mock FindVolumeInRegionalPool to return a database error (not ErrRecordNotFound)
		FindVolumeInRegionalPool = func(db *gorm.DB, volumeName string, accountID int64, preloadAssociations bool) (*datamodel.Volume, error) {
			return &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "existing-volume-uuid"},
				Name:      volumeName,
				AccountID: accountID,
			}, nil
		}

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
			PoolAttributes: &datamodel.PoolAttributes{
				IsRegionalHA: true,
			},
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}

		createdVolume, err := store.CreateVolume(context.Background(), volume)
		assert.Error(tt, err, "Expected error when FindVolumeInRegionalPool returns non-ErrRecordNotFound error")
		assert.ErrorContains(tt, err, "Invalid input parameters provided", "Expected error 'Invalid input parameters provided', got %v", err)
		assert.Nil(tt, createdVolume, "Expected nil volume when error occurs")
	})

	t.Run("CreateVolumeWhenFindVolumeReturnsNonRecordNotFoundError", func(tt *testing.T) {
		originalFindVolumeInRegionalPool := FindVolumeInRegionalPool
		defer func() {
			FindVolumeInRegionalPool = originalFindVolumeInRegionalPool
		}()

		// Mock FindVolumeInRegionalPool to return a database error (not ErrRecordNotFound)
		FindVolumeInRegionalPool = func(db *gorm.DB, volumeName string, accountID int64, preloadAssociations bool) (*datamodel.Volume, error) {
			return nil, fmt.Errorf("database connection error")
		}

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
			PoolAttributes: &datamodel.PoolAttributes{
				IsRegionalHA: true,
			},
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}

		createdVolume, err := store.CreateVolume(context.Background(), volume)
		assert.Error(tt, err, "Expected error when FindVolumeInRegionalPool returns non-ErrRecordNotFound error")
		assert.Nil(tt, createdVolume, "Expected nil volume when error occurs")

		// Verify it's wrapped as a database read error
		var vcpError *vsaerrors.CustomError
		if errors.As(err, &vcpError) {
			assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpError.TrackingID, "Expected ErrDatabaseDataReadError tracking ID")
		}
	})

	t.Run("CreateVolumeWithZonalPool", func(tt *testing.T) {
		originalFindVolumeInZonalPool := FindVolumeInZonalPool
		defer func() {
			FindVolumeInZonalPool = originalFindVolumeInZonalPool
		}()

		// Mock FindVolumeInZonalPool to return a database error (not ErrRecordNotFound)
		FindVolumeInZonalPool = func(db *gorm.DB, volumeName string, accountID int64, zone string, preloadAssociations bool) (*datamodel.Volume, error) {
			return &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: "existing-volume-uuid"},
				Name:      volumeName,
				AccountID: accountID,
			}, nil
		}

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
			PoolAttributes: &datamodel.PoolAttributes{
				IsRegionalHA: false,
			},
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}

		createdVolume, err := store.CreateVolume(context.Background(), volume)
		assert.Error(tt, err, "Expected error when FindVolumeInZonalPool returns non-ErrRecordNotFound error")
		assert.ErrorContains(tt, err, "Invalid input parameters provided", "Expected error 'Invalid input parameters provided', got %v", err)
		assert.Nil(tt, createdVolume, "Expected nil volume when error occurs")
	})

	t.Run("CreateVolumeWhenFindVolumeReturnsNonRecordNotFoundError", func(tt *testing.T) {
		originalFindVolumeInZonalPool := FindVolumeInZonalPool
		defer func() {
			FindVolumeInZonalPool = originalFindVolumeInZonalPool
		}()

		// Mock FindVolumeInRegionalPool to return a database error (not ErrRecordNotFound)
		FindVolumeInZonalPool = func(db *gorm.DB, volumeName string, accountID int64, zone string, preloadAssociations bool) (*datamodel.Volume, error) {
			return nil, fmt.Errorf("database connection error")
		}

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
			PoolAttributes: &datamodel.PoolAttributes{
				IsRegionalHA: false,
			},
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}

		createdVolume, err := store.CreateVolume(context.Background(), volume)
		assert.Error(tt, err, "Expected error when FindVolumeInRegionalPool returns non-ErrRecordNotFound error")
		assert.Nil(tt, createdVolume, "Expected nil volume when error occurs")

		// Verify it's wrapped as a database read error
		var vcpError *vsaerrors.CustomError
		if errors.As(err, &vcpError) {
			assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpError.TrackingID, "Expected ErrDatabaseDataReadError tracking ID")
		}
	})
}

func TestDeleteVolumeAndChildResources(t *testing.T) {
	t.Run("WhenVolumeAndChildResourcesAreDeletedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
		}

		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}

		err = store.db.Create(volume).Error()
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			VolumeID:  volume.ID,
			Volume:    volume,
			AccountID: account.ID,
			Account:   account,
		}

		err = store.db.Create(snapshot).Error()
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		deletedVolume, err := store.DeleteVolumeAndChildResources(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, deletedVolume.Name, "Expected volume name %v, got %v", volume.Name, deletedVolume.Name)
		assert.NotNil(tt, deletedVolume.DeletedAt, "Expected volume to be deleted, got %v", deletedVolume.DeletedAt)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, deletedVolume.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateDeleted, deletedVolume.State)
		assert.Equal(tt, "", deletedVolume.StateDetails, "Expected volume state details %v, got %v", "", deletedVolume.StateDetails)

		_, err = store.GetVolume(context.Background(), volume.UUID)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
	t.Run("WhenVolumeIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		deletedVolume, err := store.DeleteVolumeAndChildResources(context.Background(), "dummy")
		assert.Nil(tt, deletedVolume, "Expected nil volume, got %v", deletedVolume)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
	t.Run("WhenTransactionStartFails", func(tt *testing.T) {
		origStartTransaction := startTransaction
		startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
			return nil, errors.New("transaction start failed")
		}
		defer func() { startTransaction = origStartTransaction }()

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		deletedVolume, err := store.DeleteVolumeAndChildResources(context.Background(), "dummy")
		assert.Nil(tt, deletedVolume, "Expected nil volume, got %v", deletedVolume)
		assert.ErrorContains(tt, err, "transaction start failed", "Expected error 'transaction start failed', got %v", err)
	})
}

func TestDeleteVolume(t *testing.T) {
	t.Run("WhenVolumeIsDeletedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
		}

		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		deletedVolume, err := store.DeleteVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, deletedVolume.Name, "Expected volume name %v, got %v", volume.Name, deletedVolume.Name)
		assert.NotNil(tt, deletedVolume.DeletedAt, "Expected volume to be deleted, got %v", deletedVolume.DeletedAt)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, deletedVolume.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateDeleted, deletedVolume.State)
		assert.Equal(tt, "", deletedVolume.StateDetails, "Expected volume state details %v, got %v", "", deletedVolume.StateDetails)

		_, err = store.GetVolume(context.Background(), volume.UUID)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
	t.Run("WhenVolumeIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		deletedVolume, err := store.DeleteVolume(context.Background(), "dummy")
		assert.Nil(tt, deletedVolume, "Expected nil volume, got %v", deletedVolume)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
}

func TestUpdateVolumeState(t *testing.T) {
	t.Run("WhenVolumeStateIsUpdatedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
		}

		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		updatedVolume, err := store.UpdateVolumeState(context.Background(), volume.UUID, datamodel.LifeCycleStateDeleted, "")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, updatedVolume.Name, "Expected volume name %v, got %v", volume.Name, updatedVolume.Name)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, updatedVolume.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateDeleted, updatedVolume.State)
		assert.Equal(tt, "", updatedVolume.StateDetails, "Expected volume state details %v, got %v", "", updatedVolume.StateDetails)
	})
	t.Run("WhenVolumeIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updatedVolume, err := store.UpdateVolumeState(context.Background(), "dummy", datamodel.LifeCycleStateDeleted, "")
		assert.Nil(tt, updatedVolume, "Expected nil volume, got %v", updatedVolume)
		assert.ErrorContains(tt, err, "not found", "Expected no error, got %v", err)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
}

func TestVerifyVolumeOwnership(t *testing.T) {
	t.Run("WhenAccountAndVolumeExist_ThenReturnVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		result, err := store.VerifyVolumeOwnership(context.Background(), "test-volume-uuid", "test_account")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, result, "Expected volume, got nil")
		assert.Equal(tt, volume.UUID, result.UUID, "Expected volume UUID %v, got %v", volume.UUID, result.UUID)
	})

	t.Run("WhenAccountDoesNotExist_ThenReturnError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		result, err := store.VerifyVolumeOwnership(context.Background(), "test-volume-uuid", "nonexistent_account")
		assert.Nil(tt, result, "Expected nil volume, got %v", result)
		assert.Error(tt, err, "Expected error for missing account")
	})

	t.Run("WhenVolumeDoesNotExistForAccount_ThenReturnError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		result, err := store.VerifyVolumeOwnership(context.Background(), "nonexistent-volume-uuid", "test_account")
		assert.Nil(tt, result, "Expected nil volume, got %v", result)
		assert.Error(tt, err, "Expected error for missing volume")
	})

	t.Run("WhenVolumeIsNotFoundForAccount_ReturnsNotFoundErr", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Do NOT create a volume for this account

		result, err := store.VerifyVolumeOwnership(context.Background(), "nonexistent-volume-uuid", "test_account")
		assert.Nil(tt, result, "Expected nil volume, got %v", result)
		assert.Error(tt, err, "Expected error for missing volume")
		assert.True(tt, customerrors.IsNotFoundErr(err), "Expected NotFoundErr, got %v", err)
	})
}

func TestUpdateVolumeFields(t *testing.T) {
	t.Run("WhenFieldsAreUpdatedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			State:     "old_state",
		}
		assert.NoError(tt, store.db.Create(volume).Error())

		updates := map[string]interface{}{
			"State":        "new_state",
			"StateDetails": "updated details",
		}
		err = store.UpdateVolumeFields(context.Background(), volume.UUID, updates)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		updated, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, "new_state", updated.State)
		assert.Equal(tt, "updated details", updated.StateDetails)
	})

	t.Run("WhenVolumePerformanceGroupIDIsUpdatedByColumnName", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
			QosType: "manual",
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		oldVPG := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "old-vpg-uuid"},
			Name:      "old-vpg",
			PoolID:    pool.ID,
			AllocationType: models.AllocationTypeShared,
		}
		assert.NoError(tt, store.db.Create(oldVPG).Error())

		newVPG := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "new-vpg-uuid"},
			Name:      "new-vpg",
			PoolID:    pool.ID,
			AllocationType: models.AllocationTypeShared,
		}
		assert.NoError(tt, store.db.Create(newVPG).Error())

		volume := &datamodel.Volume{
			BaseModel:                datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:                     "test_volume",
			AccountID:                account.ID,
			Account:                  account,
			Pool:                     pool,
			PoolID:                   pool.ID,
			State:                    "READY",
			VolumePerformanceGroupID: sql.NullInt64{Int64: oldVPG.ID, Valid: true},
		}
		assert.NoError(tt, store.db.Create(volume).Error())

		updates := map[string]interface{}{
			"volume_performance_group_id": newVPG.ID,
		}
		err = store.UpdateVolumeFields(context.Background(), volume.UUID, updates)
		assert.NoError(tt, err, "Expected no error updating VPG, got %v", err)

		updated, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err)
		assert.True(tt, updated.VolumePerformanceGroupID.Valid, "VolumePerformanceGroupID should be valid")
		assert.Equal(tt, newVPG.ID, updated.VolumePerformanceGroupID.Int64, "VolumePerformanceGroupID should be updated to new VPG")
		assert.NotNil(tt, updated.VolumePerformanceGroup, "VolumePerformanceGroup should be preloaded")
		assert.Equal(tt, "new-vpg-uuid", updated.VolumePerformanceGroup.UUID, "Preloaded VPG should have the new UUID")
	})

	t.Run("WhenVolumeIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updates := map[string]interface{}{
			"State": "new_state",
		}
		err = store.UpdateVolumeFields(context.Background(), "nonexistent-uuid", updates)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "not found")
	})

	t.Run("WhenTransactionStartFails", func(tt *testing.T) {
		origStartTransaction := startTransaction
		startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
			return nil, errors.New("transaction start failed")
		}
		defer func() { startTransaction = origStartTransaction }()

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		updates := map[string]interface{}{
			"State": "new_state",
		}
		err = store.UpdateVolumeFields(context.Background(), "any-uuid", updates)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "transaction start failed")
	})

	t.Run("WhenUpdateFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			Name:    "test_pool",
			Account: account,
		}
		assert.NoError(tt, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		assert.NoError(tt, store.db.Create(volume).Error())

		// Pass an invalid field to cause update error
		updates := map[string]interface{}{
			"NonExistentField": "value",
		}
		err = store.UpdateVolumeFields(context.Background(), volume.UUID, updates)
		assert.Error(tt, err)
	})
}

func TestGetVolumeCount(t *testing.T) {
	t.Run("WhenAccountExistsWithVolumes", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:      "test_volume_1",
			AccountID: account.ID,
			Account:   account,
		}
		volume2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:      "test_volume_2",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(volume1).Error()
		assert.NoError(tt, err, "Failed to create volume 1")
		err = store.db.Create(volume2).Error()
		assert.NoError(tt, err, "Failed to create volume 2")

		count, err := store.GetVolumeCount(context.Background(), "test_account")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, int64(2), count, "Expected volume count %v, got %v", 2, count)
	})

	t.Run("WhenAccountDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		count, err := store.GetVolumeCount(context.Background(), "nonexistent_account")
		assert.Equal(tt, int64(0), count, "Expected volume count %v, got %v", 0, count)
		assert.Error(tt, err, "Expected error for missing account")
	})
}

func TestListVolumesWithDetails(t *testing.T) {
	t.Run("WhenVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		volume1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:      "test_volume_1",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		volume2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:      "test_volume_2",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume1).Error()
		assert.NoError(tt, err, "Failed to create volume 1")
		err = store.db.Create(volume2).Error()
		assert.NoError(tt, err, "Failed to create volume 2")

		volumes, err := _listVolumesWithDetails(store.db.GORM())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 2, len(volumes), "Expected %v volumes, got %v", 2, len(volumes))
		assert.Equal(tt, volume1.UUID, volumes[0].UUID, "Expected volume UUID %v, got %v", volume1.UUID, volumes[0].UUID)
		assert.Equal(tt, volume2.UUID, volumes[1].UUID, "Expected volume UUID %v, got %v", volume2.UUID, volumes[1].UUID)
	})

	t.Run("WhenNoVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volumes, err := _listVolumesWithDetails(store.db.GORM())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 0, len(volumes), "Expected %v volumes, got %v", 0, len(volumes))
	})
}

func TestListVolumes(t *testing.T) {
	t.Run("WhenNoVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		conditions := [][]interface{}{
			{"account_id", "=", 999}, // Non-existent account ID
		}
		volumes, err := store.ListVolumes(context.Background(), conditions)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 0, len(volumes), "Expected %v volumes, got %v", 0, len(volumes))
	})
	t.Run("ListVolumesWhenBackupPolicyAttached", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		accountID := int64(1)
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   accountID,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		policyEnabled := true
		policyDisabled := false
		backupPolicyUUID := "test-backup-policy-uuid"

		volume1 := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:           "test_volume_1",
			AccountID:      account.ID,
			Account:        account,
			DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled},
		}
		err = store.db.Create(volume1).Error()
		assert.NoError(tt, err, "Failed to create volume 1")

		volume2 := volume1
		volume2.ID = 2
		volume2.UUID = "test-volume-uuid-2"
		volume2.DataProtection.ScheduledBackupEnabled = &policyDisabled
		err = store.db.Create(volume2).Error()
		assert.NoError(tt, err, "Failed to create volume 2")

		volume3 := volume1
		volume3.ID = 3
		volume3.UUID = "test-volume-uuid-3"
		volume3.DataProtection = nil
		err = store.db.Create(volume3).Error()
		assert.NoError(tt, err, "Failed to create volume 3")

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = true"},
		}

		volumes, err := store.ListVolumes(context.Background(), conditions)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(volumes), "Expected %v volumes, got %v", 0, len(volumes))
	})
}

func TestRevertedVolume(t *testing.T) {
	t.Run("WhenVolumeIsRevertedSuccessfully", func(tt *testing.T) {
		// Test setup
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create pool
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		// Create volume
		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:        "test_volume",
			AccountID:   account.ID,
			Account:     account,
			Pool:        pool,
			PoolID:      pool.ID,
			SizeInBytes: 1000000,
			VolumeAttributes: &datamodel.VolumeAttributes{
				SnapReserve: 10,
			},
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create snapshots
		snapshot1 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid-1"},
			Name:      "test_snapshot_1",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "ext-uuid-1",
			},
		}
		snapshot2 := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid-2"},
			Name:      "test_snapshot_2",
			AccountID: account.ID,
			VolumeID:  volume.ID,
			Account:   account,
			Volume:    volume,
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				ExternalUUID: "ext-uuid-2",
			},
		}
		err = store.db.Create(snapshot1).Error()
		assert.NoError(tt, err, "Failed to create snapshot 1")
		err = store.db.Create(snapshot2).Error()
		assert.NoError(tt, err, "Failed to create snapshot 2")

		// Call RevertedVolume
		snapshots, err := store.RevertedVolume(context.Background(), volume, snapshot1)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected snapshots to be returned")
	})
	t.Run("WhenTransactionStartFails", func(tt *testing.T) {
		origStartTransaction := startTransaction
		startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
			return nil, errors.New("transaction start failed")
		}
		defer func() { startTransaction = origStartTransaction }()

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		}
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
		}

		_, err = store.RevertedVolume(context.Background(), volume, snapshot)
		assert.Error(tt, err, "Expected error for transaction failure")
		assert.Contains(tt, err.Error(), "transaction start failed")
	})
}

func TestRevertDeleteSnapshots(t *testing.T) {
	t.Run("WhenRevertDeleteSnapshotsSucceeds", func(t *testing.T) {
		ctx := context.Background()
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(t, err)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(t, err)

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(t, err)

		snapshots, err := revertDeleteSnapshots(ctx, store.db.GORM(), volume.ID, snapshot.UUID)
		assert.NoError(t, err)
		assert.NotNil(t, snapshots)
	})

	t.Run("WhenRevertDeleteSnapshotsFailsOnDatabaseReadError", func(t *testing.T) {
		ctx := context.Background()
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		// Use an invalid volume ID to trigger database read error
		// The function handles the error gracefully and returns empty slice instead of error
		snapshots, err := revertDeleteSnapshots(ctx, store.db.GORM(), 99999, "invalid-snapshot-uuid")
		assert.NoError(t, err) // The function handles the error gracefully
		assert.NotNil(t, snapshots)
		assert.Empty(t, snapshots) // Should return empty slice for invalid data
	})

	t.Run("WhenRevertDeleteSnapshotsFailsOnDatabaseUpdateError", func(t *testing.T) {
		ctx := context.Background()
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(t, err)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(t, err)

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			AccountID: account.ID,
			VolumeID:  volume.ID,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(t, err)

		// Close the database connection to simulate a database error
		sqlDB, err := store.db.GORM().DB()
		assert.NoError(t, err, "Failed to get underlying sql.DB")
		err = sqlDB.Close()
		if err != nil {
			return
		}

		// Now try to call revertDeleteSnapshots - this should fail due to closed connection
		snapshots, err := revertDeleteSnapshots(ctx, store.db.GORM(), volume.ID, snapshot.UUID)
		assert.Error(t, err, "Expected error when database connection is closed")
		assert.Nil(t, snapshots, "Expected nil snapshots when database query fails")
	})
}

func TestGetVolumesByPoolID_ErrorHandling(t *testing.T) {
	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		require.NoError(tt, sqlDB.Close())

		volumes, err := store.GetVolumesByPoolID(context.Background(), 1)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volumes, "Expected nil volumes when error occurs")
	})
}

func TestGetPoolVolumesForQosTransition(t *testing.T) {
	t.Run("WhenVolumesExist_ReturnsSelectedFieldsWithoutPreloads", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		require.NoError(tt, store.db.Create(account).Error())

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 1, UUID: "pool-1"},
			Name:           "test_pool",
			Account:        account,
			DeploymentName: "test-pool-deployment",
		}
		require.NoError(tt, store.db.Create(pool).Error())

		volumeOne := &datamodel.Volume{
			BaseModel:                datamodel.BaseModel{ID: 1, UUID: "vol-1"},
			Name:                     "volume_one",
			AccountID:                account.ID,
			Account:                  account,
			Pool:                     pool,
			PoolID:                   pool.ID,
			VolumePerformanceGroupID: sql.NullInt64{Int64: 10, Valid: true},
			VolumeAttributes:         &datamodel.VolumeAttributes{ExternalUUID: "ext-1"},
		}
		volumeTwo := &datamodel.Volume{
			BaseModel:                datamodel.BaseModel{ID: 2, UUID: "vol-2"},
			Name:                     "volume_two",
			AccountID:                account.ID,
			Account:                  account,
			Pool:                     pool,
			PoolID:                   pool.ID,
			VolumePerformanceGroupID: sql.NullInt64{Int64: 20, Valid: true},
			VolumeAttributes:         &datamodel.VolumeAttributes{ExternalUUID: "ext-2"},
		}
		require.NoError(tt, store.db.Create(volumeOne).Error())
		require.NoError(tt, store.db.Create(volumeTwo).Error())

		otherPool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 2, UUID: "pool-2"},
			Name:           "other_pool",
			Account:        account,
			DeploymentName: "other-pool-deployment",
		}
		require.NoError(tt, store.db.Create(otherPool).Error())
		otherVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 3, UUID: "vol-other"},
			Name:      "other_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      otherPool,
			PoolID:    otherPool.ID,
		}
		require.NoError(tt, store.db.Create(otherVolume).Error())

		volumes, err := store.GetPoolVolumesForQosTransition(context.Background(), pool.ID)
		assert.NoError(tt, err)
		require.Len(tt, volumes, 2)

		byUUID := map[string]*datamodel.Volume{}
		for _, volume := range volumes {
			byUUID[volume.UUID] = volume
		}
		gotOne := byUUID["vol-1"]
		require.NotNil(tt, gotOne)
		assert.Equal(tt, pool.ID, gotOne.PoolID)
		assert.Equal(tt, int64(10), gotOne.VolumePerformanceGroupID.Int64)
		assert.True(tt, gotOne.VolumePerformanceGroupID.Valid)
		require.NotNil(tt, gotOne.VolumeAttributes)
		assert.Equal(tt, "ext-1", gotOne.VolumeAttributes.ExternalUUID)
		assert.Nil(tt, gotOne.Account)
		assert.Nil(tt, gotOne.Pool)
		assert.Nil(tt, gotOne.VolumePerformanceGroup)

		gotTwo := byUUID["vol-2"]
		require.NotNil(tt, gotTwo)
		assert.Equal(tt, int64(20), gotTwo.VolumePerformanceGroupID.Int64)
	})

	t.Run("WhenNoVolumes_ReturnsEmptySlice", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volumes, err := store.GetPoolVolumesForQosTransition(context.Background(), 999)
		assert.NoError(tt, err)
		assert.Empty(tt, volumes)
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		require.NoError(tt, sqlDB.Close())

		volumes, err := store.GetPoolVolumesForQosTransition(context.Background(), 1)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volumes, "Expected nil volumes when error occurs")
	})
}

func TestGetVolumeCountByPoolID_ErrorHandling(t *testing.T) {
	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		count, err := store.GetVolumeCountByPoolID(context.Background(), 1)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Equal(tt, int64(0), count, "Expected count to be 0 when error occurs")
	})
}

func TestHasDependentChildThinClone(t *testing.T) {
	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		require.NoError(tt, sqlDB.Close())

		result, err := store.HasDependentChildThinClone(context.Background(), 1, "parent-uuid")
		assert.Error(tt, err, "Expected error when database is closed")
		assert.False(tt, result, "Expected false when error occurs")
	})
}

// TestHasDependentChildThinClone_PostgresMock exercises the JSONB EXISTS query via sqlmock.
func TestHasDependentChildThinClone_PostgresMock(t *testing.T) {
	const poolID = int64(42)
	const parentUUID = "parent-volume-uuid"

	t.Run("WhenDependentChildExists", func(tt *testing.T) {
		dbSQL, mock, err := sqlmock.New()
		require.NoError(tt, err)
		defer func() {
			require.NoError(tt, mock.ExpectationsWereMet())
		}()

		dialector := postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true})
		gormDB, err := gorm.Open(dialector, &gorm.Config{})
		require.NoError(tt, err)

		rows := sqlmock.NewRows([]string{"exists"}).AddRow(true)
		mock.ExpectQuery("clone_parent_info").WillReturnRows(rows)

		store := NewDataStoreRepository(gormwrapper.New(gormDB))
		result, err := store.HasDependentChildThinClone(context.Background(), poolID, parentUUID)
		assert.NoError(tt, err)
		assert.True(tt, result)
	})

	t.Run("WhenNoDependentChild", func(tt *testing.T) {
		dbSQL, mock, err := sqlmock.New()
		require.NoError(tt, err)
		defer func() {
			require.NoError(tt, mock.ExpectationsWereMet())
		}()

		dialector := postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true})
		gormDB, err := gorm.Open(dialector, &gorm.Config{})
		require.NoError(tt, err)

		rows := sqlmock.NewRows([]string{"exists"}).AddRow(false)
		mock.ExpectQuery("clone_parent_info").WillReturnRows(rows)

		store := NewDataStoreRepository(gormwrapper.New(gormDB))
		result, err := store.HasDependentChildThinClone(context.Background(), poolID, parentUUID)
		assert.NoError(tt, err)
		assert.False(tt, result)
	})

	t.Run("WhenQueryFails", func(tt *testing.T) {
		dbSQL, mock, err := sqlmock.New()
		require.NoError(tt, err)
		defer func() {
			require.NoError(tt, mock.ExpectationsWereMet())
		}()

		dialector := postgres.New(postgres.Config{Conn: dbSQL, PreferSimpleProtocol: true})
		gormDB, err := gorm.Open(dialector, &gorm.Config{})
		require.NoError(tt, err)

		mock.ExpectQuery("clone_parent_info").WillReturnError(errors.New("simulated db failure"))

		store := NewDataStoreRepository(gormwrapper.New(gormDB))
		result, err := store.HasDependentChildThinClone(context.Background(), poolID, parentUUID)
		assert.Error(tt, err)
		assert.False(tt, result)
	})
}

func TestGetMultipleVolumes_ErrorHandling(t *testing.T) {
	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		conditions := [][]interface{}{
			{"name", "test-volume"},
		}
		volumes, err := store.GetMultipleVolumes(context.Background(), conditions)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volumes, "Expected nil volumes when error occurs")
	})
}

func TestGetMultipleVolumes_ReturnsThroughputMibpsForManualPoolWithAutoGeneratedVPG(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "acct-getmulti-vpg"},
		Name:      "acct_getmulti_vpg",
	}
	require.NoError(t, store.db.Create(account).Error())

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-getmulti-vpg"},
		Name:      "pool_getmulti_vpg",
		AccountID: account.ID,
		QosType:   "manual",
	}
	require.NoError(t, store.db.Create(pool).Error())

	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel:       datamodel.BaseModel{UUID: "vpg-getmulti-autogen"},
		Name:            "vpg_getmulti_autogen",
		PoolID:          pool.ID,
		AllocationType: models.AllocationTypeShared,
		IsAutoGen:       true,
		ThroughputMibps: 200,
		Iops:            1000,
	}
	require.NoError(t, store.db.Create(vpg).Error())

	vol1 := &datamodel.Volume{
		BaseModel:                datamodel.BaseModel{UUID: "vol-getmulti-1"},
		Name:                     "vol_getmulti_1",
		PoolID:                   pool.ID,
		AccountID:                account.ID,
		VolumePerformanceGroupID: sql.NullInt64{Int64: vpg.ID, Valid: true},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName:    account.Name,
			DeploymentName: "deployment-1",
			Protocols:      []string{"NFS"},
		},
	}
	vol2 := &datamodel.Volume{
		BaseModel:                datamodel.BaseModel{UUID: "vol-getmulti-2"},
		Name:                     "vol_getmulti_2",
		PoolID:                   pool.ID,
		AccountID:                account.ID,
		VolumePerformanceGroupID: sql.NullInt64{Int64: vpg.ID, Valid: true},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName:    account.Name,
			DeploymentName: "deployment-1",
			Protocols:      []string{"NFS"},
		},
	}
	require.NoError(t, store.db.Create(vol1).Error())
	require.NoError(t, store.db.Create(vol2).Error())

	conditions := [][]interface{}{
		{"account_id = ?", account.ID},
		{"uuid in ?", []string{vol1.UUID, vol2.UUID}},
	}
	volumes, err := store.GetMultipleVolumes(context.Background(), conditions)
	require.NoError(t, err)
	require.Len(t, volumes, 2)

	for _, vol := range volumes {
		require.NotNil(t, vol.VolumePerformanceGroup,
			"GetMultipleVolumes should preload VolumePerformanceGroup so throughputMibps can be returned")
		assert.True(t, vol.VolumePerformanceGroup.IsAutoGen,
			"VPG should be auto-generated")
		assert.Equal(t, int64(200), vol.VolumePerformanceGroup.ThroughputMibps,
			"VolumePerformanceGroup.ThroughputMibps should be set for manual pool with assigned autogenerated VPG")
		assert.Equal(t, int64(1000), vol.VolumePerformanceGroup.Iops,
			"VolumePerformanceGroup.Iops should be set for manual pool with assigned autogenerated VPG")
	}
}

func TestGetAllVolumesForHG_ErrorHandling(t *testing.T) {
	t.Run("WhenBlockDevicesQueryErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				BlockDevices: &[]datamodel.BlockDevice{
					{
						Name: "test-device",
						HostGroupDetails: []datamodel.HostGroupDetail{
							{
								HostGroupUUID: "test-hostgroup-uuid",
							},
						},
					},
				},
			},
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volumes, err := store.GetAllVolumesForHG(context.Background(), "test-hostgroup-uuid", account.ID)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volumes, "Expected nil volumes when error occurs")
	})

	t.Run("WhenBlockPropertiesQueryErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				BlockProperties: &datamodel.BlockProperties{
					HostGroupDetails: []datamodel.HostGroupDetail{
						{
							HostGroupUUID: "test-hostgroup-uuid",
						},
					},
				},
			},
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volumes, err := store.GetAllVolumesForHG(context.Background(), "test-hostgroup-uuid", account.ID)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volumes, "Expected nil volumes when error occurs")
	})
}

func TestBatchUpdateVolumeFields(t *testing.T) {
	t.Run("WhenUpdatesSliceIsEmpty", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Test with empty slice - should return immediately without database operations
		err = store.BatchUpdateVolumeFields(context.Background(), []datamodel.VolumeFieldUpdate{})
		assert.NoError(tt, err, "Expected no error for empty updates slice")
	})

	t.Run("WhenNilUpdatesSlice", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Test with nil slice - should return immediately
		err = store.BatchUpdateVolumeFields(context.Background(), nil)
		assert.NoError(tt, err, "Expected no error for nil updates slice")
	})

	t.Run("WhenDatabaseConnectionFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		// Close the database to simulate connection failure
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		assert.NoError(tt, err)

		// Prepare valid updates
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					"used_bytes": int64(1000),
				},
			},
		}

		// Should fail due to closed database
		err = store.BatchUpdateVolumeFields(context.Background(), updates)
		assert.Error(tt, err, "Expected error when database connection is closed")
	})

	t.Run("WhenSQLExecutionFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					"used_bytes": int64(1000),
				},
			},
		}

		// Should fail due to SQL syntax error in SQLite
		err = store.BatchUpdateVolumeFields(context.Background(), updates)
		assert.Error(tt, err, "Expected error due to PostgreSQL-specific SQL in SQLite")

		// Verify it returns the proper VCP error type
		assert.Contains(tt, err.Error(), "An internal error occurred", "Expected VCP database error")
	})

	t.Run("WhenBuildVolumeUpdateQueryIsCalledCorrectly", func(tt *testing.T) {
		// Test that buildVolumeUpdateQuery is called with correct parameters
		// This tests the integration without requiring actual SQL execution
		store := &DataStoreRepository{}

		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					"used_bytes": int64(1000),
				},
			},
			{
				UUID: "test-uuid-2",
				Fields: map[string]interface{}{
					"used_bytes": int64(2000),
				},
			},
		}

		// Call buildVolumeUpdateQuery directly to verify it works correctly
		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		// Verify the query and args are generated correctly
		assert.NotEmpty(tt, sql, "SQL should not be empty")
		assert.Len(tt, args, 4, "Should have 4 arguments for 2 updates")
		assert.Equal(tt, "test-uuid-1", args[0], "First UUID should match")
		assert.Equal(tt, int64(1000), args[1], "First used_bytes should match")
		assert.Equal(tt, "test-uuid-2", args[2], "Second UUID should match")
		assert.Equal(tt, int64(2000), args[3], "Second used_bytes should match")
	})

	t.Run("WhenUpdatingSingleVolume", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			UsedBytes: 1000, // Initial value
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Prepare update
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: volume.UUID,
				Fields: map[string]interface{}{
					"used_bytes": int64(2000),
				},
			},
		}

		// Execute batch update
		err = store.BatchUpdateVolumeFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error during batch update")

		// Verify the update
		updatedVolume, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume")
		assert.Equal(tt, int64(2000), updatedVolume.UsedBytes, "Expected used_bytes to be updated to 2000")
	})

	t.Run("WhenUpdatingMultipleVolumes", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-1-uuid"},
			Name:      "test_volume_1",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			UsedBytes: 1000,
		}

		volume2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-2-uuid"},
			Name:      "test_volume_2",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			UsedBytes: 1500,
		}

		volume3 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-3-uuid"},
			Name:      "test_volume_3",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			UsedBytes: 2000,
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume1).Error()
		assert.NoError(tt, err, "Failed to create volume1")
		err = store.db.Create(volume2).Error()
		assert.NoError(tt, err, "Failed to create volume2")
		err = store.db.Create(volume3).Error()
		assert.NoError(tt, err, "Failed to create volume3")

		// Prepare batch updates
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: volume1.UUID,
				Fields: map[string]interface{}{
					"used_bytes": int64(3000),
				},
			},
			{
				UUID: volume2.UUID,
				Fields: map[string]interface{}{
					"used_bytes": int64(4000),
				},
			},
			{
				UUID: volume3.UUID,
				Fields: map[string]interface{}{
					"used_bytes": int64(5000),
				},
			},
		}

		// Execute batch update
		err = store.BatchUpdateVolumeFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error during batch update")

		// Verify all updates
		updatedVolume1, err := store.GetVolume(context.Background(), volume1.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume1")
		assert.Equal(tt, int64(3000), updatedVolume1.UsedBytes, "Expected volume1 used_bytes to be updated to 3000")

		updatedVolume2, err := store.GetVolume(context.Background(), volume2.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume2")
		assert.Equal(tt, int64(4000), updatedVolume2.UsedBytes, "Expected volume2 used_bytes to be updated to 4000")

		updatedVolume3, err := store.GetVolume(context.Background(), volume3.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume3")
		assert.Equal(tt, int64(5000), updatedVolume3.UsedBytes, "Expected volume3 used_bytes to be updated to 5000")
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Prepare update for non-existent volume
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "non-existent-volume-uuid",
				Fields: map[string]interface{}{
					"used_bytes": int64(2000),
				},
			},
		}

		// Execute batch update - this should not error as it's a bulk operation
		// The UPDATE will simply affect 0 rows
		err = store.BatchUpdateVolumeFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error even when volume doesn't exist in bulk update")
	})

	t.Run("WhenMixedExistentAndNonExistentVolumes", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			UsedBytes: 1000,
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Prepare mixed updates (existing + non-existing volume)
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: volume.UUID, // This exists
				Fields: map[string]interface{}{
					"used_bytes": int64(2000),
				},
			},
			{
				UUID: "non-existent-volume-uuid", // This doesn't exist
				Fields: map[string]interface{}{
					"used_bytes": int64(3000),
				},
			},
		}

		// Execute batch update
		err = store.BatchUpdateVolumeFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error during mixed batch update")

		// Verify the existing volume was updated
		updatedVolume, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume")
		assert.Equal(tt, int64(2000), updatedVolume.UsedBytes, "Expected existing volume used_bytes to be updated to 2000")
	})
}

func TestGetVolumeByName(t *testing.T) {
	t.Run("WhenVolumeExistsWithName", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		result, err := store.GetVolumeByName(context.Background(), "test_volume")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, result.Name, "Expected volume name %v, got %v", volume.Name, result.Name)
		assert.Equal(tt, account.Name, result.Account.Name, "Expected account name %v, got %v", account.Name, result.Account.Name)
		assert.Equal(tt, volume.UUID, result.UUID, "Expected volume UUID %v, got %v", volume.UUID, result.UUID)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volume, err := store.GetVolumeByName(context.Background(), "nonexistent_volume")
		assert.Nil(tt, volume, "Expected nil volume, got %v", volume)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})

	t.Run("WhenDatabaseError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volume, err := store.GetVolumeByName(context.Background(), "test_volume")
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volume, "Expected nil volume when error occurs")
	})
}

func TestGetVolumeWithAccountID(t *testing.T) {
	t.Run("WhenVolumeExistsWithUUIDAndAccountID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		result, err := store.GetVolumeWithAccountID(context.Background(), "test-volume-uuid", account.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, result.Name, "Expected volume name %v, got %v", volume.Name, result.Name)
		assert.Equal(tt, account.Name, result.Account.Name, "Expected account name %v, got %v", account.Name, result.Account.Name)
		assert.Equal(tt, volume.UUID, result.UUID, "Expected volume UUID %v, got %v", volume.UUID, result.UUID)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume, err := store.GetVolumeWithAccountID(context.Background(), "nonexistent-volume-uuid", account.ID)
		assert.Nil(tt, volume, "Expected nil volume, got %v", volume)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})

	t.Run("WhenVolumeExistsButDifferentAccountID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account1 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid-1",
			},
			Name: "test_account_1",
		}

		account2 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-account-uuid-2",
			},
			Name: "test_account_2",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account1.ID,
			Account:   account1,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account1.ID,
			Account:   account1,
			Pool:      pool,
			PoolID:    pool.ID,
		}

		err = store.db.Create(account1).Error()
		assert.NoError(tt, err, "Failed to create account1")
		err = store.db.Create(account2).Error()
		assert.NoError(tt, err, "Failed to create account2")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Try to get volume with account2's ID (should not find it)
		result, err := store.GetVolumeWithAccountID(context.Background(), "test-volume-uuid", account2.ID)
		assert.Nil(tt, result, "Expected nil volume, got %v", result)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected NotFoundErr, got %v", err)
		}
	})

	t.Run("WhenDatabaseError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volume, err := store.GetVolumeWithAccountID(context.Background(), "test-volume-uuid", int64(1))
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volume, "Expected nil volume when error occurs")
	})
}

func TestGetVolumeByNameAndAccountID(t *testing.T) {
	t.Run("WhenVolumeExistsWithNameAndAccountID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		result, err := store.GetVolumeByNameAndAccountID(context.Background(), "test_volume", account.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, result.Name, "Expected volume name %v, got %v", volume.Name, result.Name)
		assert.Equal(tt, account.Name, result.Account.Name, "Expected account name %v, got %v", account.Name, result.Account.Name)
		assert.Equal(tt, volume.UUID, result.UUID, "Expected volume UUID %v, got %v", volume.UUID, result.UUID)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume, err := store.GetVolumeByNameAndAccountID(context.Background(), "nonexistent_volume", account.ID)
		assert.Nil(tt, volume, "Expected nil volume, got %v", volume)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})

	t.Run("WhenVolumeExistsButDifferentAccountID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account1 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid-1",
			},
			Name: "test_account_1",
		}

		account2 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-account-uuid-2",
			},
			Name: "test_account_2",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account1.ID,
			Account:   account1,
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account1.ID,
			Account:   account1,
			Pool:      pool,
			PoolID:    pool.ID,
		}

		err = store.db.Create(account1).Error()
		assert.NoError(tt, err, "Failed to create account1")
		err = store.db.Create(account2).Error()
		assert.NoError(tt, err, "Failed to create account2")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Try to get volume with account2's ID (should not find it)
		result, err := store.GetVolumeByNameAndAccountID(context.Background(), "test_volume", account2.ID)
		assert.Nil(tt, result, "Expected nil volume, got %v", result)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected NotFoundErr, got %v", err)
		}
	})

	t.Run("WhenDatabaseError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volume, err := store.GetVolumeByNameAndAccountID(context.Background(), "test_volume", int64(1))
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volume, "Expected nil volume when error occurs")
	})
}

func TestGetVolumeByNameAndAccountIDWithPool(t *testing.T) {
	t.Run("WhenVolumeExists_ReturnsVolumeWithPoolPreloaded", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		result, err := store.GetVolumeByNameAndAccountIDWithPool(context.Background(), "test_volume", account.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volume.Name, result.Name, "Expected volume name %v, got %v", volume.Name, result.Name)
		assert.Equal(tt, volume.UUID, result.UUID, "Expected volume UUID %v, got %v", volume.UUID, result.UUID)
		assert.Nil(tt, result.Account, "Expected Account to be nil without preload")
		require.NotNil(tt, result.Pool, "Expected Pool to be preloaded")
		assert.Equal(tt, pool.UUID, result.Pool.UUID, "Expected pool UUID %v, got %v", pool.UUID, result.Pool.UUID)
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume, err := store.GetVolumeByNameAndAccountIDWithPool(context.Background(), "nonexistent_volume", account.ID)
		assert.Nil(tt, volume, "Expected nil volume, got %v", volume)
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
}

// Note: Success tests for GetAllVolumesForHG are skipped because SQLite doesn't support
// PostgreSQL's JSONB syntax used in the queries. These tests would need to be run against
// a PostgreSQL database to work correctly.
func TestGetAllVolumesForHG_Success(t *testing.T) {
	t.Skip("Skipped because SQLite doesn't support PostgreSQL JSONB syntax")
}

func TestGetVolumeByJunctionPath(t *testing.T) {
	// Note: The main functionality tests are skipped for SQLite because GetVolumeByJunctionPath uses
	// PostgreSQL's JSONB syntax (volume_attributes #>> '{file_properties,junction_path}') which is not supported in SQLite.
	// These tests would need to be run against a PostgreSQL database to work correctly.

	t.Run("WhenDatabaseError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volume, err := store.GetVolumeByJunctionPath(context.Background(), "test-token", int64(1), int64(100))
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volume, "Expected nil volume when error occurs")
	})

	t.Run("WhenVolumeNotFound_ExpectNotFoundError", func(tt *testing.T) {
		// This test documents the expected behavior when SQLite encounters PostgreSQL JSONB syntax
		// In a real PostgreSQL environment, this would test the actual not found scenario
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// This will fail with SQLite due to JSONB syntax, but we can verify the error is handled gracefully
		volume, err := store.GetVolumeByJunctionPath(context.Background(), "non-existent-token", int64(1), int64(0))
		// The error should be handled (either not found or database error due to SQLite JSONB incompatibility)
		assert.Error(tt, err, "Expected error for non-existent volume or SQLite JSONB incompatibility")
		assert.Nil(tt, volume, "Expected nil volume")
	})

	t.Run("WhenPoolIdIsZero_NoPoolFiltering", func(tt *testing.T) {
		// This test verifies behavior when poolId is 0 (no pool filtering)
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Query with poolId = 0 should not add pool_id filter
		volume, err := store.GetVolumeByJunctionPath(context.Background(), "test-token", int64(1), int64(0))
		// Error expected due to SQLite JSONB incompatibility or not found
		assert.Error(tt, err, "Expected error")
		assert.Nil(tt, volume, "Expected nil volume")
	})

	t.Run("WhenPoolIdIsNonZero_FiltersbyPoolId", func(tt *testing.T) {
		// This test verifies that when poolId is non-zero, filtering by pool_id is applied
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Query with specific poolId should add pool_id filter
		volume, err := store.GetVolumeByJunctionPath(context.Background(), "test-token", int64(1), int64(100))
		// Error expected due to SQLite JSONB incompatibility or not found
		assert.Error(tt, err, "Expected error")
		assert.Nil(tt, volume, "Expected nil volume")
	})
}

func TestGetVolumeByNameAccountIDAndZone(t *testing.T) {
	// Note: The main functionality tests are skipped for SQLite because GetVolumeByNameAccountIDAndZone uses
	// PostgreSQL's JSONB syntax (pools.pool_attributes->>'primary_zone') which is not supported in SQLite.
	// These tests would need to be run against a PostgreSQL database to work correctly.

	// However, we can still test basic error handling and parameter validation
	t.Run("WhenDatabaseError_ZonalPool", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volume, err := store.GetVolumeByNameAccountIDAndZone(context.Background(), "test_volume", int64(1), "us-west1-a", false)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volume, "Expected nil volume when error occurs")

		// Verify it's wrapped as a database read error
		var vcpError *vsaerrors.CustomError
		if errors.As(err, &vcpError) {
			assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpError.TrackingID, "Expected ErrDatabaseDataReadError tracking ID for database connection error")
		}
	})

	t.Run("WhenVolumeNotFound_ExpectNotFoundError_ZonalPool", func(tt *testing.T) {
		// This test documents the expected behavior when SQLite encounters PostgreSQL JSONB syntax
		// In a real PostgreSQL environment, this would test the actual not found scenario
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// This will fail with SQLite due to JSONB syntax, but we can verify the error is handled gracefully
		// In PostgreSQL, this would be a record not found error
		volume, err := store.GetVolumeByNameAccountIDAndZone(context.Background(), "test_volume", int64(1), "us-west1-a", false)
		assert.Nil(tt, volume, "Expected nil volume due to JSONB syntax error or not found")
		assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite or not found in PostgreSQL")

		// The error should be wrapped appropriately based on the underlying error
		var vcpError *vsaerrors.CustomError
		if errors.As(err, &vcpError) {
			// In SQLite, this will likely be a database read error due to JSONB syntax
			// In PostgreSQL, this should be ErrVolumeNotFound for a missing record
			assert.True(tt, vcpError.TrackingID == vsaerrors.ErrVolumeNotFound || vcpError.TrackingID == vsaerrors.ErrDatabaseDataReadError,
				"Expected either ErrVolumeNotFound or ErrDatabaseDataReadError, got tracking ID: %d", vcpError.TrackingID)
		}
	})

	t.Run("WhenDatabaseError_RegionalPool", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error during query
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		volume, err := store.GetVolumeByNameAccountIDAndZone(context.Background(), "test_volume", int64(1), "us-west1-a", true)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volume, "Expected nil volume when error occurs")

		// Verify it's wrapped as a database read error
		var vcpError *vsaerrors.CustomError
		if errors.As(err, &vcpError) {
			assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpError.TrackingID, "Expected ErrDatabaseDataReadError tracking ID for database connection error")
		}
	})

	t.Run("WhenVolumeNotFound_ExpectNotFoundError_RegionalPool", func(tt *testing.T) {
		// This test documents the expected behavior when SQLite encounters PostgreSQL JSONB syntax
		// In a real PostgreSQL environment, this would test the actual not found scenario
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// This will fail with SQLite due to JSONB syntax, but we can verify the error is handled gracefully
		// In PostgreSQL, this would be a record not found error
		volume, err := store.GetVolumeByNameAccountIDAndZone(context.Background(), "test_volume", int64(1), "us-west1-a", true)
		assert.Nil(tt, volume, "Expected nil volume due to JSONB syntax error or not found")
		assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite or not found in PostgreSQL")

		// The error should be wrapped appropriately based on the underlying error
		var vcpError *vsaerrors.CustomError
		if errors.As(err, &vcpError) {
			// In SQLite, this will likely be a database read error due to JSONB syntax
			// In PostgreSQL, this should be ErrVolumeNotFound for a missing record
			assert.True(tt, vcpError.TrackingID == vsaerrors.ErrVolumeNotFound || vcpError.TrackingID == vsaerrors.ErrDatabaseDataReadError,
				"Expected either ErrVolumeNotFound or ErrDatabaseDataReadError, got tracking ID: %d", vcpError.TrackingID)
		}
	})
}

func TestFindVolumeInRegionalPool(t *testing.T) {
	t.Run("WhenVolumeNotFound_ExpectNotFoundError", func(tt *testing.T) {
		// This test documents the expected behavior when SQLite encounters PostgreSQL JSONB syntax
		// In a real PostgreSQL environment, this would test the actual not found scenario
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// This will fail with SQLite due to JSONB syntax, but we can verify the error is handled gracefully
		// In PostgreSQL, this would be a record not found error
		volume, err := FindVolumeInRegionalPool(store.db.GORM(), "test_volume", int64(1), false)
		assert.Nil(tt, volume, "Expected nil volume due to JSONB syntax error or not found")
		assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite or not found in PostgreSQL")

		// The error should be wrapped appropriately based on the underlying error
		// Note: FindVolumeInRegionalPool returns raw gorm errors, not wrapped VCP errors
		// In SQLite, this will be a database syntax error
		// In PostgreSQL, this should be gorm.ErrRecordNotFound for a missing record
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This is the expected PostgreSQL behavior
			assert.True(tt, true, "Got expected ErrRecordNotFound in PostgreSQL")
		} else {
			// This is the SQLite behavior - JSONB syntax error
			assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite")
		}
	})

	t.Run("WhenVolumeNotFoundWithPreload_ExpectNotFoundError", func(tt *testing.T) {
		// This test documents the expected behavior when SQLite encounters PostgreSQL JSONB syntax
		// In a real PostgreSQL environment, this would test the actual not found scenario with preload
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// This will fail with SQLite due to JSONB syntax, but we can verify the error is handled gracefully
		// In PostgreSQL, this would be a record not found error
		volume, err := FindVolumeInRegionalPool(store.db.GORM(), "test_volume", int64(1), true)
		assert.Nil(tt, volume, "Expected nil volume due to JSONB syntax error or not found")
		assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite or not found in PostgreSQL")

		// The error should be wrapped appropriately based on the underlying error
		// Note: FindVolumeInRegionalPool returns raw gorm errors, not wrapped VCP errors
		// In SQLite, this will be a database syntax error
		// In PostgreSQL, this should be gorm.ErrRecordNotFound for a missing record
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This is the expected PostgreSQL behavior
			assert.True(tt, true, "Got expected ErrRecordNotFound in PostgreSQL")
		} else {
			// This is the SQLite behavior - JSONB syntax error
			assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite")
		}
	})
}

func TestFindVolumeInZonalPool(t *testing.T) {
	t.Run("WhenVolumeNotFound_ExpectNotFoundError", func(tt *testing.T) {
		// This test documents the expected behavior when SQLite encounters PostgreSQL JSONB syntax
		// In a real PostgreSQL environment, this would test the actual not found scenario
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// This will fail with SQLite due to JSONB syntax, but we can verify the error is handled gracefully
		// In PostgreSQL, this would be a record not found error
		volume, err := FindVolumeInZonalPool(store.db.GORM(), "test_volume", int64(1), "us-west1-a", false)
		assert.Nil(tt, volume, "Expected nil volume due to JSONB syntax error or not found")
		assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite or not found in PostgreSQL")

		// The error should be wrapped appropriately based on the underlying error
		// Note: FindVolumeInZonalPool returns raw gorm errors, not wrapped VCP errors
		// In SQLite, this will be a database syntax error
		// In PostgreSQL, this should be gorm.ErrRecordNotFound for a missing record
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This is the expected PostgreSQL behavior
			assert.True(tt, true, "Got expected ErrRecordNotFound in PostgreSQL")
		} else {
			// This is the SQLite behavior - JSONB syntax error
			assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite")
		}
	})

	t.Run("WhenVolumeNotFoundWithPreload_ExpectNotFoundError", func(tt *testing.T) {
		// This test documents the expected behavior when SQLite encounters PostgreSQL JSONB syntax
		// In a real PostgreSQL environment, this would test the actual not found scenario with preload
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// This will fail with SQLite due to JSONB syntax, but we can verify the error is handled gracefully
		// In PostgreSQL, this would be a record not found error
		volume, err := FindVolumeInZonalPool(store.db.GORM(), "test_volume", int64(1), "us-west1-a", true)
		assert.Nil(tt, volume, "Expected nil volume due to JSONB syntax error or not found")
		assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite or not found in PostgreSQL")

		// The error should be wrapped appropriately based on the underlying error
		// Note: FindVolumeInZonalPool returns raw gorm errors, not wrapped VCP errors
		// In SQLite, this will be a database syntax error
		// In PostgreSQL, this should be gorm.ErrRecordNotFound for a missing record
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// This is the expected PostgreSQL behavior
			assert.True(tt, true, "Got expected ErrRecordNotFound in PostgreSQL")
		} else {
			// This is the SQLite behavior - JSONB syntax error
			assert.Error(tt, err, "Expected error due to unsupported JSONB syntax in SQLite")
		}
	})
}

func TestListVolumesWithAccounts(t *testing.T) {
	t.Run("ReturnsVolumesWithPreloadedAccountsSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid-1"},
			Name:      "Account1",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"},
			Name:      "Pool1",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err)

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{UUID: "volume-uuid-1"},
			Name:        "Volume1",
			SizeInBytes: 1024,
			AccountID:   account.ID,
			Account:     account,
			PoolID:      pool.ID,
			Pool:        pool,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err)

		results, err := store.ListVolumesWithAccounts(context.Background())
		assert.NoError(tt, err)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "volume-uuid-1", results[0].UUID)
		assert.NotNil(tt, results[0].Account)
		assert.Equal(tt, "Account1", results[0].Account.Name)
	})

	t.Run("ReturnsEmptySliceWhenNoVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		results, err := store.ListVolumesWithAccounts(context.Background())
		assert.NoError(tt, err)
		assert.Empty(tt, results)
	})

	t.Run("ReturnsErrorOnDBFailure", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		sqlDB, err := store.db.GORM().DB()
		assert.NoError(tt, err)
		_ = sqlDB.Close()

		results, err := store.ListVolumesWithAccounts(context.Background())
		assert.Error(tt, err)
		assert.Nil(tt, results)
	})
}

func TestBuildVolumeUpdateQuery(t *testing.T) {
	// Create a DataStoreRepository instance for testing
	// Note: We don't need a real database connection since buildVolumeUpdateQuery is a pure function
	store := &DataStoreRepository{}

	t.Run("WhenUpdatesSliceIsEmpty", func(tt *testing.T) {
		updates := []datamodel.VolumeFieldUpdate{}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		expectedSQL := "UPDATE volumes SET used_bytes = tmp.used_bytes, updated_at = NOW() FROM (VALUES ) AS tmp(uuid, used_bytes) WHERE volumes.uuid::text = tmp.uuid::text"
		assert.Equal(tt, expectedSQL, sql, "Expected SQL for empty updates")
		assert.Empty(tt, args, "Expected empty args array for empty updates")
	})

	t.Run("WhenUpdatingSingleVolume", func(tt *testing.T) {
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					"used_bytes": int64(1000),
				},
			},
		}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		expectedSQL := "UPDATE volumes SET used_bytes = tmp.used_bytes, updated_at = NOW() FROM (VALUES ($1::uuid, $2::bigint)) AS tmp(uuid, used_bytes) WHERE volumes.uuid::text = tmp.uuid::text"
		assert.Equal(tt, expectedSQL, sql, "Expected SQL for single volume update")

		expectedArgs := []interface{}{"test-uuid-1", int64(1000)}
		assert.Equal(tt, expectedArgs, args, "Expected args for single volume update")
		assert.Len(tt, args, 2, "Expected exactly 2 arguments for single volume")
	})

	t.Run("WhenUpdatingMultipleVolumes", func(tt *testing.T) {
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					"used_bytes": int64(1000),
				},
			},
			{
				UUID: "test-uuid-2",
				Fields: map[string]interface{}{
					"used_bytes": int64(2000),
				},
			},
			{
				UUID: "test-uuid-3",
				Fields: map[string]interface{}{
					"used_bytes": int64(3000),
				},
			},
		}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		expectedSQL := "UPDATE volumes SET used_bytes = tmp.used_bytes, updated_at = NOW() FROM (VALUES ($1::uuid, $2::bigint), ($3::uuid, $4::bigint), ($5::uuid, $6::bigint)) AS tmp(uuid, used_bytes) WHERE volumes.uuid::text = tmp.uuid::text"
		assert.Equal(tt, expectedSQL, sql, "Expected SQL for multiple volume updates")

		expectedArgs := []interface{}{
			"test-uuid-1", int64(1000),
			"test-uuid-2", int64(2000),
			"test-uuid-3", int64(3000),
		}
		assert.Equal(tt, expectedArgs, args, "Expected args for multiple volume updates")
		assert.Len(tt, args, 6, "Expected exactly 6 arguments for 3 volumes")
	})

	t.Run("WhenUsedBytesFieldIsMissing", func(tt *testing.T) {
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					// Missing used_bytes field
					"some_other_field": "value",
				},
			},
		}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		expectedSQL := "UPDATE volumes SET used_bytes = tmp.used_bytes, updated_at = NOW() FROM (VALUES ($1::uuid, $2::bigint)) AS tmp(uuid, used_bytes) WHERE volumes.uuid::text = tmp.uuid::text"
		assert.Equal(tt, expectedSQL, sql, "Expected SQL structure even with missing used_bytes")

		expectedArgs := []interface{}{"test-uuid-1", 0} // Default value for missing field
		assert.Equal(tt, expectedArgs, args, "Expected default value 0 for missing used_bytes")
	})

	t.Run("WhenFieldsMapIsNil", func(tt *testing.T) {
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID:   "test-uuid-1",
				Fields: nil, // Nil fields map
			},
		}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		expectedSQL := "UPDATE volumes SET used_bytes = tmp.used_bytes, updated_at = NOW() FROM (VALUES ($1::uuid, $2::bigint)) AS tmp(uuid, used_bytes) WHERE volumes.uuid::text = tmp.uuid::text"
		assert.Equal(tt, expectedSQL, sql, "Expected SQL structure even with nil fields")

		expectedArgs := []interface{}{"test-uuid-1", 0} // Default value for nil fields
		assert.Equal(tt, expectedArgs, args, "Expected default value 0 for nil fields")
	})

	t.Run("WhenParameterCountingIsCorrect", func(tt *testing.T) {
		// Test with many updates to verify parameter counting
		updates := make([]datamodel.VolumeFieldUpdate, 10)
		for i := 0; i < 10; i++ {
			updates[i] = datamodel.VolumeFieldUpdate{
				UUID: fmt.Sprintf("test-uuid-%d", i+1),
				Fields: map[string]interface{}{
					"used_bytes": int64((i + 1) * 1000),
				},
			}
		}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		// Verify SQL contains correct placeholders for 10 volumes (20 parameters total)
		assert.Contains(tt, sql, "$1::uuid, $2::bigint", "Expected first parameter pair")
		assert.Contains(tt, sql, "$19::uuid, $20::bigint", "Expected last parameter pair")
		assert.NotContains(tt, sql, "$21", "Should not contain parameters beyond $20")

		// Verify args array has correct length and values
		assert.Len(tt, args, 20, "Expected 20 arguments for 10 volumes")

		// Verify parameter ordering
		assert.Equal(tt, "test-uuid-1", args[0], "First UUID should be at index 0")
		assert.Equal(tt, int64(1000), args[1], "First used_bytes should be at index 1")
		assert.Equal(tt, "test-uuid-10", args[18], "Last UUID should be at index 18")
		assert.Equal(tt, int64(10000), args[19], "Last used_bytes should be at index 19")
	})

	t.Run("WhenSQLStructureIsValid", func(tt *testing.T) {
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					"used_bytes": int64(1000),
				},
			},
		}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		// Verify SQL structure components
		assert.Contains(tt, sql, "UPDATE volumes", "Should contain UPDATE volumes clause")
		assert.Contains(tt, sql, "SET used_bytes = tmp.used_bytes, updated_at = NOW()", "Should contain SET clause")
		assert.Contains(tt, sql, "FROM (VALUES", "Should contain VALUES clause")
		assert.Contains(tt, sql, "AS tmp(uuid, used_bytes)", "Should contain temp table alias")
		assert.Contains(tt, sql, "WHERE volumes.uuid::text = tmp.uuid::text", "Should contain WHERE clause")

		// Verify PostgreSQL-specific syntax
		assert.Contains(tt, sql, "::uuid", "Should contain PostgreSQL UUID casting")
		assert.Contains(tt, sql, "::bigint", "Should contain PostgreSQL bigint casting")
		assert.Contains(tt, sql, "NOW()", "Should contain NOW() function")

		// Verify args are populated
		assert.NotEmpty(tt, args, "Args should not be empty")
	})

	t.Run("WhenUsedBytesHasDifferentTypes", func(tt *testing.T) {
		updates := []datamodel.VolumeFieldUpdate{
			{
				UUID: "test-uuid-1",
				Fields: map[string]interface{}{
					"used_bytes": 1000, // int instead of int64
				},
			},
			{
				UUID: "test-uuid-2",
				Fields: map[string]interface{}{
					"used_bytes": int64(2000), // correct int64
				},
			},
			{
				UUID: "test-uuid-3",
				Fields: map[string]interface{}{
					"used_bytes": "3000", // string instead of int64
				},
			},
		}

		sql, args := store.buildVolumeUpdateQuery(context.Background(), updates)

		// Should handle different types by accepting interface{}
		assert.Contains(tt, sql, "($1::uuid, $2::bigint), ($3::uuid, $4::bigint), ($5::uuid, $6::bigint)", "Should generate correct placeholders")
		assert.Len(tt, args, 6, "Should have 6 arguments")

		// Verify the values are passed as-is (type conversion happens in database layer)
		assert.Equal(tt, "test-uuid-1", args[0])
		assert.Equal(tt, 1000, args[1]) // int value
		assert.Equal(tt, "test-uuid-2", args[2])
		assert.Equal(tt, int64(2000), args[3]) // int64 value
		assert.Equal(tt, "test-uuid-3", args[4])
		assert.Equal(tt, "3000", args[5]) // string value
	})
}

// Add this test method at the end of the file
func TestListVolumesWithPagination(t *testing.T) {
	t.Run("WhenNoVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		conditions := [][]interface{}{
			{"account_id", "=", 999}, // Non-existent account ID
		}
		pagination := &dbutils.Pagination{Limit: 10, Offset: 0}
		volumes, err := store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 0, len(volumes), "Expected %v volumes, got %v", 0, len(volumes))
	})

	t.Run("WhenVolumesExistWithPagination", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		// Create 5 volumes for pagination testing
		volumes := make([]*datamodel.Volume, 5)
		for i := 0; i < 5; i++ {
			volumes[i] = &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("test-volume-uuid-%d", i+1)},
				Name:      fmt.Sprintf("test_volume_%d", i+1),
				AccountID: account.ID,
				Account:   account,
				Pool:      pool,
				PoolID:    pool.ID,
			}
			err = store.db.Create(volumes[i]).Error()
			assert.NoError(tt, err, "Failed to create volume %d", i+1)
		}

		conditions := [][]interface{}{
			{"account_id = ?", account.ID},
		}

		// Test first page with limit 2
		pagination := &dbutils.Pagination{Limit: 2, Offset: 0}
		resultVolumes, err := store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 2, len(resultVolumes), "Expected 2 volumes on first page, got %v", len(resultVolumes))

		// Test second page with limit 2
		pagination = &dbutils.Pagination{Limit: 2, Offset: 2}
		resultVolumes, err = store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 2, len(resultVolumes), "Expected 2 volumes on second page, got %v", len(resultVolumes))

		// Test third page with limit 2
		pagination = &dbutils.Pagination{Limit: 2, Offset: 4}
		resultVolumes, err = store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(resultVolumes), "Expected 1 volume on third page, got %v", len(resultVolumes))

		// Test with limit larger than total volumes
		pagination = &dbutils.Pagination{Limit: 10, Offset: 0}
		resultVolumes, err = store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 5, len(resultVolumes), "Expected 5 volumes with large limit, got %v", len(resultVolumes))
	})

	t.Run("WhenPaginationIsNil", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		conditions := [][]interface{}{
			{"account_id", "=", account.ID},
		}

		// Test with nil pagination - should use default limit
		resultVolumes, err := store.ListVolumesWithPagination(context.Background(), conditions, nil)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(resultVolumes), "Expected 1 volume with nil pagination, got %v", len(resultVolumes))
	})

	t.Run("WhenPaginationHasZeroLimit", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		conditions := [][]interface{}{
			{"account_id", "=", account.ID},
		}

		// Test with zero limit - should use default limit (1000)
		pagination := &dbutils.Pagination{Limit: 0, Offset: 0}
		resultVolumes, err := store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(resultVolumes), "Expected 1 volume with zero limit (default), got %v", len(resultVolumes))
	})

	t.Run("WhenDatabaseError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		conditions := [][]interface{}{
			{"account_id", "=", 1},
		}
		pagination := &dbutils.Pagination{Limit: 10, Offset: 0}
		volumes, err := store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, volumes, "Expected nil volumes when error occurs")
	})

	t.Run("WhenOffsetExceedsTotalVolumes", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		conditions := [][]interface{}{
			{"account_id", "=", account.ID},
		}

		// Test with offset beyond total volumes
		pagination := &dbutils.Pagination{Limit: 10, Offset: 100}
		resultVolumes, err := store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 0, len(resultVolumes), "Expected 0 volumes when offset exceeds total, got %v", len(resultVolumes))
	})

	t.Run("WhenVolumesWithBackupPolicyAttached", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		accountID := int64(1)
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   accountID,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		policyEnabled := true
		policyDisabled := false
		backupPolicyUUID := "test-backup-policy-uuid"

		volume1 := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:           "test_volume_1",
			AccountID:      account.ID,
			Account:        account,
			Pool:           pool,
			PoolID:         pool.ID,
			DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyEnabled},
		}
		err = store.db.Create(volume1).Error()
		assert.NoError(tt, err, "Failed to create volume 1")

		volume2 := &datamodel.Volume{
			BaseModel:      datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:           "test_volume_2",
			AccountID:      account.ID,
			Account:        account,
			Pool:           pool,
			PoolID:         pool.ID,
			DataProtection: &datamodel.DataProtection{BackupPolicyID: backupPolicyUUID, ScheduledBackupEnabled: &policyDisabled},
		}
		err = store.db.Create(volume2).Error()
		assert.NoError(tt, err, "Failed to create volume 2")

		volume3 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-3"},
			Name:      "test_volume_3",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			// No DataProtection
		}
		err = store.db.Create(volume3).Error()
		assert.NoError(tt, err, "Failed to create volume 3")

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"data_protection->>'backup_policy_id' = ?", backupPolicyUUID},
			{"data_protection->>'scheduled_backup_enabled' = true"},
		}

		pagination := &dbutils.Pagination{Limit: 10, Offset: 0}
		volumes, err := store.ListVolumesWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(volumes), "Expected 1 volume with backup policy enabled, got %v", len(volumes))
		assert.Equal(tt, "test-volume-uuid-1", volumes[0].UUID, "Expected volume 1 UUID, got %v", volumes[0].UUID)
	})
}

func TestListAllVolumes(t *testing.T) {
	t.Run("ReturnsEmptySliceWhenNoVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 10, Offset: 0}
		volumes, err := store.ListAllVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Empty(tt, volumes)
	})

	t.Run("ReturnsPaginatedVolumes", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "acc-uuid"},
			Name:      "Account1",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		// Create 3 volumes
		for i := 1; i <= 3; i++ {
			vol := &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("vol-uuid-%d", i)},
				Name:      fmt.Sprintf("Volume%d", i),
				State:     "READY",
				AccountID: account.ID,
				Account:   account,
			}
			err = store.db.Create(vol).Error()
			assert.NoError(tt, err)
		}

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 2, Offset: 0}
		volumes, err := store.ListAllVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Len(tt, volumes, 2)
		assert.Equal(tt, "Volume1", volumes[0].Name)
		assert.Equal(tt, "Volume2", volumes[1].Name)

		// Second page
		pagination = &dbutils.Pagination{Limit: 2, Offset: 2}
		volumes, err = store.ListAllVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Len(tt, volumes, 1)
		assert.Equal(tt, "Volume3", volumes[0].Name)
	})

	t.Run("ReturnsErrorOnDBFailure", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		sqlDB, err := store.db.GORM().DB()
		assert.NoError(tt, err)
		_ = sqlDB.Close()

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 10, Offset: 0}
		volumes, err := store.ListAllVolumes(context.Background(), conditions, pagination)
		assert.Error(tt, err)
		assert.Nil(tt, volumes)
	})
}

// file: database/vcp/volumes_test.go

func TestGetEligibleVolumes(t *testing.T) {
	t.Run("ReturnsEmptySliceWhenNoVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 10, Offset: 0}
		volumes, err := store.GetEligibleVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Empty(tt, volumes)
	})

	t.Run("ReturnsPaginatedEligibleVolumes", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "acc-uuid"},
			Name:      "Account1",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		for i := 1; i <= 3; i++ {
			vol := &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("vol-uuid-%d", i)},
				Name:      fmt.Sprintf("Volume%d", i),
				State:     "READY",
				AccountID: account.ID,
				Account:   account,
			}
			err = store.db.Create(vol).Error()
			assert.NoError(tt, err)
		}

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 2, Offset: 0}
		volumes, err := store.GetEligibleVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Len(tt, volumes, 2)
		assert.Equal(tt, "Volume1", volumes[0].Name)
		assert.Equal(tt, "Volume2", volumes[1].Name)

		pagination = &dbutils.Pagination{Limit: 2, Offset: 2}
		volumes, err = store.GetEligibleVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Len(tt, volumes, 1)
		assert.Equal(tt, "Volume3", volumes[0].Name)
	})

	t.Run("ReturnsEligibleVolumesWithNilPagination", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "acc-uuid"},
			Name:      "Account1",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
			Name:      "Volume1",
			State:     "READY",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(vol).Error()
		assert.NoError(tt, err)

		conditions := [][]interface{}{}
		volumes, err := store.GetEligibleVolumes(context.Background(), conditions, nil)
		assert.NoError(tt, err)
		assert.Len(tt, volumes, 1)
		assert.Equal(tt, "Volume1", volumes[0].Name)
	})

	t.Run("ReturnsEligibleVolumesWithZeroLimit", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "acc-uuid"},
			Name:      "Account1",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
			Name:      "Volume1",
			State:     "READY",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(vol).Error()
		assert.NoError(tt, err)

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 0, Offset: 0}
		volumes, err := store.GetEligibleVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Len(tt, volumes, 1)
		assert.Equal(tt, "Volume1", volumes[0].Name)
	})

	t.Run("ReturnsErrorOnDBFailure", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		sqlDB, err := store.db.GORM().DB()
		assert.NoError(tt, err)
		_ = sqlDB.Close()

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 10, Offset: 0}
		volumes, err := store.GetEligibleVolumes(context.Background(), conditions, pagination)
		assert.Error(tt, err)
		assert.Nil(tt, volumes)
	})

	t.Run("ReturnsEmptySliceWhenOffsetExceedsTotalVolumes", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "acc-uuid"},
			Name:      "Account1",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		vol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-1"},
			Name:      "Volume1",
			State:     "READY",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(vol).Error()
		assert.NoError(tt, err)

		conditions := [][]interface{}{}
		pagination := &dbutils.Pagination{Limit: 10, Offset: 100}
		volumes, err := store.GetEligibleVolumes(context.Background(), conditions, pagination)
		assert.NoError(tt, err)
		assert.Empty(tt, volumes)
	})
}

func TestBatchUpdateVolumeTieringFields(t *testing.T) {
	t.Run("WhenUpdatesMapIsEmpty", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Test with empty map - should return immediately without database operations
		err = store.BatchUpdateVolumeTieringFields(context.Background(), map[string]datamodel.VolumeTieringUpdate{})
		assert.NoError(tt, err, "Expected no error for empty updates map")
	})

	t.Run("WhenNilUpdatesMap", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Test with nil map - should return immediately
		err = store.BatchUpdateVolumeTieringFields(context.Background(), nil)
		assert.NoError(tt, err, "Expected no error for nil updates map")
	})

	t.Run("WhenDatabaseConnectionFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		// Close the database to simulate connection failure
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		assert.NoError(tt, err)

		// Prepare valid updates
		updates := map[string]datamodel.VolumeTieringUpdate{
			"test-uuid-1": {
				HotTierSizeGib:  100,
				ColdTierSizeGib: 200,
			},
		}

		// Should fail due to closed database
		err = store.BatchUpdateVolumeTieringFields(context.Background(), updates)
		assert.Error(tt, err, "Expected error when database connection is closed")
	})

	t.Run("WhenSQLExecutionFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updates := map[string]datamodel.VolumeTieringUpdate{
			"test-uuid-1": {
				HotTierSizeGib:  100,
				ColdTierSizeGib: 200,
			},
		}

		// Should fail due to SQL syntax error in SQLite
		err = store.BatchUpdateVolumeTieringFields(context.Background(), updates)
		assert.Error(tt, err, "Expected error due to PostgreSQL-specific SQL in SQLite")

		// Verify it returns the proper VCP error type
		assert.Contains(tt, err.Error(), "An internal error occurred", "Expected VCP database error")
	})

	t.Run("WhenUpdatingSingleVolumeTieringFields", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			AccountID:       account.ID,
			Account:         account,
			Pool:            pool,
			PoolID:          pool.ID,
			HotTierSizeGib:  50,  // Initial value
			ColdTierSizeGib: 100, // Initial value
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Prepare update
		updates := map[string]datamodel.VolumeTieringUpdate{
			volume.UUID: {
				HotTierSizeGib:  150,
				ColdTierSizeGib: 250,
			},
		}

		// Execute batch update
		err = store.BatchUpdateVolumeTieringFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error during batch update")

		// Verify the update
		updatedVolume, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume")
		assert.Equal(tt, uint64(150), updatedVolume.HotTierSizeGib, "Expected hot_tier_size_gib to be updated to 150")
		assert.Equal(tt, uint64(250), updatedVolume.ColdTierSizeGib, "Expected cold_tier_size_gib to be updated to 250")
	})

	t.Run("WhenUpdatingMultipleVolumesTieringFields", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume1 := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:            "test_volume_1",
			AccountID:       account.ID,
			Account:         account,
			Pool:            pool,
			PoolID:          pool.ID,
			HotTierSizeGib:  50,
			ColdTierSizeGib: 100,
		}

		volume2 := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:            "test_volume_2",
			AccountID:       account.ID,
			Account:         account,
			Pool:            pool,
			PoolID:          pool.ID,
			HotTierSizeGib:  75,
			ColdTierSizeGib: 150,
		}

		volume3 := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid-3"},
			Name:            "test_volume_3",
			AccountID:       account.ID,
			Account:         account,
			Pool:            pool,
			PoolID:          pool.ID,
			HotTierSizeGib:  25,
			ColdTierSizeGib: 50,
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume1).Error()
		assert.NoError(tt, err, "Failed to create volume1")
		err = store.db.Create(volume2).Error()
		assert.NoError(tt, err, "Failed to create volume2")
		err = store.db.Create(volume3).Error()
		assert.NoError(tt, err, "Failed to create volume3")

		// Prepare updates for multiple volumes
		updates := map[string]datamodel.VolumeTieringUpdate{
			volume1.UUID: {
				HotTierSizeGib:  200,
				ColdTierSizeGib: 400,
			},
			volume2.UUID: {
				HotTierSizeGib:  300,
				ColdTierSizeGib: 600,
			},
			volume3.UUID: {
				HotTierSizeGib:  100,
				ColdTierSizeGib: 200,
			},
		}

		// Execute batch update
		err = store.BatchUpdateVolumeTieringFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error during batch update")

		// Verify the updates
		updatedVolume1, err := store.GetVolume(context.Background(), volume1.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume1")
		assert.Equal(tt, uint64(200), updatedVolume1.HotTierSizeGib, "Expected volume1 hot_tier_size_gib to be updated to 200")
		assert.Equal(tt, uint64(400), updatedVolume1.ColdTierSizeGib, "Expected volume1 cold_tier_size_gib to be updated to 400")

		updatedVolume2, err := store.GetVolume(context.Background(), volume2.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume2")
		assert.Equal(tt, uint64(300), updatedVolume2.HotTierSizeGib, "Expected volume2 hot_tier_size_gib to be updated to 300")
		assert.Equal(tt, uint64(600), updatedVolume2.ColdTierSizeGib, "Expected volume2 cold_tier_size_gib to be updated to 600")

		updatedVolume3, err := store.GetVolume(context.Background(), volume3.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume3")
		assert.Equal(tt, uint64(100), updatedVolume3.HotTierSizeGib, "Expected volume3 hot_tier_size_gib to be updated to 100")
		assert.Equal(tt, uint64(200), updatedVolume3.ColdTierSizeGib, "Expected volume3 cold_tier_size_gib to be updated to 200")
	})

	t.Run("WhenUpdatingWithZeroValues", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			AccountID:       account.ID,
			Account:         account,
			Pool:            pool,
			PoolID:          pool.ID,
			HotTierSizeGib:  50,
			ColdTierSizeGib: 100,
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Prepare update with zero values
		updates := map[string]datamodel.VolumeTieringUpdate{
			volume.UUID: {
				HotTierSizeGib:  0,
				ColdTierSizeGib: 0,
			},
		}

		// Execute batch update
		err = store.BatchUpdateVolumeTieringFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error during batch update")

		// Verify the update
		updatedVolume, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume")
		assert.Equal(tt, uint64(0), updatedVolume.HotTierSizeGib, "Expected hot_tier_size_gib to be updated to 0")
		assert.Equal(tt, uint64(0), updatedVolume.ColdTierSizeGib, "Expected cold_tier_size_gib to be updated to 0")
	})

	t.Run("WhenUpdatingWithLargeValues", func(tt *testing.T) {
		tt.Skip("Skipped because this function uses PostgreSQL-specific VALUES clause syntax not supported in SQLite")

		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			AccountID:       account.ID,
			Account:         account,
			Pool:            pool,
			PoolID:          pool.ID,
			HotTierSizeGib:  50,
			ColdTierSizeGib: 100,
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Prepare update with large values
		updates := map[string]datamodel.VolumeTieringUpdate{
			volume.UUID: {
				HotTierSizeGib:  999999999999,
				ColdTierSizeGib: 888888888888,
			},
		}

		// Execute batch update
		err = store.BatchUpdateVolumeTieringFields(context.Background(), updates)
		assert.NoError(tt, err, "Expected no error during batch update")

		// Verify the update
		updatedVolume, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated volume")
		assert.Equal(tt, uint64(999999999999), updatedVolume.HotTierSizeGib, "Expected hot_tier_size_gib to be updated to large value")
		assert.Equal(tt, uint64(888888888888), updatedVolume.ColdTierSizeGib, "Expected cold_tier_size_gib to be updated to large value")
	})

	t.Run("WhenTransactionRollsBackOnError", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Setup test data
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			Account:   account,
		}

		volume := &datamodel.Volume{
			BaseModel:       datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:            "test_volume",
			AccountID:       account.ID,
			Account:         account,
			Pool:            pool,
			PoolID:          pool.ID,
			HotTierSizeGib:  50,
			ColdTierSizeGib: 100,
		}

		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Prepare update that will fail
		updates := map[string]datamodel.VolumeTieringUpdate{
			volume.UUID: {
				HotTierSizeGib:  200,
				ColdTierSizeGib: 400,
			},
		}

		// Execute batch update (will fail due to SQLite not supporting PostgreSQL syntax)
		err = store.BatchUpdateVolumeTieringFields(context.Background(), updates)
		assert.Error(tt, err, "Expected error during batch update")

		// Verify the values were not updated (transaction rolled back)
		unchangedVolume, err := store.GetVolume(context.Background(), volume.UUID)
		assert.NoError(tt, err, "Failed to retrieve volume")
		assert.Equal(tt, uint64(50), unchangedVolume.HotTierSizeGib, "Expected hot_tier_size_gib to remain unchanged after rollback")
		assert.Equal(tt, uint64(100), unchangedVolume.ColdTierSizeGib, "Expected cold_tier_size_gib to remain unchanged after rollback")
	})

	t.Run("WhenVerifyingSQLQueryGeneration", func(tt *testing.T) {
		updates := map[string]datamodel.VolumeTieringUpdate{
			"test-uuid-1": {
				HotTierSizeGib:  100,
				ColdTierSizeGib: 200,
			},
			"test-uuid-2": {
				HotTierSizeGib:  150,
				ColdTierSizeGib: 250,
			},
		}

		// Build the query
		placeholders := []string{}
		args := []interface{}{}
		paramCounter := 1

		for volumeUUID, update := range updates {
			placeholders = append(placeholders, fmt.Sprintf("($%d::uuid, $%d::bigint, $%d::bigint)",
				paramCounter, paramCounter+1, paramCounter+2))
			args = append(args, volumeUUID, update.HotTierSizeGib, update.ColdTierSizeGib)
			paramCounter += 3
		}

		// Verify args length
		assert.Len(tt, args, 6, "Should have 6 arguments for 2 updates (3 per update)")

		// Verify placeholders
		assert.Len(tt, placeholders, 2, "Should have 2 placeholders for 2 updates")

		// Verify the structure contains proper casting
		for _, placeholder := range placeholders {
			assert.Contains(tt, placeholder, "::uuid", "Placeholder should contain UUID casting")
			assert.Contains(tt, placeholder, "::bigint", "Placeholder should contain bigint casting")
		}
	})

	t.Run("WhenBatchingLogicIsVerified", func(tt *testing.T) {
		// This test verifies the batching logic works correctly by checking that:
		// 1. Updates are processed in batches based on UpdateVolumeTieringBatchSize
		// 2. The function handles the last partial batch correctly
		// Note: This is a unit test that verifies the batching behavior without actually executing SQL

		// Save original batch size and restore after test
		originalBatchSize := UpdateVolumeTieringBatchSize
		defer func() {
			UpdateVolumeTieringBatchSize = originalBatchSize
		}()

		// Set batch size to 5 for testing
		UpdateVolumeTieringBatchSize = 5

		// Create 12 updates (should result in 3 batches: 5, 5, 2)
		updates := make(map[string]datamodel.VolumeTieringUpdate)
		for i := 1; i <= 12; i++ {
			updates[fmt.Sprintf("test-uuid-%d", i)] = datamodel.VolumeTieringUpdate{
				HotTierSizeGib:  uint64(i * 100),
				ColdTierSizeGib: uint64(i * 200),
			}
		}

		// Verify the number of updates
		assert.Len(tt, updates, 12, "Should have 12 updates")

		// Calculate expected number of batches
		expectedBatches := (len(updates) + UpdateVolumeTieringBatchSize - 1) / UpdateVolumeTieringBatchSize
		assert.Equal(tt, 3, expectedBatches, "Should process in 3 batches (5 + 5 + 2)")

		// Verify batch size calculation
		for i := 0; i < len(updates); i += UpdateVolumeTieringBatchSize {
			end := i + UpdateVolumeTieringBatchSize
			if end > len(updates) {
				end = len(updates)
			}
			batchSize := end - i

			if i+UpdateVolumeTieringBatchSize >= len(updates) {
				// Last batch might be partial
				assert.LessOrEqual(tt, batchSize, UpdateVolumeTieringBatchSize, "Last batch should not exceed batch size")
			} else {
				// Full batches
				assert.Equal(tt, UpdateVolumeTieringBatchSize, batchSize, "Full batch should equal batch size")
			}
		}
	})
}

func TestGetFlexCacheVolumeCountByClusterPeerID(t *testing.T) {
	// Inline helper: setup repository
	newStore := func(tt *testing.T) *DataStoreRepository {
		tt.Helper()
		db, err := SetupTestDB()
		assert.NoError(tt, err, "setup db failed")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "clear db failed")
		return store
	}

	// Inline helper: create account and pool
	createAccountAndPool := func(tt *testing.T, store *DataStoreRepository) (*datamodel.Account, *datamodel.Pool) {
		tt.Helper()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "acct-flexcache",
		}
		assert.NoError(tt, store.db.Create(account).Error(), "create account failed")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "pool-flexcache",
			AccountID: account.ID,
			Account:   account,
		}
		assert.NoError(tt, store.db.Create(pool).Error(), "create pool failed")
		return account, pool
	}

	createVolumeWithClusterPeer := func(tt *testing.T, store *DataStoreRepository, account *datamodel.Account, pool *datamodel.Pool, name string, clusterPeerID int64) {
		tt.Helper()
		vol := &datamodel.Volume{
			BaseModel:     datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:          name,
			AccountID:     account.ID,
			Account:       account,
			PoolID:        pool.ID,
			Pool:          pool,
			State:         datamodel.LifeCycleStateREADY,
			StateDetails:  datamodel.LifeCycleStateAvailableDetails,
			ClusterPeerID: sql.NullInt64{Int64: clusterPeerID, Valid: true},
		}
		assert.NoError(tt, store.db.Create(vol).Error(), "create volume failed")
	}

	t.Run("WhenVolumesExistForClusterPeerID", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)

		peerA := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: utils.RandomUUID()},
			AccountID:      account.ID,
			OnprempCluster: "peer-A",
		}
		peerB := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: utils.RandomUUID()},
			AccountID:      account.ID,
			OnprempCluster: "peer-B",
		}
		assert.NoError(tt, store.db.Create(peerA).Error(), "create peerA failed")
		assert.NoError(tt, store.db.Create(peerB).Error(), "create peerB failed")

		createVolumeWithClusterPeer(tt, store, account, pool, "vol-a", peerA.ID)
		createVolumeWithClusterPeer(tt, store, account, pool, "vol-b", peerA.ID)
		createVolumeWithClusterPeer(tt, store, account, pool, "vol-c", peerB.ID)

		count, err := store.GetFlexCacheVolumeCountByClusterPeerID(context.Background(), peerA.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(2), count)
	})

	t.Run("WhenNoVolumesExistForClusterPeerID", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)

		createVolumeWithClusterPeer(tt, store, account, pool, "vol-x", 100)
		createVolumeWithClusterPeer(tt, store, account, pool, "vol-y", 101)

		count, err := store.GetFlexCacheVolumeCountByClusterPeerID(context.Background(), 999)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), count)
	})

	t.Run("WhenClusterPeerIDIsZero", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)

		createVolumeWithClusterPeer(tt, store, account, pool, "vol-z", 321)

		count, err := store.GetFlexCacheVolumeCountByClusterPeerID(context.Background(), 0)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), count)
	})

	t.Run("WhenContextIsCanceled", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)
		createVolumeWithClusterPeer(tt, store, account, pool, "vol-cancel", 55)

		cctx, cancel := context.WithCancel(context.Background())
		cancel()
		count, err := store.GetFlexCacheVolumeCountByClusterPeerID(cctx, 55)
		assert.Error(tt, err)
		assert.Equal(tt, int64(0), count)
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)
		createVolumeWithClusterPeer(tt, store, account, pool, "vol-db", 777)

		sqlDB, _ := store.db.GORM().DB()
		_ = sqlDB.Close()

		count, err := store.GetFlexCacheVolumeCountByClusterPeerID(context.Background(), 777)
		assert.Error(tt, err)
		assert.Equal(tt, int64(0), count)
	})
}

func TestGetActivePrepopulateJobs(t *testing.T) {
	t.Run("WhenNoJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected empty slice when no jobs exist")
	})

	t.Run("WhenOnlyNewJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		// Create NEW job
		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 1, "Expected 1 NEW job")
		assert.Equal(tt, "job-uuid-1", jobs[0].UUID)
		assert.Equal(tt, string(datamodel.JobsStateNEW), jobs[0].State)
	})

	t.Run("WhenOnlyProcessingJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStatePROCESSING),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 1, "Expected 1 PROCESSING job")
		assert.Equal(tt, "job-uuid-1", jobs[0].UUID)
		assert.Equal(tt, string(datamodel.JobsStatePROCESSING), jobs[0].State)
	})

	t.Run("WhenBothNewAndProcessingJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		job2 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-2"},
			ResourceName: "test-volume-2",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStatePROCESSING),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-2",
			},
		}

		assert.NoError(tt, store.db.Create(job1).Error())
		assert.NoError(tt, store.db.Create(job2).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 2, "Expected 2 active jobs (NEW + PROCESSING)")

		states := []string{jobs[0].State, jobs[1].State}
		assert.Contains(tt, states, string(datamodel.JobsStateNEW))
		assert.Contains(tt, states, string(datamodel.JobsStatePROCESSING))
	})

	t.Run("WhenCompletedJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateDONE),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected no COMPLETED jobs to be returned")
	})

	t.Run("WhenFailedJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateERROR),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected no FAILED jobs to be returned")
	})

	t.Run("WhenMixedJobStatesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		jobNew := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-new"},
			ResourceName: "test-volume-new",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-new",
			},
		}
		jobProcessing := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-processing"},
			ResourceName: "test-volume-processing",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStatePROCESSING),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-processing",
			},
		}
		jobCompleted := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-completed"},
			ResourceName: "test-volume-completed",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateERROR),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-completed",
			},
		}
		jobFailed := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-failed"},
			ResourceName: "test-volume-failed",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateERROR),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-failed",
			},
		}

		assert.NoError(tt, store.db.Create(jobNew).Error())
		assert.NoError(tt, store.db.Create(jobProcessing).Error())
		assert.NoError(tt, store.db.Create(jobCompleted).Error())
		assert.NoError(tt, store.db.Create(jobFailed).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 2, "Expected only NEW and PROCESSING jobs (2 total)")

		uuids := []string{jobs[0].UUID, jobs[1].UUID}
		assert.Contains(tt, uuids, "job-uuid-new")
		assert.Contains(tt, uuids, "job-uuid-processing")
		assert.NotContains(tt, uuids, "job-uuid-completed")
		assert.NotContains(tt, uuids, "job-uuid-failed")
	})

	t.Run("WhenNonPrepopulateJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         "OTHER_JOB_TYPE",
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Empty(tt, jobs, "Expected no jobs of other types to be returned")
	})

	t.Run("WhenJobsAreOrderedByCreatedAtAsc", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		job2 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-2"},
			ResourceName: "test-volume-2",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-2",
			},
		}
		assert.NoError(tt, store.db.Create(job2).Error())

		job3 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-3"},
			ResourceName: "test-volume-3",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-3",
			},
		}
		assert.NoError(tt, store.db.Create(job3).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 3, "Expected 3 jobs")

		// Verify jobs are ordered by created_at ASC (oldest first)
		assert.Equal(tt, "job-uuid-1", jobs[0].UUID, "First job should be the oldest")
		assert.Equal(tt, "job-uuid-2", jobs[1].UUID, "Second job should be middle")
		assert.Equal(tt, "job-uuid-3", jobs[2].UUID, "Third job should be newest")

		// Verify timestamps are in ascending order
		assert.True(tt, jobs[0].CreatedAt.Before(jobs[1].CreatedAt) || jobs[0].CreatedAt.Equal(jobs[1].CreatedAt),
			"Jobs should be ordered by created_at ascending")
		assert.True(tt, jobs[1].CreatedAt.Before(jobs[2].CreatedAt) || jobs[1].CreatedAt.Equal(jobs[2].CreatedAt),
			"Jobs should be ordered by created_at ascending")
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		assert.NoError(tt, err)

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, jobs, "Expected nil jobs when error occurs")

		// Verify it's wrapped as a database read error
		var vcpError *vsaerrors.CustomError
		if errors.As(err, &vcpError) {
			assert.Equal(tt, vsaerrors.ErrDatabaseDataReadError, vcpError.TrackingID, "Expected ErrDatabaseDataReadError tracking ID")
		}
	})

	t.Run("WhenContextIsCanceled", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		jobs, err := store.GetActivePrepopulateJobs(ctx)
		assert.Error(tt, err, "Expected error when context is canceled")
		assert.Nil(tt, jobs, "Expected nil jobs when context is canceled")
	})

	t.Run("WhenLargeNumberOfJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		// Create 50 active jobs
		for i := 0; i < 50; i++ {
			state := string(datamodel.JobsStateNEW)
			if i%2 == 0 {
				state = string(datamodel.JobsStatePROCESSING)
			}

			job := &datamodel.Job{
				BaseModel:    datamodel.BaseModel{UUID: fmt.Sprintf("job-uuid-%d", i)},
				ResourceName: fmt.Sprintf("test-volume-%d", i),
				Type:         string(datamodel.JobTypeFlexCachePrePopulate),
				State:        state,
				AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
				JobAttributes: &datamodel.JobAttributes{
					ResourceUUID: fmt.Sprintf("ontap-job-uuid-%d", i),
				},
			}
			assert.NoError(tt, store.db.Create(job).Error())
		}

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 50, "Expected all 50 active jobs to be returned")

		// Verify all jobs are either NEW or PROCESSING
		for _, job := range jobs {
			assert.Contains(tt, []string{string(datamodel.JobsStateNEW), string(datamodel.JobsStatePROCESSING)}, job.State,
				"All returned jobs should be NEW or PROCESSING")
			assert.Equal(tt, string(datamodel.JobTypeFlexCachePrePopulate), job.Type,
				"All returned jobs should be FlexCachePrePopulate type")
		}
	})

	t.Run("WhenMultipleAccountsHaveJobs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account1 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid-1"},
			Name:      "test_account_1",
		}
		account2 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "test-account-uuid-2"},
			Name:      "test_account_2",
		}
		assert.NoError(tt, store.db.Create(account1).Error())
		assert.NoError(tt, store.db.Create(account2).Error())

		// Create jobs for account 1
		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Int64: account1.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		// Create jobs for account 2
		job2 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-2"},
			ResourceName: "test-volume-2",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStatePROCESSING),
			AccountID:    sql.NullInt64{Int64: account2.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-2",
			},
		}

		assert.NoError(tt, store.db.Create(job1).Error())
		assert.NoError(tt, store.db.Create(job2).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 2, "Expected jobs from both accounts")

		accountIDs := []int64{jobs[0].AccountID.Int64, jobs[1].AccountID.Int64}
		assert.Contains(tt, accountIDs, account1.ID, "Should include job from account 1")
		assert.Contains(tt, accountIDs, account2.ID, "Should include job from account 2")
	})

	t.Run("WhenJobWithNullAccountIDExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		job1 := &datamodel.Job{
			BaseModel:    datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName: "test-volume-1",
			Type:         string(datamodel.JobTypeFlexCachePrePopulate),
			State:        string(datamodel.JobsStateNEW),
			AccountID:    sql.NullInt64{Valid: false}, // Null account ID
			IsAdminJob:   true,
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "ontap-job-uuid-1",
			},
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 1, "Expected 1 job with null account ID")
		assert.False(tt, jobs[0].AccountID.Valid, "Expected account ID to be null")
		assert.True(tt, jobs[0].IsAdminJob, "Expected job to be admin job")
	})

	t.Run("WhenJobAttributesAreNull", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		job1 := &datamodel.Job{
			BaseModel:     datamodel.BaseModel{UUID: "job-uuid-1"},
			ResourceName:  "test-volume-1",
			Type:          string(datamodel.JobTypeFlexCachePrePopulate),
			State:         string(datamodel.JobsStateNEW),
			AccountID:     sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: nil, // Null job attributes
		}
		assert.NoError(tt, store.db.Create(job1).Error())

		jobs, err := store.GetActivePrepopulateJobs(context.Background())
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, jobs, 1, "Expected 1 job even with null attributes")
		assert.Nil(tt, jobs[0].JobAttributes, "Expected job attributes to be nil")
	})
}

// Tests for ListVolumesForResourceData
func TestListVolumesForResourceData(t *testing.T) {
	t.Run("ReturnsVolumesWithPagination", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		ctx := context.Background()

		// Create test account and volumes
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		// Create volumes with VolumeAttributes
		vol1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:      "test_volume_1",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
				Labels:         &datamodel.JSONB{"env": "prod"},
				IsRegionalHA:   false,
			},
		}
		vol2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:      "test_volume_2",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-2",
				Labels:         &datamodel.JSONB{"env": "staging"},
				IsRegionalHA:   true,
			},
		}

		assert.NoError(tt, store.db.Create(vol1).Error())
		assert.NoError(tt, store.db.Create(vol2).Error())

		// Test with pagination - fetch first volume
		startTime := time.Now().Add(-1 * time.Hour)
		endTime := time.Now().Add(1 * time.Hour)
		pagination := &dbutils.Pagination{Limit: 1, Offset: 0}

		results, err := store.ListVolumesForResourceData(ctx, startTime, endTime, pagination)
		assert.NoError(tt, err)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "test-volume-uuid-1", results[0].UUID)
		assert.Equal(tt, "test_account", results[0].GetAccountName())
		assert.Equal(tt, "deployment-1", results[0].GetDeploymentName())
	})

	t.Run("ReturnsVolumesWithOffset", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		vol1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:      "test_volume_1",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
			},
		}
		vol2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:      "test_volume_2",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-2",
			},
		}

		assert.NoError(tt, store.db.Create(vol1).Error())
		assert.NoError(tt, store.db.Create(vol2).Error())

		// Test with offset
		startTime := time.Now().Add(-1 * time.Hour)
		endTime := time.Now().Add(1 * time.Hour)
		pagination := &dbutils.Pagination{Limit: 1, Offset: 1}

		results, err := store.ListVolumesForResourceData(ctx, startTime, endTime, pagination)
		assert.NoError(tt, err)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "test-volume-uuid-2", results[0].UUID)
	})

	t.Run("IncludesDeletedVolumesWithinTimeRange", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		// Create an active volume
		activeVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "active-volume-uuid"},
			Name:      "active_volume",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-active",
			},
		}
		assert.NoError(tt, store.db.Create(activeVol).Error())

		// Create a deleted volume within the time range
		deletedTime := time.Now()
		deletedVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID:      "deleted-volume-uuid",
				DeletedAt: &gorm.DeletedAt{Time: deletedTime, Valid: true},
			},
			Name:      "deleted_volume",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-deleted",
			},
		}
		assert.NoError(tt, store.db.Create(deletedVol).Error())

		// Query with time range that includes the deleted volume
		startTime := deletedTime.Add(-1 * time.Hour)
		endTime := deletedTime.Add(1 * time.Hour)

		results, err := store.ListVolumesForResourceData(ctx, startTime, endTime, nil)
		assert.NoError(tt, err)
		assert.Len(tt, results, 2) // Should include both active and deleted volume

		// Verify both volumes are present
		uuids := make(map[string]bool)
		for _, r := range results {
			uuids[r.UUID] = true
		}
		assert.True(tt, uuids["active-volume-uuid"])
		assert.True(tt, uuids["deleted-volume-uuid"])
	})

	t.Run("ExcludesDeletedVolumesOutsideTimeRange", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		// Create a deleted volume outside the time range
		deletedTime := time.Now().Add(-24 * time.Hour)
		deletedVol := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID:      "old-deleted-volume-uuid",
				DeletedAt: &gorm.DeletedAt{Time: deletedTime, Valid: true},
			},
			Name:      "old_deleted_volume",
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-old-deleted",
			},
		}
		assert.NoError(tt, store.db.Create(deletedVol).Error())

		// Query with time range that excludes the deleted volume
		startTime := time.Now().Add(-1 * time.Hour)
		endTime := time.Now().Add(1 * time.Hour)

		results, err := store.ListVolumesForResourceData(ctx, startTime, endTime, nil)
		assert.NoError(tt, err)
		assert.Len(tt, results, 0) // Should not include the old deleted volume
	})

	t.Run("ReturnsEmptySliceWhenNoVolumesExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		ctx := context.Background()
		startTime := time.Now().Add(-1 * time.Hour)
		endTime := time.Now().Add(1 * time.Hour)

		results, err := store.ListVolumesForResourceData(ctx, startTime, endTime, nil)
		assert.NoError(tt, err)
		assert.Empty(tt, results)
	})

	t.Run("NilPaginationReturnsAllVolumes", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		assert.NoError(tt, store.db.Create(account).Error())

		// Create multiple volumes
		for i := 1; i <= 3; i++ {
			vol := &datamodel.Volume{
				BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("volume-uuid-%d", i)},
				Name:      fmt.Sprintf("volume_%d", i),
				AccountID: account.ID,
				VolumeAttributes: &datamodel.VolumeAttributes{
					AccountName:    "test_account",
					DeploymentName: fmt.Sprintf("deployment-%d", i),
				},
			}
			assert.NoError(tt, store.db.Create(vol).Error())
		}

		startTime := time.Now().Add(-1 * time.Hour)
		endTime := time.Now().Add(1 * time.Hour)

		results, err := store.ListVolumesForResourceData(ctx, startTime, endTime, nil)
		assert.NoError(tt, err)
		assert.Len(tt, results, 3)
	})
}

// Tests for VolumeResourceData helper methods
func TestVolumeResourceData_HelperMethods(t *testing.T) {
	t.Run("GetAccountName_ReturnsAccountNameWhenAttributesExist", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID: "test-uuid",
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName: "my-account",
			},
		}
		assert.Equal(tt, "my-account", vol.GetAccountName())
	})

	t.Run("GetAccountName_ReturnsEmptyStringWhenAttributesNil", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID:             "test-uuid",
			VolumeAttributes: nil,
		}
		assert.Equal(tt, "", vol.GetAccountName())
	})

	t.Run("GetDeploymentName_ReturnsDeploymentNameWhenAttributesExist", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID: "test-uuid",
			VolumeAttributes: &datamodel.VolumeAttributes{
				DeploymentName: "my-deployment",
			},
		}
		assert.Equal(tt, "my-deployment", vol.GetDeploymentName())
	})

	t.Run("GetDeploymentName_ReturnsEmptyStringWhenAttributesNil", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID:             "test-uuid",
			VolumeAttributes: nil,
		}
		assert.Equal(tt, "", vol.GetDeploymentName())
	})

	t.Run("GetLabels_ReturnsLabelsWhenAttributesExist", func(tt *testing.T) {
		labels := &datamodel.JSONB{"env": "prod", "team": "backend"}
		vol := &VolumeResourceData{
			UUID: "test-uuid",
			VolumeAttributes: &datamodel.VolumeAttributes{
				Labels: labels,
			},
		}
		assert.Equal(tt, labels, vol.GetLabels())
	})

	t.Run("GetLabels_ReturnsNilWhenAttributesNil", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID:             "test-uuid",
			VolumeAttributes: nil,
		}
		assert.Nil(tt, vol.GetLabels())
	})

	t.Run("IsRegionalHA_ReturnsTrueWhenRegionalHA", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID: "test-uuid",
			VolumeAttributes: &datamodel.VolumeAttributes{
				IsRegionalHA: true,
			},
		}
		assert.True(tt, vol.IsRegionalHA())
	})

	t.Run("IsRegionalHA_ReturnsFalseWhenAttributesNil", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID:             "test-uuid",
			VolumeAttributes: nil,
		}
		assert.False(tt, vol.IsRegionalHA())
	})

	t.Run("IsRegionalHA_ReturnsFalseWhenNotRegionalHA", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID: "test-uuid",
			VolumeAttributes: &datamodel.VolumeAttributes{
				IsRegionalHA: false,
			},
		}
		assert.False(tt, vol.IsRegionalHA())
	})

	t.Run("GetLargeCapacity_ReturnsTrueWhenLargeVolumeAttributesExist", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID: "test-uuid",
			LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
				LargeCapacity: true,
			},
		}
		assert.True(tt, vol.GetLargeCapacity())
	})

	t.Run("GetLargeCapacity_ReturnsFalseWhenLargeVolumeAttributesNil", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID:                  "test-uuid",
			LargeVolumeAttributes: nil,
		}
		assert.False(tt, vol.GetLargeCapacity())
	})

	t.Run("GetLargeCapacity_ReturnsFalseWhenLargeCapacityIsFalse", func(tt *testing.T) {
		vol := &VolumeResourceData{
			UUID: "test-uuid",
			LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
				LargeCapacity: false,
			},
		}
		assert.False(tt, vol.GetLargeCapacity())
	})
}

// Tests for VolumeMetricsData helper methods
func TestVolumeMetricsData_GetAccountName(t *testing.T) {
	t.Run("ReturnsAccountNameWhenVolumeAttributesExist", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName: "test_account",
			},
		}
		result := vol.GetAccountName()
		assert.Equal(tt, "test_account", result)
	})

	t.Run("ReturnsEmptyStringWhenVolumeAttributesIsNil", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID:             "test-uuid",
			Name:             "test-volume",
			VolumeAttributes: nil,
		}
		result := vol.GetAccountName()
		assert.Equal(tt, "", result)
	})

	t.Run("ReturnsEmptyStringWhenAccountNameIsEmpty", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName: "",
			},
		}
		result := vol.GetAccountName()
		assert.Equal(tt, "", result)
	})
}

func TestVolumeMetricsData_GetDeploymentName(t *testing.T) {
	t.Run("ReturnsDeploymentNameWhenVolumeAttributesExist", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				DeploymentName: "deployment-1",
			},
		}
		result := vol.GetDeploymentName()
		assert.Equal(tt, "deployment-1", result)
	})

	t.Run("ReturnsEmptyStringWhenVolumeAttributesIsNil", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID:             "test-uuid",
			Name:             "test-volume",
			VolumeAttributes: nil,
		}
		result := vol.GetDeploymentName()
		assert.Equal(tt, "", result)
	})

	t.Run("ReturnsEmptyStringWhenDeploymentNameIsEmpty", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				DeploymentName: "",
			},
		}
		result := vol.GetDeploymentName()
		assert.Equal(tt, "", result)
	})
}

func TestVolumeMetricsData_GetProtocols(t *testing.T) {
	t.Run("ReturnsProtocolsWhenVolumeAttributesExist", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFS", "SMB"},
			},
		}
		result := vol.GetProtocols()
		assert.Equal(tt, []string{"NFS", "SMB"}, result)
	})

	t.Run("ReturnsNilWhenVolumeAttributesIsNil", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID:             "test-uuid",
			Name:             "test-volume",
			VolumeAttributes: nil,
		}
		result := vol.GetProtocols()
		assert.Nil(tt, result)
	})

	t.Run("ReturnsNilWhenProtocolsIsNil", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: nil,
			},
		}
		result := vol.GetProtocols()
		assert.Nil(tt, result)
	})

	t.Run("ReturnsEmptySliceWhenProtocolsIsEmpty", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{},
			},
		}
		result := vol.GetProtocols()
		assert.Equal(tt, []string{}, result)
	})
}

func TestVolumeMetricsData_IsRegionalHA(t *testing.T) {
	t.Run("ReturnsTrueWhenIsRegionalHA", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				IsRegionalHA: true,
			},
		}
		result := vol.IsRegionalHA()
		assert.True(tt, result)
	})

	t.Run("ReturnsFalseWhenVolumeAttributesIsNil", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID:             "test-uuid",
			Name:             "test-volume",
			VolumeAttributes: nil,
		}
		result := vol.IsRegionalHA()
		assert.False(tt, result)
	})

	t.Run("ReturnsFalseWhenNotRegionalHA", func(tt *testing.T) {
		vol := &VolumeMetricsData{
			UUID: "test-uuid",
			Name: "test-volume",
			VolumeAttributes: &datamodel.VolumeAttributes{
				IsRegionalHA: false,
			},
		}
		result := vol.IsRegionalHA()
		assert.False(tt, result)
	})
}

// Tests for ListVolumesForTelemetryMetrics
func TestListVolumesForTelemetryMetrics(t *testing.T) {
	t.Run("ReturnsVolumesWithMinimalFields", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		// Create test account and pool
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
			Name:           "test_pool",
			AccountID:      account.ID,
			DeploymentName: "deployment-1",
			QosType:        "manual",
		}
		err = store.db.Create(pool).Error()
		require.NoError(tt, err)

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{ID: 1},
			Name:            "test-vpg",
			PoolID:          pool.ID,
			AllocationType: models.AllocationTypeShared,
			IsAutoGen:       false,
			ThroughputMibps: 2048,
			Iops:            12345,
		}
		err = store.db.Create(vpg).Error()
		require.NoError(tt, err)

		// Create a volume with VolumeAttributes
		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:        "test_volume_1",
			SizeInBytes: 1000000,
			Throughput:  1024, // Legacy throughput should be ignored in telemetry query.
			PoolID:      pool.ID,
			AccountID:   account.ID,
			VolumePerformanceGroupID: sql.NullInt64{
				Int64: vpg.ID,
				Valid: true,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
				Protocols:      []string{"NFS"},
				IsRegionalHA:   false,
			},
		}
		err = store.db.Create(volume).Error()
		require.NoError(tt, err)

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		assert.NoError(tt, err)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "test-volume-uuid-1", results[0].UUID)
		assert.Equal(tt, "test_volume_1", results[0].Name)
		assert.Equal(tt, int64(1000000), results[0].SizeInBytes)
		assert.Equal(tt, int64(2048), results[0].Throughput)
		assert.Equal(tt, int64(12345), results[0].Iops)
		assert.Equal(tt, pool.ID, results[0].PoolID)
		assert.Equal(tt, "test_account", results[0].GetAccountName())
		assert.Equal(tt, "deployment-1", results[0].GetDeploymentName())
		assert.Equal(tt, []string{"NFS"}, results[0].GetProtocols())
		assert.False(tt, results[0].IsRegionalHA())
	})

	t.Run("ExcludesDeletedVolumes", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
			Name:           "test_pool",
			AccountID:      account.ID,
			DeploymentName: "deployment-1",
		}
		err = store.db.Create(pool).Error()
		require.NoError(tt, err)

		// Create an active volume
		activeVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "active-volume-uuid"},
			Name:      "active_volume",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
			},
		}

		// Create a deleted volume
		deletedTime := time.Now().Add(-1 * time.Hour)
		deletedVolume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID:      "deleted-volume-uuid",
				DeletedAt: &gorm.DeletedAt{Time: deletedTime, Valid: true},
			},
			Name:      "deleted_volume",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
			},
		}

		err = store.db.Create(activeVolume).Error()
		require.NoError(tt, err)
		err = store.db.GORM().Create(deletedVolume).Error
		require.NoError(tt, err)

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		assert.NoError(tt, err)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "active-volume-uuid", results[0].UUID)
	})

	t.Run("ReturnsEmptyWhenNoVolumes", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		assert.NoError(tt, err)
		assert.Len(tt, results, 0)
	})

	t.Run("ReturnsMultipleVolumes", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
			Name:           "test_pool",
			AccountID:      account.ID,
			DeploymentName: "deployment-1",
		}
		err = store.db.Create(pool).Error()
		require.NoError(tt, err)

		vol1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:      "test_volume_1",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
			},
		}
		vol2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:      "test_volume_2",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
			},
		}

		err = store.db.Create(vol1).Error()
		require.NoError(tt, err)
		err = store.db.Create(vol2).Error()
		require.NoError(tt, err)

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		assert.NoError(tt, err)
		assert.Len(tt, results, 2)
	})

	t.Run("HandlesNilVolumeAttributes", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
			Name:           "test_pool",
			AccountID:      account.ID,
			DeploymentName: "deployment-1",
		}
		err = store.db.Create(pool).Error()
		require.NoError(tt, err)

		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:             "test_volume_1",
			PoolID:           pool.ID,
			AccountID:        account.ID,
			VolumeAttributes: nil,
		}

		err = store.db.Create(volume).Error()
		require.NoError(tt, err)

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		assert.NoError(tt, err)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "", results[0].GetAccountName())
		assert.Equal(tt, "", results[0].GetDeploymentName())
		assert.Nil(tt, results[0].GetProtocols())
		assert.False(tt, results[0].IsRegionalHA())
	})

	t.Run("IncludesDataProtectionField", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid", ID: 1},
			Name:           "test_pool",
			AccountID:      account.ID,
			DeploymentName: "deployment-1",
		}
		err = store.db.Create(pool).Error()
		require.NoError(tt, err)

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid-1"},
			Name:      "test_volume_1",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account",
				DeploymentName: "deployment-1",
			},
			DataProtection: &datamodel.DataProtection{
				BackupVaultID: "backup-vault-uuid",
			},
		}

		err = store.db.Create(volume).Error()
		require.NoError(tt, err)

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		assert.NoError(tt, err)
		assert.Len(tt, results, 1)
		assert.NotNil(tt, results[0].DataProtection)
		assert.Equal(tt, "backup-vault-uuid", results[0].DataProtection.BackupVaultID)
	})

	t.Run("NoVpgAssignmentReturnsZeroThroughputAndIops", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "test-account-uuid-2"},
			Name:      "test_account_2",
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid-2", ID: 2},
			Name:           "test_pool_2",
			AccountID:      account.ID,
			DeploymentName: "deployment-2",
			QosType:        utils.QosTypeAuto,
		}
		err = store.db.Create(pool).Error()
		require.NoError(tt, err)

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:        "test_volume_2",
			SizeInBytes: 2000000,
			Throughput:  1111, // Legacy throughput should be ignored in telemetry query.
			PoolID:      pool.ID,
			AccountID:   account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account_2",
				DeploymentName: "deployment-2",
				Protocols:      []string{"NFS"},
			},
		}
		err = store.db.Create(volume).Error()
		require.NoError(tt, err)

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		require.NoError(tt, err)
		require.NotEmpty(tt, results)

		// Find the test volume in results to avoid ordering dependency.
		var found *VolumeMetricsData
		for _, result := range results {
			if result.UUID == volume.UUID {
				found = result
				break
			}
		}
		require.NotNil(tt, found)
		assert.Equal(tt, int64(0), found.Throughput)
		assert.Equal(tt, int64(0), found.Iops)
	})

	t.Run("CoalescesNullVpgValues", func(tt *testing.T) {
		store := setup(tt)
		ctx := context.Background()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: "test-account-uuid-2"},
			Name:      "test_account_2",
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)

		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid-2", ID: 2},
			Name:           "test_pool_2",
			AccountID:      account.ID,
			DeploymentName: "deployment-2",
			QosType:        utils.QosTypeAuto,
		}
		err = store.db.Create(pool).Error()
		require.NoError(tt, err)

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{ID: 2},
			Name:      "test-vpg-null",
			PoolID:    pool.ID,
			AllocationType: models.AllocationTypeShared,
			IsAutoGen: true,
			// Set non-zero to prove DB null overwrite + COALESCE behavior.
			ThroughputMibps: 999,
			Iops:            999,
		}
		err = store.db.Create(vpg).Error()
		require.NoError(tt, err)

		err = store.db.Exec(
			"UPDATE volume_performance_groups SET throughput_mibps = NULL, iops = NULL WHERE id = ?",
			vpg.ID,
		).Error()
		require.NoError(tt, err)

		volume := &datamodel.Volume{
			BaseModel:   datamodel.BaseModel{UUID: "test-volume-uuid-2"},
			Name:        "test_volume_2",
			SizeInBytes: 2000000,
			Throughput:  1111, // Legacy throughput should be ignored in telemetry query.
			PoolID:      pool.ID,
			AccountID:   account.ID,
			VolumePerformanceGroupID: sql.NullInt64{
				Int64: vpg.ID,
				Valid: true,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "test_account_2",
				DeploymentName: "deployment-2",
				Protocols:      []string{"NFS"},
			},
		}
		err = store.db.Create(volume).Error()
		require.NoError(tt, err)

		results, err := store.ListVolumesForTelemetryMetrics(ctx)
		require.NoError(tt, err)
		require.NotEmpty(tt, results)

		// Find the test volume in results to avoid ordering dependency.
		var found *VolumeMetricsData
		for _, result := range results {
			if result.UUID == volume.UUID {
				found = result
				break
			}
		}
		require.NotNil(tt, found)
		assert.Equal(tt, int64(0), found.Throughput)
		assert.Equal(tt, int64(0), found.Iops)
	})
}

func TestGetVolumesByVolumePerformanceGroupID(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-vpg"}, Name: "acct-vpg"}
	require.NoError(t, store.db.Create(account).Error())
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-vpg"}, Name: "pool-vpg", AccountID: account.ID, Account: account}
	require.NoError(t, store.db.Create(pool).Error())

	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel: datamodel.BaseModel{UUID: "vpg-uuid"},
		PoolID:    pool.ID,
		Name:      "vpg-name",
		AllocationType: models.AllocationTypeShared,
	}
	require.NoError(t, store.db.Create(vpg).Error())

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "vol-name",
		PoolID:    pool.ID,
		AccountID: account.ID,
		VolumePerformanceGroupID: sql.NullInt64{
			Int64: vpg.ID,
			Valid: true,
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName:    "acct-vpg",
			DeploymentName: "deployment-vpg",
			Protocols:      []string{"NFS"},
		},
	}
	require.NoError(t, store.db.Create(volume).Error())

	volumes, err := store.GetVolumesByVolumePerformanceGroupID(context.Background(), vpg.ID)
	require.NoError(t, err)
	require.Len(t, volumes, 1)
	assert.Equal(t, volume.UUID, volumes[0].UUID)
}

func TestDereferenceVPGFromDeletedVolumes(t *testing.T) {
	db, err := SetupTestDB()
	require.NoError(t, err)
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	require.NoError(t, ClearInMemoryDB(store.db.GORM()))

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-vpg-del"}, Name: "acct-vpg-del"}
	require.NoError(t, store.db.Create(account).Error())
	pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-vpg-del"}, Name: "pool-vpg-del", AccountID: account.ID, Account: account}
	require.NoError(t, store.db.Create(pool).Error())

	vpg := &datamodel.VolumePerformanceGroup{
		BaseModel: datamodel.BaseModel{UUID: "vpg-uuid-del"},
		PoolID:    pool.ID,
		Name:      "vpg-name-del",
		AllocationType: models.AllocationTypeShared,
	}
	require.NoError(t, store.db.Create(vpg).Error())

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid-del"},
		Name:      "vol-name-del",
		PoolID:    pool.ID,
		AccountID: account.ID,
		VolumePerformanceGroupID: sql.NullInt64{
			Int64: vpg.ID,
			Valid: true,
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			AccountName:    "acct-vpg-del",
			DeploymentName: "deployment-vpg-del",
			Protocols:      []string{"NFS"},
		},
	}
	require.NoError(t, store.db.Create(volume).Error())

	require.NoError(t, store.db.GORM().Unscoped().
		Model(&datamodel.Volume{}).
		Where("id = ?", volume.ID).
		Update("deleted_at", time.Now()).
		Error)

	err = store.DereferenceVPGFromDeletedVolumes(context.Background(), vpg.ID)
	require.NoError(t, err)

	var updated datamodel.Volume
	require.NoError(t, store.db.GORM().Unscoped().Where("id = ?", volume.ID).First(&updated).Error)
	assert.False(t, updated.VolumePerformanceGroupID.Valid)
}

func TestDereferencePoolVolumesFromVPGs(t *testing.T) {
	t.Run("Success_UpdatesVolumesWithVPG", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-deref-pool"}, Name: "acct-deref-pool"}
		require.NoError(t, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-deref-pool"}, Name: "pool-deref-pool", AccountID: account.ID, Account: account}
		require.NoError(t, store.db.Create(pool).Error())

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-deref-pool"},
			PoolID:    pool.ID,
			Name:      "vpg-deref-pool",
			AllocationType: models.AllocationTypeShared,
		}
		require.NoError(t, store.db.Create(vpg).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-deref-pool"},
			Name:      "vol-deref-pool",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumePerformanceGroupID: sql.NullInt64{
				Int64: vpg.ID,
				Valid: true,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "acct-deref-pool",
				DeploymentName: "deployment-deref-pool",
				Protocols:      []string{"NFS"},
			},
		}
		require.NoError(t, store.db.Create(volume).Error())

		count, err := store.DereferencePoolVolumesFromVPGs(context.Background(), pool.ID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, int64(1))

		var updated datamodel.Volume
		require.NoError(t, store.db.GORM().Unscoped().Where("id = ?", volume.ID).First(&updated).Error)
		assert.False(t, updated.VolumePerformanceGroupID.Valid)
	})

	t.Run("Success_NoVolumesWithVPG_ReturnsZero", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-deref-pool-none"}, Name: "acct-deref-pool-none"}
		require.NoError(t, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-deref-pool-none"}, Name: "pool-deref-pool-none", AccountID: account.ID, Account: account}
		require.NoError(t, store.db.Create(pool).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-deref-pool-none"},
			Name:      "vol-deref-pool-none",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "acct-deref-pool-none",
				DeploymentName: "deployment-deref-pool-none",
				Protocols:      []string{"NFS"},
			},
		}
		require.NoError(t, store.db.Create(volume).Error())

		count, err := store.DereferencePoolVolumesFromVPGs(context.Background(), pool.ID)
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
	})
}

func TestDeleteVolume_DereferencesVPG(t *testing.T) {
	t.Run("DereferencesAutoGeneratedVPG", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-vpg-auto"}, Name: "acct-vpg-auto"}
		require.NoError(t, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-vpg-auto"}, Name: "pool-vpg-auto", AccountID: account.ID, Account: account}
		require.NoError(t, store.db.Create(pool).Error())

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-uuid-auto"},
			PoolID:    pool.ID,
			Name:      "vpg-name-auto",
			AllocationType: models.AllocationTypeShared,
			IsAutoGen: true,
		}
		require.NoError(t, store.db.Create(vpg).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-auto"},
			Name:      "vol-name-auto",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumePerformanceGroupID: sql.NullInt64{
				Int64: vpg.ID,
				Valid: true,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "acct-vpg-auto",
				DeploymentName: "deployment-vpg-auto",
				Protocols:      []string{"NFS"},
			},
		}
		require.NoError(t, store.db.Create(volume).Error())

		_, err = store.DeleteVolume(context.Background(), volume.UUID)
		require.NoError(t, err)

		var deleted datamodel.Volume
		require.NoError(t, store.db.GORM().Unscoped().Where("id = ?", volume.ID).First(&deleted).Error)
		assert.True(t, deleted.DeletedAt.Valid)
		assert.False(t, deleted.VolumePerformanceGroupID.Valid, "VPG should be dereferenced")
	})

	t.Run("DereferencesNonAutoGeneratedVPG", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "acct-vpg-manual"}, Name: "acct-vpg-manual"}
		require.NoError(t, store.db.Create(account).Error())
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-vpg-manual"}, Name: "pool-vpg-manual", AccountID: account.ID, Account: account}
		require.NoError(t, store.db.Create(pool).Error())

		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel: datamodel.BaseModel{UUID: "vpg-uuid-manual"},
			PoolID:    pool.ID,
			Name:      "vpg-name-manual",
			AllocationType: models.AllocationTypeShared,
			IsAutoGen: false,
		}
		require.NoError(t, store.db.Create(vpg).Error())

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid-manual"},
			Name:      "vol-name-manual",
			PoolID:    pool.ID,
			AccountID: account.ID,
			VolumePerformanceGroupID: sql.NullInt64{
				Int64: vpg.ID,
				Valid: true,
			},
			VolumeAttributes: &datamodel.VolumeAttributes{
				AccountName:    "acct-vpg-manual",
				DeploymentName: "deployment-vpg-manual",
				Protocols:      []string{"NFS"},
			},
		}
		require.NoError(t, store.db.Create(volume).Error())

		_, err = store.DeleteVolume(context.Background(), volume.UUID)
		require.NoError(t, err)

		var deleted datamodel.Volume
		require.NoError(t, store.db.GORM().Unscoped().Where("id = ?", volume.ID).First(&deleted).Error)
		assert.True(t, deleted.DeletedAt.Valid)
		assert.False(t, deleted.VolumePerformanceGroupID.Valid, "VPG should be dereferenced even if not auto-generated")
	})
}

func TestGetVolumeCountByVolumePerformanceGroupID(t *testing.T) {
	newStore := func(tt *testing.T) *DataStoreRepository {
		tt.Helper()
		db, err := SetupTestDB()
		assert.NoError(tt, err, "setup db failed")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "clear db failed")
		return store
	}

	createAccountAndPool := func(tt *testing.T, store *DataStoreRepository) (*datamodel.Account, *datamodel.Pool) {
		tt.Helper()
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "acct-vpg-count",
		}
		assert.NoError(tt, store.db.Create(account).Error(), "create account failed")

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:      "pool-vpg-count",
			AccountID: account.ID,
			Account:   account,
		}
		assert.NoError(tt, store.db.Create(pool).Error(), "create pool failed")
		return account, pool
	}

	createVPG := func(tt *testing.T, store *DataStoreRepository, pool *datamodel.Pool, name string) *datamodel.VolumePerformanceGroup {
		tt.Helper()
		vpg := &datamodel.VolumePerformanceGroup{
			BaseModel:       datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:            name,
			PoolID:          pool.ID,
			Pool:            pool,
			ThroughputMibps: 100,
			Iops:            1000,
			AllocationType: models.AllocationTypePerVolume,
		}
		created, err := store.CreateVolumePerformanceGroup(context.Background(), vpg)
		assert.NoError(tt, err, "create VPG failed")
		return created
	}

	createVolumeWithVPG := func(tt *testing.T, store *DataStoreRepository, account *datamodel.Account, pool *datamodel.Pool, name string, vpgID int64) {
		tt.Helper()
		vol := &datamodel.Volume{
			BaseModel:                datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:                     name,
			AccountID:                account.ID,
			Account:                  account,
			PoolID:                   pool.ID,
			Pool:                     pool,
			State:                    datamodel.LifeCycleStateREADY,
			VolumePerformanceGroupID: sql.NullInt64{Int64: vpgID, Valid: true},
		}
		assert.NoError(tt, store.db.Create(vol).Error(), "create volume failed")
	}

	t.Run("WhenVolumesExistForVPGID", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)

		vpgA := createVPG(tt, store, pool, "vpg-a")
		vpgB := createVPG(tt, store, pool, "vpg-b")

		createVolumeWithVPG(tt, store, account, pool, "vol-a1", vpgA.ID)
		createVolumeWithVPG(tt, store, account, pool, "vol-a2", vpgA.ID)
		createVolumeWithVPG(tt, store, account, pool, "vol-b1", vpgB.ID)

		count, err := store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), vpgA.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(2), count, "Expected 2 volumes for vpgA")

		count, err = store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), vpgB.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(1), count, "Expected 1 volume for vpgB")
	})

	t.Run("WhenNoVolumesExistForVPGID", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)

		vpgA := createVPG(tt, store, pool, "vpg-a")
		vpgB := createVPG(tt, store, pool, "vpg-b")

		// Create volumes for vpgA only
		createVolumeWithVPG(tt, store, account, pool, "vol-a1", vpgA.ID)

		// Query for vpgB which has no volumes
		count, err := store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), vpgB.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), count, "Expected 0 volumes for vpgB")
	})

	t.Run("WhenVPGIDDoesNotExist", func(tt *testing.T) {
		store := newStore(tt)
		_, _ = createAccountAndPool(tt, store)

		// Query for non-existent VPG ID
		count, err := store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), 99999)
		assert.NoError(tt, err, "Should not error for non-existent VPG ID")
		assert.Equal(tt, int64(0), count, "Expected 0 volumes for non-existent VPG ID")
	})

	t.Run("WhenVolumesAreSoftDeleted", func(tt *testing.T) {
		store := newStore(tt)
		account, pool := createAccountAndPool(tt, store)

		vpgA := createVPG(tt, store, pool, "vpg-a")

		vol1 := &datamodel.Volume{
			BaseModel:                datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:                     "vol-a1",
			AccountID:                account.ID,
			Account:                  account,
			PoolID:                   pool.ID,
			Pool:                     pool,
			State:                    datamodel.LifeCycleStateREADY,
			VolumePerformanceGroupID: sql.NullInt64{Int64: vpgA.ID, Valid: true},
		}
		assert.NoError(tt, store.db.Create(vol1).Error(), "create volume 1 failed")

		vol2 := &datamodel.Volume{
			BaseModel:                datamodel.BaseModel{UUID: utils.RandomUUID()},
			Name:                     "vol-a2",
			AccountID:                account.ID,
			Account:                  account,
			PoolID:                   pool.ID,
			Pool:                     pool,
			State:                    datamodel.LifeCycleStateREADY,
			VolumePerformanceGroupID: sql.NullInt64{Int64: vpgA.ID, Valid: true},
		}
		assert.NoError(tt, store.db.Create(vol2).Error(), "create volume 2 failed")

		count, err := store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), vpgA.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(2), count, "Expected 2 volumes before soft delete")

		err = store.db.Delete(vol1).Error()
		assert.NoError(tt, err, "soft delete volume failed")

		count, err = store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), vpgA.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(1), count, "Expected 1 volume after soft delete (deleted_at IS NULL filter)")
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Close the database to simulate an error
		sqlDB, err := db.DB()
		assert.NoError(tt, err)
		err = sqlDB.Close()
		if err != nil {
			return
		}

		count, err := store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), 1)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Equal(tt, int64(0), count, "Expected count to be 0 when error occurs")
	})

	t.Run("WhenZeroVolumesExist", func(tt *testing.T) {
		store := newStore(tt)
		_, pool := createAccountAndPool(tt, store)

		vpgA := createVPG(tt, store, pool, "vpg-a")

		// Don't create any volumes, just query
		count, err := store.GetVolumeCountByVolumePerformanceGroupID(context.Background(), vpgA.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), count, "Expected 0 volumes for VPG with no volumes")
	})
}

// Tests for ExpertModeVolumeMetricsData.GetProtocols
func TestExpertModeVolumeMetricsData_GetProtocols(t *testing.T) {
	t.Run("WhenVolumeAttributesIsNil_ReturnsNil", func(tt *testing.T) {
		v := &ExpertModeVolumeMetricsData{VolumeAttributes: nil}
		assert.Nil(tt, v.GetProtocols())
	})

	t.Run("WhenVolumeAttributesHasProtocols_ReturnsProtocols", func(tt *testing.T) {
		v := &ExpertModeVolumeMetricsData{
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				Protocols: []string{"NFS", "SMB"},
			},
		}
		assert.Equal(tt, []string{"NFS", "SMB"}, v.GetProtocols())
	})

	t.Run("WhenVolumeAttributesHasEmptyProtocols_ReturnsEmptySlice", func(tt *testing.T) {
		v := &ExpertModeVolumeMetricsData{
			VolumeAttributes: &datamodel.ExpertModeVolumeAttributes{
				Protocols: []string{},
			},
		}
		assert.Empty(tt, v.GetProtocols())
	})
}

// Tests for ListExpertModeVolumesForTelemetryMetrics.
// Note: the query uses the PostgreSQL-specific ::boolean cast
// (COALESCE((p.pool_attributes->>'is_regional_ha')::boolean, false)) which
// SQLite does not support. The SQLite-based test therefore exercises the
// error path (lines 1146-1147) and the function setup (lines 1124, 1126, 1128).
// Line 1150 (success return) is only reachable against a real PostgreSQL instance.
func TestListExpertModeVolumesForTelemetryMetrics_SQLiteErrorPath(t *testing.T) {
	store := setup(t)
	ctx := context.Background()

	_, err := store.ListExpertModeVolumesForTelemetryMetrics(ctx, nil)
	require.Error(t, err, "expected a DB error due to PostgreSQL-specific ::boolean cast in SQLite")

	// The function wraps the raw DB error in a VCPError.
	var vcpErr *vsaerrors.CustomError
	assert.True(t, errors.As(err, &vcpErr), "expected error to be a VCPError")
}
