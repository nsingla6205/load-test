package backgroundactivities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	hyperscalerModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestSyncZiZsActivity_GetAllBackupVaults(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.Background()

		expectedVaults := []*datamodel.BackupVault{
			{
				BaseModel: datamodel.BaseModel{UUID: "vault-1"},
				Name:      "test-vault-1",
			},
			{
				BaseModel: datamodel.BaseModel{UUID: "vault-2"},
				Name:      "test-vault-2",
			},
		}

		expectedConditions := [][]interface{}{
			{"life_cycle_state = ?", datamodel.LifeCycleStateREADY},
		}

		mockSE.On("GetMultipleBackupVaults", ctx, expectedConditions).Return(expectedVaults, nil).Once()

		result, err := activity.GetAllBackupVaults(ctx)

		assert.NoError(t, err)
		assert.Equal(t, expectedVaults, result)
		mockSE.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.Background()

		expectedConditions := [][]interface{}{
			{"life_cycle_state = ?", datamodel.LifeCycleStateREADY},
		}

		mockSE.On("GetMultipleBackupVaults", ctx, expectedConditions).Return(nil, errors.New("database error")).Once()

		result, err := activity.GetAllBackupVaults(ctx)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "database error")
		mockSE.AssertExpectations(t)
	})
}

func TestSyncZiZsActivity_SyncBucketDetails(t *testing.T) {
	// Store original function
	originalGetCloudService := activities.GetCloudService
	defer func() {
		activities.GetCloudService = originalGetCloudService
	}()

	t.Run("Success", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		bucketDetails := &datamodel.BucketDetails{
			BucketName:          "test-bucket",
			ServiceAccountName:  "test-sa",
			VendorSubnetID:      "subnet-123",
			TenantProjectNumber: "123456789",
		}

		// Mock cloud service
		mockCloudService := hyperscaler.NewMockServices(t)
		activities.GetCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockCloudService, nil
		}

		expectedCloudBucketDetails := &hyperscalerModels.BucketDetails{
			Name:         "test-bucket",
			SatisfiesPzi: true,
			SatisfiesPzs: false,
		}

		mockCloudService.On("GetBucket", ctx, "test-bucket").Return(expectedCloudBucketDetails, nil).Once()

		result, err := activity.SyncBucketDetails(ctx, bucketDetails)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, bucketDetails.BucketName, result.BucketName)
		assert.Equal(t, bucketDetails.ServiceAccountName, result.ServiceAccountName)
		assert.Equal(t, bucketDetails.VendorSubnetID, result.VendorSubnetID)
		assert.Equal(t, bucketDetails.TenantProjectNumber, result.TenantProjectNumber)
		assert.Equal(t, expectedCloudBucketDetails.SatisfiesPzi, result.SatisfiesPzi)
		assert.Equal(t, expectedCloudBucketDetails.SatisfiesPzs, result.SatisfiesPzs)
		mockCloudService.AssertExpectations(t)
	})

	t.Run("EmptyTenantProjectNumber", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		bucketDetails := &datamodel.BucketDetails{
			BucketName:          "test-bucket",
			ServiceAccountName:  "test-sa",
			VendorSubnetID:      "subnet-123",
			TenantProjectNumber: "", // Empty project number
		}

		result, err := activity.SyncBucketDetails(ctx, bucketDetails)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "tenant project number is required but not found in bucket details")
	})

	t.Run("GetCloudServiceError", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		bucketDetails := &datamodel.BucketDetails{
			BucketName:          "test-bucket",
			ServiceAccountName:  "test-sa",
			VendorSubnetID:      "subnet-123",
			TenantProjectNumber: "123456789",
		}

		// Mock GetCloudService to return error
		activities.GetCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return nil, errors.New("failed to get cloud service")
		}

		result, err := activity.SyncBucketDetails(ctx, bucketDetails)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to get cloud service")
	})

	t.Run("GetBucketError", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		bucketDetails := &datamodel.BucketDetails{
			BucketName:          "test-bucket",
			ServiceAccountName:  "test-sa",
			VendorSubnetID:      "subnet-123",
			TenantProjectNumber: "123456789",
		}

		// Mock cloud service
		mockCloudService := hyperscaler.NewMockServices(t)
		activities.GetCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockCloudService, nil
		}

		mockCloudService.On("GetBucket", ctx, "test-bucket").Return(nil, errors.New("failed to get bucket from cloud")).Once()

		result, err := activity.SyncBucketDetails(ctx, bucketDetails)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to get bucket from cloud")
		mockCloudService.AssertExpectations(t)
	})
}

func TestSyncZiZsActivity_UpdateBackupVault(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault",
		}

		mockSE.On("UpdateBackupVaultBucketDetails", ctx, backupVault).Return(nil).Once()

		err := activity.UpdateBackupVault(ctx, backupVault)

		assert.NoError(t, err)
		mockSE.AssertExpectations(t)
	})

	t.Run("NilBackupVault", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		err := activity.UpdateBackupVault(ctx, nil)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "backup vault parameter is required")
		mockSE.AssertExpectations(t)
	})

	t.Run("DatabaseError", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault",
		}

		mockSE.On("UpdateBackupVaultBucketDetails", ctx, backupVault).Return(errors.New("database update error")).Once()

		err := activity.UpdateBackupVault(ctx, backupVault)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update backup vault vault-1")
		assert.Contains(t, err.Error(), "database update error")
		mockSE.AssertExpectations(t)
	})
}

// Test the complete flow with mocked dependencies
func TestSyncZiZsActivity_CompleteFlow(t *testing.T) {
	t.Run("GetAllBackupVaultsSuccess", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.Background()

		// Test data
		backupVaults := []*datamodel.BackupVault{
			{
				BaseModel: datamodel.BaseModel{UUID: "vault-1"},
				Name:      "test-vault-1",
				BucketDetails: datamodel.BucketDetailsArray{
					{
						BucketName:          "bucket-1",
						ServiceAccountName:  "sa-1",
						VendorSubnetID:      "subnet-1",
						TenantProjectNumber: "123456789",
					},
				},
			},
		}

		// Mock GetAllBackupVaults
		expectedConditions := [][]interface{}{
			{"life_cycle_state = ?", datamodel.LifeCycleStateREADY},
		}
		mockSE.On("GetMultipleBackupVaults", ctx, expectedConditions).Return(backupVaults, nil).Once()

		// Test GetAllBackupVaults
		vaults, err := activity.GetAllBackupVaults(ctx)
		assert.NoError(t, err)
		assert.Len(t, vaults, 1)
		assert.Equal(t, "vault-1", vaults[0].UUID)

		mockSE.AssertExpectations(t)
	})

	t.Run("UpdateBackupVaultSuccess", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		// Test data
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-1",
					ServiceAccountName:  "sa-1",
					VendorSubnetID:      "subnet-1",
					TenantProjectNumber: "123456789",
					SatisfiesPzi:        true,
					SatisfiesPzs:        true,
				},
			},
		}

		// Test UpdateBackupVault
		mockSE.On("UpdateBackupVaultBucketDetails", ctx, backupVault).Return(nil).Once()

		err := activity.UpdateBackupVault(ctx, backupVault)
		assert.NoError(t, err)

		mockSE.AssertExpectations(t)
	})
}

// Test edge cases and validation
func TestSyncZiZsActivity_EdgeCases(t *testing.T) {
	t.Run("EmptyBackupVaultsList", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.Background()

		expectedConditions := [][]interface{}{
			{"life_cycle_state = ?", datamodel.LifeCycleStateREADY},
		}

		mockSE.On("GetMultipleBackupVaults", ctx, expectedConditions).Return([]*datamodel.BackupVault{}, nil).Once()

		result, err := activity.GetAllBackupVaults(ctx)

		assert.NoError(t, err)
		assert.Empty(t, result)
		mockSE.AssertExpectations(t)
	})

	t.Run("SyncBucketDetailsWithCloudService", func(t *testing.T) {
		// Store original function
		originalGetCloudService := activities.GetCloudService
		defer func() {
			activities.GetCloudService = originalGetCloudService
		}()

		mockSE := database.NewMockStorage(t)
		activity := &SyncBackupZiZsActivity{SE: mockSE}
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, log.NewLogger())

		bucketDetails := &datamodel.BucketDetails{
			BucketName:          "test-bucket",
			ServiceAccountName:  "test-sa",
			VendorSubnetID:      "subnet-123",
			TenantProjectNumber: "123456789",
		}

		// Mock cloud service
		mockCloudService := hyperscaler.NewMockServices(t)
		activities.GetCloudService = func(ctx context.Context) (hyperscaler.Services, error) {
			return mockCloudService, nil
		}

		expectedCloudBucketDetails := &hyperscalerModels.BucketDetails{
			Name:         "test-bucket",
			SatisfiesPzi: true,
			SatisfiesPzs: true,
		}

		mockCloudService.On("GetBucket", ctx, "test-bucket").Return(expectedCloudBucketDetails, nil).Once()

		result, err := activity.SyncBucketDetails(ctx, bucketDetails)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.True(t, result.SatisfiesPzi)
		assert.True(t, result.SatisfiesPzs)
		mockCloudService.AssertExpectations(t)
	})
}
