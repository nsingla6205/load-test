package backgroundactivities

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/metricsinterface"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/kms_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	utils2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

type mockKmsRotationLocker struct {
	lockFunc   func(context.Context, log.Logger) error
	clientID   string
	renewFunc  func(context.Context) error
	unlockFunc func(context.Context) error
}

func (m *mockKmsRotationLocker) Lock(ctx context.Context, logger log.Logger) error {
	if m.lockFunc != nil {
		return m.lockFunc(ctx, logger)
	}
	return nil
}
func (m *mockKmsRotationLocker) ClientID() string {
	return m.clientID
}
func (m *mockKmsRotationLocker) RenewLock(ctx context.Context) error {
	if m.renewFunc != nil {
		return m.renewFunc(ctx)
	}
	return nil
}
func (m *mockKmsRotationLocker) Unlock(ctx context.Context) error {
	if m.unlockFunc != nil {
		return m.unlockFunc(ctx)
	}
	return nil
}

func TestRotateKmsSAKeyActivity_ListKmsConfigs(t *testing.T) {
	ctx := context.Background()

	t.Run("ListKmsConfigsReturnsKmsConfigsOnSuccess", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		expectedKmsConfigs := []*datamodel.KmsConfig{
			{
				BaseModel: datamodel.BaseModel{UUID: "kms-uuid-1"},
				State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
			},
			{
				BaseModel: datamodel.BaseModel{UUID: "kms-uuid-2"},
				State:     string(gcpserver.KmsConfigV1betaKmsStateREADY),
			},
		}

		// Setup expected conditions for GetMultipleKmsConfigs
		expectedConditions := [][]interface{}{
			{"state in ?", []string{string(gcpserver.KmsConfigV1betaKmsStateINUSE), string(gcpserver.KmsConfigV1betaKmsStateREADY)}},
		}

		mockSE.On("GetMultipleKmsConfigs", ctx, expectedConditions).Return(expectedKmsConfigs, nil)

		result, err := activity.ListKmsConfigs(ctx)

		assert.NoError(tt, err)
		assert.Equal(tt, expectedKmsConfigs, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ListKmsConfigsReturnsErrorWhenDatabaseFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		dbError := errors.New("database connection error")
		expectedConditions := [][]interface{}{
			{"state in ?", []string{string(gcpserver.KmsConfigV1betaKmsStateINUSE), string(gcpserver.KmsConfigV1betaKmsStateREADY)}},
		}
		mockSE.On("GetMultipleKmsConfigs", ctx, expectedConditions).Return(nil, dbError)

		result, err := activity.ListKmsConfigs(ctx)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ListKmsConfigsReturnsEmptyListWhenNoKmsConfigsFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		expectedConditions := [][]interface{}{
			{"state in ?", []string{string(gcpserver.KmsConfigV1betaKmsStateINUSE), string(gcpserver.KmsConfigV1betaKmsStateREADY)}},
		}
		mockSE.On("GetMultipleKmsConfigs", ctx, expectedConditions).Return([]*datamodel.KmsConfig{}, nil)

		result, err := activity.ListKmsConfigs(ctx)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
		mockSE.AssertExpectations(tt)
	})
}

// Removed TestRotateKmsSAKeyActivity_RotateServiceAccountKey - function RotateServiceAccountKey was removed
// All test cases that called RotateServiceAccountKey have been removed
func Test_syncKeyWithOntap(t *testing.T) {
	ctx := context.Background()

	t.Run("SyncKeyWithOntapSuccessful", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "svm-uuid",
			},
			SvmDetails: &datamodel.SvmDetails{
				ExternalKmsConfigUUID: "kms-config-uuid",
			},
		}

		// Valid base64 encoded service account key
		validPrivateKeyData := base64.StdEncoding.EncodeToString([]byte("test-private-key-content"))

		// Mock GetSvmForPoolID
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)

		// Mock GetOntapRestProviderForPool
		originalGetOntapRestProviderForPool := GetOntapRestProviderForPool
		defer func() { GetOntapRestProviderForPool = originalGetOntapRestProviderForPool }()

		mockProvider := vsa.NewMockProvider(tt)
		GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Mock ModifyGcpKms
		mockProvider.On("ModifyGcpKms", "kms-config-uuid", mock.AnythingOfType("*log.Secret")).Return(nil, nil, nil)

		// Mock IsGcpKmsReachable
		mockProvider.On("IsGcpKmsReachable", vsa.GetKmsConfigParams{
			ExternalKmsConfigID: "kms-config-uuid",
		}).Return(true, nil)

		err := _syncKeyWithOntap(ctx, mockSE, validPrivateKeyData, "old-key-data", pool)

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})

	t.Run("SyncKeyWithOntapFailsWhenGetSvmForPoolIDFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}

		validPrivateKeyData := base64.StdEncoding.EncodeToString([]byte("test-private-key-content"))

		svmError := errors.New("failed to get SVM")
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(nil, svmError)

		err := _syncKeyWithOntap(ctx, mockSE, validPrivateKeyData, "old-key-data", pool)

		assert.Error(tt, err)
		assert.Equal(tt, svmError, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SyncKeyWithOntapFailsWhenGetOntapRestProviderFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "svm-uuid",
			},
			SvmDetails: &datamodel.SvmDetails{
				ExternalKmsConfigUUID: "kms-config-uuid",
			},
		}

		validPrivateKeyData := base64.StdEncoding.EncodeToString([]byte("test-private-key-content"))

		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)

		// Mock GetOntapRestProviderForPool to fail
		originalGetOntapRestProviderForPool := GetOntapRestProviderForPool
		defer func() { GetOntapRestProviderForPool = originalGetOntapRestProviderForPool }()

		providerError := errors.New("failed to get ONTAP provider")
		GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return nil, providerError
		}

		err := _syncKeyWithOntap(ctx, mockSE, validPrivateKeyData, "old-key-data", pool)

		assert.Error(tt, err)
		assert.Equal(tt, providerError, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("SyncKeyWithOntapFailsWhenBase64DecodeFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "svm-uuid",
			},
			SvmDetails: &datamodel.SvmDetails{
				ExternalKmsConfigUUID: "kms-config-uuid",
			},
		}

		// Invalid base64 string
		invalidBase64Key := "invalid-base64!@#$"

		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)

		// Mock GetOntapRestProviderForPool
		originalGetOntapRestProviderForPool := GetOntapRestProviderForPool
		defer func() { GetOntapRestProviderForPool = originalGetOntapRestProviderForPool }()

		mockProvider := vsa.NewMockProvider(tt)
		GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		// Even with invalid base64, ModifyGcpKms might be called with the raw string
		mockProvider.On("ModifyGcpKms", "kms-config-uuid", mock.AnythingOfType("*log.Secret")).Return(nil, nil, nil).Maybe()
		mockProvider.On("IsGcpKmsReachable", mock.Anything).Return(true, nil).Maybe()

		err := _syncKeyWithOntap(ctx, mockSE, invalidBase64Key, "old-key-data", pool)

		// The function should return an error for invalid base64
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "illegal base64 data")
		mockSE.AssertExpectations(tt)
	})

	t.Run("SyncKeyWithOntapFailsWhenModifyGcpKmsFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
		}

		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{
				UUID: "svm-uuid",
			},
			SvmDetails: &datamodel.SvmDetails{
				ExternalKmsConfigUUID: "kms-config-uuid",
			},
		}

		validPrivateKeyData := base64.StdEncoding.EncodeToString([]byte("test-private-key-content"))

		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)

		// Mock GetOntapRestProviderForPool
		originalGetOntapRestProviderForPool := GetOntapRestProviderForPool
		defer func() { GetOntapRestProviderForPool = originalGetOntapRestProviderForPool }()

		mockProvider := vsa.NewMockProvider(tt)
		GetOntapRestProviderForPool = func(ctx context.Context, se database.Storage, pool *datamodel.Pool) (vsa.Provider, error) {
			return mockProvider, nil
		}

		modifyError := errors.New("failed to modify GCP KMS")
		mockProvider.On("ModifyGcpKms", "kms-config-uuid", mock.AnythingOfType("*log.Secret")).Return(nil, nil, modifyError)

		err := _syncKeyWithOntap(ctx, mockSE, validPrivateKeyData, "old-key-data", pool)

		assert.Error(tt, err)
		assert.Equal(tt, modifyError, err)
		mockSE.AssertExpectations(tt)
		mockProvider.AssertExpectations(tt)
	})
}

func TestListPoolsByKmsConfigId(t *testing.T) {
	ctx := context.Background()

	t.Run("ListPoolsByKmsConfigIdReturnsPoolViewsOnSuccess", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		kmsConfigId := int64(123)
		expectedPoolViews := []*datamodel.PoolView{
			{Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1", ID: 1},
				Name:      "pool-1",
			},
			},
			{Pool: datamodel.Pool{
				BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2", ID: 2},
				Name:      "pool-2",
			},
			},
		}

		// Setup expected filter conditions
		mockSE.On("ListPools", ctx, mock.MatchedBy(func(filter *utils2.Filter) bool {
			// Verify the filter has the correct condition (kms_config_id = X)
			return len(filter.Conditions) == 1 &&
				filter.Conditions[0].Field == "kms_config_id" &&
				filter.Conditions[0].Op == "=" &&
				filter.Conditions[0].Value == kmsConfigId
		})).Return(expectedPoolViews, nil)

		result, err := ListPoolsByKmsConfigId(ctx, kmsConfigId, mockSE)

		assert.NoError(tt, err)
		assert.Len(tt, result, 2)
		// Verify PoolViews are returned directly
		for i, poolView := range result {
			assert.Equal(tt, expectedPoolViews[i].UUID, poolView.UUID)
			assert.Equal(tt, expectedPoolViews[i].ID, poolView.ID)
		}
		mockSE.AssertExpectations(tt)
	})

	t.Run("ListPoolsByKmsConfigIdReturnsErrorWhenDatabaseFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		kmsConfigId := int64(123)
		dbError := errors.New("database connection error")

		mockSE.On("ListPools", ctx, mock.Anything).Return(nil, dbError)

		result, err := ListPoolsByKmsConfigId(ctx, kmsConfigId, mockSE)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ListPoolsByKmsConfigIdReturnsEmptyListWhenNoPoolsFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		kmsConfigId := int64(123)

		mockSE.On("ListPools", ctx, mock.Anything).Return([]*datamodel.PoolView{}, nil)

		result, err := ListPoolsByKmsConfigId(ctx, kmsConfigId, mockSE)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ListPoolsByKmsConfigIdHandlesNilPoolViews", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		kmsConfigId := int64(123)

		mockSE.On("ListPools", ctx, mock.Anything).Return(nil, nil)

		result, err := ListPoolsByKmsConfigId(ctx, kmsConfigId, mockSE)

		assert.NoError(tt, err)
		assert.Empty(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ListPoolsByKmsConfigIdWithLargeDataset", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		kmsConfigId := int64(123)

		// Create a large dataset of pool views
		var expectedPoolViews []*datamodel.PoolView
		for i := 0; i < 100; i++ {
			expectedPoolViews = append(expectedPoolViews, &datamodel.PoolView{
				Pool: datamodel.Pool{
					BaseModel: datamodel.BaseModel{
						UUID: fmt.Sprintf("pool-uuid-%d", i),
						ID:   int64(i),
					},
					Name: fmt.Sprintf("pool-%d", i),
				},
			})
		}

		mockSE.On("ListPools", ctx, mock.Anything).Return(expectedPoolViews, nil)

		result, err := ListPoolsByKmsConfigId(ctx, kmsConfigId, mockSE)

		assert.NoError(tt, err)
		assert.Len(tt, result, 100)
		// Verify first and last pool view are returned correctly
		assert.Equal(tt, "pool-uuid-0", result[0].UUID)
		assert.Equal(tt, "pool-uuid-99", result[99].UUID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ListPoolsByKmsConfigIdWithDifferentKmsConfigIds", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)

		testCases := []int64{0, -1, 999999999}

		for _, kmsConfigId := range testCases {
			mockSE.On("ListPools", ctx, mock.MatchedBy(func(filter *utils2.Filter) bool {
				return len(filter.Conditions) == 1 && filter.Conditions[0].Value == kmsConfigId
			})).Return([]*datamodel.PoolView{}, nil).Once()

			result, err := ListPoolsByKmsConfigId(ctx, kmsConfigId, mockSE)

			assert.NoError(tt, err)
			assert.Empty(tt, result)
		}

		mockSE.AssertExpectations(tt)
	})
}

// New tests for GetKmsConfigServiceAccount method
func TestRotateKmsSAKeyActivity_GetKmsConfigServiceAccount(t *testing.T) {
	ctx := context.Background()

	t.Run("GetKmsConfigServiceAccount_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)

		activity := &RotateKmsSAKeyActivity{
			SE: mockStorage,
		}

		kmsConfigID := "test-kms-config-uuid"
		serviceAccountID := int64(123)

		// Test data
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{
				ID:   serviceAccountID,
				UUID: "test-sa-uuid",
			},
			Name:                "test-service-account",
			ServiceAccountEmail: "test-sa@test-project.iam.gserviceaccount.com",
			ServiceName:         "cmek",
		}

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: kmsConfigID,
			},
			Name:             "test-kms-config",
			ServiceAccountID: &serviceAccountID,
			ServiceAccount:   serviceAccount, // Preloaded
		}

		// Set up expectations
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(kmsConfig, nil)

		// Execute
		result, err := activity.GetKmsConfigServiceAccount(ctx, kmsConfigID)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, serviceAccount.UUID, result.UUID)
		assert.Equal(tt, serviceAccount.ServiceAccountEmail, result.ServiceAccountEmail)
		assert.Equal(tt, serviceAccount.Name, result.Name)

		// Verify expectations
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetKmsConfigServiceAccount_KmsConfigNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)

		activity := &RotateKmsSAKeyActivity{
			SE: mockStorage,
		}

		kmsConfigID := "non-existent-kms-config"

		// Set up expectations
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(nil, errors.New("KMS config not found"))

		// Execute
		result, err := activity.GetKmsConfigServiceAccount(ctx, kmsConfigID)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, result)

		// Verify expectations
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetKmsConfigServiceAccount_NoServiceAccountID", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)

		activity := &RotateKmsSAKeyActivity{
			SE: mockStorage,
		}

		kmsConfigID := "test-kms-config-uuid"

		// Test data - KMS config without service account ID
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: kmsConfigID,
			},
			Name:             "test-kms-config",
			ServiceAccountID: nil, // No service account ID
		}

		// Set up expectations
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(kmsConfig, nil)

		// Execute
		result, err := activity.GetKmsConfigServiceAccount(ctx, kmsConfigID)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "no service account associated with KMS config")

		// Verify expectations
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetKmsConfigServiceAccount_ServiceAccountNotPreloaded", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)

		activity := &RotateKmsSAKeyActivity{
			SE: mockStorage,
		}

		kmsConfigID := "test-kms-config-uuid"
		serviceAccountID := int64(123)

		// Test data - KMS config with service account ID but not preloaded
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: kmsConfigID,
			},
			Name:             "test-kms-config",
			ServiceAccountID: &serviceAccountID,
			ServiceAccount:   nil, // Not preloaded
		}

		// Set up expectations
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(kmsConfig, nil)

		// Execute
		result, err := activity.GetKmsConfigServiceAccount(ctx, kmsConfigID)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "service account not found for KMS config")

		// Verify expectations
		mockStorage.AssertExpectations(tt)
	})

	t.Run("GetKmsConfigServiceAccount_ZeroServiceAccountID", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)

		activity := &RotateKmsSAKeyActivity{
			SE: mockStorage,
		}

		kmsConfigID := "test-kms-config-uuid"
		serviceAccountID := int64(0) // Zero ID

		// Test data - KMS config with zero service account ID
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{
				UUID: kmsConfigID,
			},
			Name:             "test-kms-config",
			ServiceAccountID: &serviceAccountID,
		}

		// Set up expectations
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(kmsConfig, nil)

		// Execute
		result, err := activity.GetKmsConfigServiceAccount(ctx, kmsConfigID)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "no service account associated with KMS config")
	})
}

// Tests for GetKmsConfig method
func TestRotateKmsSAKeyActivity_GetKmsConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("GetKmsConfig_Success", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockStorage}

		kmsConfigID := "test-kms-config-id"
		expectedKmsConfig := &datamodel.KmsConfig{
			BaseModel:    datamodel.BaseModel{UUID: kmsConfigID},
			State:        string(gcpserver.KmsConfigV1betaKmsStateINUSE),
			KeyProjectID: "test-project",
		}

		// Mock the database call
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(expectedKmsConfig, nil)

		// Execute
		result, err := activity.GetKmsConfig(ctx, kmsConfigID)

		// Assert
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, expectedKmsConfig.UUID, result.UUID)
		assert.Equal(tt, expectedKmsConfig.State, result.State)
		assert.Equal(tt, expectedKmsConfig.KeyProjectID, result.KeyProjectID)
	})

	t.Run("GetKmsConfig_KmsConfigNotFound", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockStorage}

		kmsConfigID := "non-existent-kms-config-id"
		dbError := errors.New("KMS config not found")

		// Mock the database call to return error
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(nil, dbError)

		// Execute
		result, err := activity.GetKmsConfig(ctx, kmsConfigID)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "An internal error occurred")
	})

	t.Run("GetKmsConfig_DatabaseError", func(tt *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockStorage}

		kmsConfigID := "test-kms-config-id"
		dbError := errors.New("database connection failed")

		// Mock the database call to return error
		mockStorage.EXPECT().GetKmsConfigByUUID(ctx, kmsConfigID).Return(nil, dbError)

		// Execute
		result, err := activity.GetKmsConfig(ctx, kmsConfigID)

		// Assert
		assert.Error(tt, err)
		assert.Nil(tt, result)
		assert.Contains(tt, err.Error(), "An internal error occurred")
	})
}

// Tests for _extractKeyID function
func Test_extractKeyID(t *testing.T) {
	t.Run("extractKeyID_Success", func(tt *testing.T) {
		// Create a valid service account key JSON
		keyData := map[string]interface{}{
			"type":                        "service_account",
			"project_id":                  "test-project",
			"private_key_id":              "test-key-id-123",
			"private_key":                 "-----BEGIN PRIVATE KEY-----\ntest-key\n-----END PRIVATE KEY-----\n",
			"client_email":                "test@test-project.iam.gserviceaccount.com",
			"client_id":                   "123456789",
			"auth_uri":                    "https://accounts.google.com/o/oauth2/auth",
			"token_uri":                   "https://oauth2.googleapis.com/token",
			"auth_provider_x509_cert_url": "https://www.googleapis.com/oauth2/v1/certs",
		}

		// Convert to JSON
		keyBytes, err := json.Marshal(keyData)
		assert.NoError(tt, err)

		// Base64 encode
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Encrypt the key (mock the encryption)
		originalEncryptPassword := utils.EncryptPassword
		defer func() { utils.EncryptPassword = originalEncryptPassword }()
		utils.EncryptPassword = func(password log.Secret) (*string, error) {
			encryptedValue := string(password)
			return &encryptedValue, nil
		}

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			decryptedValue := encodedKey
			return &decryptedValue, nil
		}

		// Execute
		result, err := _extractKeyID("encrypted-service-account-key")

		// Assert
		assert.NoError(tt, err)
		assert.Equal(tt, "test-key-id-123", result)
	})

	t.Run("extractKeyID_DecryptionFailed", func(tt *testing.T) {
		// Mock decrypt password to fail
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			return nil, errors.New("decryption failed")
		}

		// Execute
		result, err := _extractKeyID("invalid-encrypted-key")

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "failed to decrypt service account key")
	})

	t.Run("extractKeyID_InvalidBase64", func(tt *testing.T) {
		// Mock decrypt password to return invalid base64
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			invalidBase64 := "invalid-base64-data!!!"
			return &invalidBase64, nil
		}

		// Execute
		result, err := _extractKeyID("encrypted-service-account-key")

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
	})

	t.Run("extractKeyID_InvalidJSON", func(tt *testing.T) {
		// Mock decrypt password to return invalid JSON
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			invalidJSON := base64.StdEncoding.EncodeToString([]byte("invalid-json"))
			return &invalidJSON, nil
		}

		// Execute
		result, err := _extractKeyID("encrypted-service-account-key")

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "failed to unmarshal credentials")
	})

	t.Run("extractKeyID_MissingPrivateKeyID", func(tt *testing.T) {
		// Create service account key JSON without private_key_id
		keyData := map[string]interface{}{
			"type":         "service_account",
			"project_id":   "test-project",
			"client_email": "test@test-project.iam.gserviceaccount.com",
			// Missing private_key_id
		}

		// Convert to JSON and base64 encode
		keyBytes, err := json.Marshal(keyData)
		assert.NoError(tt, err)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			return &encodedKey, nil
		}

		// Execute
		result, err := _extractKeyID("encrypted-service-account-key")

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "key not found or not a string")
	})

	t.Run("extractKeyID_PrivateKeyIDNotString", func(tt *testing.T) {
		// Create service account key JSON with private_key_id as non-string
		keyData := map[string]interface{}{
			"type":           "service_account",
			"project_id":     "test-project",
			"private_key_id": 12345, // Not a string
			"client_email":   "test@test-project.iam.gserviceaccount.com",
		}

		// Convert to JSON and base64 encode
		keyBytes, err := json.Marshal(keyData)
		assert.NoError(tt, err)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			return &encodedKey, nil
		}

		// Execute
		result, err := _extractKeyID("encrypted-service-account-key")

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "key not found or not a string")
	})
}

// Tests for _extractKeyIDFromRawBase64 function
func Test_extractKeyIDFromRawBase64(t *testing.T) {
	t.Run("extractKeyIDFromRawBase64_Success", func(tt *testing.T) {
		// Create a valid service account key JSON
		keyData := map[string]interface{}{
			"type":           "service_account",
			"project_id":     "test-project",
			"private_key_id": "test-key-id-456",
			"private_key":    "-----BEGIN PRIVATE KEY-----\ntest-key\n-----END PRIVATE KEY-----\n",
			"client_email":   "test@test-project.iam.gserviceaccount.com",
		}

		// Convert to JSON and base64 encode
		keyBytes, err := json.Marshal(keyData)
		assert.NoError(tt, err)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Execute
		result, err := _extractKeyIDFromRawBase64(encodedKey)

		// Assert
		assert.NoError(tt, err)
		assert.Equal(tt, "test-key-id-456", result)
	})

	t.Run("extractKeyIDFromRawBase64_InvalidBase64", func(tt *testing.T) {
		invalidBase64 := "invalid-base64-data!!!"

		// Execute
		result, err := _extractKeyIDFromRawBase64(invalidBase64)

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "failed to decode base64 data")
	})

	t.Run("extractKeyIDFromRawBase64_InvalidJSON", func(tt *testing.T) {
		invalidJSON := base64.StdEncoding.EncodeToString([]byte("invalid-json"))

		// Execute
		result, err := _extractKeyIDFromRawBase64(invalidJSON)

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "failed to unmarshal credentials")
	})

	t.Run("extractKeyIDFromRawBase64_MissingPrivateKeyID", func(tt *testing.T) {
		keyData := map[string]interface{}{
			"type":         "service_account",
			"project_id":   "test-project",
			"client_email": "test@test-project.iam.gserviceaccount.com",
		}

		keyBytes, err := json.Marshal(keyData)
		assert.NoError(tt, err)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Execute
		result, err := _extractKeyIDFromRawBase64(encodedKey)

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "private_key_id not found or not a string")
	})

	t.Run("extractKeyIDFromRawBase64_PrivateKeyIDNotString", func(tt *testing.T) {
		keyData := map[string]interface{}{
			"type":           "service_account",
			"project_id":     "test-project",
			"private_key_id": 12345, // Not a string
			"client_email":   "test@test-project.iam.gserviceaccount.com",
		}

		keyBytes, err := json.Marshal(keyData)
		assert.NoError(tt, err)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Execute
		result, err := _extractKeyIDFromRawBase64(encodedKey)

		// Assert
		assert.Error(tt, err)
		assert.Empty(tt, result)
		assert.Contains(tt, err.Error(), "private_key_id not found or not a string")
	})
}

// Tests for ValidateKeyRotationRequiredActivity
func TestRotateKmsSAKeyActivity_ValidateKeyRotationRequiredActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("ValidateKeyRotationRequiredActivity_ServiceAccountNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(nil, errors.New("not found"))

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_KmsConfigNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(nil, errors.New("not found"))

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_InvalidKmsConfigState", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateKEYCHECKPENDING),
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, result.RotationRequired)
		assert.Contains(tt, result.Reason, "not in valid state")
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_KmsConfigInMigratingState", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     datamodel.LifeCycleStateMigrating,
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, result.RotationRequired)
		assert.Contains(tt, result.Reason, "KMS config is not in valid state for rotation (current state: MIGRATING)")
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_KmsConfigInErrorState_RotationNotAllowed", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		keyData := map[string]interface{}{
			"private_key_id": "current-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(log.Secret) (*string, error) {
			return &encodedKey, nil
		}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountPasswordLocation: "encrypted-key",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateERROR),
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, result.RotationRequired)
		assert.Equal(tt, "", result.CurrentKeyID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_FailedToExtractKeyID", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountPasswordLocation: "invalid-key-data",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
		}

		// Mock extractKeyID to fail
		originalExtractKeyID := extractKeyID
		defer func() { extractKeyID = originalExtractKeyID }()
		extractKeyID = func(string) (string, error) {
			return "", errors.New("failed to extract key ID")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_MultipleActiveKeys", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		keyData := map[string]interface{}{
			"private_key_id": "current-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(log.Secret) (*string, error) {
			return &encodedKey, nil
		}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountPasswordLocation: "encrypted-key",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "key-1", IsActive: true},
					{KeyID: "key-2", IsActive: true},
				},
			},
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.RotationRequired)
		assert.Contains(tt, result.Reason, "multiple active keys")
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_NonPrimaryKeyExists", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		keyData := map[string]interface{}{
			"private_key_id": "current-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(log.Secret) (*string, error) {
			return &encodedKey, nil
		}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountPasswordLocation: "encrypted-key",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "key-1", IsPrimary: true, IsActive: true},
					{KeyID: "key-2", IsPrimary: false, IsActive: true},
				},
			},
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.RotationRequired)
		// With 2 active keys, it hits the "multiple active keys" check first
		assert.Contains(tt, result.Reason, "multiple active keys")
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_SingleNonPrimaryKeyExists", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		keyData := map[string]interface{}{
			"private_key_id": "current-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(log.Secret) (*string, error) {
			return &encodedKey, nil
		}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountPasswordLocation: "encrypted-key",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "key-1", IsPrimary: true, IsActive: false}, // Primary but inactive
					{KeyID: "key-2", IsPrimary: false, IsActive: true}, // Non-primary but active
				},
			},
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.RotationRequired)
		// With only 1 active key (non-primary), it should hit the "not yet primary" check
		assert.Contains(tt, result.Reason, "not yet primary")
		mockSE.AssertExpectations(tt)
	})

	t.Run("ValidateKeyRotationRequiredActivity_RotationRequired", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		keyData := map[string]interface{}{
			"private_key_id": "current-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		// Mock decrypt password
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(log.Secret) (*string, error) {
			return &encodedKey, nil
		}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountPasswordLocation: "encrypted-key",
		}
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-config-uuid"},
			State:     string(gcpserver.KmsConfigV1betaKmsStateINUSE),
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("GetKmsConfigByUUID", ctx, "kms-config-uuid").Return(kmsConfig, nil)

		result, err := activity.ValidateKeyRotationRequiredActivity(ctx, "sa-uuid", "kms-config-uuid")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.RotationRequired)
		assert.Equal(tt, "current-key-id", result.CurrentKeyID)
		mockSE.AssertExpectations(tt)
	})
}

// Tests for CreateServiceAccountKeyActivity
func TestRotateKmsSAKeyActivity_CreateServiceAccountKeyActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateServiceAccountKeyActivity_ServiceAccountNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(nil, errors.New("not found"))

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "current-key-id")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_NewKeyAlreadyExists", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "current-key-id", IsPrimary: true, IsActive: true},
					{KeyID: "new-key-id", IsPrimary: false, IsActive: true},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "current-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.KeyExists)
		assert.Equal(tt, "new-key-id", result.NewKeyID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_FailedToGetGcpService", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
		}

		// Mock getGcpService to fail
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "current-key-id")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_FailedToCreateKey", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
		}

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock gcpServiceCreateServiceAccountKey to fail
		originalCreateKey := gcpServiceCreateServiceAccountKey
		defer func() { gcpServiceCreateServiceAccountKey = originalCreateKey }()
		gcpServiceCreateServiceAccountKey = func(hyperscaler2.Services, context.Context, string) (*hyperscaler.ServiceAccountKey, error) {
			return nil, errors.New("failed to create key")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "current-key-id")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_FailedToExtractKeyID", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
		}

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock gcpServiceCreateServiceAccountKey
		originalCreateKey := gcpServiceCreateServiceAccountKey
		defer func() { gcpServiceCreateServiceAccountKey = originalCreateKey }()
		gcpServiceCreateServiceAccountKey = func(hyperscaler2.Services, context.Context, string) (*hyperscaler.ServiceAccountKey, error) {
			return &hyperscaler.ServiceAccountKey{
				Name:           "projects/test/serviceAccounts/test@test.iam.gserviceaccount.com/keys/key-id",
				PrivateKeyData: "invalid-base64",
			}, nil
		}

		// Mock extractKeyIDFromRawBase64 to fail
		originalExtractKeyIDFromRawBase64 := extractKeyIDFromRawBase64
		defer func() { extractKeyIDFromRawBase64 = originalExtractKeyIDFromRawBase64 }()
		extractKeyIDFromRawBase64 = func(string) (string, error) {
			return "", errors.New("failed to extract key ID")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "current-key-id")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_FailedToEncrypt", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
		}

		keyData := map[string]interface{}{
			"private_key_id": "new-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock gcpServiceCreateServiceAccountKey
		originalCreateKey := gcpServiceCreateServiceAccountKey
		defer func() { gcpServiceCreateServiceAccountKey = originalCreateKey }()
		gcpServiceCreateServiceAccountKey = func(hyperscaler2.Services, context.Context, string) (*hyperscaler.ServiceAccountKey, error) {
			return &hyperscaler.ServiceAccountKey{
				Name:           "projects/test/serviceAccounts/test@test.iam.gserviceaccount.com/keys/key-id",
				PrivateKeyData: encodedKey,
			}, nil
		}

		// Mock extractKeyIDFromRawBase64
		originalExtractKeyIDFromRawBase64 := extractKeyIDFromRawBase64
		defer func() { extractKeyIDFromRawBase64 = originalExtractKeyIDFromRawBase64 }()
		extractKeyIDFromRawBase64 = func(string) (string, error) {
			return "new-key-id", nil
		}

		// Mock EncryptPassword to fail
		originalEncryptPassword := utils.EncryptPassword
		defer func() { utils.EncryptPassword = originalEncryptPassword }()
		utils.EncryptPassword = func(log.Secret) (*string, error) {
			return nil, errors.New("encryption failed")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "current-key-id")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_Success", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
		}

		keyData := map[string]interface{}{
			"private_key_id": "new-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock gcpServiceCreateServiceAccountKey
		originalCreateKey := gcpServiceCreateServiceAccountKey
		defer func() { gcpServiceCreateServiceAccountKey = originalCreateKey }()
		gcpServiceCreateServiceAccountKey = func(hyperscaler2.Services, context.Context, string) (*hyperscaler.ServiceAccountKey, error) {
			return &hyperscaler.ServiceAccountKey{
				Name:           "projects/test/serviceAccounts/test@test.iam.gserviceaccount.com/keys/key-id",
				PrivateKeyData: encodedKey,
			}, nil
		}

		// Mock extractKeyIDFromRawBase64
		originalExtractKeyIDFromRawBase64 := extractKeyIDFromRawBase64
		defer func() { extractKeyIDFromRawBase64 = originalExtractKeyIDFromRawBase64 }()
		extractKeyIDFromRawBase64 = func(string) (string, error) {
			return "new-key-id", nil
		}

		// Mock EncryptPassword
		originalEncryptPassword := utils.EncryptPassword
		defer func() { utils.EncryptPassword = originalEncryptPassword }()
		utils.EncryptPassword = func(log.Secret) (*string, error) {
			encrypted := "encrypted-key-data"
			return &encrypted, nil
		}

		// Mock AccessCryptoKeyAndEncryptData
		originalAccessCryptoKey := kms_activities.AccessCryptoKeyAndEncryptData
		defer func() { kms_activities.AccessCryptoKeyAndEncryptData = originalAccessCryptoKey }()
		kms_activities.AccessCryptoKeyAndEncryptData = func(context.Context, *datamodel.KmsConfig, string, time.Duration, time.Duration) error {
			return nil
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "current-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, result.KeyExists)
		assert.Equal(tt, "new-key-id", result.NewKeyID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_TooManyKeysPendingDeletion_WithMetricsEmitter", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{
			SE:             mockSE,
			MetricsEmitter: &metricsinterface.NoOpKmsMetricsEmitter{},
		}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "primary-key", IsPrimary: true, IsActive: true},
					{KeyID: "delete-key-1", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-2", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-3", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-4", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-5", IsPrimary: false, IsActive: false},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "primary-key")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		customErr, ok := err.(*vsaerrors.CustomError)
		assert.True(tt, ok, "error should be of type *CustomError")
		assert.True(tt, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_TooManyTotalKeys_WithMetricsEmitter", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{
			SE:             mockSE,
			MetricsEmitter: &metricsinterface.NoOpKmsMetricsEmitter{},
		}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		// 8 total keys, but only 4 pending deletion (below maxPendingDeletionKeysAllowed=5)
		// so we skip the first check and hit the total keys check (>= maxTotalKeysBeforeRotation=8)
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "primary-key", IsPrimary: true, IsActive: true},
					{KeyID: "other-primary-1", IsPrimary: true, IsActive: true},
					{KeyID: "other-primary-2", IsPrimary: true, IsActive: true},
					{KeyID: "other-primary-3", IsPrimary: true, IsActive: true},
					{KeyID: "delete-key-1", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-2", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-3", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-4", IsPrimary: false, IsActive: false},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "primary-key")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		customErr, ok := err.(*vsaerrors.CustomError)
		assert.True(tt, ok, "error should be of type *CustomError")
		assert.True(tt, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_TooManyKeysPendingDeletion", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		// Create a service account with 5 keys marked for deletion (IsPrimary=false, IsActive=false)
		// This simulates DeleteOldSAKeyFromGCPActivity failing repeatedly
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "primary-key", IsPrimary: true, IsActive: true},
					{KeyID: "delete-key-1", IsPrimary: false, IsActive: false}, // marked for deletion
					{KeyID: "delete-key-2", IsPrimary: false, IsActive: false}, // marked for deletion
					{KeyID: "delete-key-3", IsPrimary: false, IsActive: false}, // marked for deletion
					{KeyID: "delete-key-4", IsPrimary: false, IsActive: false}, // marked for deletion
					{KeyID: "delete-key-5", IsPrimary: false, IsActive: false}, // marked for deletion (5th)
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "primary-key")

		assert.Error(tt, err)
		assert.Nil(tt, result)
		// Check that error is of type CustomError with correct tracking ID
		customErr, ok := err.(*vsaerrors.CustomError)
		assert.True(tt, ok, "error should be of type *CustomError")
		assert.True(tt, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_TooManyTotalKeys", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		// Create a service account with 8 total keys (approaching GCP's 10-key limit)
		// All keys except primary are marked for deletion to avoid triggering idempotency check
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "primary-key", IsPrimary: true, IsActive: true},
					{KeyID: "delete-key-1", IsPrimary: false, IsActive: false},  // marked for deletion
					{KeyID: "delete-key-2", IsPrimary: false, IsActive: false},  // marked for deletion
					{KeyID: "delete-key-3", IsPrimary: false, IsActive: false},  // marked for deletion
					{KeyID: "delete-key-4", IsPrimary: false, IsActive: false},  // marked for deletion - this is 5th
					{KeyID: "old-primary-1", IsPrimary: false, IsActive: false}, // old primary marked for deletion
					{KeyID: "old-primary-2", IsPrimary: false, IsActive: false}, // old primary marked for deletion
					{KeyID: "old-primary-3", IsPrimary: false, IsActive: false}, // 8th key total
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "primary-key")

		// With 8 total keys and 7 pending deletion (>= 5), this should fail
		// The pending deletion check comes first (7 >= 5)
		assert.Error(tt, err)
		assert.Nil(tt, result)
		// Check that error is of type CustomError with correct tracking ID
		customErr, ok := err.(*vsaerrors.CustomError)
		assert.True(tt, ok, "error should be of type *CustomError")
		assert.True(tt, customErr.IsError(vsaerrors.ErrResourceStateConflictError))
		mockSE.AssertExpectations(tt)
	})

	t.Run("CreateServiceAccountKeyActivity_KeyLimitCheckPassesWithFewKeys", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "kms-uuid"}}

		// Create a service account with acceptable number of keys (4 pending deletion, 5 total)
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "primary-key", IsPrimary: true, IsActive: true},
					{KeyID: "delete-key-1", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-2", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-3", IsPrimary: false, IsActive: false},
					{KeyID: "delete-key-4", IsPrimary: false, IsActive: false}, // 4 pending deletion, below limit of 5
				},
			},
		}

		keyData := map[string]interface{}{
			"private_key_id": "new-key-id",
		}
		keyBytes, _ := json.Marshal(keyData)
		encodedKey := base64.StdEncoding.EncodeToString(keyBytes)

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock gcpServiceCreateServiceAccountKey
		originalCreateKey := gcpServiceCreateServiceAccountKey
		defer func() { gcpServiceCreateServiceAccountKey = originalCreateKey }()
		gcpServiceCreateServiceAccountKey = func(hyperscaler2.Services, context.Context, string) (*hyperscaler.ServiceAccountKey, error) {
			return &hyperscaler.ServiceAccountKey{
				Name:           "projects/test/serviceAccounts/test@test.iam.gserviceaccount.com/keys/key-id",
				PrivateKeyData: encodedKey,
			}, nil
		}

		// Mock extractKeyIDFromRawBase64
		originalExtractKeyIDFromRawBase64 := extractKeyIDFromRawBase64
		defer func() { extractKeyIDFromRawBase64 = originalExtractKeyIDFromRawBase64 }()
		extractKeyIDFromRawBase64 = func(string) (string, error) {
			return "new-key-id", nil
		}

		// Mock EncryptPassword
		originalEncryptPassword := utils.EncryptPassword
		defer func() { utils.EncryptPassword = originalEncryptPassword }()
		utils.EncryptPassword = func(log.Secret) (*string, error) {
			encrypted := "encrypted-key-data"
			return &encrypted, nil
		}

		// Mock AccessCryptoKeyAndEncryptData
		originalAccessCryptoKey := kms_activities.AccessCryptoKeyAndEncryptData
		defer func() { kms_activities.AccessCryptoKeyAndEncryptData = originalAccessCryptoKey }()
		kms_activities.AccessCryptoKeyAndEncryptData = func(context.Context, *datamodel.KmsConfig, string, time.Duration, time.Duration) error {
			return nil
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		result, err := activity.CreateServiceAccountKeyActivity(ctx, "sa-uuid", kmsConfig, "primary-key")

		// Should succeed - key limit check passes
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, result.KeyExists)
		assert.Equal(tt, "new-key-id", result.NewKeyID)
		mockSE.AssertExpectations(tt)
	})
}

// Tests for StoreNewKeyInDBActivity
func TestRotateKmsSAKeyActivity_StoreNewKeyInDBActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("StoreNewKeyInDBActivity_ServiceAccountNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(nil, errors.New("not found"))

		err := activity.StoreNewKeyInDBActivity(ctx, "sa-uuid", "new-key-id", "new-key-data", "current-key-id")

		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("StoreNewKeyInDBActivity_KeyAlreadyExists", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "new-key-id", KeyData: "new-key-data"},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		err := activity.StoreNewKeyInDBActivity(ctx, "sa-uuid", "new-key-id", "new-key-data", "current-key-id")

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("StoreNewKeyInDBActivity_AddOldKeyFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid", CreatedAt: time.Now()},
			ServiceAccountPasswordLocation: "old-key-data",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("AddKeyToServiceAccount", ctx, "sa-uuid", mock.Anything).Return(errors.New("failed to add key"))

		err := activity.StoreNewKeyInDBActivity(ctx, "sa-uuid", "new-key-id", "new-key-data", "current-key-id")

		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("StoreNewKeyInDBActivity_AddNewKeyFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid", CreatedAt: time.Now()},
			ServiceAccountPasswordLocation: "old-key-data",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "current-key-id", IsPrimary: true},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("AddKeyToServiceAccount", ctx, "sa-uuid", mock.Anything).Return(errors.New("failed to add key"))

		err := activity.StoreNewKeyInDBActivity(ctx, "sa-uuid", "new-key-id", "new-key-data", "current-key-id")

		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("StoreNewKeyInDBActivity_Success", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid", CreatedAt: time.Now()},
			ServiceAccountPasswordLocation: "old-key-data",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "current-key-id", IsPrimary: true},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("AddKeyToServiceAccount", ctx, "sa-uuid", mock.Anything).Return(nil)
		mockSE.On("UpdateServiceAccountPasswordLocation", ctx, "sa-uuid", "new-key-data").Return(nil)

		err := activity.StoreNewKeyInDBActivity(ctx, "sa-uuid", "new-key-id", "new-key-data", "current-key-id")

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("StoreNewKeyInDBActivity_InitializesNilAttributes", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:                      datamodel.BaseModel{UUID: "sa-uuid", CreatedAt: time.Now()},
			ServiceAccountPasswordLocation: "old-key-data",
			ServiceAccountAttributes:       nil,
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("AddKeyToServiceAccount", ctx, "sa-uuid", mock.Anything).Return(nil).Times(2)
		mockSE.On("UpdateServiceAccountPasswordLocation", ctx, "sa-uuid", "new-key-data").Return(nil)

		err := activity.StoreNewKeyInDBActivity(ctx, "sa-uuid", "new-key-id", "new-key-data", "current-key-id")

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})
}

// Tests for BatchPoolsForKeyRotationActivity
func TestRotateKmsSAKeyActivity_BatchPoolsForKeyRotationActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("BatchPoolsForKeyRotationActivity_Success", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		poolViews := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}, Name: "pool-1", State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"}, Name: "pool-2", State: datamodel.LifeCycleStateInUse}},
		}

		// Mock listPoolsByKmsConfigId
		originalListPools := listPoolsByKmsConfigId
		defer func() { listPoolsByKmsConfigId = originalListPools }()
		listPoolsByKmsConfigId = func(context.Context, int64, database.Storage) ([]*datamodel.PoolView, error) {
			return poolViews, nil
		}

		result, err := activity.BatchPoolsForKeyRotationActivity(ctx, 123)

		assert.NoError(tt, err)
		assert.Len(tt, result, 2)
		assert.Equal(tt, "pool-uuid-1", result[0].UUID)
		assert.Equal(tt, "pool-uuid-2", result[1].UUID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("BatchPoolsForKeyRotationActivity_FiltersDeletingPools", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		poolViews := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}, Name: "pool-1", State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"}, Name: "pool-2", State: datamodel.LifeCycleStateDeleting}}, // Should be filtered out
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"}, Name: "pool-3", State: datamodel.LifeCycleStateInUse}},
		}

		originalListPools := listPoolsByKmsConfigId
		defer func() { listPoolsByKmsConfigId = originalListPools }()
		listPoolsByKmsConfigId = func(context.Context, int64, database.Storage) ([]*datamodel.PoolView, error) {
			return poolViews, nil
		}

		result, err := activity.BatchPoolsForKeyRotationActivity(ctx, 123)

		assert.NoError(tt, err)
		assert.Len(tt, result, 2)
		assert.Equal(tt, "pool-uuid-1", result[0].UUID)
		assert.Equal(tt, "pool-uuid-3", result[1].UUID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("BatchPoolsForKeyRotationActivity_FiltersErrorPoolsWithNoVolumes", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		poolViews := []*datamodel.PoolView{
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-1"}, Name: "pool-1", State: datamodel.LifeCycleStateREADY}},
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-2"}, Name: "pool-2", State: datamodel.LifeCycleStateError}, VolumeCount: 0}, // Should be filtered out (no volumes)
			{Pool: datamodel.Pool{BaseModel: datamodel.BaseModel{UUID: "pool-uuid-3"}, Name: "pool-3", State: datamodel.LifeCycleStateError}, VolumeCount: 5}, // Should be included (has volumes)
		}

		originalListPools := listPoolsByKmsConfigId
		defer func() { listPoolsByKmsConfigId = originalListPools }()
		listPoolsByKmsConfigId = func(context.Context, int64, database.Storage) ([]*datamodel.PoolView, error) {
			return poolViews, nil
		}

		result, err := activity.BatchPoolsForKeyRotationActivity(ctx, 123)

		assert.NoError(tt, err)
		assert.Len(tt, result, 2)
		assert.Equal(tt, "pool-uuid-1", result[0].UUID)
		assert.Equal(tt, "pool-uuid-3", result[1].UUID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("BatchPoolsForKeyRotationActivity_FailedToListPools", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Mock listPoolsByKmsConfigId to fail
		originalListPools := listPoolsByKmsConfigId
		defer func() { listPoolsByKmsConfigId = originalListPools }()
		listPoolsByKmsConfigId = func(context.Context, int64, database.Storage) ([]*datamodel.PoolView, error) {
			return nil, errors.New("failed to list pools")
		}

		result, err := activity.BatchPoolsForKeyRotationActivity(ctx, 123)

		assert.Error(tt, err)
		assert.Nil(tt, result)
		mockSE.AssertExpectations(tt)
	})
}

// Tests for MigratePoolToNewKeyActivity
func TestRotateKmsSAKeyActivity_MigratePoolToNewKeyActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("MigratePoolToNewKeyActivity_PoolNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(nil, errors.New("not found"))

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err) // Returns error in result, not as error
		assert.NotNil(tt, result)
		assert.False(tt, result.Success)
		assert.Contains(tt, result.Error, "Failed to get pool")
		mockSE.AssertExpectations(tt)
	})

	t.Run("MigratePoolToNewKeyActivity_SvmNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(pool, nil)
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(nil, errors.New("not found"))

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, result.Success)
		assert.Contains(tt, result.Error, "Failed to get SVM")
		mockSE.AssertExpectations(tt)
	})

	t.Run("MigratePoolToNewKeyActivity_AlreadyUsingNewKey", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-uuid"},
			SvmDetails: &datamodel.SvmDetails{
				CurrentKmsKeyID: "new-key-id",
			},
		}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(pool, nil)
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.Success)
		assert.Equal(tt, "svm-uuid", result.SvmUUID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("MigratePoolToNewKeyActivity_InitializesNilSvmDetails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
		}
		svm := &datamodel.Svm{
			BaseModel:  datamodel.BaseModel{UUID: "svm-uuid"},
			SvmDetails: nil,
		}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(pool, nil)
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)

		// Mock DecryptPassword to return the test value (treating encrypted value as if it decrypts to same value)
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			decrypted := string(encryptedPassword)
			return &decrypted, nil
		}

		// Mock syncKeyWithOntap
		originalSyncKey := syncKeyWithOntap
		defer func() { syncKeyWithOntap = originalSyncKey }()
		syncKeyWithOntap = func(context.Context, database.Storage, string, string, *datamodel.Pool) error {
			return nil
		}

		mockSE.On("UpdateSvmCurrentKmsKeyID", ctx, "svm-uuid", "new-key-id").Return(nil)

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.Success)
		mockSE.AssertExpectations(tt)
	})

	t.Run("MigratePoolToNewKeyActivity_SyncKeyFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			Name:      "test-pool",
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-uuid"},
			SvmDetails: &datamodel.SvmDetails{
				CurrentKmsKeyID: "old-key-id",
			},
		}

		// Mock DecryptPassword to return the test value (treating encrypted value as if it decrypts to same value)
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			decrypted := string(encryptedPassword)
			return &decrypted, nil
		}

		// Mock syncKeyWithOntap to fail
		originalSyncKey := syncKeyWithOntap
		defer func() { syncKeyWithOntap = originalSyncKey }()
		syncKeyWithOntap = func(context.Context, database.Storage, string, string, *datamodel.Pool) error {
			return errors.New("sync failed")
		}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(pool, nil)
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.False(tt, result.Success)
		assert.Contains(tt, result.Error, "sync failed")
		mockSE.AssertExpectations(tt)
	})

	t.Run("MigratePoolToNewKeyActivity_UpdateSvmKeyIDFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			Name:      "test-pool",
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-uuid"},
			SvmDetails: &datamodel.SvmDetails{
				CurrentKmsKeyID: "old-key-id",
			},
		}

		// Mock DecryptPassword to return the test value (treating encrypted value as if it decrypts to same value)
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			decrypted := string(encryptedPassword)
			return &decrypted, nil
		}

		// Mock syncKeyWithOntap to succeed
		originalSyncKey := syncKeyWithOntap
		defer func() { syncKeyWithOntap = originalSyncKey }()
		syncKeyWithOntap = func(context.Context, database.Storage, string, string, *datamodel.Pool) error {
			return nil
		}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(pool, nil)
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)
		mockSE.On("UpdateSvmCurrentKmsKeyID", ctx, "svm-uuid", "new-key-id").Return(errors.New("update failed"))

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err) // Non-fatal error
		assert.NotNil(tt, result)
		assert.True(tt, result.Success) // Migration succeeded, tracking failed
		mockSE.AssertExpectations(tt)
	})

	t.Run("MigratePoolToNewKeyActivity_Success", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"},
			Name:      "test-pool",
		}
		svm := &datamodel.Svm{
			BaseModel: datamodel.BaseModel{UUID: "svm-uuid"},
			SvmDetails: &datamodel.SvmDetails{
				CurrentKmsKeyID: "old-key-id",
			},
		}

		// Mock DecryptPassword to return the test value (treating encrypted value as if it decrypts to same value)
		originalDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = originalDecryptPassword }()
		utils.DecryptPassword = func(encryptedPassword log.Secret) (*string, error) {
			decrypted := string(encryptedPassword)
			return &decrypted, nil
		}

		// Mock syncKeyWithOntap to succeed
		originalSyncKey := syncKeyWithOntap
		defer func() { syncKeyWithOntap = originalSyncKey }()
		syncKeyWithOntap = func(context.Context, database.Storage, string, string, *datamodel.Pool) error {
			return nil
		}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(pool, nil)
		mockSE.On("GetSvmForPoolID", ctx, pool.ID).Return(svm, nil)
		mockSE.On("UpdateSvmCurrentKmsKeyID", ctx, "svm-uuid", "new-key-id").Return(nil)

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.Success)
		assert.Equal(tt, "svm-uuid", result.SvmUUID)
		mockSE.AssertExpectations(tt)
	})

	t.Run("MigratePoolToNewKeyActivity_PoolInErrorState", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		pool := &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "pool-uuid"}, State: datamodel.LifeCycleStateError,
		}

		mockSE.On("GetPoolByUUID", ctx, "pool-uuid").Return(pool, nil)

		result, err := activity.MigratePoolToNewKeyActivity(ctx, "pool-uuid", "new-key-data", "old-key-data", "new-key-id")

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.True(tt, result.Success)
		mockSE.AssertExpectations(tt)
	})
}

// Tests for CompleteKeyRotationActivity
func TestRotateKmsSAKeyActivity_CompleteKeyRotationActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("CompleteKeyRotationActivity_ServiceAccountNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(nil, errors.New("not found"))

		err := activity.CompleteKeyRotationActivity(ctx, "sa-uuid", "kms-config-uuid", "new-key-id", "old-key-id")

		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CompleteKeyRotationActivity_NewKeyNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		err := activity.CompleteKeyRotationActivity(ctx, "sa-uuid", "kms-config-uuid", "new-key-id", "old-key-id")

		assert.Error(tt, err)
		// Check the unwrapped error message
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Contains(tt, customErr.OriginalErr.Error(), "not found in keys array")
		} else {
			assert.Contains(tt, err.Error(), "not found in keys array")
		}
		mockSE.AssertExpectations(tt)
	})

	t.Run("CompleteKeyRotationActivity_AlreadyPrimary", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "new-key-id", IsPrimary: true},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		err := activity.CompleteKeyRotationActivity(ctx, "sa-uuid", "kms-config-uuid", "new-key-id", "old-key-id")

		assert.NoError(tt, err) // Already completed - idempotent
		mockSE.AssertExpectations(tt)
	})

	t.Run("CompleteKeyRotationActivity_MarkKeyForDeletionFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "new-key-id", IsPrimary: false},
					{KeyID: "old-key-id", IsPrimary: true},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("SetPrimaryKeyForServiceAccount", ctx, "sa-uuid", "new-key-id").Return(nil)
		mockSE.On("MarkKeyForDeletion", ctx, "sa-uuid", "old-key-id").Return(errors.New("key not found"))

		// Should continue even if mark for deletion fails (non-fatal)
		err := activity.CompleteKeyRotationActivity(ctx, "sa-uuid", "kms-config-uuid", "new-key-id", "old-key-id")

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CompleteKeyRotationActivity_SetPrimaryKeyFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "new-key-id", IsPrimary: false},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("SetPrimaryKeyForServiceAccount", ctx, "sa-uuid", "new-key-id").Return(errors.New("failed to set primary"))

		err := activity.CompleteKeyRotationActivity(ctx, "sa-uuid", "kms-config-uuid", "new-key-id", "old-key-id")

		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("CompleteKeyRotationActivity_Success", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		serviceAccount := &datamodel.ServiceAccount{
			BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "new-key-id", IsPrimary: false},
					{KeyID: "old-key-id", IsPrimary: true},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		mockSE.On("SetPrimaryKeyForServiceAccount", ctx, "sa-uuid", "new-key-id").Return(nil)
		mockSE.On("MarkKeyForDeletion", ctx, "sa-uuid", "old-key-id").Return(nil)

		err := activity.CompleteKeyRotationActivity(ctx, "sa-uuid", "kms-config-uuid", "new-key-id", "old-key-id")

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})
}

// Tests for DeleteOldSAKeyFromGCPActivity
func TestRotateKmsSAKeyActivity_DeleteOldSAKeyFromGCPActivity(t *testing.T) {
	ctx := context.Background()

	t.Run("DeleteOldSAKeyFromGCPActivity_ServiceAccountNotFound", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(nil, errors.New("not found"))

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteOldSAKeyFromGCPActivity_NoKeysMarkedForDeletion", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Service account has no keys marked for deletion
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "active-key-id", IsPrimary: true, IsActive: true},
				},
			},
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.NoError(tt, err) // No keys to delete = success
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteOldSAKeyFromGCPActivity_InvalidEmailFormat", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Service account with invalid email format (no valid global project ID can be extracted)
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "invalid-email-format",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "old-key-id", IsPrimary: false, IsActive: false}, // Marked for deletion
				},
			},
		}

		// Mock getGcpService - it will be called before extractGlobalProjectIDFromEmail
		mockGcpService := &google.GcpServices{}
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.Error(tt, err)
		// Check the unwrapped error message
		var customErr *vsaerrors.CustomError
		if vsaerrors.As(err, &customErr) {
			assert.Contains(tt, customErr.OriginalErr.Error(), "could not extract global project ID")
		} else {
			assert.Contains(tt, err.Error(), "could not extract global project ID")
		}
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteOldSAKeyFromGCPActivity_FailedToGetGcpService", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Service account with key marked for deletion (IsPrimary=false, IsActive=false)
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test-project.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "old-key-id", IsPrimary: false, IsActive: false}, // Marked for deletion
				},
			},
		}

		// Mock getGcpService to fail
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("failed to get GCP service")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.Error(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteOldSAKeyFromGCPActivity_KeyAlreadyDeletedFromGCP", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Service account with key marked for deletion
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test-project.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "old-key-id", IsPrimary: false, IsActive: false}, // Marked for deletion
				},
			},
		}

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock deleteServiceAccountKeyWithRetry to return 404 (key already deleted from GCP)
		originalDeleteKey := deleteServiceAccountKeyWithRetry
		defer func() { deleteServiceAccountKeyWithRetry = originalDeleteKey }()
		deleteServiceAccountKeyWithRetry = func(ctx context.Context, c hyperscaler2.Services, keyName string) error {
			return errors.New("404 not found")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		// Even if GCP returns 404, we should still remove from JSON
		mockSE.On("RemoveKeyFromServiceAccount", ctx, "sa-uuid", "old-key-id").Return(nil)

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.NoError(tt, err) // 404 is treated as success (idempotent)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteOldSAKeyFromGCPActivity_GCPDeleteFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Service account with key marked for deletion
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test-project.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "old-key-id", IsPrimary: false, IsActive: false}, // Marked for deletion
				},
			},
		}

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock deleteServiceAccountKeyWithRetry to return non-404 error
		originalDeleteKey := deleteServiceAccountKeyWithRetry
		defer func() { deleteServiceAccountKeyWithRetry = originalDeleteKey }()
		deleteServiceAccountKeyWithRetry = func(ctx context.Context, c hyperscaler2.Services, keyName string) error {
			return errors.New("403 permission denied")
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		// RemoveKeyFromServiceAccount should NOT be called if GCP delete fails

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.Error(tt, err) // GCP delete failed, key remains marked for deletion for retry
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteOldSAKeyFromGCPActivity_Success", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Service account with key marked for deletion
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test-project.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "old-key-id", IsPrimary: false, IsActive: false}, // Marked for deletion
				},
			},
		}

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock deleteServiceAccountKeyWithRetry to succeed
		originalDeleteKey := deleteServiceAccountKeyWithRetry
		defer func() { deleteServiceAccountKeyWithRetry = originalDeleteKey }()
		deleteServiceAccountKeyWithRetry = func(ctx context.Context, c hyperscaler2.Services, keyName string) error {
			return nil
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		// After successful GCP deletion, key should be removed from JSON
		mockSE.On("RemoveKeyFromServiceAccount", ctx, "sa-uuid", "old-key-id").Return(nil)

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})

	t.Run("DeleteOldSAKeyFromGCPActivity_MultipleKeysMarkedForDeletion", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &RotateKmsSAKeyActivity{SE: mockSE}

		// Service account with multiple keys marked for deletion
		serviceAccount := &datamodel.ServiceAccount{
			BaseModel:           datamodel.BaseModel{UUID: "sa-uuid"},
			ServiceAccountEmail: "test@test-project.iam.gserviceaccount.com",
			ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
				Keys: []datamodel.ServiceAccountKey{
					{KeyID: "primary-key-id", IsPrimary: true, IsActive: true}, // Active key - should NOT be deleted
					{KeyID: "old-key-1", IsPrimary: false, IsActive: false},    // Marked for deletion
					{KeyID: "old-key-2", IsPrimary: false, IsActive: false},    // Marked for deletion
				},
			},
		}

		mockGcpService := &google.GcpServices{}

		// Mock getGcpService
		originalGetGcpService := getGcpService
		defer func() { getGcpService = originalGetGcpService }()
		getGcpService = func(context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}

		// Mock deleteServiceAccountKeyWithRetry to succeed for all keys
		originalDeleteKey := deleteServiceAccountKeyWithRetry
		defer func() { deleteServiceAccountKeyWithRetry = originalDeleteKey }()
		deleteServiceAccountKeyWithRetry = func(ctx context.Context, c hyperscaler2.Services, keyName string) error {
			return nil
		}

		mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(serviceAccount, nil)
		// Both marked keys should be removed after GCP deletion
		mockSE.On("RemoveKeyFromServiceAccount", ctx, "sa-uuid", "old-key-1").Return(nil)
		mockSE.On("RemoveKeyFromServiceAccount", ctx, "sa-uuid", "old-key-2").Return(nil)

		err := activity.DeleteOldSAKeyFromGCPActivity(ctx, "sa-uuid", "kms-config-uuid", "old-key-id")

		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})
}

// Tests for isProjectNumber helper function
func TestIsProjectNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "all digits",
			input:    "123456789012",
			expected: true,
		},
		{
			name:     "single digit",
			input:    "1",
			expected: true,
		},
		{
			name:     "alphanumeric",
			input:    "my-project-123",
			expected: false,
		},
		{
			name:     "letters only",
			input:    "myproject",
			expected: false,
		},
		{
			name:     "empty string",
			input:    "",
			expected: false,
		},
		{
			name:     "digits with dash",
			input:    "123-456",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isProjectNumber(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Tests for extractGlobalProjectIDFromEmail helper function
func TestExtractGlobalProjectIDFromEmail(t *testing.T) {
	// Create a mock GCP service (not used for non-numeric project IDs)
	mockGcpService := &google.GcpServices{}

	t.Run("valid service account email with project name", func(t *testing.T) {
		result, err := extractGlobalProjectIDFromEmail("my-service-account@my-project-id.iam.gserviceaccount.com", mockGcpService)
		assert.NoError(t, err)
		assert.Equal(t, "my-project-id", result)
	})

	t.Run("valid email with dashes and numbers in project name", func(t *testing.T) {
		result, err := extractGlobalProjectIDFromEmail("sa-123@test-project-456.iam.gserviceaccount.com", mockGcpService)
		assert.NoError(t, err)
		assert.Equal(t, "test-project-456", result)
	})

	t.Run("email without @ symbol", func(t *testing.T) {
		_, err := extractGlobalProjectIDFromEmail("invalid-email.iam.gserviceaccount.com", mockGcpService)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing @ symbol")
	})

	t.Run("email without correct suffix", func(t *testing.T) {
		_, err := extractGlobalProjectIDFromEmail("test@myproject.example.com", mockGcpService)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing .iam.gserviceaccount.com suffix")
	})

	t.Run("empty email", func(t *testing.T) {
		_, err := extractGlobalProjectIDFromEmail("", mockGcpService)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing @ symbol")
	})

	t.Run("email with only @ and suffix", func(t *testing.T) {
		_, err := extractGlobalProjectIDFromEmail("@.iam.gserviceaccount.com", mockGcpService)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "empty project ID")
	})
}

func TestKmsRotationLockActivities_LocalSetup(t *testing.T) {
	ctx := context.Background()
	activity := &RotateKmsSAKeyActivity{SE: database.NewMockStorage(t)}

	t.Run("AcquireKmsRotationLockActivity returns empty clientID and nil when LOCAL_SETUP is true", func(t *testing.T) {
		t.Setenv("LOCAL_SETUP", "true")
		defer t.Setenv("LOCAL_SETUP", "")

		clientID, err := activity.AcquireKmsRotationLockActivity(ctx, "kms-config-uuid")

		assert.NoError(t, err)
		assert.Equal(t, "", clientID)
	})

	t.Run("RenewKmsRotationLockActivity returns nil when LOCAL_SETUP is true", func(t *testing.T) {
		t.Setenv("LOCAL_SETUP", "true")
		defer t.Setenv("LOCAL_SETUP", "")

		err := activity.RenewKmsRotationLockActivity(ctx, "kms-config-uuid", "any-client-id")

		assert.NoError(t, err)
	})

	t.Run("ReleaseKmsRotationLockActivity returns nil when LOCAL_SETUP is true", func(t *testing.T) {
		t.Setenv("LOCAL_SETUP", "true")
		defer t.Setenv("LOCAL_SETUP", "")

		err := activity.ReleaseKmsRotationLockActivity(ctx, "kms-config-uuid", "any-client-id")

		assert.NoError(t, err)
	})
}

func TestKmsRotationLockActivities_NonLocalSetup_CreateLockerFails(t *testing.T) {
	ctx := context.Background()
	activity := &RotateKmsSAKeyActivity{SE: database.NewMockStorage(t)}
	t.Setenv("LOCAL_SETUP", "false")

	t.Run("AcquireKmsRotationLockActivity returns internal error when locker creation fails", func(t *testing.T) {
		clientID, err := activity.AcquireKmsRotationLockActivity(ctx, "kms-config-uuid")
		assert.Error(t, err)
		assert.Equal(t, "", clientID)
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(t, customErr)
		assert.Equal(t, vsaerrors.ErrInternalServerError, customErr.TrackingID)
	})

	t.Run("RenewKmsRotationLockActivity returns internal error when locker creation fails", func(t *testing.T) {
		err := activity.RenewKmsRotationLockActivity(ctx, "kms-config-uuid", "lock-client-id")
		assert.Error(t, err)
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(t, customErr)
		assert.Equal(t, vsaerrors.ErrInternalServerError, customErr.TrackingID)
	})

	t.Run("ReleaseKmsRotationLockActivity returns internal error when locker creation fails", func(t *testing.T) {
		err := activity.ReleaseKmsRotationLockActivity(ctx, "kms-config-uuid", "lock-client-id")
		assert.Error(t, err)
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(t, customErr)
		assert.Equal(t, vsaerrors.ErrInternalServerError, customErr.TrackingID)
	})
}

func TestRotateKmsSAKeyActivity_StoreNewKeyInDBActivity_UpdatePasswordLocationFails(t *testing.T) {
	ctx := context.Background()
	mockSE := database.NewMockStorage(t)
	activity := &RotateKmsSAKeyActivity{SE: mockSE}

	sa := &datamodel.ServiceAccount{
		BaseModel: datamodel.BaseModel{UUID: "sa-uuid"},
		ServiceAccountAttributes: &datamodel.ServiceAccountAttributes{
			Keys: []datamodel.ServiceAccountKey{},
		},
	}
	mockSE.On("GetServiceAccountWithKeys", ctx, "sa-uuid").Return(sa, nil)
	mockSE.On("AddKeyToServiceAccount", ctx, "sa-uuid", mock.Anything).Return(nil).Twice()
	mockSE.On("UpdateServiceAccountPasswordLocation", ctx, "sa-uuid", "new-key-data").Return(errors.New("update failed")).Once()

	err := activity.StoreNewKeyInDBActivity(ctx, "sa-uuid", "new-key-id", "new-key-data", "")
	assert.Error(t, err)
	customErr := vsaerrors.ExtractCustomError(err)
	assert.NotNil(t, customErr)
	assert.Equal(t, vsaerrors.ErrDatabaseDataUpdateError, customErr.TrackingID)
}

func TestKmsRotationLockActivities_WithMockLocker_Branches(t *testing.T) {
	ctx := context.Background()
	activity := &RotateKmsSAKeyActivity{SE: database.NewMockStorage(t)}
	t.Setenv("LOCAL_SETUP", "false")

	origNewKmsRotationLocker := newKmsRotationLocker
	origMaxAcquire := kmsRotationLockMaxAcquireWait
	origRetryWait := kmsRotationLockRetryWait
	defer func() {
		newKmsRotationLocker = origNewKmsRotationLocker
		kmsRotationLockMaxAcquireWait = origMaxAcquire
		kmsRotationLockRetryWait = origRetryWait
	}()

	t.Run("Acquire success returns client id", func(t *testing.T) {
		newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
			return &mockKmsRotationLocker{
				clientID: "client-1",
				lockFunc: func(context.Context, log.Logger) error { return nil },
			}, nil
		}
		clientID, err := activity.AcquireKmsRotationLockActivity(ctx, "kms-config-uuid")
		assert.NoError(t, err)
		assert.Equal(t, "client-1", clientID)
	})

	t.Run("Acquire returns conflict for non-ErrAlreadyHeld lock error", func(t *testing.T) {
		newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
			return &mockKmsRotationLocker{
				lockFunc: func(context.Context, log.Logger) error { return errors.New("boom") },
			}, nil
		}
		_, err := activity.AcquireKmsRotationLockActivity(ctx, "kms-config-uuid")
		assert.Error(t, err)
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(t, customErr)
		assert.Equal(t, vsaerrors.ErrResourceStateConflictError, customErr.TrackingID)
	})

	t.Run("Acquire times out when lock always already held", func(t *testing.T) {
		kmsRotationLockMaxAcquireWait = 25 * time.Millisecond
		kmsRotationLockRetryWait = 5 * time.Millisecond
		newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
			return &mockKmsRotationLocker{
				lockFunc: func(context.Context, log.Logger) error { return utils.ErrAlreadyHeld },
			}, nil
		}
		_, err := activity.AcquireKmsRotationLockActivity(ctx, "kms-config-uuid")
		assert.Error(t, err)
		customErr := vsaerrors.ExtractCustomError(err)
		assert.NotNil(t, customErr)
		assert.Equal(t, vsaerrors.ErrResourceStateConflictError, customErr.TrackingID)
	})

	t.Run("Acquire returns context cancellation", func(t *testing.T) {
		kmsRotationLockMaxAcquireWait = 100 * time.Millisecond
		kmsRotationLockRetryWait = 50 * time.Millisecond
		newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
			return &mockKmsRotationLocker{
				lockFunc: func(context.Context, log.Logger) error { return utils.ErrAlreadyHeld },
			}, nil
		}
		cctx, cancel := context.WithCancel(ctx)
		cancel()
		_, err := activity.AcquireKmsRotationLockActivity(cctx, "kms-config-uuid")
		assert.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("Renew returns wrapped error when renew lock fails", func(t *testing.T) {
		newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
			return &mockKmsRotationLocker{
				renewFunc: func(context.Context) error { return errors.New("renew failed") },
			}, nil
		}
		err := activity.RenewKmsRotationLockActivity(ctx, "kms-config-uuid", "client-1")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "renew KMS rotation lock")
	})

	t.Run("Release returns nil when unlock fails", func(t *testing.T) {
		newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
			return &mockKmsRotationLocker{
				unlockFunc: func(context.Context) error { return errors.New("unlock failed") },
			}, nil
		}
		err := activity.ReleaseKmsRotationLockActivity(ctx, "kms-config-uuid", "client-1")
		assert.NoError(t, err)
	})

	t.Run("Release success", func(t *testing.T) {
		newKmsRotationLocker = func(ctx context.Context, name string, options ...utils.Option) (kmsRotationLocker, error) {
			return &mockKmsRotationLocker{}, nil
		}
		err := activity.ReleaseKmsRotationLockActivity(ctx, "kms-config-uuid", "client-1")
		assert.NoError(t, err)
	})
}
