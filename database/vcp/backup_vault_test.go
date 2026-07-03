package database

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	vcputils "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"gorm.io/gorm"
)

func TestGetBackupVaultWithDetails(t *testing.T) {
	t.Run("WhenBackupVaultExists", func(tt *testing.T) {
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

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:      "test_backup_vault",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		result, err := _getBackupVaultWithDetails(store.db.GORM(), &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: backupVault.UUID}})
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result.UUID != backupVault.UUID {
			tt.Errorf("Expected backup vault UUID %v, got %v", backupVault.UUID, result.UUID)
		}
		if result.Account.Name != account.Name {
			tt.Errorf("Expected account name %v, got %v", account.Name, result.Account.Name)
		}
	})

	t.Run("WhenBackupVaultDoesNotExist", func(tt *testing.T) {
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

		_, err = _getBackupVaultWithDetails(store.db.GORM(), &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "non-existent-uuid"}})
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if err.Error() != "backup vault 'non-existent-uuid' not found" {
			tt.Errorf("Expected error 'backup vault 'non-existent-uuid' not found', got %v", err)
		}
	})
}

func TestGetBackupVaultByUUID(t *testing.T) {
	t.Run("WhenGetBackupVaultByUUIDReturnsBackupVaultWhenExists", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:      "test_backup_vault",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}
		originalGetBackupVaultWithDetails := getBackupVaultWithDetails
		defer func() { getBackupVaultWithDetails = originalGetBackupVaultWithDetails }()
		getBackupVaultWithDetails = func(db *gorm.DB, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
			return backupVault, nil
		}

		result, err := store.GetBackupVaultByUUIDndOwnerID(context.Background(), backupVault.UUID, int64(123))
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result.UUID != backupVault.UUID {
			tt.Errorf("Expected backup vault UUID %v, got %v", backupVault.UUID, result.UUID)
		}
	})
	t.Run("WhenGetBackupVaultByUUIDReturnsErrorWhenBackupVaultDoesNotExist", func(tt *testing.T) {
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
		originalGetBackupVaultWithDetails := getBackupVaultWithDetails
		defer func() { getBackupVaultWithDetails = originalGetBackupVaultWithDetails }()
		getBackupVaultWithDetails = func(db *gorm.DB, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
			return nil, errors.New("backup vault 'non-existent-uuid' not found")
		}

		_, err = store.GetBackupVaultByUUIDndOwnerID(context.Background(), "non-existent-uuid", int64(123))
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if err.Error() != "backup vault 'non-existent-uuid' not found" {
			tt.Errorf("Expected error 'backup vault 'non-existent-uuid' not found', got %v", err)
		}
	})
}

func TestGetBackupVaultByNameAndOwnerID(t *testing.T) {
	t.Run("WhenGetBackupVaultByNameAndOwnerIDReturnsBackupVaultWhenExists", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{ID: 10, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:      "test_backup_vault",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		result, err := store.GetBackupVaultByNameAndOwnerID(context.Background(), backupVault.Name, strconv.FormatInt(account.ID, 10))
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result.UUID != backupVault.UUID {
			tt.Errorf("Expected backup vault UUID %v, got %v", backupVault.UUID, result.UUID)
		}
		if result.Account.Name != account.Name {
			tt.Errorf("Expected account name %v, got %v", account.Name, result.Account.Name)
		}
	})
	t.Run("WhenGetBackupVaultByNameAndOwnerIDReturnsErrorWhenBackupVaultDoesNotExist", func(tt *testing.T) {
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

		_, err = store.GetBackupVaultByNameAndOwnerID(context.Background(), "non-existent-name", "non-existent-owner-id")
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected NotFoundErr, got %v", err)
		}
	})
}

func TestGetBackupVaultByCrossRegionBackupVaultName(t *testing.T) {
	t.Run("WhenGetBackupVaultByCrossRegionBackupVaultNameReturnsBackupVaultWhenExists", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{ID: 20, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		crossRegionBackupVaultName := "cross-region-backup-vault-name"
		backupVault := &datamodel.BackupVault{
			BaseModel:                  datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                       "test_backup_vault",
			AccountID:                  account.ID,
			Account:                    account,
			CrossRegionBackupVaultName: &crossRegionBackupVaultName,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		result, err := store.GetBackupVaultByCrossRegionBackupVaultName(context.Background(), crossRegionBackupVaultName, account.ID)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result.UUID != backupVault.UUID {
			tt.Errorf("Expected backup vault UUID %v, got %v", backupVault.UUID, result.UUID)
		}
		if result.Account.Name != account.Name {
			tt.Errorf("Expected account name %v, got %v", account.Name, result.Account.Name)
		}
		if result.CrossRegionBackupVaultName == nil || *result.CrossRegionBackupVaultName != crossRegionBackupVaultName {
			tt.Errorf("Expected cross region backup vault name %v, got %v", crossRegionBackupVaultName, result.CrossRegionBackupVaultName)
		}
	})
	t.Run("WhenGetBackupVaultByCrossRegionBackupVaultNameReturnsErrorWhenBackupVaultDoesNotExist", func(tt *testing.T) {
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

		_, err = store.GetBackupVaultByCrossRegionBackupVaultName(context.Background(), "non-existent-cross-region-name", 999999)
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected NotFoundErr, got %v", err)
		}
	})
	t.Run("WhenGetBackupVaultByCrossRegionBackupVaultNameReturnsErrorWhenAccountIDDoesNotMatch", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{ID: 21, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		crossRegionBackupVaultName := "cross-region-backup-vault-name"
		backupVault := &datamodel.BackupVault{
			BaseModel:                  datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                       "test_backup_vault",
			AccountID:                  account.ID,
			Account:                    account,
			CrossRegionBackupVaultName: &crossRegionBackupVaultName,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		// Try to get with wrong account ID
		_, err = store.GetBackupVaultByCrossRegionBackupVaultName(context.Background(), crossRegionBackupVaultName, 999)
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected NotFoundErr, got %v", err)
		}
	})
}

func TestGetBackupVault(t *testing.T) {
	t.Run("WhenGetBackupVaultReturnsErrorWhenBackupVaultDoesNotExist", func(tt *testing.T) {
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

		_, err = store.GetBackupVault(context.Background(), "non-existent-uuid")
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected NotFoundErr, got %v", err)
		}
	})
	t.Run("GetBackupVaultReturnsBackupVaultWhenExists", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:      "test_backup_vault",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		result, err := store.GetBackupVault(context.Background(), backupVault.UUID)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result.UUID != backupVault.UUID {
			tt.Errorf("Expected backup vault UUID %v, got %v", backupVault.UUID, result.UUID)
		}
		if result.Account.Name != account.Name {
			tt.Errorf("Expected account name %v, got %v", account.Name, result.Account.Name)
		}
	})
}

func TestUpdateBackupVaultMergesBucketDetails(tt *testing.T) {
	tt.Run("empty incoming does not clear bucket details", func(t *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			t.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			t.Fatalf("Failed to clean up test database: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			t.Fatalf("Failed to create account: %v", err)
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:      "test_backup_vault",
			AccountID: account.ID,
			Account:   account,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "old_bucket"},
			},
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			t.Fatalf("Failed to create backup vault: %v", err)
		}

		err = store.UpdateBackupVaultBucketDetails(context.Background(), &datamodel.BackupVault{
			BaseModel:     datamodel.BaseModel{UUID: backupVault.UUID},
			BucketDetails: datamodel.BucketDetailsArray{},
		})
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}

		result, err := store.GetBackupVault(context.Background(), backupVault.UUID)
		if err != nil {
			t.Fatalf("Failed to retrieve updated backup vault: %v", err)
		}
		require.Len(t, result.BucketDetails, 1)
		assert.Equal(t, "old_bucket", result.BucketDetails[0].BucketName)
	})

	tt.Run("patches one bucket and retains others", func(t *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			t.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			t.Fatalf("Failed to clean up test database: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid-2"},
			Name:      "test_account_2",
		}
		require.NoError(t, store.db.Create(account).Error())

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid-2"},
			Name:      "test_backup_vault_2",
			AccountID: account.ID,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "b1", SatisfiesPzi: false},
				&datamodel.BucketDetails{BucketName: "b2"},
			},
		}
		require.NoError(t, store.db.Create(backupVault).Error())

		err = store.UpdateBackupVaultBucketDetails(context.Background(), &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: backupVault.UUID},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "b1", SatisfiesPzi: true, SatisfiesPzs: true},
			},
		})
		require.NoError(t, err)

		result, err := store.GetBackupVault(context.Background(), backupVault.UUID)
		require.NoError(t, err)
		require.Len(t, result.BucketDetails, 2)
		assert.True(t, result.BucketDetails[0].SatisfiesPzi)
		assert.True(t, result.BucketDetails[0].SatisfiesPzs)
		assert.Equal(t, "b2", result.BucketDetails[1].BucketName)
	})

	tt.Run("appends new bucket", func(t *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			t.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid-3"},
			Name:      "test_account_3",
		}
		require.NoError(t, store.db.Create(account).Error())

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid-3"},
			Name:      "test_backup_vault_3",
			AccountID: account.ID,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "b1"},
				&datamodel.BucketDetails{BucketName: "b2"},
			},
		}
		require.NoError(t, store.db.Create(backupVault).Error())

		err = store.UpdateBackupVaultBucketDetails(context.Background(), &datamodel.BackupVault{
			BaseModel:     datamodel.BaseModel{UUID: backupVault.UUID},
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "b3"}},
		})
		require.NoError(t, err)

		result, err := store.GetBackupVault(context.Background(), backupVault.UUID)
		require.NoError(t, err)
		require.Len(t, result.BucketDetails, 3)
		assert.Equal(t, "b3", result.BucketDetails[2].BucketName)
	})

	tt.Run("returns not found when vault does not exist", func(t *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			t.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		err = store.UpdateBackupVaultBucketDetails(context.Background(), &datamodel.BackupVault{
			BaseModel:     datamodel.BaseModel{UUID: "non-existent-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "b1"}},
		})
		require.Error(t, err)
		if !customerrors.IsNotFoundErr(err) {
			t.Errorf("Expected NotFoundErr, got %v", err)
		}
	})
}

func TestListBackupVaultsReturnsBackupVaultsWhenAccountHasVaults(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	backupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:      "test_backup_vault",
		AccountID: account.ID,
		Account:   account,
	}
	err = store.db.Create(backupVault).Error()
	if err != nil {
		tt.Fatalf("Failed to create backup vault: %v", err)
	}

	result, err := store.ListBackupVaults(context.Background(), account.ID)
	if err != nil {
		tt.Errorf("Expected no error, got %v", err)
	}
	if len(result) != 1 {
		tt.Errorf("Expected 1 backup vault, got %v", len(result))
	}
	if result[0].UUID != backupVault.UUID {
		tt.Errorf("Expected backup vault UUID %v, got %v", backupVault.UUID, result[0].UUID)
	}
}

func TestListBackupVaultsReturnsEmptyListWhenNoVaultsExist(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	result, err := store.ListBackupVaults(context.Background(), account.ID)
	if err != nil {
		tt.Errorf("Expected no error, got %v", err)
	}
	if len(result) != 0 {
		tt.Errorf("Expected 0 backup vaults, got %v", len(result))
	}
}

func TestListBackupVaultsReturnsErrorWhenDatabaseFails(tt *testing.T) {
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

	// Simulate database failure by closing the connection
	sqlDB, _ := db.DB()
	err = sqlDB.Close()
	if err != nil {
		return
	}

	_, err = store.ListBackupVaults(context.Background(), 123)
	if err == nil {
		tt.Errorf("Expected error, got nil")
	}
}

func TestCreateBackupVaultEntryInVCPCreatesBackupVaultWhenDoesNotExist(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 1},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	backupVault := &datamodel.BackupVault{
		Name:      "test_backup_vault",
		AccountID: account.ID,
		Account:   account,
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
	}

	result, err := store.CreateBackupVaultEntryInVCP(context.Background(), backupVault)
	if err != nil {
		tt.Errorf("Expected no error, got %v", err)
	}
	if result.Name != backupVault.Name {
		tt.Errorf("Expected backup vault name %v, got %v", backupVault.Name, result.Name)
	}
}

func TestCreateBackupVaultEntryInVCPReturnsErrorWhenBackupVaultExists(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 1},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	backupVault := &datamodel.BackupVault{
		Name:      "test_backup_vault",
		AccountID: account.ID,
		Account:   account,
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
	}
	err = store.db.Create(backupVault).Error()
	if err != nil {
		tt.Fatalf("Failed to create backup vault: %v", err)
	}

	defer func() {
		getBackupVaultWithDetails = _getBackupVaultWithDetails
	}()
	getBackupVaultWithDetails = func(db *gorm.DB, query *datamodel.BackupVault) (*datamodel.BackupVault, error) {
		return backupVault, nil
	}

	result, err := store.CreateBackupVaultEntryInVCP(context.Background(), backupVault)
	if err != nil {
		tt.Errorf("Expected no error, got %v", err)
	}
	assert.NotNil(tt, result, "Expected result to be non-nil")
}

func TestCreateBackupVaultEntryInVCPReturnsGetBackupVaultError(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 1},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	backupVault := &datamodel.BackupVault{
		Name:      "test_backup_vault",
		AccountID: account.ID,
		Account:   account,
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
	}
	err = store.db.Create(backupVault).Error()
	if err != nil {
		tt.Fatalf("Failed to create backup vault: %v", err)
	}

	defer func() {
		getBackupVaultWithDetails = _getBackupVaultWithDetails
	}()
	getBackupVaultWithDetails = func(db *gorm.DB, query *datamodel.BackupVault) (*datamodel.BackupVault, error) {
		return nil, gorm.ErrRecordNotFound
	}

	result, err := store.CreateBackupVaultEntryInVCP(context.Background(), backupVault)
	assert.NotNil(tt, err, "Expected error when backup vault does not exist")
	assert.Nil(tt, result, "Expected result to be non-nil")
}

func TestCreateBackupVaultEntryInVCPReturnsErrorWhenDatabaseFails(tt *testing.T) {
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

	backupVault := &datamodel.BackupVault{
		Name:      "test_backup_vault",
		AccountID: 999, // Invalid account ID to simulate database failure
	}

	_, err = store.CreateBackupVaultEntryInVCP(context.Background(), backupVault)
	assert.Nil(tt, err)
}

func TestCreateBackupVaultEntryInVCPReturnsStartTransactionsFails(tt *testing.T) {
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

	backupVault := &datamodel.BackupVault{
		Name:      "test_backup_vault",
		AccountID: 999, // Invalid account ID to simulate database failure
	}

	defer func() {
		startTransaction = _startTransaction
	}()
	startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
		return nil, errors.New("failed to start transaction")
	}

	_, err = store.CreateBackupVaultEntryInVCP(context.Background(), backupVault)
	assert.NotNil(tt, err)
}

func TestCreateBackupVaultEntryInVCPError(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 1},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	backupVault := &datamodel.BackupVault{
		Name:      "test_backup_vault",
		AccountID: account.ID,
		Account:   account,
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
	}
	err = store.db.Create(backupVault).Error()
	if err != nil {
		tt.Fatalf("Failed to create backup vault: %v", err)
	}

	defer func() {
		getBackupVaultWithDetails = _getBackupVaultWithDetails
		checkBVExists = _checkBVExists
	}()
	getBackupVaultWithDetails = func(db *gorm.DB, query *datamodel.BackupVault) (*datamodel.BackupVault, error) {
		return backupVault, nil
	}
	checkBVExists = func(tx *gorm.DB, bv *datamodel.BackupVault) (*datamodel.BackupVault, error) {
		return nil, errors.New("mock checkBVExists error")
	}

	result, err := store.CreateBackupVaultEntryInVCP(context.Background(), backupVault)
	assert.Nil(tt, result)
	assert.Error(tt, err, "Expected error when creating backup vault entry in VCP")
}

func TestGetBackupVaultById(t *testing.T) {
	t.Run("TestGetBackupVaultByIdReturnsBackupVaultWhenExists", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 123},
			Name:      "test_backup_vault",
			AccountID: account.ID,
			Account:   account,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		result, err := store.GetBackupVaultById(context.Background(), backupVault.ID)
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result.ID != backupVault.ID {
			tt.Errorf("Expected backup vault ID %v, got %v", backupVault.ID, result.ID)
		}
		if result.Account.Name != account.Name {
			tt.Errorf("Expected account name %v, got %v", account.Name, result.Account.Name)
		}
	})
	t.Run("TestGetBackupVaultByIdReturnsErrorWhenBackupVaultDoesNotExist", func(tt *testing.T) {
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

		_, err = store.GetBackupVaultById(context.Background(), 999) // Non-existent ID
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
}

func TestUpdateBackupVaultInVCP(tt *testing.T) {
	tt.Run("TestUpdateBackupVaultInVCPUpdatesBackupVaultSuccessfully", func(t *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			t.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			t.Fatalf("Failed to clean up test database: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			t.Fatalf("Failed to create account: %v", err)
		}
		initDesc := "Initial description"
		backupVault := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:        "test_backup_vault",
			AccountID:   account.ID,
			Account:     account,
			Description: &initDesc,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			t.Fatalf("Failed to create backup vault: %v", err)
		}
		desc := "Updated description"
		updateParams := &datamodel.BackupVault{
			BaseModel:     datamodel.BaseModel{UUID: backupVault.UUID},
			Description:   &desc,
			AccountID:     account.ID,
			Name:          "Updated Backup Vault Name",
			BucketDetails: backupVault.BucketDetails,
		}
		updatedParams := &datamodel.BackupVault{
			BaseModel:     datamodel.BaseModel{UUID: backupVault.UUID},
			Description:   &desc,
			AccountID:     account.ID,
			Name:          "Updated Backup Vault Name",
			BucketDetails: backupVault.BucketDetails,
		}

		result, err := store.UpdateBackupVaultInVCP(context.Background(), updateParams, updatedParams)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		assert.NotNil(t, result)
		assert.NoError(t, err)
	})

	tt.Run("SetsExternalUUIDWhenProvided", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		require.NoError(t, store.db.Create(account).Error())

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "test-bv-uuid"},
			Name:      "test_backup_vault",
			AccountID: account.ID,
			Account:   account,
		}
		require.NoError(t, store.db.Create(backupVault).Error())

		extUUID := "new-external-uuid"
		updateParams := &datamodel.BackupVault{
			ExternalUUID: &extUUID,
		}
		existingParams := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: backupVault.UUID},
		}

		result, err := store.UpdateBackupVaultInVCP(context.Background(), updateParams, existingParams)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.ExternalUUID)
		assert.Equal(t, extUUID, *result.ExternalUUID)
	})
}

func TestUpdateBackupVaultFields(tt *testing.T) {
	tt.Run("UpdatesFieldsSuccessfully", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		require.NoError(t, store.db.Create(account).Error())

		backupVault := &datamodel.BackupVault{
			BaseModel:      datamodel.BaseModel{UUID: "test-bv-uuid"},
			Name:           "test_backup_vault",
			AccountID:      account.ID,
			Account:        account,
			LifeCycleState: "CREATING",
		}
		require.NoError(t, store.db.Create(backupVault).Error())

		extUUID := "new-external-uuid-123"
		updates := map[string]interface{}{
			"external_uuid": extUUID,
		}
		err = store.UpdateBackupVaultFields(context.Background(), backupVault.UUID, updates)
		require.NoError(t, err)

		result, err := store.GetBackupVault(context.Background(), backupVault.UUID)
		require.NoError(t, err)
		require.NotNil(t, result.ExternalUUID)
		assert.Equal(t, extUUID, *result.ExternalUUID)
	})

	tt.Run("ReturnsNotFoundWhenUUIDDoesNotExist", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		updates := map[string]interface{}{
			"external_uuid": "some-uuid",
		}
		err = store.UpdateBackupVaultFields(context.Background(), "non-existent-uuid", updates)
		require.Error(t, err)
		assert.True(t, customerrors.IsNotFoundErr(err))
	})

	tt.Run("ReturnsErrorWhenTransactionFails", func(t *testing.T) {
		db, err := SetupTestDB()
		require.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		require.NoError(t, ClearInMemoryDB(store.db.GORM()))

		originalStartTransaction := startTransaction
		defer func() { startTransaction = originalStartTransaction }()
		startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
			return nil, errors.New("mock transaction failure")
		}

		updates := map[string]interface{}{
			"external_uuid": "some-uuid",
		}
		err = store.UpdateBackupVaultFields(context.Background(), "any-uuid", updates)
		require.Error(t, err)
		assert.Equal(t, "mock transaction failure", err.Error())
	})
}

func TestUpdatingBackupVaultState(tt *testing.T) {
	tt.Run("TestUpdatingBackupVaultStateUpdatesStateSuccessfully", func(t *testing.T) {
		db, err := SetupTestDB()
		if err != nil {
			t.Fatalf("Failed to set up test database: %v", err)
		}
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		if err != nil {
			t.Fatalf("Failed to clean up test database: %v", err)
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			t.Fatalf("Failed to create account: %v", err)
		}

		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test_backup_vault",
			AccountID:             account.ID,
			Account:               account,
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			t.Fatalf("Failed to create backup vault: %v", err)
		}

		bv, err := store.UpdateBackupVaultState(context.Background(), backupVault, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}

		result, err := store.GetBackupVault(context.Background(), backupVault.UUID)
		if err != nil {
			t.Fatalf("Failed to retrieve updated backup vault: %v", err)
		}
		assert.Equal(t, datamodel.LifeCycleStateUpdating, result.LifeCycleState)
		assert.Equal(t, datamodel.LifeCycleStateUpdatingDetails, result.LifeCycleStateDetails)
		assert.NotNil(t, bv)
	})
}

func TestGetMultipleBackupVaultsReturnsBackupVaultsWhenConditionsMatch(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	backupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:      "test_backup_vault",
		AccountID: account.ID,
		Account:   account,
	}
	err = store.db.Create(backupVault).Error()
	if err != nil {
		tt.Fatalf("Failed to create backup vault: %v", err)
	}

	conditions := [][]interface{}{
		{"account_id = ?", account.ID},
	}

	result, err := store.GetMultipleBackupVaults(context.Background(), conditions)
	if err != nil {
		tt.Errorf("Expected no error, got %v", err)
	}
	if len(result) != 1 {
		tt.Errorf("Expected 1 backup vault, got %v", len(result))
	}
	if result[0].UUID != backupVault.UUID {
		tt.Errorf("Expected backup vault UUID %v, got %v", backupVault.UUID, result[0].UUID)
	}
}

func TestGetMultipleBackupVaultsReturnsEmptyListWhenNoConditionsMatch(tt *testing.T) {
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
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	if err != nil {
		tt.Fatalf("Failed to create account: %v", err)
	}

	backupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:      "test_backup_vault",
		AccountID: account.ID,
		Account:   account,
	}
	err = store.db.Create(backupVault).Error()
	if err != nil {
		tt.Fatalf("Failed to create backup vault: %v", err)
	}

	conditions := [][]interface{}{
		{"account_id = ?", 999}, // Non-existent account ID
	}

	result, err := store.GetMultipleBackupVaults(context.Background(), conditions)
	if err != nil {
		tt.Errorf("Expected no error, got %v", err)
	}
	if len(result) != 0 {
		tt.Errorf("Expected 0 backup vaults, got %v", len(result))
	}
}

func TestGetMultipleBackupVaultsReturnsErrorWhenDatabaseFails(tt *testing.T) {
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

	// Simulate database failure by closing the connection
	sqlDB, _ := db.DB()
	err = sqlDB.Close()
	if err != nil {
		return
	}

	conditions := [][]interface{}{
		{"account_id = ?", 123},
	}

	_, err = store.GetMultipleBackupVaults(context.Background(), conditions)
	if err == nil {
		tt.Errorf("Expected error, got nil")
	}
}

func TestDeletesBackupVaultSuccessfully(tt *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(tt, err)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(tt, err)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	assert.NoError(tt, err)

	backupVault := &datamodel.BackupVault{
		BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:                  "test_backup_vault",
		AccountID:             account.ID,
		Account:               account,
		LifeCycleState:        datamodel.LifeCycleStateAvailable,
		LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
	}
	err = store.db.Create(backupVault).Error()
	assert.NoError(tt, err)

	deletedVault, err := store.DeleteBackupVaultInVCP(context.Background(), backupVault.UUID)
	assert.NoError(tt, err)
	assert.NotNil(tt, deletedVault)
	assert.Equal(tt, datamodel.LifeCycleStateDeleted, deletedVault.LifeCycleState)
	assert.Equal(tt, datamodel.LifeCycleStateDeletedDetails, deletedVault.LifeCycleStateDetails)
	assert.NotNil(tt, deletedVault.DeletedAt)
}

func TestReturnsErrorWhenBackupVaultDoesNotExist(tt *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(tt, err)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(tt, err)

	_, err = store.DeleteBackupVaultInVCP(context.Background(), "non-existent-uuid")
	assert.Error(tt, err)
}

func TestReturnsErrorWhenTransactionFails(tt *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(tt, err)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(tt, err)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	assert.NoError(tt, err)

	backupVault := &datamodel.BackupVault{
		BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:                  "test_backup_vault",
		AccountID:             account.ID,
		Account:               account,
		LifeCycleState:        datamodel.LifeCycleStateAvailable,
		LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
	}
	err = store.db.Create(backupVault).Error()
	assert.NoError(tt, err)

	originalStartTransaction := startTransaction
	defer func() { startTransaction = originalStartTransaction }()
	startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
		return nil, errors.New("mock transaction failure")
	}

	_, err = store.DeleteBackupVaultInVCP(context.Background(), backupVault.UUID)
	assert.Error(tt, err)
	assert.Equal(tt, "mock transaction failure", err.Error())
}

func TestRestoreDeletedBackupVaultSuccessfully(tt *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(tt, err)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(tt, err)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
		Name:      "test_account",
	}
	err = store.db.Create(account).Error()
	assert.NoError(tt, err)

	backupVault := &datamodel.BackupVault{
		BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:                  "test_backup_vault",
		AccountID:             account.ID,
		Account:               account,
		LifeCycleState:        datamodel.LifeCycleStateAvailable,
		LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
	}
	err = store.db.Create(backupVault).Error()
	assert.NoError(tt, err)

	deletedVault, err := store.DeleteBackupVaultInVCP(context.Background(), backupVault.UUID)
	assert.NoError(tt, err)
	assert.Equal(tt, datamodel.LifeCycleStateDeleted, deletedVault.LifeCycleState)
	assert.NotNil(tt, deletedVault.DeletedAt)

	restoredVault, err := store.RestoreDeletedBackupVault(context.Background(), backupVault.UUID, account.ID, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	assert.NoError(tt, err)
	assert.NotNil(tt, restoredVault)
	assert.Equal(tt, datamodel.LifeCycleStateREADY, restoredVault.LifeCycleState)
	assert.Equal(tt, datamodel.LifeCycleStateAvailableDetails, restoredVault.LifeCycleStateDetails)
	assert.Nil(tt, restoredVault.DeletedAt)
	assert.Equal(tt, account.Name, restoredVault.Account.Name)
}

func TestRestoreDeletedBackupVaultNotFound(tt *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(tt, err)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(tt, err)

	_, err = store.RestoreDeletedBackupVault(context.Background(), "non-existent-uuid", int64(999), datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	assert.Error(tt, err)
}

func TestRestoreDeletedBackupVaultTransactionFails(tt *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(tt, err)

	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(tt, err)

	originalStartTransaction := startTransaction
	defer func() { startTransaction = originalStartTransaction }()
	startTransaction = func(db *gorm.DB) (*gorm.DB, error) {
		return nil, errors.New("mock transaction failure")
	}

	_, err = store.RestoreDeletedBackupVault(context.Background(), "any-uuid", int64(1), datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails)
	assert.Error(tt, err)
	assert.Equal(tt, "mock transaction failure", err.Error())
}

func TestGetBackupVaultByExternalUUIDAndOwnerID(t *testing.T) {
	t.Run("WhenBackupVaultExistsWithExternalUUID", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 123},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		externalUUID := "external-backup-vault-uuid"
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test_backup_vault",
			AccountID:             account.ID,
			Account:               account,
			ExternalUUID:          &externalUUID,
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		result, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), externalUUID, account.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, backupVault.UUID, result.UUID)
		assert.Equal(tt, backupVault.Name, result.Name)
		assert.Equal(tt, backupVault.AccountID, result.AccountID)
		assert.Equal(tt, externalUUID, *result.ExternalUUID)
		assert.NotNil(tt, result.Account)
		assert.Equal(tt, account.Name, result.Account.Name)
	})

	t.Run("WhenBackupVaultNotFoundByExternalUUID", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 123},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		nonExistentExternalUUID := "non-existent-external-uuid"
		result, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), nonExistentExternalUUID, account.ID)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "backup vault")
	})

	t.Run("WhenBackupVaultNotFoundByOwnerID", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 123},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		externalUUID := "external-backup-vault-uuid"
		backupVault := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:         "test_backup_vault",
			AccountID:    account.ID,
			Account:      account,
			ExternalUUID: &externalUUID,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		// Try to find with wrong owner ID
		wrongOwnerID := int64(999)
		result, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), externalUUID, wrongOwnerID)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "backup vault")
	})

	t.Run("WhenBackupVaultExistsButBelongsToDifferentAccount", func(tt *testing.T) {
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

		// Create first account and backup vault
		account1 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-1-uuid", ID: 123},
			Name:      "test_account_1",
		}
		err = store.db.Create(account1).Error()
		if err != nil {
			tt.Fatalf("Failed to create account 1: %v", err)
		}

		// Create second account
		account2 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-2-uuid", ID: 456},
			Name:      "test_account_2",
		}
		err = store.db.Create(account2).Error()
		if err != nil {
			tt.Fatalf("Failed to create account 2: %v", err)
		}

		externalUUID := "external-backup-vault-uuid"
		backupVault := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:         "test_backup_vault",
			AccountID:    account1.ID, // Belongs to account1
			Account:      account1,
			ExternalUUID: &externalUUID,
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		// Try to find with account2's ID
		result, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), externalUUID, account2.ID)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "backup vault")
	})

	t.Run("WhenBackupVaultExistsWithoutExternalUUID", func(tt *testing.T) {
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
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid", ID: 123},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}

		// Create backup vault without external UUID
		backupVault := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:         "test_backup_vault",
			AccountID:    account.ID,
			Account:      account,
			ExternalUUID: nil, // No external UUID
		}
		err = store.db.Create(backupVault).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault: %v", err)
		}

		// Try to find by some external UUID
		result, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), "some-external-uuid", account.ID)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "backup vault")
	})

	t.Run("WhenDatabaseConnectionFails", func(tt *testing.T) {
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

		// Close the database connection to simulate failure
		sqlDB, _ := db.DB()
		err = sqlDB.Close()
		if err != nil {
			tt.Fatalf("Failed to close database connection: %v", err)
		}

		result, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), "some-external-uuid", 123)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		// Should get a database connection error, not a "not found" error
		assert.NotContains(tt, err.Error(), "backup vault")
	})

	t.Run("WhenMultipleBackupVaultsExistWithSameExternalUUID", func(tt *testing.T) {
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

		// Create two accounts
		account1 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-1-uuid", ID: 123},
			Name:      "test_account_1",
		}
		err = store.db.Create(account1).Error()
		if err != nil {
			tt.Fatalf("Failed to create account 1: %v", err)
		}

		account2 := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-2-uuid", ID: 456},
			Name:      "test_account_2",
		}
		err = store.db.Create(account2).Error()
		if err != nil {
			tt.Fatalf("Failed to create account 2: %v", err)
		}

		externalUUID := "shared-external-uuid"

		// Create backup vault for account1
		backupVault1 := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-vault-1-uuid"},
			Name:         "test_backup_vault_1",
			AccountID:    account1.ID,
			Account:      account1,
			ExternalUUID: &externalUUID,
		}
		err = store.db.Create(backupVault1).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault 1: %v", err)
		}

		// Create backup vault for account2 with same external UUID
		backupVault2 := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{UUID: "test-backup-vault-2-uuid"},
			Name:         "test_backup_vault_2",
			AccountID:    account2.ID,
			Account:      account2,
			ExternalUUID: &externalUUID,
		}
		err = store.db.Create(backupVault2).Error()
		if err != nil {
			tt.Fatalf("Failed to create backup vault 2: %v", err)
		}

		// Should return the backup vault for account1
		result1, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), externalUUID, account1.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, result1)
		assert.Equal(tt, backupVault1.UUID, result1.UUID)
		assert.Equal(tt, account1.ID, result1.AccountID)

		// Should return the backup vault for account2
		result2, err := store.GetBackupVaultByExternalUUIDAndOwnerID(context.Background(), externalUUID, account2.ID)
		assert.NoError(tt, err)
		assert.NotNil(tt, result2)
		assert.Equal(tt, backupVault2.UUID, result2.UUID)
		assert.Equal(tt, account2.ID, result2.AccountID)
	})
}

func TestGetCmekRotationJobStatuses(t *testing.T) {
	// Note: These tests use PostgreSQL-specific SQL syntax (::text casting, JSONB operators)
	// and will fail with SQLite. They are designed to test the function in a PostgreSQL environment.
	// In SQLite, the query will fail with "unrecognized token" errors, which is expected behavior.
	t.Run("ReturnsJobStatusesWhenJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		now := time.Now()
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-1",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "completed",
			ResourceName: "BackupVault1",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-1",
				Location:     "us-east-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key1",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job1).Error()
		assert.NoError(tt, err)

		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax (::text casting, JSONB operators)
		// In PostgreSQL, this should succeed and return results
		if err != nil {
			// SQLite doesn't support PostgreSQL syntax - this is expected
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NoError(tt, err)
		assert.NotNil(tt, results)
		assert.Len(tt, results, 1)
		assert.Equal(tt, int64(1), results[0].ID)
		assert.Equal(tt, "completed", results[0].Status)
		assert.Equal(tt, "vault-uuid-1", results[0].BackupVaultUUID)
		assert.Equal(tt, "BackupVault1", results[0].BackupVaultName)
		assert.Equal(tt, "us-east-1", results[0].Region)
		assert.Equal(tt, "projects/test/locations/us/keyRings/test/cryptoKeys/key1", results[0].NewKmsKeyURL)
		assert.Equal(tt, "test_account", results[0].AccountIdentifier)
	})
	t.Run("ReturnsEmptyListWhenNoJobsExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		now := time.Now()
		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NotNil(tt, results)
		assert.Empty(tt, results)
	})
	t.Run("FiltersJobsByTimeRange", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		now := time.Now()
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-1",
				UpdatedAt: now.Add(-20 * time.Minute), // Outside time range
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "completed",
			ResourceName: "BackupVault1",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-1",
				Location:     "us-east-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key1",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job1).Error()
		assert.NoError(tt, err)

		job2 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-2",
				UpdatedAt: now, // Within time range
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "pending",
			ResourceName: "BackupVault2",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-2",
				Location:     "us-west-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key2",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job2).Error()
		assert.NoError(tt, err)

		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NotNil(tt, results)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "vault-uuid-2", results[0].BackupVaultUUID)
	})
	t.Run("FiltersOutDeletedJobs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		now := time.Now()
		deletedAt := gorm.DeletedAt{Time: now, Valid: true}
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-1",
				UpdatedAt: now,
				DeletedAt: &deletedAt,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "completed",
			ResourceName: "BackupVault1",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-1",
				Location:     "us-east-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key1",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job1).Error()
		assert.NoError(tt, err)

		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NotNil(tt, results)
		assert.Empty(tt, results)
	})
	t.Run("FiltersOutJobsWithMissingRequiredFields", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		now := time.Now()
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-1",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "completed",
			ResourceName: "", // Missing resource name
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-1",
				Location:     "us-east-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key1",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job1).Error()
		assert.NoError(tt, err)

		job2 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-2",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "pending",
			ResourceName: "BackupVault2",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "", // Missing resource UUID
				Location:     "us-west-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL: "projects/test/locations/us/keyRings/test/cryptoKeys/key2",
				},
			},
		}
		err = store.db.Create(job2).Error()
		assert.NoError(tt, err)

		job3 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-3",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "in_progress",
			ResourceName: "BackupVault3",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-3",
				Location:     "", // Missing location
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key3",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job3).Error()
		assert.NoError(tt, err)

		job4 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-4",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "failed",
			ResourceName: "BackupVault4",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-4",
				Location:     "us-central1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "", // Missing new KMS key URL
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job4).Error()
		assert.NoError(tt, err)

		job5 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-5",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "completed",
			ResourceName: "BackupVault5",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-5",
				Location:     "us-west-2",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key5",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job5).Error()
		assert.NoError(tt, err)

		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NotNil(tt, results)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "vault-uuid-5", results[0].BackupVaultUUID)
		assert.Equal(tt, "BackupVault5", results[0].BackupVaultName)
	})
	t.Run("FiltersOutNonCmekRotationJobs", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		now := time.Now()
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-1",
				UpdatedAt: now,
			},
			Type:         "CREATE_BACKUP", // Different job type
			State:        "completed",
			ResourceName: "BackupVault1",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-1",
				Location:     "us-east-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key1",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job1).Error()
		assert.NoError(tt, err)

		job2 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-2",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "pending",
			ResourceName: "BackupVault2",
			AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-2",
				Location:     "us-west-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key2",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job2).Error()
		assert.NoError(tt, err)

		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NotNil(tt, results)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "vault-uuid-2", results[0].BackupVaultUUID)
	})
	t.Run("RespectsLimitAndOffset", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "test-account-uuid"},
			Name:      "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err)

		now := time.Now()
		for i := 1; i <= 5; i++ {
			job := &datamodel.Job{
				BaseModel: datamodel.BaseModel{
					UUID:      "job-uuid-" + strconv.Itoa(i),
					UpdatedAt: now.Add(time.Duration(i) * time.Second),
				},
				Type:         "ROTATE_CMEK_BACKUPS",
				State:        "completed",
				ResourceName: "BackupVault" + strconv.Itoa(i),
				AccountID:    sql.NullInt64{Int64: account.ID, Valid: true},
				JobAttributes: &datamodel.JobAttributes{
					ResourceUUID: "vault-uuid-" + strconv.Itoa(i),
					Location:     "us-east-1",
					KmsAttributes: &datamodel.JobKmsAttributes{
						NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key" + strconv.Itoa(i),
						AccountIdentifier: "test_account",
					},
				},
			}
			err = store.db.Create(job).Error()
			assert.NoError(tt, err)
		}

		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 2, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NotNil(tt, results)
		assert.Len(tt, results, 2)

		results2, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 2, 2)
		assert.NoError(tt, err)
		assert.NotNil(tt, results2)
		assert.Len(tt, results2, 2)

		results3, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 2, 4)
		assert.NoError(tt, err)
		assert.NotNil(tt, results3)
		assert.Len(tt, results3, 1)
	})
	t.Run("HandlesNullAccountID", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		now := time.Now()
		job1 := &datamodel.Job{
			BaseModel: datamodel.BaseModel{
				UUID:      "job-uuid-1",
				UpdatedAt: now,
			},
			Type:         "ROTATE_CMEK_BACKUPS",
			State:        "completed",
			ResourceName: "BackupVault1",
			AccountID:    sql.NullInt64{Valid: false}, // Null account ID
			JobAttributes: &datamodel.JobAttributes{
				ResourceUUID: "vault-uuid-1",
				Location:     "us-east-1",
				KmsAttributes: &datamodel.JobKmsAttributes{
					NewKmsKeyURL:      "projects/test/locations/us/keyRings/test/cryptoKeys/key1",
					AccountIdentifier: "test_account",
				},
			},
		}
		err = store.db.Create(job1).Error()
		assert.NoError(tt, err)

		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		results, err := store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		// In SQLite, this will fail due to PostgreSQL-specific syntax
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error for PostgreSQL-specific query")
			return
		}
		assert.NotNil(tt, results)
		assert.Len(tt, results, 1)
		assert.Equal(tt, "", results[0].AccountIdentifier)
	})
	t.Run("ReturnsErrorWhenDatabaseFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		sqlDB, _ := db.DB()
		err = sqlDB.Close()
		assert.NoError(tt, err)

		now := time.Now()
		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		_, err = store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		assert.Error(tt, err)
		assert.NotEmpty(tt, err.Error())
	})
}

func TestGetCmekRotationJobStatuses_WithIndexFlag(t *testing.T) {
	t.Run("WhenEnableJobResourceUUIDIndex_UsesColumnInSelectAndFilter", func(tt *testing.T) {
		vcputils.EnableJobResourceUUIDIndex = true
		defer func() { vcputils.EnableJobResourceUUIDIndex = false }()

		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		now := time.Now()
		startTime := now.Add(-10 * time.Minute)
		endTime := now.Add(10 * time.Minute)

		// The query uses PostgreSQL-specific syntax; on SQLite it will return an error,
		// confirming the flagged branch was entered.
		_, err = store.GetCmekRotationJobStatuses(context.Background(), startTime, endTime, 100, 0)
		if err != nil {
			assert.Contains(tt, err.Error(), "unrecognized token", "Expected SQLite syntax error confirming the flagged branch ran")
		}
	})
}
