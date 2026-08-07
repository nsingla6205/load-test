package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/go-faster/jx"
	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	vsaCoreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

func TestParseCmekSupervisorGracePeriod_Default(t *testing.T) {
	t.Setenv("CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "")
	got := parseCmekSupervisorGracePeriod()
	assert.Equal(t, 14*time.Minute, got)
}

func TestParseCmekSupervisorGracePeriod_Custom(t *testing.T) {
	t.Setenv("CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "22")
	got := parseCmekSupervisorGracePeriod()
	assert.Equal(t, 22*time.Minute, got)
}

func TestParseCmekSupervisorGracePeriod_Invalid(t *testing.T) {
	t.Setenv("CMEK_WORKFLOW_GLOBAL_TIMEOUT_MINUTES", "invalid")
	got := parseCmekSupervisorGracePeriod()
	assert.Equal(t, 14*time.Minute, got)
}

// V1betaCreateKmsConfiguration unittests
func TestV1betaCreateKmsConfigurations(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	expectCreateJobMaybe := func(mockOrchestrator *factory.MockOrchestratorFactory) {
		job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "sde-job-id"}}
		mockOrchestrator.EXPECT().CreateJob(mock.Anything, mock.Anything).
			Maybe().
			Return(job, nil)
		mockOrchestrator.EXPECT().
			UpdateJobStatus(mock.Anything, job.UUID, string(datamodel.JobsStateERROR), mock.AnythingOfType("int"), mock.AnythingOfType("string")).
			Maybe().
			Return(nil)
		mockOrchestrator.EXPECT().
			UpdateJobAttributes(mock.Anything, job.UUID, mock.MatchedBy(func(attrs *datamodel.JobAttributes) bool {
				return attrs != nil && attrs.SupervisorAttributes != nil && attrs.SupervisorAttributes.OverrideGracePeriod == 0
			})).
			Maybe().
			Return(nil)
	}

	t.Run("CreateKmsConfigurationReturnsBadRequestWhenKeyFullPathIsInvalid", func(t *testing.T) {
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "invalid"}
		handler := Handler{}
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Code)
		assert.Equal(t, "Invalid KeyFullPath format", result.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Message)
	})
	t.Run("CreateKmsConfigurationReturnsBadRequestWhenLocationIdIsInvalid", func(t *testing.T) {
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-central1/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{}
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Code)
		assert.Equal(t, "LocationID represents neither a region nor a zone", result.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Message)
	})
	t.Run("CreateKmsConfigurationFailsWithConflictError", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		// Define mock response
		mockResponse := &kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(false),
				Response: models.KmsConfigV1beta{UUID: "test", KeyFullPath: nil},
			},
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(mockResponse, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))
		mockOrchestrator.EXPECT().CreateKmsConfig(mock.Anything, mock.Anything).Return(nil, "", errors.NewConflictErr("some error"))
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(http.StatusConflict), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
	})
	t.Run("CreateKmsConfigurationWhenKeyringLocationDoNotMatchWithRegion", func(t *testing.T) {
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}
		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		handler := Handler{}
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Code)
	})
	t.Run("CreateKmsConfigurationFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		// Define mock response
		mockResponse := &kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(false),
				Response: models.KmsConfigV1beta{UUID: "test", KeyFullPath: nil},
			},
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(mockResponse, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))
		mockOrchestrator.EXPECT().CreateKmsConfig(mock.Anything, mock.Anything).Return(nil, "", errors.New("some error"))
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Code)
	})
	t.Run("V1betaCreateKmsConfigurationFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		origParseKmsConfigResponse := parseKmsConfigResponse
		defer func() {
			createClient = originalCreateClient
			parseKmsConfigResponse = origParseKmsConfigResponse
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(nil, errors.New("some error"))

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Code)
	})
	t.Run("ParseKmsConfigResponseFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		origParseKmsConfigResponse := parseKmsConfigResponse
		defer func() {
			createClient = originalCreateClient
			parseKmsConfigResponse = origParseKmsConfigResponse
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		// Define mock response
		mockResponse := &kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(false),
				Response: "not-a-json-object",
			},
		}
		parseKmsConfigResponse = func(payloadResponse interface{}) (*models.KmsConfigV1beta, error) {
			return nil, errors.New("some error")
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(mockResponse, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Code)
	})
	t.Run("CreateKmsConfigurationSuccess", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		kmsConfig := &vsaCoreModels.KmsConfig{KmsAttributes: &vsaCoreModels.KmsAttributes{}}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		// Define mock response
		mockResponse := &kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(true),
				Response: models.KmsConfigV1beta{UUID: "test", KeyFullPath: nil},
			},
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(mockResponse, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		getKmsConfigParams := &common.GetKmsConfigParams{
			AccountName: params.ProjectNumber,
			KeyFullPath: req.KeyFullPath,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, getKmsConfigParams).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		mockOrchestrator.EXPECT().CreateKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, "operation-id", nil)
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("GetExistingKmsConfigFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.New("some other error"))
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Code)
	})
	t.Run("GetExistingKmsConfigReturnsConflictForDifferentKeyPath", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/different-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil,
			errors.NewConflictErr("A KMS configuration already exists for this account with a different key path"))
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		conflictResult, ok := result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict)
		assert.True(t, ok, "expected V1betaCreateKmsConfigurationConflict response")
		assert.Equal(t, float64(http.StatusConflict), conflictResult.Code)
		assert.Contains(t, conflictResult.Message, "A KMS configuration already exists")
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInErrorState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		job := &vsaCoreModels.Job{}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateError,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, mock.Anything, mock.Anything).Return(job, nil)
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInCreatingState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		job := &vsaCoreModels.Job{}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateCreating,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, mock.Anything, mock.Anything).Return(job, nil)
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NotEmpty(t, result)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInCreatingStateWithDifferentResourceID_ReturnsConflict", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{
			KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key",
			ResourceId:  gcpgenserver.NewOptString("req-resource-id"),
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:         datamodel.LifeCycleStateCreating,
			ResourceID:    "existing-resource-id",
			KmsAttributes: &vsaCoreModels.KmsAttributes{},
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(http.StatusConflict), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Message, "existing-resource-id")
		assert.Contains(t, result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Message, "must use a distinct key")
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInInUseState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		job := &vsaCoreModels.Job{}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateInUse,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, mock.Anything, mock.Anything).Return(job, nil)
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NotEmpty(t, result)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("WhenGetJobByResourceUUIDFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateCreating,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("some other error"))
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NotEmpty(t, result) // Should return internal server error response, not empty
		assert.NoError(t, err)     // HTTP error response doesn't return Go error
	})
	t.Run("WhenCheckKmsConfigurationParseAndValidateRegionAndZoneReturnsGlobalRegion", func(t *testing.T) {
		// Define input parameters
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "regionGlobal", "", nil
		}
		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/global/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Call the method under test
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, float64(http.StatusBadRequest), result.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Code)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInDeletingState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateDeleting,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// No GetJobByResourceUUID call expected since the method returns early for Deleting state
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(409), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInUpdatingState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateUpdating,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// No GetJobByResourceUUID call expected since the method returns early for Updating state
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(409), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInMigratingState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateMigrating,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// No GetJobByResourceUUID call expected since the method returns early for Migrating state
		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(409), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInCreatedState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		job := &vsaCoreModels.Job{BaseModel: vsaCoreModels.BaseModel{UUID: "operation-123"}}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateCreated,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, mock.Anything, mock.Anything).Return(job, nil)

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)
		assert.Equal(t, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInReadyState", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		job := &vsaCoreModels.Job{BaseModel: vsaCoreModels.BaseModel{UUID: "operation-123"}}
		kmsConfig := &vsaCoreModels.KmsConfig{State: datamodel.LifeCycleStateREADY,
			KmsAttributes: &vsaCoreModels.KmsAttributes{}}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, mock.Anything, mock.Anything).Return(job, nil)

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, job.UUID)
		assert.Equal(t, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInReadyStateWithDifferentResourceID_ReturnsConflict", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{
			KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key",
			ResourceId:  gcpgenserver.NewOptString("req-resource-id"),
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:         datamodel.LifeCycleStateREADY,
			ResourceID:    "existing-resource-id",
			KmsAttributes: &vsaCoreModels.KmsAttributes{},
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(http.StatusConflict), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Message, "existing-resource-id")
		assert.Contains(t, result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Message, "must use a distinct key")
	})
	t.Run("CvpClientCreateKmsConfigurationReturnsConflictError", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Simulate a conflict error from CVP as an error (not a response)
		conflictError := &kms_configurations.V1betaCreateKmsConfigurationConflict{
			Payload: &models.Error{
				Code:    409,
				Message: "KMS configuration already exists",
			},
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(nil, conflictError)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(409), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
	})
	t.Run("CvpClientCreateKmsConfigurationReturnsBadRequestError", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Simulate a bad request error from CVP as an error (not a response)
		badRequestError := &kms_configurations.V1betaCreateKmsConfigurationBadRequest{
			Payload: &models.Error{
				Code:    400,
				Message: "Invalid KMS key path",
			},
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(nil, badRequestError)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Code)
	})
	t.Run("CvpClientCreateKmsConfigurationReturnsUnauthorizedError", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Simulate an unauthorized error from CVP as an error (not a response)
		unauthorizedError := &kms_configurations.V1betaCreateKmsConfigurationUnauthorized{
			Payload: &models.Error{
				Code:    401,
				Message: "Authentication failed",
			},
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(nil, unauthorizedError)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(401), result.(*gcpgenserver.V1betaCreateKmsConfigurationUnauthorized).Code)
	})
	t.Run("OrchestratorCreateKmsConfigFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key"}

		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Mock successful CVP response
		mockResponse := &kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(true),
				Response: models.KmsConfigV1beta{UUID: "test", KeyFullPath: nil},
			},
		}
		mockClient.On("V1betaCreateKmsConfiguration", mock.Anything).Return(mockResponse, nil)

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))
		// Mock orchestrator CreateKmsConfig to return error
		mockOrchestrator.EXPECT().CreateKmsConfig(mock.Anything, mock.Anything).Return(nil, "", errors.New("orchestrator failed to create kms config"))

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Code)
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInErrorStateWithDifferentResourceID_ReturnsConflict", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{
			KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key",
			ResourceId:  gcpgenserver.NewOptString("req-resource-id"),
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:         datamodel.LifeCycleStateError,
			ResourceID:    "existing-resource-id",
			KmsAttributes: &vsaCoreModels.KmsAttributes{},
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(http.StatusConflict), result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Message, "existing-resource-id")
		assert.Contains(t, result.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Message, "must use a distinct key")
	})
	t.Run("GetExistingKmsConfigReturnsKmsConfigInErrorStateWithSameResourceID", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		expectCreateJobMaybe(mockOrchestrator)
		params := gcpgenserver.V1betaCreateKmsConfigurationParams{
			LocationId:    "us-east4",
			ProjectNumber: "test-project",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.KmsConfigV1beta{
			KeyFullPath: "projects/test-project/locations/us-east4/keyRings/test-keyring/cryptoKeys/test-key",
			ResourceId:  gcpgenserver.NewOptString("req-resource-id"),
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		job := &vsaCoreModels.Job{BaseModel: vsaCoreModels.BaseModel{UUID: "operation-123"}}
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:         datamodel.LifeCycleStateError,
			ResourceID:    "req-resource-id",
			KmsAttributes: &vsaCoreModels.KmsAttributes{},
		}
		mockOrchestrator.EXPECT().GetExistingKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().GetJobByResourceUUID(mock.Anything, mock.Anything, mock.Anything).Return(job, nil)

		result, err := handler.V1betaCreateKmsConfiguration(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Contains(t, result.(*gcpgenserver.OperationV1beta).Response.String(), kmsConfig.ResourceID)
		assert.Contains(t, result.(*gcpgenserver.OperationV1beta).Name.Value, job.UUID)
	})
}

// V1betaEncryptVolumes' unit-tests
func TestV1betaEncryptVolumes(t *testing.T) {
	t.Run("V1betaEncryptVolumesWhenOrchestratorGetMultipleKMSReturnsNotFound", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock of VCP Orchestrator and Datastore - Reusing data from other UTs
		mockLogger := log.NewLogger()
		store, err := database.NewTestStorage(mockLogger)
		if err != nil {
			t.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		serviceAccounts := []*datamodel.ServiceAccount{
			{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10"}, Name: "ServiceAccount1"},
			{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2"},
		}
		err = store.DB().Create(serviceAccounts).Error
		if err != nil {
			t.Fatalf("Failed to create Service-Accounts table: %v", err)
		}

		kmsConfigs := []*datamodel.KmsConfig{
			{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ResourceID: "Resource-Id1-VCP", ServiceAccountID: &serviceAccounts[0].ID,
				KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount1@account.com"}},
			{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ResourceID: "Resource-Id2-VCP", ServiceAccountID: &serviceAccounts[1].ID,
				KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount2@account.com"}},
		}
		err = store.DB().Create(kmsConfigs).Error
		if err != nil {
			t.Fatalf("Failed to create KMS Configs table: %v", err)
		}

		handler := Handler{}
		orchInstance := factory.GetOrchestratorForProvider(store, nil)
		handler.Orchestrator = orchInstance

		// Mock of CVP Client
		mockClient := kms_configurations.NewMockClientService(t)
		kmsConfigsCVP := make([]*models.KmsConfigV1beta, 0)
		mockResponse := &kms_configurations.V1betaGetMultipleKmsConfigsOK{
			Payload: &kms_configurations.V1betaGetMultipleKmsConfigsOKBody{
				KmsConfigurations: kmsConfigsCVP,
			},
		}

		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		params := gcpgenserver.V1betaEncryptVolumesParams{
			LocationId:     "local",
			ProjectNumber:  "test-project",
			KmsConfigId:    "uuid99",
			XCorrelationID: gcpgenserver.NewOptString("x-correlationId"),
		}

		result, err := handler.V1betaEncryptVolumes(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		assert.Equal(t, float64(404), result.(*gcpgenserver.V1betaEncryptVolumesBadRequest).Code)
		assert.Equal(t, "CMEK policy with UUID uuid99 not found", result.(*gcpgenserver.V1betaEncryptVolumesBadRequest).Message)
	})
	t.Run("V1betaEncryptVolumesWhenOrchestratorEncryptVolumesReturnsError", func(t *testing.T) {
		// GetMultiple always merges from CVP when CVP_HOST is set; this test uses the real
		// orchestrator and does not stub createClient, so clear CVP_HOST to stay VCP-only.
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaEncryptVolumesParams{
			LocationId:     "local",
			ProjectNumber:  "test-project",
			KmsConfigId:    "uuid1",
			XCorrelationID: gcpgenserver.NewOptString("x-correlationId"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock of VCP Orchestrator and Datastore - Reusing data from other UTs
		mockLogger := log.NewLogger()
		store, err := database.NewTestStorage(mockLogger)
		if err != nil {
			t.Fatalf("Failed to create test storage: %v", err)
		}

		err = database.ClearInMemoryDB(store.DB())
		if err != nil {
			t.Fatalf("Failed to clean up test storage: %v", err)
		}

		serviceAccounts := []*datamodel.ServiceAccount{
			{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10"}, Name: "ServiceAccount1"},
			{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2"},
		}
		err = store.DB().Create(serviceAccounts).Error
		if err != nil {
			t.Fatalf("Failed to create Service-Accounts table: %v", err)
		}

		kmsConfigs := []*datamodel.KmsConfig{
			{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ResourceID: "Resource-Id1-VCP", ServiceAccountID: &serviceAccounts[0].ID,
				KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount1@account.com"}},
			{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ResourceID: "Resource-Id2-VCP", ServiceAccountID: &serviceAccounts[1].ID,
				KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount2@account.com"}},
		}
		err = store.DB().Create(kmsConfigs).Error
		if err != nil {
			t.Fatalf("Failed to create KMS Configs table: %v", err)
		}

		handler := Handler{}
		orchInstance := factory.GetOrchestratorForProvider(store, nil)
		handler.Orchestrator = orchInstance

		result, err := handler.V1betaEncryptVolumes(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaEncryptVolumesInternalServerError).Code)
		assert.Equal(t, "Account not found", result.(*gcpgenserver.V1betaEncryptVolumesInternalServerError).Message)
	})
	t.Run("V1betaEncryptVolumesWhenOrchestratorEncryptVolumesReturnsEmptyOperation", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaEncryptVolumesParams{
			LocationId:     "local",
			ProjectNumber:  "test-project",
			KmsConfigId:    "uuid1",
			XCorrelationID: gcpgenserver.NewOptString("x-correlationId"),
		}
		kmsConfigsResult := make([]*vsaCoreModels.KmsConfig, 0)
		kmsConfigsResult = append(kmsConfigsResult, &vsaCoreModels.KmsConfig{
			Name: "uuid1",
		})

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetMultipleKMSConfigs(mock.Anything, mock.Anything).Return(kmsConfigsResult, nil)
		mockOrchestrator.EXPECT().MigrateKmsConfig(mock.Anything, mock.Anything).Return("", nil)
		handlerForOrch := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handlerForOrch.V1betaEncryptVolumes(context.Background(), params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "Job ID not returned by VCP for CMEK policy migration", result.(*gcpgenserver.V1betaEncryptVolumesInternalServerError).Message)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaEncryptVolumesInternalServerError).Code)
	})
	t.Run("V1betaEncryptVolumesWhenEncryptVolumesIsSuccessful", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaEncryptVolumesParams{
			LocationId:     "local",
			ProjectNumber:  "test-project",
			KmsConfigId:    "uuid1",
			XCorrelationID: gcpgenserver.NewOptString("x-correlationId"),
		}
		kmsConfigsResult := make([]*vsaCoreModels.KmsConfig, 0)
		kmsConfigsResult = append(kmsConfigsResult, &vsaCoreModels.KmsConfig{
			Name: "uuid1",
		})

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetMultipleKMSConfigs(mock.Anything, mock.Anything).Return(kmsConfigsResult, nil)
		mockOrchestrator.EXPECT().MigrateKmsConfig(mock.Anything, mock.Anything).Return("operationID", nil)
		handlerForOrch := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handlerForOrch.V1betaEncryptVolumes(context.Background(), params)

		var encryptStatus models.EncryptVolumeStatusV1beta

		errMarshall := json.Unmarshal(result.(*gcpgenserver.OperationV1beta).Response, &encryptStatus)
		if errMarshall != nil {
			tt.Errorf("Error unmarshalling JSON: %v\n", err)
		}

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, "/v1beta/projects/test-project/locations/local/operations/operationID", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("V1betaEncryptVolumesWhenOrchestratorReturnsConflict", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaEncryptVolumesParams{
			LocationId:     "local",
			ProjectNumber:  "test-project",
			KmsConfigId:    "uuid1",
			XCorrelationID: gcpgenserver.NewOptString("x-correlationId"),
		}
		kmsConfigsResult := []*vsaCoreModels.KmsConfig{
			{Name: "uuid1"},
		}

		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		conflictMsg := "A pool encryption (EncryptPool) is already in progress for this account"
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetMultipleKMSConfigs(mock.Anything, mock.Anything).Return(kmsConfigsResult, nil)
		mockOrchestrator.EXPECT().MigrateKmsConfig(mock.Anything, mock.Anything).Return("", errors.NewConflictErr(conflictMsg))
		handlerForOrch := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handlerForOrch.V1betaEncryptVolumes(context.Background(), params)

		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		conflict, ok := result.(*gcpgenserver.V1betaEncryptVolumesConflict)
		assert.True(tt, ok)
		assert.Equal(tt, float64(http.StatusConflict), conflict.Code)
		assert.Equal(tt, conflictMsg, conflict.Message)
	})
}

func TestV1betaEncryptVolumesWhenParseAndValidateRegionAndZoneReturnsError(t *testing.T) {
	originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "", "", &gcpgenserver.Error{Code: 400, Message: "LocationID represents neither a region nor a zone"}
	}

	handler := Handler{}
	params := gcpgenserver.V1betaEncryptVolumesParams{
		LocationId:    "invalid-location",
		ProjectNumber: "test-project",
	}
	result, err := handler.V1betaEncryptVolumes(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaEncryptVolumesBadRequest).Code)
	assert.Equal(t, "LocationID represents neither a region nor a zone", result.(*gcpgenserver.V1betaEncryptVolumesBadRequest).Message)
}

func TestConvertEncryptVolumesToOperationV1Beta(t *testing.T) {
	// Generated using CoPilot
	t.Run("SuccessfulConversion", func(t *testing.T) {
		params := gcpgenserver.V1betaEncryptVolumesParams{
			KmsConfigId:   "test-kms-config-id",
			ProjectNumber: "test-project-number",
			LocationId:    "test-location-id",
		}
		operationID := "test-operation-id"

		result, err := convertEncryptVolumesToOperationV1Beta(params, operationID)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, operationID), result.Name.Value)
		assert.False(t, result.Done.Value)
		assert.NotNil(t, result.Response)
	})
	t.Run("ErrorDuringJSONEncoding", func(t *testing.T) {
		// Simulate an error by passing invalid data to the JSON encoder
		originalFunc := encodeEncryptVolumeV1beta
		defer func() { encodeEncryptVolumeV1beta = originalFunc }() // Restore the original function after the test

		encodeEncryptVolumeV1beta = func(*models.EncryptVolumeV1beta) (jx.Raw, error) {
			return nil, fmt.Errorf("JSON encoding error")
		}

		params := gcpgenserver.V1betaEncryptVolumesParams{
			KmsConfigId:   "test-kms-config-id",
			ProjectNumber: "test-project-number",
			LocationId:    "test-location-id",
		}
		operationID := "test-operation-id"

		result, err := convertEncryptVolumesToOperationV1Beta(params, operationID)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, "JSON encoding error", err.Error())
	})
}

func TestEncodeEncryptVolumeV1beta(t *testing.T) {
	// Generated using CoPilot
	t.Run("SuccessfulEncoding", func(t *testing.T) {
		encryptVolume := &models.EncryptVolumeV1beta{
			EncryptionStatus: &models.EncryptVolumeStatusV1beta{
				UUID:   "test-uuid",
				Status: "UPDATING",
			},
		}

		result, err := encodeEncryptVolumeV1beta(encryptVolume)
		assert.NoError(t, err)
		assert.NotNil(t, result)

		var decoded map[string]interface{}
		err = json.Unmarshal(result, &decoded)
		assert.NoError(t, err)
	})
}

// V1betaDeleteKmsConfiguration unittests
func TestV1betaDelete1KmsConfiguration(t *testing.T) {
	t.Run("CreatKmsConfigurationSuccess", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		params := gcpgenserver.V1betaDeleteKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
			KmsConfigId:   "kms-config-uuid",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		kmsConfig := &vsaCoreModels.KmsConfig{KmsAttributes: &vsaCoreModels.KmsAttributes{}}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		operationID := fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "operation-id")
		mockOrchestrator.EXPECT().DeleteKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, "operation-id", nil)
		result, err := handler.V1betaDeleteKmsConfiguration(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, operationID, result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenFailureNotFound", func(t *testing.T) {
		// Define input parameters
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		params := gcpgenserver.V1betaDeleteKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
			KmsConfigId:   "kms-config-uuid",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().DeleteKmsConfig(mock.Anything, mock.Anything).Return(nil, "", errors.NewNotFoundErr("kms", nil))
		// Call the method under test
		result, err := handler.V1betaDeleteKmsConfiguration(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.Equal(t, result, &gcpgenserver.V1betaDeleteKmsConfigurationNotFound{Code: 404, Message: "kms not found"})
	})
	t.Run("WhenFailure", func(t *testing.T) {
		// Define input parameters
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		params := gcpgenserver.V1betaDeleteKmsConfigurationParams{
			LocationId:    "invalid-location",
			ProjectNumber: "test-project",
			KmsConfigId:   "kms-config-uuid",
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().DeleteKmsConfig(mock.Anything, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("error"))
		// Call the method under test
		result, err := handler.V1betaDeleteKmsConfiguration(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.Equal(t, result, &gcpgenserver.V1betaDeleteKmsConfigurationBadRequest{Code: 400, Message: "error"})
	})
}

// V1betaDescribeKmsConfiguration unittests
func TestV1betaDescribeKmsConfiguration(t *testing.T) {
	t.Run("DescribeKmsReturnsBadRequestWhenLocationIdIsInvalid", func(t *testing.T) {
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId: "invalid-location",
		}
		handler := Handler{}

		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaDescribeKmsConfigurationBadRequest).Code)
		assert.Equal(t, "LocationID represents neither a region nor a zone", result.(*gcpgenserver.V1betaDescribeKmsConfigurationBadRequest).Message)
	})

	t.Run("DescribeKmsHandlesNilLocationIdGracefully", func(t *testing.T) {
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId: "",
		}
		handler := Handler{}

		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaDescribeKmsConfigurationBadRequest).Code)
		assert.Equal(t, "LocationID represents neither a region nor a zone", result.(*gcpgenserver.V1betaDescribeKmsConfigurationBadRequest).Message)
	})

	t.Run("WhenDescribeKmsConfigurationSuccess", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}

		// Define mock response
		updatedTime := strfmt.DateTime(time.Now())
		description := "test-description"
		keyFullPath := "test-key-full-path"
		mockResponse := &kms_configurations.V1betaDescribeKmsConfigurationOK{
			Payload: &models.KmsConfigV1beta{
				UUID:                "test-id",
				ServiceAccountEmail: "test-email",
				KeyFullPath:         &keyFullPath,
				KmsState:            "test-state",
				KmsStateDetails:     "test-details",
				Description:         &description,
				CreatedTime:         strfmt.DateTime(time.Now()),
				UpdatedTime:         &updatedTime,
				DeletedTime:         &updatedTime,
				Instructions:        "test-instructions",
			},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the resource name is as expected
		assert.Equal(t, "test-id", result.(*gcpgenserver.KmsConfigV1beta).UUID.Value)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithNotFound", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationNotFound{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationNotFound).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationNotFound).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithBadRequest", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationBadRequest).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithUnprocessableEntity", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}
		// Define mock error
		errorCode := float64(422)
		errorMessage := "Unprocessable error"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationUnprocessableEntity{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationUnprocessableEntity).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationUnprocessableEntity).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithConflict", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}
		// Define mock error
		errorCode := float64(409)
		errorMessage := "Conflict error"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationConflict{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationConflict).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationConflict).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithUnauthorized", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}
		// Define mock error
		errorCode := float64(401)
		errorMessage := "Unauthorized error"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationUnauthorized).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithForbidden", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}
		// Define mock error
		errorCode := float64(403)
		errorMessage := "Forbidden error"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationForbidden).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithTooManyRequests", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}
		// Define mock error
		errorCode := float64(429)
		errorMessage := "Too Many Requests error"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationTooManyRequests).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithDefault", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}
		// Define mock error
		errorCode := float64(500)
		errorMessage := "default error"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError).Message)
	})

	t.Run("WhenDescribeKmsConfigurationFailsWithUnknownError", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}
		// Define mock error
		errorCode := float64(500)
		errorMessage := "unknown error during the describe kms config"
		mockError := &kms_configurations.V1betaDescribeKmsConfigurationInternalServerError{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Maybe().Return(nil, errors.NewNotFoundErr("not found", nil))
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError).Message)
	})

	t.Run("WhenStateIsInUseInSDEAndCreatedInVCP", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}

		// Define mock response
		updatedTime := strfmt.DateTime(time.Now())
		description := "test-description"
		keyFullPath := "test-key-full-path"
		mockResponse := &kms_configurations.V1betaDescribeKmsConfigurationOK{
			Payload: &models.KmsConfigV1beta{
				UUID:                "test-id",
				ServiceAccountEmail: "test-email",
				KeyFullPath:         &keyFullPath,
				KmsState:            datamodel.LifeCycleStateInUse,
				KmsStateDetails:     "test-details",
				Description:         &description,
				CreatedTime:         strfmt.DateTime(time.Now()),
				UpdatedTime:         &updatedTime,
				DeletedTime:         &updatedTime,
				Instructions:        "test-instructions",
			},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:          datamodel.LifeCycleStateCreated,
			KmsAttributes:  &vsaCoreModels.KmsAttributes{},
			ServiceAccount: &vsaCoreModels.ServiceAccount{},
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the resource name is as expected
		assert.Equal(t, datamodel.LifeCycleStateInUse, string(result.(*gcpgenserver.KmsConfigV1beta).KmsState.Value))
	})

	t.Run("WhenStateIsInErrorInSDEAndCreatedInVCP", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		// create a mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			KmsConfigId:    "kms-config-id-1",
		}

		// Define mock response
		updatedTime := strfmt.DateTime(time.Now())
		description := "test-description"
		keyFullPath := "test-key-full-path"
		mockResponse := &kms_configurations.V1betaDescribeKmsConfigurationOK{
			Payload: &models.KmsConfigV1beta{
				UUID:                "test-id",
				ServiceAccountEmail: "test-email",
				KeyFullPath:         &keyFullPath,
				KmsState:            datamodel.LifeCycleStateError,
				KmsStateDetails:     "test-details",
				Description:         &description,
				CreatedTime:         strfmt.DateTime(time.Now()),
				UpdatedTime:         &updatedTime,
				DeletedTime:         &updatedTime,
				Instructions:        "test-instructions",
			},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeKmsConfiguration(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			createClient = originalCreateClient
			parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:          datamodel.LifeCycleStateCreated,
			KmsAttributes:  &vsaCoreModels.KmsAttributes{},
			ServiceAccount: &vsaCoreModels.ServiceAccount{},
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Call the method under test
		result, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the resource name is as expected
		assert.Equal(t, datamodel.LifeCycleStateError, string(result.(*gcpgenserver.KmsConfigV1beta).KmsState.Value))
	})
}

// V1betaUpdateKmsConfiguration unittests
func TestV1betaUpdateKmsConfiguration(t *testing.T) {
	t.Run("WhenUpdateKmsConfigurationSuccess", func(t *testing.T) {
		// Define input parameters
		params := gcpgenserver.V1betaUpdateKmsConfigurationParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.KmsConfigUpdateV1beta{
			KeyFullPath: gcpgenserver.NewOptString("projects/projectID/locations/us/keyRings/keyRing/cryptoKeys/keyName"),
			Description: gcpgenserver.NewOptString("test-description"),
			ResourceId:  gcpgenserver.NewOptString("test-resource-id"),
		}

		resourceId := "test-resource-id"
		kms := &vsaCoreModels.KmsConfig{
			BaseModel: vsaCoreModels.BaseModel{
				UUID: "kms-config-id-1",
			},
			Name:            resourceId,
			KeyName:         "keyName",
			KeyRing:         "keyRing",
			KeyProjectID:    "projectID",
			KeyRingLocation: "us",
			ResourceID:      resourceId,
			Description:     "test-description",
			State:           datamodel.LifeCycleStateAvailable,
			KmsAttributes:   &vsaCoreModels.KmsAttributes{},
		}

		mockOrch := &factory.MockOrchestratorFactory{}
		mockOrch.On("UpdateKmsConfig", mock.Anything, mock.Anything).Return(kms, nil)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}
		handler := Handler{
			Orchestrator: mockOrch,
		}
		// Call the method under test
		result, err := handler.V1betaUpdateKmsConfiguration(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the result is a KmsConfigV1beta
		kmsConfigResult, ok := result.(*gcpgenserver.KmsConfigV1beta)
		assert.True(t, ok)
		assert.Equal(t, "kms-config-id-1", kmsConfigResult.UUID.Value)
		assert.Equal(t, "test-description", kmsConfigResult.Description.Value)
		assert.Equal(t, "test-resource-id", kmsConfigResult.ResourceId.Value)
	})
	t.Run("WhenFailure", func(t *testing.T) {
		// Define input parameters
		params := gcpgenserver.V1betaUpdateKmsConfigurationParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.KmsConfigUpdateV1beta{
			KeyFullPath: gcpgenserver.NewOptString("projects/projectID/locations/us/keyRings/keyRing/cryptoKeys/keyName"),
			Description: gcpgenserver.NewOptString("test-description"),
			ResourceId:  gcpgenserver.NewOptString("test-resource-id"),
		}

		mockOrch := &factory.MockOrchestratorFactory{}
		mockOrch.On("UpdateKmsConfig", mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("kms", nil))

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}
		handler := Handler{
			Orchestrator: mockOrch,
		}
		// Call the method under test
		result, err := handler.V1betaUpdateKmsConfiguration(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.Equal(t, result, &gcpgenserver.V1betaUpdateKmsConfigurationNotFound{Code: 404, Message: "kms not found"})
	})
	t.Run("WhenConflictErro", func(t *testing.T) {
		// Define input parameters
		params := gcpgenserver.V1betaUpdateKmsConfigurationParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.KmsConfigUpdateV1beta{
			KeyFullPath: gcpgenserver.NewOptString("projects/projectID/locations/us/keyRings/keyRing/cryptoKeys/keyName"),
			Description: gcpgenserver.NewOptString("test-description"),
			ResourceId:  gcpgenserver.NewOptString("test-resource-id"),
		}

		mockOrch := &factory.MockOrchestratorFactory{}
		mockOrch.On("UpdateKmsConfig", mock.Anything, mock.Anything).Return(nil, errors.NewConflictErr("kms"))

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "region", "zone", nil
		}
		handler := Handler{
			Orchestrator: mockOrch,
		}
		// Call the method under test
		result, err := handler.V1betaUpdateKmsConfiguration(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.Equal(t, result, &gcpgenserver.V1betaUpdateKmsConfigurationConflict{Code: 409, Message: "kms"})
	})
}

// V1betaCheckKmsConfiguration unittests
func TestV1betaCheckKmsConfiguration(t *testing.T) {
	t.Run("WhenCheckKmsConfigurationParseAndValidateRegionAndZoneFails", func(t *testing.T) {
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "", "", &gcpgenserver.Error{}
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("WhenCheckKmsConfigurationWhenVsaKmsConfigFoundSuccess", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Define mock response
		isHealthy := true
		mockResponse := &kms_configurations.V1betaCheckKmsConfigOK{
			Payload: &models.KmsConfigCheckV1beta{
				KmsConfigHealthCheck: &models.KmsConfigHealthCheckV1beta{
					HealthError:  "test-health-error",
					Instructions: "test-instructions",
					IsHealthy:    &isHealthy,
				},
				ServiceAccount: "test-service-account",
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{KmsAttributes: &vsaCoreModels.KmsAttributes{SdeKmsConfigUUID: "sdeUUID"},
			ServiceAccount: &vsaCoreModels.ServiceAccount{}}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Expect impersonation to succeed
		mockOrchestrator.EXPECT().AccessCryptoKeyAndEncryptDataWithImpersonation(mock.Anything, mock.Anything).Return(nil)
		// Expect health update to be called after successful impersonation
		mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the ServiceAccount value is as expected
		assert.Equal(t, "test-service-account", result.(*gcpgenserver.KmsConfigCheckV1beta).ServiceAccount.Value)
	})

	t.Run("WhenCheckKmsConfigurationWhenVsaKmsConfigNotFoundSuccess", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Define mock response
		isHealthy := true
		mockResponse := &kms_configurations.V1betaCheckKmsConfigOK{
			Payload: &models.KmsConfigCheckV1beta{
				KmsConfigHealthCheck: &models.KmsConfigHealthCheckV1beta{
					HealthError:  "test-health-error",
					Instructions: "test-instructions",
					IsHealthy:    &isHealthy,
				},
				ServiceAccount: "test-service-account",
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the ServiceAccount value is as expected
		assert.Equal(t, "test-service-account", result.(*gcpgenserver.KmsConfigCheckV1beta).ServiceAccount.Value)
	})

	t.Run("WhenCheckKmsConfigurationWithUnhealthyInitialCheck", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Define mock response with unhealthy status
		isHealthy := false
		mockResponse := &kms_configurations.V1betaCheckKmsConfigOK{
			Payload: &models.KmsConfigCheckV1beta{
				KmsConfigHealthCheck: &models.KmsConfigHealthCheckV1beta{
					HealthError:  "Key not accessible",
					Instructions: "Check permissions",
					IsHealthy:    &isHealthy,
				},
				ServiceAccount: "test-service-account",
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{KmsAttributes: &vsaCoreModels.KmsAttributes{SdeKmsConfigUUID: "sdeUUID"},
			ServiceAccount: &vsaCoreModels.ServiceAccount{}}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Should only update health when initially unhealthy, no impersonation attempt
		mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the ServiceAccount value is as expected
		assert.Equal(t, "test-service-account", result.(*gcpgenserver.KmsConfigCheckV1beta).ServiceAccount.Value)
	})

	t.Run("WhenCheckKmsConfigurationWithImpersonationFailure", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Define mock response with healthy initial status
		isHealthy := true
		mockResponse := &kms_configurations.V1betaCheckKmsConfigOK{
			Payload: &models.KmsConfigCheckV1beta{
				KmsConfigHealthCheck: &models.KmsConfigHealthCheckV1beta{
					HealthError:  "",
					Instructions: "test-instructions",
					IsHealthy:    &isHealthy,
				},
				ServiceAccount: "test-service-account",
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{KmsAttributes: &vsaCoreModels.KmsAttributes{SdeKmsConfigUUID: "sdeUUID"},
			ServiceAccount: &vsaCoreModels.ServiceAccount{}}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Expect impersonation to fail
		impersonationError := fmt.Errorf("impersonation failed: permission denied")
		mockOrchestrator.EXPECT().AccessCryptoKeyAndEncryptDataWithImpersonation(mock.Anything, mock.Anything).Return(impersonationError)
		// Expect health update to be called after failed impersonation
		mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Should return error response due to impersonation failure
		errorResult, ok := result.(*gcpgenserver.KmsConfigCheckV1beta)
		assert.True(t, ok)
		assert.False(t, errorResult.KmsConfigHealthCheck.Value.IsHealthy)
		assert.Contains(t, errorResult.KmsConfigHealthCheck.Value.HealthError.Value, "impersonation failed")
	})

	t.Run("WhenCheckKmsConfigurationHealthUpdateFails", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Define mock response
		isHealthy := true
		mockResponse := &kms_configurations.V1betaCheckKmsConfigOK{
			Payload: &models.KmsConfigCheckV1beta{
				KmsConfigHealthCheck: &models.KmsConfigHealthCheckV1beta{
					HealthError:  "",
					Instructions: "test-instructions",
					IsHealthy:    &isHealthy,
				},
				ServiceAccount: "test-service-account",
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		kmsConfig := &vsaCoreModels.KmsConfig{KmsAttributes: &vsaCoreModels.KmsAttributes{SdeKmsConfigUUID: "sdeUUID"},
			ServiceAccount: &vsaCoreModels.ServiceAccount{}}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		// Expect impersonation to succeed
		mockOrchestrator.EXPECT().AccessCryptoKeyAndEncryptDataWithImpersonation(mock.Anything, mock.Anything).Return(nil)
		// Expect health update to fail
		healthUpdateError := fmt.Errorf("database connection lost")
		mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).Return(nil, healthUpdateError)
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Should return internal server error due to health update failure
		errorResult, ok := result.(*gcpgenserver.V1betaCheckKmsConfigInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), errorResult.Code)
		assert.Equal(t, "Failed to update KMS config health", errorResult.Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithBadRequest", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &kms_configurations.V1betaCheckKmsConfigBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigBadRequest).Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithUnprocessableEntity", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(422)
		errorMessage := "Unprocessable error"
		mockError := &kms_configurations.V1betaCheckKmsConfigUnprocessableEntity{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigUnprocessableEntity).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigUnprocessableEntity).Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithConflict", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(409)
		errorMessage := "Conflict error"
		mockError := &kms_configurations.V1betaCheckKmsConfigConflict{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigConflict).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigConflict).Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithUnauthorized", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(401)
		errorMessage := "Unauthorized error"
		mockError := &kms_configurations.V1betaCheckKmsConfigUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigUnauthorized).Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithForbidden", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(403)
		errorMessage := "Forbidden error"
		mockError := &kms_configurations.V1betaCheckKmsConfigForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigForbidden).Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithTooManyRequests", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(429)
		errorMessage := "Too Many Requests error"
		mockError := &kms_configurations.V1betaCheckKmsConfigTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigTooManyRequests).Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithDefault", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(500)
		errorMessage := "default error"
		mockError := &kms_configurations.V1betaCheckKmsConfigDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigInternalServerError).Message)
	})

	t.Run("WhenDCheckKmsConfigurationFailsWithUnknownError", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		// Define input parameters
		params := gcpgenserver.V1betaCheckKmsConfigParams{}

		// Define mock error
		errorCode := float64(500)
		errorMessage := "unknown error during the check kms config"
		mockError := &kms_configurations.V1betaCheckKmsConfigInternalServerError{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCheckKmsConfig(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		// Call the method under test
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaCheckKmsConfigInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaCheckKmsConfigInternalServerError).Message)
	})

	// --- VCP path tests (CVP_HOST="") ---

	t.Run("VCP_ReturnsNotFoundWhenKmsConfigMissing", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).
			Return(nil, errors.NewNotFoundErr("KMS Configuration", nil))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		assert.NoError(t, err)
		notFoundResult, ok := result.(*gcpgenserver.V1betaCheckKmsConfigNotFound)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusNotFound), notFoundResult.Code)
	})

	t.Run("VCP_ReturnsHealthyWhenAccessSucceeds", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		kmsConfig := &vsaCoreModels.KmsConfig{
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123@cmek-project.iam.gserviceaccount.com",
			},
			ServiceAccount: &vsaCoreModels.ServiceAccount{},
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().AccessCryptoKeyAndEncryptDataWithImpersonation(mock.Anything, mock.Anything).Return(nil)
		mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).Return(kmsConfig, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		assert.NoError(t, err)
		healthResult, ok := result.(*gcpgenserver.KmsConfigCheckV1beta)
		assert.True(t, ok)
		assert.True(t, healthResult.KmsConfigHealthCheck.Value.IsHealthy)
		assert.Equal(t, "cmek-usea1-123@cmek-project.iam.gserviceaccount.com", healthResult.ServiceAccount.Value)
	})

	t.Run("VCP_ReturnsUnhealthyWhenAccessFails", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		kmsConfig := &vsaCoreModels.KmsConfig{
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123@cmek-project.iam.gserviceaccount.com",
			},
			ServiceAccount: &vsaCoreModels.ServiceAccount{},
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().AccessCryptoKeyAndEncryptDataWithImpersonation(mock.Anything, mock.Anything).
			Return(fmt.Errorf("key unreachable"))
		mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).Return(kmsConfig, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		assert.NoError(t, err)
		healthResult, ok := result.(*gcpgenserver.KmsConfigCheckV1beta)
		assert.True(t, ok)
		assert.False(t, healthResult.KmsConfigHealthCheck.Value.IsHealthy)
		assert.Contains(t, healthResult.KmsConfigHealthCheck.Value.HealthError.Value, "key unreachable")
	})

	t.Run("VCP_ReturnsInternalErrorWhenHealthUpdateFails", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		params := gcpgenserver.V1betaCheckKmsConfigParams{
			KmsConfigId:    "kms-config-id-1",
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		kmsConfig := &vsaCoreModels.KmsConfig{
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				VcpServiceAccountEmail: "cmek-usea1-123@cmek-project.iam.gserviceaccount.com",
			},
			ServiceAccount: &vsaCoreModels.ServiceAccount{},
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
		mockOrchestrator.EXPECT().AccessCryptoKeyAndEncryptDataWithImpersonation(mock.Anything, mock.Anything).Return(nil)
		mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("database error"))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

		assert.NoError(t, err)
		iseResult, ok := result.(*gcpgenserver.V1betaCheckKmsConfigInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(http.StatusInternalServerError), iseResult.Code)
	})
}

// V1betaListKmsConfigurations unittests
func TestV1betaListKmsConfigurations(t *testing.T) {
	t.Run("WhenListKmsConfigurationsSuccess", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Define request
		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock response
		kmsConfigurations := []*models.KmsConfigV1beta{}
		updatedTime := strfmt.DateTime(time.Now())
		description := "test-description"
		keyFullPath := "test-key-full-path"
		kmsConfig := models.KmsConfigV1beta{
			UUID:                "test-id",
			ServiceAccountEmail: "test-email",
			KeyFullPath:         &keyFullPath,
			KmsState:            "test-state",
			KmsStateDetails:     "test-details",
			Description:         &description,
			CreatedTime:         strfmt.DateTime(time.Now()),
			UpdatedTime:         &updatedTime,
			DeletedTime:         &updatedTime,
			Instructions:        "test-instructions",
		}
		kmsConfigurations = append(kmsConfigurations, &kmsConfig)
		mockResponse := &kms_configurations.V1betaListKmsConfigurationsOK{
			Payload: kmsConfigurations,
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(mockResponse, nil)

		// Set up the mock orchestrator behavior
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("KMS configuration not found", nil))

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the resource name is as expected
		kmsConfigsResponse := result.(*gcpgenserver.V1betaListKmsConfigurationsOKApplicationJSON)
		assert.Equal(t, "test-id", (*kmsConfigsResponse)[0].UUID.Value)
	})

	t.Run("WhenListKmsConfigurationFailsWithBadRequest", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &kms_configurations.V1betaListKmsConfigurationsBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListKmsConfigurationsBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListKmsConfigurationsBadRequest).Message)
	})

	t.Run("WhenListKmsConfigurationFailsWithConflict", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error
		errorCode := float64(409)
		errorMessage := "Conflict error"
		mockError := &kms_configurations.V1betaListKmsConfigurationsConflict{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListKmsConfigurationsConflict).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListKmsConfigurationsConflict).Message)
	})

	t.Run("WhenListKmsConfigurationFailsWithUnauthorized", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error
		errorCode := float64(401)
		errorMessage := "Unauthorized error"
		mockError := &kms_configurations.V1betaListKmsConfigurationsUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListKmsConfigurationsUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListKmsConfigurationsUnauthorized).Message)
	})

	t.Run("WhenListKmsConfigurationFailsWithForbidden", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error
		errorCode := float64(403)
		errorMessage := "Forbidden error"
		mockError := &kms_configurations.V1betaListKmsConfigurationsForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListKmsConfigurationsForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListKmsConfigurationsForbidden).Message)
	})

	t.Run("WhenListKmsConfigurationFailsWithTooManyRequests", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error
		errorCode := float64(429)
		errorMessage := "Too Many Requests error"
		mockError := &kms_configurations.V1betaListKmsConfigurationsTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListKmsConfigurationsTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListKmsConfigurationsTooManyRequests).Message)
	})

	t.Run("WhenListKmsConfigurationFailsWithDefault", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error
		errorCode := float64(500)
		errorMessage := "default error"
		mockError := &kms_configurations.V1betaListKmsConfigurationsDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListKmsConfigurationsInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListKmsConfigurationsInternalServerError).Message)
	})

	t.Run("WhenListKmsConfigurationFailsWithUnknownError", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		// Create a mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaListKmsConfigurationsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error
		errorCode := float64(500)
		errorMessage := "unknown error during the list kms configurations"
		mockError := &kms_configurations.V1betaListKmsConfigurationsInternalServerError{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListKmsConfigurationsInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListKmsConfigurationsInternalServerError).Message)
	})
}

func TestV1betaGetMultipleKmsConfigs(t *testing.T) {
	// Mock of VCP Orchestrator and Datastore
	mockLogger := log.NewLogger()
	store, err := database.NewTestStorage(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	err = database.ClearInMemoryDB(store.DB())
	if err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	orchInstance := factory.GetOrchestratorForProvider(store, nil)

	serviceAccounts := []*datamodel.ServiceAccount{
		{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10"}, Name: "ServiceAccount1"},
		{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2"},
	}
	err = store.DB().Create(serviceAccounts).Error
	if err != nil {
		t.Fatalf("Failed to create Service-Accounts table: %v", err)
	}

	kmsConfigs := []*datamodel.KmsConfig{
		{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ResourceID: "Resource-Id1-VCP", ServiceAccountID: &serviceAccounts[0].ID,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount1@account.com"}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ResourceID: "Resource-Id2-VCP", ServiceAccountID: &serviceAccounts[1].ID,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount2@account.com"}},
	}
	err = store.DB().Create(kmsConfigs).Error
	if err != nil {
		t.Fatalf("Failed to create KMS Configs table: %v", err)
	}

	// Mock of CVP Client
	mockClient := kms_configurations.NewMockClientService(t)

	params := gcpgenserver.V1betaGetMultipleKmsConfigsParams{
		LocationId:     "test-location",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
	}
	req := &gcpgenserver.KmsConfigIdListV1beta{
		KmsConfigIds: []string{"uuid3", "uuid4"},
	}

	kmsConfigsCVP := make([]*models.KmsConfigV1beta, 0)
	resourceId3 := "Resource-Id3-SDE"
	resourceId4 := "Resource-Id4-SDE"

	nowTime := strfmt.DateTime(time.Now())
	description := "Test-description"
	keyFullPath := "Test-key-SDE-full-path"
	kmsConfigsCVP = append(kmsConfigsCVP, &models.KmsConfigV1beta{
		UUID:                "uuid3",
		ServiceAccountEmail: "service-account3@sde.com",
		CreatedTime:         nowTime,
		UpdatedTime:         &nowTime,
		Description:         &description,
		ResourceID:          &resourceId3,
		KmsState:            "Test-state-3",
		KmsStateDetails:     "Test-state-details-3",
		Instructions:        "Test-instructions-3",
		KeyFullPath:         &keyFullPath,
	})
	kmsConfigsCVP = append(kmsConfigsCVP, &models.KmsConfigV1beta{
		UUID:                "uuid4",
		ServiceAccountEmail: "service-account4@sde.com",
		CreatedTime:         nowTime,
		UpdatedTime:         &nowTime,
		Description:         &description,
		ResourceID:          &resourceId4,
		KmsState:            "Test-state-4",
		KmsStateDetails:     "Test-state-details-4",
		Instructions:        "Test-instructions-4",
		KeyFullPath:         &keyFullPath,
	})

	handler := Handler{}
	handler.Orchestrator = orchInstance

	mockResponse := &kms_configurations.V1betaGetMultipleKmsConfigsOK{
		Payload: &kms_configurations.V1betaGetMultipleKmsConfigsOKBody{
			KmsConfigurations: kmsConfigsCVP,
		},
	}

	mockClient.EXPECT().
		V1betaGetMultipleKmsConfigs(mock.Anything).
		Return(mockResponse, nil)
	cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	t.Run("WhenGetMultipleKmsConfigsOnlyHasEntriesInSDE", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 2, len(result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations))
		assert.Equal(t, "Resource-Id3-SDE", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[0].ResourceId.Value)
		assert.Equal(t, "Resource-Id4-SDE", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[1].ResourceId.Value)
	})
	t.Run("WhenGetMultipleKmsConfigsOnlyHasEntriesInVCP", func(t *testing.T) {
		req := &gcpgenserver.KmsConfigIdListV1beta{
			KmsConfigIds: []string{"uuid1", "uuid2"},
		}
		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 2, len(result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations))
		assert.Equal(t, "Resource-Id1-VCP", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[0].ResourceId.Value)
		assert.Equal(t, "Resource-Id2-VCP", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[1].ResourceId.Value)
	})
	t.Run("WhenGetMultipleKmsConfigsHasSomeEntriesInVcpAndInSde", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		req := &gcpgenserver.KmsConfigIdListV1beta{
			KmsConfigIds: []string{"uuid2", "uuid3", "uuid4"},
		}
		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 3, len(result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations))
		assert.Equal(t, "Resource-Id2-VCP", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[0].ResourceId.Value)
		assert.Equal(t, "Resource-Id3-SDE", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[1].ResourceId.Value)
		assert.Equal(t, "Resource-Id4-SDE", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[2].ResourceId.Value)
	})
	t.Run("WhenGetMultipleKmsConfigsHasEntriesInVcpAndSde", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		req := &gcpgenserver.KmsConfigIdListV1beta{
			KmsConfigIds: []string{"uuid1", "uuid2", "uuid3", "uuid4"},
		}
		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 4, len(result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations))
		assert.Equal(t, "Resource-Id1-VCP", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[0].ResourceId.Value)
		assert.Equal(t, "Resource-Id2-VCP", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[1].ResourceId.Value)
		assert.Equal(t, "Resource-Id3-SDE", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[2].ResourceId.Value)
		assert.Equal(t, "Resource-Id4-SDE", result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations[3].ResourceId.Value)
	})
}

// TestV1betaGetMultipleKmsConfigs_SdeOverlapMerge covers SDE merge rules when the same UUID exists in VCP and SDE:
// VCP ERROR wins over SDE (state + VCP stateDetails preserved); VCP IN_USE only SDE ERROR overrides; VCP READY + SDE ERROR merges.
func TestV1betaGetMultipleKmsConfigs_SdeOverlapMerge(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	mockLogger := log.NewLogger()
	store, err := database.NewTestStorage(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}
	if err = database.ClearInMemoryDB(store.DB()); err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	orchInstance := factory.GetOrchestratorForProvider(store, nil)

	serviceAccounts := []*datamodel.ServiceAccount{
		{BaseModel: datamodel.BaseModel{ID: 111, UUID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaa1"}, Name: "SA-merge"},
	}
	if err = store.DB().Create(serviceAccounts).Error; err != nil {
		t.Fatalf("Failed to create Service-Accounts: %v", err)
	}

	saID := int64(111)
	uuidVCPError := "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbb1"
	uuidVCPInUse := "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbb2"
	uuidVCPReady := "bbbbbbbb-bbbb-4bbb-bbbb-bbbbbbbbbbb3"

	kmsConfigs := []*datamodel.KmsConfig{
		{
			BaseModel: datamodel.BaseModel{UUID: uuidVCPError, DeletedAt: nil},
			Name:      "kms-err", ResourceID: "res-vcp-err", ServiceAccountID: &saID,
			State: datamodel.LifeCycleStateError, StateDetails: "vcp-only-error-detail",
			KeyRing: "kr", KeyRingLocation: "us-east4", KeyName: "kn", KeyProjectID: "kp", CustomerProjectID: "cp",
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sde@example.com"},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: uuidVCPInUse, DeletedAt: nil},
			Name:      "kms-inuse", ResourceID: "res-vcp-inuse", ServiceAccountID: &saID,
			State: datamodel.LifeCycleStateInUse, StateDetails: "db-in-use-details",
			KeyRing: "kr", KeyRingLocation: "us-east4", KeyName: "kn", KeyProjectID: "kp", CustomerProjectID: "cp",
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sde@example.com"},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: uuidVCPReady, DeletedAt: nil},
			Name:      "kms-ready", ResourceID: "res-vcp-ready", ServiceAccountID: &saID,
			State: datamodel.LifeCycleStateREADY, StateDetails: datamodel.LifeCycleStateReadyDetails,
			KeyRing: "kr", KeyRingLocation: "us-east4", KeyName: "kn", KeyProjectID: "kp", CustomerProjectID: "cp",
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sde@example.com"},
		},
	}
	if err = store.DB().Create(kmsConfigs).Error; err != nil {
		t.Fatalf("Failed to create KMS configs: %v", err)
	}

	keyPath := "projects/kp/locations/us-east4/keyRings/kr/cryptoKeys/kn"
	nowTime := strfmt.DateTime(time.Now())
	makeSDE := func(uuid, kmsState, details string) *models.KmsConfigV1beta {
		return &models.KmsConfigV1beta{
			UUID:                uuid,
			KmsState:            kmsState,
			KmsStateDetails:     details,
			KeyFullPath:         &keyPath,
			CreatedTime:         nowTime,
			UpdatedTime:         &nowTime,
			ServiceAccountEmail: "sa@sde.com",
		}
	}
	sdeList := []*models.KmsConfigV1beta{
		makeSDE(uuidVCPError, datamodel.LifeCycleStateInUse, "sde-in-use-should-not-appear"),
		makeSDE(uuidVCPInUse, datamodel.LifeCycleStateError, "sde-error-detail"),
		makeSDE(uuidVCPReady, datamodel.LifeCycleStateError, "sde-error-for-ready"),
	}

	mockClient := kms_configurations.NewMockClientService(t)
	mockClient.EXPECT().
		V1betaGetMultipleKmsConfigs(mock.Anything).
		Return(&kms_configurations.V1betaGetMultipleKmsConfigsOK{
			Payload: &kms_configurations.V1betaGetMultipleKmsConfigsOKBody{KmsConfigurations: sdeList},
		}, nil)

	cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	handler := Handler{Orchestrator: orchInstance}
	params := gcpgenserver.V1betaGetMultipleKmsConfigsParams{
		LocationId:     "us-east4",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.KmsConfigIdListV1beta{
		KmsConfigIds: []string{uuidVCPError, uuidVCPInUse, uuidVCPReady},
	}

	result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	ok := result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK)
	assert.Len(t, ok.KmsConfigurations, 3)

	find := func(uuid string) *gcpgenserver.KmsConfigV1beta {
		for i := range ok.KmsConfigurations {
			if ok.KmsConfigurations[i].UUID.Value == uuid {
				return &ok.KmsConfigurations[i]
			}
		}
		t.Fatalf("kms config %s not found in response", uuid)
		return nil
	}

	gotErr := find(uuidVCPError)
	assert.Equal(t, gcpgenserver.KmsConfigV1betaKmsStateERROR, gotErr.KmsState.Value)
	assert.Equal(t, "vcp-only-error-detail", gotErr.KmsStateDetails.Value)

	gotInUse := find(uuidVCPInUse)
	assert.Equal(t, gcpgenserver.KmsConfigV1betaKmsStateERROR, gotInUse.KmsState.Value)
	assert.Equal(t, "sde-error-detail", gotInUse.KmsStateDetails.Value)

	gotReady := find(uuidVCPReady)
	assert.Equal(t, gcpgenserver.KmsConfigV1betaKmsStateERROR, gotReady.KmsState.Value)
	assert.Equal(t, "sde-error-for-ready", gotReady.KmsStateDetails.Value)
}

func TestV1betaGetMultipleKmsConfigsErrorConditions(t *testing.T) {
	// Mock of VCP Orchestrator and Datastore
	mockLogger := log.NewLogger()
	store, err := database.NewTestStorage(mockLogger)
	if err != nil {
		t.Fatalf("Failed to create test storage: %v", err)
	}

	err = database.ClearInMemoryDB(store.DB())
	if err != nil {
		t.Fatalf("Failed to clean up test storage: %v", err)
	}

	orchInstance := factory.GetOrchestratorForProvider(store, nil)

	serviceAccounts := []*datamodel.ServiceAccount{
		{BaseModel: datamodel.BaseModel{ID: int64(111), UUID: "uuid10"}, Name: "ServiceAccount1"},
		{BaseModel: datamodel.BaseModel{ID: int64(222), UUID: "uuid20"}, Name: "ServiceAccount2"},
	}
	err = store.DB().Create(serviceAccounts).Error
	if err != nil {
		t.Fatalf("Failed to create Service-Accounts table: %v", err)
	}

	kmsConfigs := []*datamodel.KmsConfig{
		{BaseModel: datamodel.BaseModel{UUID: "uuid1", DeletedAt: nil}, Name: "kmsConfig1", ResourceID: "Resource-Id1-SDE", ServiceAccountID: &serviceAccounts[0].ID,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount1@account.com"}},
		{BaseModel: datamodel.BaseModel{UUID: "uuid2", DeletedAt: nil}, Name: "kmsConfig2", ResourceID: "Resource-Id2-SDE", ServiceAccountID: &serviceAccounts[1].ID,
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "sdeServiceAccount2@account.com"}},
	}
	err = store.DB().Create(kmsConfigs).Error
	if err != nil {
		t.Fatalf("Failed to create KMS Configs table: %v", err)
	}

	params := gcpgenserver.V1betaGetMultipleKmsConfigsParams{
		LocationId:     "test-location",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
	}
	req := &gcpgenserver.KmsConfigIdListV1beta{
		KmsConfigIds: []string{""},
	}

	handler := Handler{}
	handler.Orchestrator = orchInstance

	t.Run("WhenGetMultipleKmsConfigsReturnsBadRequest", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &kms_configurations.V1betaGetMultipleKmsConfigsBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsBadRequest).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithUnauthorized", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		errorCode := float64(401)
		errorMessage := "Unauthorized error"
		mockError := &kms_configurations.V1betaGetMultipleKmsConfigsUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, mockError)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsUnauthorized).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithForbidden", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		errorCode := float64(403)
		errorMessage := "Forbidden error"
		mockError := &kms_configurations.V1betaGetMultipleKmsConfigsForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, mockError)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsForbidden).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithNotFound", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		errorCode := float64(404)
		errorMessage := "Not found"
		mockError := &kms_configurations.V1betaGetMultipleKmsConfigsNotFound{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, mockError)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsNotFound).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsNotFound).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithErrorTooManyRequests", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		errorCode := float64(429)
		errorMessage := "Too many requests"
		mockError := &kms_configurations.V1betaGetMultipleKmsConfigsTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, mockError)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsTooManyRequests).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithDefault", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		errorCode := float64(500)
		errorMessage := "Default error"
		mockError := &kms_configurations.V1betaGetMultipleKmsConfigsDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, mockError)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithUnknownError", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		errorCode := float64(500)
		errorMessage := "Unknown error encountered during Get Multiple KMS configurations operation"
		mockError := &kms_configurations.V1betaGetMultipleKmsConfigsInternalServerError{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, mockError)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithCvpResponseNil", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		expectedErrorMsg := "Unknown error encountered during Get Multiple KMS configurations operation"
		expectedErrorCode := float64(500)

		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(nil, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, expectedErrorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Code)
		assert.Equal(t, expectedErrorMsg, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Message)
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithCvpPayloadNil", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		req := &gcpgenserver.KmsConfigIdListV1beta{
			KmsConfigIds: []string{"uuid5"},
		}
		mockResponse := &kms_configurations.V1betaGetMultipleKmsConfigsOK{
			Payload: nil,
		}
		mockClient := kms_configurations.NewMockClientService(t)
		mockClient.EXPECT().
			V1betaGetMultipleKmsConfigs(mock.Anything).
			Return(mockResponse, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 0, len(result.(*gcpgenserver.V1betaGetMultipleKmsConfigsOK).KmsConfigurations))
	})
	t.Run("WhenGetMultipleKmsConfigsFailsWithUnknownErrorFromVCP", func(tt *testing.T) {
		origCVPHost := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origCVPHost }()
		cvp.CVP_HOST = "localhost:8009"

		expectedErrorMsg := "Unknown error encountered during Get Multiple KMS configurations operation"
		expectedErrorCode := float64(500)
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockOrchestrator.EXPECT().GetMultipleKMSConfigs(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("internal error"))
		handler := Handler{
			Orchestrator: mockOrchestrator,
		}

		result, err := handler.V1betaGetMultipleKmsConfigs(context.Background(), req, params)
		assert.NoError(tt, err)
		assert.NotNil(tt, result)
		assert.Equal(tt, expectedErrorMsg, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Message)
		assert.Equal(tt, expectedErrorCode, result.(*gcpgenserver.V1betaGetMultipleKmsConfigsInternalServerError).Code)
	})
}

func TestGetKmsInstructions(t *testing.T) {
	t.Run("ReturnsEmptyStringWhenKmsAttributesIsNil", func(t *testing.T) {
		kmsConfig := &vsaCoreModels.KmsConfig{}
		instructions := getKmsInstructions(kmsConfig)
		assert.Equal(t, "", instructions)
	})
	t.Run("ReturnsEmptyStringWhenServiceAccountEmailIsEmpty", func(t *testing.T) {
		kmsConfig := &vsaCoreModels.KmsConfig{}
		kmsConfig.KmsAttributes = &vsaCoreModels.KmsAttributes{
			SdeServiceAccountEmail: "",
		}
		instructions := getKmsInstructions(kmsConfig)
		assert.Equal(t, "", instructions)
	})
	t.Run("UsesCustomerProjectIDWhenKeyProjectIDIsEmpty", func(t *testing.T) {
		kmsConfig := &vsaCoreModels.KmsConfig{
			KeyProjectID:      "",
			CustomerProjectID: "customer-project-id",
			KeyName:           "key-name",
			KeyRing:           "key-ring",
			KeyRingLocation:   "key-ring-location",
		}
		kmsConfig.KmsAttributes = &vsaCoreModels.KmsAttributes{
			SdeServiceAccountEmail: "service-account@test.com",
		}
		instructions := getKmsInstructions(kmsConfig)
		assert.Contains(t, instructions, "customer-project-id")
		assert.Contains(t, instructions, "serviceAccount:service-account@test.com")
	})
	t.Run("GeneratesInstructionsWithKeyProjectID", func(t *testing.T) {
		kmsConfig := &vsaCoreModels.KmsConfig{
			KeyProjectID:    "key-project-id",
			KeyName:         "key-name",
			KeyRing:         "key-ring",
			KeyRingLocation: "key-ring-location",
		}
		kmsConfig.KmsAttributes = &vsaCoreModels.KmsAttributes{
			SdeServiceAccountEmail: "service-account@test.com",
		}
		expectedOutput := `Please copy and paste the commands listed below into Google Cloud Shell in the project that contains the key ring. The commands create a KMS role and assign it to the CVS service account so that it can access the key.
## CREATE KMS role ## gcloud iam roles create cmekNetAppVolumesRole --project=key-project-id --title='cmekNetAppVolumesRole' --description='custom cmek cvs role' --permissions=cloudkms.cryptoKeyVersions.get,cloudkms.cryptoKeyVersions.list,cloudkms.cryptoKeyVersions.useToDecrypt,cloudkms.cryptoKeyVersions.useToEncrypt,cloudkms.cryptoKeys.get,cloudkms.keyRings.get,cloudkms.locations.get,cloudkms.locations.list,resourcemanager.projects.get --stage=GA
 ## ASSIGN role and give KEY ACCESS to CVS service account ## gcloud kms keys add-iam-policy-binding key-name --project=key-project-id --keyring key-ring --location key-ring-location --member serviceAccount:service-account@test.com --role projects/key-project-id/roles/cmekNetAppVolumesRole`

		instructions := getKmsInstructions(kmsConfig)
		assert.Contains(t, instructions, "key-project-id")
		assert.Contains(t, instructions, "key-name")
		assert.Contains(t, instructions, "key-ring")
		assert.Contains(t, instructions, "key-ring-location")
		assert.Contains(t, instructions, "serviceAccount:service-account@test.com")
		assert.Equal(t, expectedOutput, instructions)
	})
}

func TestConvertOrchestratorModelToKmsConfigV1beta(t *testing.T) {
	t.Run("ReturnsValidKmsConfigV1betaWhenAllFieldsArePopulated", func(t *testing.T) {
		expectedDate := time.Date(2022, time.February, 2, 2, 2, 2, 2, time.UTC)
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:           "ACTIVE",
			KeyProjectID:    "test-project-id",
			KeyRingLocation: "test-location",
			KeyRing:         "test-key-ring",
			KeyName:         "test-key-name",
			StateDetails:    "test-state-details",
			Description:     "test-description",
			ResourceID:      "test-resource-id",
		}
		kmsConfig.BaseModel = vsaCoreModels.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: expectedDate,
			UpdatedAt: expectedDate,
			DeletedAt: &expectedDate,
		}
		kmsConfig.KmsAttributes = &vsaCoreModels.KmsAttributes{
			SdeServiceAccountEmail: "test-service-account@test.com",
		}

		result := convertOrchestratorModelToKmsConfigV1beta(kmsConfig)

		assert.NotNil(t, result)
		assert.Equal(t, kmsConfig.UUID, result.UUID.Value)
		assert.Equal(t, kmsConfig.KmsAttributes.SdeServiceAccountEmail, result.ServiceAccountEmail.Value)
		assert.Contains(t, result.KeyFullPath, kmsConfig.KeyProjectID)
		assert.Contains(t, result.KeyFullPath, kmsConfig.KeyRingLocation)
		assert.Contains(t, result.KeyFullPath, kmsConfig.KeyRing)
		assert.Contains(t, result.KeyFullPath, kmsConfig.KeyName)
		assert.Equal(t, kmsConfig.State, string(result.KmsState.Value))
		assert.Equal(t, kmsConfig.StateDetails, result.KmsStateDetails.Value)
		assert.Equal(t, kmsConfig.Description, result.Description.Value)
		assert.Equal(t, kmsConfig.ResourceID, result.ResourceId.Value)
		assert.Equal(t, expectedDate, result.CreatedTime.Value)
		assert.Equal(t, expectedDate, result.UpdatedTime.Value)
		assert.Equal(t, expectedDate, result.DeletedTime.Value)
	})
	t.Run("HandlesNilDeletedTimeGracefully", func(t *testing.T) {
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:           "ACTIVE",
			KeyProjectID:    "test-project-id",
			KeyRingLocation: "test-location",
			KeyRing:         "test-key-ring",
			KeyName:         "test-key-name",
			StateDetails:    "test-state-details",
			Description:     "test-description",
			ResourceID:      "test-resource-id",
		}
		kmsConfig.BaseModel = vsaCoreModels.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: nil,
		}
		kmsConfig.KmsAttributes = &vsaCoreModels.KmsAttributes{
			SdeServiceAccountEmail: "test-service-account@test.com",
		}
		zeroTime := gcpgenserver.OptDateTime{Value: time.Time{}}

		result := convertOrchestratorModelToKmsConfigV1beta(kmsConfig)
		assert.NotNil(t, result)
		assert.Equal(t, zeroTime, result.DeletedTime)
	})
	t.Run("HandlesNilKmsAttributesGracefully", func(t *testing.T) {
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:           "ACTIVE",
			KeyProjectID:    "test-project-id",
			KeyRingLocation: "test-location",
			KeyRing:         "test-key-ring",
			KeyName:         "test-key-name",
			StateDetails:    "test-state-details",
			Description:     "test-description",
			ResourceID:      "test-resource-id",
		}
		kmsConfig.BaseModel = vsaCoreModels.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			DeletedAt: &time.Time{},
		}

		result := convertOrchestratorModelToKmsConfigV1beta(kmsConfig)
		assert.NotNil(t, result)
		assert.Equal(t, gcpgenserver.OptString{Value: ""}, result.ServiceAccountEmail)
	})
	t.Run("IncludesGrantSuffixInKeyFullPathWhenKmsGrantIsSet", func(t *testing.T) {
		kmsConfig := &vsaCoreModels.KmsConfig{
			State:           "ACTIVE",
			KeyProjectID:    "test-project-id",
			KeyRingLocation: "test-location",
			KeyRing:         "test-key-ring",
			KeyName:         "test-key-name",
			KmsGrant:        "test-grant-id",
		}
		kmsConfig.BaseModel = vsaCoreModels.BaseModel{UUID: "test-uuid"}

		result := convertOrchestratorModelToKmsConfigV1beta(kmsConfig)
		assert.NotNil(t, result)
		assert.Equal(t,
			"projects/test-project-id/locations/test-location/keyRings/test-key-ring/cryptoKeys/test-key-name/grants/test-grant-id",
			result.KeyFullPath)
	})
}

func TestCategorizeCvpClientErrorsForCreateKmsConfigs(t *testing.T) {
	t.Run("ReturnsUnprocessableEntityOnUnprocessableEntityError", func(t *testing.T) {
		code := float64(422)
		msg := "unprocessable"
		err := &kms_configurations.V1betaCreateKmsConfigurationUnprocessableEntity{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationUnprocessableEntity{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaCreateKmsConfigurationUnprocessableEntity).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaCreateKmsConfigurationUnprocessableEntity).Message)
	})

	t.Run("ReturnsConflictOnConflictError", func(t *testing.T) {
		code := float64(409)
		msg := "conflict"
		err := &kms_configurations.V1betaCreateKmsConfigurationConflict{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationConflict{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaCreateKmsConfigurationConflict).Message)
	})

	t.Run("ReturnsBadRequestOnBadRequestError", func(t *testing.T) {
		code := float64(400)
		msg := "bad request"
		err := &kms_configurations.V1betaCreateKmsConfigurationBadRequest{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationBadRequest{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest).Message)
	})

	t.Run("ReturnsUnauthorizedOnUnauthorizedError", func(t *testing.T) {
		code := float64(401)
		msg := "unauthorized"
		err := &kms_configurations.V1betaCreateKmsConfigurationUnauthorized{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationUnauthorized{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaCreateKmsConfigurationUnauthorized).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaCreateKmsConfigurationUnauthorized).Message)
	})

	t.Run("ReturnsForbiddenOnForbiddenError", func(t *testing.T) {
		code := float64(403)
		msg := "forbidden"
		err := &kms_configurations.V1betaCreateKmsConfigurationForbidden{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationForbidden{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaCreateKmsConfigurationForbidden).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaCreateKmsConfigurationForbidden).Message)
	})

	t.Run("ReturnsTooManyRequestsOnTooManyRequestsError", func(t *testing.T) {
		code := float64(429)
		msg := "too many requests"
		err := &kms_configurations.V1betaCreateKmsConfigurationTooManyRequests{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationTooManyRequests{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaCreateKmsConfigurationTooManyRequests).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaCreateKmsConfigurationTooManyRequests).Message)
	})

	t.Run("ReturnsInternalServerErrorOnDefaultError", func(t *testing.T) {
		code := float64(500)
		msg := "internal"
		err := &kms_configurations.V1betaCreateKmsConfigurationDefault{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Message)
	})

	t.Run("ReturnsInternalServerErrorOnUnknownErrorType", func(t *testing.T) {
		err := fmt.Errorf("some unknown error")
		res, _ := categorizeCvpClientErrorsForCreateKmsConfigs(err)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{}, res)
		assert.Equal(t, float64(500), res.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Code)
		assert.Equal(t, "unknown error during the create kms config", res.(*gcpgenserver.V1betaCreateKmsConfigurationInternalServerError).Message)
	})
}

func TestV1betaListKmsConfigurations_UncoveredScenarios(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	params := gcpgenserver.V1betaListKmsConfigurationsParams{
		LocationId:     "test-location",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
	}

	t.Run("WhenResIsNil", func(t *testing.T) {
		// Setup mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Mock the CVP client to return nil response
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(nil, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Setup mock orchestrator factory
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)

		// Assertions - should return InternalServerError when res is nil
		assert.NoError(t, err)
		resultPtr, ok := result.(*gcpgenserver.V1betaListKmsConfigurationsInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), resultPtr.Code)
		assert.Equal(t, "unknown error during the list kms configurations", resultPtr.Message)
	})

	t.Run("WhenResPayloadIsNil", func(t *testing.T) {
		// Setup mock client
		mockClient := kms_configurations.NewMockClientService(t)

		// Mock the CVP client to return response with nil payload
		mockRes := &kms_configurations.V1betaListKmsConfigurationsOK{
			Payload: nil,
		}
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(mockRes, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Setup mock orchestrator factory
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)

		// Assertions - should return InternalServerError when res.Payload is nil
		assert.NoError(t, err)
		resultPtr, ok := result.(*gcpgenserver.V1betaListKmsConfigurationsInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), resultPtr.Code)
		assert.Equal(t, "unknown error during the list kms configurations", resultPtr.Message)
	})

	t.Run("WhenOrchestratorGetKmsConfigReturnsNonNotFoundError", func(t *testing.T) {
		// Setup mock client with valid response
		mockClient := kms_configurations.NewMockClientService(t)
		keyFullPath := "projects/test-project/locations/us-central1/keyRings/test-keyring/cryptoKeys/test-key"
		resourceID := "resource-123"
		kmsConfig := models.KmsConfigV1beta{
			UUID:        "test-id",
			ResourceID:  &resourceID,
			KeyFullPath: &keyFullPath,
			KmsState:    "ACTIVE",
		}
		mockRes := &kms_configurations.V1betaListKmsConfigurationsOK{
			Payload: []*models.KmsConfigV1beta{&kmsConfig},
		}
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(mockRes, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Setup mock orchestrator factory that returns a non-NotFoundErr error
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().
			GetKmsConfig(mock.Anything, mock.MatchedBy(func(params *common.GetKmsConfigParams) bool {
				return params.UUID == "test-id" && params.AccountName == "12345"
			})).
			Return(nil, fmt.Errorf("database connection error"))

		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)

		// Assertions - should return InternalServerError when orchestrator returns non-NotFoundErr
		assert.NoError(t, err)
		resultPtr, ok := result.(*gcpgenserver.V1betaListKmsConfigurationsInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), resultPtr.Code)
		assert.Equal(t, "unknown error during the list kms configurations", resultPtr.Message)
	})

	t.Run("WhenSdeKmsConfigStateIsError", func(t *testing.T) {
		// Setup mock client with valid response
		mockClient := kms_configurations.NewMockClientService(t)
		keyFullPath := "projects/test-project/locations/us-central1/keyRings/test-keyring/cryptoKeys/test-key"
		resourceID := "resource-123"
		kmsConfig := models.KmsConfigV1beta{
			UUID:        "test-id",
			ResourceID:  &resourceID,
			KeyFullPath: &keyFullPath,
			KmsState:    datamodel.LifeCycleStateError,
		}
		mockRes := &kms_configurations.V1betaListKmsConfigurationsOK{
			Payload: []*models.KmsConfigV1beta{&kmsConfig},
		}
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(mockRes, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Setup mock orchestrator factory that returns a KmsConfig with Error state
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		vcpKmsConfig := &vsaCoreModels.KmsConfig{
			BaseModel: vsaCoreModels.BaseModel{
				UUID: "test-id",
			},
			State: datamodel.LifeCycleStateCreated,
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				SdeServiceAccountEmail: "test@example.com",
			},
			KeyProjectID:    "test-project",
			KeyRingLocation: "us-central1",
			KeyRing:         "test-keyring",
			KeyName:         "test-key",
			ResourceID:      "resource-123",
		}
		mockOrchestrator.EXPECT().
			GetKmsConfig(mock.Anything, mock.MatchedBy(func(params *common.GetKmsConfigParams) bool {
				return params.UUID == "test-id" && params.AccountName == "12345"
			})).
			Return(vcpKmsConfig, nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)

		// Assertions - state should be overridden to "ERROR"
		assert.NoError(t, err)
		resultPtr, ok := result.(*gcpgenserver.V1betaListKmsConfigurationsOKApplicationJSON)
		assert.True(t, ok)
		resultSlice := *resultPtr
		assert.Len(t, resultSlice, 1)
		assert.Equal(t, "test-id", resultSlice[0].UUID.Value)
		assert.Equal(t, "ERROR", string(resultSlice[0].KmsState.Value))
	})

	t.Run("WhenSdeKmsConfigStateIsInUse", func(t *testing.T) {
		// Setup mock client with valid response
		mockClient := kms_configurations.NewMockClientService(t)
		keyFullPath := "projects/test-project/locations/us-central1/keyRings/test-keyring/cryptoKeys/test-key"
		resourceID := "resource-123"
		kmsConfig := models.KmsConfigV1beta{
			UUID:        "test-id",
			ResourceID:  &resourceID,
			KeyFullPath: &keyFullPath,
			KmsState:    datamodel.LifeCycleStateInUse,
		}
		mockRes := &kms_configurations.V1betaListKmsConfigurationsOK{
			Payload: []*models.KmsConfigV1beta{&kmsConfig},
		}
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(mockRes, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Setup mock orchestrator factory that returns a KmsConfig with InUse state
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		vcpKmsConfig := &vsaCoreModels.KmsConfig{
			BaseModel: vsaCoreModels.BaseModel{
				UUID: "test-id",
			},
			State: datamodel.LifeCycleStateCreated,
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				SdeServiceAccountEmail: "test@example.com",
			},
			KeyProjectID:    "test-project",
			KeyRingLocation: "us-central1",
			KeyRing:         "test-keyring",
			KeyName:         "test-key",
			ResourceID:      "resource-123",
		}
		mockOrchestrator.EXPECT().
			GetKmsConfig(mock.Anything, mock.MatchedBy(func(params *common.GetKmsConfigParams) bool {
				return params.UUID == "test-id" && params.AccountName == "12345"
			})).
			Return(vcpKmsConfig, nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)

		// Assertions - state should be overridden to "IN_USE"
		assert.NoError(t, err)
		resultPtr, ok := result.(*gcpgenserver.V1betaListKmsConfigurationsOKApplicationJSON)
		assert.True(t, ok)
		resultSlice := *resultPtr
		assert.Len(t, resultSlice, 1)
		assert.Equal(t, "test-id", resultSlice[0].UUID.Value)
		assert.Equal(t, "IN_USE", string(resultSlice[0].KmsState.Value))
	})

	t.Run("WhenSdeKmsConfigStateIsOtherThanErrorOrInUse", func(t *testing.T) {
		// Setup mock client with valid response
		mockClient := kms_configurations.NewMockClientService(t)
		keyFullPath := "projects/test-project/locations/us-central1/keyRings/test-keyring/cryptoKeys/test-key"
		resourceID := "resource-123"
		kmsConfig := models.KmsConfigV1beta{
			UUID:        "test-id",
			ResourceID:  &resourceID,
			KeyFullPath: &keyFullPath,
			KmsState:    datamodel.LifeCycleStateREADY,
		}
		mockRes := &kms_configurations.V1betaListKmsConfigurationsOK{
			Payload: []*models.KmsConfigV1beta{&kmsConfig},
		}
		mockClient.EXPECT().
			V1betaListKmsConfigurations(mock.Anything).
			Return(mockRes, nil)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Setup mock orchestrator factory that returns a KmsConfig with some other state
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		vcpKmsConfig := &vsaCoreModels.KmsConfig{
			BaseModel: vsaCoreModels.BaseModel{
				UUID: "test-id",
			},
			State: datamodel.LifeCycleStateCreated,
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				SdeServiceAccountEmail: "test@example.com",
			},
			KeyProjectID:    "test-project",
			KeyRingLocation: "us-central1",
			KeyRing:         "test-keyring",
			KeyName:         "test-key",
			ResourceID:      "resource-123",
		}
		mockOrchestrator.EXPECT().
			GetKmsConfig(mock.Anything, mock.MatchedBy(func(params *common.GetKmsConfigParams) bool {
				return params.UUID == "test-id" && params.AccountName == "12345"
			})).
			Return(vcpKmsConfig, nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		// Call the method under test
		result, err := handler.V1betaListKmsConfigurations(context.Background(), params)

		// Assertions - VCP state should be displayed
		assert.NoError(t, err)
		resultPtr, ok := result.(*gcpgenserver.V1betaListKmsConfigurationsOKApplicationJSON)
		assert.True(t, ok)
		resultSlice := *resultPtr
		assert.Len(t, resultSlice, 1)
		assert.Equal(t, "test-id", resultSlice[0].UUID.Value)
		assert.Equal(t, "KEY_CHECK_PENDING", string(resultSlice[0].KmsState.Value)) // VCP state is shown
	})
}

func TestConvertErrorToKmsConfigCheckV1beta_ReturnsUnhealthyWithErrorMessage(t *testing.T) {
	t.Run("ReturnsUnhealthyWithErrorMessage", func(t *testing.T) {
		err := errors.New("access denied")
		kmsConfig := &vsaCoreModels.KmsConfig{ResourceID: "resource-id", KmsAttributes: &vsaCoreModels.KmsAttributes{SdeServiceAccountEmail: "some"}}
		result := convertErrorToKmsConfigCheckV1beta(err, kmsConfig)
		assert.False(t, result.KmsConfigHealthCheck.Value.IsHealthy)
		assert.Equal(t, "access denied", result.KmsConfigHealthCheck.Value.HealthError.Value)
		assert.NotEmpty(t, result.ServiceAccount)
	})
}

func TestCategorizeCvpClientErrorsForUpdate(t *testing.T) {
	logger := log.NewLogger()

	t.Run("ReturnsBadRequestOnBadRequestError", func(t *testing.T) {
		code := float64(400)
		msg := "bad request"
		err := &kms_configurations.V1betaUpdateKmsConfigurationBadRequest{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res := categorizeCvpClientErrorsForUpdate(err, logger)
		assert.IsType(t, &gcpgenserver.V1betaUpdateKmsConfigurationBadRequest{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaUpdateKmsConfigurationBadRequest).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaUpdateKmsConfigurationBadRequest).Message)
	})

	t.Run("ReturnsUnauthorizedOnUnauthorizedError", func(t *testing.T) {
		code := float64(401)
		msg := "unauthorized"
		err := &kms_configurations.V1betaUpdateKmsConfigurationUnauthorized{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res := categorizeCvpClientErrorsForUpdate(err, logger)
		assert.IsType(t, &gcpgenserver.V1betaUpdateKmsConfigurationUnauthorized{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaUpdateKmsConfigurationUnauthorized).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaUpdateKmsConfigurationUnauthorized).Message)
	})

	t.Run("ReturnsForbiddenOnForbiddenError", func(t *testing.T) {
		code := float64(403)
		msg := "forbidden"
		err := &kms_configurations.V1betaUpdateKmsConfigurationForbidden{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res := categorizeCvpClientErrorsForUpdate(err, logger)
		assert.IsType(t, &gcpgenserver.V1betaUpdateKmsConfigurationForbidden{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaUpdateKmsConfigurationForbidden).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaUpdateKmsConfigurationForbidden).Message)
	})

	t.Run("ReturnsNotFoundOnNotFoundError", func(t *testing.T) {
		code := float64(404)
		msg := "not found"
		err := &kms_configurations.V1betaUpdateKmsConfigurationNotFound{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res := categorizeCvpClientErrorsForUpdate(err, logger)
		assert.IsType(t, &gcpgenserver.V1betaUpdateKmsConfigurationNotFound{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaUpdateKmsConfigurationNotFound).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaUpdateKmsConfigurationNotFound).Message)
	})

	t.Run("ReturnsConflictOnConflictError", func(t *testing.T) {
		code := float64(409)
		msg := "conflict"
		err := &kms_configurations.V1betaUpdateKmsConfigurationConflict{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res := categorizeCvpClientErrorsForUpdate(err, logger)
		assert.IsType(t, &gcpgenserver.V1betaUpdateKmsConfigurationConflict{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaUpdateKmsConfigurationConflict).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaUpdateKmsConfigurationConflict).Message)
	})

	t.Run("ReturnsInternalServerErrorOnInternalServerError", func(t *testing.T) {
		code := float64(500)
		msg := "internal server error"
		err := &kms_configurations.V1betaUpdateKmsConfigurationInternalServerError{
			Payload: &models.Error{Code: code, Message: msg},
		}
		res := categorizeCvpClientErrorsForUpdate(err, logger)
		assert.IsType(t, &gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError{}, res)
		assert.Equal(t, code, res.(*gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError).Code)
		assert.Equal(t, msg, res.(*gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError).Message)
	})

	t.Run("ReturnsInternalServerErrorOnUnknownErrorType", func(t *testing.T) {
		err := fmt.Errorf("some unknown error")
		res := categorizeCvpClientErrorsForUpdate(err, logger)
		assert.IsType(t, &gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError{}, res)
		assert.Equal(t, float64(500), res.(*gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError).Code)
		assert.Equal(t, "Unknown error encountered during Update KMS configurations operation", res.(*gcpgenserver.V1betaUpdateKmsConfigurationInternalServerError).Message)
	})
}

func TestV1betaCheckKmsConfiguration_RoutesVCPCreatedConfigToVCPPath(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = "localhost:8009"

	originalForceFlag := utils.ForceVCPKMSPathForTesting
	utils.ForceVCPKMSPathForTesting = true
	defer func() { utils.ForceVCPKMSPathForTesting = originalForceFlag }()

	originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "us-east4", nil
	}

	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		t.Fatalf("createClient should not be called for VCP-created KMS config")
		return cvpapi.Cvp{}
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}

	params := gcpgenserver.V1betaCheckKmsConfigParams{
		KmsConfigId:    "kms-config-id-1",
		LocationId:     "test-location",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
	}

	kmsConfig := &vsaCoreModels.KmsConfig{
		KmsAttributes: &vsaCoreModels.KmsAttributes{
			CreationMode:           datamodel.KmsCreationModeVCP,
			VcpServiceAccountEmail: "vcp-sa@test-project.iam.gserviceaccount.com",
		},
	}
	mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsConfig, nil)
	mockOrchestrator.EXPECT().AccessCryptoKeyAndEncryptDataWithImpersonation(mock.Anything, mock.Anything).Return(nil)
	mockOrchestrator.EXPECT().CheckAndUpdateKmsConfigHealth(mock.Anything, mock.Anything).Return(kmsConfig, nil)

	result, err := handler.V1betaCheckKmsConfig(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	_, ok := result.(*gcpgenserver.KmsConfigCheckV1beta)
	assert.True(t, ok)
}

func TestV1betaCheckKmsConfigVCP_InternalGetError(t *testing.T) {
	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrchestrator}

	params := gcpgenserver.V1betaCheckKmsConfigParams{
		KmsConfigId:    "kms-config-id-1",
		LocationId:     "us-east4",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("db unavailable"))

	res, err := handler.v1betaCheckKmsConfigVCP(context.Background(), params)
	assert.NoError(t, err)
	internalErr, ok := res.(*gcpgenserver.V1betaCheckKmsConfigInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusInternalServerError), internalErr.Code)
}

func TestCategorizeCreateKmsConfigOrchestratorErrors_BadRequest(t *testing.T) {
	res, err := categorizeCreateKmsConfigOrchestratorErrors(errors.NewBadRequestErr("bad req"))
	assert.NoError(t, err)
	badReq, ok := res.(*gcpgenserver.V1betaCreateKmsConfigurationBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(http.StatusBadRequest), badReq.Code)
	assert.Contains(t, badReq.Message, "bad req")
}

func TestCreateSDEKmsConfig_Branches(t *testing.T) {
	origCreateClient := createClient
	defer func() { createClient = origCreateClient }()

	params := gcpgenserver.V1betaCreateKmsConfigurationParams{
		LocationId:     "us-east4",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.KmsConfigV1beta{
		KeyFullPath: "projects/p/locations/us-east4/keyRings/r/cryptoKeys/k",
		ResourceId:  gcpgenserver.NewOptString("res-id"),
		Description: gcpgenserver.NewOptString("desc"),
	}

	t.Run("CreateJobFailureReturnsInternalServerError", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return cvpapi.Cvp{} }
		mockOrchestrator.EXPECT().CreateJob(mock.Anything, mock.MatchedBy(func(p *common.CreateJobParams) bool {
			return p != nil && p.CorrelationID == "corr-id"
		})).Return(nil, fmt.Errorf("queue down"))
		handler := Handler{Orchestrator: mockOrchestrator}

		out, errResp := handler.createSDEKmsConfig(context.Background(), req, params)
		assert.Nil(t, out)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{}, errResp)
	})

	t.Run("CvpCreateErrorAndUpdateJobStatusFailure", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockClient := kms_configurations.NewMockClientService(t)
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{KmsConfigurations: mockClient}
		}
		job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-1"}, TrackingID: 1234}
		mockOrchestrator.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(job, nil)
		mockClient.EXPECT().V1betaCreateKmsConfiguration(mock.Anything).Return(nil, fmt.Errorf("cvp error"))
		mockOrchestrator.EXPECT().
			UpdateJobStatus(mock.Anything, "job-1", string(datamodel.JobsStateERROR), 1234, mock.AnythingOfType("string")).
			Return(fmt.Errorf("update status failed"))
		handler := Handler{Orchestrator: mockOrchestrator}

		out, errResp := handler.createSDEKmsConfig(context.Background(), req, params)
		assert.Nil(t, out)
		assert.NotNil(t, errResp)
	})

	t.Run("ParseFailureAndClearGracePeriodUpdateFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockClient := kms_configurations.NewMockClientService(t)
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{KmsConfigurations: mockClient}
		}
		job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "job-2"}}
		mockOrchestrator.EXPECT().CreateJob(mock.Anything, mock.Anything).Return(job, nil)
		done := false
		mockClient.EXPECT().V1betaCreateKmsConfiguration(mock.Anything).Return(&kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "op",
				Done:     &done,
				Response: "bad-response-type",
			},
		}, nil)
		mockOrchestrator.EXPECT().UpdateJobAttributes(mock.Anything, "job-2", mock.Anything).Return(fmt.Errorf("update attrs failed"))
		handler := Handler{Orchestrator: mockOrchestrator}

		out, errResp := handler.createSDEKmsConfig(context.Background(), req, params)
		assert.Nil(t, out)
		assert.IsType(t, &gcpgenserver.V1betaCreateKmsConfigurationInternalServerError{}, errResp)
	})
}

func TestV1betaDescribeKmsConfiguration_Branches(t *testing.T) {
	origCreateClient := createClient
	defer func() { createClient = origCreateClient }()
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()

	params := gcpgenserver.V1betaDescribeKmsConfigurationParams{
		KmsConfigId:    "kms-id",
		LocationId:     "us-east4",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}

	originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-east4", "us-east4", nil
	}

	t.Run("ReturnsModelDirectlyForVCPCreatedConfig", func(t *testing.T) {
		cvp.CVP_HOST = "localhost:8009"
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(&vsaCoreModels.KmsConfig{
			BaseModel: vsaCoreModels.BaseModel{UUID: "kms-id"},
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				CreationMode: datamodel.KmsCreationModeVCP,
			},
		}, nil)
		handler := Handler{Orchestrator: mockOrchestrator}

		res, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.KmsConfigV1beta{}, res)
	})

	t.Run("ReturnsInternalServerErrorForNonNotFoundGetError", func(t *testing.T) {
		cvp.CVP_HOST = "localhost:8009"
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, fmt.Errorf("db error"))
		handler := Handler{Orchestrator: mockOrchestrator}

		res, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeKmsConfigurationInternalServerError{}, res)
	})

	t.Run("ReturnsNotFoundWhenHostNotConfiguredAndRecordMissing", func(t *testing.T) {
		cvp.CVP_HOST = ""
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("not found", nil))
		handler := Handler{Orchestrator: mockOrchestrator}

		res, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeKmsConfigurationNotFound{}, res)
	})

	t.Run("UsesSdeKmsConfigUUIDWhenPresent", func(t *testing.T) {
		cvp.CVP_HOST = "localhost:8009"
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockClient := kms_configurations.NewMockClientService(t)
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{KmsConfigurations: mockClient}
		}
		kmsCfg := &vsaCoreModels.KmsConfig{
			BaseModel: vsaCoreModels.BaseModel{UUID: "kms-id"},
			KmsAttributes: &vsaCoreModels.KmsAttributes{
				CreationMode:     datamodel.KmsCreationModeSDE,
				SdeKmsConfigUUID: "sde-id-1",
			},
		}
		mockOrchestrator.EXPECT().GetKmsConfig(mock.Anything, mock.Anything).Return(kmsCfg, nil)
		mockClient.EXPECT().V1betaDescribeKmsConfiguration(mock.MatchedBy(func(p *kms_configurations.V1betaDescribeKmsConfigurationParams) bool {
			return p != nil && p.KmsConfigID == "sde-id-1"
		})).Return(&kms_configurations.V1betaDescribeKmsConfigurationOK{
			Payload: &models.KmsConfigV1beta{UUID: "sde-id-1", KmsState: datamodel.LifeCycleStateREADY},
		}, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		res, err := handler.V1betaDescribeKmsConfiguration(context.Background(), params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.KmsConfigV1beta{}, res)
	})
}

func TestV1betaListKmsConfigurations_VCPPath(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	defer func() { cvp.CVP_HOST = origCVPHost }()
	cvp.CVP_HOST = ""

	params := gcpgenserver.V1betaListKmsConfigurationsParams{
		LocationId:     "us-east4",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}

	t.Run("ReturnsInternalServerErrorWhenListFails", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().ListKmsConfigs(mock.Anything, "12345").Return(nil, fmt.Errorf("db error"))
		handler := Handler{Orchestrator: mockOrchestrator}

		res, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListKmsConfigurationsInternalServerError{}, res)
	})

	t.Run("ReturnsMappedListWhenListSucceeds", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		mockOrchestrator.EXPECT().ListKmsConfigs(mock.Anything, "12345").Return([]*vsaCoreModels.KmsConfig{
			{BaseModel: vsaCoreModels.BaseModel{UUID: "kms-1"}},
		}, nil)
		handler := Handler{Orchestrator: mockOrchestrator}

		res, err := handler.V1betaListKmsConfigurations(context.Background(), params)
		assert.NoError(t, err)
		listRes, ok := res.(*gcpgenserver.V1betaListKmsConfigurationsOKApplicationJSON)
		assert.True(t, ok)
		assert.Len(t, *listRes, 1)
		assert.Equal(t, "kms-1", (*listRes)[0].UUID.Value)
	})
}
