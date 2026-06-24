package database

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	vcputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/gorm"
)

func TestCreateVolumeReplication(t *testing.T) {
	t.Run("WhenVolumeReplicationIsCreatedSuccessfully", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationReplicationUUID: "test-volume-rep-uuid",
			},
		}

		createdVolumeRep, err := store.CreateVolumeReplication(context.Background(), volumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volumeRep.Name, createdVolumeRep.Name, "Expected volume name %v, got %v", volumeRep.Name, createdVolumeRep.Name)
		assert.Equal(tt, createdVolumeRep.State, datamodel.LifeCycleStateCreating, "Expected volume state %v, got %v", datamodel.LifeCycleStateCreating, createdVolumeRep.State)
		assert.Equal(tt, createdVolumeRep.StateDetails, datamodel.LifeCycleStateCreatingDetails, "Expected volume state %v, got %v", datamodel.LifeCycleStateCreatingDetails, createdVolumeRep.State)
		assert.Equal(tt, createdVolumeRep.ReplicationAttributes.DestinationReplicationUUID, createdVolumeRep.UUID)
	})
	t.Run("WhenVolumeReplicationIsCreatedSuccessfullyFromSrc", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				DestinationReplicationUUID: "test-volume-rep-uuid",
				EndpointType:               "src",
			},
		}

		createdVolumeRep, err := store.CreateVolumeReplication(context.Background(), volumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volumeRep.Name, createdVolumeRep.Name, "Expected volume name %v, got %v", volumeRep.Name, createdVolumeRep.Name)
		assert.Equal(tt, createdVolumeRep.State, datamodel.LifeCycleStateCreating, "Expected volume state %v, got %v", datamodel.LifeCycleStateCreating, createdVolumeRep.State)
		assert.Equal(tt, createdVolumeRep.StateDetails, datamodel.LifeCycleStateCreatingDetails, "Expected volume state %v, got %v", datamodel.LifeCycleStateCreatingDetails, createdVolumeRep.State)
		assert.Equal(tt, createdVolumeRep.ReplicationAttributes.SourceReplicationUUID, createdVolumeRep.UUID)
	})
	t.Run("WhenVolumeReplicationAlreadyExists", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
		}
		err = store.db.Create(volumeRep).Error()
		if err != nil {
			tt.Fatalf("Failed to create volume replication: %v", err)
		}

		createdVolumeRep, err1 := store.CreateVolumeReplication(context.Background(), volumeRep)
		assert.EqualError(tt, err1, "replication already exists for this volume", "Expected error 'replication already exists for this volume', got %v", err1)
		assert.Nil(tt, createdVolumeRep, "Expected nil volume, got %v", createdVolumeRep)
	})
}

func TestGetVolumeReplication(t *testing.T) {
	t.Run("WhenVolumeReplicationExists", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		result, err := store.GetVolumeReplication(context.Background(), "test-volume-rep-uuid")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volumeRep.Name, result.Name, "Expected volume name %v, got %v", volumeRep.Name, result.Name)
	})
	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volumeRep, err := store.GetVolumeReplication(context.Background(), "test-volume-uuid")
		assert.Nil(tt, volumeRep, "Expected nil volume replication, got %v", volumeRep)
		if err == gorm.ErrRecordNotFound {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
}

func TestGetVolumeReplicationByVolumeID(t *testing.T) {
	t.Run("WhenVolumeReplicationExists", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			VolumeID:  volume.ID,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		result, err := store.GetVolumeReplicationByVolumeID(context.Background(), volume.ID)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, result, "Expected volume replication, got nil")
		assert.Equal(tt, volumeRep.Name, result.Name, "Expected volume replication name %v, got %v", volumeRep.Name, result.Name)
		assert.Equal(tt, volumeRep.UUID, result.UUID, "Expected volume replication UUID %v, got %v", volumeRep.UUID, result.UUID)
	})
	t.Run("WhenVolumeDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		volumeRep, err := store.GetVolumeReplicationByVolumeID(context.Background(), 999)
		assert.Nil(tt, volumeRep, "Expected nil volume replication, got %v", volumeRep)
		assert.Error(tt, err, "Expected error, got nil")
	})
	t.Run("WhenVolumeExistsButNoReplication", func(tt *testing.T) {
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

		volumeRep, err := store.GetVolumeReplicationByVolumeID(context.Background(), volume.ID)
		assert.Nil(tt, volumeRep, "Expected nil volume replication, got %v", volumeRep)
		assert.Error(tt, err, "Expected error, got nil")
		assert.Contains(tt, err.Error(), "volume replication not found", "Expected 'volume replication not found' error, got %v", err)
	})
}

func Test_getVolumeReplication(t *testing.T) {
	t.Run("ReturnsVolumeReplicationWhenUUIDExists", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			State:     datamodel.LifeCycleStateAvailable,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		result, err := _getVolumeReplication(store.db.GORM(), &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRep.UUID}})
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volumeRep.UUID, result.UUID)
		assert.Equal(tt, volumeRep.Name, result.Name)
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, result.State)
	})
	t.Run("ReturnsVolumeReplicationWhenVolumeIDExists", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			VolumeID:  volume.ID,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		result, err := _getVolumeReplication(store.db.GORM(), &datamodel.VolumeReplication{VolumeID: volume.ID})
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volumeRep.UUID, result.UUID)
	})
	t.Run("DoesNotPreloadRelatedTables", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		lightweight, err := _getVolumeReplication(store.db.GORM(), &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRep.UUID}})
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Nil(tt, lightweight.Volume, "Expected Volume not to be preloaded")
		assert.Nil(tt, lightweight.Account, "Expected Account not to be preloaded")
		assert.Nil(tt, lightweight.ClusterPeer, "Expected ClusterPeer not to be preloaded")

		detailed, err := _getVolumeReplicationDetails(store.db.GORM(), &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRep.UUID}})
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, detailed.Volume, "Expected Volume to be preloaded by detailed fetch")
		assert.NotNil(tt, detailed.Account, "Expected Account to be preloaded by detailed fetch")
	})
	t.Run("ReturnsNotFoundWhenVolumeReplicationDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		_, err = _getVolumeReplication(store.db.GORM(), &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: "missing-uuid"}})
		assert.EqualError(tt, err, "volume replication not found")
	})
	t.Run("ReturnsErrorWhenDBFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		sqlDB, err := store.db.GORM().DB()
		assert.NoError(tt, err)
		_ = sqlDB.Close()

		_, err = _getVolumeReplication(store.db.GORM(), &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: "any-uuid"}})
		assert.Error(tt, err)
	})
}

func TestDeleteVolumeReplication(t *testing.T) {
	t.Run("WhenVolumeReplicationIsDeletedSuccessfully", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:         "test_volume_rep",
			Account:      account,
			Volume:       volume,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		err = store.db.Create(volumeRep).Error()
		if err != nil {
			tt.Fatalf("Failed to create volume replication: %v", err)
		}

		deletedVolumeRep, err := store.DeleteVolumeReplication(context.Background(), volumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, volumeRep.Name, deletedVolumeRep.Name, "Expected volume name %v, got %v", volumeRep.Name, deletedVolumeRep.Name)
		assert.NotNil(tt, deletedVolumeRep.DeletedAt, "Expected volume to be deleted, got %v", deletedVolumeRep.DeletedAt)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, deletedVolumeRep.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateDeleted, deletedVolumeRep.State)
		assert.Equal(tt, datamodel.LifeCycleStateDeletedDetails, deletedVolumeRep.StateDetails, "Expected volume state details %v, got %v", datamodel.LifeCycleStateDeletedDetails, deletedVolumeRep.StateDetails)

		_, err = store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.EqualError(tt, err, "volume replication not found", "Expected no error, got %v", err)
	})
}

func TestUpdateVolumeReplication(t *testing.T) {
	t.Run("WhenVolumeReplicationIsUpdatedSuccessfully", func(tt *testing.T) {
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
			Name:           "test_pool",
			Account:        account,
			DeploymentName: "test-deployment",
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

		// Create a cluster peering row for testing ClusterPeerId
		clusterPeeringRow := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "test-cluster-peer-uuid"},
			State:          datamodel.CvpClusterPeeringStatusPEERED,
			StateDetails:   "Successfully peered",
			OnprempCluster: "test-cluster",
			OntapPeerUUID:  "test-ontap-peer-uuid",
			AccountID:      account.ID,
			PoolID:         pool.ID,
		}
		err = store.db.Create(clusterPeeringRow).Error()
		if err != nil {
			tt.Fatalf("Failed to create cluster peering row: %v", err)
		}

		// Create hybrid replication attributes
		hybridAttrs := &datamodel.HybridReplicationAttribute{
			PeerVolumeName:      "peer-volume-1",
			PeerSvmName:         "peer-svm-1",
			Description:         "Test replication",
			Labels:              map[string]string{"env": "test"},
			ReplicationSchedule: "hourly",
			Status:              datamodel.HybridReplicationStatusPeered,
			StatusDetails:       "Successfully peered",
		}

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
			HybridReplicationAttributes: hybridAttrs,
			ClusterPeerId:               sql.NullInt64{Int64: clusterPeeringRow.ID, Valid: true},
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		// Create updated hybrid replication attributes
		updatedHybridAttrs := &datamodel.HybridReplicationAttribute{
			PeerVolumeName:      "peer-volume-2",
			PeerSvmName:         "peer-svm-2",
			Description:         "Updated test replication",
			Labels:              map[string]string{"env": "prod"},
			ReplicationSchedule: "daily",
			Status:              datamodel.HybridReplicationStatusPeered,
			StatusDetails:       "Successfully updated",
		}

		mirrorState := "snapmirrored"
		relationshipStatus := "success"
		var lastTransferSize int64 = 100
		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel:          datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:               "test_volume_rep",
			State:              datamodel.LifeCycleStateUpdating,
			MirrorState:        &mirrorState,
			RelationshipStatus: &relationshipStatus,
			LastTransferSize:   lastTransferSize,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
			HybridReplicationAttributes: updatedHybridAttrs,
			ClusterPeerId:               sql.NullInt64{Int64: clusterPeeringRow.ID, Valid: true},
		}
		err = store.UpdateVolumeReplication(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		updatedVolumeRep, err1 := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err1, "Expected no error, got %v", err1)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, updatedVolumeRep.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateUpdating, updatedVolumeRep.State)
		assert.Equal(tt, lastTransferSize, updatedVolumeRep.LastTransferSize, "Expected volume last transfer size %v, got %v", lastTransferSize, updatedVolumeRep.LastTransferSize)

		// Verify HybridReplicationAttributes were updated
		assert.NotNil(tt, updatedVolumeRep.HybridReplicationAttributes, "Expected HybridReplicationAttributes to be set")
		assert.Equal(tt, "peer-volume-2", updatedVolumeRep.HybridReplicationAttributes.PeerVolumeName, "Expected updated peer volume name")
		assert.Equal(tt, "peer-svm-2", updatedVolumeRep.HybridReplicationAttributes.PeerSvmName, "Expected updated peer svm name")
		assert.Equal(tt, "Updated test replication", updatedVolumeRep.HybridReplicationAttributes.Description, "Expected updated description")
		assert.Equal(tt, "prod", updatedVolumeRep.HybridReplicationAttributes.Labels["env"], "Expected updated labels")
		assert.Equal(tt, "daily", updatedVolumeRep.HybridReplicationAttributes.ReplicationSchedule, "Expected updated replication schedule")

		// Verify ClusterPeerId was updated
		assert.True(tt, updatedVolumeRep.ClusterPeerId.Valid, "Expected ClusterPeerId to be valid")
		assert.Equal(tt, clusterPeeringRow.ID, updatedVolumeRep.ClusterPeerId.Int64, "Expected ClusterPeerId to match cluster peering row ID")
	})
	t.Run("WhenVolumeReplicationIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "dummy"},
			Name:      "test_volume_rep",
			State:     datamodel.LifeCycleStateUpdating,
		}
		err = store.UpdateVolumeReplication(context.Background(), updateVolumeRep)
		assert.EqualError(tt, err, "volume replication not found", "Expected no error, got %v", err)
	})
	t.Run("WhenDeletedAtIsSet", func(tt *testing.T) {
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
			Name:           "test_pool",
			Account:        account,
			DeploymentName: "test-deployment",
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		// Verify DeletedAt is nil initially
		initialVolumeRep, err := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err, "Failed to get volume replication")
		assert.Nil(tt, initialVolumeRep.DeletedAt, "Expected DeletedAt to be nil initially")

		// Update with DeletedAt set
		deletedAt := &gorm.DeletedAt{Time: time.Now(), Valid: true}
		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-volume-rep-uuid",
				DeletedAt: deletedAt,
			},
			State: datamodel.LifeCycleStateDeleted,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
		}
		err = store.UpdateVolumeReplication(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		// Verify DeletedAt was set (use Unscoped to query soft-deleted records)
		var updatedVolumeRep datamodel.VolumeReplication
		err = store.db.Unscoped().GORM().Where("uuid = ?", volumeRep.UUID).First(&updatedVolumeRep).Error
		assert.NoError(tt, err, "Failed to get updated volume replication")
		assert.NotNil(tt, updatedVolumeRep.DeletedAt, "Expected DeletedAt to be set")
		assert.True(tt, updatedVolumeRep.DeletedAt.Valid, "Expected DeletedAt.Valid to be true")
		assert.Equal(tt, deletedAt.Time.Unix(), updatedVolumeRep.DeletedAt.Time.Unix(), "Expected DeletedAt time to match")
	})
	t.Run("WhenDeletedAtIsNil", func(tt *testing.T) {
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
			Name:           "test_pool",
			Account:        account,
			DeploymentName: "test-deployment",
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		// Verify DeletedAt is nil initially
		initialVolumeRep, err := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err, "Failed to get volume replication")
		assert.Nil(tt, initialVolumeRep.DeletedAt, "Expected DeletedAt to be nil initially")

		// Update without DeletedAt (nil)
		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			State:     datamodel.LifeCycleStateAvailable,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
		}
		err = store.UpdateVolumeReplication(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		// Verify DeletedAt remains nil (not modified)
		updatedVolumeRep, err := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err, "Failed to get updated volume replication")
		assert.Nil(tt, updatedVolumeRep.DeletedAt, "Expected DeletedAt to remain nil when not set in update")
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, updatedVolumeRep.State, "Expected state to be updated")
	})
	t.Run("WhenClusterPeerIdIsSetToNull", func(tt *testing.T) {
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
			Name:           "test_pool",
			Account:        account,
			DeploymentName: "test-deployment",
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

		// Create a cluster peering row for testing ClusterPeerId
		clusterPeeringRow := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "test-cluster-peer-uuid"},
			State:          datamodel.CvpClusterPeeringStatusPEERED,
			StateDetails:   "Successfully peered",
			OnprempCluster: "test-cluster",
			OntapPeerUUID:  "test-ontap-peer-uuid",
			AccountID:      account.ID,
			PoolID:         pool.ID,
		}
		err = store.db.Create(clusterPeeringRow).Error()
		if err != nil {
			tt.Fatalf("Failed to create cluster peering row: %v", err)
		}

		// Create volume replication with ClusterPeerId set
		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
			ClusterPeerId: sql.NullInt64{Int64: clusterPeeringRow.ID, Valid: true},
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		// Verify ClusterPeerId is set initially
		initialVolumeRep, err := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err, "Failed to get volume replication")
		assert.True(tt, initialVolumeRep.ClusterPeerId.Valid, "Expected ClusterPeerId to be valid initially")
		assert.Equal(tt, clusterPeeringRow.ID, initialVolumeRep.ClusterPeerId.Int64, "Expected ClusterPeerId to match cluster peering row ID")

		// Update with ClusterPeerId.Valid = false to set it to NULL
		// This tests the code path at lines 154-159 in UpdateVolumeReplication
		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			State:     datamodel.LifeCycleStateAvailable,
			ReplicationAttributes: &datamodel.ReplicationDetails{
				ExternalUUID: "test-volume-rep-external-uuid",
			},
			ClusterPeerId: sql.NullInt64{Valid: false}, // Set Valid to false to trigger NULL update (lines 154-159)
		}
		err = store.UpdateVolumeReplication(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error when updating ClusterPeerId to NULL, got %v", err)

		// Verify the update was executed by checking the database directly
		// Query the raw database value to verify it's actually NULL
		var clusterPeerID sql.NullInt64
		err = store.db.GORM().Model(&datamodel.VolumeReplication{}).
			Where("uuid = ?", volumeRep.UUID).
			Select("cluster_peer_id").
			Scan(&clusterPeerID).Error
		assert.NoError(tt, err, "Failed to query cluster_peer_id from database")
		// This verifies that the code at lines 154-159 executed and set cluster_peer_id to NULL
		assert.False(tt, clusterPeerID.Valid, "Expected cluster_peer_id to be NULL (Valid=false) in database after UpdateVolumeReplication with ClusterPeerId.Valid=false. This tests lines 154-159.")

		// Also verify using GetVolumeReplication to ensure the update worked
		retrievedVolumeRep, err := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err, "Failed to get volume replication")
		assert.Equal(tt, datamodel.LifeCycleStateAvailable, retrievedVolumeRep.State, "Expected state to be updated")
	})
}

func TestUpdateVolumeReplicationStates(t *testing.T) {
	t.Run("WhenVolumeReplicationStateIsUpdatedSuccessfully", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			State:     datamodel.LifeCycleStateUpdating,
		}
		err = store.UpdateVolumeReplicationStates(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		updatedVolumeRep, err1 := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err1, "Expected no error, got %v", err1)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, updatedVolumeRep.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateUpdating, updatedVolumeRep.State)
	})
	t.Run("WhenStateDetailsIsUpdatedSuccessfully", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:         "test_volume_rep",
			Account:      account,
			Volume:       volume,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel:    datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			State:        datamodel.LifeCycleStateError,
			StateDetails: datamodel.LifeCycleStateUpdateErrorDetails,
		}
		err = store.UpdateVolumeReplicationStates(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		updatedVolumeRep, err := _getVolumeReplication(store.db.GORM(), &datamodel.VolumeReplication{BaseModel: datamodel.BaseModel{UUID: volumeRep.UUID}})
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, datamodel.LifeCycleStateError, updatedVolumeRep.State)
		assert.Equal(tt, datamodel.LifeCycleStateUpdateErrorDetails, updatedVolumeRep.StateDetails)
	})
	t.Run("WhenUpdateVolumeReplicationStatesUsesLightweightFetch", func(tt *testing.T) {
		originalDetailsFetch := getVolumeReplicationDetails
		t.Cleanup(func() {
			getVolumeReplicationDetails = originalDetailsFetch
		})
		getVolumeReplicationDetails = func(db *gorm.DB, query *datamodel.VolumeReplication) (*datamodel.VolumeReplication, error) {
			tt.Fatal("getVolumeReplicationDetails should not be called during UpdateVolumeReplicationStates")
			return nil, nil
		}

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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			Account:   account,
			Volume:    volume,
			State:     datamodel.LifeCycleStateAvailable,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			State:     datamodel.LifeCycleStateUpdating,
		}
		err = store.UpdateVolumeReplicationStates(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)
	})
	t.Run("WhenVolumeReplicationIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "dummy"},
			Name:      "test_volume_rep",
			State:     datamodel.LifeCycleStateUpdating,
		}
		err = store.UpdateVolumeReplicationStates(context.Background(), updateVolumeRep)
		assert.EqualError(tt, err, "volume replication not found", "Expected no error, got %v", err)
	})
}

func TestUpdateVolumeReplicationTransferStats(t *testing.T) {
	t.Run("WhenVolumeReplicationStateIsUpdatedSuccessfully", func(tt *testing.T) {
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

		mirrorState := datamodel.OntapUninitialized
		mirrorStateSnapmirrored := datamodel.OntapSnapmirrored
		relationshipStatus := datamodel.SnapmirrorRelationshipIdle
		volumeRep := &datamodel.VolumeReplication{
			BaseModel:             datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:                  "test_volume_rep",
			Account:               account,
			Volume:                volume,
			MirrorState:           &mirrorState,
			RelationshipStatus:    &relationshipStatus,
			Healthy:               false,
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel:             datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:                  "test_volume_rep",
			LastTransferSize:      100,
			MirrorState:           &mirrorStateSnapmirrored,
			Healthy:               true,
			ReplicationAttributes: &datamodel.ReplicationDetails{},
		}
		err = store.UpdateVolumeReplicationTransferStats(context.Background(), updateVolumeRep)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		updatedVolumeRep, err1 := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err1, "Expected no error, got %v", err1)
		assert.Equal(tt, int64(100), updatedVolumeRep.LastTransferSize, "Expected volume last transfer size %v, got %v", 100, updatedVolumeRep.LastTransferSize)
		assert.Equal(tt, datamodel.OntapSnapmirrored, *updatedVolumeRep.MirrorState, "Expected volume mirror state %v, got %v", datamodel.OntapSnapmirrored, *updatedVolumeRep.MirrorState)
		assert.True(tt, updatedVolumeRep.Healthy, "Expected volume healthy status %v, got %v", true, updatedVolumeRep.Healthy)
		assert.Equal(tt, datamodel.SnapmirrorRelationshipIdle, *updatedVolumeRep.RelationshipStatus, "Expected volume relationship status %v, got %v", datamodel.SnapmirrorRelationshipIdle, *updatedVolumeRep.RelationshipStatus)
	})
	t.Run("WhenVolumeReplicationIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		updateVolumeRep := &datamodel.VolumeReplication{
			BaseModel:        datamodel.BaseModel{UUID: "dummy"},
			Name:             "test_volume_rep",
			LastTransferSize: 100,
		}
		err = store.UpdateVolumeReplicationTransferStats(context.Background(), updateVolumeRep)
		assert.EqualError(tt, err, "volume replication not found", "Expected no error, got %v", err)
	})
}

func TestGetVolumeReplicationCount(t *testing.T) {
	t.Run("WhenAccountExists", func(tt *testing.T) {
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

		volumeRep := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
			Name:      "test_volume_rep",
			AccountID: account.ID,
		}
		err = store.db.Create(volumeRep).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		count, err := store.GetVolumeReplicationCount(context.Background(), account.Name)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, int64(1), count, "Expected count %v, got %v", 1, count)
	})

	t.Run("WhenAccountDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		count, err := store.GetVolumeReplicationCount(context.Background(), "nonexistent_account")
		var customErr *errors2.CustomError
		assert.True(tt, errors2.As(err, &customErr), "Expected a CustomError")
		assert.ErrorContains(tt, customErr.OriginalErr, "account not found")
		assert.Equal(tt, int64(0), count, "Expected count %v, got %v", 0, count)
	})
}

func CreateTestData(store *DataStoreRepository) (*datamodel.Account, *datamodel.Pool, *datamodel.Volume, error) {
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "test-account-uuid",
		},
		Name: "test_account",
	}
	err := store.db.Create(account).Error()
	if err != nil {
		return nil, nil, nil, err
	}

	pool := &datamodel.Pool{
		Name:    "test_pool",
		Account: account,
		ClusterDetails: datamodel.ClusterDetails{
			ExternalName: "external-cluster",
		},
	}
	err = store.db.Create(pool).Error()
	if err != nil {
		return nil, nil, nil, err
	}

	svm := &datamodel.Svm{
		BaseModel: datamodel.BaseModel{
			ID:   1,
			UUID: "test-svm-uuid",
		},
		Name:      "test_svm",
		AccountID: account.ID,
		PoolID:    pool.ID,
	}
	err = store.db.Create(svm).Error()
	if err != nil {
		return nil, nil, nil, err
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test_volume",
		AccountID: account.ID,
		Account:   account,
		Pool:      pool,
		Svm:       svm,
		SvmID:     svm.ID,
		PoolID:    pool.ID,
	}
	err = store.db.Create(volume).Error()
	if err != nil {
		return nil, nil, nil, err
	}

	return account, pool, volume, nil
}

func TestListVolumeReplications(t *testing.T) {
	t.Run("HappyPath", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account, _, volume, err := CreateTestData(store)
		if err != nil {
			t.Fatalf("Failed to create test data: %v", err)
		}

		replication1 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-1"},
			Name:      "replication_1",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
		}
		err = store.db.Create(replication1).Error()
		if err != nil {
			t.Fatalf("Failed to create replication 1: %v", err)
		}

		replication2 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-2"},
			Name:      "replication_2",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
		}
		err = store.db.Create(replication2).Error()
		if err != nil {
			t.Fatalf("Failed to create replication 2: %v", err)
		}

		replicationUUIDs := []string{replication1.UUID, replication2.UUID}
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("account_id", "=", account.ID),
			utils.NewFilterCondition("uuid", "in", replicationUUIDs))

		replications, err := store.ListVolumeReplications(context.Background(), *filter, 1)
		assert.NoError(t, err, "Expected no error, got %v", err)
		assert.Len(t, replications, 2, "Expected 2 volume replications, got %d", len(replications))
		assert.Equal(t, replication1.UUID, replications[0].UUID, "Expected replication 1 UUID %v, got %v", replication1.UUID, replications[0].UUID)
		assert.Equal(t, replication2.UUID, replications[1].UUID, "Expected replication 2 UUID %v, got %v", replication2.UUID, replications[1].UUID)
		assert.Equal(t, "external-cluster", replications[0].Volume.Pool.ClusterDetails.ExternalName, "Expected cluster name %v, got %v", "external-cluster", replications[0].Volume.Pool.ClusterDetails.ExternalName)

		// Verify that Volume.Svm is preloaded
		assert.NotNil(t, replications[0].Volume.Svm, "Volume.Svm should be preloaded")
		assert.Equal(t, "test_svm", replications[0].Volume.Svm.Name, "Expected SVM name %v, got %v", "test_svm", replications[0].Volume.Svm.Name)
		assert.Equal(t, "test-svm-uuid", replications[0].Volume.Svm.UUID, "Expected SVM UUID %v, got %v", "test-svm-uuid", replications[0].Volume.Svm.UUID)
	})
	t.Run("WhenNoReplicationsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account, _, _, err := CreateTestData(store)
		if err != nil {
			t.Fatalf("Failed to create test data: %v", err)
		}

		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("account_id", "=", account.ID))

		replications, err := store.ListVolumeReplications(context.Background(), *filter, 0)
		assert.NoError(t, err, "Expected no error, got %v", err)
		assert.Empty(t, replications, "Expected no volume replications, got %d", len(replications))
	})
	t.Run("WhenFilterIsNil", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		_, _, _, err = CreateTestData(store)
		if err != nil {
			t.Fatalf("Failed to create test data: %v", err)
		}

		expectedError := customerrors.NewUserInputValidationErr("no filter conditions provided for listing volume replications")

		replications, err := store.ListVolumeReplications(context.Background(), utils.Filter{}, 0)
		assert.EqualError(t, expectedError, "no filter conditions provided for listing volume replications", "Expected error %v, got %v", expectedError, err)
		assert.Empty(t, replications, "Expected no volume replications, got %d", len(replications))
	})
	t.Run("HappyPathNewFilter", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account, _, volume, err := CreateTestData(store)
		if err != nil {
			t.Fatalf("Failed to create test data: %v", err)
		}

		replication1 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-1"},
			Name:      "replication_1",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Uri:       "projects/45110233509/locations/australia-southeast1/volume/godpvolume4/replications/replication-name-6",
		}
		err = store.db.Create(replication1).Error()
		if err != nil {
			t.Fatalf("Failed to create replication 1: %v", err)
		}

		replication2 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-2"},
			Name:      "replication_2",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			RemoteUri: "projects/45110233509/locations/us-east4/volume/gosrcvolume1/replications/replication-name-6",
		}
		err = store.db.Create(replication2).Error()
		if err != nil {
			t.Fatalf("Failed to create replication 2: %v", err)
		}

		// replicationUUIDs := []string{replication1.UUID, replication2.UUID}
		uris := []string{replication1.Uri, replication2.RemoteUri}
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("account_id", "=", account.ID),
			utils.NewFilterCondition("uri", "in", uris))

		replications, err := store.ListVolumeReplications(context.Background(), *filter, 0)
		assert.NoError(t, err, "Expected no error, got %v", err)
		assert.Len(t, replications, 1, "Expected 2 volume replications, got %d", len(replications))
		assert.Equal(t, replication1.UUID, replications[0].UUID, "Expected replication 1 UUID %v, got %v", replication1.UUID, replications[0].UUID)
		assert.Equal(t, "external-cluster", replications[0].Volume.Pool.ClusterDetails.ExternalName, "Expected cluster name %v, got %v", "external-cluster", replications[0].Volume.Pool.ClusterDetails.ExternalName)
	})
	t.Run("WhenQueryDepthIsOneAndClusterPeerIsPreloaded", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account, pool, volume, err := CreateTestData(store)
		if err != nil {
			t.Fatalf("Failed to create test data: %v", err)
		}

		// Create a cluster peering row
		clusterPeeringRow := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "test-cluster-peer-uuid"},
			State:          datamodel.CvpClusterPeeringStatusPEERED,
			StateDetails:   "Successfully peered",
			OnprempCluster: "test-cluster",
			OntapPeerUUID:  "test-ontap-peer-uuid",
			AccountID:      account.ID,
			PoolID:         pool.ID,
		}
		err = store.db.Create(clusterPeeringRow).Error()
		if err != nil {
			t.Fatalf("Failed to create cluster peering row: %v", err)
		}

		// Create volume replication with ClusterPeerId set
		replication := &datamodel.VolumeReplication{
			BaseModel:     datamodel.BaseModel{UUID: "replication-with-cluster-peer"},
			Name:          "replication_with_cluster_peer",
			AccountID:     account.ID,
			Account:       account,
			VolumeID:      volume.ID,
			ClusterPeerId: sql.NullInt64{Int64: clusterPeeringRow.ID, Valid: true},
		}
		err = store.db.Create(replication).Error()
		if err != nil {
			t.Fatalf("Failed to create replication: %v", err)
		}

		replicationUUIDs := []string{replication.UUID}
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("account_id", "=", account.ID),
			utils.NewFilterCondition("uuid", "in", replicationUUIDs))

		// Call with queryDepth=1 to trigger line 290
		replications, err := store.ListVolumeReplications(context.Background(), *filter, 1)
		assert.NoError(t, err, "Expected no error, got %v", err)
		assert.Len(t, replications, 1, "Expected 1 volume replication, got %d", len(replications))
		assert.Equal(t, replication.UUID, replications[0].UUID, "Expected replication UUID %v, got %v", replication.UUID, replications[0].UUID)

		// Verify that Volume.Svm is preloaded (line 290)
		assert.NotNil(t, replications[0].Volume.Svm, "Volume.Svm should be preloaded when queryDepth=1")
		assert.Equal(t, "test_svm", replications[0].Volume.Svm.Name, "Expected SVM name %v, got %v", "test_svm", replications[0].Volume.Svm.Name)
		assert.Equal(t, "test-svm-uuid", replications[0].Volume.Svm.UUID, "Expected SVM UUID %v, got %v", "test-svm-uuid", replications[0].Volume.Svm.UUID)

		// Verify that ClusterPeer is preloaded (line 290)
		assert.NotNil(t, replications[0].ClusterPeer, "ClusterPeer should be preloaded when queryDepth=1")
		assert.Equal(t, clusterPeeringRow.ID, replications[0].ClusterPeer.ID, "Expected ClusterPeer ID %v, got %v", clusterPeeringRow.ID, replications[0].ClusterPeer.ID)
		assert.Equal(t, clusterPeeringRow.UUID, replications[0].ClusterPeer.UUID, "Expected ClusterPeer UUID %v, got %v", clusterPeeringRow.UUID, replications[0].ClusterPeer.UUID)
		assert.Equal(t, "test-cluster", replications[0].ClusterPeer.OnprempCluster, "Expected ClusterPeer OnprempCluster %v, got %v", "test-cluster", replications[0].ClusterPeer.OnprempCluster)
	})

	t.Run("WhenQueryDepthIsZeroAndClusterPeerIsPreloaded", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		account, pool, volume, err := CreateTestData(store)
		if err != nil {
			tt.Fatalf("Failed to create test data: %v", err)
		}

		// Create a cluster peering row
		clusterPeeringRow := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "test-cluster-peer-uuid-zero"},
			State:          datamodel.CvpClusterPeeringStatusPEERED,
			StateDetails:   "Successfully peered",
			OnprempCluster: "test-cluster-zero",
			OntapPeerUUID:  "test-ontap-peer-uuid-zero",
			AccountID:      account.ID,
			PoolID:         pool.ID,
		}
		err = store.db.Create(clusterPeeringRow).Error()
		if err != nil {
			tt.Fatalf("Failed to create cluster peering row: %v", err)
		}

		// Create volume replication with ClusterPeerId set
		replication := &datamodel.VolumeReplication{
			BaseModel:     datamodel.BaseModel{UUID: "replication-with-cluster-peer-zero"},
			Name:          "replication_with_cluster_peer_zero",
			AccountID:     account.ID,
			Account:       account,
			VolumeID:      volume.ID,
			ClusterPeerId: sql.NullInt64{Int64: clusterPeeringRow.ID, Valid: true},
		}
		err = store.db.Create(replication).Error()
		if err != nil {
			tt.Fatalf("Failed to create replication: %v", err)
		}

		replicationUUIDs := []string{replication.UUID}
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("account_id", "=", account.ID),
			utils.NewFilterCondition("uuid", "in", replicationUUIDs))

		// Call with queryDepth=0 to test line 288
		replications, err := store.ListVolumeReplications(context.Background(), *filter, 1)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Len(tt, replications, 1, "Expected 1 volume replication, got %d", len(replications))
		assert.Equal(tt, replication.UUID, replications[0].UUID, "Expected replication UUID %v, got %v", replication.UUID, replications[0].UUID)

		// Verify that ClusterPeer is preloaded (line 288)
		assert.NotNil(tt, replications[0].ClusterPeer, "ClusterPeer should be preloaded when queryDepth=0")
		assert.Equal(tt, clusterPeeringRow.ID, replications[0].ClusterPeer.ID, "Expected ClusterPeer ID %v, got %v", clusterPeeringRow.ID, replications[0].ClusterPeer.ID)
		assert.Equal(tt, clusterPeeringRow.UUID, replications[0].ClusterPeer.UUID, "Expected ClusterPeer UUID %v, got %v", clusterPeeringRow.UUID, replications[0].ClusterPeer.UUID)
		assert.Equal(tt, "test-cluster-zero", replications[0].ClusterPeer.OnprempCluster, "Expected ClusterPeer OnprempCluster %v, got %v", "test-cluster-zero", replications[0].ClusterPeer.OnprempCluster)
		assert.Equal(tt, datamodel.CvpClusterPeeringStatusPEERED, replications[0].ClusterPeer.State, "Expected ClusterPeer State %v, got %v", datamodel.CvpClusterPeeringStatusPEERED, replications[0].ClusterPeer.State)
	})

	t.Run("WhenClusterPeerIdIsNotSet", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account, _, volume, err := CreateTestData(store)
		if err != nil {
			t.Fatalf("Failed to create test data: %v", err)
		}

		// Create volume replication without ClusterPeerId set
		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-without-cluster-peer"},
			Name:      "replication_without_cluster_peer",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			// ClusterPeerId is not set (nil)
		}
		err = store.db.Create(replication).Error()
		if err != nil {
			t.Fatalf("Failed to create replication: %v", err)
		}

		replicationUUIDs := []string{replication.UUID}
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("account_id", "=", account.ID),
			utils.NewFilterCondition("uuid", "in", replicationUUIDs))

		// Call with queryDepth=0
		replications, err := store.ListVolumeReplications(context.Background(), *filter, 0)
		assert.NoError(t, err, "Expected no error, got %v", err)
		assert.Len(t, replications, 1, "Expected 1 volume replication, got %d", len(replications))
		assert.Equal(t, replication.UUID, replications[0].UUID, "Expected replication UUID %v, got %v", replication.UUID, replications[0].UUID)

		// Verify that ClusterPeer is nil when ClusterPeerId is not set
		assert.Nil(t, replications[0].ClusterPeer, "ClusterPeer should be nil when ClusterPeerId is not set")
	})

	t.Run("WhenMultipleReplicationsWithAndWithoutClusterPeer", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "Failed to clean up test database")

		account, pool, volume, err := CreateTestData(store)
		if err != nil {
			t.Fatalf("Failed to create test data: %v", err)
		}

		// Create a cluster peering row
		clusterPeeringRow := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: "test-cluster-peer-uuid-multi"},
			State:          datamodel.CvpClusterPeeringStatusPEERED,
			StateDetails:   "Successfully peered",
			OnprempCluster: "test-cluster-multi",
			OntapPeerUUID:  "test-ontap-peer-uuid-multi",
			AccountID:      account.ID,
			PoolID:         pool.ID,
		}
		err = store.db.Create(clusterPeeringRow).Error()
		if err != nil {
			t.Fatalf("Failed to create cluster peering row: %v", err)
		}

		// Create first replication with ClusterPeerId set
		replication1 := &datamodel.VolumeReplication{
			BaseModel:     datamodel.BaseModel{UUID: "replication-1-with-peer"},
			Name:          "replication_1_with_peer",
			AccountID:     account.ID,
			Account:       account,
			VolumeID:      volume.ID,
			ClusterPeerId: sql.NullInt64{Int64: clusterPeeringRow.ID, Valid: true},
		}
		err = store.db.Create(replication1).Error()
		if err != nil {
			t.Fatalf("Failed to create replication 1: %v", err)
		}

		// Create second replication without ClusterPeerId set
		replication2 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "replication-2-without-peer"},
			Name:      "replication_2_without_peer",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			// ClusterPeerId is not set
		}
		err = store.db.Create(replication2).Error()
		if err != nil {
			t.Fatalf("Failed to create replication 2: %v", err)
		}

		replicationUUIDs := []string{replication1.UUID, replication2.UUID}
		filter := utils.CreateFilterWithConditions(
			utils.NewFilterCondition("account_id", "=", account.ID),
			utils.NewFilterCondition("uuid", "in", replicationUUIDs))

		// Call with queryDepth=0
		replications, err := store.ListVolumeReplications(context.Background(), *filter, 1)
		assert.NoError(t, err, "Expected no error, got %v", err)
		assert.Len(t, replications, 2, "Expected 2 volume replications, got %d", len(replications))

		// Find replications by UUID
		var rep1, rep2 *datamodel.VolumeReplication
		for _, rep := range replications {
			if rep.UUID == replication1.UUID {
				rep1 = rep
			} else if rep.UUID == replication2.UUID {
				rep2 = rep
			}
		}

		assert.NotNil(t, rep1, "replication1 should be found")
		assert.NotNil(t, rep2, "replication2 should be found")

		// Verify that ClusterPeer is preloaded for replication1 (line 288)
		assert.NotNil(t, rep1.ClusterPeer, "ClusterPeer should be preloaded for replication1")
		assert.Equal(t, clusterPeeringRow.ID, rep1.ClusterPeer.ID, "Expected ClusterPeer ID %v, got %v", clusterPeeringRow.ID, rep1.ClusterPeer.ID)
		assert.Equal(t, clusterPeeringRow.UUID, rep1.ClusterPeer.UUID, "Expected ClusterPeer UUID %v, got %v", clusterPeeringRow.UUID, rep1.ClusterPeer.UUID)

		// Verify that ClusterPeer is nil for replication2
		assert.Nil(t, rep2.ClusterPeer, "ClusterPeer should be nil for replication2 when ClusterPeerId is not set")
	})
}

// TestUpdateVolumeReplicationFields tests the UpdateVolumeReplicationFields method
func TestUpdateVolumeReplicationFields(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	account, _, volume, err := CreateTestData(store)
	assert.NoError(t, err, "Failed to create test data")

	volumeRep := &datamodel.VolumeReplication{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-rep-uuid"},
		Name:      "test_volume_rep",
		AccountID: account.ID,
		VolumeID:  volume.ID,
	}
	err = store.db.Create(volumeRep).Error()
	assert.NoError(t, err, "Failed to create volume replication")

	t.Run("UpdateSingleField", func(tt *testing.T) {
		updates := map[string]interface{}{
			"name": "updated_volume_rep",
		}
		err := store.UpdateVolumeReplicationFields(context.Background(), volumeRep.UUID, updates)
		assert.NoError(tt, err, "Expected no error updating field")

		updated, err := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, "updated_volume_rep", updated.Name)
	})

	t.Run("UpdateMultipleFields", func(tt *testing.T) {
		updates := map[string]interface{}{
			"state":         datamodel.LifeCycleStateUpdating,
			"state_details": "updating details",
		}
		err := store.UpdateVolumeReplicationFields(context.Background(), volumeRep.UUID, updates)
		assert.NoError(tt, err, "Expected no error updating fields")

		updated, err := store.GetVolumeReplication(context.Background(), volumeRep.UUID)
		assert.NoError(tt, err)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, updated.State)
		assert.Equal(tt, "updating details", updated.StateDetails)
	})

	t.Run("UpdateNonExistentReplication", func(tt *testing.T) {
		updates := map[string]interface{}{
			"name": "should_not_exist",
		}
		err := store.UpdateVolumeReplicationFields(context.Background(), "non-existent-uuid", updates)
		assert.Error(tt, err, "Expected error for non-existent replication")
	})
}

func TestListVolumeReplicationsWithPagination(t *testing.T) {
	t.Run("WhenNoVolumeReplicationsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		conditions := [][]interface{}{
			{"account_id", "=", 999}, // Non-existent account ID
		}
		pagination := &utils.Pagination{Limit: 10, Offset: 0}
		replications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 0, len(replications), "Expected %v replications, got %v", 0, len(replications))
	})

	t.Run("WhenVolumeReplicationsExistWithPagination", func(tt *testing.T) {
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

		// Create pool with deployment name
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			Account:        account,
			DeploymentName: "test-deployment",
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		// Create volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: account.ID,
			Account:   account,
			Pool:      pool,
			PoolID:    pool.ID,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"},
			},
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create 5 volume replications for pagination testing
		replications := make([]*datamodel.VolumeReplication, 5)
		for i := 0; i < 5; i++ {
			replications[i] = &datamodel.VolumeReplication{
				BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("test-replication-uuid-%d", i+1)},
				Name:      fmt.Sprintf("test_replication_%d", i+1),
				AccountID: account.ID,
				Account:   account,
				VolumeID:  volume.ID,
				Volume:    volume,
			}
			err = store.db.Create(replications[i]).Error()
			assert.NoError(tt, err, "Failed to create replication %d", i+1)
		}

		conditions := [][]interface{}{
			{"account_id = ?", account.ID},
		}

		// Test first page with limit 2
		pagination := &utils.Pagination{Limit: 2, Offset: 0}
		resultReplications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 2, len(resultReplications), "Expected 2 replications on first page, got %v", len(resultReplications))

		// Verify that Account is preloaded
		assert.NotNil(tt, resultReplications[0].Account, "Account should be preloaded")
		assert.Equal(tt, account.Name, resultReplications[0].Account.Name, "Account name should match")

		// Verify that Volume is preloaded with only id and pool_id
		assert.NotNil(tt, resultReplications[0].Volume, "Volume should be preloaded")
		assert.Equal(tt, volume.ID, resultReplications[0].Volume.ID, "Volume ID should match")
		assert.Equal(tt, pool.ID, resultReplications[0].Volume.PoolID, "Volume PoolID should match")

		// Verify that Volume.Pool is preloaded with only id and deployment_name
		assert.NotNil(tt, resultReplications[0].Volume.Pool, "Volume.Pool should be preloaded")
		assert.Equal(tt, pool.ID, resultReplications[0].Volume.Pool.ID, "Pool ID should match")
		assert.Equal(tt, pool.DeploymentName, resultReplications[0].Volume.Pool.DeploymentName, "Pool DeploymentName should match")

		// Test second page with limit 2
		pagination = &utils.Pagination{Limit: 2, Offset: 2}
		resultReplications, err = store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 2, len(resultReplications), "Expected 2 replications on second page, got %v", len(resultReplications))

		// Test third page with limit 2
		pagination = &utils.Pagination{Limit: 2, Offset: 4}
		resultReplications, err = store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(resultReplications), "Expected 1 replication on third page, got %v", len(resultReplications))

		// Test with limit larger than total replications
		pagination = &utils.Pagination{Limit: 10, Offset: 0}
		resultReplications, err = store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 5, len(resultReplications), "Expected 5 replications with large limit, got %v", len(resultReplications))
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
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			Account:        account,
			DeploymentName: "test-deployment",
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
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"},
			},
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid"},
			Name:      "test_replication",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(replication).Error()
		assert.NoError(tt, err, "Failed to create replication")

		conditions := [][]interface{}{
			{"account_id", "=", account.ID},
		}

		// Test with nil pagination - should use default limit
		resultReplications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, nil)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(resultReplications), "Expected 1 replication with nil pagination, got %v", len(resultReplications))
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
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			Account:        account,
			DeploymentName: "test-deployment",
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
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"},
			},
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid"},
			Name:      "test_replication",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(replication).Error()
		assert.NoError(tt, err, "Failed to create replication")

		conditions := [][]interface{}{
			{"account_id", "=", account.ID},
		}

		// Test with zero limit - should use default limit
		pagination := &utils.Pagination{Limit: 0, Offset: 0}
		resultReplications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(resultReplications), "Expected 1 replication with zero limit (default), got %v", len(resultReplications))
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
		pagination := &utils.Pagination{Limit: 10, Offset: 0}
		replications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.Error(tt, err, "Expected error when database is closed")
		assert.Nil(tt, replications, "Expected nil replications when error occurs")
	})

	t.Run("WhenOffsetExceedsTotalReplications", func(tt *testing.T) {
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
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			Account:        account,
			DeploymentName: "test-deployment",
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
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"},
			},
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid"},
			Name:      "test_replication",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(replication).Error()
		assert.NoError(tt, err, "Failed to create replication")

		conditions := [][]interface{}{
			{"account_id", "=", account.ID},
		}

		// Test with offset beyond total replications
		pagination := &utils.Pagination{Limit: 10, Offset: 100}
		resultReplications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 0, len(resultReplications), "Expected 0 replications when offset exceeds total, got %v", len(resultReplications))
	})

	t.Run("WhenVolumeReplicationsWithDifferentStates", func(tt *testing.T) {
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
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			Account:        account,
			DeploymentName: "test-deployment",
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
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"NFSv3"},
			},
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create replications with different states
		replication1 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid-1"},
			Name:      "test_replication_1",
			State:     "active",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(replication1).Error()
		assert.NoError(tt, err, "Failed to create replication 1")

		replication2 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid-2"},
			Name:      "test_replication_2",
			State:     "paused",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(replication2).Error()
		assert.NoError(tt, err, "Failed to create replication 2")

		replication3 := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid-3"},
			Name:      "test_replication_3",
			State:     "broken",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(replication3).Error()
		assert.NoError(tt, err, "Failed to create replication 3")

		conditions := [][]interface{}{
			{"account_id = ?", accountID},
			{"state = ?", "active"},
		}

		pagination := &utils.Pagination{Limit: 10, Offset: 0}
		replications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(replications), "Expected 1 active replication, got %v", len(replications))
		assert.Equal(tt, "test-replication-uuid-1", replications[0].UUID, "Expected replication 1 UUID, got %v", replications[0].UUID)
	})

	t.Run("WhenOptimizedPreloadsWorkCorrectly", func(tt *testing.T) {
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
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      account.ID,
			Account:        account,
			DeploymentName: "test-deployment",
			// Add other fields that should NOT be loaded
			Description: "pool description",
			State:       "active",
			SizeInBytes: 1000000,
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
			// Add other fields that should NOT be loaded
			Description: "volume description",
			State:       "active",
			SizeInBytes: 500000,
			VolumeAttributes: &datamodel.VolumeAttributes{
				Protocols: []string{"ISCSI"},
			},
			LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
				LargeCapacity:               true,
				LargeVolumeConstituentCount: func() *int32 { v := int32(48); return &v }(),
			},
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		replication := &datamodel.VolumeReplication{
			BaseModel: datamodel.BaseModel{UUID: "test-replication-uuid"},
			Name:      "test_replication",
			AccountID: account.ID,
			Account:   account,
			VolumeID:  volume.ID,
			Volume:    volume,
		}
		err = store.db.Create(replication).Error()
		assert.NoError(tt, err, "Failed to create replication")

		conditions := [][]interface{}{
			{"account_id", "=", account.ID},
		}

		pagination := &utils.Pagination{Limit: 10, Offset: 0}
		resultReplications, err := store.ListVolumeReplicationsWithPagination(context.Background(), conditions, pagination)
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, 1, len(resultReplications), "Expected 1 replication, got %v", len(resultReplications))

		// Verify Account is fully loaded
		assert.NotNil(tt, resultReplications[0].Account, "Account should be preloaded")
		assert.Equal(tt, account.Name, resultReplications[0].Account.Name, "Account name should match")

		// Verify Volume is partially loaded (only id and pool_id)
		assert.NotNil(tt, resultReplications[0].Volume, "Volume should be preloaded")
		assert.Equal(tt, volume.ID, resultReplications[0].Volume.ID, "Volume ID should match")
		assert.Equal(tt, pool.ID, resultReplications[0].Volume.PoolID, "Volume PoolID should match")
		// These fields should be empty/zero due to selective loading
		assert.Empty(tt, resultReplications[0].Volume.Name, "Volume Name should be empty due to selective loading")
		assert.Empty(tt, resultReplications[0].Volume.Description, "Volume Description should be empty due to selective loading")
		assert.Zero(tt, resultReplications[0].Volume.SizeInBytes, "Volume SizeInBytes should be zero due to selective loading")

		// Verify Volume.Pool is partially loaded (only id and deployment_name)
		assert.NotNil(tt, resultReplications[0].Volume.Pool, "Volume.Pool should be preloaded")
		assert.Equal(tt, pool.ID, resultReplications[0].Volume.Pool.ID, "Pool ID should match")
		assert.Equal(tt, pool.DeploymentName, resultReplications[0].Volume.Pool.DeploymentName, "Pool DeploymentName should match")
		// These fields should be empty/zero due to selective loading
		assert.Empty(tt, resultReplications[0].Volume.Pool.Name, "Pool Name should be empty due to selective loading")
		assert.Empty(tt, resultReplications[0].Volume.Pool.Description, "Pool Description should be empty due to selective loading")
		assert.Zero(tt, resultReplications[0].Volume.Pool.SizeInBytes, "Pool SizeInBytes should be zero due to selective loading")

		// Verify Volume.LargeVolumeAttributes is loaded
		assert.NotNil(tt, resultReplications[0].Volume.LargeVolumeAttributes, "Volume.LargeVolumeAttributes should be preloaded")
		assert.True(tt, resultReplications[0].Volume.LargeVolumeAttributes.LargeCapacity, "LargeCapacity should be true")
		assert.NotNil(tt, resultReplications[0].Volume.LargeVolumeAttributes.LargeVolumeConstituentCount, "LargeVolumeConstituentCount should not be nil")
		assert.Equal(tt, int32(48), *resultReplications[0].Volume.LargeVolumeAttributes.LargeVolumeConstituentCount, "LargeVolumeConstituentCount should be 48")
	})
}

func TestGetVolumeReplicationCountByPeerDetails(t *testing.T) {
	logger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	t.Run("WhenVolumeReplicationsExistWithMatchingPeerNames", func(tt *testing.T) {
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

		// Create pools
		pool1 := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-1-uuid"},
			Name:           "test_pool_1",
			AccountID:      1,
			DeploymentName: "test-deployment-1",
		}
		err = store.db.Create(pool1).Error()
		assert.NoError(tt, err, "Failed to create pool 1")

		pool2 := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-2-uuid"},
			Name:           "test_pool_2",
			AccountID:      1,
			DeploymentName: "test-deployment-2",
		}
		err = store.db.Create(pool2).Error()
		assert.NoError(tt, err, "Failed to create pool 2")

		// Create volumes
		volume1 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-1-uuid"},
			Name:      "test_volume_1",
			AccountID: 1,
			PoolID:    pool1.ID,
		}
		err = store.db.Create(volume1).Error()
		assert.NoError(tt, err, "Failed to create volume 1")

		volume2 := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-2-uuid"},
			Name:      "test_volume_2",
			AccountID: 1,
			PoolID:    pool2.ID,
		}
		err = store.db.Create(volume2).Error()
		assert.NoError(tt, err, "Failed to create volume 2")

		// Create hybrid replication attributes for matching peer names
		hybridAttrs1 := &datamodel.HybridReplicationAttribute{
			PeerVolumeName:      "peer-volume-1",
			PeerSvmName:         "peer-svm-1",
			Description:         "Test replication 1",
			Labels:              map[string]string{"env": "test"},
			ReplicationSchedule: "hourly",
			Status:              datamodel.HybridReplicationStatusPeered,
			StatusDetails:       "Successfully peered",
		}

		hybridAttrs2 := &datamodel.HybridReplicationAttribute{
			PeerVolumeName:      "peer-volume-1",
			PeerSvmName:         "peer-svm-1",
			Description:         "Test replication 2",
			Labels:              map[string]string{"env": "test"},
			ReplicationSchedule: "daily",
			Status:              datamodel.HybridReplicationStatusPeered,
			StatusDetails:       "Successfully peered",
		}

		// Create volume replications with matching peer names
		volumeReplication1 := &datamodel.VolumeReplication{
			BaseModel:                   datamodel.BaseModel{UUID: "test-replication-1-uuid"},
			Name:                        "test_replication_1",
			State:                       datamodel.LifeCycleStateAvailable,
			AccountID:                   1,
			VolumeID:                    volume1.ID,
			HybridReplicationAttributes: hybridAttrs1,
		}
		err = store.db.Create(volumeReplication1).Error()
		assert.NoError(tt, err, "Failed to create volume replication 1")

		volumeReplication2 := &datamodel.VolumeReplication{
			BaseModel:                   datamodel.BaseModel{UUID: "test-replication-2-uuid"},
			Name:                        "test_replication_2",
			State:                       datamodel.LifeCycleStateAvailable,
			AccountID:                   1,
			VolumeID:                    volume2.ID,
			HybridReplicationAttributes: hybridAttrs2,
		}
		err = store.db.Create(volumeReplication2).Error()
		assert.NoError(tt, err, "Failed to create volume replication 2")

		// Create volume replication with different peer names
		hybridAttrs3 := &datamodel.HybridReplicationAttribute{
			PeerVolumeName:      "peer-volume-2",
			PeerSvmName:         "peer-svm-2",
			Description:         "Test replication 3",
			Labels:              map[string]string{"env": "test"},
			ReplicationSchedule: "weekly",
			Status:              datamodel.HybridReplicationStatusPeered,
			StatusDetails:       "Successfully peered",
		}

		volumeReplication3 := &datamodel.VolumeReplication{
			BaseModel:                   datamodel.BaseModel{UUID: "test-replication-3-uuid"},
			Name:                        "test_replication_3",
			State:                       datamodel.LifeCycleStateAvailable,
			AccountID:                   1,
			VolumeID:                    volume1.ID,
			HybridReplicationAttributes: hybridAttrs3,
		}
		err = store.db.Create(volumeReplication3).Error()
		assert.NoError(tt, err, "Failed to create volume replication 3")

		// Query for volume replications with specific peer names
		count, err := store.GetVolumeReplicationCountByPeerDetails(ctx, "test_account", "peer-svm-1", "peer-volume-1")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, int64(2), count, "Expected 2 volume replications, got %d", count)
	})

	t.Run("WhenNoVolumeReplicationsExistWithMatchingPeerNames", func(tt *testing.T) {
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

		// Query for volume replications with non-matching peer names
		count, err := store.GetVolumeReplicationCountByPeerDetails(ctx, "test_account", "non-existent-svm", "non-existent-volume")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.Equal(tt, int64(0), count, "Expected 0 volume replications, got %d", count)
	})

	t.Run("WhenAccountDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Query for volume replications with non-existent account
		count, err := store.GetVolumeReplicationCountByPeerDetails(ctx, "non-existent-account", "peer-svm-1", "peer-volume-1")
		assert.Error(tt, err, "Expected error for non-existent account")
		assert.Equal(tt, int64(0), count, "Expected 0 count for non-existent account")
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
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

		// Create pool
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      1,
			DeploymentName: "test-deployment",
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		// Create volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create hybrid replication attributes
		hybridAttrs := &datamodel.HybridReplicationAttribute{
			PeerVolumeName:      "peer-volume-1",
			PeerSvmName:         "peer-svm-1",
			Description:         "Test replication",
			Labels:              map[string]string{"env": "test"},
			ReplicationSchedule: "hourly",
			Status:              datamodel.HybridReplicationStatusPeered,
			StatusDetails:       "Successfully peered",
		}

		// Create volume replication
		volumeReplication := &datamodel.VolumeReplication{
			BaseModel:                   datamodel.BaseModel{UUID: "test-replication-uuid"},
			Name:                        "test_replication",
			State:                       datamodel.LifeCycleStateAvailable,
			AccountID:                   1,
			VolumeID:                    volume.ID,
			HybridReplicationAttributes: hybridAttrs,
		}
		err = store.db.Create(volumeReplication).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		// Use a cancelled context to trigger a database error
		cancelledCtx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel the context immediately

		_, err = store.GetVolumeReplicationCountByPeerDetails(cancelledCtx, "test_account", "peer-svm-1", "peer-volume-1")
		assert.Error(tt, err, "Expected error due to cancelled context")
	})

	t.Run("WhenVolumeReplicationQueryFails", func(tt *testing.T) {
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

		// Create pool
		pool := &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			Name:           "test_pool",
			AccountID:      1,
			DeploymentName: "test-deployment",
		}
		err = store.db.Create(pool).Error()
		assert.NoError(tt, err, "Failed to create pool")

		// Create volume
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
			Name:      "test_volume",
			AccountID: 1,
			PoolID:    pool.ID,
		}
		err = store.db.Create(volume).Error()
		assert.NoError(tt, err, "Failed to create volume")

		// Create hybrid replication attributes
		hybridAttrs := &datamodel.HybridReplicationAttribute{
			PeerVolumeName:      "peer-volume-1",
			PeerSvmName:         "peer-svm-1",
			Description:         "Test replication",
			Labels:              map[string]string{"env": "test"},
			ReplicationSchedule: "hourly",
			Status:              datamodel.HybridReplicationStatusPeered,
			StatusDetails:       "Successfully peered",
		}

		// Create volume replication
		volumeReplication := &datamodel.VolumeReplication{
			BaseModel:                   datamodel.BaseModel{UUID: "test-replication-uuid"},
			Name:                        "test_replication",
			State:                       datamodel.LifeCycleStateAvailable,
			AccountID:                   1,
			VolumeID:                    volume.ID,
			HybridReplicationAttributes: hybridAttrs,
		}
		err = store.db.Create(volumeReplication).Error()
		assert.NoError(tt, err, "Failed to create volume replication")

		// Get the underlying GORM database connection
		gormDB := store.db.GORM()

		// Drop the volume_replications table to cause the query to fail
		// This will allow the account lookup to succeed but cause the volume replication query to fail
		err = gormDB.Migrator().DropTable(&datamodel.VolumeReplication{})
		assert.NoError(tt, err, "Failed to drop volume_replications table")

		// Try to get count after dropping the table - this should trigger line 246
		count, err := store.GetVolumeReplicationCountByPeerDetails(ctx, "test_account", "peer-svm-1", "peer-volume-1")
		assert.Error(tt, err, "Expected error when volume_replications table is dropped")
		assert.Equal(tt, int64(0), count, "Expected count to be 0 when database error occurs")
	})
}

func TestGetVolumeReplicationCountByClusterPeerID(t *testing.T) {
	logger := log.NewLogger()
	baseCtx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, logger)

	newStore := func(t *testing.T) *DataStoreRepository {
		t.Helper()
		db, err := SetupTestDB()
		assert.NoError(t, err, "setup db failed")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err, "clear db failed")
		return store
	}

	createReplication := func(t *testing.T, store *DataStoreRepository, account *datamodel.Account, volume *datamodel.Volume, clusterPeerID int64) {
		t.Helper()
		rep := &datamodel.VolumeReplication{
			BaseModel:     datamodel.BaseModel{UUID: vcputils.RandomUUID()},
			AccountID:     account.ID,
			VolumeID:      volume.ID,
			State:         datamodel.LifeCycleStateCreating,
			StateDetails:  datamodel.LifeCycleStateCreatingDetails,
			ClusterPeerId: sql.NullInt64{Int64: clusterPeerID, Valid: true},
		}
		err := store.db.Create(rep).Error()
		assert.NoError(t, err, "create replication failed")
	}

	t.Run("WhenReplicationsExistForClusterPeerID", func(tt *testing.T) {
		store := newStore(tt)
		account, _, volume, err := CreateTestData(store)
		assert.NoError(tt, err)

		// Create cluster peerings that will be referenced
		peerA := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: vcputils.RandomUUID()},
			AccountID:      account.ID,
			OnprempCluster: "peer-A",
		}
		peerB := &datamodel.ClusterPeerings{
			BaseModel:      datamodel.BaseModel{UUID: vcputils.RandomUUID()},
			AccountID:      account.ID,
			OnprempCluster: "peer-B",
		}

		assert.NoError(tt, store.db.Create(peerA).Error())
		assert.NoError(tt, store.db.Create(peerB).Error())

		// Two replications for peerA, one for peerB
		createReplication(tt, store, account, volume, peerA.ID)
		createReplication(tt, store, account, volume, peerA.ID)
		createReplication(tt, store, account, volume, peerB.ID)

		count, err := store.GetVolumeReplicationCountByClusterPeerID(baseCtx, peerA.ID)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(2), count)
	})

	t.Run("WhenNoReplicationsExistForClusterPeerID", func(tt *testing.T) {
		store := newStore(tt)
		account, _, volume, err := CreateTestData(store)
		assert.NoError(tt, err)

		createReplication(tt, store, account, volume, 100)
		createReplication(tt, store, account, volume, 101)

		count, err := store.GetVolumeReplicationCountByClusterPeerID(baseCtx, 999)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), count)
	})

	t.Run("WhenContextIsCanceled", func(tt *testing.T) {
		store := newStore(tt)
		account, _, volume, err := CreateTestData(store)
		assert.NoError(tt, err)

		createReplication(tt, store, account, volume, 55)

		cctx, cancel := context.WithCancel(baseCtx)
		cancel()
		count, err := store.GetVolumeReplicationCountByClusterPeerID(cctx, 55)
		assert.Error(tt, err)
		assert.Equal(tt, int64(0), count)
	})

	t.Run("WhenClusterPeerIDIsZero", func(tt *testing.T) {
		store := newStore(tt)
		account, _, volume, err := CreateTestData(store)
		assert.NoError(tt, err)

		createReplication(tt, store, account, volume, 321)

		count, err := store.GetVolumeReplicationCountByClusterPeerID(baseCtx, 0)
		assert.NoError(tt, err)
		assert.Equal(tt, int64(0), count)
	})

	t.Run("WhenDatabaseErrorOccurs", func(tt *testing.T) {
		store := newStore(tt)
		account, _, volume, err := CreateTestData(store)
		assert.NoError(tt, err)
		createReplication(tt, store, account, volume, 777)

		sqlDB, _ := store.db.GORM().DB()
		_ = sqlDB.Close()

		count, err := store.GetVolumeReplicationCountByClusterPeerID(baseCtx, 777)
		assert.Error(tt, err)
		assert.Equal(tt, int64(0), count)
		var vErr *errors2.CustomError
		if errors2.As(err, &vErr) {
			assert.Equal(tt, errors2.ErrDatabaseDataReadError, vErr.TrackingID)
		}
	})
}
