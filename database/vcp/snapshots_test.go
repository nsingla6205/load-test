package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	dbutils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/gorm"
)

func TestCreatingSnapshot(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	t.Run("WhenSnapshotIsCreatedSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.NoError(t, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			Name:     "test_snapshot",
			VolumeID: volume.ID,
		}
		createdSnapshot, err := store.CreatingSnapshot(ctx, snapshot)
		assert.NoError(t, err, "Expected no error, got %v", err)
		assert.Equal(t, snapshot.Name, createdSnapshot.Name, "Expected snapshot name %v, got %v", snapshot.Name, createdSnapshot.Name)
		assert.Equal(t, datamodel.LifeCycleStateCreating, createdSnapshot.State, "Expected snapshot state %v, got %v", datamodel.LifeCycleStateCreating, createdSnapshot.State)
	})

	t.Run("WhenSnapshotCreationFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		if err != nil {
			t.Fatalf("Failed to create volume: %v", err)
		}

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot1",
			VolumeID:  volume.ID,
		}
		err = store.db.Create(snapshot).Error()
		if err != nil {
			t.Fatalf("Failed to create snapshot: %v", err)
		}

		_, err = store.CreatingSnapshot(context.Background(), snapshot)
		assert.ErrorContains(t, err, "Snapshot already exists", "Expected error 'Snapshot already exists', got %v", err)
	})
}

func TestUpdateSnapshot(t *testing.T) {
	t.Run("WhenUpdateIsSuccessful", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.Equal(t, err, nil)

		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateCreating,
		}
		err = store.db.Create(snapshot).Error()
		if err != nil {
			t.Fatalf("Failed to create snapshot: %v", err)
		}

		updatedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			State:     datamodel.LifeCycleStateAvailable,
		}

		dbSnapshot, err := store.UpdateSnapshot(context.Background(), updatedSnapshot)
		assert.Equal(t, err, nil)

		result := &datamodel.Snapshot{}
		err = store.db.GORM().First(result, "uuid = ?", updatedSnapshot.UUID).Error
		assert.Equal(t, err, nil)
		assert.Equal(t, updatedSnapshot.State, result.State, "Expected state %v, got %v", updatedSnapshot.State, result.State)
		assert.Equal(t, updatedSnapshot.State, dbSnapshot.State, "Expected state %v, got %v", updatedSnapshot.State, dbSnapshot.State)
	})

	t.Run("WhenSnapshotDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		updatedSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "non-existent-uuid"},
			State:     datamodel.LifeCycleStateAvailable,
		}

		dbSnapshot, err := store.UpdateSnapshot(context.Background(), updatedSnapshot)
		assert.ErrorContains(t, err, "not found", "Expected error 'not found', got %v", err)
		assert.Nil(tt, dbSnapshot)
	})
}

func TestGetAppConsistentSnapshotsForVolume(t *testing.T) {
	t.Run("WhenAppConsistentSnapshotExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		err = store.db.Create(volume).Error()
		assert.Equal(t, err, nil)

		snapshot := &datamodel.Snapshot{
			BaseModel:       datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			VolumeID:        volume.ID,
			State:           datamodel.LifeCycleStateAvailable,
			IsAppConsistent: true,
			AccountID:       1,
		}
		err = store.db.Create(snapshot).Error()
		if err != nil {
			t.Fatalf("Failed to create snapshot: %v", err)
		}
		forVolume, err := store.GetAppConsistentSnapshotsForVolume(context.Background(), 1, volume.ID)
		if err != nil {
			t.Fatalf("Failed to get volume: %v", err)
		}
		assert.Equal(tt, 1, len(forVolume))
	})

	t.Run("WhenAppConsistentSnapshotDoesnotExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		err = store.db.Create(volume).Error()
		assert.Equal(t, err, nil)

		snapshot := &datamodel.Snapshot{
			BaseModel:       datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			VolumeID:        volume.ID,
			State:           datamodel.LifeCycleStateAvailable,
			IsAppConsistent: false,
			AccountID:       1,
		}
		err = store.db.Create(snapshot).Error()
		if err != nil {
			t.Fatalf("Failed to create snapshot: %v", err)
		}
		forVolume, err := store.GetAppConsistentSnapshotsForVolume(context.Background(), 1, volume.ID)
		if err != nil {
			t.Fatalf("Failed to get volume: %v", err)
		}
		assert.Equal(tt, 0, len(forVolume))
	})
}

func TestGetSnapshot(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	t.Run("WhenSnapshotExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{
				UUID: "test-volume-uuid",
				ID:   1,
			},
			Name:      "test_volume",
			AccountID: 1,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create a snapshot
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateAvailable,
			AccountID: 1,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		// Get the snapshot
		retrievedSnapshot, err := store.GetSnapshotByUUID(ctx, snapshot.UUID, 1, volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, retrievedSnapshot, "Expected snapshot to be retrieved")
		assert.Equal(tt, snapshot.UUID, retrievedSnapshot.UUID, "Expected UUID %v, got %v", snapshot.UUID, retrievedSnapshot.UUID)
		assert.Equal(tt, snapshot.Name, retrievedSnapshot.Name, "Expected name %v, got %v", snapshot.Name, retrievedSnapshot.Name)
		assert.Equal(tt, snapshot.State, retrievedSnapshot.State, "Expected state %v, got %v", snapshot.State, retrievedSnapshot.State)
		assert.Equal(tt, snapshot.VolumeID, retrievedSnapshot.VolumeID, "Expected VolumeID %v, got %v", snapshot.VolumeID, retrievedSnapshot.VolumeID)
		assert.Equal(tt, volume.Name, retrievedSnapshot.Volume.Name, "Expected VolumeName %v, got %v", volume.Name, retrievedSnapshot.Volume.Name)
		assert.Equal(tt, account.Name, retrievedSnapshot.Account.Name, "Expected AccountName %v, got %v", account.Name, retrievedSnapshot.Account.Name)
	})

	t.Run("WhenSnapshotDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Try to get a non-existent snapshot
		nonExistentUUID := "non-existent-uuid"
		_, err = store.GetSnapshotByUUID(ctx, nonExistentUUID, 1, 1)
		assert.Error(tt, err, "Expected error when snapshot does not exist")
		assert.ErrorContains(tt, err, "not found", "Expected error 'not found', got %v", err)
	})
}

func TestGetSnapshotByPoolID(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	t.Run("WhenSnapshotExistsForPoolID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create a pool
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create a snapshot
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			VolumeID:  volume.ID,
			AccountID: account.ID,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		// Get the snapshot by pool ID
		retrievedSnapshot, err := store.GetSnapshotByPoolID(ctx, snapshot.UUID, account.ID, pool.ID, false)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, retrievedSnapshot, "Expected snapshot to be retrieved")
		assert.Equal(tt, snapshot.UUID, retrievedSnapshot.UUID, "Expected UUID %v, got %v", snapshot.UUID, retrievedSnapshot.UUID)
		assert.Equal(tt, snapshot.Name, retrievedSnapshot.Name, "Expected name %v, got %v", snapshot.Name, retrievedSnapshot.Name)
		assert.Equal(tt, snapshot.VolumeID, retrievedSnapshot.VolumeID, "Expected VolumeID %v, got %v", snapshot.VolumeID, retrievedSnapshot.VolumeID)
		assert.Equal(tt, volume.PoolID, retrievedSnapshot.Volume.PoolID, "Expected PoolID %v, got %v", volume.PoolID, retrievedSnapshot.Volume.PoolID)
	})

	t.Run("WhenSnapshotDoesNotExistForPoolID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create a pool
		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create a snapshot
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			VolumeID:  volume.ID,
			AccountID: account.ID,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		// Try to get the snapshot with a non-matching pool ID
		retrievedSnapshot, err := store.GetSnapshotByPoolID(ctx, snapshot.UUID, account.ID, 999, false)
		assert.Error(tt, err, "Expected error when pool ID does not match")
		assert.Nil(tt, retrievedSnapshot, "Expected nil snapshot")
		assert.ErrorContains(tt, err, "Restore snapshots across pool is not supported", "Expected error 'snapshot not found for the given pool ID', got %v", err)
	})

	t.Run("WhenSnapshotDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Try to get a non-existent snapshot
		nonExistentUUID := "non-existent-uuid"
		retrievedSnapshot, err := store.GetSnapshotByPoolID(ctx, nonExistentUUID, 1, 1, false)
		assert.Error(tt, err, "Expected error when snapshot does not exist")
		assert.Nil(tt, retrievedSnapshot, "Expected nil snapshot")
		assert.ErrorContains(tt, err, "snapshot not found", "Expected error 'snapshot not found' not found', got %v", err)
	})
}

func TestDeleteSnapshot(t *testing.T) {
	t.Run("WhenSnapshotIsDeletedSuccessfully", func(tt *testing.T) {
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
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(snapshot).Error()
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		deletedSnapshot, err := store.DeleteSnapshot(context.Background(), snapshot.UUID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, snapshot.Name, deletedSnapshot.Name, "Expected snapshot name %v, got %v", snapshot.Name, deletedSnapshot.Name)
		assert.NotNil(tt, deletedSnapshot.DeletedAt, "Expected snapshot to be deleted, got %v", deletedSnapshot.DeletedAt)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, deletedSnapshot.State, "Expected snapshot state %v, got %v", datamodel.LifeCycleStateDeleted, deletedSnapshot.State)
		assert.Equal(tt, datamodel.LifeCycleStateDeletedDetails, deletedSnapshot.StateDetails, "Expected snapshot state details %v, got %v", "", deletedSnapshot.StateDetails)

		_, err = store.GetSnapshotByUUID(context.Background(), snapshot.UUID, account.ID, volume.ID)
		var vcpErr *vsaerrors.CustomError
		if vsaerrors.As(err, &vcpErr) {
			assert.True(tt, customerrors.IsNotFoundErr(vcpErr.Unwrap()), "Expected underlying NotFoundErr, got %v", vcpErr.Unwrap())
		} else {
			tt.Fatalf("Expected VCP CustomError, got %v", err)
		}
	})
	t.Run("WhenSnapshotIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		deletedSnapshot, err := store.DeleteSnapshot(context.Background(), "dummy")
		assert.Nil(tt, deletedSnapshot, "Expected nil snapshot, got %v", deletedSnapshot)
		var vcpErr *vsaerrors.CustomError
		if vsaerrors.As(err, &vcpErr) {
			assert.True(tt, customerrors.IsNotFoundErr(vcpErr.Unwrap()), "Expected underlying NotFoundErr, got %v", vcpErr.Unwrap())
		} else {
			tt.Fatalf("Expected VCP CustomError, got %v", err)
		}
	})
	t.Run("ReturnsErrorWhenSnapshotIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Mock startTransaction to return an error
		origStartTransaction := startTransaction
		startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
			return nil, errors.New("failed to start transaction")
		}
		defer func() { startTransaction = origStartTransaction }()

		deletedSnapshot, err := store.DeleteSnapshot(context.Background(), "non-existent-uuid")
		assert.Nil(tt, deletedSnapshot, "Expected nil snapshot, got %v", deletedSnapshot)
		assert.Error(tt, err, "Expected error when snapshot does not exist")
		assert.ErrorContains(tt, err, "failed to start transaction")
	})
}

func TestDeletingSnapshot(t *testing.T) {
	t.Run("ReturnsErrorWhenSnapshotDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

		snapshot := &datamodel.Snapshot{BaseModel: datamodel.BaseModel{UUID: "non-existent-uuid"}}

		err = store.DeletingSnapshot(context.Background(), snapshot)
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		assert.Error(tt, err)
	})

	t.Run("UpdatesSnapshotStateToDeletingSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

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

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     1,
			AccountID:    account.ID,
			Account:      account,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailable,
		}
		err = store.db.Create(snapshot).Error()
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		err = store.DeletingSnapshot(context.Background(), snapshot)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}

		updatedSnapshot := &datamodel.Snapshot{}
		err = store.db.GORM().First(updatedSnapshot, "uuid = ?", snapshot.UUID).Error
		if err != nil {
			tt.Fatalf("Failed to fetch updated pool: %v", err)
		}
		if updatedSnapshot.State != datamodel.LifeCycleStateDeleting {
			tt.Errorf("Expected state %v, got %v", datamodel.LifeCycleStateDeleting, updatedSnapshot.State)
		}
		if updatedSnapshot.StateDetails != datamodel.LifeCycleStateDeletingDetails {
			tt.Errorf("Expected state details %v, got %v", datamodel.LifeCycleStateDeletingDetails, updatedSnapshot.StateDetails)
		}
	})
}

func TestBatchDeleteSnapshots(t *testing.T) {
	t.Run("BatchDeleteSnapshotsSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

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
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
			Name:      "test_pool",
			AccountID: account.ID,
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-west-1a",
			},
		}
		err = store.db.Create(pool).Error()
		if err != nil {
			tt.Fatalf("Failed to create pool: %v", err)
		}

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		if err != nil {
			tt.Fatalf("Failed to create volume: %v", err)
		}

		snapshot1 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid-1", ID: 1},
			Name:         "test_snapshot-1",
			VolumeID:     1,
			AccountID:    account.ID,
			Account:      account,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailable,
		}
		snapshot2 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid-2", ID: 2},
			Name:         "test_snapshot-2",
			VolumeID:     1,
			AccountID:    account.ID,
			Account:      account,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailable,
		}

		err = store.db.Create(snapshot1).Error()
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		err = store.db.Create(snapshot2).Error()
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		deletedSnapshots, err := store.BatchDeleteSnapshots(context.Background(), []int64{snapshot1.ID, snapshot2.ID})
		if err != nil {
			tt.Fatalf("Failed to batch delete snapshots: %v", err)
		}
		assert.Len(tt, deletedSnapshots, 2)

		for _, snapshots := range deletedSnapshots {
			assert.Equal(tt, snapshots.Volume.Name, volume.Name)
		}

		updatedSnapshot1 := &datamodel.Snapshot{}
		err = store.db.GORM().Unscoped().First(updatedSnapshot1, "uuid = ?", snapshot1.UUID).Error
		if err != nil {
			tt.Fatalf("Failed to fetch updated snapshot1: %v", err)
		}

		updatedSnapshot2 := &datamodel.Snapshot{}
		err = store.db.GORM().Unscoped().First(updatedSnapshot2, "uuid = ?", snapshot2.UUID).Error
		if err != nil {
			tt.Fatalf("Failed to fetch updated snapshot2: %v", err)
		}
		assert.NotNil(tt, updatedSnapshot1.DeletedAt, "Expected DeletedAt to be not nil")
		assert.NotNil(tt, updatedSnapshot2.DeletedAt, "Expected DeletedAt to be not nil")

		assert.Equal(tt, datamodel.LifeCycleStateDeletedDetails, updatedSnapshot1.StateDetails)
		assert.Equal(tt, datamodel.LifeCycleStateDeletedDetails, updatedSnapshot2.StateDetails)

		assert.Equal(tt, datamodel.LifeCycleStateDeleted, updatedSnapshot1.State)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, updatedSnapshot2.State)
	})
}

func TestGetSnapshotsByVolumeID(t *testing.T) {
	t.Run("WhenSnapshotsExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create snapshots for the volume
		snapshot1 := &datamodel.Snapshot{
			Name:     "snap1",
			VolumeID: volume.ID,
		}
		snapshot2 := &datamodel.Snapshot{
			Name:     "snap2",
			VolumeID: volume.ID,
		}
		_, err = store.CreatingSnapshot(context.Background(), snapshot1)
		assert.NoError(tt, err, "Failed to create snapshot1")
		_, err = store.CreatingSnapshot(context.Background(), snapshot2)
		assert.NoError(tt, err, "Failed to create snapshot2")

		// Get snapshots by volume ID
		snapshots, err := store.GetSnapshotsByVolumeID(context.Background(), volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots")
		assert.GreaterOrEqual(tt, len(snapshots), 2, "Expected at least 2 snapshots")
		var foundSnap1, foundSnap2 bool
		for _, s := range snapshots {
			if s.Name == "snap1" {
				foundSnap1 = true
			}
			if s.Name == "snap2" {
				foundSnap2 = true
			}
		}
		assert.True(tt, foundSnap1, "Expected to find snap1")
		assert.True(tt, foundSnap2, "Expected to find snap2")
	})

	t.Run("WhenNoSnapshotsExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Get snapshots by volume ID (should be none)
		snapshots, err := store.GetSnapshotsByVolumeID(context.Background(), volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots slice")
		assert.Equal(tt, 0, len(snapshots), "Expected 0 snapshots")
	})
}

func TestGetSnapshotsByVolumeIDs(t *testing.T) {
	t.Run("GetSnapshotsByVolumeIDsSuccessful", func(tt *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			tt.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			tt.Fatalf("Failed to clean up test database: %v", err)
		}

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

		snapshot1 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid-1"},
			Name:         "test_snapshot-1",
			VolumeID:     1,
			AccountID:    account.ID,
			Account:      account,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailable,
		}
		snapshot2 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid-2"},
			Name:         "test_snapshot-2",
			VolumeID:     1,
			AccountID:    account.ID,
			Account:      account,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateAvailable,
		}

		err = store.db.Create(snapshot1).Error()
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}
		err = store.db.Create(snapshot2).Error()
		if err != nil {
			tt.Fatalf("Failed to create snapshot: %v", err)
		}

		snapshots, err := store.GetSnapshotsByVolumeIDs(context.Background(), []int64{snapshot1.ID, snapshot2.ID, 3})
		assert.NoError(tt, err)
		assert.Len(tt, snapshots, 2)
	})
}

func TestGetReplicationSnapshotsByVolumeID(t *testing.T) {
	t.Run("WhenSnapshotsExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create snapshots for the volume
		snapshot1 := &datamodel.Snapshot{
			Name:     "snap1",
			VolumeID: volume.ID,
			BaseModel: datamodel.BaseModel{
				UUID: "snapmirror-snapshot-uuid5",
				//	DeletedAt: nil,
			},
		}
		snapshot2 := &datamodel.Snapshot{
			Name:     "snapmirror.snap2",
			VolumeID: volume.ID,
			BaseModel: datamodel.BaseModel{
				UUID: "snapmirror-snapshot-uuid",
				DeletedAt: &gorm.DeletedAt{
					Time: time.Now(),
				},
			},
		}

		_, err = store.CreatingSnapshot(context.Background(), snapshot1)
		assert.NoError(tt, err, "Failed to create snapshot1")
		_, err = store.CreatingSnapshot(context.Background(), snapshot2)
		assert.NoError(tt, err, "Failed to create snapshot2")

		// Get snapshots by volume ID
		snapshots, err := store.GetReplicationSnapshotsByVolumeID(context.Background(), volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots")
		assert.Equal(tt, len(snapshots), 1)
	})
	t.Run("WhenNoSnapshotsExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Get snapshots by volume ID (should be none)
		snapshots, err := store.GetReplicationSnapshotsByVolumeID(context.Background(), volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots slice")
		assert.Equal(tt, 0, len(snapshots), "Expected 0 snapshots")
	})
}

func TestGetSnapshotsWithConditions(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	t.Run("WhenSnapshotExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create a snapshot
		snapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "test_snapshot",
			VolumeID:  volume.ID,
			State:     datamodel.LifeCycleStateAvailable,
			AccountID: 1,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		// Query for the snapshot using conditions
		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account_id", "=", 1),
			dbutils.NewFilterCondition("volume_id", "=", volume.ID),
			dbutils.NewFilterCondition("name", "=", "test_snapshot"),
		)
		snapshots, err := store.GetSnapshotsWithCondition(ctx, *filter)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, snapshots, 1, "Expected 1 snapshot, got %d", len(snapshots))
		assert.Equal(tt, snapshot.UUID, snapshots[0].UUID, "Expected UUID %v, got %v", snapshot.UUID, snapshots[0].UUID)
		assert.Equal(tt, snapshot.Name, snapshots[0].Name, "Expected name %v, got %v", snapshot.Name, snapshots[0].Name)
		assert.Equal(tt, snapshot.State, snapshots[0].State, "Expected state %v, got %v", snapshot.State, snapshots[0].State)
		assert.Equal(tt, volume.Name, snapshots[0].Volume.Name, "Expected VolumeName %v, got %v", volume.Name, snapshots[0].Volume.Name)
	})

	t.Run("WhenSnapshotDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Query for a snapshot that does not exist
		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account_id", "=", 999),
			dbutils.NewFilterCondition("volume_id", "=", 999),
			dbutils.NewFilterCondition("name", "=", "non-existent-snapshot"),
		)
		snapshots, err := store.GetSnapshotsWithCondition(ctx, *filter)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, snapshots, 0, "Expected 0 snapshots, got %d", len(snapshots))
	})
	t.Run("WhenFilterConditionsAreInvalid", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		filter := dbutils.CreateFilterWithConditions(
			dbutils.NewFilterCondition("account", "=", 999),
		)

		_, err = store.GetSnapshotsWithCondition(context.Background(), *filter)
		assert.Error(t, err)
	})
}

func TestUnDeleteSnapshot(t *testing.T) {
	t.Run("RestoresDeletedSnapshotSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid", DeletedAt: &gorm.DeletedAt{}},
			VolumeID:     volume.ID,
			State:        datamodel.LifeCycleStateDeleted,
			StateDetails: datamodel.LifeCycleStateDeletedDetails,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		err = store.UnDeleteSnapshot(context.Background(), snapshot)
		assert.NoError(tt, err)

		updated := &datamodel.Snapshot{}
		err = store.db.GORM().First(updated, "uuid = ?", snapshot.UUID).Error
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, updated.State)
		assert.Equal(tt, datamodel.LifeCycleStateReadyDetails, updated.StateDetails)
		assert.True(tt, updated.DeletedAt == nil || !updated.DeletedAt.Valid)
	})

	t.Run("ReturnsErrorWhenSnapshotIsNil", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		err = store.UnDeleteSnapshot(context.Background(), nil)
		assert.Error(tt, err)
	})
}

func TestGetWronglyDeletedSnapshot(t *testing.T) {
	t.Run("ReturnsSoftDeletedSnapshotByExternalUUID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid", DeletedAt: &gorm.DeletedAt{Time: time.Now(), Valid: true}},
			Name:               "wrongly_deleted_snapshot",
			VolumeID:           volume.ID,
			ExternalUUID:       "ext-uuid-1",
			SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-uuid-1"},
			State:              datamodel.LifeCycleStateDeleted,
			StateDetails:       datamodel.LifeCycleStateDeletedDetails,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		result, err := store.GetWronglyDeletedSnapshot(context.Background(), "ext-uuid-1")
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "test-snapshot-uuid", result.UUID)
		assert.Equal(tt, "ext-uuid-1", result.ExternalUUID)
		assert.NotNil(tt, result.Volume)
		assert.Equal(tt, "test_volume", result.Volume.Name)
	})

	t.Run("ReturnsErrorWhenNoMatch", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		result, err := store.GetWronglyDeletedSnapshot(context.Background(), "non-existent-external-uuid")
		assert.Error(tt, err)
		assert.Nil(tt, result)
	})
}

func TestBatchCreateSnapshots(t *testing.T) {
	db, err := SetupTestDB()
	if err != nil {
		t.Fatalf("Failed to set up test database: %v", err)
	}
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	_ = ClearInMemoryDB(store.db.GORM())

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"}, Name: "test_account"}
	_ = store.db.Create(account).Error()

	snapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{}, Name: "batch_create_1", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateAvailable},
		{BaseModel: datamodel.BaseModel{}, Name: "batch_create_2", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateAvailable},
	}
	uuids, err := store.BatchCreateSnapshots(context.Background(), snapshots, true)
	if err != nil {
		t.Fatalf("BatchCreateSnapshots failed: %v", err)
	}
	if len(uuids) != 2 {
		t.Fatalf("Expected 2 UUIDs, got %d", len(uuids))
	}
}

func TestBatchUpdateSnapshots(t *testing.T) {
	db, err := SetupTestDB()
	if err != nil {
		t.Fatalf("Failed to set up test database: %v", err)
	}
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	_ = ClearInMemoryDB(store.db.GORM())

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"}, Name: "test_account"}
	_ = store.db.Create(account).Error()

	snapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{}, Name: "batch_update_1", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateAvailable},
		{BaseModel: datamodel.BaseModel{}, Name: "batch_update_2", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateAvailable},
	}
	uuids, err := store.BatchCreateSnapshots(context.Background(), snapshots, true)
	if err != nil {
		t.Fatalf("BatchCreateSnapshots failed: %v", err)
	}
	for i, uuid := range uuids {
		snapshots[i].UUID = uuid
		snapshots[i].State = datamodel.LifeCycleStateDeleted
		snapshots[i].StateDetails = datamodel.LifeCycleStateDeletedDetails
	}
	err = store.BatchUpdateSnapshots(context.Background(), snapshots)
	if err != nil {
		t.Fatalf("BatchUpdateSnapshots failed: %v", err)
	}
}

func TestBatchUnDeleteSnapshots(t *testing.T) {
	db, err := SetupTestDB()
	if err != nil {
		t.Fatalf("Failed to set up test database: %v", err)
	}
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	_ = ClearInMemoryDB(store.db.GORM())

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"}, Name: "test_account"}
	_ = store.db.Create(account).Error()

	snapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{}, Name: "batch_undelete_1", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateDeleted, StateDetails: datamodel.LifeCycleStateDeletedDetails},
		{BaseModel: datamodel.BaseModel{}, Name: "batch_undelete_2", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateDeleted, StateDetails: datamodel.LifeCycleStateDeletedDetails},
	}
	uuids, err := store.BatchCreateSnapshots(context.Background(), snapshots, true)
	if err != nil {
		t.Fatalf("BatchCreateSnapshots failed: %v", err)
	}
	for i, uuid := range uuids {
		snapshots[i].UUID = uuid
	}
	err = store.BatchUnDeleteSnapshots(context.Background(), snapshots)
	if err != nil {
		t.Fatalf("BatchUnDeleteSnapshots failed: %v", err)
	}
}

func TestBatchGetSnapshotsByUUIDs(t *testing.T) {
	db, err := SetupTestDB()
	if err != nil {
		t.Fatalf("Failed to set up test database: %v", err)
	}
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	_ = ClearInMemoryDB(store.db.GORM())

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"}, Name: "test_account"}
	_ = store.db.Create(account).Error()

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:      "test_pool",
		AccountID: account.ID,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone: "us-east-1a",
		},
	}
	_ = store.db.Create(pool).Error()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
		Name:      "test_volume",
		AccountID: account.ID,
		PoolID:    pool.ID,
	}
	err = store.db.Create(volume).Error()
	if err != nil {
		t.Fatalf("Failed to create volume: %v", err)
	}

	snapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{}, Name: "batch_get_1", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateAvailable},
		{BaseModel: datamodel.BaseModel{}, Name: "batch_get_2", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateAvailable},
	}
	uuids, err := store.BatchCreateSnapshots(context.Background(), snapshots, true)
	if err != nil {
		t.Fatalf("BatchCreateSnapshots failed: %v", err)
	}
	fetched, err := store.BatchGetSnapshotsByUUIDs(context.Background(), uuids)
	if err != nil {
		t.Fatalf("BatchGetSnapshotsByUUIDs failed: %v", err)
	}
	if len(fetched) != 2 {
		t.Fatalf("Expected 2 snapshots, got %d", len(fetched))
	}
}

func TestBatchGetWronglyDeletedSnapshots(t *testing.T) {
	db, err := SetupTestDB()
	if err != nil {
		t.Fatalf("Failed to set up test database: %v", err)
	}
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	_ = ClearInMemoryDB(store.db.GORM())

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"}, Name: "test_account"}
	_ = store.db.Create(account).Error()

	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-pool-uuid"},
		Name:      "test_pool",
		AccountID: account.ID,
		PoolAttributes: &datamodel.PoolAttributes{
			PrimaryZone: "us-east-1a",
		},
	}
	_ = store.db.Create(pool).Error()

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
		Name:      "test_volume",
		AccountID: account.ID,
		PoolID:    pool.ID,
	}
	err = store.db.Create(volume).Error()
	if err != nil {
		t.Fatalf("Failed to create volume: %v", err)
	}

	snapshots := []*datamodel.Snapshot{
		{BaseModel: datamodel.BaseModel{}, Name: "batch_wrongly_deleted_1", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateDeleted, StateDetails: datamodel.LifeCycleStateDeletedDetails, ExternalUUID: "ext-uuid-1", SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-uuid-1"}},
		{BaseModel: datamodel.BaseModel{}, Name: "batch_wrongly_deleted_2", VolumeID: 1, AccountID: account.ID, Account: account, State: datamodel.LifeCycleStateDeleted, StateDetails: datamodel.LifeCycleStateDeletedDetails, ExternalUUID: "ext-uuid-2", SnapshotAttributes: &datamodel.SnapshotAttributes{ExternalUUID: "ext-uuid-2"}},
	}
	uuids, err := store.BatchCreateSnapshots(context.Background(), snapshots, true)
	if err != nil {
		t.Fatalf("BatchCreateSnapshots failed: %v", err)
	}
	for i, uuid := range uuids {
		snapshots[i].UUID = uuid
	}
	externalUUIDs := []string{"ext-uuid-1", "ext-uuid-2"}
	fetched, err := store.BatchGetWronglyDeletedSnapshots(context.Background(), externalUUIDs)
	if err != nil {
		t.Fatalf("BatchGetWronglyDeletedSnapshots failed: %v", err)
	}
	if len(fetched) != 2 {
		t.Fatalf("Expected 2 wrongly deleted snapshots, got %d", len(fetched))
	}
}

// TestUpdateSnapshot_PersistsExternalUUIDToColumnAndAttributes guards write in both
// the external_uuid column and in the snapshot_attributes.
func TestUpdateSnapshot_PersistsExternalUUIDToColumnAndAttributes(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)
	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "dual-write-account-uuid"}, Name: "dual_write_account"}
	assert.NoError(t, store.db.Create(account).Error())

	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{ID: 1, UUID: "dual-write-pool-uuid"},
		Name:           "dual_write_pool",
		AccountID:      account.ID,
		PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "us-east-1a"},
	}
	assert.NoError(t, store.db.Create(pool).Error())

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "dual-write-volume-uuid"},
		Name:      "dual_write_volume",
		AccountID: account.ID,
		PoolID:    pool.ID,
	}
	assert.NoError(t, store.db.Create(volume).Error())

	created, err := store.CreatingSnapshot(ctx, &datamodel.Snapshot{
		Name:               "dual_write_snapshot",
		VolumeID:           volume.ID,
		AccountID:          account.ID,
		SnapshotAttributes: &datamodel.SnapshotAttributes{},
	})
	assert.NoError(t, err)

	created.SetExternalUUID("ontap-external-uuid")
	created.State = datamodel.LifeCycleStateREADY
	created.StateDetails = datamodel.LifeCycleStateAvailableDetails
	_, err = store.UpdateSnapshot(ctx, created)
	assert.NoError(t, err)

	var persisted datamodel.Snapshot
	assert.NoError(t, store.db.GORM().First(&persisted, "uuid = ?", created.UUID).Error)
	assert.Equal(t, "ontap-external-uuid", persisted.ExternalUUID)
	assert.NotNil(t, persisted.SnapshotAttributes)
	assert.Equal(t, "ontap-external-uuid", persisted.SnapshotAttributes.ExternalUUID)

	// Assert on the stored JSON directly so the mirror can't be satisfied by struct defaults alone.
	var rawAttributes string
	assert.NoError(t, store.db.GORM().Model(&datamodel.Snapshot{}).
		Where("uuid = ?", created.UUID).
		Select("snapshot_attributes").
		Scan(&rawAttributes).Error)
	assert.Contains(t, rawAttributes, `"external_uuid":"ontap-external-uuid"`)
}

func TestGetSnapshotsByTypeAndVolumeID(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	t.Run("WhenSnapshotsOfSpecificTypeExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create snapshots with different types
		snapshot1 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{},
			Name:         "adhoc-backup-snap1",
			Type:         "adhoc-backup",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}
		snapshot2 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{},
			Name:         "adhoc-backup-snap2",
			Type:         "adhoc-backup",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}
		snapshot3 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{},
			Name:         "scheduled-backup-snap",
			Type:         "scheduled-backup",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		// Create snapshots with time delay to test ordering
		_, err = store.CreatingSnapshot(ctx, snapshot1)
		assert.NoError(tt, err, "Failed to create snapshot1")
		time.Sleep(10 * time.Millisecond)

		_, err = store.CreatingSnapshot(ctx, snapshot2)
		assert.NoError(tt, err, "Failed to create snapshot2")
		time.Sleep(10 * time.Millisecond)

		_, err = store.CreatingSnapshot(ctx, snapshot3)
		assert.NoError(tt, err, "Failed to create snapshot3")

		// Get snapshots by type
		snapshots, err := store.GetSnapshotsByTypeAndVolumeID(ctx, "adhoc-backup", volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots")
		assert.Equal(tt, 2, len(snapshots), "Expected 2 adhoc-backup snapshots")

		// Verify ordering (newest first)
		assert.Equal(tt, "adhoc-backup-snap2", snapshots[0].Name, "Expected snap2 to be first (newest)")
		assert.Equal(tt, "adhoc-backup-snap1", snapshots[1].Name, "Expected snap1 to be second (oldest)")

		// Verify preloaded data
		assert.NotNil(tt, snapshots[0].Volume, "Expected Volume to be preloaded")
		assert.NotNil(tt, snapshots[0].Account, "Expected Account to be preloaded")
		assert.Equal(tt, volume.Name, snapshots[0].Volume.Name, "Expected correct volume name")
		assert.Equal(tt, account.Name, snapshots[0].Account.Name, "Expected correct account name")
	})

	t.Run("WhenSnapshotsInErrorStateExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create snapshots with different states
		snapshot1 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{},
			Name:         "adhoc-backup-ready",
			Type:         "adhoc-backup",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}
		snapshot2 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{},
			Name:         "adhoc-backup-error",
			Type:         "adhoc-backup",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateError,
			StateDetails: "Some error occurred",
		}
		snapshot3 := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{},
			Name:         "adhoc-backup-creating",
			Type:         "adhoc-backup",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
		}

		_, err = store.CreatingSnapshot(ctx, snapshot1)
		assert.NoError(tt, err, "Failed to create snapshot1")

		err = store.db.Create(snapshot2).Error()
		assert.NoError(tt, err, "Failed to create snapshot2")

		_, err = store.CreatingSnapshot(ctx, snapshot3)
		assert.NoError(tt, err, "Failed to create snapshot3")

		// Get snapshots by type (should exclude error state)
		snapshots, err := store.GetSnapshotsByTypeAndVolumeID(ctx, "adhoc-backup", volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots")
		assert.Equal(tt, 2, len(snapshots), "Expected 2 snapshots (excluding error state)")

		// Verify no error state snapshots are returned
		for _, snap := range snapshots {
			assert.NotEqual(tt, datamodel.LifeCycleStateError, snap.State, "Should not return snapshots in error state")
		}
	})

	t.Run("WhenNoSnapshotsOfTypeExistForVolume", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create a snapshot with different type
		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{},
			Name:         "scheduled-backup-snap",
			Type:         "scheduled-backup",
			VolumeID:     volume.ID,
			AccountID:    1,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}
		_, err = store.CreatingSnapshot(ctx, snapshot)
		assert.NoError(tt, err, "Failed to create snapshot")

		// Get snapshots by different type
		snapshots, err := store.GetSnapshotsByTypeAndVolumeID(ctx, "adhoc-backup", volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots slice")
		assert.Equal(tt, 0, len(snapshots), "Expected 0 snapshots of type adhoc-backup")
	})

	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Get snapshots for non-existent volume
		nonExistentVolumeID := int64(999)
		snapshots, err := store.GetSnapshotsByTypeAndVolumeID(ctx, "adhoc-backup", nonExistentVolumeID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, snapshots, "Expected non-nil snapshots slice")
		assert.Equal(tt, 0, len(snapshots), "Expected 0 snapshots for non-existent volume")
	})

	t.Run("WhenMultipleSnapshotsExistVerifyOrdering", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create an account first
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create a volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create multiple snapshots with specific creation times
		baseTime := time.Now()
		snapshots := []struct {
			name      string
			createdAt time.Time
		}{
			{"snap1", baseTime.Add(-3 * time.Hour)},
			{"snap2", baseTime.Add(-2 * time.Hour)},
			{"snap3", baseTime.Add(-1 * time.Hour)},
			{"snap4", baseTime},
		}

		for i, s := range snapshots {
			snapshot := &datamodel.Snapshot{
				BaseModel: datamodel.BaseModel{
					UUID:      fmt.Sprintf("snapshot-uuid-%d", i+1),
					CreatedAt: s.createdAt,
				},
				Name:         s.name,
				VolumeID:     volume.ID,
				AccountID:    account.ID,
				Type:         "adhoc-backup",
				State:        datamodel.LifeCycleStateREADY,
				StateDetails: datamodel.LifeCycleStateAvailable,
			}
			err = store.db.Create(snapshot).Error()
			assert.NoError(tt, err, "Failed to create snapshot %s", s.name)
		}

		// Get snapshots and verify ordering
		retrievedSnapshots, err := store.GetSnapshotsByTypeAndVolumeID(ctx, "adhoc-backup", volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 4, len(retrievedSnapshots), "Expected 4 snapshots")

		// Verify descending order by creation time
		assert.Equal(tt, "snap4", retrievedSnapshots[0].Name, "Expected newest snapshot first")
		assert.Equal(tt, "snap3", retrievedSnapshots[1].Name)
		assert.Equal(tt, "snap2", retrievedSnapshots[2].Name)
		assert.Equal(tt, "snap1", retrievedSnapshots[3].Name, "Expected oldest snapshot last")
	})
}

func TestUpdateSnapshotForHandleResource(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	t.Run("SuccessfullyUpdatesSnapshotWhenInReadyState", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
			Description:  "Original description",
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "updated_snapshot",
			Description:        "Updated description",
			State:              datamodel.LifeCycleStateAvailable,
			StateDetails:       datamodel.LifeCycleStateAvailableDetails,
			ExternalUUID:       "external-uuid-123",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "updated_snapshot", result.Name)
		assert.Equal(tt, "Updated description", result.Description)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateAvailableDetails, result.StateDetails)
		assert.Equal(tt, "external-uuid-123", result.ExternalUUID)
	})

	t.Run("StartTransactionFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")
		origStartTransaction := startTransaction
		startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
			return nil, errors.New("failed to start transaction")
		}
		defer func() { startTransaction = origStartTransaction }()

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
			Description:  "Original description",
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "updated_snapshot",
			Description:        "Updated description",
			State:              datamodel.LifeCycleStateAvailable,
			StateDetails:       datamodel.LifeCycleStateAvailableDetails,
			ExternalUUID:       "external-uuid-123",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}

		_, err = store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "failed to start transaction")
	})

	t.Run("GetSnapshotWithDetailsFails", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
			Description:  "Original description",
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		wrongSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "wrong-snapshot-uuid"},
		}

		_, err = store.UpdateSnapshotForHandleResource(ctx, wrongSnapshot)
		assert.Error(tt, err)
		assert.EqualError(tt, err, "Resource not found")
	})

	t.Run("SuccessfullyUpdatesSnapshotWhenInAvailableState", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "updated_snapshot",
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "updated_snapshot", result.Name)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateReadyDetails, result.StateDetails)
	})

	t.Run("ReturnsErrorWhenSnapshotIsInCreatingState", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateCreating,
			StateDetails: datamodel.LifeCycleStateCreatingDetails,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "updated_snapshot",
			State:     datamodel.LifeCycleStateAvailable,
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.Error(tt, err)
		assert.Nil(tt, result)

		assert.Contains(tt, err.Error(), "Cannot delete resource while it is transitioning")
	})

	t.Run("ReturnsErrorWhenSnapshotIsInDeletingState", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateDeleting,
			StateDetails: datamodel.LifeCycleStateDeletingDetails,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "updated_snapshot",
			State:     datamodel.LifeCycleStateAvailable,
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.Error(tt, err)
		assert.Nil(tt, result)

		assert.Contains(tt, err.Error(), "Cannot delete resource while it is transitioning")
	})

	t.Run("ReturnsErrorWhenSnapshotIsInUpdatingState", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateUpdating,
			StateDetails: datamodel.LifeCycleStateUpdatingDetails,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "updated_snapshot",
			State:     datamodel.LifeCycleStateAvailable,
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.Error(tt, err)
		assert.Nil(tt, result)

		assert.Contains(tt, err.Error(), "Cannot delete resource while it is transitioning")
	})

	t.Run("ReturnsErrorWhenSnapshotNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "non-existent-uuid"},
			Name:      "updated_snapshot",
			State:     datamodel.LifeCycleStateAvailable,
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)

		if err != nil {
			assert.Error(tt, err)
			assert.Nil(tt, result)
		} else {
			tt.Logf("Expected error but got none - this indicates the bug in the function")
		}
	})

	t.Run("HandlesTransactionRollbackOnDatabaseUpdateError", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "test_snapshot",
			VolumeID:     volume.ID,
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateREADY,
			StateDetails: datamodel.LifeCycleStateReadyDetails,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		err = db.Migrator().DropTable(&datamodel.Snapshot{})
		assert.NoError(tt, err, "Failed to drop table")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel: datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:      "updated_snapshot",
			State:     datamodel.LifeCycleStateAvailable,
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)

		if err != nil {
			assert.Error(tt, err)
			assert.Nil(tt, result)
		} else {
			tt.Logf("Expected error due to dropped table but got none. This indicates the function bug. Result: %+v", result)
		}
	})

	t.Run("VerifiesUpdateIntegrityWithCompleteDataSet", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		originalSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "original_snapshot",
			Description:        "Original description",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			State:              datamodel.LifeCycleStateREADY,
			StateDetails:       datamodel.LifeCycleStateReadyDetails,
			ExternalUUID:       "original-external-uuid",
			SnapshotAttributes: &datamodel.SnapshotAttributes{},
		}
		err = store.db.Create(originalSnapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel:    datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:         "completely_updated_snapshot",
			Description:  "Completely updated description",
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
			ExternalUUID: "updated-external-uuid",
			SnapshotAttributes: &datamodel.SnapshotAttributes{
				SizeInBytes:            1024000,
				LogicalSizeUsedInBytes: 512000,
			},
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)

		assert.Equal(tt, "completely_updated_snapshot", result.Name)
		assert.Equal(tt, "Completely updated description", result.Description)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateAvailableDetails, result.StateDetails)
		assert.Equal(tt, "updated-external-uuid", result.ExternalUUID)
		assert.Equal(tt, int64(1024000), result.SnapshotAttributes.SizeInBytes)
		assert.Equal(tt, int64(512000), result.SnapshotAttributes.LogicalSizeUsedInBytes)

		fetchedSnapshot, err := store.GetSnapshotByUUID(ctx, "test-snapshot-uuid", account.ID, volume.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, "completely_updated_snapshot", fetchedSnapshot.Name)
		assert.Equal(tt, "Completely updated description", fetchedSnapshot.Description)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, fetchedSnapshot.State)
		assert.Equal(tt, datamodel.LifeCycleStateAvailableDetails, fetchedSnapshot.StateDetails)
		assert.Equal(tt, "updated-external-uuid", fetchedSnapshot.ExternalUUID)
		assert.Equal(tt, int64(1024000), fetchedSnapshot.SnapshotAttributes.SizeInBytes)
		assert.Equal(tt, int64(512000), fetchedSnapshot.SnapshotAttributes.LogicalSizeUsedInBytes)
	})

	t.Run("HandlesNilSnapshotAttributes", func(tt *testing.T) {
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
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid", ID: 1},
			Name:      "test_volume",
			AccountID: account.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		snapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "test_snapshot",
			VolumeID:           volume.ID,
			AccountID:          account.ID,
			State:              datamodel.LifeCycleStateREADY,
			StateDetails:       datamodel.LifeCycleStateReadyDetails,
			SnapshotAttributes: nil,
		}
		err = store.db.Create(snapshot).Error()
		assert.NoError(tt, err, "Failed to create snapshot")

		updateSnapshot := &datamodel.Snapshot{
			BaseModel:          datamodel.BaseModel{UUID: "test-snapshot-uuid"},
			Name:               "updated_snapshot",
			State:              datamodel.LifeCycleStateAvailable,
			StateDetails:       datamodel.LifeCycleStateAvailableDetails,
			SnapshotAttributes: nil,
		}

		result, err := store.UpdateSnapshotForHandleResource(ctx, updateSnapshot)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "updated_snapshot", result.Name)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result.State)
	})
}
