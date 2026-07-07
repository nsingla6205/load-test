package kms_activities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/testsuite"
)

// Helper function to create string pointer
func stringPtr(s string) *string {
	return &s
}

func TestUpdateKmsConfig_Error(t *testing.T) {
	tests := []struct {
		name        string
		kmsConfig   *datamodel.KmsConfig
		params      *common.UpdateKmsConfigParams
		expectedErr string
	}{
		{
			name: "DatabaseUpdateError",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			params: &common.UpdateKmsConfigParams{
				ResourceID: "updated-resource-id",
			},
			expectedErr: "database update failed",
		},
		{
			name: "DatabaseUpdateErrorWithKeyDetails",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			params: &common.UpdateKmsConfigParams{
				KeyName:         "new-key",
				KeyRing:         "new-keyring",
				KeyRingLocation: "us-west1",
				KeyProjectID:    "new-project",
				ResourceID:      "updated-resource-id",
			},
			expectedErr: "database update failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockStorage := database.NewMockStorage(t)
			ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

			// Mock database update to return error
			mockStorage.On("UpdateKmsConfig", ctx, tt.kmsConfig.UUID, mock.Anything).Return(errors.New(tt.expectedErr))

			// Act
			err := UpdateKmsConfig(mockStorage, ctx, tt.kmsConfig, tt.params)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedErr)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestUpdateKmsConfigState_Success(t *testing.T) {
	tests := []struct {
		name         string
		kmsConfig    *datamodel.KmsConfig
		state        string
		stateDetails string
	}{
		{
			name: "UpdateToCreatedState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			state:        datamodel.LifeCycleStateCreated,
			stateDetails: datamodel.LifeCycleStateCreatedDetails,
		},
		{
			name: "UpdateToUpdatingState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			state:        datamodel.LifeCycleStateUpdating,
			stateDetails: datamodel.LifeCycleStateUpdatingDetails,
		},
		{
			name: "UpdateToErrorState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			state:        datamodel.LifeCycleStateError,
			stateDetails: "An error occurred during processing",
		},
		{
			name: "UpdateToReadyState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			state:        datamodel.LifeCycleStateREADY,
			stateDetails: datamodel.LifeCycleStateReadyDetails,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockStorage := database.NewMockStorage(t)
			activity := KmsConfigActivity{SE: mockStorage}
			testSuite := &testsuite.WorkflowTestSuite{}
			env := testSuite.NewTestActivityEnvironment()
			env.RegisterActivity(activity.UpdateKmsConfigState)

			expectedKms := &datamodel.KmsConfig{
				BaseModel: tt.kmsConfig.BaseModel,
				Name:      tt.kmsConfig.Name,
				State:     tt.state,
			}

			mockStorage.On("UpdateKmsConfigState", mock.Anything, tt.kmsConfig.UUID, tt.state, tt.stateDetails).Return(expectedKms, nil)

			// Act
			_, err := env.ExecuteActivity(activity.UpdateKmsConfigState, tt.kmsConfig, tt.state, tt.stateDetails)

			// Assert
			assert.NoError(t, err)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestUpdateKmsConfigState_Error(t *testing.T) {
	tests := []struct {
		name         string
		kmsConfig    *datamodel.KmsConfig
		state        string
		stateDetails string
		dbError      string
	}{
		{
			name: "DatabaseUpdateError",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			state:        datamodel.LifeCycleStateUpdating,
			stateDetails: datamodel.LifeCycleStateUpdatingDetails,
			dbError:      "database connection failed",
		},
		{
			name: "InvalidStateTransition",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			state:        datamodel.LifeCycleStateError,
			stateDetails: "System error occurred",
			dbError:      "invalid state transition",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockStorage := database.NewMockStorage(t)
			activity := KmsConfigActivity{SE: mockStorage}
			testSuite := &testsuite.WorkflowTestSuite{}
			env := testSuite.NewTestActivityEnvironment()
			env.RegisterActivity(activity.UpdateKmsConfigState)

			mockStorage.On("UpdateKmsConfigState", mock.Anything, tt.kmsConfig.UUID, tt.state, tt.stateDetails).Return(nil, errors.New(tt.dbError))

			// Act
			_, err := env.ExecuteActivity(activity.UpdateKmsConfigState, tt.kmsConfig, tt.state, tt.stateDetails)

			// Assert
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.dbError)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestUpdateKmsConfigState_NotFoundReturnsSuccess(t *testing.T) {
	// Test idempotent behavior when KMS config is already deleted
	mockStorage := database.NewMockStorage(t)
	activity := KmsConfigActivity{SE: mockStorage}
	testSuite := &testsuite.WorkflowTestSuite{}
	env := testSuite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.UpdateKmsConfigState)

	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "non-existent-uuid"},
		Name:      "test-kms",
	}

	// Return NotFoundErr to simulate already deleted record
	mockStorage.On("UpdateKmsConfigState", mock.Anything, kmsConfig.UUID, datamodel.LifeCycleStateError, "error details").Return(nil, errors.NewNotFoundErr("KMS Configuration", nil))

	// Act
	_, err := env.ExecuteActivity(activity.UpdateKmsConfigState, kmsConfig, datamodel.LifeCycleStateError, "error details")

	// Assert - Should succeed (idempotent)
	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func TestUpdateKmsConfig_Standalone_Success(t *testing.T) {
	// Test the standalone UpdateKmsConfig function directly
	tests := []struct {
		name           string
		kmsConfig      *datamodel.KmsConfig
		params         *common.UpdateKmsConfigParams
		expectedFields map[string]interface{}
	}{
		{
			name: "UpdateWithKeyNameSetsStateToCreated",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			params: &common.UpdateKmsConfigParams{
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
			},
			expectedFields: map[string]interface{}{
				"key_name":          "test-key",
				"key_ring":          "test-ring",
				"key_ring_location": "us-central1",
				"key_project_id":    "test-project",
				"kms_grant":         "",
				"state":             datamodel.LifeCycleStateCreated,
				"state_details":     datamodel.LifeCycleStateCreatedDetails,
			},
		},
		{
			name: "UpdateWithKeyNameAndGrantSetsStateToReady",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			params: &common.UpdateKmsConfigParams{
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
				KmsGrant:        "test-grant-id",
			},
			expectedFields: map[string]interface{}{
				"key_name":          "test-key",
				"key_ring":          "test-ring",
				"key_ring_location": "us-central1",
				"key_project_id":    "test-project",
				"kms_grant":         "test-grant-id",
				"state":             datamodel.LifeCycleStateREADY,
				"state_details":     datamodel.LifeCycleStateReadyDetails,
			},
		},
		{
			name: "GrantOnlyUpdateWhileInUseRetainsInUseState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel:       datamodel.BaseModel{UUID: "test-uuid"},
				Name:            "test-kms",
				State:           datamodel.LifeCycleStateInUse,
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
				KmsGrant:        "old-grant",
			},
			params: &common.UpdateKmsConfigParams{
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
				KmsGrant:        "new-grant",
			},
			expectedFields: map[string]interface{}{
				"key_name":          "test-key",
				"key_ring":          "test-ring",
				"key_ring_location": "us-central1",
				"key_project_id":    "test-project",
				"kms_grant":         "new-grant",
				"state":             datamodel.LifeCycleStateInUse,
				"state_details":     datamodel.LifeCycleStateInUseDetails,
			},
		},
		{
			name: "GrantOnlyUpdateFromCreatedSetsReadyState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel:       datamodel.BaseModel{UUID: "test-uuid"},
				Name:            "test-kms",
				State:           datamodel.LifeCycleStateCreated,
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
				KmsGrant:        "",
			},
			params: &common.UpdateKmsConfigParams{
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
				KmsGrant:        "new-grant",
			},
			expectedFields: map[string]interface{}{
				"key_name":          "test-key",
				"key_ring":          "test-ring",
				"key_ring_location": "us-central1",
				"key_project_id":    "test-project",
				"kms_grant":         "new-grant",
				"state":             datamodel.LifeCycleStateREADY,
				"state_details":     datamodel.LifeCycleStateReadyDetails,
			},
		},
		{
			name: "GrantOnlyUpdateWhileReadyDoesNotChangeState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel:       datamodel.BaseModel{UUID: "test-uuid"},
				Name:            "test-kms",
				State:           datamodel.LifeCycleStateREADY,
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
				KmsGrant:        "old-grant",
			},
			params: &common.UpdateKmsConfigParams{
				KeyName:         "test-key",
				KeyRing:         "test-ring",
				KeyRingLocation: "us-central1",
				KeyProjectID:    "test-project",
				KmsGrant:        "new-grant",
			},
			expectedFields: map[string]interface{}{
				"key_name":          "test-key",
				"key_ring":          "test-ring",
				"key_ring_location": "us-central1",
				"key_project_id":    "test-project",
				"kms_grant":         "new-grant",
			},
		},
		{
			name: "UpdateWithoutKeyNameDoesNotSetState",
			kmsConfig: &datamodel.KmsConfig{
				BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
				Name:      "test-kms",
			},
			params: &common.UpdateKmsConfigParams{
				ResourceID:  "test-resource",
				Description: stringPtr("test description"),
			},
			expectedFields: map[string]interface{}{
				"resource_id": "test-resource",
				"description": "test description",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			mockStorage := database.NewMockStorage(t)
			ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})

			mockStorage.On("UpdateKmsConfig", ctx, tt.kmsConfig.UUID, mock.MatchedBy(func(updateFields map[string]interface{}) bool {
				// Check if all expected fields are present and have correct values
				for key, expectedValue := range tt.expectedFields {
					actualValue, exists := updateFields[key]
					if !exists || actualValue != expectedValue {
						return false
					}
				}
				// Check if no unexpected fields are present
				return len(updateFields) == len(tt.expectedFields)
			})).Return(nil)

			// Act
			err := UpdateKmsConfig(mockStorage, ctx, tt.kmsConfig, tt.params)

			// Assert
			assert.NoError(t, err)
			mockStorage.AssertExpectations(t)
		})
	}
}

func TestUpdateKmsConfig_Standalone_Error(t *testing.T) {
	// Arrange
	mockStorage := database.NewMockStorage(t)
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, log.Fields{})
	kmsConfig := &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
		Name:      "test-kms",
	}
	params := &common.UpdateKmsConfigParams{
		ResourceID: "test-resource",
	}

	mockStorage.On("UpdateKmsConfig", ctx, kmsConfig.UUID, mock.Anything).Return(errors.New("database error"))

	// Act
	err := UpdateKmsConfig(mockStorage, ctx, kmsConfig, params)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database error")
	mockStorage.AssertExpectations(t)
}

func TestEnrichUpdateKmsConfigParamsFromKeyURI(t *testing.T) {
	t.Run("NilParams", func(t *testing.T) {
		assert.NotPanics(t, func() {
			EnrichUpdateKmsConfigParamsFromKeyURI(nil)
		})
	})

	t.Run("EmptyKeyUri", func(t *testing.T) {
		params := &common.UpdateKmsConfigParams{KeyName: "unchanged"}
		EnrichUpdateKmsConfigParamsFromKeyURI(params)
		assert.Equal(t, "unchanged", params.KeyName)
	})

	t.Run("InvalidKeyUri", func(t *testing.T) {
		params := &common.UpdateKmsConfigParams{KeyUri: "not-a-valid-key-path"}
		EnrichUpdateKmsConfigParamsFromKeyURI(params)
		assert.Empty(t, params.KeyName)
		assert.Empty(t, params.KmsGrant)
	})

	t.Run("ValidKeyUriWithGrant", func(t *testing.T) {
		params := &common.UpdateKmsConfigParams{
			KeyUri: "projects/test-project/locations/us-central1/keyRings/test-ring/cryptoKeys/test-key/grants/grant-1",
		}
		EnrichUpdateKmsConfigParamsFromKeyURI(params)
		assert.Equal(t, "test-key", params.KeyName)
		assert.Equal(t, "test-ring", params.KeyRing)
		assert.Equal(t, "us-central1", params.KeyRingLocation)
		assert.Equal(t, "test-project", params.KeyProjectID)
		assert.Equal(t, "grant-1", params.KmsGrant)
	})
}

func TestIsGrantOnlyKmsKeyUpdate(t *testing.T) {
	baseKmsConfig := &datamodel.KmsConfig{
		KeyName:         "test-key",
		KeyRing:         "test-ring",
		KeyRingLocation: "us-central1",
		KeyProjectID:    "test-project",
		KmsGrant:        "old-grant",
	}
	baseParams := &common.UpdateKmsConfigParams{
		KeyName:         "test-key",
		KeyRing:         "test-ring",
		KeyRingLocation: "us-central1",
		KeyProjectID:    "test-project",
		KmsGrant:        "new-grant",
	}

	t.Run("NilKmsConfig", func(t *testing.T) {
		assert.False(t, IsGrantOnlyKmsKeyUpdate(nil, baseParams))
	})

	t.Run("NilParams", func(t *testing.T) {
		assert.False(t, IsGrantOnlyKmsKeyUpdate(baseKmsConfig, nil))
	})

	t.Run("EmptyKeyName", func(t *testing.T) {
		params := &common.UpdateKmsConfigParams{KmsGrant: "new-grant"}
		assert.False(t, IsGrantOnlyKmsKeyUpdate(baseKmsConfig, params))
	})

	t.Run("GrantOnly", func(t *testing.T) {
		assert.True(t, IsGrantOnlyKmsKeyUpdate(baseKmsConfig, baseParams))
	})

	t.Run("SameGrant", func(t *testing.T) {
		params := *baseParams
		params.KmsGrant = "old-grant"
		assert.False(t, IsGrantOnlyKmsKeyUpdate(baseKmsConfig, &params))
	})

	t.Run("DifferentKeyName", func(t *testing.T) {
		params := *baseParams
		params.KeyName = "other-key"
		assert.False(t, IsGrantOnlyKmsKeyUpdate(baseKmsConfig, &params))
	})
}
