package database

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gormwrapper "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils/gorm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestGetKmsConfig(t *testing.T) {
	t.Run("WhenKmsConfigExists", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
			Name:      "test",
			AccountID: account.ID,
		}
		err = store.db.Create(account).Error()
		if err != nil {
			tt.Fatalf("Failed to create account: %v", err)
		}
		err = store.db.Create(kmsConfig).Error()
		if err != nil {
			tt.Fatalf("Failed to create kms config: %v", err)
		}

		result, err := store.GetKmsConfig(context.Background(), "test-uuid")
		if err != nil {
			tt.Errorf("Expected no error, got %v", err)
		}
		if result.Name != kmsConfig.Name {
			tt.Errorf("Expected kms config name %v, got %v", kmsConfig.Name, result.Name)
		}
		if result.AccountID != account.ID {
			tt.Errorf("Expected account name %v, got %v", account.ID, result.AccountID)
		}
	})

	t.Run("WhenKmsConfigDoesNotExist", func(tt *testing.T) {
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

		_, err = store.GetKmsConfig(context.Background(), "test-uuid")
		if err == nil {
			tt.Errorf("Expected error, got nil")
		}
		if !customerrors.IsNotFoundErr(err) {
			tt.Errorf("Expected error %v, got %v", gorm.ErrRecordNotFound, err)
		}
	})
}

func TestUpdateUpdateKmsConfigState(t *testing.T) {
	t.Run("WhenUpdateKmsConfigStateIsUpdatedSuccessfully", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			Name:      "test_kms_config",
			AccountID: account.ID,
			BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
		}

		err = store.db.Create(kmsConfig).Error()
		if err != nil {
			tt.Fatalf("Failed to create kms config: %v", err)
		}

		_, err = store.UpdateKmsConfigState(context.Background(), "test-uuid", datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
		assert.NoError(tt, err, "Expected no error, got %v", err)

		updatedkms, err1 := store.GetKmsConfig(context.Background(), "test-uuid")
		assert.NoError(tt, err1, "Expected no error, got %v", err1)
		assert.Equal(tt, datamodel.LifeCycleStateUpdating, updatedkms.State, "Expected volume state %v, got %v", datamodel.LifeCycleStateUpdating, updatedkms.State)
	})
	t.Run("WhenUpdateKmsConfigIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		kms := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "dummy"},
			Name:      "test_volume_rep",
			State:     datamodel.LifeCycleStateUpdating,
		}
		_, err = store.UpdateKmsConfigState(context.Background(), kms.UUID, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails)
		assert.EqualError(tt, err, "KMS Configuration not found", "Expected no error, got %v", err)
	})
}

func TestGetMultipleKMSConfigs(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	serviceAccounts := []*datamodel.ServiceAccount{
		{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10"}, Name: "ServiceAccount1"},
		{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2"},
	}
	kmsConfigs := []*datamodel.KmsConfig{
		{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ServiceAccountID: &serviceAccounts[0].ID},
		{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ServiceAccountID: &serviceAccounts[1].ID},
	}

	err = store.db.Create(serviceAccounts).Error()
	assert.NoError(t, err, "Failed to create Service account table")
	err = store.db.Create(kmsConfigs).Error()
	assert.NoError(t, err, "Failed to create KMS config table")

	t.Run("RetrievesKMSConfigsSuccessfully", func(tt *testing.T) {
		kmsConfigUUIDList := []string{"uuid1", "uuid2"}
		conditions := [][]interface{}{{"uuid in ?", kmsConfigUUIDList}}
		result, err := store.GetMultipleKmsConfigs(context.Background(), conditions)

		assert.NoError(tt, err)
		assert.Equal(tt, "kmsConfig1", result[0].Name)
		assert.Equal(tt, "kmsConfig2", result[1].Name)
		assert.Equal(tt, "ServiceAccount1", result[0].ServiceAccount.Name)
		assert.Equal(tt, "ServiceAccount2", result[1].ServiceAccount.Name)
	})
	t.Run("ReturnsEmptyWhenRecordsAreNotFound", func(tt *testing.T) {
		kmsConfigUUIDList := []string{"nonexistent-uuid"}
		conditions := [][]interface{}{{"uuid in ?", kmsConfigUUIDList}}
		result, err := store.GetMultipleKmsConfigs(context.Background(), conditions)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
	})
	t.Run("HandlesEmptyUUIDListGracefully", func(tt *testing.T) {
		kmsConfigUUIDList := []string{}
		conditions := [][]interface{}{{"uuid in ?", kmsConfigUUIDList}}
		result, err := store.GetMultipleKmsConfigs(context.Background(), conditions)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
	})
}

func TestGetMultipleKMSConfigsDBErrorCondition(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	t.Run("WhenRetrieveKMSConfigsRunsIntoDBError", func(tt *testing.T) {
		dbErr := db.Migrator().DropTable(&datamodel.KmsConfig{})
		if dbErr != nil {
			assert.Fail(tt, "Dropping table KmsConfig from in-memory DB failed; aborting test")
		}
		kmsConfigUUIDList := []string{"uuid1", "uuid2"}
		conditions := [][]interface{}{{"uuid in ?", kmsConfigUUIDList}}
		result, kmsErr := store.GetMultipleKmsConfigs(context.Background(), conditions)

		assert.Error(tt, kmsErr)
		assert.Nil(tt, result)
	})
}

func TestGetKmsConfigsByUUIDs(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "sa-uuid"},
		Name:      "ServiceAccount1",
	}
	kmsConfig := &datamodel.KmsConfig{
		BaseModel:        datamodel.BaseModel{UUID: "kms-uuid"},
		Name:             "kmsConfig1",
		ServiceAccountID: &serviceAccount.ID,
		KmsAttributes: &datamodel.KmsAttributes{
			VcpServiceAccountEmail: "kms-sa@example.com",
		},
	}

	err = store.db.Create(serviceAccount).Error()
	assert.NoError(t, err)
	err = store.db.Create(kmsConfig).Error()
	assert.NoError(t, err)

	t.Run("ReturnsBaseRowAndKmsAttributesWithoutServiceAccountPreload", func(tt *testing.T) {
		// Batch KMS derives every supported field from the base row + KmsAttributes JSON column,
		// so the fetch deliberately does not preload the ServiceAccount relation.
		result, listErr := store.GetKmsConfigsByUUIDs(
			context.Background(),
			[]string{"kms-uuid"},
		)

		require.NoError(tt, listErr)
		require.Len(tt, result, 1)
		assert.Nil(tt, result[0].ServiceAccount)
		assert.Equal(tt, "kms-sa@example.com", result[0].KmsAttributes.VcpServiceAccountEmail)
	})

	t.Run("ReturnsEmptyForUnknownUUIDs", func(tt *testing.T) {
		result, listErr := store.GetKmsConfigsByUUIDs(
			context.Background(),
			[]string{"missing"},
		)

		require.NoError(tt, listErr)
		assert.Empty(tt, result)
	})

	t.Run("ReturnsErrorWhenQueryFails", func(tt *testing.T) {
		err := store.db.GORM().Migrator().DropTable(&datamodel.KmsConfig{})
		require.NoError(tt, err)

		result, listErr := store.GetKmsConfigsByUUIDs(
			context.Background(),
			[]string{"kms-uuid"},
		)

		require.Error(tt, listErr)
		assert.Nil(tt, result)
	})
}

func TestPersistenceStore_GetKmsConfigsByUUIDs(t *testing.T) {
	logger := log.NewLogger()
	storage, err := SetupStorageForTest(logger)
	require.NoError(t, err)

	ps := storage.(*PersistenceStore)
	ctx := context.Background()

	serviceAccount := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "sa-uuid"},
		Name:      "ServiceAccount1",
	}
	kmsConfig := &datamodel.KmsConfig{
		BaseModel:        datamodel.BaseModel{UUID: "kms-uuid"},
		Name:             "kmsConfig1",
		ServiceAccountID: &serviceAccount.ID,
		KmsAttributes: &datamodel.KmsAttributes{
			VcpServiceAccountEmail: "kms-sa@example.com",
		},
	}

	err = ps.db.GORM().Create(serviceAccount).Error
	require.NoError(t, err)
	err = ps.db.GORM().Create(kmsConfig).Error
	require.NoError(t, err)

	result, listErr := ps.GetKmsConfigsByUUIDs(ctx, []string{"kms-uuid"})
	require.NoError(t, listErr)
	require.Len(t, result, 1)
	assert.Equal(t, "kms-uuid", result[0].UUID)
}

func TestCreateGetUpdateListKmsConfigAndGetJob(t *testing.T) {
	db, err := SetupTestDB()
	assert.NoError(t, err, "Failed to set up test database")
	wrapper := gormwrapper.New(db)
	store := NewDataStoreRepository(wrapper)

	err = ClearInMemoryDB(store.db.GORM())
	assert.NoError(t, err, "Failed to clean up test database")

	serviceAccounts := []*datamodel.ServiceAccount{
		{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10", DeletedAt: nil}, Name: "ServiceAccount1", AccountID: 1111, State: KmsSaStateEnable},
		{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2", AccountID: 2222},
	}
	accounts := []*datamodel.Account{
		{BaseModel: datamodel.BaseModel{ID: int64(1111), UUID: "uuid100"}, Name: "Account1"},
		{BaseModel: datamodel.BaseModel{ID: int64(2222), UUID: "uuid200"}, Name: "Account2"},
		{BaseModel: datamodel.BaseModel{ID: int64(3333), UUID: "uuid300"}, Name: "Account3"},
	}
	kmsConfigs := []*datamodel.KmsConfig{
		{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ServiceAccountID: &serviceAccounts[0].ID, AccountID: 1111, State: "Ready", StateDetails: "Key is in Ready state"},
		{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ServiceAccountID: &serviceAccounts[1].ID, AccountID: 2222, State: datamodel.LifeCycleStateAvailable},
		{BaseModel: datamodel.BaseModel{UUID: "uuid3", DeletedAt: nil}, Name: "kmsConfig3", ServiceAccountID: &serviceAccounts[0].ID, AccountID: 2222},
		{BaseModel: datamodel.BaseModel{UUID: "uuid4", DeletedAt: nil}, Name: "kmsConfig4", ServiceAccountID: &serviceAccounts[1].ID, AccountID: 1111},
		{BaseModel: datamodel.BaseModel{UUID: "uuid5", DeletedAt: nil}, Name: "kmsConfig5", AccountID: 3333, State: "Ready", StateDetails: "Key is in Ready state", Description: "kms description"},
		{BaseModel: datamodel.BaseModel{UUID: "uuid6", DeletedAt: nil}, Name: "kmsConfig6", AccountID: 4444, State: datamodel.LifeCycleStateCreating},
		{BaseModel: datamodel.BaseModel{UUID: "uuid7", DeletedAt: nil}, Name: "kmsConfig7", AccountID: 5555, State: datamodel.LifeCycleStateDeleting},
		{BaseModel: datamodel.BaseModel{UUID: "uuid8", DeletedAt: nil}, Name: "kmsConfig8", ServiceAccountID: &serviceAccounts[1].ID, AccountID: 6666, State: datamodel.LifeCycleStateAvailable},
		{BaseModel: datamodel.BaseModel{UUID: "uuid9", DeletedAt: nil}, Name: "kmsConfig9", ServiceAccountID: &serviceAccounts[1].ID, AccountID: 6666, State: "Ready", ResourceID: "kmsConfig9"},
	}
	jobs := []*datamodel.Job{
		{BaseModel: datamodel.BaseModel{UUID: "job-uuid1", DeletedAt: nil}, JobAttributes: &datamodel.JobAttributes{ResourceUUID: "uuid1"}},
		{BaseModel: datamodel.BaseModel{UUID: "job-uuid2", DeletedAt: nil}, JobAttributes: &datamodel.JobAttributes{ResourceUUID: "uuid2"}, Type: "create_kms_config"},
	}
	err = store.db.Create(accounts).Error()
	assert.NoError(t, err, "Failed to create Service account table")
	err = store.db.Create(kmsConfigs).Error()
	assert.NoError(t, err, "Failed to create KMS config table")
	err = store.db.Create(jobs).Error()
	assert.NoError(t, err, "Failed to create Job table")

	t.Run("GetKmsConfigRetrievesKMSConfigSuccessfully", func(tt *testing.T) {
		kmsConfigUUID := "uuid1"
		result, err := store.GetKmsConfigByUUID(context.Background(), kmsConfigUUID)

		assert.NoError(tt, err)
		assert.Equal(tt, "kmsConfig1", result.Name)
	})
	t.Run("GetKmsConfigReturnsErrorWhenRecordIsNotFound", func(tt *testing.T) {
		kmsConfigUUID := "nonexistent-uuid"
		result, err := store.GetKmsConfigByUUID(context.Background(), kmsConfigUUID)

		assert.ErrorContains(tt, err, "KMS Configuration not found")
		assert.Empty(tt, result)
	})
	t.Run("GetKmsConfigVariationRetrievesKMSConfigSuccessfully", func(tt *testing.T) {
		kmsConfig := new(datamodel.KmsConfig)
		kmsConfig.UUID = "uuid1"
		result, err := getKmsConfig(db, kmsConfig)

		assert.NoError(tt, err)
		assert.Equal(tt, "kmsConfig1", result.Name)
	})
	t.Run("GetKmsConfigVariationReturnsErrorWhenRecordIsNotFound", func(tt *testing.T) {
		kmsConfig := new(datamodel.KmsConfig)
		kmsConfig.UUID = "nonexistent-uuid"

		result, err := getKmsConfig(db, kmsConfig)

		assert.ErrorContains(tt, err, "KMS Configuration not found")
		assert.Nil(tt, result)
	})

	t.Run("ListKmsByAccountIDRetrievesKMSConfigsSuccessfully", func(tt *testing.T) {
		accountId := int64(1111)
		result, err := store.ListKmsConfigByAccountID(context.Background(), accountId)

		assert.NoError(tt, err)
		assert.Equal(tt, "kmsConfig1", result[0].Name)
		assert.Equal(tt, "kmsConfig4", result[1].Name)
	})
	t.Run("ListKmsByAccountIDReturnsEmptyWhenRecordsAreNotFound", func(tt *testing.T) {
		accountId := int64(9999)
		result, err := store.ListKmsConfigByAccountID(context.Background(), accountId)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
	})

	t.Run("GetJobByKmsConfigIDRetrievesJobSuccessfully", func(tt *testing.T) {
		kmsConfigUUID := "uuid1"
		result, err := store.GetJobByResourceUUID(context.Background(), kmsConfigUUID, "")

		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid1", result.UUID)
	})
	t.Run("GetJobByKmsConfigIDWithJobTypeFilter", func(tt *testing.T) {
		kmsConfigUUID := "uuid2"
		result, err := store.GetJobByResourceUUID(context.Background(), kmsConfigUUID, "create_kms_config")

		assert.NoError(tt, err)
		assert.Equal(tt, "job-uuid2", result.UUID)
	})
	t.Run("GetJobByKmsConfigIDReturnsErrorWhenRecordIsNotFound", func(tt *testing.T) {
		kmsConfigUUID := "nonexistent-uuid"
		result, err := store.GetJobByResourceUUID(context.Background(), kmsConfigUUID, "")

		assert.ErrorContains(tt, err, "Job not found")
		assert.Nil(tt, result)
	})

	t.Run("UpdateKmsConfigStateUpdatesKMSConfigSuccessfully", func(tt *testing.T) {
		kmsConfig := new(datamodel.KmsConfig)
		kmsConfig.UUID = "uuid1"
		kmsConfig.State = "In_Use"
		kmsConfig.StateDetails = "Key in use"

		_, err = store.UpdateKmsConfigState(context.Background(), kmsConfig.UUID, kmsConfig.State, kmsConfig.StateDetails)
		assert.NoError(tt, err)

		result, err := store.GetKmsConfigByUUID(context.Background(), "uuid1")
		assert.NoError(tt, err)
		assert.Equal(tt, "In_Use", result.State)
		assert.Equal(tt, "Key in use", result.StateDetails)
	})
	t.Run("UpdateKmsConfigStateReturnsErrorWhenRecordIsNotFound", func(tt *testing.T) {
		kmsConfig := new(datamodel.KmsConfig)
		kmsConfig.UUID = "nonexistent-uuid"

		_, err = store.UpdateKmsConfigState(context.Background(), kmsConfig.UUID, kmsConfig.State, kmsConfig.StateDetails)
		assert.ErrorContains(tt, err, "KMS Configuration not found")
	})
	t.Run("UpdateKmsConfigAttributesSuccessfully", func(tt *testing.T) {
		kmsConfigAttributes := datamodel.KmsAttributes{
			SdeKmsConfigUUID:       "uuid-sde",
			SdeServiceAccountEmail: "sa-sde@sde.com",
			Instructions:           "Instructions",
		}
		kmsConfigUpdated, err := store.UpdateKmsConfigAttributes(context.Background(), kmsConfigs[8].UUID, &kmsConfigAttributes)

		assert.NoError(tt, err)
		assert.NotNil(tt, kmsConfigUpdated)
		assert.Equal(tt, kmsConfigAttributes.SdeKmsConfigUUID, kmsConfigUpdated.KmsAttributes.SdeKmsConfigUUID)
		assert.Equal(tt, kmsConfigAttributes.SdeServiceAccountEmail, kmsConfigUpdated.KmsAttributes.SdeServiceAccountEmail)
		assert.Equal(tt, kmsConfigAttributes.Instructions, kmsConfigUpdated.KmsAttributes.Instructions)
	})
	t.Run("UpdateKmsConfigAttributesWhenKMSConfigRecordIsNotPresent", func(tt *testing.T) {
		kmsConfigAttributes := datamodel.KmsAttributes{
			SdeKmsConfigUUID:       "uuid-sde",
			SdeServiceAccountEmail: "sa-sde@sde.com",
			Instructions:           "Instructions",
		}
		kmsConfigUpdated, err := store.UpdateKmsConfigAttributes(context.Background(), "non-existent-uuid", &kmsConfigAttributes)

		assert.Error(tt, err)
		assert.Nil(tt, kmsConfigUpdated)
	})

	t.Run("UpdateKmsConfigDetailsInternalWhenFullPathIsInvalid", func(tt *testing.T) {
		keyFullPathInvalid := "projects/projectId/locations/australia-southeast1/keyRings/KeyRingName/cryptoKeysKeyName"
		resultUpdate, err := _updateKmsConfigDetails(db, kmsConfigs[8].UUID, keyFullPathInvalid, kmsConfigs[8].ResourceID)

		assert.Error(tt, err)
		assert.Nil(tt, resultUpdate)
	})
	t.Run("UpdateKmsConfigDetailsInternalWhenRecordIsNotFound", func(tt *testing.T) {
		uuidNonExistent := "uuid-non-existent"
		keyFullPath := "projects/projectId/locations/australia-southeast1/keyRings/KeyRingName/cryptoKeys/KeyName"
		resultUpdate, err := _updateKmsConfigDetails(db, uuidNonExistent, keyFullPath, kmsConfigs[8].ResourceID)

		assert.Error(tt, err)
		assert.Nil(tt, resultUpdate)
	})
	t.Run("WhenUpdateKmsConfigDetailsInternalIsSuccessful", func(tt *testing.T) {
		resourceID := "resourceIdUpdated"
		keyFullPathInvalid := "projects/projectId/locations/australia-southeast1/keyRings/KeyRingName/cryptoKeys/KeyName"
		resultUpdate, err := _updateKmsConfigDetails(db, kmsConfigs[8].UUID, keyFullPathInvalid, resourceID)

		assert.NoError(tt, err)
		assert.NotNil(tt, resultUpdate)
		assert.Equal(tt, resourceID, resultUpdate.ResourceID)
		assert.Equal(tt, "KeyName", resultUpdate.KeyName)
		assert.Equal(tt, "KeyRingName", resultUpdate.KeyRing)
		assert.Equal(tt, "australia-southeast1", resultUpdate.KeyRingLocation)
		assert.Equal(tt, "projectId", resultUpdate.KeyProjectID)
	})
	t.Run("WhenUpdateKmsConfigDetailsInternalSetsKmsGrantFromKeyFullPath", func(tt *testing.T) {
		resourceID := "resourceIdWithGrant"
		keyFullPathWithGrant := "projects/projectId/locations/australia-southeast1/keyRings/KeyRingName/cryptoKeys/KeyName/grants/grant-id"
		resultUpdate, err := _updateKmsConfigDetails(db, kmsConfigs[8].UUID, keyFullPathWithGrant, resourceID)

		assert.NoError(tt, err)
		assert.NotNil(tt, resultUpdate)
		assert.Equal(tt, "grant-id", resultUpdate.KmsGrant)
		assert.Equal(tt, "KeyName", resultUpdate.KeyName)
		assert.Equal(tt, "KeyRingName", resultUpdate.KeyRing)
		assert.Equal(tt, "australia-southeast1", resultUpdate.KeyRingLocation)
		assert.Equal(tt, "projectId", resultUpdate.KeyProjectID)
	})
	t.Run("WhenUpdateKmsConfigDetailsExternalIsSuccessful", func(tt *testing.T) {
		resourceID := "resourceIdUpdated"
		keyFullPathInvalid := "projects/projectId/locations/australia-southeast1/keyRings/KeyRingName/cryptoKeys/KeyName"
		resultUpdate, err := store.UpdateKmsConfigDetails(context.Background(), kmsConfigs[8].UUID, keyFullPathInvalid, resourceID)

		assert.NoError(tt, err)
		assert.NotNil(tt, resultUpdate)
		assert.Equal(tt, resourceID, resultUpdate.ResourceID)
		assert.Equal(tt, "KeyName", resultUpdate.KeyName)
		assert.Equal(tt, "KeyRingName", resultUpdate.KeyRing)
		assert.Equal(tt, "australia-southeast1", resultUpdate.KeyRingLocation)
		assert.Equal(tt, "projectId", resultUpdate.KeyProjectID)
	})
	t.Run("WhenUpdateKmsConfigDetailsExternalRunsIntoError", func(tt *testing.T) {
		keyFullPathInvalid := "projects/projectId/locations/australia-southeast1/keyRings/KeyRingName/cryptoKeysKeyName"
		resultUpdate, err := store.UpdateKmsConfigDetails(context.Background(), kmsConfigs[8].UUID, keyFullPathInvalid, kmsConfigs[8].ResourceID)

		assert.Error(tt, err)
		assert.Nil(tt, resultUpdate)
	})

	t.Run("CreatesKmsConfigFailsWhenAnotherIsPresentInCreatingState", func(tt *testing.T) {
		kmsConfigInCreatingState := &datamodel.KmsConfig{
			AccountID: 4444,
		}
		result, err := store.CreateKmsConfig(context.Background(), kmsConfigInCreatingState)
		assert.Error(tt, err)
		assert.Equal(tt, "another config create operation is in progress for this region and project", err.Error())
		assert.Nil(tt, result)
	})
	t.Run("CreatesKmsConfigFailsWhenAnotherIsPresentInDeletingState", func(tt *testing.T) {
		kmsConfigInDeletingState := &datamodel.KmsConfig{
			AccountID: 5555,
		}
		result, err := store.CreateKmsConfig(context.Background(), kmsConfigInDeletingState)
		assert.Error(tt, err)
		assert.Equal(tt, "another config delete operation is in progress for this region and project", err.Error())
		assert.Nil(tt, result)
	})
	t.Run("CreatesKmsConfigWhenFailsWhenState", func(tt *testing.T) {
		kmsConfigTest := &datamodel.KmsConfig{
			AccountID: 6666,
		}
		result, err := store.CreateKmsConfig(context.Background(), kmsConfigTest)
		assert.NoError(tt, err)
		assert.Equal(tt, kmsConfigs[7].UUID, result.UUID)
		assert.Equal(tt, kmsConfigs[7].Name, result.Name)
	})
	t.Run("CreatesKmsConfigSuccessfully", func(tt *testing.T) {
		ctx := context.Background()
		kmsConfigCreate := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: utils.RandomUUID(),
			},
			AccountID:       1111,
			Name:            "kmsConfigCreate",
			State:           "Ready",
			StateDetails:    "Read for use",
			Description:     "KMS config for creation",
			KeyRingLocation: "global",
			KeyRing:         "key-ring",
			KeyName:         "key-name",
		}

		resultCreate, err := store.CreateKmsConfig(ctx, kmsConfigCreate)
		assert.NoError(tt, err)
		assert.NotNil(tt, resultCreate)
		assert.Equal(tt, kmsConfigCreate.Name, resultCreate.Name)

		resultGet, err := store.GetKmsConfigByUUID(ctx, resultCreate.UUID)
		assert.NoError(tt, err)

		assert.Equal(tt, kmsConfigCreate.Name, resultGet.Name)
		assert.Equal(tt, kmsConfigCreate.State, resultGet.State)
		assert.Equal(tt, kmsConfigCreate.StateDetails, resultGet.StateDetails)
		assert.Equal(tt, kmsConfigCreate.Description, resultGet.Description)
		assert.Equal(tt, kmsConfigCreate.KeyRingLocation, resultGet.KeyRingLocation)
		assert.Equal(tt, kmsConfigCreate.KeyRing, resultGet.KeyRing)
		assert.Equal(tt, kmsConfigCreate.KeyName, resultGet.KeyName)
		assert.Equal(tt, accounts[0].Name, resultGet.Account.Name)
	})
	t.Run("CreatesSecondKmsConfigWhenDifferentKeyPathExistsAndFlagEnabled", func(tt *testing.T) {
		originalEnableMultipleCmek := utils.EnableMultipleCmek
		originalMaxNumberOfKmsPerAccount := utils.MaxNumberOfKmsPerAccount
		defer func() {
			utils.EnableMultipleCmek = originalEnableMultipleCmek
			utils.MaxNumberOfKmsPerAccount = originalMaxNumberOfKmsPerAccount
		}()
		utils.EnableMultipleCmek = true
		utils.MaxNumberOfKmsPerAccount = 5

		ctx := context.Background()
		kmsConfigCreate := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: utils.RandomUUID(),
			},
			AccountID:       1111,
			Name:            "kmsConfigSecondKey",
			State:           datamodel.LifeCycleStateAvailable,
			KeyProjectID:    "project-a",
			KeyRingLocation: "global",
			KeyRing:         "ring-a",
			KeyName:         "key-a",
		}

		resultCreate, err := store.CreateKmsConfig(ctx, kmsConfigCreate)
		assert.NoError(tt, err)
		assert.NotNil(tt, resultCreate)
		assert.Equal(tt, kmsConfigCreate.Name, resultCreate.Name)
	})
	t.Run("RejectsCreateWhenMaxKmsConfigLimitReachedAndFlagEnabled", func(tt *testing.T) {
		originalEnableMultipleCmek := utils.EnableMultipleCmek
		originalMaxNumberOfKmsPerAccount := utils.MaxNumberOfKmsPerAccount
		defer func() {
			utils.EnableMultipleCmek = originalEnableMultipleCmek
			utils.MaxNumberOfKmsPerAccount = originalMaxNumberOfKmsPerAccount
		}()
		utils.EnableMultipleCmek = true
		utils.MaxNumberOfKmsPerAccount = 3

		ctx := context.Background()
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 7777, UUID: "uuid777"}, Name: "AccountMax"}
		existingConfigs := []*datamodel.KmsConfig{
			{BaseModel: datamodel.BaseModel{UUID: "max-1"}, Name: "kms1", AccountID: 7777, State: datamodel.LifeCycleStateAvailable, KeyProjectID: "p1", KeyRingLocation: "l1", KeyRing: "r1", KeyName: "k1"},
			{BaseModel: datamodel.BaseModel{UUID: "max-2"}, Name: "kms2", AccountID: 7777, State: datamodel.LifeCycleStateAvailable, KeyProjectID: "p2", KeyRingLocation: "l2", KeyRing: "r2", KeyName: "k2"},
			{BaseModel: datamodel.BaseModel{UUID: "max-3"}, Name: "kms3", AccountID: 7777, State: datamodel.LifeCycleStateAvailable, KeyProjectID: "p3", KeyRingLocation: "l3", KeyRing: "r3", KeyName: "k3"},
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)
		err = store.db.Create(existingConfigs).Error()
		require.NoError(tt, err)

		kmsConfigCreate := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: utils.RandomUUID()},
			AccountID:       7777,
			Name:            "kmsConfigFourth",
			State:           datamodel.LifeCycleStateAvailable,
			KeyProjectID:    "p4",
			KeyRingLocation: "l4",
			KeyRing:         "r4",
			KeyName:         "k4",
		}

		resultCreate, err := store.CreateKmsConfig(ctx, kmsConfigCreate)
		assert.Error(tt, err)
		assert.True(tt, customerrors.IsConflictErr(err))
		assert.ErrorContains(tt, err, maxKmsConfigLimitReachedForAccountError)
		assert.Nil(tt, resultCreate)
	})
	t.Run("AllowsCreateWhenBelowMaxKmsConfigLimitAndFlagEnabled", func(tt *testing.T) {
		originalEnableMultipleCmek := utils.EnableMultipleCmek
		originalMaxNumberOfKmsPerAccount := utils.MaxNumberOfKmsPerAccount
		defer func() {
			utils.EnableMultipleCmek = originalEnableMultipleCmek
			utils.MaxNumberOfKmsPerAccount = originalMaxNumberOfKmsPerAccount
		}()
		utils.EnableMultipleCmek = true
		utils.MaxNumberOfKmsPerAccount = 3

		ctx := context.Background()
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 8888, UUID: "uuid888"}, Name: "AccountBelowMax"}
		existingConfigs := []*datamodel.KmsConfig{
			{BaseModel: datamodel.BaseModel{UUID: "below-1"}, Name: "kms1", AccountID: 8888, State: datamodel.LifeCycleStateAvailable, KeyProjectID: "p1", KeyRingLocation: "l1", KeyRing: "r1", KeyName: "k1"},
			{BaseModel: datamodel.BaseModel{UUID: "below-2"}, Name: "kms2", AccountID: 8888, State: datamodel.LifeCycleStateAvailable, KeyProjectID: "p2", KeyRingLocation: "l2", KeyRing: "r2", KeyName: "k2"},
		}
		err := store.db.Create(account).Error()
		require.NoError(tt, err)
		err = store.db.Create(existingConfigs).Error()
		require.NoError(tt, err)

		kmsConfigCreate := &datamodel.KmsConfig{
			BaseModel:       datamodel.BaseModel{UUID: utils.RandomUUID()},
			AccountID:       8888,
			Name:            "kmsConfigThird",
			State:           datamodel.LifeCycleStateAvailable,
			KeyProjectID:    "p3",
			KeyRingLocation: "l3",
			KeyRing:         "r3",
			KeyName:         "k3",
		}

		resultCreate, err := store.CreateKmsConfig(ctx, kmsConfigCreate)
		assert.NoError(tt, err)
		assert.NotNil(tt, resultCreate)
		assert.Equal(tt, kmsConfigCreate.Name, resultCreate.Name)
	})
	t.Run("ReturnsExistingAvailableConfigWhenDifferentKeyPathAndFlagDisabled", func(tt *testing.T) {
		originalEnableMultipleCmek := utils.EnableMultipleCmek
		defer func() { utils.EnableMultipleCmek = originalEnableMultipleCmek }()
		utils.EnableMultipleCmek = false

		ctx := context.Background()
		kmsConfigCreate := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: utils.RandomUUID(),
			},
			AccountID:       2222,
			Name:            "kmsConfigDifferentKey",
			State:           datamodel.LifeCycleStateAvailable,
			KeyProjectID:    "project-b",
			KeyRingLocation: "us-east4",
			KeyRing:         "ring-b",
			KeyName:         "key-b",
		}

		resultCreate, err := store.CreateKmsConfig(ctx, kmsConfigCreate)
		assert.NoError(tt, err)
		assert.NotNil(tt, resultCreate)
		assert.Equal(tt, "uuid2", resultCreate.UUID)
		assert.Equal(tt, "kmsConfig2", resultCreate.Name)
	})
	t.Run("CreatesKmsConfigWhenDifferentKeyPathAndAnotherInCreatingStateAndFlagEnabled", func(tt *testing.T) {
		originalEnableMultipleCmek := utils.EnableMultipleCmek
		defer func() { utils.EnableMultipleCmek = originalEnableMultipleCmek }()
		utils.EnableMultipleCmek = true

		ctx := context.Background()
		kmsConfigCreate := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: utils.RandomUUID(),
			},
			AccountID:       4444,
			Name:            "kmsConfigParallelCreate",
			State:           datamodel.LifeCycleStateAvailable,
			KeyProjectID:    "project-c",
			KeyRingLocation: "us-central1",
			KeyRing:         "ring-c",
			KeyName:         "key-c",
		}

		resultCreate, err := store.CreateKmsConfig(ctx, kmsConfigCreate)
		assert.NoError(tt, err)
		assert.NotNil(tt, resultCreate)
		assert.Equal(tt, kmsConfigCreate.Name, resultCreate.Name)
		assert.Equal(tt, kmsConfigCreate.UUID, resultCreate.UUID)
	})
}

func TestDeleteKmsConfig(t *testing.T) {
	t.Run("WhenKmsConfigIsDeletedSuccessfully", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			Name:      "test_kms_config",
			AccountID: account.ID,
			BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
		}

		err = store.db.Create(kmsConfig).Error()
		if err != nil {
			tt.Fatalf("Failed to create kms config: %v", err)
		}

		deletedKmsConfig, err := store.DeleteKmsConfig(context.Background(), kmsConfig.UUID, datamodel.LifeCycleStateDeleted, "")
		assert.NoError(tt, err, "Expected no error, got %v", err)
		assert.NotNil(tt, deletedKmsConfig.DeletedAt, "Expected kms config to be deleted, got %v", deletedKmsConfig.DeletedAt)
		assert.Equal(tt, datamodel.LifeCycleStateDeleted, deletedKmsConfig.State, "Expected kms config state %v, got %v", datamodel.LifeCycleStateDeleted, deletedKmsConfig.State)
		assert.Equal(tt, "", deletedKmsConfig.StateDetails, "Expected kms config details %v, got %v", "", deletedKmsConfig.StateDetails)

		_, err = store.GetKmsConfigByUUID(context.Background(), kmsConfig.UUID)
		assert.EqualError(tt, err, "KMS Configuration not found", "Expected no error, got %v", err)
	})
	t.Run("WhenKmsConfigIsNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		deletedKmsConfig, err := store.DeleteKmsConfig(context.Background(), "dummy", datamodel.LifeCycleStateDeleted, "")
		assert.Nil(tt, deletedKmsConfig, "Expected nil volume replication, got %v", deletedKmsConfig)
		assert.EqualError(tt, err, "KMS Configuration not found", "Expected no error, got %v", err)
	})
}

func TestGetKmsConfigByKeyFullPath(t *testing.T) {
	t.Run("GetKmsConfigByKeyFullPathReturnsKmsConfigSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"}, Name: "account"}
		serviceAccount := &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{ID: 2, UUID: "sa-uuid"}, Name: "sa", AccountID: 1}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:         datamodel.BaseModel{UUID: "kms-uuid"},
			Name:              "kms",
			AccountID:         1,
			ServiceAccountID:  &serviceAccount.ID,
			KeyRingLocation:   "us-central1",
			KeyRing:           "ring1",
			KeyName:           "key1",
			CustomerProjectID: "projectNumber",
			KeyProjectID:      "project1",
		}
		err = store.db.Create(account).Error()
		assert.NoError(t, err)
		err = store.db.Create(serviceAccount).Error()
		assert.NoError(t, err)
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(t, err)

		keyFullPath := "projects/project1/locations/us-central1/keyRings/ring1/cryptoKeys/key1"
		result, err := store.GetKmsConfigByKeyFullPath(context.Background(), keyFullPath, account.ID)
		assert.NoError(t, err)
		assert.Equal(t, kmsConfig.UUID, result.UUID)
		assert.Equal(t, kmsConfig.Name, result.Name)
		assert.Equal(t, serviceAccount.Name, result.ServiceAccount.Name)
		assert.Equal(t, account.Name, result.Account.Name)
	})
	t.Run("TestGetKmsConfigByKeyFullPathReturnsErrorWhenKeyFullPathIsInvalid", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err)

		invalidKeyFullPath := "invalid/key/full/path"
		result, err := store.GetKmsConfigByKeyFullPath(context.Background(), invalidKeyFullPath, 1)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
	t.Run("TestGetKmsConfigByKeyFullPathReturnsNotFoundWhenNoRecordExists", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)
		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(t, err)

		keyFullPath := "projects/project1/locations/us-central1/keyRings/ring1/cryptoKeys/key1"
		result, err := store.GetKmsConfigByKeyFullPath(context.Background(), keyFullPath, 1)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestIsKmsConfigInUse(t *testing.T) {
	t.Run("ReturnsTrueWhenKmsConfigIsInUseByAtLeastOneVM", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:      "test-kms",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err)

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()

		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return true, nil
		}

		inUse, err := store.IsKmsConfigInUse(context.Background(), "test-kms-uuid")
		assert.NoError(tt, err)
		assert.True(tt, inUse)
	})

	t.Run("ReturnsFalseWhenKmsConfigIsNotInUseByAnyVM", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:      "test-kms",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err)

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()

		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return false, nil
		}

		inUse, err := store.IsKmsConfigInUse(context.Background(), "test-kms-uuid")
		assert.NoError(tt, err)
		assert.False(tt, inUse)
	})

	t.Run("ReturnsErrorWhenKmsConfigDoesNotExist", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		inUse, err := store.IsKmsConfigInUse(context.Background(), "non-existent-uuid")
		assert.Error(tt, err)
		assert.False(tt, inUse)
		assert.True(tt, customerrors.IsNotFoundErr(err))
	})

	t.Run("ReturnsErrorWhenDatabaseOperationFails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err)
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err)

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:      "test-kms",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err)

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()

		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return false, fmt.Errorf("database error")
		}

		inUse, err := store.IsKmsConfigInUse(context.Background(), "test-kms-uuid")
		assert.Error(tt, err)
		assert.False(tt, inUse)
		assert.Contains(tt, err.Error(), "database error")
	})
}

func Test_isKmsConfigInUse(t *testing.T) {
	t.Run("ReturnsFalseWhenNotFoundErrorOccurs", func(t *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err)

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "kms-uuid"},
			Name:      "test-kms",
		}

		originalGetPoolsByKmsConfigID := getPoolsByKmsConfigID
		defer func() { getPoolsByKmsConfigID = originalGetPoolsByKmsConfigID }()

		getPoolsByKmsConfigID = func(db *gorm.DB, kmsConfigID int64) ([]*datamodel.Pool, error) {
			return nil, errors.New("some error")
		}

		inUse, err := _isKmsConfigInUse(db, kmsConfig)
		assert.Error(t, err)
		assert.False(t, inUse)
	})
	t.Run("ReturnsErrorWhenDatabaseErrorOccurs", func(t *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(t, err)

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "kms-uuid"},
			Name:      "test-kms",
		}

		originalGetPoolsByKmsConfigID := getPoolsByKmsConfigID
		defer func() { getPoolsByKmsConfigID = originalGetPoolsByKmsConfigID }()

		getPoolsByKmsConfigID = func(db *gorm.DB, kmsConfigID int64) ([]*datamodel.Pool, error) {
			return nil, fmt.Errorf("database connection error")
		}

		inUse, err := _isKmsConfigInUse(db, kmsConfig)
		assert.Error(t, err)
		assert.False(t, inUse)
		assert.Contains(t, err.Error(), "database connection error")
	})
}

func TestUpdateKmsConfigStateForHandleResource(t *testing.T) {
	t.Run("SuccessfullyUpdatesStateToDisabledOnOffEvent", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: "Initial state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource disabled", datamodel.StateOff)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateDisabled, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateDisabledDetails, result.StateDetails)
	})

	t.Run("SuccessfullyUpdatesStateToInUseOnOnEventWhenKmsConfigIsInUse", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: "Disabled state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()
		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return true, nil
		}

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource enabled", datamodel.StateOn)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateInUse, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateInUseDetails, result.StateDetails, "Should set 'In use' state details when KMS config is in use")
	})

	t.Run("SuccessfullyUpdatesStateToCreatedOnOnEventWhenKmsConfigIsNotInUse", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: "Disabled state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()
		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return false, nil
		}

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource enabled", datamodel.StateOn)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateCreated, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateCreatedDetails, result.StateDetails, "Should set 'Created successfully' state details when KMS config is not in use")
	})

	t.Run("ReturnsErrorOnOnEventWhenKmsConfigIsNotInDisabledState", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: "Available state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource enabled", datamodel.StateOn)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Cannot Enable gcpKmsConfig which is not in disabled state")
	})

	t.Run("ReturnsErrorWhenKmsConfigNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "non-existent-uuid", "State details", datamodel.StateOff)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, customerrors.IsNotFoundErr(err))
	})

	t.Run("ReturnsErrorOnInvalidEvent", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: "Available state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "State details", "INVALID_EVENT")
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "Invalid event")
	})

	t.Run("ReturnsErrorWhenIsKmsConfigInUseFailsOnOnEvent", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: "Disabled state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()
		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return false, fmt.Errorf("database connection error")
		}

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource enabled", datamodel.StateOn)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "database connection error")
	})
}

func TestUpdateKmsConfig(t *testing.T) {
	t.Run("UpdatesKmsConfigSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create test service account
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-sa-uuid",
			},
			Name:      "test_service_account",
			AccountID: account.ID,
			State:     KmsSaStateEnable,
		}
		err = store.db.Create(serviceAccount).Error()
		assert.NoError(tt, err, "Failed to create service account")

		// Create test KMS config
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test_kms_config",
			AccountID:    account.ID,
			State:        "Ready",
			StateDetails: "Initial state",
			Description:  "Test KMS config",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		// Prepare updates
		updates := map[string]interface{}{
			"service_account_id": serviceAccount.ID,
			"state":              "Updated",
			"state_details":      "Updated state details",
			"description":        "Updated description",
		}

		// Execute update
		err = store.UpdateKmsConfig(context.Background(), kmsConfig.UUID, updates)
		assert.NoError(tt, err, "Expected no error during update")

		// Verify the update
		updatedKmsConfig, err := store.GetKmsConfigByUUID(context.Background(), kmsConfig.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated KMS config")
		assert.Equal(tt, int64(2), *updatedKmsConfig.ServiceAccountID, "Expected service account ID to be updated")
		assert.Equal(tt, "Updated", updatedKmsConfig.State, "Expected state to be updated")
		assert.Equal(tt, "Updated state details", updatedKmsConfig.StateDetails, "Expected state details to be updated")
		assert.Equal(tt, "Updated description", updatedKmsConfig.Description, "Expected description to be updated")
		assert.NotNil(tt, updatedKmsConfig.UpdatedAt, "Expected updated_at to be set")
	})

	t.Run("UpdatesPartialFieldsSuccessfully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create test KMS config
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test_kms_config",
			AccountID:    account.ID,
			State:        "Ready",
			StateDetails: "Initial state",
			Description:  "Test KMS config",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		// Update only specific fields
		updates := map[string]interface{}{
			"state": "Partially Updated",
		}

		// Execute update
		err = store.UpdateKmsConfig(context.Background(), kmsConfig.UUID, updates)
		assert.NoError(tt, err, "Expected no error during partial update")

		// Verify the update
		updatedKmsConfig, err := store.GetKmsConfigByUUID(context.Background(), kmsConfig.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated KMS config")
		assert.Equal(tt, "Partially Updated", updatedKmsConfig.State, "Expected state to be updated")
		assert.Equal(tt, "Initial state", updatedKmsConfig.StateDetails, "Expected state details to remain unchanged")
		assert.Equal(tt, "Test KMS config", updatedKmsConfig.Description, "Expected description to remain unchanged")
	})

	t.Run("ReturnsErrorWhenKmsConfigNotFound", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Try to update non-existent KMS config
		updates := map[string]interface{}{
			"state": "Updated",
		}

		err = store.UpdateKmsConfig(context.Background(), "non-existent-uuid", updates)
		assert.Error(tt, err, "Expected error for non-existent KMS config")
		assert.Contains(tt, err.Error(), "KMS Configuration not found")
	})

	t.Run("HandlesEmptyUpdatesMapGracefully", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create test KMS config
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:      "test_kms_config",
			AccountID: account.ID,
			State:     "Ready",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		// Update with empty map (should only update updated_at)
		updates := map[string]interface{}{}

		// Execute update
		err = store.UpdateKmsConfig(context.Background(), kmsConfig.UUID, updates)
		assert.NoError(tt, err, "Expected no error with empty updates map")

		// Verify that updated_at was still set
		updatedKmsConfig, err := store.GetKmsConfigByUUID(context.Background(), kmsConfig.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated KMS config")
		assert.NotNil(tt, updatedKmsConfig.UpdatedAt, "Expected updated_at to be set even with empty updates")
	})

	t.Run("UpdatesResourceIDAndKeyDetails", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create test KMS config
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:      "test_kms_config",
			AccountID: account.ID,
			State:     "Ready",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		// Update with key and resource details
		updates := map[string]interface{}{
			"resource_id":         "new-resource-id-123",
			"key_ring_location":   "us-central1",
			"key_ring":            "test-key-ring",
			"key_name":            "test-crypto-key",
			"key_project_id":      "test-project-123",
			"customer_project_id": "customer-project-456",
		}

		// Execute update
		err = store.UpdateKmsConfig(context.Background(), kmsConfig.UUID, updates)
		assert.NoError(tt, err, "Expected no error during update")

		// Verify the update
		updatedKmsConfig, err := store.GetKmsConfigByUUID(context.Background(), kmsConfig.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated KMS config")
		assert.Equal(tt, "new-resource-id-123", updatedKmsConfig.ResourceID, "Expected resource ID to be updated")
		assert.Equal(tt, "us-central1", updatedKmsConfig.KeyRingLocation, "Expected key ring location to be updated")
		assert.Equal(tt, "test-key-ring", updatedKmsConfig.KeyRing, "Expected key ring to be updated")
		assert.Equal(tt, "test-crypto-key", updatedKmsConfig.KeyName, "Expected key name to be updated")
		assert.Equal(tt, "test-project-123", updatedKmsConfig.KeyProjectID, "Expected key project ID to be updated")
		assert.Equal(tt, "customer-project-456", updatedKmsConfig.CustomerProjectID, "Expected customer project ID to be updated")
	})

	t.Run("HandlesNilServiceAccountIDUpdate", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create test service account
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "test-sa-uuid",
			},
			Name:      "test_service_account",
			AccountID: account.ID,
		}
		err = store.db.Create(serviceAccount).Error()
		assert.NoError(tt, err, "Failed to create service account")

		// Create test KMS config with service account
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:        datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:             "test_kms_config",
			AccountID:        account.ID,
			ServiceAccountID: &serviceAccount.ID,
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		// Update to set service account ID to nil
		updates := map[string]interface{}{
			"service_account_id": nil,
		}

		// Execute update
		err = store.UpdateKmsConfig(context.Background(), kmsConfig.UUID, updates)
		assert.NoError(tt, err, "Expected no error when setting service account ID to nil")

		// Verify the update
		updatedKmsConfig, err := store.GetKmsConfigByUUID(context.Background(), kmsConfig.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated KMS config")
		assert.Nil(tt, updatedKmsConfig.ServiceAccountID, "Expected service account ID to be nil")
	})

	t.Run("UpdatesMultipleFieldsAtOnce", func(tt *testing.T) {
		db, err := SetupTestDB()
		assert.NoError(tt, err, "Failed to set up test database")
		wrapper := gormwrapper.New(db)
		store := NewDataStoreRepository(wrapper)

		err = ClearInMemoryDB(store.db.GORM())
		assert.NoError(tt, err, "Failed to clean up test database")

		// Create test account
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				ID:   1,
				UUID: "test-account-uuid",
			},
			Name: "test_account",
		}
		err = store.db.Create(account).Error()
		assert.NoError(tt, err, "Failed to create account")

		// Create test KMS config
		originalTimestamp := time.Now().Add(-1 * time.Hour)
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-kms-uuid",
				UpdatedAt: originalTimestamp,
			},
			Name:         "test_kms_config",
			AccountID:    account.ID,
			State:        "Ready",
			StateDetails: "Initial state",
			Description:  "Initial description",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		// Update multiple fields
		updates := map[string]interface{}{
			"state":         "MultiUpdated",
			"state_details": "Multi update state details",
			"description":   "Multi update description",
			"resource_id":   "multi-resource-123",
		}

		// Execute update
		err = store.UpdateKmsConfig(context.Background(), kmsConfig.UUID, updates)
		assert.NoError(tt, err, "Expected no error during multi-field update")

		// Verify all updates
		updatedKmsConfig, err := store.GetKmsConfigByUUID(context.Background(), kmsConfig.UUID)
		assert.NoError(tt, err, "Failed to retrieve updated KMS config")
		assert.Equal(tt, "MultiUpdated", updatedKmsConfig.State, "Expected state to be updated")
		assert.Equal(tt, "Multi update state details", updatedKmsConfig.StateDetails, "Expected state details to be updated")
		assert.Equal(tt, "Multi update description", updatedKmsConfig.Description, "Expected description to be updated")
		assert.Equal(tt, "multi-resource-123", updatedKmsConfig.ResourceID, "Expected resource ID to be updated")
		assert.True(tt, updatedKmsConfig.UpdatedAt.After(originalTimestamp), "Expected updated_at to be more recent")
	})
}

func TestUpdateKmsConfigStateForHandleResource_StateDetails(t *testing.T) {
	t.Run("VerifiesStateDetailsForInUseState", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: "Disabled state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()
		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return true, nil
		}

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource enabled", datamodel.StateOn)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateInUse, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateInUseDetails, result.StateDetails, "Should set 'In use' state details when KMS config is in use")
	})

	t.Run("VerifiesStateDetailsForCreatedState", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateDisabled,
			StateDetails: "Disabled state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		originalIsKmsConfigInUse := isKmsConfigInUse
		defer func() { isKmsConfigInUse = originalIsKmsConfigInUse }()
		isKmsConfigInUse = func(db *gorm.DB, kmsConfig *datamodel.KmsConfig) (bool, error) {
			return false, nil
		}

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource enabled", datamodel.StateOn)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateCreated, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateCreatedDetails, result.StateDetails, "Should set 'Created successfully' state details when KMS config is not in use")
	})

	t.Run("VerifiesStateDetailsForDisabledState", func(tt *testing.T) {
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

		kmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: "test-kms-uuid"},
			Name:         "test-kms",
			AccountID:    account.ID,
			State:        datamodel.LifeCycleStateAvailable,
			StateDetails: "Initial state",
		}
		err = store.db.Create(kmsConfig).Error()
		assert.NoError(tt, err, "Failed to create kms config")

		result, err := store.UpdateKmsConfigStateForHandleResource(context.Background(), "test-kms-uuid", "Resource disabled", datamodel.StateOff)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, datamodel.LifeCycleStateDisabled, result.State)
		assert.Equal(tt, datamodel.LifeCycleStateDisabledDetails, result.StateDetails)
	})
}

func TestIsStateReady(t *testing.T) {
	t.Run("WhenStateIsReady_ShouldReturnNil", func(tt *testing.T) {
		// Test states that are considered "ready" - should return nil (no error)
		readyStates := []string{
			datamodel.LifeCycleStateAvailable,
			datamodel.LifeCycleStateCreated,
			datamodel.LifeCycleStateDisabled,
			datamodel.LifeCycleStateInUse,
			"", // empty state should be considered ready
		}

		for _, state := range readyStates {
			tt.Run(fmt.Sprintf("State_%s", state), func(t *testing.T) {
				err := isStateReady(state)
				assert.NoError(t, err, "Expected no error for state: %s", state)
			})
		}
	})

	t.Run("WhenStateIsDeleting_ShouldReturnNotFoundError", func(tt *testing.T) {
		err := isStateReady(datamodel.LifeCycleStateDeleting)

		assert.NotNil(tt, err, "Expected error for deleting state")
		assert.True(tt, customerrors.IsNotFoundErr(err), "Expected NotFoundErr for deleting state")
		assert.Contains(tt, err.Error(), "Config does not exist", "Error message should indicate config doesn't exist")
	})

	t.Run("WhenStateIsDeleted_ShouldReturnNotFoundError", func(tt *testing.T) {
		err := isStateReady(datamodel.LifeCycleStateDeleted)

		assert.NotNil(tt, err, "Expected error for deleted state")
		assert.True(tt, customerrors.IsNotFoundErr(err), "Expected NotFoundErr for deleted state")
		assert.Contains(tt, err.Error(), "Config does not exist", "Error message should indicate config doesn't exist")
	})

	t.Run("WhenStateIsError_ShouldReturnUserInputValidationError", func(tt *testing.T) {
		err := isStateReady(datamodel.LifeCycleStateError)

		assert.NotNil(tt, err, "Expected error for error state")
		assert.True(tt, customerrors.IsUserInputValidationErr(err), "Expected UserInputValidationErr for error state")
		assert.Contains(tt, err.Error(), "Can not update a KmsConfig which is in creating or error state",
			"Error message should indicate config is in error state")
	})

	t.Run("WhenStateIsCreating_ShouldReturnUserInputValidationError", func(tt *testing.T) {
		err := isStateReady(datamodel.LifeCycleStateCreating)

		assert.NotNil(tt, err, "Expected error for creating state")
		assert.True(tt, customerrors.IsUserInputValidationErr(err), "Expected UserInputValidationErr for creating state")
		assert.Contains(tt, err.Error(), "Can not update a KmsConfig which is in creating or error state",
			"Error message should indicate config is in creating state")
	})

	t.Run("WhenStateIsUpdating_ShouldReturnUserInputValidationError", func(tt *testing.T) {
		err := isStateReady(datamodel.LifeCycleStateUpdating)

		assert.NotNil(tt, err, "Expected error for updating state")
		assert.True(tt, customerrors.IsUserInputValidationErr(err), "Expected UserInputValidationErr for updating state")
		assert.Contains(tt, err.Error(), "GCP KMS configuration is already transitioning between states",
			"Error message should indicate config is transitioning")
	})

	t.Run("WhenStateIsMigrating_ShouldReturnUserInputValidationError", func(tt *testing.T) {
		err := isStateReady(datamodel.LifeCycleStateMigrating)

		assert.NotNil(tt, err, "Expected error for migrating state")
		assert.True(tt, customerrors.IsUserInputValidationErr(err), "Expected UserInputValidationErr for migrating state")
		assert.Contains(tt, err.Error(), "GCP KMS configuration is already transitioning between states",
			"Error message should indicate config is transitioning")
	})

	t.Run("WhenStateIsUnknown_ShouldReturnNil", func(tt *testing.T) {
		// Test with unknown/custom states - should return nil as they're not explicitly handled
		unknownStates := []string{
			"unknown_state",
			"custom_state",
			"invalid_state",
		}

		for _, state := range unknownStates {
			tt.Run(fmt.Sprintf("State_%s", state), func(t *testing.T) {
				err := isStateReady(state)
				assert.NoError(t, err, "Expected no error for unknown state: %s", state)
			})
		}
	})

	t.Run("ErrorTypes_ShouldBeCorrect", func(tt *testing.T) {
		// Test that the correct error types are returned for different categories of states
		testCases := []struct {
			state       string
			expectError bool
			errorCheck  func(error) bool
			description string
		}{
			{
				state:       datamodel.LifeCycleStateDeleting,
				expectError: true,
				errorCheck:  customerrors.IsNotFoundErr,
				description: "Deleting state should return NotFoundErr",
			},
			{
				state:       datamodel.LifeCycleStateDeleted,
				expectError: true,
				errorCheck:  customerrors.IsNotFoundErr,
				description: "Deleted state should return NotFoundErr",
			},
			{
				state:       datamodel.LifeCycleStateError,
				expectError: true,
				errorCheck:  customerrors.IsUserInputValidationErr,
				description: "Error state should return UserInputValidationErr",
			},
			{
				state:       datamodel.LifeCycleStateCreating,
				expectError: true,
				errorCheck:  customerrors.IsUserInputValidationErr,
				description: "Creating state should return UserInputValidationErr",
			},
			{
				state:       datamodel.LifeCycleStateUpdating,
				expectError: true,
				errorCheck:  customerrors.IsUserInputValidationErr,
				description: "Updating state should return UserInputValidationErr",
			},
			{
				state:       datamodel.LifeCycleStateMigrating,
				expectError: true,
				errorCheck:  customerrors.IsUserInputValidationErr,
				description: "Migrating state should return UserInputValidationErr",
			},
			{
				state:       datamodel.LifeCycleStateAvailable,
				expectError: false,
				errorCheck:  nil,
				description: "Available state should return no error",
			},
		}

		for _, tc := range testCases {
			tt.Run(tc.description, func(t *testing.T) {
				err := isStateReady(tc.state)

				if tc.expectError {
					assert.NotNil(t, err, "Expected error for state: %s", tc.state)
					if tc.errorCheck != nil {
						assert.True(t, tc.errorCheck(err), "Error type check failed for state: %s", tc.state)
					}
				} else {
					assert.NoError(t, err, "Expected no error for state: %s", tc.state)
				}
			})
		}
	})
}

func TestCreateKmsServiceAccountAdvisoryLock(t *testing.T) {
	// On Postgres the create path must take a transaction-scoped advisory lock before the
	// read-then-insert window so concurrent first-time creates for the same account cannot
	// both insert duplicate service account rows.
	t.Run("ReturnsExistingEnabledServiceAccount", func(tt *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(tt, err)
		defer db.Close()

		gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(gdb))

		mock.ExpectBegin()
		mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT .* FROM "service_accounts"`).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "uuid", "name", "state", "account_id",
			}).AddRow(111, "existing-sa", "ServiceAccount1", KmsSaStateEnable, 4242))
		mock.ExpectCommit()

		result, err := store.CreateKmsServiceAccount(context.Background(),
			&datamodel.ServiceAccount{AccountID: 4242})

		require.NoError(tt, err)
		require.NotNil(tt, result)
		assert.Equal(tt, "existing-sa", result.UUID)
		assert.Equal(tt, "ServiceAccount1", result.Name)
		assert.Equal(tt, int64(4242), result.AccountID)
		assert.NoError(tt, mock.ExpectationsWereMet())
	})

	t.Run("CreatesServiceAccountWhenNoneExists", func(tt *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(tt, err)
		defer db.Close()

		gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(gdb))

		mock.ExpectBegin()
		mock.ExpectExec("pg_advisory_xact_lock").WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectQuery(`SELECT .* FROM "service_accounts"`).
			WillReturnRows(sqlmock.NewRows([]string{"id", "uuid"}))
		mock.ExpectQuery(`INSERT INTO "service_accounts"`).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(222))
		mock.ExpectQuery(`SELECT .* FROM "service_accounts"`).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "uuid", "name", "state", "account_id",
			}).AddRow(222, "new-sa", "ServiceAccount2", KmsSaStateEnable, 4343))
		mock.ExpectCommit()

		result, err := store.CreateKmsServiceAccount(context.Background(),
			&datamodel.ServiceAccount{
				BaseModel: datamodel.BaseModel{UUID: "new-sa"},
				Name:      "ServiceAccount2",
				State:     KmsSaStateEnable,
				AccountID: 4343,
			})

		require.NoError(tt, err)
		require.NotNil(tt, result)
		assert.Equal(tt, "new-sa", result.UUID)
		assert.Equal(tt, "ServiceAccount2", result.Name)
		assert.NoError(tt, mock.ExpectationsWereMet())
	})

	t.Run("ReturnsErrorAndRollsBackWhenAdvisoryLockFails", func(tt *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(tt, err)
		defer db.Close()

		gdb, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
		require.NoError(tt, err)
		store := NewDataStoreRepository(gormwrapper.New(gdb))

		mock.ExpectBegin()
		mock.ExpectExec("pg_advisory_xact_lock").WillReturnError(errors.New("lock failed"))
		mock.ExpectRollback()

		result, err := store.CreateKmsServiceAccount(context.Background(),
			&datamodel.ServiceAccount{AccountID: 4242})

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.NoError(tt, mock.ExpectationsWereMet())
	})
}
