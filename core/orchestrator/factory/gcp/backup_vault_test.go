package gcp

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/auth"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	utilErrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	workflow_engine_mock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	"gorm.io/gorm"
)

func TestCheck(t *testing.T) {
	t.Run("WhenBackupVaultHasAllFieldsPopulated", func(tt *testing.T) {
		desc := "A test backup vault"
		SourceRegionName := "us-east1"
		BackupRegionName := "us-central1"
		crbName := "cross-region-vault"
		minEnforcedRetentionDuration := int64(30)
		bv := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				ID:        1,
				UUID:      "backup-vault-uuid",
				CreatedAt: time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				UpdatedAt: time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC),
			},
			Account:               &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:                  "backup-vault-name",
			Description:           &desc,
			LifeCycleState:        "ACTIVE",
			LifeCycleStateDetails: "Available for use",
			BackupRegionName:      &BackupRegionName,
			SourceRegionName:      &SourceRegionName,
			RegionName:            "us-central1",
			AccountVendorID:       "vendor-id",
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &minEnforcedRetentionDuration,
				IsDailyBackupImmutable:                 true,
				IsMonthlyBackupImmutable:               false,
				IsWeeklyBackupImmutable:                true,
			},
			CrossRegionBackupVaultName: &crbName,
			BackupVaultType:            "STANDARD",
		}

		result := _convertDatastoreBackupVaultToModel(bv)

		if result.ID != bv.ID {
			tt.Errorf("Expected ID %v, got %v", bv.ID, result.ID)
		}
		if result.OwnerID != bv.Account.UUID {
			tt.Errorf("Expected OwnerID %v, got %v", bv.Account.UUID, result.OwnerID)
		}
		if result.BackupVaultID != bv.UUID {
			tt.Errorf("Expected BackupVaultID %v, got %v", bv.UUID, result.BackupVaultID)
		}
		if result.Name != bv.Name {
			tt.Errorf("Expected Name %v, got %v", bv.Name, result.Name)
		}
		if result.Description != bv.Description {
			tt.Errorf("Expected Description %v, got %v", bv.Description, result.Description)
		}
		if result.LifeCycleState != bv.LifeCycleState {
			tt.Errorf("Expected LifeCycleState %v, got %v", bv.LifeCycleState, result.LifeCycleState)
		}
		if result.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration {
			tt.Errorf("Expected BackupMinimumEnforcedRetentionDuration %v, got %v", bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration, result.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration)
		}
		if result.BackupVaultType == nil || *result.BackupVaultType != bv.BackupVaultType {
			tt.Errorf("Expected BackupVaultType %v, got %v", bv.BackupVaultType, result.BackupVaultType)
		}
	})

	t.Run("WhenBackupVaultHasCmekAttributes", func(tt *testing.T) {
		kmsConfigPath := "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key"
		encryptionState := "ENCRYPTION_STATE_COMPLETED"
		backupsPrimaryKeyVersion := "1"
		bv := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				ID:   3,
				UUID: "backup-vault-uuid",
			},
			Account: &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:    "backup-vault-name",
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath:    &kmsConfigPath,
				EncryptionState:          &encryptionState,
				BackupsPrimaryKeyVersion: &backupsPrimaryKeyVersion,
			},
		}

		result := _convertDatastoreBackupVaultToModel(bv)

		if result.KmsConfigResourcePath == nil || *result.KmsConfigResourcePath != kmsConfigPath {
			tt.Errorf("Expected KmsConfigResourcePath %v, got %v", kmsConfigPath, result.KmsConfigResourcePath)
		}
		if result.EncryptionState == nil || *result.EncryptionState != encryptionState {
			tt.Errorf("Expected EncryptionState %v, got %v", encryptionState, result.EncryptionState)
		}
		if result.BackupsPrimaryKeyVersion == nil || *result.BackupsPrimaryKeyVersion != backupsPrimaryKeyVersion {
			tt.Errorf("Expected BackupsPrimaryKeyVersion %v, got %v", backupsPrimaryKeyVersion, result.BackupsPrimaryKeyVersion)
		}
	})

	t.Run("WhenBackupVaultHasPartialCmekAttributes", func(tt *testing.T) {
		kmsConfigPath := "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key"
		bv := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				ID:   4,
				UUID: "backup-vault-uuid",
			},
			Account: &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:    "backup-vault-name",
			CmekAttributes: &datamodel.CmekAttributes{
				KmsConfigResourcePath: &kmsConfigPath,
				// EncryptionState and BackupsPrimaryKeyVersion are nil
			},
		}

		result := _convertDatastoreBackupVaultToModel(bv)

		if result.KmsConfigResourcePath == nil || *result.KmsConfigResourcePath != kmsConfigPath {
			tt.Errorf("Expected KmsConfigResourcePath %v, got %v", kmsConfigPath, result.KmsConfigResourcePath)
		}
		if result.EncryptionState != nil {
			tt.Errorf("Expected EncryptionState to be nil, got %v", result.EncryptionState)
		}
		if result.BackupsPrimaryKeyVersion != nil {
			tt.Errorf("Expected BackupsPrimaryKeyVersion to be nil, got %v", result.BackupsPrimaryKeyVersion)
		}
	})

	t.Run("WhenBackupVaultHasNoCmekAttributes", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				ID:   5,
				UUID: "backup-vault-uuid",
			},
			Account: &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:    "backup-vault-name",
			// CmekAttributes is nil
		}

		result := _convertDatastoreBackupVaultToModel(bv)

		if result.KmsConfigResourcePath != nil {
			tt.Errorf("Expected KmsConfigResourcePath to be nil, got %v", result.KmsConfigResourcePath)
		}
		if result.EncryptionState != nil {
			tt.Errorf("Expected EncryptionState to be nil, got %v", result.EncryptionState)
		}
		if result.BackupsPrimaryKeyVersion != nil {
			tt.Errorf("Expected BackupsPrimaryKeyVersion to be nil, got %v", result.BackupsPrimaryKeyVersion)
		}
	})

	t.Run("WhenBackupVaultHasMissingOptionalFields", func(tt *testing.T) {
		minEnforcedRetentionDuration := int64(30)
		bv := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				ID:   2,
				UUID: "backup-vault-uuid",
			},
			Account: &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},

			Name:       "backup-vault-name",
			RegionName: "us-central1",
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &minEnforcedRetentionDuration,
				IsDailyBackupImmutable:                 false,
				IsMonthlyBackupImmutable:               false,
				IsWeeklyBackupImmutable:                false,
			},
		}

		result := _convertDatastoreBackupVaultToModel(bv)

		if result.Description != nil {
			tt.Errorf("Expected empty Description, got %v", result.Description)
		}
		if result.LifeCycleState != "" {
			tt.Errorf("Expected empty LifeCycleState, got %v", result.LifeCycleState)
		}
		if result.BackupRetentionPolicy.IsDailyBackupImmutable != bv.ImmutableAttributes.IsDailyBackupImmutable {
			tt.Errorf("Expected IsDailyBackupImmutable %v, got %v", bv.ImmutableAttributes.IsDailyBackupImmutable, result.BackupRetentionPolicy.IsDailyBackupImmutable)
		}
	})
}

func Test_convertDatastoreBackupVaultToModel_CrossRegionSourceVaultWhenCRLocationMatchesBackupRegion(t *testing.T) {
	backupRegion := "us-central1"
	sourceRegion := "us-east1"
	accountName := "my-gcp-project"
	vaultName := "source-vault"
	crossRegionVaultName := fmt.Sprintf("projects/%s/locations/%s/backupVaults/destination-vault", accountName, backupRegion)

	bv := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{
			ID:   42,
			UUID: "bv-uuid",
		},
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "owner-uuid"},
			Name:      accountName,
		},
		Name:                       vaultName,
		BackupVaultType:            CrossRegionBackupType,
		BackupRegionName:           &backupRegion,
		SourceRegionName:           &sourceRegion,
		CrossRegionBackupVaultName: &crossRegionVaultName,
	}

	result := _convertDatastoreBackupVaultToModel(bv)

	assert.Equal(t, fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", accountName, sourceRegion, vaultName), *result.SourceBackupVault)
	assert.Equal(t, crossRegionVaultName, *result.DestinationBackupVault)
}

// When the cross-region vault resource location matches SourceRegion (and not BackupRegion), the current vault is the
// destination: SourceBackupVault is the peer resource name; DestinationBackupVault is the local backup-region path.
func Test_convertDatastoreBackupVaultToModel_CrossRegionDestinationVaultWhenCRLocationMatchesSourceRegion(t *testing.T) {
	backupRegion := "us-central1"
	sourceRegion := "us-east1"
	accountName := "my-gcp-project"
	vaultName := "destination-vault"
	// Peer vault lives in source region; path location[3] must equal SourceRegion and differ from BackupRegion.
	crossRegionVaultName := fmt.Sprintf("projects/%s/locations/%s/backupVaults/source-vault-peer", accountName, sourceRegion)

	bv := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{
			ID:   43,
			UUID: "bv-uuid-dest",
		},
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "owner-uuid"},
			Name:      accountName,
		},
		Name:                       vaultName,
		BackupVaultType:            CrossRegionBackupType,
		BackupRegionName:           &backupRegion,
		SourceRegionName:           &sourceRegion,
		CrossRegionBackupVaultName: &crossRegionVaultName,
	}

	result := _convertDatastoreBackupVaultToModel(bv)

	assert.Equal(t, crossRegionVaultName, *result.SourceBackupVault)
	assert.Equal(t, fmt.Sprintf("projects/%s/locations/%s/backupVaults/%s", accountName, backupRegion, vaultName), *result.DestinationBackupVault)
}

func Test_convertDatastoreBackupVaultToModel_TenantProject(t *testing.T) {
	t.Run("CrossProjectVault_PopulatesTenantProjectFromBucketDetails", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "bv-uuid"},
			Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:        "bv",
			ServiceType: datamodel.ServiceTypeCrossProject,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{TenantProjectNumber: "596181058421"},
			},
		}
		result := _convertDatastoreBackupVaultToModel(bv)
		assert.NotNil(tt, result.TenantProject)
		assert.Equal(tt, "596181058421", *result.TenantProject)
	})

	t.Run("NonCrossProjectVault_TenantProjectIsNil", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "bv-uuid"},
			Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:        "bv",
			ServiceType: datamodel.ServiceTypeGCNV,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{TenantProjectNumber: "596181058421"},
			},
		}
		result := _convertDatastoreBackupVaultToModel(bv)
		assert.Nil(tt, result.TenantProject)
	})

	t.Run("CrossProjectVault_NoBucketDetails_TenantProjectIsNil", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "bv-uuid"},
			Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:        "bv",
			ServiceType: datamodel.ServiceTypeCrossProject,
		}
		result := _convertDatastoreBackupVaultToModel(bv)
		assert.Nil(tt, result.TenantProject)
	})

	t.Run("CrossProjectVault_EmptyTenantProjectNumber_TenantProjectIsNil", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{ID: 1, UUID: "bv-uuid"},
			Account:     &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:        "bv",
			ServiceType: datamodel.ServiceTypeCrossProject,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{TenantProjectNumber: ""},
			},
		}
		result := _convertDatastoreBackupVaultToModel(bv)
		assert.Nil(tt, result.TenantProject)
	})
}

func TestGetBackupVaultByNameAndOwnerIDReturnsBackupVault(tt *testing.T) {
	tt.Run("WhenBackupVaultExists", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		minEnforcedRetentionDuration := int64(30)
		bv := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:      "backup-vault-name",
			Account:   account,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &minEnforcedRetentionDuration,
			},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		getBackupVaultByNameAndOwnerID = func(ctx context.Context, se database.Storage, bvName, ownerID string) (*datamodel.BackupVault, error) {
			return bv, nil
		}

		o := &GCPOrchestrator{storage: se}
		result, err := o.GetBackupVaultByNameAndOwnerID(context.Background(), bv.Name, account.UUID)
		assert.NoError(tt, err, "Expected no error")
		assert.Equal(tt, bv.UUID, result.BackupVaultID, "Expected BackupVaultID to match")
		assert.Equal(tt, bv.Name, result.Name, "Expected Name to match")
	})

	tt.Run("WhenBackupVaultHasNilImmutableAttributes", func(tt *testing.T) {
		bv := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{
				ID:   3,
				UUID: "backup-vault-uuid-3",
			},
			Account: &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "owner-uuid"}},
			Name:    "backup-vault-name",
			// ImmutableAttributes is nil
		}

		result := _convertDatastoreBackupVaultToModel(bv)

		// Test that BackupRetentionPolicy is not set when ImmutableAttributes is nil
		if result.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration != nil {
			tt.Errorf("Expected BackupMinimumEnforcedRetentionDuration to be nil when ImmutableAttributes is nil, got %v", result.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration)
		}
		if result.BackupRetentionPolicy.IsDailyBackupImmutable != false {
			tt.Errorf("Expected IsDailyBackupImmutable to be false when ImmutableAttributes is nil, got %v", result.BackupRetentionPolicy.IsDailyBackupImmutable)
		}
	})
	tt.Run("WhenBackupVaultDoesNotExist", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		getBackupVaultByNameAndOwnerID = func(ctx context.Context, se database.Storage, bvName, ownerID string) (*datamodel.BackupVault, error) {
			return nil, errors.New("backup vault not found")
		}

		o := &GCPOrchestrator{storage: se}
		result, err := o.GetBackupVaultByNameAndOwnerID(context.Background(), "non-existent-backup-vault", account.UUID)
		assert.Error(tt, err, "Expected error")
		assert.Nil(tt, result, "Expected result to be nil")
		assert.Equal(tt, "backup vault not found", err.Error(), "Expected error message to match")
	})
	tt.Run("WhenAccountDoesNotExist", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		getBackupVaultByNameAndOwnerID = func(ctx context.Context, se database.Storage, bvName, ownerID string) (*datamodel.BackupVault, error) {
			return nil, errors.New("account not found")
		}

		o := &GCPOrchestrator{storage: se}
		result, err := o.GetBackupVaultByNameAndOwnerID(context.Background(), "backup-vault-name", "non-existent-owner-uuid")
		assert.Error(tt, err, "Expected error")
		assert.Nil(tt, result, "Expected result to be nil")
		assert.Equal(tt, "account not found", err.Error(), "Expected error message to match")
	})
	tt.Run("WhenBackupVaultNameIsEmpty", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		getBackupVaultByNameAndOwnerID = func(ctx context.Context, se database.Storage, bvName, ownerID string) (*datamodel.BackupVault, error) {
			return nil, errors.New("backup vault not found")
		}

		o := &GCPOrchestrator{storage: se}
		result, err := o.GetBackupVaultByNameAndOwnerID(context.Background(), "non-existent-backup-vault", account.UUID)
		assert.Error(tt, err, "Expected error")
		assert.Nil(tt, result, "Expected result to be nil")
		assert.Equal(tt, "backup vault not found", err.Error(), "Expected error message to match")
	})
}

func TestReturnsErrorWhenAccountNotFound(tt *testing.T) {
	mockLogger := log.NewLogger()
	se, err := database.NewTestStorage(mockLogger)
	assert.NoError(tt, err, "Failed to create test storage")
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return nil, errors.New("account not found")
	}

	result, err := _getBackupVaultByNameAndOwnerID(context.Background(), se, "backup-vault-name", "non-existent-owner-uuid")
	assert.Error(tt, err, "Expected error")
	assert.Nil(tt, result, "Expected result to be nil")
	assert.Equal(tt, "account not found", err.Error(), "Expected error message to match")
}

func TestListBackupVaults(t *testing.T) {
	t.Run("WhenNoBackupVaultsExist", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		o := &GCPOrchestrator{storage: se}
		result, err := o.ListBackupVaults(context.Background(), account.UUID)
		assert.NoError(tt, err)
		assert.Nil(tt, result)
		assert.Empty(tt, result)
	})
	t.Run("WhenAccountNotFoundAndCreated", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(tt, err, "Failed to create test storage")

		newAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "new-account-uuid"}}
		accountCreated := false

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			// Simulate account creation when account doesn't exist
			accountCreated = true
			return newAccount, nil
		}

		o := &GCPOrchestrator{storage: se}
		result, err := o.ListBackupVaults(context.Background(), "non-existent-account")

		assert.NoError(tt, err, "Expected no error when account is created")
		assert.True(tt, accountCreated, "Expected account to be created")
		assert.Nil(tt, result, "Expected result to be nil")
		assert.Empty(tt, result, "Expected empty backup vaults list for new account")
	})
}

func TestListBackupVaultsByOwnerID(t *testing.T) {
	t.Run("WhenListBackupVaultsErrors", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)

		mockStorage.On("ListBackupVaults", ctx, int64(1)).Return(nil, errors.New("failed to list backup vaults"))
		bvs, err := ListBackupVaultsByOwnerID(ctx, mockStorage, 1)
		assert.Error(t, err, "Expected error when listing backup vaults")
		assert.Nil(t, bvs, "Expected backup vaults to be nil")
	})
	t.Run("WhenListBackupVaultsSuccess", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)

		desc := "desc"
		bRegionName := "us-central1"
		sRegionName := "us-west1"
		minEnforcedDuration := int64(10)
		bVaults := []*datamodel.BackupVault{
			{
				BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
				Name:      "vault-1",
				Account: &datamodel.Account{
					BaseModel: datamodel.BaseModel{ID: 1},
				},
				AccountID:             1,
				RegionName:            "us-east1",
				BackupRegionName:      &bRegionName,
				SourceRegionName:      &sRegionName,
				LifeCycleState:        "Available",
				LifeCycleStateDetails: "Available for use",
				BackupVaultType:       "IN_REGION",
				AccountVendorID:       "vendor1",
				Description:           &desc,
				ImmutableAttributes: &datamodel.ImmutableAttributes{
					BackupMinimumEnforcedRetentionDuration: &minEnforcedDuration,
					IsDailyBackupImmutable:                 false,
					IsWeeklyBackupImmutable:                false,
					IsMonthlyBackupImmutable:               false,
					IsAdhocBackupImmutable:                 false,
				},
			},
		}
		mockStorage.On("ListBackupVaults", ctx, int64(1)).Return(bVaults, nil)
		bvs, err := ListBackupVaultsByOwnerID(ctx, mockStorage, 1)
		assert.NoError(t, err)
		assert.NotNil(t, bvs)
	})
}

func TestGetBackupVaultByUUID(tt *testing.T) {
	mockLogger := log.NewLogger()
	_, err := database.NewTestStorage(mockLogger)
	assert.NoError(tt, err, "Failed to create test storage")
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}

	mockStorage := new(database.MockStorage)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", context.Background(), "backup-vault-uuid", int64(account.ID)).Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

	res, err := GetBackupVaultByUUIDAndOwnerID(context.Background(), mockStorage, "backup-vault-uuid", account.ID)

	assert.Error(tt, err, "Expected error when backup vault not found")
	assert.Nil(tt, res, "Expected result to be nil")
}

func TestGetBackupVaultByUUIDAndOwnerIDSuccess(tt *testing.T) {
	mockLogger := log.NewLogger()
	_, err := database.NewTestStorage(mockLogger)
	assert.NoError(tt, err, "Failed to create test storage")
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}

	desc := "desc"
	bRegionName := "us-central1"
	sRegionName := "us-west1"
	minEnforcedDuration := int64(10)
	bv := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "uuid1", CreatedAt: time.Now(), UpdatedAt: time.Now()},
		Name:      "vault-1",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
		},
		AccountID:             1,
		RegionName:            "us-east1",
		BackupRegionName:      &bRegionName,
		SourceRegionName:      &sRegionName,
		LifeCycleState:        "Available",
		LifeCycleStateDetails: "Available for use",
		BackupVaultType:       "IN_REGION",
		AccountVendorID:       "vendor1",
		Description:           &desc,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			BackupMinimumEnforcedRetentionDuration: &minEnforcedDuration,
			IsDailyBackupImmutable:                 false,
			IsWeeklyBackupImmutable:                false,
			IsMonthlyBackupImmutable:               false,
			IsAdhocBackupImmutable:                 false,
		},
	}
	mockStorage := new(database.MockStorage)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", context.Background(), "backup-vault-uuid", int64(account.ID)).Return(bv, nil)

	res, err := GetBackupVaultByUUIDAndOwnerID(context.Background(), mockStorage, "backup-vault-uuid", account.ID)

	assert.NoError(tt, err, "Expected error when backup vault not found")
	assert.NotNil(tt, res, "Expected result to be nil")
}

func TestGetBackupVaultByUUIDError(tt *testing.T) {
	mockLogger := log.NewLogger()
	_, err := database.NewTestStorage(mockLogger)
	assert.NoError(tt, err, "Failed to create test storage")
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}

	mockStorage := new(database.MockStorage)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", context.Background(), "backup-vault-uuid", int64(account.ID)).Return(nil, gorm.ErrCheckConstraintViolated)

	res, err := GetBackupVaultByUUIDAndOwnerID(context.Background(), mockStorage, "backup-vault-uuid", account.ID)

	assert.Error(tt, err, "Expected error when backup vault not found")
	assert.Nil(tt, res, "Expected result to be nil")
}

func TestGetBackupVaultByUUIDGetOrCreateError(tt *testing.T) {
	mockLogger := log.NewLogger()
	se, err := database.NewTestStorage(mockLogger)
	assert.NoError(tt, err, "Failed to create test storage")
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return nil, errors.New("account not found")
	}

	o := &GCPOrchestrator{storage: se}
	res, err := o.GetBackupVaultByUUID(context.Background(), "backup-vault-uuid", account.UUID)

	assert.Error(tt, err, "Expected error when backup vault not found")
	assert.Nil(tt, res, "Expected result to be nil")
}

func TestUpdateBackupVault(t *testing.T) {
	t.Run("WhenAccountNotFound", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		mrd := int64(30)
		daily := true
		monthly := true
		weekly := false
		manual := false
		params := &commonparams.BackupVaultParams{
			OwnerID: "owner-uuid",
			Name:    "backup-vault-name",
			Region:  "us-central1",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &mrd,
				IsDailyBackupImmutable:                 &daily,
				IsWeeklyBackupImmutable:                &weekly,
				IsMonthlyBackupImmutable:               &monthly,
				IsAdhocBackupImmutable:                 &manual,
			},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, errors.New("account not found")
		}

		bv, _, err := updateBackupVault(ctx, se, temporal, params)
		assert.Error(t, err, "Expected error when account not found")
		assert.Nil(t, bv, "Expected backup vault to be nil")
	})
	t.Run("WhenCreateJobFails", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		mrd := int64(30)
		daily := true
		monthly := true
		weekly := false
		manual := false
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			Name:          "backup-vault-name",
			Region:        "us-central1",
			BackupVaultID: "backup-vault-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &mrd,
				IsDailyBackupImmutable:                 &daily,
				IsWeeklyBackupImmutable:                &weekly,
				IsMonthlyBackupImmutable:               &monthly,
				IsAdhocBackupImmutable:                 &manual,
			},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		mockStorage := new(database.MockStorage)

		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))

		bv, _, err := updateBackupVault(ctx, se, temporal, params)
		assert.Error(t, err, "Expected error when validation fails")
		assert.Nil(t, bv, "Expected backup vault to be nil")
	})
	t.Run("WhenGetBackupVaultByUUIDndOwnerIDFails", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		mrd := int64(30)
		daily := true
		monthly := true
		weekly := false
		manual := false
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			Name:          "backup-vault-name",
			Region:        "us-central1",
			BackupVaultID: "backup-vault-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &mrd,
				IsDailyBackupImmutable:                 &daily,
				IsWeeklyBackupImmutable:                &weekly,
				IsMonthlyBackupImmutable:               &monthly,
				IsAdhocBackupImmutable:                 &manual,
			},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		mockStorage := new(database.MockStorage)

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		bv, _, err := updateBackupVault(ctx, se, temporal, params)
		assert.Error(t, err, "Expected error when validation fails")
		assert.Nil(t, bv, "Expected backup vault to be nil")
	})
	t.Run("WhenUpdatingBackupVaultStateFails", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		se, err := database.NewTestStorage(mockLogger)
		assert.NoError(t, err, "Failed to create test storage")
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		mrd := int64(30)
		daily := true
		monthly := true
		weekly := false
		manual := false
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			Name:          "backup-vault-name",
			Region:        "us-central1",
			BackupVaultID: "backup-vault-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &mrd,
				IsDailyBackupImmutable:                 &daily,
				IsWeeklyBackupImmutable:                &weekly,
				IsMonthlyBackupImmutable:               &monthly,
				IsAdhocBackupImmutable:                 &manual,
			},
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		mockStorage := new(database.MockStorage)

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		bvResp := &datamodel.BackupVault{}
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(bvResp, nil)
		mockStorage.On("UpdatingBackupVaultState", ctx, mock.Anything).Return(nil, errors.New("failed to update backup vault state"))

		bv, _, err := updateBackupVault(ctx, se, temporal, params)
		assert.Error(t, err, "Expected error when validation fails")
		assert.Nil(t, bv, "Expected backup vault to be nil")
	})
	t.Run("WhenCrossRegionBackupVaultIsUpdatedFromDestinationRegion", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			Name:          "backup-vault-name",
			Region:        "us-central1",
			BackupVaultID: "backup-vault-uuid",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).
			Return(&datamodel.BackupVault{
				BaseModel:        datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
				Name:             "backup-vault-name",
				BackupVaultType:  activities.CrossRegionBackupType,
				BackupRegionName: nillable.ToPointer("us-central1"),
			}, nil)

		bv, _, err := updateBackupVault(ctx, mockStorage, temporal, params)
		assert.Error(t, err)
		assert.Nil(t, bv)
		assert.Equal(t, "cross-region backup vault cannot be updated from the destination region", err.Error())
	})
}

func TestGetMultipleBackupVaultsReturnsVaultsForValidUUIDs(tt *testing.T) {
	mockLogger := log.NewLogger()
	mockStorage := new(database.MockStorage)
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	backupVaultUUIDList := []string{"uuid1", "uuid2"}
	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "uuid1"},
			Name:      "vault1",
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{UUID: "owner-uuid"},
			},
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: nil,
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "uuid2"},
			Name:      "vault2",
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{UUID: "owner-uuid"},
			},
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: nil,
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		},
	}
	mockStorage.On("GetMultipleBackupVaults", ctx, [][]interface{}{{"uuid in ?", backupVaultUUIDList}}).Return(backupVaults, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetMultipleBackupVaults(ctx, backupVaultUUIDList)

	assert.NoError(tt, err)
	assert.Len(tt, result, 2)
	assert.Equal(tt, "uuid1", result[0].BackupVaultID)
	assert.Equal(tt, "uuid2", result[1].BackupVaultID)
}

func TestGetMultipleBackupVaultsReturnsEmptyListForNoMatchingUUIDs(tt *testing.T) {
	mockLogger := log.NewLogger()
	mockStorage := new(database.MockStorage)
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	backupVaultUUIDList := []string{"non-existent-uuid"}
	mockStorage.On("GetMultipleBackupVaults", ctx, [][]interface{}{{"uuid in ?", backupVaultUUIDList}}).Return([]*datamodel.BackupVault{}, nil)

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetMultipleBackupVaults(ctx, backupVaultUUIDList)

	assert.NoError(tt, err)
	assert.Empty(tt, result)
}

func TestGetMultipleBackupVaultsReturnsErrorWhenStorageFails(tt *testing.T) {
	mockLogger := log.NewLogger()
	mockStorage := new(database.MockStorage)
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	backupVaultUUIDList := []string{"uuid1", "uuid2"}
	mockStorage.On("GetMultipleBackupVaults", ctx, [][]interface{}{{"uuid in ?", backupVaultUUIDList}}).Return(nil, errors.New("database error"))

	o := &GCPOrchestrator{storage: mockStorage}
	result, err := o.GetMultipleBackupVaults(ctx, backupVaultUUIDList)

	assert.Error(tt, err)
	assert.Nil(tt, result)
	assert.Equal(tt, "database error", err.Error())
}

func TestReturnsErrorWhenBackupVaultHasBackups(t *testing.T) {
	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)
	ctx := context.Background()

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	backupVault := &datamodel.BackupVault{
		BaseModel:      datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
		LifeCycleState: datamodel.LifeCycleStateAvailable,
	}
	job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}

	mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("GetAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
	mockStorage.On("GetBackupCountByBackupVaultID", ctx, backupVault.ID).Return(int64(1), nil)

	o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
	result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "backup vault has backups, please delete backups before deleting backup vault", err.Error())
}

func TestReturnsGetBackupVaultByUUIDndOwnerIDErrorWhenBackupVaultHasBackups(t *testing.T) {
	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)
	ctx := context.Background()

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}

	mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("GetAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(nil, errors.New("backup vault not found"))

	o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
	result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "backup vault not found", err.Error())
}

func TestReturnsJobErrorWhenBackupVaultHasBackups(t *testing.T) {
	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)
	ctx := context.Background()

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	backupVault := &datamodel.BackupVault{
		BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
		LifeCycleState:        datamodel.LifeCycleStateAvailable,
		LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
	}
	mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("GetAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
	mockStorage.On("GetBackupCountByBackupVaultID", ctx, backupVault.ID).Return(int64(1), nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("failed to create job"))
	o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
	result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
}

func TestReturnsUpdatingErrorWhenBackupVaultHasBackups(t *testing.T) {
	mockStorage := new(database.MockStorage)
	mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)
	ctx := context.Background()

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	backupVault := &datamodel.BackupVault{
		BaseModel:      datamodel.BaseModel{UUID: "backup-vault-uuid"},
		LifeCycleState: datamodel.LifeCycleStateUpdating,
	}
	var backups []*datamodel.Backup
	job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-uuid"}}

	mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("GetAccount", ctx, "owner-uuid").Return(account, nil)
	mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
	mockStorage.On("GetBackupsByBackupVaultOwnerIDAndFilter", ctx, "backup-vault-uuid", int64(1), [][]interface{}(nil)).Return(backups, nil)

	o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
	result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
	})

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Empty(t, jobID)
	assert.Equal(t, "backup vault is in transition state", err.Error())
}

func TestDeleteBackupVaultWhenCrossRegionBackupVaultIsDeletedFromDestinationRegion(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
	temporal := workflow_engine_mock.NewMockTemporalTestClient(t)
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		Name:          "backup-vault-name",
		Region:        "us-central1",
		BackupVaultID: "backup-vault-uuid",
	}
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).
		Return(&datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:             "backup-vault-name",
			LifeCycleState:   datamodel.LifeCycleStateAvailable,
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: nillable.ToPointer("us-central1"),
		}, nil)

	o := &GCPOrchestrator{storage: mockStorage, temporal: temporal}
	bv, _, err := o.DeleteBackupVault(ctx, params)
	assert.Error(t, err)
	assert.Nil(t, bv)
	assert.Equal(t, "backup vault cannot be deleted from the destination region", err.Error())
}

func TestDeleteBackupVaultRollbackScenarios(t *testing.T) {
	t.Run("WhenWorkflowStartFails_ShouldRollbackBackupVaultState", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, "backup-vault-uuid").Return(int64(0), nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(backupVault, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(backupVault, nil)
		mockStorage.On("UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, mock.Anything).Return(nil)

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		})

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify rollback was called
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, "workflow start failed")
	})

	t.Run("WhenWorkflowStartFails_AndRollbackFails_ShouldLogError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, "backup-vault-uuid").Return(int64(0), nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(backupVault, nil)
		// Mock rollback to fail
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(nil, errors.New("rollback failed"))
		mockStorage.On("UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, mock.Anything).Return(nil)

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		})

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify rollback was attempted even though it failed
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, "workflow start failed")
	})

	t.Run("WhenWorkflowStartFails_AndJobUpdateFails_ShouldLogError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, "backup-vault-uuid").Return(int64(0), nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(backupVault, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(backupVault, nil)
		// Mock job update to fail
		mockStorage.On("UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, mock.Anything).Return(errors.New("job update failed"))

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		})

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify both rollback operations were attempted
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, "workflow start failed")
	})

	t.Run("WhenWorkflowStartSucceeds_ShouldNotRollback", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		enforcedDuration := int64(30)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
			Account:               account,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &enforcedDuration,
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, "backup-vault-uuid").Return(int64(0), nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(backupVault, nil)

		// Mock workflow execution to succeed
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		})

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "job-uuid", jobID)

		// Verify rollback was NOT called
		mockStorage.AssertNotCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertNotCalled(t, "UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, mock.Anything)
	})
	t.Run("WhenCreateJobFails_ShouldNotRollback", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		enforcedDuration := int64(30)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
			Account:               account,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &enforcedDuration,
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, "backup-vault-uuid").Return(int64(0), nil)
		// Mock CreateJob to fail
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("create job failed"))

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		})

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "create job failed", err.Error())

		// Verify rollback was NOT called since no job was created
		mockStorage.AssertNotCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertNotCalled(t, "UpdateJob", ctx, mock.Anything, datamodel.LifeCycleStateError, 0, mock.Anything)
	})

	t.Run("WhenBackupVaultHasDifferentOriginalState_ShouldRollbackToCorrectState", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		enforcedDuration := int64(30)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: "Updating backup vault",
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &enforcedDuration,
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
			Account: account,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", ctx, int64(1)).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, "backup-vault-uuid").Return(int64(0), nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).Return(backupVault, nil)
		// Mock rollback to original state
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, "Updating backup vault").Return(backupVault, nil)
		mockStorage.On("UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, mock.Anything).Return(nil)

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		o := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}
		result, jobID, err := o.DeleteBackupVault(ctx, &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		})

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify rollback was called with the original state
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, "Updating backup vault")
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", datamodel.LifeCycleStateError, 0, "workflow start failed")
	})
}

func TestRotateCmekBackupsForBackupVault_Success(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
		Name:      "test-project-number",
	}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)

	dbBV := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{
			UUID: "backup-vault-uuid",
		},
		Name:            "backup-vault-name",
		AccountID:       account.ID,
		BackupVaultType: "IN_REGION",
		LifeCycleState:  datamodel.LifeCycleStateREADY,
		CmekAttributes: &datamodel.CmekAttributes{
			KmsConfigResourcePath:    nillable.ToPointer("projects/p/locations/r/kmsConfigs/test"),
			BackupsPrimaryKeyVersion: nillable.ToPointer("projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"),
			EncryptionState:          nillable.ToPointer("ENCRYPTION_STATE_COMPLETED"),
		},
	}

	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(dbBV, nil)

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "workflow-id",
	}
	mockStorage.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.Type == string(datamodel.JobTypeRotateCmekBackups) &&
			j.State == string(datamodel.JobsStateNEW) &&
			j.ResourceName == dbBV.Name &&
			j.JobAttributes != nil &&
			j.JobAttributes.ResourceUUID == dbBV.UUID &&
			j.JobAttributes.Location == params.Region &&
			j.JobAttributes.KmsAttributes != nil &&
			j.JobAttributes.KmsAttributes.NewKmsKeyURL == primaryKeyVersion &&
			j.JobAttributes.KmsAttributes.AccountIdentifier == account.Name
	})).Return(job, nil)

	mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, primaryKeyVersion)

	assert.NoError(t, err)
	assert.Equal(t, "job-uuid", jobUUID)
	mockStorage.AssertExpectations(t)
}

func TestRotateCmekBackupsForBackupVault_GetOrCreateAccountFails(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	// Ensure getOrCreateAccount returns an error so that the early return path is exercised.
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return nil, errors.New("failed to get or create account")
	}

	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}

	// Storage/temporal are not used when getOrCreateAccount fails, so simple mocks are sufficient.
	orchestrator := &GCPOrchestrator{
		storage:  new(database.MockStorage),
		temporal: workflow_engine_mock.NewMockTemporalTestClient(t),
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1")

	assert.Error(t, err)
	assert.Equal(t, "", jobUUID)
	assert.Contains(t, err.Error(), "failed to get or create account")
}

func TestRotateCmekBackupsForBackupVault_GetBackupVaultByUUIDFails(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)
	// Force GetBackupVaultByUUIDndOwnerID to fail so that the corresponding early return path is covered.
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).
		Return((*datamodel.BackupVault)(nil), errors.New("failed to get backup vault"))

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: workflow_engine_mock.NewMockTemporalTestClient(t),
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1")

	assert.Error(t, err)
	assert.Equal(t, "", jobUUID)
	assert.Contains(t, err.Error(), "failed to get backup vault")
	mockStorage.AssertExpectations(t)
}

func TestRotateCmekBackupsForBackupVault_TransitionalState(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)
	dbBV := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:            "backup-vault-name",
		AccountID:       account.ID,
		BackupVaultType: "IN_REGION",
		LifeCycleState:  datamodel.LifeCycleStateUpdating,
	}
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(dbBV, nil)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: workflow_engine_mock.NewMockTemporalTestClient(t),
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1")

	assert.Error(t, err)
	assert.Equal(t, "", jobUUID)
	assert.Contains(t, err.Error(), "backup vault is in transition state")
	mockStorage.AssertExpectations(t)
}

func TestRotateCmekBackupsForBackupVault_NonCmekVault(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)
	dbBV := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:            "backup-vault-name",
		AccountID:       account.ID,
		BackupVaultType: "IN_REGION",
		LifeCycleState:  datamodel.LifeCycleStateREADY,
		CmekAttributes:  nil,
	}
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(dbBV, nil)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: workflow_engine_mock.NewMockTemporalTestClient(t),
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1")

	assert.Error(t, err)
	assert.Equal(t, "", jobUUID)
	assert.Contains(t, err.Error(), "cmek backup rotation can not be called for backup vault without CMEK configuration")
	mockStorage.AssertExpectations(t)
}

func TestRotateCmekBackupsForBackupVault_CrossRegionSourceDisallowed(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)
	sourceRegion := "us-central1"
	dbBV := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:             "backup-vault-name",
		AccountID:        account.ID,
		BackupVaultType:  activities.CrossRegionBackupType,
		LifeCycleState:   datamodel.LifeCycleStateREADY,
		SourceRegionName: &sourceRegion,
		CmekAttributes: &datamodel.CmekAttributes{
			KmsConfigResourcePath: nillable.ToPointer("projects/p/locations/r/kmsConfigs/test"),
		},
	}
	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(dbBV, nil)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: workflow_engine_mock.NewMockTemporalTestClient(t),
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1")

	assert.Error(t, err)
	assert.Equal(t, "", jobUUID)
	assert.Contains(t, err.Error(), "cmek backup rotation can not be called for cross region source backup vault")
	mockStorage.AssertExpectations(t)
}

func TestRotateCmekBackupsForBackupVault_CreateJobFails(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)
	dbBV := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:      "backup-vault-name",
		AccountID: account.ID,
		CmekAttributes: &datamodel.CmekAttributes{
			KmsConfigResourcePath: nillable.ToPointer("projects/p/locations/r/kmsConfigs/test"),
		},
	}

	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(dbBV, nil)
	mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("create job failed"))

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: workflow_engine_mock.NewMockTemporalTestClient(t),
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1")

	assert.Error(t, err)
	assert.Equal(t, "", jobUUID)
	mockStorage.AssertExpectations(t)
}

func TestRotateCmekBackupsForBackupVault_WorkflowStartFails_UpdatesJobState(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
	params := &commonparams.BackupVaultParams{
		OwnerID:       "owner-uuid",
		BackupVaultID: "backup-vault-uuid",
		Region:        "us-central1",
	}

	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	mockStorage := new(database.MockStorage)
	dbBV := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "backup-vault-uuid"},
		Name:      "backup-vault-name",
		AccountID: account.ID,
		CmekAttributes: &datamodel.CmekAttributes{
			KmsConfigResourcePath: nillable.ToPointer("projects/p/locations/r/kmsConfigs/test"),
		},
	}
	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "workflow-id",
	}

	mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, params.BackupVaultID, int64(account.ID)).Return(dbBV, nil)
	mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(errors.New("update failed"))

	mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(t)
	// Cause ExecuteWorkflow to fail so that the defer block runs UpdateJob.
	mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("workflow execution failed"))

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	jobUUID, err := orchestrator.RotateCmekBackupsForBackupVault(ctx, params, "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1")

	assert.Error(t, err)
	assert.Equal(t, "", jobUUID)
	mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything)
}

func TestUpdateBackupVaultDeferFunction(t *testing.T) {
	t.Run("WhenWorkflowStartFails_ShouldRollbackBackupVaultState", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(backupVault, nil)
		// Mock rollback
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(backupVault, nil)
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		}

		result, jobID, err := updateBackupVault(ctx, mockStorage, mockTemporal, params)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify rollback was called
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed")
	})

	t.Run("WhenWorkflowStartFails_AndRollbackFails_ShouldLogError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(backupVault, nil)
		// Mock rollback to fail
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(nil, errors.New("rollback failed"))
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		}

		result, jobID, err := updateBackupVault(ctx, mockStorage, mockTemporal, params)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify rollback was attempted even though it failed
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed")
	})

	t.Run("WhenWorkflowStartFails_AndJobUpdateFails_ShouldLogError", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(backupVault, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails).Return(backupVault, nil)
		// Mock job update to fail
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(errors.New("job update failed"))

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		}

		result, jobID, err := updateBackupVault(ctx, mockStorage, mockTemporal, params)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify both rollback operations were attempted
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed")
	})

	t.Run("WhenWorkflowStartSucceeds_ShouldNotRollback", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		enforcedDuration := int64(30)
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
			Account:               account,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &enforcedDuration,
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(backupVault, nil)

		// Mock workflow execution to succeed
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		}

		result, jobID, err := updateBackupVault(ctx, mockStorage, mockTemporal, params)

		// Verify results
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "job-uuid", jobID)

		// Verify rollback was NOT called
		mockStorage.AssertNotCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertNotCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything)
	})

	t.Run("WhenCreateJobFails_ShouldNotRollback", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
			Account:               account,
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		// Mock CreateJob to fail
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("create job failed"))

		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		}

		result, jobID, err := updateBackupVault(ctx, mockStorage, mockTemporal, params)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "create job failed", err.Error())

		// Verify rollback was NOT called since no job was created
		mockStorage.AssertNotCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, datamodel.LifeCycleStateAvailableDetails)
		mockStorage.AssertNotCalled(t, "UpdateJob", ctx, mock.Anything, string(datamodel.JobsStateERROR), 0, mock.Anything)
	})

	t.Run("WhenBackupVaultHasDifferentOriginalState_ShouldRollbackToCorrectState", func(t *testing.T) {
		ctx := context.Background()
		mockLogger := log.NewLogger()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"}}
		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "backup-vault-uuid", ID: 1},
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: "Updating backup vault",
			Account:               account,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		// Setup mocks
		mockStorage.On("GetOrCreateAccount", ctx, "owner-uuid").Return(account, nil)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, "backup-vault-uuid", int64(1)).Return(backupVault, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails).Return(backupVault, nil)
		// Mock rollback to original state
		mockStorage.On("UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, "Updating backup vault").Return(backupVault, nil)
		mockStorage.On("UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

		// Mock workflow execution to fail
		mockTemporal.On("ExecuteWorkflow", ctx, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("workflow start failed"))

		params := &commonparams.BackupVaultParams{
			OwnerID:       "owner-uuid",
			BackupVaultID: "backup-vault-uuid",
			Name:          "test-vault",
		}

		result, jobID, err := updateBackupVault(ctx, mockStorage, mockTemporal, params)

		// Verify results
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Empty(t, jobID)
		assert.Equal(t, "workflow start failed", err.Error())

		// Verify rollback was called with the original state
		mockStorage.AssertCalled(t, "UpdateBackupVaultState", ctx, backupVault, datamodel.LifeCycleStateAvailable, "Updating backup vault")
		mockStorage.AssertCalled(t, "UpdateJob", ctx, "job-uuid", string(datamodel.JobsStateERROR), 0, "workflow start failed")
	})
}

func TestIsBackupVaultAttachedToVolume(t *testing.T) {
	ctx := context.Background()

	t.Run("WhenBackupVaultHasAttachedVolumes_ReturnsTrue", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		backupVaultUUID := "backup-vault-uuid-with-volumes"

		// Mock storage to return a count > 0 indicating attached volumes
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, backupVaultUUID).Return(int64(2), nil)

		result, err := orchestrator.IsBackupVaultAttachedToVolume(ctx, backupVaultUUID)

		assert.NoError(tt, err)
		assert.True(tt, result, "Expected true when backup vault has attached volumes")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBackupVaultHasNoAttachedVolumes_ReturnsFalse", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		backupVaultUUID := "backup-vault-uuid-no-volumes"

		// Mock storage to return a count of 0 indicating no attached volumes
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, backupVaultUUID).Return(int64(0), nil)

		result, err := orchestrator.IsBackupVaultAttachedToVolume(ctx, backupVaultUUID)

		assert.NoError(tt, err)
		assert.False(tt, result, "Expected false when backup vault has no attached volumes")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenDatabaseReturnsError_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		backupVaultUUID := "backup-vault-uuid-error"
		expectedError := errors.New("database connection error")

		// Mock storage to return an error
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, backupVaultUUID).Return(int64(0), expectedError)

		result, err := orchestrator.IsBackupVaultAttachedToVolume(ctx, backupVaultUUID)

		assert.Error(tt, err)
		assert.False(tt, result, "Expected false when error occurs")
		assert.Equal(tt, expectedError, err, "Expected the same error that was returned from storage")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBackupVaultUUIDIsEmpty_CallsDatabaseWithEmptyString", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		backupVaultUUID := ""

		// Mock storage to return 0 for empty UUID
		mockStorage.On("GetVolumeCountByBackupVaultID", ctx, backupVaultUUID).Return(int64(0), nil)

		result, err := orchestrator.IsBackupVaultAttachedToVolume(ctx, backupVaultUUID)

		assert.NoError(tt, err)
		assert.False(tt, result, "Expected false for empty backup vault UUID")
		mockStorage.AssertExpectations(tt)
	})
}

func TestGetBackupVaultByExternalUUIDAndOwnerID(t *testing.T) {
	ctx := context.Background()

	// Store original function to restore after tests
	originalGetOrCreateAccount := getOrCreateAccount
	defer func() {
		getOrCreateAccount = originalGetOrCreateAccount
	}()

	t.Run("WhenBackupVaultFound_ReturnsBackupVault", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		ownerID := "owner-uuid"

		backupVault := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:                  "test-backup-vault",
			AccountID:             account.ID,
			Account:               account,
			ExternalUUID:          &externalUUID,
			LifeCycleState:        datamodel.LifeCycleStateAvailable,
			LifeCycleStateDetails: datamodel.LifeCycleStateAvailableDetails,
			RegionName:            "us-central1",
			AccountVendorID:       "vendor-id",
			BackupVaultType:       "STANDARD",
		}

		// Mock the package-level function
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// Mock the storage method for getting backup vault
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(backupVault, nil)

		result, err := orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, ownerID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, backupVault.UUID, result.UUID)
		assert.Equal(tt, backupVault.Name, result.Name)
		assert.Equal(tt, backupVault.AccountID, result.AccountID)
		assert.Equal(tt, externalUUID, *result.ExternalUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAccountNotFound_CreatesAccount_ReturnsBackupVault", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		ownerID := "owner-uuid"

		backupVault := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:         "test-backup-vault",
			AccountID:    account.ID,
			Account:      account,
			ExternalUUID: &externalUUID,
		}

		// Mock the package-level function to simulate account not found initially, then created
		callCount := 0
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			callCount++
			if callCount == 1 {
				// First call simulates account creation workflow
				return account, nil
			}
			return account, nil
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(backupVault, nil)

		result, err := orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, ownerID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, backupVault.UUID, result.UUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAccountCreationFails_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		externalUUID := "external-backup-vault-uuid"
		ownerID := "invalid-owner"
		expectedError := errors.New("account creation failed")

		// Mock the package-level function to return error
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, expectedError
		}

		result, err := orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, ownerID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBackupVaultNotFound_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "non-existent-external-uuid"
		ownerID := "owner-uuid"

		// Mock the package-level function
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(nil, gorm.ErrRecordNotFound)

		result, err := orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, ownerID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, gorm.ErrRecordNotFound, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenDatabaseError_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		ownerID := "owner-uuid"
		expectedError := errors.New("database connection error")

		// Mock the package-level function
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(nil, expectedError)

		result, err := orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, ownerID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenEmptyParameters_HandlesGracefully", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		externalUUID := ""
		ownerID := ""
		expectedError := errors.New("invalid parameters")

		// Mock the package-level function to handle empty parameters
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, expectedError
		}

		result, err := orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, ownerID)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAccountCreationRaceCondition_ReturnsAccountAfterRetry", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		ownerID := "owner-uuid"

		backupVault := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:         "test-backup-vault",
			AccountID:    account.ID,
			Account:      account,
			ExternalUUID: &externalUUID,
		}

		// Mock the package-level function to simulate race condition - creation fails but account exists after
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			// This simulates the race condition where another thread created the account
			return account, nil
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(backupVault, nil)

		result, err := orchestrator.GetBackupVaultByExternalUUIDAndOwnerID(ctx, externalUUID, ownerID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, backupVault.UUID, result.UUID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestCreateBackupVault(t *testing.T) {
	ctx := context.Background()

	originalGetOrCreateAccount := getOrCreateAccount
	defer func() {
		getOrCreateAccount = originalGetOrCreateAccount
	}()

	t.Run("WhenGetOrCreateAccountFails_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		expectedErr := errors.New("failed to get account")
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, expectedErr
		}

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-1",
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedErr, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCreateBackupVaultEntryFails_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
			Name:      "project-1",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		expectedErr := errors.New("create backup vault failed")
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv != nil &&
				bv.Name == "bv-1" &&
				bv.AccountID == account.ID &&
				bv.RegionName == "us-east4"
		})).Return(nil, expectedErr)

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-1",
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, expectedErr, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCreateBackupVaultEntrySucceeds_ReturnsConvertedModel", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
			Name:      "project-1",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		created := &datamodel.BackupVault{
			BaseModel:  datamodel.BaseModel{ID: 11, UUID: "vault-uuid"},
			Name:       "bv-1",
			AccountID:  account.ID,
			Account:    account,
			RegionName: "us-east4",
		}
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv != nil &&
				bv.Name == "bv-1" &&
				bv.AccountID == account.ID &&
				bv.RegionName == "us-east4"
		})).Return(created, nil)

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-1",
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "vault-uuid", result.BackupVaultID)
		assert.Equal(tt, "bv-1", result.Name)
		assert.Equal(tt, "owner-uuid", result.OwnerID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossRegionRemoteCreateFails_RollsBackCreatedBackupVault", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
			Name:      "project-1",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		backupRegion := "us-west1"
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 12, UUID: "vault-cross-uuid"},
			Name:             "bv-cross",
			AccountID:        account.ID,
			Account:          account,
			RegionName:       "us-east4",
			BackupRegionName: &backupRegion,
			BackupVaultType:  CrossRegionBackupType,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(created, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(nil, errors.New("create job failed"))
		mockStorage.On("DeleteBackupVaultInVCP", ctx, created.UUID).Return(created, nil).Once()

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-cross",
			BackupRegion:  &backupRegion,
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "create job failed")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossRegionRemoteCreateSucceeds_DoesNotRollback", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
			Name:      "project-1",
		}
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		backupRegion := "us-west1"
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 13, UUID: "vault-cross-success-uuid"},
			Name:             "bv-cross-success",
			AccountID:        account.ID,
			Account:          account,
			RegionName:       "us-east4",
			BackupRegionName: &backupRegion,
			BackupVaultType:  CrossRegionBackupType,
		}
		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(created, nil)
		mockStorage.On("CreateJob", ctx, mock.Anything).Return(job, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-cross-success",
			BackupRegion:  &backupRegion,
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		mockStorage.AssertNotCalled(tt, "DeleteBackupVaultInVCP", mock.Anything, mock.Anything)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

func TestBuildBackupVaultFromCreateParams(t *testing.T) {
	t.Run("WhenParamsIncludeDescriptionImmutableCmekAndTenantProject_PopulatesAllFields", func(tt *testing.T) {
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 77, UUID: "owner-uuid"},
			Name:      "test-account",
		}
		backupRegion := "us-west1"
		locationID := "us-central1"
		description := "backup vault description"
		tenantProject := "tenant-project-12345"
		minRetention := int64(30)
		dailyImmutable := true
		weeklyImmutable := true
		monthlyImmutable := false
		adhocImmutable := true
		kmsConfigPath := "projects/p1/locations/us-central1/keyRings/r1/cryptoKeys/k1"
		backupsPrimaryKeyVersion := "projects/p1/locations/us-central1/keyRings/r1/cryptoKeys/k1/cryptoKeyVersions/1"

		params := &commonparams.CreateBackupVaultParams{
			ResourceId:    "test-backup-vault",
			Description:   description,
			BackupRegion:  &backupRegion,
			LocationId:    locationID,
			ProjectNumber: "project-123",
			TenantProject: &tenantProject,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &minRetention,
				IsDailyBackupImmutable:                 &dailyImmutable,
				IsWeeklyBackupImmutable:                &weeklyImmutable,
				IsMonthlyBackupImmutable:               &monthlyImmutable,
				IsAdhocBackupImmutable:                 &adhocImmutable,
			},
			KmsConfigResourcePath:    &kmsConfigPath,
			BackupsPrimaryKeyVersion: &backupsPrimaryKeyVersion,
		}

		result := buildBackupVaultFromCreateParams(params, account)

		assert.NotNil(tt, result)
		assert.Equal(tt, "test-backup-vault", result.Name)
		assert.Equal(tt, account.ID, result.AccountID)
		assert.Equal(tt, account, result.Account)
		assert.Equal(tt, locationID, result.RegionName)
		assert.Equal(tt, &backupRegion, result.BackupRegionName)
		assert.Equal(tt, datamodel.LifeCycleStateREADY, result.LifeCycleState)
		assert.Equal(tt, datamodel.LifeCycleStateAvailableDetails, result.LifeCycleStateDetails)
		assert.Equal(tt, CrossRegionBackupType, result.BackupVaultType)
		assert.NotNil(tt, result.SourceRegionName)
		assert.Equal(tt, locationID, *result.SourceRegionName)

		assert.NotNil(tt, result.Description)
		assert.Equal(tt, description, *result.Description)

		assert.NotNil(tt, result.ImmutableAttributes)
		assert.Equal(tt, &minRetention, result.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration)
		assert.Equal(tt, dailyImmutable, result.ImmutableAttributes.IsDailyBackupImmutable)
		assert.Equal(tt, weeklyImmutable, result.ImmutableAttributes.IsWeeklyBackupImmutable)
		assert.Equal(tt, monthlyImmutable, result.ImmutableAttributes.IsMonthlyBackupImmutable)
		assert.Equal(tt, adhocImmutable, result.ImmutableAttributes.IsAdhocBackupImmutable)

		assert.NotNil(tt, result.CmekAttributes)
		assert.Equal(tt, &kmsConfigPath, result.CmekAttributes.KmsConfigResourcePath)
		assert.Equal(tt, &backupsPrimaryKeyVersion, result.CmekAttributes.BackupsPrimaryKeyVersion)

		assert.Equal(tt, datamodel.ServiceTypeCrossProject, result.ServiceType)
		assert.Len(tt, result.BucketDetails, 1)
		assert.NotNil(tt, result.BucketDetails[0])
		assert.Equal(tt, tenantProject, result.BucketDetails[0].TenantProjectNumber)
	})

	t.Run("WhenBackupRegionEqualsLocationWithOptionalFieldsSet_UsesInRegionType", func(tt *testing.T) {
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 78, UUID: "owner-uuid-2"},
			Name:      "test-account-2",
		}
		locationID := "us-central1"
		backupRegion := locationID
		description := "another description"
		tenantProject := "tenant-project-2"
		dailyImmutable := true

		params := &commonparams.CreateBackupVaultParams{
			ResourceId:    "test-backup-vault-2",
			Description:   description,
			BackupRegion:  &backupRegion,
			LocationId:    locationID,
			TenantProject: &tenantProject,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				IsDailyBackupImmutable: &dailyImmutable,
			},
		}

		result := buildBackupVaultFromCreateParams(params, account)

		assert.NotNil(tt, result)
		assert.Equal(tt, InRegionBackupType, result.BackupVaultType)
		assert.NotNil(tt, result.Description)
		assert.Equal(tt, description, *result.Description)
		assert.NotNil(tt, result.ImmutableAttributes)
		assert.Equal(tt, dailyImmutable, result.ImmutableAttributes.IsDailyBackupImmutable)
		assert.Equal(tt, datamodel.ServiceTypeCrossProject, result.ServiceType)
		assert.Len(tt, result.BucketDetails, 1)
		assert.Equal(tt, tenantProject, result.BucketDetails[0].TenantProjectNumber)
	})
}

func TestUpdateBackupVaultInternal(t *testing.T) {
	// Create a logger for context
	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	// Store original function to restore after tests
	originalGetOrCreateAccount := getOrCreateAccount
	originalConvertDatastoreBackupVaultToModel := convertDatastoreBackupVaultToModel
	defer func() {
		getOrCreateAccount = originalGetOrCreateAccount
		convertDatastoreBackupVaultToModel = originalConvertDatastoreBackupVaultToModel
	}()

	t.Run("WhenSuccessfulUpdateWithDescription", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		newDescription := "Updated description"

		existingBV := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:            "test-backup-vault",
			AccountID:       account.ID,
			Account:         account,
			ExternalUUID:    &externalUUID,
			LifeCycleState:  datamodel.LifeCycleStateAvailable,
			RegionName:      "us-central1",
			AccountVendorID: "vendor-id",
			BackupVaultType: "STANDARD",
		}

		updatedBV := &datamodel.BackupVault{
			BaseModel:       existingBV.BaseModel,
			Name:            existingBV.Name,
			AccountID:       existingBV.AccountID,
			Account:         existingBV.Account,
			Description:     &newDescription,
			LifeCycleState:  existingBV.LifeCycleState,
			RegionName:      existingBV.RegionName,
			AccountVendorID: existingBV.AccountVendorID,
			BackupVaultType: existingBV.BackupVaultType,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               "owner-uuid",
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID: bv.UUID,
				Name:          bv.Name,
				Description:   bv.Description,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv.Description != nil && *bv.Description == newDescription
		}), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, existingBV.UUID, result.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSuccessfulUpdateWithBackupRetentionPolicy", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		newRetentionDuration := int64(30)
		dailyImmutable := true
		weeklyImmutable := true
		monthlyImmutable := false
		adhocImmutable := false

		existingBV := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:            "test-backup-vault",
			AccountID:       account.ID,
			Account:         account,
			ExternalUUID:    &externalUUID,
			LifeCycleState:  datamodel.LifeCycleStateAvailable,
			RegionName:      "us-central1",
			AccountVendorID: "vendor-id",
			BackupVaultType: "STANDARD",
		}

		updatedBV := &datamodel.BackupVault{
			BaseModel:   existingBV.BaseModel,
			Name:        existingBV.Name,
			AccountID:   existingBV.AccountID,
			Account:     existingBV.Account,
			Description: existingBV.Description,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &newRetentionDuration,
				IsDailyBackupImmutable:                 dailyImmutable,
				IsWeeklyBackupImmutable:                weeklyImmutable,
				IsMonthlyBackupImmutable:               monthlyImmutable,
				IsAdhocBackupImmutable:                 adhocImmutable,
			},
			LifeCycleState:  existingBV.LifeCycleState,
			RegionName:      existingBV.RegionName,
			AccountVendorID: existingBV.AccountVendorID,
			BackupVaultType: existingBV.BackupVaultType,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       "owner-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &newRetentionDuration,
				IsDailyBackupImmutable:                 &dailyImmutable,
				IsWeeklyBackupImmutable:                &weeklyImmutable,
				IsMonthlyBackupImmutable:               &monthlyImmutable,
				IsAdhocBackupImmutable:                 &adhocImmutable,
			},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID: bv.UUID,
				Name:          bv.Name,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv.ImmutableAttributes != nil &&
				bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration != nil &&
				*bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration == newRetentionDuration &&
				bv.ImmutableAttributes.IsDailyBackupImmutable == dailyImmutable
		}), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSuccessfulUpdateWithPartialRetentionPolicy", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		existingRetentionDuration := int64(15)
		dailyImmutable := true

		existingBV := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:            "test-backup-vault",
			AccountID:       account.ID,
			Account:         account,
			ExternalUUID:    &externalUUID,
			LifeCycleState:  datamodel.LifeCycleStateAvailable,
			RegionName:      "us-central1",
			AccountVendorID: "vendor-id",
			BackupVaultType: "STANDARD",
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &existingRetentionDuration,
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}

		updatedBV := &datamodel.BackupVault{
			BaseModel:   existingBV.BaseModel,
			Name:        existingBV.Name,
			AccountID:   existingBV.AccountID,
			Account:     existingBV.Account,
			Description: existingBV.Description,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &existingRetentionDuration,
				IsDailyBackupImmutable:                 dailyImmutable,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
			LifeCycleState:  existingBV.LifeCycleState,
			RegionName:      existingBV.RegionName,
			AccountVendorID: existingBV.AccountVendorID,
			BackupVaultType: existingBV.BackupVaultType,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       "owner-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				IsDailyBackupImmutable: &dailyImmutable,
			},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID: bv.UUID,
				Name:          bv.Name,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv.ImmutableAttributes != nil &&
				bv.ImmutableAttributes.IsDailyBackupImmutable == dailyImmutable &&
				bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration != nil &&
				*bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration == existingRetentionDuration
		}), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSuccessfulUpdateWithDescriptionAndRetentionPolicy", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		newDescription := "Complete update"
		newRetentionDuration := int64(45)
		dailyImmutable := true
		weeklyImmutable := true
		monthlyImmutable := true
		adhocImmutable := true

		existingBV := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:            "test-backup-vault",
			AccountID:       account.ID,
			Account:         account,
			ExternalUUID:    &externalUUID,
			LifeCycleState:  datamodel.LifeCycleStateAvailable,
			RegionName:      "us-central1",
			AccountVendorID: "vendor-id",
			BackupVaultType: "STANDARD",
		}

		updatedBV := &datamodel.BackupVault{
			BaseModel:   existingBV.BaseModel,
			Name:        existingBV.Name,
			AccountID:   existingBV.AccountID,
			Account:     existingBV.Account,
			Description: &newDescription,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &newRetentionDuration,
				IsDailyBackupImmutable:                 dailyImmutable,
				IsWeeklyBackupImmutable:                weeklyImmutable,
				IsMonthlyBackupImmutable:               monthlyImmutable,
				IsAdhocBackupImmutable:                 adhocImmutable,
			},
			LifeCycleState:  existingBV.LifeCycleState,
			RegionName:      existingBV.RegionName,
			AccountVendorID: existingBV.AccountVendorID,
			BackupVaultType: existingBV.BackupVaultType,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       "owner-uuid",
			Description:   &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &newRetentionDuration,
				IsDailyBackupImmutable:                 &dailyImmutable,
				IsWeeklyBackupImmutable:                &weeklyImmutable,
				IsMonthlyBackupImmutable:               &monthlyImmutable,
				IsAdhocBackupImmutable:                 &adhocImmutable,
			},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID: bv.UUID,
				Name:          bv.Name,
				Description:   bv.Description,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv.Description != nil &&
				*bv.Description == newDescription &&
				bv.ImmutableAttributes != nil &&
				*bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration == newRetentionDuration &&
				bv.ImmutableAttributes.IsDailyBackupImmutable == dailyImmutable
		}), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, &newDescription, result.Description)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAccountCreationFails_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		externalUUID := "external-backup-vault-uuid"
		expectedError := errors.New("account creation failed")

		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               "invalid-owner",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, expectedError
		}

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBackupVaultNotFound_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "non-existent-external-uuid"

		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               "owner-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(nil, gorm.ErrRecordNotFound)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, gorm.ErrRecordNotFound, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateBackupVaultInVCPFails_ReturnsError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		newDescription := "Updated description"
		expectedError := errors.New("database update error")

		existingBV := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:            "test-backup-vault",
			AccountID:       account.ID,
			Account:         account,
			ExternalUUID:    &externalUUID,
			LifeCycleState:  datamodel.LifeCycleStateAvailable,
			RegionName:      "us-central1",
			AccountVendorID: "vendor-id",
			BackupVaultType: "STANDARD",
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               "owner-uuid",
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.Anything, existingBV).Return(nil, expectedError)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, expectedError, err)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNoUpdatesProvided_PreservesExistingValues", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		existingDescription := "Existing description"
		existingRetentionDuration := int64(15)

		existingBV := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:            "test-backup-vault",
			AccountID:       account.ID,
			Account:         account,
			ExternalUUID:    &externalUUID,
			Description:     &existingDescription,
			LifeCycleState:  datamodel.LifeCycleStateAvailable,
			RegionName:      "us-central1",
			AccountVendorID: "vendor-id",
			BackupVaultType: "STANDARD",
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &existingRetentionDuration,
				IsDailyBackupImmutable:                 true,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               "owner-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID: bv.UUID,
				Name:          bv.Name,
				Description:   bv.Description,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv.Description != nil &&
				*bv.Description == existingDescription &&
				bv.ImmutableAttributes != nil &&
				*bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration == existingRetentionDuration
		}), existingBV).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, &existingDescription, result.Description)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenExistingBackupVaultHasNoImmutableAttributes_CreatesNewAttributes", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		newRetentionDuration := int64(30)
		dailyImmutable := true

		existingBV := &datamodel.BackupVault{
			BaseModel:           datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:                "test-backup-vault",
			AccountID:           account.ID,
			Account:             account,
			ExternalUUID:        &externalUUID,
			LifeCycleState:      datamodel.LifeCycleStateAvailable,
			RegionName:          "us-central1",
			AccountVendorID:     "vendor-id",
			BackupVaultType:     "STANDARD",
			ImmutableAttributes: nil, // No existing attributes
		}

		updatedBV := &datamodel.BackupVault{
			BaseModel:       existingBV.BaseModel,
			Name:            existingBV.Name,
			AccountID:       existingBV.AccountID,
			Account:         existingBV.Account,
			Description:     existingBV.Description,
			LifeCycleState:  existingBV.LifeCycleState,
			RegionName:      existingBV.RegionName,
			AccountVendorID: existingBV.AccountVendorID,
			BackupVaultType: existingBV.BackupVaultType,
			ImmutableAttributes: &datamodel.ImmutableAttributes{
				BackupMinimumEnforcedRetentionDuration: &newRetentionDuration,
				IsDailyBackupImmutable:                 dailyImmutable,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       "owner-uuid",
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{
				BackupMinimumEnforcedRetentionDuration: &newRetentionDuration,
				IsDailyBackupImmutable:                 &dailyImmutable,
			},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID: bv.UUID,
				Name:          bv.Name,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv.ImmutableAttributes != nil &&
				bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration != nil &&
				*bv.ImmutableAttributes.BackupMinimumEnforcedRetentionDuration == newRetentionDuration
		}), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenLifeCycleStatePreserved_KeepsOriginalState", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		newDescription := "Updated description"
		originalLifeCycleState := datamodel.LifeCycleStateAvailable
		originalLifeCycleStateDetails := datamodel.LifeCycleStateAvailableDetails

		existingBV := &datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:                  "test-backup-vault",
			AccountID:             account.ID,
			Account:               account,
			ExternalUUID:          &externalUUID,
			LifeCycleState:        originalLifeCycleState,
			LifeCycleStateDetails: originalLifeCycleStateDetails,
			RegionName:            "us-central1",
			AccountVendorID:       "vendor-id",
			BackupVaultType:       "STANDARD",
		}

		updatedBV := &datamodel.BackupVault{
			BaseModel:             existingBV.BaseModel,
			Name:                  existingBV.Name,
			AccountID:             existingBV.AccountID,
			Account:               existingBV.Account,
			Description:           &newDescription,
			LifeCycleState:        originalLifeCycleState,
			LifeCycleStateDetails: originalLifeCycleStateDetails,
			RegionName:            existingBV.RegionName,
			AccountVendorID:       existingBV.AccountVendorID,
			BackupVaultType:       existingBV.BackupVaultType,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               "owner-uuid",
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID:  bv.UUID,
				Name:           bv.Name,
				LifeCycleState: bv.LifeCycleState,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv.LifeCycleState == originalLifeCycleState &&
				bv.LifeCycleStateDetails == originalLifeCycleStateDetails
		}), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, originalLifeCycleState, result.LifeCycleState)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenUpdateBackupVaultBucketDetailsFails_LogsWarningAndSucceeds", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := new(workflow_engine_mock.MockTemporalTestClient)

		orchestrator := &GCPOrchestrator{
			storage:  mockStorage,
			temporal: mockTemporal,
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "owner-uuid"},
			Name:      "test-account",
		}

		externalUUID := "external-backup-vault-uuid"
		newDescription := "Updated description"

		existingBV := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 1, UUID: "backup-vault-uuid"},
			Name:            "test-backup-vault",
			AccountID:       account.ID,
			Account:         account,
			ExternalUUID:    &externalUUID,
			LifeCycleState:  datamodel.LifeCycleStateAvailable,
			RegionName:      "us-central1",
			AccountVendorID: "vendor-id",
			BackupVaultType: "STANDARD",
		}

		updatedBV := &datamodel.BackupVault{
			BaseModel:       existingBV.BaseModel,
			Name:            existingBV.Name,
			AccountID:       existingBV.AccountID,
			Account:         existingBV.Account,
			Description:     &newDescription,
			LifeCycleState:  existingBV.LifeCycleState,
			RegionName:      existingBV.RegionName,
			AccountVendorID: existingBV.AccountVendorID,
			BackupVaultType: existingBV.BackupVaultType,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               "owner-uuid",
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
			return &models.BackupVaultV1beta{
				BackupVaultID: bv.UUID,
				Name:          bv.Name,
				Description:   bv.Description,
			}
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.Anything, existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(errors.New("bucket details update failed"))

		// The DB failure is non-fatal: the function logs a warning and still returns success.
		result, operationID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, existingBV.UUID, result.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})
}

func TestUpdateBackupVaultInternal_VCPOrchestration(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	const (
		externalUUID = "external-vault-uuid"
		sdeVaultID   = "sde-internal-vault-id"
		ownerID      = "owner-project-number"
		region       = "us-west1"
	)
	newDescription := "updated description"
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: ownerID},
		Name:      ownerID,
	}

	// Save and restore all overridden function vars.
	origGetOrCreate := getOrCreateAccount
	origConvert := convertDatastoreBackupVaultToModel
	origGetFromSDE := getBackupVaultFromSDEByExternalUUID
	defer func() {
		getOrCreateAccount = origGetOrCreate
		convertDatastoreBackupVaultToModel = origConvert
		getBackupVaultFromSDEByExternalUUID = origGetFromSDE
	}()

	getOrCreateAccount = func(_ context.Context, _ database.Storage, _ string) (*datamodel.Account, error) {
		return account, nil
	}
	convertDatastoreBackupVaultToModel = func(bv *datamodel.BackupVault) *models.BackupVaultV1beta {
		return &models.BackupVaultV1beta{BackupVaultID: bv.UUID}
	}

	// Enable the feature flag for all subtests; each subtest restores it after.
	origFlag := env.EnableBackupVaultVCPOrchestration
	env.EnableBackupVaultVCPOrchestration = true
	defer func() { env.EnableBackupVaultVCPOrchestration = origFlag }()

	// -----------------------------------------------------------------------
	// Subtest: flag ON + CRB (useExternalUUID) + vault IS in VCP DB
	// No SDE update API call; only VCP DB is updated.
	// -----------------------------------------------------------------------
	t.Run("FlagON_CRB_VaultPresentInVCP_UpdatesVCPOnly", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		existingBV := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: sdeVaultID},
			AccountID:    account.ID,
			ExternalUUID: nillable.GetStringPtr(externalUUID),
			RegionName:   region,
		}
		updatedBV := &datamodel.BackupVault{
			BaseModel:    existingBV.BaseModel,
			AccountID:    existingBV.AccountID,
			Description:  &newDescription,
			ExternalUUID: nillable.GetStringPtr(externalUUID),
		}
		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               ownerID,
			Region:                region,
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault"), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, opID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.Equal(tt, "", opID)
		assert.NotNil(tt, result)
		assert.Equal(tt, sdeVaultID, result.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	// -----------------------------------------------------------------------
	// Subtest: flag ON + CRB (useExternalUUID) + vault NOT in VCP DB (SDE-only)
	// Vault is fetched from SDE, inserted into VCP DB, then the update is applied.
	// -----------------------------------------------------------------------
	t.Run("FlagON_CRB_VaultAbsentFromVCP_FetchesFromSDEInsertsThenUpdates", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		sdeDataModel := &datamodel.BackupVault{
			BaseModel:  datamodel.BaseModel{UUID: sdeVaultID},
			RegionName: region,
		}
		insertedBV := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 2, UUID: sdeVaultID},
			AccountID: account.ID,
		}
		updatedBV := &datamodel.BackupVault{
			BaseModel:    insertedBV.BaseModel,
			AccountID:    insertedBV.AccountID,
			Description:  &newDescription,
			ExternalUUID: nillable.GetStringPtr(externalUUID),
		}
		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               ownerID,
			Region:                region,
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		// Vault absent in VCP DB.
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		// getBackupVaultFromSDEByExternalUUID returns the SDE model.
		getBackupVaultFromSDEByExternalUUID = func(_ context.Context, extUUID, rgn, acct string) (*datamodel.BackupVault, error) {
			assert.Equal(tt, externalUUID, extUUID)
			assert.Equal(tt, region, rgn)
			assert.Equal(tt, ownerID, acct)
			return sdeDataModel, nil
		}

		// After insert, the update path runs on the inserted record.
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(insertedBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault"), insertedBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, opID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.Equal(tt, "", opID)
		assert.NotNil(tt, result)
		assert.Equal(tt, sdeVaultID, result.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	// -----------------------------------------------------------------------
	// Subtest: flag OFF + useExternalUUID + vault IS in VCP DB — update only, no SDE fetch.
	// -----------------------------------------------------------------------
	t.Run("FlagOFF_CRB_VaultPresentInVCP_UpdatesWithoutSDEFetch", func(tt *testing.T) {
		env.EnableBackupVaultVCPOrchestration = false
		defer func() { env.EnableBackupVaultVCPOrchestration = true }()

		mockStorage := new(database.MockStorage)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		existingBV := &datamodel.BackupVault{
			BaseModel:    datamodel.BaseModel{ID: 1, UUID: sdeVaultID},
			AccountID:    account.ID,
			ExternalUUID: nillable.GetStringPtr(externalUUID),
		}
		updatedBV := &datamodel.BackupVault{
			BaseModel:    existingBV.BaseModel,
			AccountID:    existingBV.AccountID,
			Description:  &newDescription,
			ExternalUUID: nillable.GetStringPtr(externalUUID),
		}
		params := &commonparams.BackupVaultParams{
			BackupVaultID:         externalUUID,
			OwnerID:               ownerID,
			Region:                region,
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		// SDE fetch must NOT be called when flag is OFF.
		sdeCallCount := 0
		getBackupVaultFromSDEByExternalUUID = func(_ context.Context, _, _, _ string) (*datamodel.BackupVault, error) {
			sdeCallCount++
			return nil, fmt.Errorf("should not have been called")
		}

		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault"), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		result, opID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, true)

		assert.NoError(tt, err)
		assert.Equal(tt, "", opID)
		assert.NotNil(tt, result)
		assert.Equal(tt, 0, sdeCallCount, "SDE fetch must not be called when flag is OFF")
		mockStorage.AssertExpectations(tt)
	})

	// -----------------------------------------------------------------------
	// Subtest: flag ON + CMEK path (useExternalUUID == false) — CRB branch skipped
	// -----------------------------------------------------------------------
	t.Run("FlagON_CMEK_useExternalUUID_false_VCPDBOnly", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		existingBV := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: sdeVaultID},
			AccountID: account.ID,
		}
		updatedBV := &datamodel.BackupVault{
			BaseModel:    existingBV.BaseModel,
			AccountID:    existingBV.AccountID,
			Description:  &newDescription,
			ExternalUUID: existingBV.ExternalUUID,
		}
		params := &commonparams.BackupVaultParams{
			BackupVaultID:         sdeVaultID,
			OwnerID:               ownerID,
			Region:                region,
			Description:           &newDescription,
			BackupRetentionPolicy: commonparams.BackupRetentionPolicyParams{},
		}

		// SDE fetch must NOT be called for CMEK path.
		sdeCallCount := 0
		getBackupVaultFromSDEByExternalUUID = func(_ context.Context, _, _, _ string) (*datamodel.BackupVault, error) {
			sdeCallCount++
			return nil, fmt.Errorf("should not be called for CMEK path")
		}

		mockStorage.On("GetBackupVaultByUUIDndOwnerID", ctx, sdeVaultID, account.ID).Return(existingBV, nil)
		mockStorage.On("UpdateBackupVaultInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault"), existingBV).Return(updatedBV, nil)
		mockStorage.On("UpdateBackupVaultBucketDetails", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(nil)

		// useExternalUUID = false → CMEK path, CRB branch not entered even with flag ON.
		result, opID, err := orchestrator.UpdateBackupVaultInternal(ctx, params, false)

		assert.NoError(tt, err)
		assert.Equal(tt, "", opID)
		assert.NotNil(tt, result)
		assert.Equal(tt, 0, sdeCallCount, "SDE fetch must not be called for CMEK path")
		mockStorage.AssertExpectations(tt)
	})
}

func TestDeleteBackupVaultInternal(t *testing.T) {
	ctx := context.Background()

	externalUUID := "external-backup-vault-uuid"       // source region UUID == destination vault's ExternalUUID
	destinationUUID := "destination-backup-vault-uuid" // destination vault's own UUID
	ownerID := "owner-uuid"

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 1, UUID: ownerID},
		Name:      "test-account",
	}

	remoteBV := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: destinationUUID, ID: 1},
		Name:            "test-backup-vault",
		AccountID:       account.ID,
		Account:         account,
		AccountVendorID: "vendor-id",
		RegionName:      "us-central1",
		ExternalUUID:    &externalUUID,
	}

	// SDE resolution/deletion now happens inside DeleteBackupVaultInternal; stub the SDE helpers so these
	// unit tests stay VCP-focused. The "not found in VCP" cases resolve to no SDE match (nil).
	originalGetBackupVaultFromSDEByExternalUUID := getBackupVaultFromSDEByExternalUUID
	getBackupVaultFromSDEByExternalUUID = func(_ context.Context, _, _, _ string) (*datamodel.BackupVault, error) {
		return nil, nil
	}
	defer func() { getBackupVaultFromSDEByExternalUUID = originalGetBackupVaultFromSDEByExternalUUID }()

	originalDeleteBackupVaultInSDEInternal := deleteBackupVaultInSDE
	deleteBackupVaultInSDE = func(_ context.Context, _, _, _ string) error {
		return nil
	}
	defer func() { deleteBackupVaultInSDE = originalDeleteBackupVaultInSDEInternal }()

	t.Run("WhenSuccessfulDelete_ReturnsEmptyStringAndNoError", func(tt *testing.T) {
		originalUseVCPRegion := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = originalUseVCPRegion }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		// Mock GetAccount - successful
		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)

		// Mock GetBackupVaultByExternalUUIDAndOwnerID - successful
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(remoteBV, nil)

		// Mock DeleteBackupVaultInVCP - successful
		mockStorage.On("DeleteBackupVaultInVCP", ctx, destinationUUID).Return(remoteBV, nil)

		// Act
		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		// Assert
		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, destinationUUID, params.BackupVaultID) // Verify UUID was updated
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAccountNotFound_ReturnsEmptyStringAndNoError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		notFoundError := utilErrors.NewNotFoundErr("account", nil)

		mockStorage.On("GetAccount", ctx, ownerID).Return(nil, notFoundError)

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, externalUUID, params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenAccountNotFoundWrappedInVCPError_ReturnsEmptyStringAndNoError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		// Matches database/vcp/accounts.go GetAccount not-found path.
		projectNumber := "123456789012"
		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       projectNumber,
			Name:          "test-backup-vault",
			Region:        "us-east4",
		}

		accountNotFound := vsaerrors.NewVCPError(
			vsaerrors.ErrAccountNotFound,
			utilErrors.NewNotFoundErr("account", nil),
		)
		mockStorage.On("GetAccount", ctx, projectNumber).Return(nil, accountNotFound)

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.NoError(tt, err)
		assert.True(tt, utilErrors.IsNotFoundErr(accountNotFound))
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, externalUUID, params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBackupVaultNotFound_ReturnsEmptyStringAndNoError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
			Region:        "us-west1",
		}

		backupVaultNotFound := utilErrors.NewNotFoundErr("backup vault", &externalUUID)

		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(nil, backupVaultNotFound)

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, externalUUID, params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossRegionDestinationAccountNotFound_ReturnsEmptyStringAndNoError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		destinationProject := "987654321098"
		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       destinationProject,
			Name:          "test-backup-vault",
			Region:        "us-west1",
		}

		mockStorage.On("GetAccount", ctx, destinationProject).Return(nil, vsaerrors.NewVCPError(
			vsaerrors.ErrAccountNotFound,
			utilErrors.NewNotFoundErr("account", nil),
		))

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossRegionDestinationBackupVaultAlreadyDeleted_ReturnsEmptyStringAndNoError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
			Region:        "us-west1",
		}

		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", &externalUUID))

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetBackupVaultByExternalUUIDAndOwnerIDFailsWithNonNotFoundError_ReturnsError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		expectedError := errors.New("database read failed")

		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(nil, expectedError)

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		assert.Equal(tt, "", operationID)
		assert.False(tt, utilErrors.IsNotFoundErr(err))
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenDeleteBackupVaultInVCPFails_ReturnsError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		expectedError := errors.New("failed to delete backup vault in VCP")

		// Mock GetAccount - successful
		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)

		// Mock GetBackupVaultByExternalUUIDAndOwnerID - successful
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(remoteBV, nil)

		// Mock DeleteBackupVaultInVCP - fails
		mockStorage.On("DeleteBackupVaultInVCP", ctx, destinationUUID).Return(nil, expectedError)

		// Act
		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		// Assert
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenBackupVaultIDIsUpdatedInParams_VerifyParamsMutation", func(tt *testing.T) {
		originalUseVCPRegion := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = originalUseVCPRegion }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		// Mock GetAccount - successful
		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)

		// Mock GetBackupVaultByExternalUUIDAndOwnerID - successful
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(remoteBV, nil)

		// Mock DeleteBackupVaultInVCP - successful
		mockStorage.On("DeleteBackupVaultInVCP", ctx, destinationUUID).Return(remoteBV, nil)

		// Verify initial state
		assert.Equal(tt, externalUUID, params.BackupVaultID)

		// Act
		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		// Assert
		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		// Verify the params.BackupVaultID was mutated to internal UUID
		assert.Equal(tt, destinationUUID, params.BackupVaultID)
		assert.NotEqual(tt, externalUUID, params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenGetAccountReturnsNilAccount_ReturnsError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		expectedError := errors.New("account is nil")

		// Mock GetAccount - returns nil account
		mockStorage.On("GetAccount", ctx, ownerID).Return(nil, expectedError)

		// Act
		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		// Assert
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenDatabaseConnectionError_ReturnsError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		expectedError := errors.New("database connection error")

		// Mock GetAccount - database error
		mockStorage.On("GetAccount", ctx, ownerID).Return(nil, expectedError)

		// Act
		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		// Assert
		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenHydrationEnabledAndCrossProjectDeleteHydrationFails_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{
			storage: mockStorage,
		}

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		originalUseVCPRegion := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = originalUseVCPRegion }()

		expectedError := errors.New("failed to hydrate deleted backup vault to CCFE")
		originalHydrateDeletedBackupVaults := hydrateDeletedBackupVaults
		hydrateDeletedBackupVaults = func(ctx context.Context, backupVault *datamodel.BackupVault, params *commonparams.BackupVaultParams) error {
			return expectedError
		}
		defer func() { hydrateDeletedBackupVaults = originalHydrateDeletedBackupVaults }()

		backupRegionName := "us-central1"
		crossProjectRemoteBV := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: destinationUUID, ID: 1},
			Name:             "test-backup-vault",
			AccountID:        account.ID,
			Account:          account,
			AccountVendorID:  "vendor-id",
			RegionName:       "us-central1",
			ExternalUUID:     &externalUUID,
			BackupRegionName: &backupRegionName,
			ServiceType:      datamodel.ServiceTypeCrossProject,
		}

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
		}

		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(crossProjectRemoteBV, nil)
		mockStorage.On("DeleteBackupVaultInVCP", ctx, destinationUUID).Return(crossProjectRemoteBV, nil)

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.Error(tt, err)
		assert.Equal(tt, expectedError, err)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, destinationUUID, params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenFoundInVCP_DeletesInSDEUsingDestinationUUIDThenDeletesInVCP", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalFlag := env.EnableBackupVaultVCPOrchestration
		env.EnableBackupVaultVCPOrchestration = true
		defer func() { env.EnableBackupVaultVCPOrchestration = originalFlag }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
			Region:        "us-west1",
		}

		var sdeDeletedID string
		origDelete := deleteBackupVaultInSDE
		deleteBackupVaultInSDE = func(_ context.Context, _, _, id string) error {
			sdeDeletedID = id
			return nil
		}
		defer func() { deleteBackupVaultInSDE = origDelete }()

		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(remoteBV, nil)
		mockStorage.On("DeleteBackupVaultInVCP", ctx, destinationUUID).Return(remoteBV, nil)

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, destinationUUID, sdeDeletedID) // SDE deleted using the destination vault's own UUID, not the external (source) UUID
		assert.Equal(tt, destinationUUID, params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNotInVCPButResolvedFromSDE_DeletesInSDEAndToleratesVCPNotFound", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalFlag := env.EnableBackupVaultVCPOrchestration
		env.EnableBackupVaultVCPOrchestration = true
		defer func() { env.EnableBackupVaultVCPOrchestration = originalFlag }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = false
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
			Region:        "us-west1",
		}

		// Legacy SDE-first vault: resolved from SDE by external UUID, keyed in SDE by its own (destination) UUID.
		sdeBV := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "sde-destination-uuid"},
			Name:      "legacy-vault",
		}
		origResolve := getBackupVaultFromSDEByExternalUUID
		getBackupVaultFromSDEByExternalUUID = func(_ context.Context, ext, _, _ string) (*datamodel.BackupVault, error) {
			assert.Equal(tt, externalUUID, ext)
			return sdeBV, nil
		}
		defer func() { getBackupVaultFromSDEByExternalUUID = origResolve }()

		var sdeDeletedID string
		origDelete := deleteBackupVaultInSDE
		deleteBackupVaultInSDE = func(_ context.Context, _, _, id string) error {
			sdeDeletedID = id
			return nil
		}
		defer func() { deleteBackupVaultInSDE = origDelete }()

		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", &externalUUID))
		// No VCP record exists for a legacy SDE-only vault, so the VCP delete returns NotFound and is tolerated.
		mockStorage.On("DeleteBackupVaultInVCP", ctx, "sde-destination-uuid").
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.NoError(tt, err)
		assert.Equal(tt, "", operationID)
		assert.Equal(tt, "sde-destination-uuid", sdeDeletedID)
		// params.BackupVaultID is updated to the resolved destination UUID before the VCP delete.
		assert.Equal(tt, "sde-destination-uuid", params.BackupVaultID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenSDEDeleteFails_ReturnsErrorWithoutVCPDelete", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalFlag := env.EnableBackupVaultVCPOrchestration
		env.EnableBackupVaultVCPOrchestration = true
		defer func() { env.EnableBackupVaultVCPOrchestration = originalFlag }()

		params := &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			OwnerID:       ownerID,
			Name:          "test-backup-vault",
			Region:        "us-west1",
		}

		expectedErr := errors.New("SDE delete failed")
		origDelete := deleteBackupVaultInSDE
		deleteBackupVaultInSDE = func(_ context.Context, _, _, _ string) error {
			return expectedErr
		}
		defer func() { deleteBackupVaultInSDE = origDelete }()

		mockStorage.On("GetAccount", ctx, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", ctx, externalUUID, account.ID).Return(remoteBV, nil)

		operationID, err := orchestrator.DeleteBackupVaultInternal(ctx, params)

		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Equal(tt, "", operationID)
		mockStorage.AssertExpectations(tt) // DeleteBackupVaultInVCP must not be called
	})
}

func TestHydrateDeletedBackupVaults(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	backupRegionName := "us-central1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
		Name:             "test-backup-vault",
		BackupRegionName: &backupRegionName,
		BackupVaultType:  "STANDARD",
		CmekAttributes:   &datamodel.CmekAttributes{},
	}
	params := &commonparams.BackupVaultParams{
		OwnerID: "owner-uuid",
	}

	t.Run("WhenGenerateCallbackTokenFails_ReturnsError", func(tt *testing.T) {
		expectedErr := errors.New("failed to generate callback token")

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", expectedErr
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		err := _hydrateDeletedBackupVaults(ctx, backupVault, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenHydrateDeletedBackupVaultsFails_ReturnsError", func(tt *testing.T) {
		expectedErr := errors.New("failed to hydrate deleted backup vaults")
		expectedToken := "test-token"

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return expectedToken, nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		originalHydrateDeletedBackupVaults := commonparams.HydrateDeletedBackupVaults
		commonparams.HydrateDeletedBackupVaults = func(ctx context.Context, logger log.Logger, requests []string, backupVaultName string, location string, projectID string, token string) error {
			assert.Equal(tt, []string{"backupVaults/" + backupVault.Name}, requests)
			assert.Equal(tt, backupVault.Name, backupVaultName)
			assert.Equal(tt, backupRegionName, location)
			assert.Equal(tt, params.OwnerID, projectID)
			assert.Equal(tt, expectedToken, token)
			return expectedErr
		}
		defer func() { commonparams.HydrateDeletedBackupVaults = originalHydrateDeletedBackupVaults }()

		err := _hydrateDeletedBackupVaults(ctx, backupVault, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenHydrationSucceeds_ReturnsNil", func(tt *testing.T) {
		expectedToken := "test-token"

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return expectedToken, nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		originalHydrateDeletedBackupVaults := commonparams.HydrateDeletedBackupVaults
		commonparams.HydrateDeletedBackupVaults = func(ctx context.Context, logger log.Logger, requests []string, backupVaultName string, location string, projectID string, token string) error {
			assert.Equal(tt, []string{"backupVaults/" + backupVault.Name}, requests)
			assert.Equal(tt, backupVault.Name, backupVaultName)
			assert.Equal(tt, backupRegionName, location)
			assert.Equal(tt, params.OwnerID, projectID)
			assert.Equal(tt, expectedToken, token)
			return nil
		}
		defer func() { commonparams.HydrateDeletedBackupVaults = originalHydrateDeletedBackupVaults }()

		err := _hydrateDeletedBackupVaults(ctx, backupVault, params)
		assert.NoError(tt, err)
	})

	t.Run("WhenCrossProjectCrossRegionDestinationVault_HydrationSkipped", func(tt *testing.T) {
		destinationRegion := "us-east1"
		crbDestinationVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "crb-dest-uuid"},
			Name:             "crb-dest-vault",
			BackupRegionName: &destinationRegion,
			BackupVaultType:  CrossRegionBackupType,
			ServiceType:      datamodel.ServiceTypeCrossProject,
		}
		destParams := &commonparams.BackupVaultParams{
			OwnerID: "owner-uuid",
			Region:  destinationRegion,
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			tt.Fatalf("GenerateCallbackToken should not be called when hydration is skipped")
			return "", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateDeletedBackupVaults := commonparams.HydrateDeletedBackupVaults
		commonparams.HydrateDeletedBackupVaults = func(ctx context.Context, logger log.Logger, requests []string, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { commonparams.HydrateDeletedBackupVaults = originalHydrateDeletedBackupVaults }()

		err := _hydrateDeletedBackupVaults(ctx, crbDestinationVault, destParams)
		assert.NoError(tt, err)
		assert.False(tt, hydrationCalled, "delete hydration should be skipped for cross-project cross-region destination vault")
	})

	t.Run("WhenCrossRegionButNotCrossProject_HydrationOccurs", func(tt *testing.T) {
		destinationRegion := "us-east1"
		crossRegionVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "cross-region-uuid"},
			Name:             "cross-region-vault",
			BackupRegionName: &destinationRegion,
			BackupVaultType:  CrossRegionBackupType,
			ServiceType:      datamodel.ServiceTypeGCNV,
		}
		destParams := &commonparams.BackupVaultParams{
			OwnerID: "owner-uuid",
			Region:  destinationRegion,
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateDeletedBackupVaults := commonparams.HydrateDeletedBackupVaults
		commonparams.HydrateDeletedBackupVaults = func(ctx context.Context, logger log.Logger, requests []string, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { commonparams.HydrateDeletedBackupVaults = originalHydrateDeletedBackupVaults }()

		err := _hydrateDeletedBackupVaults(ctx, crossRegionVault, destParams)
		assert.NoError(tt, err)
		assert.True(tt, hydrationCalled, "delete hydration should occur for non-cross-project cross-region vault")
	})

	t.Run("WhenCrossProjectButNotCrossRegion_HydrationOccurs", func(tt *testing.T) {
		region := "us-central1"
		crossProjectVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "cross-project-uuid"},
			Name:             "cross-project-vault",
			BackupRegionName: &region,
			BackupVaultType:  "STANDARD",
			ServiceType:      datamodel.ServiceTypeCrossProject,
		}
		destParams := &commonparams.BackupVaultParams{
			OwnerID: "owner-uuid",
			Region:  region,
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateDeletedBackupVaults := commonparams.HydrateDeletedBackupVaults
		commonparams.HydrateDeletedBackupVaults = func(ctx context.Context, logger log.Logger, requests []string, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { commonparams.HydrateDeletedBackupVaults = originalHydrateDeletedBackupVaults }()

		err := _hydrateDeletedBackupVaults(ctx, crossProjectVault, destParams)
		assert.NoError(tt, err)
		assert.True(tt, hydrationCalled, "delete hydration should occur for cross-project non-cross-region vault")
	})
}

// TestCreateBackupVault_VCPOrchestration covers the ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION branch of
// CreateBackupVault: cross-region vaults are hydrated asynchronously via CreateBackupVaultWorkflow.
func TestCreateBackupVault_VCPOrchestration(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	originalGetOrCreateAccount := getOrCreateAccount
	origUseVCPRegion := cvp.CVP_HOST
	origOrchestration := env.EnableBackupVaultVCPOrchestration
	defer func() {
		getOrCreateAccount = originalGetOrCreateAccount
		cvp.CVP_HOST = origUseVCPRegion
		env.EnableBackupVaultVCPOrchestration = origOrchestration
	}()
	// VCP orchestration path: orchestration flag on, USE_VCP_REGION off.
	cvp.CVP_HOST = "http://cvp-host"
	env.EnableBackupVaultVCPOrchestration = true

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
		Name:      "project-1",
	}
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	t.Run("InRegion_PersistsWithSuppliedUUID_NoWorkflowLaunched", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		created := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 11, UUID: "supplied-uuid"},
			Name:            "bv-1",
			AccountID:       account.ID,
			Account:         account,
			RegionName:      "us-east4",
			BackupVaultType: InRegionBackupType,
		}
		// VCP must persist the vault using the UUID generated/supplied by the caller, not generate its own.
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv != nil && bv.UUID == "supplied-uuid" && bv.Name == "bv-1" && bv.BackupVaultType == InRegionBackupType
		})).Return(created, nil)

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-1",
			BackupVaultID: "supplied-uuid",
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "supplied-uuid", result.BackupVaultID)
		assert.Equal(tt, "bv-1", result.Name)
		mockStorage.AssertExpectations(tt)
		// In-region vaults must not launch the cross-region hydration workflow.
		mockStorage.AssertNotCalled(tt, "CreateJob", mock.Anything, mock.Anything)
		mockTemporal.AssertNotCalled(tt, "ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("CrossRegion_PersistsAndLaunchesWorkflow", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		backupRegion := "us-west1"
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 12, UUID: "supplied-cross-uuid"},
			Name:             "bv-cross",
			AccountID:        account.ID,
			Account:          account,
			RegionName:       "us-east4",
			BackupRegionName: &backupRegion,
			BackupVaultType:  CrossRegionBackupType,
		}
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			return bv != nil && bv.UUID == "supplied-cross-uuid" && bv.BackupVaultType == CrossRegionBackupType
		})).Return(created, nil)

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		mockStorage.On("CreateJob", ctx, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.Type == string(datamodel.JobTypeCreateBackupVault) &&
				j.State == string(datamodel.JobsStateNEW) &&
				j.ResourceName == created.Name &&
				j.JobAttributes != nil &&
				j.JobAttributes.ResourceUUID == created.UUID
		})).Return(job, nil)

		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-cross",
			BackupVaultID: "supplied-cross-uuid",
			BackupRegion:  &backupRegion,
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "supplied-cross-uuid", result.BackupVaultID)
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("CrossRegion_WorkflowLaunchFails_RollsBackVaultAndMarksJobError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		backupRegion := "us-west1"
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 13, UUID: "rollback-uuid"},
			Name:             "bv-rollback",
			AccountID:        account.ID,
			Account:          account,
			RegionName:       "us-east4",
			BackupRegionName: &backupRegion,
			BackupVaultType:  CrossRegionBackupType,
		}
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault")).Return(created, nil)

		job := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}
		mockStorage.On("CreateJob", ctx, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
		mockTemporal.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("workflow start failed"))

		// On failure the orchestrator must roll back the VCP entry and mark the job as errored.
		mockStorage.On("DeleteBackupVaultInVCP", ctx, created.UUID).Return(created, nil).Once()
		mockStorage.On("UpdateJob", ctx, job.UUID, string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil).Once()

		origDeleteInSDE := deleteBackupVaultInSDE
		tt.Cleanup(func() { deleteBackupVaultInSDE = origDeleteInSDE })
		sdeDeleteCalled := false
		deleteBackupVaultInSDE = func(_ context.Context, _, _, _ string) error {
			sdeDeleteCalled = true
			return nil
		}

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-rollback",
			BackupVaultID: "rollback-uuid",
			BackupRegion:  &backupRegion,
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.True(tt, sdeDeleteCalled, "SDE rollback must be triggered on workflow launch failure")
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})

	t.Run("InRegion_PersistFails_RollsBackSDE", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault")).
			Return(nil, errors.New("db unavailable"))

		var capturedRegion, capturedProject, capturedUUID string
		origDeleteInSDE := deleteBackupVaultInSDE
		tt.Cleanup(func() { deleteBackupVaultInSDE = origDeleteInSDE })
		deleteBackupVaultInSDE = func(_ context.Context, region, project, uuid string) error {
			capturedRegion = region
			capturedProject = project
			capturedUUID = uuid
			return nil
		}

		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-sde-rollback",
			BackupVaultID: "sde-pre-assigned-uuid",
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "us-east4", capturedRegion)
		assert.Equal(tt, "project-1", capturedProject)
		assert.Equal(tt, "sde-pre-assigned-uuid", capturedUUID)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("InRegion_PersistFails_NoPreassignedUUID_SkipsSDeRollback", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage, temporal: mockTemporal}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.AnythingOfType("*datamodel.BackupVault")).
			Return(nil, errors.New("db unavailable"))

		origDeleteInSDE := deleteBackupVaultInSDE
		tt.Cleanup(func() { deleteBackupVaultInSDE = origDeleteInSDE })
		sdeDeleteCalled := false
		deleteBackupVaultInSDE = func(_ context.Context, _, _, _ string) error {
			sdeDeleteCalled = true
			return nil
		}

		// No pre-assigned BackupVaultID: SDE was not called before this orchestrator call.
		params := &commonparams.CreateBackupVaultParams{
			ProjectNumber: "project-1",
			LocationId:    "us-east4",
			ResourceId:    "bv-no-uuid",
		}

		result, err := orchestrator.CreateBackupVault(ctx, params)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.False(tt, sdeDeleteCalled, "SDE rollback must not be triggered when no UUID was pre-assigned")
		mockStorage.AssertExpectations(tt)
	})
}

func TestCreateBackupVaultEntryInVCP(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
		Name:      "test-account",
	}
	backupRegion := "us-central1"

	t.Run("WhenGetOrCreateAccountFails_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		expectedErr := errors.New("failed to get account")
		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, expectedErr
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
	})

	t.Run("WhenCreateBackupVaultEntryInVCPFails_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}
		expectedErr := errors.New("create backup vault entry failed")

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(nil, expectedErr)

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossProject_CrossRegionNetappUuidHydrationFails_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		expectedErr := errors.New("netapp_uuid hydration failed")
		originalHydrateCrossRegion := hydrateBackupVaultCrossRegionNetappUuid
		hydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, backupVault *datamodel.BackupVault, crossRegionUuid string, params *commonparams.BackupVaultParams) error {
			return expectedErr
		}
		defer func() { hydrateBackupVaultCrossRegionNetappUuid = originalHydrateCrossRegion }()

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "created-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
			ServiceType:      datamodel.ServiceTypeCrossProject,
			AccountID:        account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(created, nil).Once()
		mockStorage.On("DeleteBackupVaultInVCP", ctx, created.UUID).Return(created, nil).Once()

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossProject_HydrationFails_RollbackDeletesCreatedBackupVault", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		expectedErr := errors.New("netapp_uuid hydration failed")
		originalHydrateCrossRegion := hydrateBackupVaultCrossRegionNetappUuid
		hydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, backupVault *datamodel.BackupVault, crossRegionUuid string, params *commonparams.BackupVaultParams) error {
			return expectedErr
		}
		defer func() { hydrateBackupVaultCrossRegionNetappUuid = originalHydrateCrossRegion }()

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "created-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
			ServiceType:      datamodel.ServiceTypeCrossProject,
			AccountID:        account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(created, nil).Once()
		mockStorage.On("DeleteBackupVaultInVCP", ctx, created.UUID).Return(created, nil).Once()

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossProject_HydrationSucceeds_ReturnsCreatedBackupVault", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		originalHydrateCrossRegion := hydrateBackupVaultCrossRegionNetappUuid
		hydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, backupVault *datamodel.BackupVault, crossRegionUuid string, params *commonparams.BackupVaultParams) error {
			return nil
		}
		defer func() { hydrateBackupVaultCrossRegionNetappUuid = originalHydrateCrossRegion }()

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "created-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
			ServiceType:      datamodel.ServiceTypeCrossProject,
			AccountID:        account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(created, nil)

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, created.UUID, result.UUID)
		assert.Equal(tt, created.Name, result.Name)
		mockStorage.AssertExpectations(tt)
	})

	// ---- SDE rollback tests (ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION) ----

	t.Run("WhenVCPOrchestrationEnabled_VCPCreateFails_RollsBackSDE", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalFlag := env.EnableBackupVaultVCPOrchestration
		env.EnableBackupVaultVCPOrchestration = true
		defer func() { env.EnableBackupVaultVCPOrchestration = originalFlag }()

		expectedErr := errors.New("create backup vault entry failed")
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(nil, expectedErr)

		var capturedRegion, capturedOwner, capturedUUID string
		origDeleteInSDE := deleteBackupVaultInSDE
		tt.Cleanup(func() { deleteBackupVaultInSDE = origDeleteInSDE })
		deleteBackupVaultInSDE = func(_ context.Context, region, owner, uuid string) error {
			capturedRegion = region
			capturedOwner = owner
			capturedUUID = uuid
			return nil
		}

		bv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "sde-vault-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
		}
		params := &commonparams.BackupVaultParams{
			AccountName: "test-account",
			OwnerID:     "owner-id",
			Region:      "us-central1",
		}

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
		assert.Equal(tt, "us-central1", capturedRegion, "SDE rollback must use params.Region")
		assert.Equal(tt, "owner-id", capturedOwner, "SDE rollback must use params.OwnerID")
		assert.Equal(tt, "sde-vault-uuid", capturedUUID, "SDE rollback must use the pre-assigned bv.UUID")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenVCPOrchestrationEnabled_HydrationFails_RollsBackSDEAndVCP", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalFlag := env.EnableBackupVaultVCPOrchestration
		env.EnableBackupVaultVCPOrchestration = true
		defer func() { env.EnableBackupVaultVCPOrchestration = originalFlag }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		// CVP_HOST configured so the flag is the sole condition that enables hydration.
		originalCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = originalCVPHost }()

		hydrateErr := errors.New("hydrate created backup vaults failed")
		originalHydrateCreatedBackupVaults := hydrateCreatedBackupVaults
		hydrateCreatedBackupVaults = func(ctx context.Context, backupVault *datamodel.BackupVault, params *commonparams.BackupVaultParams) error {
			return hydrateErr
		}
		defer func() { hydrateCreatedBackupVaults = originalHydrateCreatedBackupVaults }()

		sdeDeleteCalled := false
		origDeleteInSDE := deleteBackupVaultInSDE
		tt.Cleanup(func() { deleteBackupVaultInSDE = origDeleteInSDE })
		deleteBackupVaultInSDE = func(_ context.Context, _, _, _ string) error {
			sdeDeleteCalled = true
			return nil
		}

		bv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "sde-vault-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
		}
		params := &commonparams.BackupVaultParams{
			AccountName: "test-account",
			OwnerID:     "owner-id",
			Region:      "us-central1",
		}
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "created-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
			AccountID:        account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(created, nil).Once()
		mockStorage.On("DeleteBackupVaultInVCP", ctx, created.UUID).Return(created, nil).Once()

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, hydrateErr, err)
		assert.Nil(tt, result)
		assert.True(tt, sdeDeleteCalled, "SDE rollback must be triggered when hydration fails with orchestration enabled")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenVCPOrchestrationEnabled_SDERollbackFails_OriginalErrorReturned", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalFlag := env.EnableBackupVaultVCPOrchestration
		env.EnableBackupVaultVCPOrchestration = true
		defer func() { env.EnableBackupVaultVCPOrchestration = originalFlag }()

		expectedErr := errors.New("create backup vault entry failed")
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(nil, expectedErr)

		origDeleteInSDE := deleteBackupVaultInSDE
		tt.Cleanup(func() { deleteBackupVaultInSDE = origDeleteInSDE })
		deleteBackupVaultInSDE = func(_ context.Context, _, _, _ string) error {
			return errors.New("SDE delete failed")
		}

		bv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "sde-vault-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
		}
		params := &commonparams.BackupVaultParams{
			AccountName: "test-account",
			OwnerID:     "owner-id",
			Region:      "us-central1",
		}

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err, "original VCP create error must be returned even when SDE rollback itself fails")
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenVCPOrchestrationEnabled_EmptyUUID_SkipsSDERollback", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalFlag := env.EnableBackupVaultVCPOrchestration
		env.EnableBackupVaultVCPOrchestration = true
		defer func() { env.EnableBackupVaultVCPOrchestration = originalFlag }()

		expectedErr := errors.New("create backup vault entry failed")
		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(nil, expectedErr)

		origDeleteInSDE := deleteBackupVaultInSDE
		tt.Cleanup(func() { deleteBackupVaultInSDE = origDeleteInSDE })
		sdeDeleteCalled := false
		deleteBackupVaultInSDE = func(_ context.Context, _, _, _ string) error {
			sdeDeleteCalled = true
			return nil
		}

		// bv has no UUID — SDE was not called before this function, so rollback must be skipped.
		bv := &datamodel.BackupVault{
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
		}
		params := &commonparams.BackupVaultParams{
			AccountName: "test-account",
			OwnerID:     "owner-id",
			Region:      "us-central1",
		}

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
		assert.False(tt, sdeDeleteCalled, "SDE rollback must not be triggered when bv.UUID is empty")
		mockStorage.AssertExpectations(tt)
	})
}

func TestHydrateCreatedBackupVaults(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	backupRegionName := "us-central1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
		Name:             "test-backup-vault",
		BackupRegionName: &backupRegionName,
		BackupVaultType:  "STANDARD",
	}
	params := &commonparams.BackupVaultParams{
		OwnerID: "owner-uuid",
	}

	t.Run("WhenGenerateCallbackTokenFails_ReturnsError", func(tt *testing.T) {
		expectedErr := errors.New("failed to generate callback token")

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", expectedErr
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		err := _hydrateCreatedBackupVaults(ctx, backupVault, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenHydrateCreatedBackupVaultsFails_ReturnsError", func(tt *testing.T) {
		expectedErr := errors.New("failed to hydrate created backup vaults")
		expectedToken := "test-token"

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return expectedToken, nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		originalHydrateCreatedBackupVaults := commonparams.HydrateCreatedBackupVaults
		commonparams.HydrateCreatedBackupVaults = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			assert.Len(tt, requests, 1)
			assert.NotNil(tt, requests[0].BackupVault)
			assert.Equal(tt, backupVault.Name, requests[0].BackupVault.ResourceId)
			assert.Equal(tt, backupVault.UUID, requests[0].BackupVault.BackupVaultId)
			assert.Equal(tt, backupVault.Name, backupVaultName)
			assert.Equal(tt, backupRegionName, location)
			assert.Equal(tt, params.OwnerID, projectID)
			assert.Equal(tt, expectedToken, token)
			return expectedErr
		}
		defer func() { commonparams.HydrateCreatedBackupVaults = originalHydrateCreatedBackupVaults }()

		err := _hydrateCreatedBackupVaults(ctx, backupVault, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenHydrationSucceeds_ReturnsNil", func(tt *testing.T) {
		expectedToken := "test-token"

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return expectedToken, nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		originalHydrateCreatedBackupVaults := commonparams.HydrateCreatedBackupVaults
		commonparams.HydrateCreatedBackupVaults = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			assert.Len(tt, requests, 1)
			assert.NotNil(tt, requests[0].BackupVault)
			assert.Equal(tt, backupVault.Name, requests[0].BackupVault.ResourceId)
			assert.Equal(tt, backupVault.UUID, requests[0].BackupVault.BackupVaultId)
			assert.Equal(tt, backupVault.Name, backupVaultName)
			assert.Equal(tt, backupRegionName, location)
			assert.Equal(tt, params.OwnerID, projectID)
			assert.Equal(tt, expectedToken, token)
			return nil
		}
		defer func() { commonparams.HydrateCreatedBackupVaults = originalHydrateCreatedBackupVaults }()

		err := _hydrateCreatedBackupVaults(ctx, backupVault, params)
		assert.NoError(tt, err)
	})

	t.Run("WhenCrossProjectCrossRegionDestination_SkipsHydration", func(tt *testing.T) {
		destinationRegion := "us-east1"
		crbDestinationVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "crb-dest-uuid"},
			Name:             "crb-dest-vault",
			BackupRegionName: &destinationRegion,
			BackupVaultType:  CrossRegionBackupType,
			ServiceType:      datamodel.ServiceTypeCrossProject,
		}
		destParams := &commonparams.BackupVaultParams{
			OwnerID: "owner-uuid",
			Region:  destinationRegion,
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			tt.Fatalf("GenerateCallbackToken should not be called when hydration is skipped")
			return "", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateCreatedBackupVaults := commonparams.HydrateCreatedBackupVaults
		commonparams.HydrateCreatedBackupVaults = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { commonparams.HydrateCreatedBackupVaults = originalHydrateCreatedBackupVaults }()

		err := _hydrateCreatedBackupVaults(ctx, crbDestinationVault, destParams)
		assert.NoError(tt, err)
		assert.False(tt, hydrationCalled, "hydration should be skipped for cross-project cross-region destination vault")
	})

	t.Run("WhenCrossRegionButNotCrossProject_HydrationOccurs", func(tt *testing.T) {
		destinationRegion := "us-east1"
		crossRegionVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "cross-region-uuid"},
			Name:             "cross-region-vault",
			BackupRegionName: &destinationRegion,
			BackupVaultType:  CrossRegionBackupType,
			ServiceType:      datamodel.ServiceTypeGCNV,
		}
		destParams := &commonparams.BackupVaultParams{
			OwnerID: "owner-uuid",
			Region:  destinationRegion,
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateCreatedBackupVaults := commonparams.HydrateCreatedBackupVaults
		commonparams.HydrateCreatedBackupVaults = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { commonparams.HydrateCreatedBackupVaults = originalHydrateCreatedBackupVaults }()

		err := _hydrateCreatedBackupVaults(ctx, crossRegionVault, destParams)
		assert.NoError(tt, err)
		assert.True(tt, hydrationCalled, "hydration should occur for non-cross-project cross-region vault")
	})

	t.Run("WhenCrossProjectButNotCrossRegion_HydrationOccurs", func(tt *testing.T) {
		region := "us-central1"
		crossProjectVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "cross-project-uuid"},
			Name:             "cross-project-vault",
			BackupRegionName: &region,
			BackupVaultType:  "STANDARD",
			ServiceType:      datamodel.ServiceTypeCrossProject,
		}
		destParams := &commonparams.BackupVaultParams{
			OwnerID: "owner-uuid",
			Region:  region,
		}

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "test-token", nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		hydrationCalled := false
		originalHydrateCreatedBackupVaults := commonparams.HydrateCreatedBackupVaults
		commonparams.HydrateCreatedBackupVaults = func(ctx context.Context, logger log.Logger, requests []models.Request, backupVaultName string, location string, projectID string, token string) error {
			hydrationCalled = true
			return nil
		}
		defer func() { commonparams.HydrateCreatedBackupVaults = originalHydrateCreatedBackupVaults }()

		err := _hydrateCreatedBackupVaults(ctx, crossProjectVault, destParams)
		assert.NoError(tt, err)
		assert.True(tt, hydrationCalled, "hydration should occur for cross-project non-cross-region vault")
	})
}

func TestShouldSkipDestinationHydration(t *testing.T) {
	destinationRegion := "us-east1"

	crbDestVault := &datamodel.BackupVault{
		BackupRegionName: &destinationRegion,
		BackupVaultType:  CrossRegionBackupType,
		ServiceType:      datamodel.ServiceTypeCrossProject,
	}

	t.Run("WhenBackupVaultIsNil_ReturnsFalse", func(tt *testing.T) {
		assert.False(tt, shouldSkipDestinationHydration(nil))
	})

	t.Run("WhenCrossRegionButNotCrossProject_ReturnsFalse", func(tt *testing.T) {
		vault := &datamodel.BackupVault{
			BackupVaultType: CrossRegionBackupType,
			ServiceType:     datamodel.ServiceTypeGCNV,
		}
		assert.False(tt, shouldSkipDestinationHydration(vault))
	})

	t.Run("WhenCrossProjectButNotCrossRegion_ReturnsFalse", func(tt *testing.T) {
		vault := &datamodel.BackupVault{
			BackupVaultType: "STANDARD",
			ServiceType:     datamodel.ServiceTypeCrossProject,
		}
		assert.False(tt, shouldSkipDestinationHydration(vault))
	})

	t.Run("WhenCrossProjectCrossRegion_ReturnsTrue", func(tt *testing.T) {
		assert.True(tt, shouldSkipDestinationHydration(crbDestVault))
	})

	t.Run("WhenCrossProjectCrossRegionWithoutBackupRegion_StillReturnsTrue", func(tt *testing.T) {
		vault := &datamodel.BackupVault{
			BackupVaultType: CrossRegionBackupType,
			ServiceType:     datamodel.ServiceTypeCrossProject,
		}
		assert.True(tt, shouldSkipDestinationHydration(vault))
	})
}

// setupGCPTestBVClient replaces sde.BackupVaultCreateClient with a mock CVP client, returning a
// cleanup func. Use in gcp-package tests that exercise functions calling sde.DeleteSDEBackupVault
// or sde.GetSDEBackupVaultByExternalUUID.
func setupGCPTestBVClient(t *testing.T, mockBVClient backup_vault.ClientService) func() {
	t.Helper()
	cvpClient := &cvpapi.Cvp{BackupVault: mockBVClient}
	orig := sde.BackupVaultCreateClient
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}
	return func() { sde.BackupVaultCreateClient = orig }
}

// ---- Section 2: _getBackupVaultFromSDEByExternalUUID ----

func TestGetBackupVaultFromSDEByExternalUUID(t *testing.T) {
	const (
		region       = "us-central1"
		ownerID      = "1234567890"
		externalUUID = "ext-uuid-123"
		sdeVaultID   = "sde-internal-id"
	)

	ctx := context.Background()

	t.Run("WhenSDEReturnsMatchingVault_ConvertsToDataModel", func(tt *testing.T) {
		resourceID := "my-vault"
		bvType := "STANDARD"
		ext := externalUUID
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			&backup_vault.V1betaListBackupVaultsOK{Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{BackupVaultID: sdeVaultID, ExternalUUID: &ext, ResourceID: &resourceID, BackupVaultType: &bvType, State: "ACTIVE"},
				},
			}}, nil).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		result, err := _getBackupVaultFromSDEByExternalUUID(ctx, externalUUID, region, ownerID)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, sdeVaultID, result.UUID)
	})

	t.Run("WhenSDEReturnsNilVault_ReturnsNilNil", func(tt *testing.T) {
		other := "different-uuid"
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			&backup_vault.V1betaListBackupVaultsOK{Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{BackupVaultID: "other-id", ExternalUUID: &other},
				},
			}}, nil).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		result, err := _getBackupVaultFromSDEByExternalUUID(ctx, externalUUID, region, ownerID)

		assert.NoError(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("WhenSDEReturnsIsNotFoundErr_ReturnsNilNil", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			nil, fmt.Errorf("not found: %w", utilErrors.NewNotFoundErr("backup vault", nil))).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		result, err := _getBackupVaultFromSDEByExternalUUID(ctx, externalUUID, region, ownerID)

		assert.NoError(tt, err)
		assert.Nil(tt, result)
	})

	t.Run("WhenSDEReturnsGenericError_PropagatesError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			nil, fmt.Errorf("transport failure")).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		result, err := _getBackupVaultFromSDEByExternalUUID(ctx, externalUUID, region, ownerID)

		assert.Nil(tt, result)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "transport failure")
	})

	t.Run("WhenConvertToDataModelFails_ReturnsWrappedError", func(tt *testing.T) {
		resourceID := "my-vault"
		ext := externalUUID
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaListBackupVaults", mock.Anything).Return(
			&backup_vault.V1betaListBackupVaultsOK{Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*cvpmodels.BackupVaultV1beta{
					{BackupVaultID: sdeVaultID, ExternalUUID: &ext, ResourceID: &resourceID},
				},
			}}, nil).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		origConvert := activities.ConvertToBackupVaultDataModel
		activities.ConvertToBackupVaultDataModel = func(payload *cvpmodels.BackupVaultV1beta, region string) (*datamodel.BackupVault, error) {
			return nil, fmt.Errorf("conversion error")
		}
		defer func() { activities.ConvertToBackupVaultDataModel = origConvert }()

		result, err := _getBackupVaultFromSDEByExternalUUID(ctx, externalUUID, region, ownerID)

		assert.Nil(tt, result)
		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to convert backup vault to data model")
		assert.Contains(tt, err.Error(), "conversion error")
	})
}

// ---- Section 3: _deleteBackupVaultInSDE ----

func TestDeleteBackupVaultInSDE(t *testing.T) {
	const (
		region     = "us-central1"
		ownerID    = "1234567890"
		sdeVaultID = "sde-vault-id"
	)

	ctx := context.Background()

	t.Run("WhenSDEReturnsOperationV1beta_ReturnsNil", func(tt *testing.T) {
		done := true
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			&backup_vault.V1betaDeleteBackupVaultAccepted{
				Payload: &cvpmodels.OperationV1beta{Name: "operations/op-1", Done: &done},
			}, nil, nil).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		err := _deleteBackupVaultInSDE(ctx, region, ownerID, sdeVaultID)

		assert.NoError(tt, err)
	})

	t.Run("WhenSDEReturnsNotFound_ReturnsNil", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultNotFound{
				Payload: &cvpmodels.Error{Code: 404, Message: "not found"},
			}).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		err := _deleteBackupVaultInSDE(ctx, region, ownerID, sdeVaultID)

		assert.NoError(tt, err)
	})

	t.Run("WhenSDEReturnsBadRequest_ReturnsError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultBadRequest{
				Payload: &cvpmodels.Error{Code: 400, Message: "bad request"},
			}).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		err := _deleteBackupVaultInSDE(ctx, region, ownerID, sdeVaultID)

		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to delete backup vault")
		assert.Contains(tt, err.Error(), sdeVaultID)
	})

	t.Run("WhenSDEReturnsConflict_ReturnsError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, &backup_vault.V1betaDeleteBackupVaultConflict{
				Payload: &cvpmodels.Error{Code: 409, Message: "conflict"},
			}).Once()
		defer setupGCPTestBVClient(tt, mockClient)()

		err := _deleteBackupVaultInSDE(ctx, region, ownerID, sdeVaultID)

		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to delete backup vault")
	})
}

// ---- Section 1: DeleteBackupVaultInternal – ENABLE_BACKUP_VAULT_VCP_ORCHESTRATION block ----

func TestDeleteBackupVaultInternal_VCPOrchestration(t *testing.T) {
	const (
		ownerID      = "proj-123"
		externalUUID = "ext-bv-uuid"
		region       = "us-central1"
		sdeVaultUUID = "sde-internal-uuid"
	)

	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 10, UUID: ownerID}}

	// originals to restore after each sub-test
	origOrchestration := env.EnableBackupVaultVCPOrchestration
	origGetBVFromSDE := getBackupVaultFromSDEByExternalUUID
	origDeleteInSDE := deleteBackupVaultInSDE
	origHydrate := hydrateDeletedBackupVaults
	origHydrationEnabled := hydrationEnabled
	defer func() {
		env.EnableBackupVaultVCPOrchestration = origOrchestration
		getBackupVaultFromSDEByExternalUUID = origGetBVFromSDE
		deleteBackupVaultInSDE = origDeleteInSDE
		hydrateDeletedBackupVaults = origHydrate
		hydrationEnabled = origHydrationEnabled
	}()

	env.EnableBackupVaultVCPOrchestration = true
	hydrationEnabled = false

	newParams := func() *commonparams.BackupVaultParams {
		return &commonparams.BackupVaultParams{
			BackupVaultID: externalUUID,
			Region:        region,
			OwnerID:       ownerID,
		}
	}

	t.Run("WhenRemoteBvNilAndSDEReturnsNil_EarlyReturn", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetAccount", mock.Anything, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", mock.Anything, externalUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		getBackupVaultFromSDEByExternalUUID = func(ctx context.Context, extUUID, rgn, acct string) (*datamodel.BackupVault, error) {
			assert.Equal(tt, externalUUID, extUUID)
			return nil, nil
		}

		o := &GCPOrchestrator{storage: mockStorage}
		result, err := o.DeleteBackupVaultInternal(ctx, newParams())

		assert.NoError(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenRemoteBvNilAndSDEReturnsError_PropagatesError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		sdeErr := fmt.Errorf("sde lookup failed")
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetAccount", mock.Anything, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", mock.Anything, externalUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		getBackupVaultFromSDEByExternalUUID = func(ctx context.Context, extUUID, rgn, acct string) (*datamodel.BackupVault, error) {
			return nil, sdeErr
		}

		o := &GCPOrchestrator{storage: mockStorage}
		_, err := o.DeleteBackupVaultInternal(ctx, newParams())

		require.Error(tt, err)
		assert.Equal(tt, sdeErr, err)
	})

	t.Run("WhenRemoteBvFoundInSDE_DeleteSDEFails_PropagatesError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		sdeVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: sdeVaultUUID}}
		sdeDeleteErr := fmt.Errorf("sde delete failed")
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetAccount", mock.Anything, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", mock.Anything, externalUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		getBackupVaultFromSDEByExternalUUID = func(ctx context.Context, extUUID, rgn, acct string) (*datamodel.BackupVault, error) {
			return sdeVault, nil
		}
		deleteBackupVaultInSDE = func(ctx context.Context, rgn, acct, bvID string) error {
			assert.Equal(tt, sdeVaultUUID, bvID)
			return sdeDeleteErr
		}

		o := &GCPOrchestrator{storage: mockStorage}
		_, err := o.DeleteBackupVaultInternal(ctx, newParams())

		require.Error(tt, err)
		assert.Equal(tt, sdeDeleteErr, err)
	})

	t.Run("WhenRemoteBvFoundInSDE_DeleteSDESucceeds_DeletesFromVCP", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "http://cvp-host"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		sdeVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: sdeVaultUUID}}
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetAccount", mock.Anything, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", mock.Anything, externalUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))
		mockStorage.On("DeleteBackupVaultInVCP", mock.Anything, sdeVaultUUID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		getBackupVaultFromSDEByExternalUUID = func(ctx context.Context, extUUID, rgn, acct string) (*datamodel.BackupVault, error) {
			return sdeVault, nil
		}
		deleteBackupVaultInSDE = func(ctx context.Context, rgn, acct, bvID string) error {
			return nil
		}

		o := &GCPOrchestrator{storage: mockStorage}
		result, err := o.DeleteBackupVaultInternal(ctx, newParams())

		assert.NoError(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenRemoteBvAlreadyFoundInVCP_DeleteSDESucceeds_DeletesFromVCP", func(tt *testing.T) {
		vcpVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: sdeVaultUUID}}
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetAccount", mock.Anything, ownerID).Return(account, nil)
		mockStorage.On("GetBackupVaultByExternalUUIDAndOwnerID", mock.Anything, externalUUID, account.ID).
			Return(vcpVault, nil)
		mockStorage.On("DeleteBackupVaultInVCP", mock.Anything, sdeVaultUUID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))

		deleteBackupVaultInSDE = func(ctx context.Context, rgn, acct, bvID string) error {
			assert.Equal(tt, sdeVaultUUID, bvID)
			return nil
		}

		o := &GCPOrchestrator{storage: mockStorage}
		result, err := o.DeleteBackupVaultInternal(ctx, newParams())

		assert.NoError(tt, err)
		assert.Empty(tt, result)
		mockStorage.AssertExpectations(tt)
	})
}

// ---- Section 4: _deleteBackupVault – validation guards (lines 219-256) ----

func TestDeleteBackupVault_ValidationGuards(t *testing.T) {
	const (
		ownerID      = "proj-123"
		vaultUUID    = "bv-uuid"
		vaultName    = "my-vault"
		backupRegion = "us-west1"
		sourceRegion = "us-central1"
	)

	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 10, UUID: ownerID}}
	params := &commonparams.BackupVaultParams{
		BackupVaultID: vaultUUID,
		Name:          vaultName,
		OwnerID:       ownerID,
		Region:        sourceRegion,
	}

	origGetOrCreate := getOrCreateAccount
	defer func() { getOrCreateAccount = origGetOrCreate }()
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	t.Run("WhenVaultIsUpdating_ReturnsUserInputValidationErr", func(tt *testing.T) {
		bkRegion := backupRegion
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   datamodel.LifeCycleStateUpdating,
			BackupRegionName: &bkRegion,
		}
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "transition state")
	})

	t.Run("WhenVaultIsDeleting_ReturnsUserInputValidationErr", func(tt *testing.T) {
		bkRegion := backupRegion
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   datamodel.LifeCycleStateDeleting,
			BackupRegionName: &bkRegion,
		}
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "transition state")
	})

	t.Run("WhenCrossRegionAndDeletingFromBackupRegion_ReturnsUserInputValidationErr", func(tt *testing.T) {
		bkRegion := sourceRegion // params.Region == backupRegion → deletion from destination
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   "ACTIVE",
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: &bkRegion,
		}
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "destination region")
	})

	t.Run("WhenVaultHasBackups_ReturnsUserInputValidationErr", func(tt *testing.T) {
		bkRegion := backupRegion
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   "ACTIVE",
			BackupVaultType:  "STANDARD",
			BackupRegionName: &bkRegion,
		}
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", mock.Anything, dbBv.ID).Return(int64(3), nil)

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "has backups")
	})

	t.Run("WhenGetBackupCountReturnsGenericError_PropagatesError", func(tt *testing.T) {
		bkRegion := backupRegion
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   "ACTIVE",
			BackupVaultType:  "STANDARD",
			BackupRegionName: &bkRegion,
		}
		dbErr := fmt.Errorf("db connection error")
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", mock.Anything, dbBv.ID).Return(int64(0), dbErr)

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.Equal(tt, dbErr, err)
	})

	t.Run("WhenVaultHasVolumes_ReturnsUserInputValidationErr", func(tt *testing.T) {
		bkRegion := backupRegion
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   "ACTIVE",
			BackupVaultType:  "STANDARD",
			BackupRegionName: &bkRegion,
		}
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", mock.Anything, dbBv.ID).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", mock.Anything, vaultUUID).Return(int64(2), nil)

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.True(tt, utilErrors.IsUserInputValidationErr(err))
		assert.Contains(tt, err.Error(), "has volumes attached")
	})

	t.Run("WhenGetVolumeCountReturnsGenericError_PropagatesError", func(tt *testing.T) {
		bkRegion := backupRegion
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   "ACTIVE",
			BackupVaultType:  "STANDARD",
			BackupRegionName: &bkRegion,
		}
		dbErr := fmt.Errorf("volume count db error")
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", mock.Anything, dbBv.ID).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", mock.Anything, vaultUUID).Return(int64(0), dbErr)

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.Equal(tt, dbErr, err)
	})
}

// ---- Section 4 (else) + Section 5: minimal vault construction ----

func TestDeleteBackupVault_MinimalVaultConstruction(t *testing.T) {
	const (
		ownerID   = "proj-123"
		vaultUUID = "bv-uuid"
		vaultName = "my-vault"
	)

	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 10, UUID: ownerID}}
	params := &commonparams.BackupVaultParams{
		BackupVaultID: vaultUUID,
		Name:          vaultName,
		OwnerID:       ownerID,
		Region:        "us-central1",
	}

	origGetOrCreate := getOrCreateAccount
	defer func() { getOrCreateAccount = origGetOrCreate }()
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}

	// Section 4 else path (lines 249-256): vault not in VCP → minimal vault is constructed and
	// used as the basis for the job and workflow. We verify this by checking the arguments
	// passed to CreateJob (ResourceName comes from params, matching the constructed vault's Name).
	t.Run("WhenVaultNotInVCP_ConstructsMinimalVaultForJob", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(nil, utilErrors.NewNotFoundErr("backup vault", nil))
		mockStorage.On("CreateJob", mock.Anything, mock.MatchedBy(func(j *datamodel.Job) bool {
			return j.ResourceName == vaultName &&
				j.JobAttributes != nil &&
				j.JobAttributes.ResourceUUID == vaultUUID
		})).Return(nil, fmt.Errorf("stop here"))

		_, _, err := _deleteBackupVault(ctx, mockStorage, nil, params)

		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "stop here")
		mockStorage.AssertExpectations(tt)
	})

	// Section 5 (lines 314-323): dbBv is non-nil after validation but UpdateBackupVaultState
	// returns (nil, nil) — the nil-guard at line 314 re-constructs a minimal vault from params.
	// We verify by checking the dbBv passed to ExecuteWorkflow has the correct fields.
	t.Run("WhenUpdateBackupVaultStateReturnsNil_ReconstructsMinimalVault", func(tt *testing.T) {
		bkRegion := "us-west1"
		dbBv := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{ID: 1, UUID: vaultUUID},
			LifeCycleState:   "ACTIVE",
			BackupVaultType:  "STANDARD",
			BackupRegionName: &bkRegion,
		}
		createdJob := &datamodel.Job{
			BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
			WorkflowID: "workflow-id",
		}

		mockTemporal := workflow_engine_mock.NewMockTemporalTestClient(tt)
		mockStorage := new(database.MockStorage)
		mockStorage.On("GetBackupVaultByUUIDndOwnerID", mock.Anything, vaultUUID, account.ID).
			Return(dbBv, nil)
		mockStorage.On("GetBackupCountByBackupVaultID", mock.Anything, dbBv.ID).Return(int64(0), nil)
		mockStorage.On("GetVolumeCountByBackupVaultID", mock.Anything, vaultUUID).Return(int64(0), nil)
		mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)
		// Initial UpdateBackupVaultState call (DELETING) returns (nil, nil) → triggers section 5.
		mockStorage.On("UpdateBackupVaultState", mock.Anything, mock.Anything,
			datamodel.LifeCycleStateDeleting, datamodel.LifeCycleStateDeletingDetails).
			Return((*datamodel.BackupVault)(nil), nil).Once()
		// Rollback UpdateBackupVaultState (back to ACTIVE) triggered by the deferred rollback.
		mockStorage.On("UpdateBackupVaultState", mock.Anything, mock.Anything,
			"ACTIVE", "").
			Return((*datamodel.BackupVault)(nil), nil).Once()
		// ExecuteWorkflow: ctx, options, workflowFn, params, dbBv — 5 args total.
		// Verify the reconstructed minimal vault (arg 4) has the correct UUID, Name, AccountID.
		mockTemporal.On("ExecuteWorkflow",
			mock.Anything, // ctx
			mock.Anything, // StartWorkflowOptions
			mock.Anything, // workflow function
			mock.Anything, // *BackupVaultParams
			mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
				return bv != nil && bv.UUID == vaultUUID && bv.Name == vaultName && bv.AccountID == account.ID
			}),
		).Return(nil, fmt.Errorf("workflow start failed"))
		// Rollback: job marked as error.
		mockStorage.On("UpdateJob", mock.Anything, createdJob.UUID, string(datamodel.JobsStateERROR), mock.Anything, mock.Anything).
			Return(nil)

		_, _, err := _deleteBackupVault(ctx, mockStorage, mockTemporal, params)

		require.Error(tt, err)
		assert.Contains(tt, err.Error(), "workflow start failed")
		mockStorage.AssertExpectations(tt)
		mockTemporal.AssertExpectations(tt)
	})
}

func TestHydrateBackupVaultCrossRegionNetappUuid(t *testing.T) {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	backupRegionName := "us-east4"
	crossRegionNetappUuid := "source-vault-uuid-1234"
	crossRegionBvName := "source-bv-resource-id"
	backupVault := &datamodel.BackupVault{
		BaseModel:                  datamodel.BaseModel{UUID: "dest-backup-vault-uuid"},
		Name:                       "dest-backup-vault",
		BackupRegionName:           &backupRegionName,
		CrossRegionBackupVaultName: &crossRegionBvName,
	}
	params := &commonparams.BackupVaultParams{
		OwnerID: "owner-uuid",
	}

	t.Run("WhenGenerateCallbackTokenFails_ReturnsError", func(tt *testing.T) {
		expectedErr := errors.New("failed to generate callback token")

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return "", expectedErr
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		err := _hydrateBackupVaultCrossRegionNetappUuid(ctx, backupVault, crossRegionNetappUuid, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenHydrationFails_ReturnsError", func(tt *testing.T) {
		expectedErr := errors.New("failed to hydrate netapp_uuid")
		expectedToken := "test-token"

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return expectedToken, nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		originalHydrate := commonparams.HydrateBackupVaultCrossRegionNetappUuid
		commonparams.HydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, logger log.Logger, backupVaultResourceID, crossRegionUuid, location, projectID, token string) error {
			assert.Equal(tt, crossRegionBvName, backupVaultResourceID)
			assert.Equal(tt, crossRegionNetappUuid, crossRegionUuid)
			assert.Equal(tt, backupRegionName, location)
			assert.Equal(tt, params.OwnerID, projectID)
			assert.Equal(tt, expectedToken, token)
			return expectedErr
		}
		defer func() { commonparams.HydrateBackupVaultCrossRegionNetappUuid = originalHydrate }()

		err := _hydrateBackupVaultCrossRegionNetappUuid(ctx, backupVault, crossRegionNetappUuid, params)
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
	})

	t.Run("WhenHydrationSucceeds_ReturnsNil", func(tt *testing.T) {
		expectedToken := "test-token"

		originalGenerateCallbackToken := auth.GenerateCallbackToken
		auth.GenerateCallbackToken = func(ctx context.Context) (string, error) {
			return expectedToken, nil
		}
		defer func() { auth.GenerateCallbackToken = originalGenerateCallbackToken }()

		originalHydrate := commonparams.HydrateBackupVaultCrossRegionNetappUuid
		commonparams.HydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, logger log.Logger, backupVaultResourceID, crossRegionUuid, location, projectID, token string) error {
			assert.Equal(tt, crossRegionBvName, backupVaultResourceID)
			assert.Equal(tt, crossRegionNetappUuid, crossRegionUuid)
			assert.Equal(tt, backupRegionName, location)
			assert.Equal(tt, params.OwnerID, projectID)
			assert.Equal(tt, expectedToken, token)
			return nil
		}
		defer func() { commonparams.HydrateBackupVaultCrossRegionNetappUuid = originalHydrate }()

		err := _hydrateBackupVaultCrossRegionNetappUuid(ctx, backupVault, crossRegionNetappUuid, params)
		assert.NoError(tt, err)
	})
}

func TestCreateBackupVaultEntryInVCP_CrossRegionNetappUuidHydration(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
		Name:      "test-account",
	}
	backupRegion := "us-east4"
	externalUUID := "source-vault-uuid-5678"

	t.Run("WhenCrossProject_HydratesDestinationUuidAsCrossRegionNetappUuid", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		var capturedCrossRegionUuid string
		var capturedCrossRegionBvName string
		originalHydrateCrossRegion := hydrateBackupVaultCrossRegionNetappUuid
		hydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, backupVault *datamodel.BackupVault, crossRegionUuid string, params *commonparams.BackupVaultParams) error {
			capturedCrossRegionUuid = crossRegionUuid
			capturedCrossRegionBvName = *backupVault.CrossRegionBackupVaultName
			return nil
		}
		defer func() { hydrateBackupVaultCrossRegionNetappUuid = originalHydrateCrossRegion }()

		sourceBvName := "source-bv-name"
		crossRegionName := "source-bv-resource-id"
		bv := &datamodel.BackupVault{Name: sourceBvName, BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}
		destUUID := "dest-bv-uuid-1234"
		created := &datamodel.BackupVault{
			BaseModel:                  datamodel.BaseModel{UUID: destUUID},
			Name:                       sourceBvName,
			BackupRegionName:           &backupRegion,
			ServiceType:                datamodel.ServiceTypeCrossProject,
			CrossRegionBackupVaultName: &crossRegionName,
			ExternalUUID:               &externalUUID,
			AccountID:                  account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == sourceBvName
		})).Return(created, nil)

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, destUUID, capturedCrossRegionUuid, "netapp_uuid should be the destination BV UUID")
		assert.Equal(tt, crossRegionName, capturedCrossRegionBvName, "URL resource ID should be CrossRegionBackupVaultName")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCrossProjectAndNoExternalUUID_StillHydratesWithDestUUID", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		var capturedCrossRegionUuid string
		originalHydrateCrossRegion := hydrateBackupVaultCrossRegionNetappUuid
		hydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, backupVault *datamodel.BackupVault, crossRegionUuid string, params *commonparams.BackupVaultParams) error {
			capturedCrossRegionUuid = crossRegionUuid
			return nil
		}
		defer func() { hydrateBackupVaultCrossRegionNetappUuid = originalHydrateCrossRegion }()

		destUUID := "dest-uuid-no-external"
		crossRegionName := "source-bv-resource-id"
		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}
		created := &datamodel.BackupVault{
			BaseModel:                  datamodel.BaseModel{UUID: destUUID},
			Name:                       "test-bv",
			BackupRegionName:           &backupRegion,
			ServiceType:                datamodel.ServiceTypeCrossProject,
			CrossRegionBackupVaultName: &crossRegionName,
			AccountID:                  account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(created, nil)

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, destUUID, capturedCrossRegionUuid, "should hydrate with destination BV UUID even without ExternalUUID")
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenNonCrossProjectServiceType_SkipsCrossRegionHydration", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		originalGetOrCreateAccount := getOrCreateAccount
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}
		defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		defer func() { hydrationEnabled = originalHydrationEnabled }()

		originalUseVCPRegion := cvp.CVP_HOST
		cvp.CVP_HOST = ""
		defer func() { cvp.CVP_HOST = originalUseVCPRegion }()

		originalHydrateCreated := hydrateCreatedBackupVaults
		hydrateCreatedBackupVaults = func(ctx context.Context, backupVault *datamodel.BackupVault, params *commonparams.BackupVaultParams) error {
			return nil
		}
		defer func() { hydrateCreatedBackupVaults = originalHydrateCreated }()

		hydrateCalled := false
		originalHydrateCrossRegion := hydrateBackupVaultCrossRegionNetappUuid
		hydrateBackupVaultCrossRegionNetappUuid = func(ctx context.Context, backupVault *datamodel.BackupVault, crossRegionUuid string, params *commonparams.BackupVaultParams) error {
			hydrateCalled = true
			return nil
		}
		defer func() { hydrateBackupVaultCrossRegionNetappUuid = originalHydrateCrossRegion }()

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		params := &commonparams.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "created-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
			ServiceType:      datamodel.ServiceTypeGCNV,
			ExternalUUID:     &externalUUID,
			AccountID:        account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(created, nil)

		result, err := orchestrator.CreateBackupVaultEntryInVCP(ctx, bv, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, hydrateCalled)
		mockStorage.AssertExpectations(tt)
	})
}

func TestBackfillBackupVaultInVCP(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 42, UUID: "owner-uuid"},
		Name:      "test-account",
	}
	backupRegion := "us-central1"

	origGetOrCreateAccount := getOrCreateAccount
	t.Cleanup(func() { getOrCreateAccount = origGetOrCreateAccount })

	t.Run("WhenGetOrCreateAccountFails_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		expectedErr := errors.New("failed to get account")
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return nil, expectedErr
		}

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}

		result, err := orchestrator.BackfillBackupVaultInVCP(ctx, bv, "project-number")
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCreateBackupVaultEntryInVCPFails_ReturnsError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		expectedErr := errors.New("create backup vault entry failed")
		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(nil, expectedErr)

		result, err := orchestrator.BackfillBackupVaultInVCP(ctx, bv, "project-number")
		assert.Error(tt, err)
		assert.Equal(tt, expectedErr, err)
		assert.Nil(tt, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("WhenCreateBackupVaultEntryInVCPSucceeds_ReturnsCreatedBackupVaultWithoutHydration", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			return account, nil
		}

		// hydrateCreatedBackupVaults must not be invoked by BackfillBackupVaultInVCP: the vault
		// already exists in CCFE, so a create notification must not be sent. Setting hydrationEnabled
		// and pointing the hydrate hook at a function that fails the test if called proves this.
		originalHydrationEnabled := hydrationEnabled
		hydrationEnabled = true
		tt.Cleanup(func() { hydrationEnabled = originalHydrationEnabled })

		originalHydrateCreatedBackupVaults := hydrateCreatedBackupVaults
		hydrateCreatedBackupVaults = func(ctx context.Context, backupVault *datamodel.BackupVault, params *commonparams.BackupVaultParams) error {
			tt.Fatal("hydrateCreatedBackupVaults must not be called by BackfillBackupVaultInVCP")
			return nil
		}
		tt.Cleanup(func() { hydrateCreatedBackupVaults = originalHydrateCreatedBackupVaults })

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		created := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "created-uuid"},
			Name:             "test-bv",
			BackupRegionName: &backupRegion,
			AccountID:        account.ID,
		}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.MatchedBy(func(in *datamodel.BackupVault) bool {
			return in != nil && in.AccountID == account.ID && in.Name == "test-bv"
		})).Return(created, nil)

		result, err := orchestrator.BackfillBackupVaultInVCP(ctx, bv, "project-number")
		assert.NoError(tt, err)
		assert.Equal(tt, created, result)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("PassesProjectNumberToGetOrCreateAccount", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(tt)
		orchestrator := &GCPOrchestrator{storage: mockStorage}

		var capturedAccountName string
		getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
			capturedAccountName = accountName
			return account, nil
		}

		bv := &datamodel.BackupVault{Name: "test-bv", BackupRegionName: &backupRegion}
		created := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "created-uuid"}, Name: "test-bv", AccountID: account.ID}

		mockStorage.On("CreateBackupVaultEntryInVCP", ctx, mock.Anything).Return(created, nil)

		_, err := orchestrator.BackfillBackupVaultInVCP(ctx, bv, "project-number-123")
		assert.NoError(tt, err)
		assert.Equal(tt, "project-number-123", capturedAccountName)
		mockStorage.AssertExpectations(tt)
	})
}
