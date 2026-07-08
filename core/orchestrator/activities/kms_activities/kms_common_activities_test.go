package kms_activities

import (
	"context"
	"database/sql"
	"encoding/base64"
	goErrors "errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/async"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/google"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/retry"
	"go.temporal.io/sdk/testsuite"
)

func TestPollKmsConfigOperationActivity(t *testing.T) {
	t.Run("PollKmsConfigOperationActivityReturnsErrorWhenTokenGenerationFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.PollKmsConfigOperationActivity)
		params := &common.PollKmsConfigParams{
			OperationUri:  "operation-id",
			OperationDone: false,
			ProjectNumber: "123456789",
		}

		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "", errors.New("failed to get signed token")
		}

		_, err := env.ExecuteActivity(activity.PollKmsConfigOperationActivity, params)
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get signed token")
	})
	t.Run("PollKmsConfigOperationActivityReturnsErrorWhenResponseIsNil", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.PollKmsConfigOperationActivity)
		params := &common.PollKmsConfigParams{OperationUri: "operation-id",
			OperationDone: false,
		}
		mockClient := kms_configurations.NewMockClientService(t)

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			createClient = originalCreateClient
			getSignedJwtToken = originalGetSignedJwtToken
			pollCvpOperationForWorkflow = _pollCvpOperationForWorkflow
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}

		pollCvpOperationForWorkflow = func(ctx context.Context, cvpClient cvpapi.Cvp, operationParams *async.V1betaDescribeOperationParams) (*cvpModels.OperationV1beta, error) {
			return nil, errors.New("new error")
		}

		_, err := env.ExecuteActivity(activity.PollKmsConfigOperationActivity, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("PollKmsConfigOperationActivityReturnsUpdatedKmsConfigOnSuccess", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.PollKmsConfigOperationActivity)
		kp := "kp"
		params := &common.PollKmsConfigParams{OperationUri: "operation-id",
			OperationDone: false}
		mockClient := kms_configurations.NewMockClientService(t)
		// Define mock response
		mockResponse := &kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &cvpModels.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(false),
				Response: cvpModels.KmsConfigV1beta{UUID: "external-uuid", KeyFullPath: &kp},
			},
		}
		// Set up the mock client behavior

		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			createClient = originalCreateClient
			getSignedJwtToken = originalGetSignedJwtToken
			pollCvpOperationForWorkflow = _pollCvpOperationForWorkflow
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}

		pollCvpOperationForWorkflow = func(ctx context.Context, cvpClient cvpapi.Cvp, operationParams *async.V1betaDescribeOperationParams) (*cvpModels.OperationV1beta, error) {
			return mockResponse.Payload, nil
		}

		_, err := env.ExecuteActivity(activity.PollKmsConfigOperationActivity, params)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

// TestFailedKmsConfigCreateActivity_NonActivityContext verifies that _failedKmsConfigCreateActivity can be called
// from a non-activity context (e.g., orphan job workflow manager) without panicking.
// Before the fix (VSCP-4440), this test would have panicked due to activity.RecordHeartbeat
// being called outside of a Temporal activity context.
func TestFailedKmsConfigCreateActivity_NonActivityContext(t *testing.T) {
	t.Run("DoesNotPanicWhenCalledFromNonActivityContext", func(tt *testing.T) {
		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			createClient = originalCreateClient
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}

		done := true
		resp := &kms_configurations.V1betaDeleteKmsConfigurationAccepted{
			Payload: &cvpModels.OperationV1beta{
				Name: "delete-kms-configuration",
				Done: &done,
			},
		}
		mockClient.On("V1betaDeleteKmsConfiguration", mock.Anything).Return(resp, nil, nil)

		mockSE := database.NewMockStorage(t)
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:         datamodel.BaseModel{UUID: "uuid"},
			State:             datamodel.LifeCycleStateError,
			StateDetails:      "failure reason",
			CustomerProjectID: "123456789",
			ServiceAccount:    &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "sa-uuid"}},
		}
		mockSE.On("DeleteKmsConfig", mock.Anything, kmsConfig.UUID, datamodel.LifeCycleStateDeleted, "failure reason").Return(nil, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "sa-uuid", datamodel.LifeCycleStateError, "failure reason").Return(&datamodel.ServiceAccount{}, nil)

		// Call helper directly with context.Background() - NO activity context
		// This simulates how it's called from orphan job workflow manager
		assert.NotPanics(tt, func() {
			err := _failedKmsConfigCreateActivity(context.Background(), mockSE, kmsConfig, "failure reason", "us-central1")
			assert.NoError(tt, err)
		})
	})
}

func TestFailedKmsConfigCreateActivity(t *testing.T) {
	t.Run("WhenTokenGenerationFails", func(tt *testing.T) {
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "", errors.New("failed to get signed token")
		}

		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FailedKmsConfigCreateActivity)
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:         datamodel.BaseModel{UUID: "uuid"},
			State:             datamodel.LifeCycleStateError,
			StateDetails:      "failure reason",
			CustomerProjectID: "123456789",
			ServiceAccount:    &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "sa-uuid"}},
		}
		// DB cleanup happens before JWT token generation
		mockSE.On("DeleteKmsConfig", mock.Anything, "uuid", datamodel.LifeCycleStateDeleted, "failure reason").Return(nil, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "sa-uuid", datamodel.LifeCycleStateError, "failure reason").Return(&datamodel.ServiceAccount{}, nil)

		_, err := env.ExecuteActivity(activity.FailedKmsConfigCreateActivity, kmsConfig, "failure reason", "location-id")
		assert.Error(tt, err)
		assert.Contains(tt, err.Error(), "failed to get signed token")
	})
	t.Run("WhenDeleteKmsConfigFails", func(tt *testing.T) {
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}

		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FailedKmsConfigCreateActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, State: datamodel.LifeCycleStateError, StateDetails: "failure reason",
			ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("DeleteKmsConfig", mock.Anything, kmsConfig.UUID, datamodel.LifeCycleStateDeleted, kmsConfig.StateDetails).Return(nil, errors.New("failure reason"))
		_, err := env.ExecuteActivity(activity.FailedKmsConfigCreateActivity, kmsConfig, "failure reason", "location-id")
		assert.Error(tt, err)
	})
	t.Run("WhenDeleteKmsConfigErrorNotFound", func(tt *testing.T) {
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}

		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FailedKmsConfigCreateActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, State: datamodel.LifeCycleStateDeleted, StateDetails: "failure reason",
			ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("DeleteKmsConfig", mock.Anything, kmsConfig.UUID, kmsConfig.State, kmsConfig.StateDetails).Return(nil, errors.NewNotFoundErr("failure reason", nil))
		_, err := env.ExecuteActivity(activity.FailedKmsConfigCreateActivity, kmsConfig, "failure reason", "location-id")
		assert.Error(tt, err)
	})
	t.Run("WhenSuccess", func(tt *testing.T) {
		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			createClient = originalCreateClient
			getSignedJwtToken = originalGetSignedJwtToken
			pollCvpOperationForWorkflow = _pollCvpOperationForWorkflow
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}

		pollCvpOperationForWorkflow = func(ctx context.Context, cvpClient cvpapi.Cvp, operationParams *async.V1betaDescribeOperationParams) (*cvpModels.OperationV1beta, error) {
			return nil, nil
		}
		done := false
		resp := &kms_configurations.V1betaDeleteKmsConfigurationAccepted{
			Payload: &cvpModels.OperationV1beta{
				Name: "delete-kms-configuration",
				Done: &done,
			},
		}
		mockClient.On("V1betaDeleteKmsConfiguration", mock.Anything).Return(resp, nil, nil)
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, State: datamodel.LifeCycleStateError, StateDetails: "failure reason",
			ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("DeleteKmsConfig", mock.Anything, kmsConfig.UUID, datamodel.LifeCycleStateDeleted, kmsConfig.StateDetails).Return(nil, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FailedKmsConfigCreateActivity)
		_, err := env.ExecuteActivity(activity.FailedKmsConfigCreateActivity, kmsConfig, "failure reason", "location-id")
		assert.NoError(tt, err)
	})
	t.Run("WhenVCPCreated_SkipsSDE", func(tt *testing.T) {
		mockSE := database.NewMockStorage(tt)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FailedKmsConfigCreateActivity)
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:         datamodel.BaseModel{UUID: "uuid"},
			State:             datamodel.LifeCycleStateError,
			StateDetails:      "failure reason",
			CustomerProjectID: "123456789",
			ServiceAccount:    &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "sa-uuid"}},
			KmsAttributes:     &datamodel.KmsAttributes{CreationMode: datamodel.KmsCreationModeVCP},
		}
		mockSE.On("DeleteKmsConfig", mock.Anything, kmsConfig.UUID, datamodel.LifeCycleStateDeleted, "failure reason").Return(nil, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "sa-uuid", datamodel.LifeCycleStateError, "failure reason").Return(&datamodel.ServiceAccount{}, nil)

		// No SDE/CVP mocks — if SDE code is called, the test will fail with unexpected mock calls
		_, err := env.ExecuteActivity(activity.FailedKmsConfigCreateActivity, kmsConfig, "failure reason", "location-id")
		assert.NoError(tt, err)
		mockSE.AssertExpectations(tt)
	})
	t.Run("WhenV1betaDeleteKmsConfigurationFails", func(tt *testing.T) {
		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			createClient = originalCreateClient
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}
		mockClient.On("V1betaDeleteKmsConfiguration", mock.Anything).Return(nil, nil, errors.New("some error"))
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, State: datamodel.LifeCycleStateError, StateDetails: "failure reason",
			ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("DeleteKmsConfig", mock.Anything, kmsConfig.UUID, datamodel.LifeCycleStateDeleted, kmsConfig.StateDetails).Return(nil, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FailedKmsConfigCreateActivity)
		_, err := env.ExecuteActivity(activity.FailedKmsConfigCreateActivity, kmsConfig, "failure reason", "location-id")
		assert.Error(tt, err)
	})
	t.Run("WhenV1betaDeleteKmsConfigurationFailsDueToNotFoundError", func(tt *testing.T) {
		mockClient := kms_configurations.NewMockClientService(t)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		originalGetSignedJwtToken := getSignedJwtToken
		defer func() {
			createClient = originalCreateClient
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}
		mockClient.On("V1betaDeleteKmsConfiguration", mock.Anything).Return(nil, nil, &kms_configurations.V1betaDeleteKmsConfigurationNotFound{})
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, State: datamodel.LifeCycleStateError, StateDetails: "failure reason",
			ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("DeleteKmsConfig", mock.Anything, kmsConfig.UUID, datamodel.LifeCycleStateDeleted, kmsConfig.StateDetails).Return(nil, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.FailedKmsConfigCreateActivity)
		_, err := env.ExecuteActivity(activity.FailedKmsConfigCreateActivity, kmsConfig, "failure reason", "location-id")
		assert.Error(tt, err)
	})
}

func TestCreatedKmsConfigActivity(t *testing.T) {
	t.Run("CreatedKmsConfigActivityUpdatesStateToReady", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreatedKmsConfigActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, State: datamodel.LifeCycleStateCreated, StateDetails: datamodel.LifeCycleStateCreatedDetails,
			ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("UpdateKmsConfigState", mock.Anything, kmsConfig.UUID, kmsConfig.State, kmsConfig.StateDetails).Return(kmsConfig, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		_, err := env.ExecuteActivity(activity.CreatedKmsConfigActivity, kmsConfig)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if kmsConfig.State != datamodel.LifeCycleStateCreated {
			t.Errorf("expected state to be READY, got %v", kmsConfig.State)
		}
		if kmsConfig.StateDetails != datamodel.LifeCycleStateCreatedDetails {
			t.Errorf("expected state details to be set to ready details")
		}
	})
	t.Run("WhenUpdateKmsConfigStateFails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreatedKmsConfigActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, State: datamodel.LifeCycleStateCreated, StateDetails: datamodel.LifeCycleStateCreatedDetails,
			ServiceAccount: &datamodel.ServiceAccount{}}
		mockSE.On("UpdateKmsConfigState", mock.Anything, kmsConfig.UUID, kmsConfig.State, kmsConfig.StateDetails).Return(nil, errors.New("some one"))
		_, err := env.ExecuteActivity(activity.CreatedKmsConfigActivity, kmsConfig)
		assert.Error(tt, err)
	})
}

func TestCreateVSAKmsConfigSAKeyActivity(t *testing.T) {
	// All existing tests use SdeServiceAccountEmail (SDE path), so set CVP_HOST
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("returns error if getGcpService fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes: &datamodel.KmsAttributes{SdeServiceAccountEmail: "prefix-test@project.iam.gserviceaccount.com"},
		}
		defer func() { getGcpService = hyperscaler2.ResolveCloudService }()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("gcp error")
		}
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		if err == nil {
			tt.Fatal("expected error, got nil")
		}
	})

	t.Run("creates new service account if not found in db", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			Name:         "test",
			Description:  "desc",
			AccountID:    1,
			StateDetails: "details",
			KmsAttributes: &datamodel.KmsAttributes{
				SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com",
			},
		}
		origIsKeyPresent := isServiceAccountKeyPresentInGCP
		origExtractKeyID := extractPrivateKeyIDFromPassword
		defer func() {
			getGcpService = hyperscaler2.ResolveCloudService
			GcpServiceCreateServiceAccountKey = _gcpServiceCreateServiceAccountKey
			isServiceAccountKeyPresentInGCP = origIsKeyPresent
			extractPrivateKeyIDFromPassword = origExtractKeyID
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		GcpServiceCreateServiceAccountKey = func(gcpService hyperscaler2.Services, ctx context.Context, email string) (*hyperscaler.ServiceAccountKey, error) {
			return &hyperscaler.ServiceAccountKey{PrivateKeyData: "keydata"}, nil
		}
		// Mock: SA key exists in GCP, no sync needed
		extractPrivateKeyIDFromPassword = func(keyData string) (string, error) { return "test-key-id", nil }
		isServiceAccountKeyPresentInGCP = func(ctx context.Context, gcpService hyperscaler2.Services, email, keyID string) (bool, error) {
			return true, nil
		}
		pass := "enc"
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &pass, nil }
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(nil, errors.NewNotFoundErr("service account", nil))
		mockSE.On("CreateKmsServiceAccount", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{ServiceAccountEmail: "test@project.iam.gserviceaccount.com"}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		result, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		var response *datamodel.KmsConfig
		if err == nil {
			err = result.Get(&response)
		}
		if err != nil {
			tt.Fatalf("expected no error, got %v", err)
		}
		if response.ServiceAccount.ServiceAccountEmail != "test@project.iam.gserviceaccount.com" {
			tt.Errorf("expected ServiceAccountEmail to be set, got %v", response.ServiceAccount.ServiceAccountEmail)
		}
	})

	t.Run("uses VcpServiceAccountEmail for VCP-created config", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)

		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "kms-uuid"},
			KmsAttributes: &datamodel.KmsAttributes{
				CreationMode:           datamodel.KmsCreationModeVCP,
				VcpServiceAccountEmail: "vcp-sa@project.iam.gserviceaccount.com",
				SdeServiceAccountEmail: "sde-sa@project.iam.gserviceaccount.com",
			},
		}

		origGetGcpService := getGcpService
		origDecryptPassword := utils.DecryptPassword
		origValidate := utils.ValidateSAKeyInGCP
		defer func() {
			getGcpService = origGetGcpService
			utils.DecryptPassword = origDecryptPassword
			utils.ValidateSAKeyInGCP = origValidate
		}()

		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		utils.ValidateSAKeyInGCP = false
		pass := "non-empty-password"
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &pass, nil }

		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "vcp-sa@project.iam.gserviceaccount.com").Return(
			&datamodel.ServiceAccount{
				BaseModel:                      datamodel.BaseModel{ID: 7, UUID: "sa-uuid"},
				ServiceAccountEmail:            "vcp-sa@project.iam.gserviceaccount.com",
				ServiceAccountPasswordLocation: "enc",
			}, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "sa-uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, "kms-uuid", mock.Anything).Return(nil)

		result, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		assert.NoError(tt, err)
		var response *datamodel.KmsConfig
		assert.NoError(tt, result.Get(&response))
		assert.Equal(tt, "vcp-sa@project.iam.gserviceaccount.com", response.ServiceAccount.ServiceAccountEmail)
	})

	t.Run("returns error if CreateKmsServiceAccount fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			Name:         "test",
			Description:  "desc",
			AccountID:    1,
			StateDetails: "details",
			KmsAttributes: &datamodel.KmsAttributes{
				SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com",
			},
		}
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		defer func() {
			GcpServiceCreateServiceAccountKey = _gcpServiceCreateServiceAccountKey
		}()
		GcpServiceCreateServiceAccountKey = func(gcpService hyperscaler2.Services, ctx context.Context, email string) (*hyperscaler.ServiceAccountKey, error) {
			return &hyperscaler.ServiceAccountKey{PrivateKeyData: "keydata"}, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(nil, errors.NewNotFoundErr("service account", nil))
		mockSE.On("CreateKmsServiceAccount", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		if err == nil {
			tt.Fatal("expected error, got nil")
		}
	})

	t.Run("returns error if DecryptPassword fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		defer func() { getGcpService = hyperscaler2.ResolveCloudService }()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(&datamodel.ServiceAccount{ServiceAccountPasswordLocation: "bad"}, nil)
		origDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return nil, errors.New("decrypt error") }
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		if err == nil {
			tt.Fatal("expected error, got nil")
		}
	})

	t.Run("returns error if synchronizeServiceAccountKeys fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		defer func() {
			getGcpService = hyperscaler2.ResolveCloudService
			synchronizeServiceAccountKeys = _synchronizeServiceAccountKeys
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountPasswordLocation: "enc"}, nil)
		origDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		empty := ""
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &empty, nil }
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			return nil, errors.New("sync error")
		}
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		if err == nil {
			tt.Fatal("expected error, got nil")
		}
	})

	t.Run("returns error if UpdateServiceAccountEmailAndKey fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		defer func() {
			getGcpService = hyperscaler2.ResolveCloudService
			synchronizeServiceAccountKeys = _synchronizeServiceAccountKeys
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountPasswordLocation: "enc"}, nil)
		origDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		empty := ""
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &empty, nil }
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			val := "enc"
			return &val, nil
		}
		mockSE.On("UpdateServiceAccountEmailAndKey", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(nil, errors.New("update error"))
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		if err == nil {
			tt.Fatal("expected error, got nil")
		}
	})

	t.Run("returns error if UpdateKmsConfig fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		defer func() {
			getGcpService = hyperscaler2.ResolveCloudService
			synchronizeServiceAccountKeys = _synchronizeServiceAccountKeys
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountPasswordLocation: "enc"}, nil)
		origDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		empty := ""
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &empty, nil }
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			val := "enc"
			return &val, nil
		}
		mockSE.On("UpdateServiceAccountEmailAndKey", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com"}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("update kms error"))
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		if err == nil {
			tt.Fatal("expected error, got nil")
		}
	})

	t.Run("returns error if UpdateServiceAccountState fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		defer func() {
			getGcpService = hyperscaler2.ResolveCloudService
			synchronizeServiceAccountKeys = _synchronizeServiceAccountKeys
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountPasswordLocation: "enc"}, nil)
		origDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		empty := ""
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &empty, nil }
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			val := "enc"
			return &val, nil
		}
		mockSE.On("UpdateServiceAccountEmailAndKey", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com"}, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(nil, errors.New("update state error"))
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		if err == nil {
			tt.Fatal("expected error, got nil")
		}
	})

	t.Run("returns updated kmsConfig on success", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		defer func() {
			getGcpService = hyperscaler2.ResolveCloudService
			synchronizeServiceAccountKeys = _synchronizeServiceAccountKeys
		}()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountPasswordLocation: "enc"}, nil)
		origDecryptPassword := utils.DecryptPassword
		defer func() { utils.DecryptPassword = origDecryptPassword }()
		empty := ""
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &empty, nil }
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			val := "enc"
			return &val, nil
		}
		mockSE.On("UpdateServiceAccountEmailAndKey", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com"}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		result, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		var response *datamodel.KmsConfig
		if err == nil {
			err = result.Get(&response)
		}
		if err != nil {
			tt.Fatalf("expected no error, got %v", err)
		}
		if response.ServiceAccount.ServiceAccountEmail != "test@project.iam.gserviceaccount.com" {
			tt.Errorf("expected ServiceAccountEmail to be set, got %v", response.ServiceAccount.ServiceAccountEmail)
		}
	})

	t.Run("re-syncs when password is non-empty but specific key not found in GCP", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		origGetGcpService := getGcpService
		origSyncKeys := synchronizeServiceAccountKeys
		origIsKeyPresent := isServiceAccountKeyPresentInGCP
		origExtractKeyID := extractPrivateKeyIDFromPassword
		origDecryptPassword := utils.DecryptPassword
		origValidate := utils.ValidateSAKeyInGCP
		defer func() {
			getGcpService = origGetGcpService
			synchronizeServiceAccountKeys = origSyncKeys
			isServiceAccountKeyPresentInGCP = origIsKeyPresent
			extractPrivateKeyIDFromPassword = origExtractKeyID
			utils.DecryptPassword = origDecryptPassword
			utils.ValidateSAKeyInGCP = origValidate
		}()
		utils.ValidateSAKeyInGCP = true
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		// Password is non-empty but specific key not found in GCP → should re-sync
		validPass := "valid-encrypted-key"
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &validPass, nil }
		extractPrivateKeyIDFromPassword = func(keyData string) (string, error) { return "deleted-key-id", nil }
		isServiceAccountKeyPresentInGCP = func(ctx context.Context, gcpService hyperscaler2.Services, email, keyID string) (bool, error) {
			return false, nil // key not found in GCP
		}
		syncCalled := false
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			syncCalled = true
			val := "new-enc-key"
			return &val, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com", ServiceAccountPasswordLocation: "enc"}, nil)
		mockSE.On("UpdateServiceAccountEmailAndKey", mock.Anything, "uuid", "test@project.iam.gserviceaccount.com", "new-enc-key").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com"}, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		assert.NoError(tt, err)
		assert.True(tt, syncCalled, "synchronizeServiceAccountKeys should have been called when specific key not found in GCP")
	})

	t.Run("skips sync when password is non-empty and specific key found in GCP", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		origGetGcpService := getGcpService
		origSyncKeys := synchronizeServiceAccountKeys
		origIsKeyPresent := isServiceAccountKeyPresentInGCP
		origExtractKeyID := extractPrivateKeyIDFromPassword
		origDecryptPassword := utils.DecryptPassword
		origValidate := utils.ValidateSAKeyInGCP
		defer func() {
			getGcpService = origGetGcpService
			synchronizeServiceAccountKeys = origSyncKeys
			isServiceAccountKeyPresentInGCP = origIsKeyPresent
			extractPrivateKeyIDFromPassword = origExtractKeyID
			utils.DecryptPassword = origDecryptPassword
			utils.ValidateSAKeyInGCP = origValidate
		}()
		utils.ValidateSAKeyInGCP = true
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		// Password is non-empty and specific key exists in GCP → should NOT re-sync
		validPass := "valid-encrypted-key"
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &validPass, nil }
		extractPrivateKeyIDFromPassword = func(keyData string) (string, error) { return "existing-key-id", nil }
		isServiceAccountKeyPresentInGCP = func(ctx context.Context, gcpService hyperscaler2.Services, email, keyID string) (bool, error) {
			return true, nil // key exists in GCP
		}
		syncCalled := false
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			syncCalled = true
			val := "enc"
			return &val, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com", ServiceAccountPasswordLocation: "enc"}, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		result, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		assert.NoError(tt, err)
		assert.False(tt, syncCalled, "synchronizeServiceAccountKeys should NOT have been called when specific key found in GCP")
		var response *datamodel.KmsConfig
		assert.NoError(tt, result.Get(&response))
	})

	t.Run("re-syncs when password is non-empty but GCP key check fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		origGetGcpService := getGcpService
		origSyncKeys := synchronizeServiceAccountKeys
		origIsKeyPresent := isServiceAccountKeyPresentInGCP
		origExtractKeyID := extractPrivateKeyIDFromPassword
		origDecryptPassword := utils.DecryptPassword
		origValidate := utils.ValidateSAKeyInGCP
		defer func() {
			getGcpService = origGetGcpService
			synchronizeServiceAccountKeys = origSyncKeys
			isServiceAccountKeyPresentInGCP = origIsKeyPresent
			extractPrivateKeyIDFromPassword = origExtractKeyID
			utils.DecryptPassword = origDecryptPassword
			utils.ValidateSAKeyInGCP = origValidate
		}()
		utils.ValidateSAKeyInGCP = true
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		// Password is non-empty, but GCP key check returns error → should re-sync as safe fallback
		validPass := "valid-encrypted-key"
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &validPass, nil }
		extractPrivateKeyIDFromPassword = func(keyData string) (string, error) { return "some-key-id", nil }
		isServiceAccountKeyPresentInGCP = func(ctx context.Context, gcpService hyperscaler2.Services, email, keyID string) (bool, error) {
			return false, errors.New("GCP list keys error")
		}
		syncCalled := false
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			syncCalled = true
			val := "new-enc-key"
			return &val, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com", ServiceAccountPasswordLocation: "enc"}, nil)
		mockSE.On("UpdateServiceAccountEmailAndKey", mock.Anything, "uuid", "test@project.iam.gserviceaccount.com", "new-enc-key").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com"}, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		assert.NoError(tt, err)
		assert.True(tt, syncCalled, "synchronizeServiceAccountKeys should have been called when GCP key check fails")
	})

	t.Run("re-syncs when password is non-empty but key ID extraction fails", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		origGetGcpService := getGcpService
		origSyncKeys := synchronizeServiceAccountKeys
		origExtractKeyID := extractPrivateKeyIDFromPassword
		origDecryptPassword := utils.DecryptPassword
		origValidate := utils.ValidateSAKeyInGCP
		defer func() {
			getGcpService = origGetGcpService
			synchronizeServiceAccountKeys = origSyncKeys
			extractPrivateKeyIDFromPassword = origExtractKeyID
			utils.DecryptPassword = origDecryptPassword
			utils.ValidateSAKeyInGCP = origValidate
		}()
		utils.ValidateSAKeyInGCP = true
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		// Password is non-empty, but key data is corrupt/unparseable → should re-sync
		validPass := "corrupt-key-data"
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &validPass, nil }
		extractPrivateKeyIDFromPassword = func(keyData string) (string, error) {
			return "", errors.New("failed to parse key JSON")
		}
		syncCalled := false
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			syncCalled = true
			val := "new-enc-key"
			return &val, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com", ServiceAccountPasswordLocation: "enc"}, nil)
		mockSE.On("UpdateServiceAccountEmailAndKey", mock.Anything, "uuid", "test@project.iam.gserviceaccount.com", "new-enc-key").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com"}, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		_, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		assert.NoError(tt, err)
		assert.True(tt, syncCalled, "synchronizeServiceAccountKeys should have been called when key ID extraction fails")
	})

	t.Run("skips GCP validation when ValidateSAKeyInGCP is disabled", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateVSAKmsConfigSAKeyActivity)
		kmsConfig := &datamodel.KmsConfig{
			ServiceAccount: &datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}},
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "test@project.iam.gserviceaccount.com"},
		}
		origGetGcpService := getGcpService
		origSyncKeys := synchronizeServiceAccountKeys
		origDecryptPassword := utils.DecryptPassword
		origValidate := utils.ValidateSAKeyInGCP
		defer func() {
			getGcpService = origGetGcpService
			synchronizeServiceAccountKeys = origSyncKeys
			utils.DecryptPassword = origDecryptPassword
			utils.ValidateSAKeyInGCP = origValidate
		}()
		utils.ValidateSAKeyInGCP = false // disable validation
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return &google.GcpServices{}, nil
		}
		// Password is non-empty — with validation disabled, no GCP check should happen
		validPass := "valid-encrypted-key"
		utils.DecryptPassword = func(_ log.Secret) (*string, error) { return &validPass, nil }
		syncCalled := false
		synchronizeServiceAccountKeys = func(ctx context.Context, gcpService hyperscaler2.Services, email string) (*string, error) {
			syncCalled = true
			val := "enc"
			return &val, nil
		}
		mockSE.On("GetServiceAccountFromEmail", mock.Anything, "test@project.iam.gserviceaccount.com").Return(
			&datamodel.ServiceAccount{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccountEmail: "test@project.iam.gserviceaccount.com", ServiceAccountPasswordLocation: "enc"}, nil)
		mockSE.On("UpdateServiceAccountState", mock.Anything, "uuid", mock.Anything, mock.Anything).Return(&datamodel.ServiceAccount{}, nil)
		mockSE.On("UpdateKmsConfig", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		result, err := env.ExecuteActivity(activity.CreateVSAKmsConfigSAKeyActivity, kmsConfig)
		assert.NoError(tt, err)
		assert.False(tt, syncCalled, "synchronizeServiceAccountKeys should NOT have been called when validation is disabled")
		var response *datamodel.KmsConfig
		assert.NoError(tt, result.Get(&response))
	})
}

func Test_extractPrivateKeyID(t *testing.T) {
	t.Run("extracts key ID from valid base64-encoded JSON", func(tt *testing.T) {
		keyJSON := `{"type":"service_account","private_key_id":"abc123","client_email":"test@project.iam.gserviceaccount.com"}`
		encoded := base64.StdEncoding.EncodeToString([]byte(keyJSON))
		keyID, err := _extractPrivateKeyID(encoded)
		assert.NoError(tt, err)
		assert.Equal(tt, "abc123", keyID)
	})

	t.Run("returns error for invalid base64", func(tt *testing.T) {
		keyID, err := _extractPrivateKeyID("not-valid-base64!!!")
		assert.Error(tt, err)
		assert.Empty(tt, keyID)
		assert.Contains(tt, err.Error(), "base64")
	})

	t.Run("returns error for invalid JSON", func(tt *testing.T) {
		encoded := base64.StdEncoding.EncodeToString([]byte("not json"))
		keyID, err := _extractPrivateKeyID(encoded)
		assert.Error(tt, err)
		assert.Empty(tt, keyID)
		assert.Contains(tt, err.Error(), "parse key JSON")
	})

	t.Run("returns error when private_key_id is missing", func(tt *testing.T) {
		keyJSON := `{"type":"service_account","client_email":"test@project.iam.gserviceaccount.com"}`
		encoded := base64.StdEncoding.EncodeToString([]byte(keyJSON))
		keyID, err := _extractPrivateKeyID(encoded)
		assert.Error(tt, err)
		assert.Empty(tt, keyID)
		assert.Contains(tt, err.Error(), "private_key_id not found")
	})
}

func Test_gcpServiceCreateServiceAccountKey(t *testing.T) {
	ctx := context.Background()
	email := "test@project.iam.gserviceaccount.com"

	t.Run("returns key on success", func(t *testing.T) {
		expectedKey := &hyperscaler.ServiceAccountKey{PrivateKeyData: "keydata"}
		mockGCPService := new(hyperscaler2.MockServices)
		mockGCPService.On("CreateServiceAccountKey", mock.Anything, email).Return(expectedKey, nil)
		key, err := _gcpServiceCreateServiceAccountKey(mockGCPService, ctx, email)
		assert.NoError(t, err)
		assert.Equal(t, expectedKey, key)
	})

	t.Run("returns error on failure", func(t *testing.T) {
		mockGCPService := new(hyperscaler2.MockServices)
		mockGCPService.On("CreateServiceAccountKey", mock.Anything, mock.Anything).Return(nil, errors.New("error"))
		key, err := _gcpServiceCreateServiceAccountKey(mockGCPService, ctx, email)
		assert.Error(t, err)
		assert.Nil(t, key)
	})
}

func TestGrantRoleActivity(t *testing.T) {
	t.Run("GrantRoleActivityReturnsErrorWhenServiceAccountEmailIsEmpty", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.GrantRoleActivity)
		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: ""},
			ServiceAccount: &datamodel.ServiceAccount{ServiceAccountEmail: ""},
		}
		mockGcpService := &google.GcpServices{}
		origGetGcpService := getGcpService
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}
		defer func() { getGcpService = origGetGcpService }()
		origGrant := gcpGrantServiceAccountRole
		gcpGrantServiceAccountRole = func(ctx context.Context, gcpService hyperscaler2.Services, serviceAccountEmail, member, role string) error {
			if serviceAccountEmail == "" || member == "" {
				return errors.New("missing email")
			}
			return nil
		}
		defer func() { gcpGrantServiceAccountRole = origGrant }()
		_, err := env.ExecuteActivity(activity.GrantRoleActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing email")
	})
	t.Run("GrantRoleActivityReturnsErrorWhenServiceAccountIsNil", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.GrantRoleActivity)
		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "svc@project.iam.gserviceaccount.com"},
			ServiceAccount: &datamodel.ServiceAccount{ServiceAccountEmail: ""},
		}
		mockGcpService := &google.GcpServices{}
		origGetGcpService := getGcpService
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return mockGcpService, nil
		}
		defer func() { getGcpService = origGetGcpService }()
		origGrant := gcpGrantServiceAccountRole
		gcpGrantServiceAccountRole = func(ctx context.Context, gcpService hyperscaler2.Services, serviceAccountEmail, member, role string) error {
			if member == "" {
				return errors.New("missing email")
			}
			return nil
		}
		defer func() { gcpGrantServiceAccountRole = origGrant }()
		_, err := env.ExecuteActivity(activity.GrantRoleActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "missing email")
	})
	t.Run("GrantRoleActivityReturnsErrorWhenGetGcpServiceFails", func(t *testing.T) {
		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.GrantRoleActivity)
		kmsConfig := &datamodel.KmsConfig{
			KmsAttributes:  &datamodel.KmsAttributes{SdeServiceAccountEmail: "svc@project.iam.gserviceaccount.com"},
			ServiceAccount: &datamodel.ServiceAccount{ServiceAccountEmail: ""},
		}
		origGetGcpService := getGcpService
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("some error")
		}
		defer func() { getGcpService = origGetGcpService }()
		_, err := env.ExecuteActivity(activity.GrantRoleActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "some error")
	})
}

func TestUpdatePoolWithKmsConfigActivity(t *testing.T) {
	t.Run("UpdatePoolWithKmsConfigActivityReturnsUpdatedPoolOnSuccess", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolWithKmsConfigActivity)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}
		kmsConfigID := "kms-uuid"
		updatedPool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}, KmsConfigID: sql.NullInt64{Int64: 1, Valid: true}}
		mockSE.On("UpdatePoolWithKmsConfigID", mock.Anything, pool, kmsConfigID).Return(updatedPool, nil)

		result, err := env.ExecuteActivity(activity.UpdatePoolWithKmsConfigActivity, pool, kmsConfigID)
		var response *datamodel.Pool
		if err == nil {
			err = result.Get(&response)
		}
		assert.NoError(t, err)
		assert.Equal(t, updatedPool, response)
	})
	t.Run("UpdatePoolWithKmsConfigActivityReturnsErrorOnFailure", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdatePoolWithKmsConfigActivity)
		pool := &datamodel.Pool{BaseModel: datamodel.BaseModel{ID: 1}}
		kmsConfigID := "kms-uuid"
		mockSE.On("UpdatePoolWithKmsConfigID", mock.Anything, pool, kmsConfigID).Return(nil, errors.New("update error"))

		result, err := env.ExecuteActivity(activity.UpdatePoolWithKmsConfigActivity, pool, kmsConfigID)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "update error")
	})
}

func TestAccessCryptoKeyAndEncryptDataWithImpersonationActivity(t *testing.T) {
	t.Run("AccessCryptoKeyAndEncryptDataWithImpersonationActivityReturnsNoErrorOnSuccess", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.AccessCryptoKeyAndEncryptDataWithImpersonationActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData
		defer func() { AccessCryptoKeyAndEncryptData = origAccessCryptoKey }()
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return nil
		}
		_, err := env.ExecuteActivity(activity.AccessCryptoKeyAndEncryptDataWithImpersonationActivity, kmsConfig)
		assert.NoError(t, err)
	})
	t.Run("AccessCryptoKeyAndEncryptDataWithImpersonationActivityReturnsErrorOnFailure", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.AccessCryptoKeyAndEncryptDataWithImpersonationActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData
		defer func() { AccessCryptoKeyAndEncryptData = origAccessCryptoKey }()
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return errors.New("access error")
		}
		_, err := env.ExecuteActivity(activity.AccessCryptoKeyAndEncryptDataWithImpersonationActivity, kmsConfig)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "access error")
	})
	t.Run("AccessCryptoKeyAndEncryptDataWithImpersonationActivityUsesCorrectTimeouts", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.AccessCryptoKeyAndEncryptDataWithImpersonationActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData
		defer func() { AccessCryptoKeyAndEncryptData = origAccessCryptoKey }()

		var receivedTimeout, receivedInterval time.Duration
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			receivedTimeout = timeout
			receivedInterval = timeoutInterval
			return nil
		}
		_, err := env.ExecuteActivity(activity.AccessCryptoKeyAndEncryptDataWithImpersonationActivity, kmsConfig)
		assert.NoError(t, err)
		assert.Equal(t, RetryTimeOutForGetCryptoKey, receivedTimeout, "Expected correct timeout value")
		assert.Equal(t, RetryIntervalForGetCryptoKey, receivedInterval, "Expected correct interval value")
	})
}

// kmsTestConfig returns a baseline SDE KMS config used across access tests.
func kmsTestConfig() *datamodel.KmsConfig {
	return &datamodel.KmsConfig{
		BaseModel: datamodel.BaseModel{UUID: "uuid"},
		ServiceAccount: &datamodel.ServiceAccount{
			ServiceAccountPasswordLocation: "encrypted-location",
		},
		KmsAttributes: &datamodel.KmsAttributes{
			SdeServiceAccountEmail: "svc@project.iam.gserviceaccount.com",
		},
		KeyProjectID:    "project",
		KeyRingLocation: "location",
		KeyRing:         "keyring",
		KeyName:         "keyname",
	}
}

// passthroughRetryDo installs a retryDo that executes the inner function once and
// returns its error, restoring the original on cleanup.
func passthroughRetryDo(t *testing.T) {
	orig := retryDo
	t.Cleanup(func() { retryDo = orig })
	retryDo = func(ctx context.Context, timeout, wait time.Duration, caller string, fn retry.Retriable) error {
		_, err := fn(1)
		return err
	}
}

// withMockCloudService overrides getGcpService to return the given mock, restoring
// the original on cleanup.
func withMockCloudService(t *testing.T, m *hyperscaler2.MockServices) {
	orig := getGcpService
	t.Cleanup(func() { getGcpService = orig })
	getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) { return m, nil }
}

func TestAccessCryptoKey(t *testing.T) {
	ctx := context.Background()

	t.Run("ReturnsErrorWhenGetGcpServiceFails", func(t *testing.T) {
		orig := getGcpService
		defer func() { getGcpService = orig }()
		getGcpService = func(ctx context.Context) (hyperscaler2.Services, error) {
			return nil, errors.New("resolve error")
		}
		kmsConfig := kmsTestConfig()
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resolve error")
	})

	t.Run("TerminalGetErrorIsNotRetried", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		// Terminal permission-denied error: Encrypt must never be reached.
		mockSvc.On("GetKmsCryptoKey", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors2.NewVCPError(errors2.ErrKMSPermissionDenied, errors.New("permission denied accessing KMS key")))
		withMockCloudService(t, mockSvc)
		passthroughRetryDo(t)

		kmsConfig := kmsTestConfig()
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.Error(t, err)
		var cerr *errors2.CustomError
		assert.True(t, goErrors.As(err, &cerr))
		assert.Equal(t, errors2.ErrKMSPermissionDenied, cerr.TrackingID)
		mockSvc.AssertNotCalled(t, "EncryptWithKmsKey", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("TransientGetErrorIsWrappedAsRetriable", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		mockSvc.On("GetKmsCryptoKey", mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("transient boom"))
		withMockCloudService(t, mockSvc)
		passthroughRetryDo(t)

		kmsConfig := kmsTestConfig()
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Projects.Locations.KeyRings.CryptoKeys.Get")
		assert.Contains(t, err.Error(), "transient boom")
	})

	t.Run("ValidateKeyPropertiesFailureIsReturned", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		mockSvc.On("GetKmsCryptoKey", mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscaler.CryptoKey{HasPrimary: true, PrimaryAlgorithm: "GOOGLE_SYMMETRIC_ENCRYPTION", PrimaryState: "DISABLED"}, nil)
		withMockCloudService(t, mockSvc)
		passthroughRetryDo(t)

		kmsConfig := kmsTestConfig()
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.Error(t, err)
		var cerr *errors2.CustomError
		assert.True(t, goErrors.As(err, &cerr))
		assert.Equal(t, errors2.ErrKMSKeyDisabledOrDestroyed, cerr.TrackingID)
	})

	t.Run("SuccessRunsGetThenEncrypt", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		mockSvc.On("GetKmsCryptoKey", mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscaler.CryptoKey{HasPrimary: true, PrimaryAlgorithm: "EXTERNAL_SYMMETRIC_ENCRYPTION", PrimaryState: "ENABLED"}, nil)
		mockSvc.On("EncryptWithKmsKey", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		withMockCloudService(t, mockSvc)

		var receivedCaller string
		orig := retryDo
		defer func() { retryDo = orig }()
		retryDo = func(ctx context.Context, timeout, wait time.Duration, caller string, fn retry.Retriable) error {
			receivedCaller = caller
			_, err := fn(1)
			return err
		}

		kmsConfig := kmsTestConfig()
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.NoError(t, err)
		assert.Equal(t, "AccessCryptoKeyAndEncryptData", receivedCaller, "Expected correct caller name in retry function")
	})

	t.Run("VCPCreatedConfigSetsDirectAccess", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		mockSvc.On("GetKmsCryptoKey", mock.Anything, mock.MatchedBy(func(a *hyperscaler.KmsAccess) bool {
			return a.IsVCPCreated
		}), mock.Anything).Return(&hyperscaler.CryptoKey{HasPrimary: true, PrimaryAlgorithm: "EXTERNAL_SYMMETRIC_ENCRYPTION", PrimaryState: "ENABLED"}, nil)
		mockSvc.On("EncryptWithKmsKey", mock.Anything, mock.MatchedBy(func(a *hyperscaler.KmsAccess) bool {
			return a.IsVCPCreated
		}), mock.Anything, mock.Anything).Return(nil)
		withMockCloudService(t, mockSvc)
		passthroughRetryDo(t)

		kmsConfig := kmsTestConfig()
		kmsConfig.KmsAttributes = &datamodel.KmsAttributes{
			VcpServiceAccountEmail: "cmek-usea1-123456789@cmek-project.iam.gserviceaccount.com",
			CreationMode:           datamodel.KmsCreationModeVCP,
		}
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.NoError(t, err)
	})

	t.Run("GrantPresentSkipsGetAndEncryptsWithGrantPath", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		// GetKmsCryptoKey must NOT be set up: when a grant is present, Get is skipped.
		mockSvc.On("EncryptWithKmsKey", mock.Anything, mock.Anything, mock.MatchedBy(func(keyPath string) bool {
			return strings.Contains(keyPath, "cryptoKeys/keyname/grants/grant-id")
		}), mock.Anything).Return(nil)
		withMockCloudService(t, mockSvc)
		passthroughRetryDo(t)

		kmsConfig := kmsTestConfig()
		kmsConfig.KmsGrant = "grant-id"
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.NoError(t, err)
		mockSvc.AssertNotCalled(t, "GetKmsCryptoKey", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("TerminalEncryptErrorIsNotRetried", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		mockSvc.On("GetKmsCryptoKey", mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscaler.CryptoKey{HasPrimary: true, PrimaryAlgorithm: "EXTERNAL_SYMMETRIC_ENCRYPTION", PrimaryState: "ENABLED"}, nil)
		mockSvc.On("EncryptWithKmsKey", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(errors2.NewVCPError(errors2.ErrKMSKeyUnreachable, errors.New("key_unreachable")))
		withMockCloudService(t, mockSvc)
		passthroughRetryDo(t)

		kmsConfig := kmsTestConfig()
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, RetryTimeOutForGetCryptoKey, RetryIntervalForGetCryptoKey)
		assert.Error(t, err)
		var cerr *errors2.CustomError
		assert.True(t, goErrors.As(err, &cerr))
		assert.Equal(t, errors2.ErrKMSKeyUnreachable, cerr.TrackingID)
	})

	t.Run("VerifiesTimeoutParametersAreUsedCorrectly", func(t *testing.T) {
		mockSvc := hyperscaler2.NewMockServices(t)
		withMockCloudService(t, mockSvc)

		var receivedTimeouts []time.Duration
		var receivedIntervals []time.Duration
		orig := retryDo
		defer func() { retryDo = orig }()
		retryDo = func(ctx context.Context, timeout, wait time.Duration, caller string, fn retry.Retriable) error {
			receivedTimeouts = append(receivedTimeouts, timeout)
			receivedIntervals = append(receivedIntervals, wait)
			// Just track the parameters, don't execute the function
			return nil
		}

		customTimeout := 45 * time.Second
		customInterval := 10 * time.Second
		kmsConfig := kmsTestConfig()
		err := _accessCryptoKeyAndEncryptData(ctx, kmsConfig, kmsConfig.ServiceAccount.ServiceAccountPasswordLocation, customTimeout, customInterval)
		assert.NoError(t, err) // Should succeed since we're not executing the actual KMS calls

		// Should have been called twice - once for crypto key access, once for encryption
		assert.Len(t, receivedTimeouts, 2, "Expected retry to be called twice")
		assert.Equal(t, customTimeout, receivedTimeouts[0], "First retry should use custom timeout")
		assert.Equal(t, RetryTimeOutForGetCryptoKey, receivedTimeouts[1], "Second retry should use hardcoded timeout")
		assert.Equal(t, customInterval, receivedIntervals[0], "First retry should use custom interval")
		assert.Equal(t, RetryIntervalForGetCryptoKey, receivedIntervals[1], "Second retry should use hardcoded interval")
	})
}

func Test_synchronizeServiceAccountKeys(t *testing.T) {
	ctx := context.Background()
	email := "test@project.iam.gserviceaccount.com"

	t.Run("returns key data on success", func(t *testing.T) {
		mockGCPService := new(hyperscaler2.MockServices)
		mockGCPService.On("DeleteAllServiceAccountKeys", ctx, email).Return(nil)
		expectedKey := &hyperscaler.ServiceAccountKey{PrivateKeyData: "keydata"}
		mockGCPService.On("CreateServiceAccountKey", ctx, email).Return(expectedKey, nil)

		key, err := _synchronizeServiceAccountKeys(ctx, mockGCPService, email)
		assert.NoError(t, err)
		assert.NotNil(t, key)
		assert.Equal(t, "keydata", *key)
	})

	t.Run("returns error if DeleteAllServiceAccountKeys fails", func(t *testing.T) {
		mockGCPService := new(hyperscaler2.MockServices)
		mockGCPService.On("DeleteAllServiceAccountKeys", ctx, email).Return(errors.New("delete error"))

		key, err := _synchronizeServiceAccountKeys(ctx, mockGCPService, email)
		assert.Error(t, err)
		assert.Nil(t, key)
	})

	t.Run("returns error if CreateServiceAccountKey fails", func(t *testing.T) {
		mockGCPService := new(hyperscaler2.MockServices)
		mockGCPService.On("DeleteAllServiceAccountKeys", ctx, email).Return(nil)
		mockGCPService.On("CreateServiceAccountKey", ctx, email).Return(nil, errors.New("create error"))

		key, err := _synchronizeServiceAccountKeys(ctx, mockGCPService, email)
		assert.Error(t, err)
		assert.Nil(t, key)
	})
}

func TestGcpWrapperFunctions(t *testing.T) {
	t.Run("disable-enable-key-present wrappers call underlying methods", func(tt *testing.T) {
		gcpSvc := &google.GcpServices{}
		assert.Panics(tt, func() { _ = _gcpDisableServiceAccount(gcpSvc, "sa@test.iam.gserviceaccount.com") })
		assert.Panics(tt, func() { _ = _gcpEnableServiceAccount(gcpSvc, "sa@test.iam.gserviceaccount.com") })
		assert.Panics(tt, func() {
			_, _ = _isServiceAccountKeyPresentInGCP(context.Background(), gcpSvc, "sa@test.iam.gserviceaccount.com", "key-id")
		})
	})
}

func TestPollCvpOperationForWorkflow(t *testing.T) {
	t.Run("WhenV1betaDescribeOperationFails", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)
		params := &async.V1betaDescribeOperationParams{}
		mockAsyncClient := async.NewMockClientService(t)
		// Set up the mock client behavior

		cvpClient := &cvpapi.Cvp{Async: mockAsyncClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockAsyncClient.On("V1betaDescribeOperation", mock.Anything).Return(nil, errors.New("some error")).Once()
		resp, err := pollCvpOperationForWorkflow(ctx, *cvpClient, params)
		assert.NotNil(tt, err)
		assert.Nil(t, resp)
	})
	t.Run("WhenDoneButOperationFailed", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)
		params := &async.V1betaDescribeOperationParams{}
		mockAsyncClient := async.NewMockClientService(t)
		// Set up the mock client behavior
		done := true
		mockOp := cvpModels.OperationV1beta{
			Done: &done,
			Error: &cvpModels.StatusV1Beta{
				Code:    http.StatusConflict,
				Message: "Failed",
			},
		}
		mockResp := &async.V1betaDescribeOperationOK{
			Payload: &mockOp,
		}
		cvpClient := &cvpapi.Cvp{Async: mockAsyncClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockAsyncClient.On("V1betaDescribeOperation", mock.Anything).Return(mockResp, nil).Once()
		resp, err := pollCvpOperationForWorkflow(ctx, *cvpClient, params)
		assert.NotNil(tt, err)
		assert.Nil(t, resp)
	})
	t.Run("WhenDoneAndOperationSuccess", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)
		params := &async.V1betaDescribeOperationParams{}
		mockAsyncClient := async.NewMockClientService(t)
		// Set up the mock client behavior
		done := true
		mockOp := cvpModels.OperationV1beta{
			Done: &done,
		}
		mockResp := &async.V1betaDescribeOperationOK{
			Payload: &mockOp,
		}
		cvpClient := &cvpapi.Cvp{Async: mockAsyncClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockAsyncClient.On("V1betaDescribeOperation", mock.Anything).Return(mockResp, nil).Once()
		resp, err := pollCvpOperationForWorkflow(ctx, *cvpClient, params)
		assert.Nil(tt, err)
		assert.NotNil(t, resp)
	})
	t.Run("WhenNotDoneAndOperationSuccess", func(tt *testing.T) {
		mockLogger := log.NewLogger()
		ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)
		params := &async.V1betaDescribeOperationParams{}
		mockAsyncClient := async.NewMockClientService(t)
		// Set up the mock client behavior
		done := false
		mockOp := cvpModels.OperationV1beta{
			Done: &done,
			Error: &cvpModels.StatusV1Beta{
				Code:    http.StatusConflict,
				Message: "Failed",
			},
		}
		mockResp := &async.V1betaDescribeOperationOK{
			Payload: &mockOp,
		}
		cvpClient := &cvpapi.Cvp{Async: mockAsyncClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockAsyncClient.On("V1betaDescribeOperation", mock.Anything).Return(mockResp, nil).Once()
		resp, err := pollCvpOperationForWorkflow(ctx, *cvpClient, params)
		assert.NotNil(tt, err)
		assert.Nil(t, resp)
	})
}

func TestGetResponseForPollCvpOperation(t *testing.T) {
	mockLogger := log.NewLogger()
	ctx := context.WithValue(context.Background(), middleware.ContextSLoggerKey, mockLogger)
	mockClient := kms_configurations.NewMockClientService(t)

	cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
	originalCreateClient := createClient
	originalGetSignedJwtToken := getSignedJwtToken

	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	responsePayloadName := "responsePayloadName"
	projectNumber := "projectNumber"
	locationID := "locationID"

	t.Run("WhenGetSignedJwtTokenFails", func(tt *testing.T) {
		defer func() {
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "", errors.New("failed to get signed token")
		}

		payload, errPoll := GetResponseforPollCvpOperation(ctx, responsePayloadName, projectNumber, locationID)
		assert.Error(tt, errPoll)
		assert.Nil(tt, payload)
		assert.Contains(tt, errPoll.Error(), "failed to get signed token")
	})
	t.Run("WhenPollCvpOperationForWorkflowReturnsError", func(tt *testing.T) {
		defer func() {
			createClient = originalCreateClient
			pollCvpOperationForWorkflow = _pollCvpOperationForWorkflow
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}
		pollCvpOperationForWorkflow = func(ctx context.Context, cvpClient cvpapi.Cvp, operationParams *async.V1betaDescribeOperationParams) (*cvpModels.OperationV1beta, error) {
			return nil, errors.New("new error")
		}

		payload, errPoll := GetResponseforPollCvpOperation(ctx, responsePayloadName, projectNumber, locationID)
		assert.Error(tt, errPoll)
		assert.Nil(tt, payload)
	})
	t.Run("WhenPollCvpOperationForWorkflowReturnsPayload", func(tt *testing.T) {
		defer func() {
			createClient = originalCreateClient
			pollCvpOperationForWorkflow = _pollCvpOperationForWorkflow
			getSignedJwtToken = originalGetSignedJwtToken
		}()
		getSignedJwtToken = func(projectNumber string) (string, error) {
			return "mock-jwt-token", nil
		}
		done := true
		response := cvpModels.OperationV1beta{
			Name: "operationName",
			Done: &done,
		}
		pollCvpOperationForWorkflow = func(ctx context.Context, cvpClient cvpapi.Cvp, operationParams *async.V1betaDescribeOperationParams) (*cvpModels.OperationV1beta, error) {
			return &response, nil
		}

		responsePoll, errPoll := GetResponseforPollCvpOperation(ctx, responsePayloadName, projectNumber, locationID)
		assert.NoError(tt, errPoll)
		assert.NotNil(tt, responsePoll)
		assert.Equal(tt, responsePoll.Name, "operationName")
	})
}

func TestVerifyVsaKmsReachabilityActivity(t *testing.T) {
	t.Run("ReturnsNoErrorWhenAccessCryptoKeySucceeds", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.VerifyVsaKmsReachabilityActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData

		defer func() {
			AccessCryptoKeyAndEncryptData = origAccessCryptoKey
			UpdateKmsConfigHealth = _updateKmsConfigHealth
		}()
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return nil
		}
		UpdateKmsConfigHealth = func(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
			return kmsConfig, nil
		}
		mockSE.On("GetKmsConfigByUUID", mock.Anything, kmsConfig.UUID).Return(kmsConfig, nil)
		_, err := env.ExecuteActivity(activity.VerifyVsaKmsReachabilityActivity, kmsConfig.UUID, true)
		assert.NoError(t, err)
	})
	t.Run("WhenUpdateKmsConfigHealthFails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.VerifyVsaKmsReachabilityActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData
		defer func() {
			AccessCryptoKeyAndEncryptData = origAccessCryptoKey
			UpdateKmsConfigHealth = _updateKmsConfigHealth
		}()
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return nil
		}
		UpdateKmsConfigHealth = func(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
			return nil, errors.New("some error")
		}
		mockSE.On("GetKmsConfigByUUID", mock.Anything, kmsConfig.UUID).Return(kmsConfig, nil)
		_, err := env.ExecuteActivity(activity.VerifyVsaKmsReachabilityActivity, kmsConfig.UUID, true)
		assert.Error(t, err)
	})
	t.Run("ReturnsErrorWhenAccessCryptoKeyFails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.VerifyVsaKmsReachabilityActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData
		defer func() {
			AccessCryptoKeyAndEncryptData = origAccessCryptoKey
			UpdateKmsConfigHealth = _updateKmsConfigHealth
		}()
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return errors.New("unreachable")
		}
		UpdateKmsConfigHealth = func(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
			return kmsConfig, nil
		}
		mockSE.On("GetKmsConfigByUUID", mock.Anything, kmsConfig.UUID).Return(kmsConfig, nil)
		_, err := env.ExecuteActivity(activity.VerifyVsaKmsReachabilityActivity, kmsConfig.UUID, true)
		assert.Error(t, err)
		assert.ErrorContains(t, err, "unreachable")
	})
	t.Run("WhenGetKmsConfigByUUIDFails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.VerifyVsaKmsReachabilityActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData

		defer func() {
			AccessCryptoKeyAndEncryptData = origAccessCryptoKey
			UpdateKmsConfigHealth = _updateKmsConfigHealth
		}()
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return nil
		}
		UpdateKmsConfigHealth = func(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
			return kmsConfig, nil
		}
		mockSE.On("GetKmsConfigByUUID", mock.Anything, kmsConfig.UUID).Return(nil, errors.New("some error"))
		_, err := env.ExecuteActivity(activity.VerifyVsaKmsReachabilityActivity, kmsConfig.UUID, true)
		assert.Error(t, err)
	})
	t.Run("WhenGetKmsConfigByUUIDFailsNonRetriableError", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.VerifyVsaKmsReachabilityActivity)
		kmsConfig := &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}, ServiceAccount: &datamodel.ServiceAccount{}}
		origAccessCryptoKey := AccessCryptoKeyAndEncryptData

		defer func() {
			AccessCryptoKeyAndEncryptData = origAccessCryptoKey
			UpdateKmsConfigHealth = _updateKmsConfigHealth
		}()
		AccessCryptoKeyAndEncryptData = func(ctx context.Context, kmsConfig *datamodel.KmsConfig, secretPassword string, timeout, timeoutInterval time.Duration) error {
			return nil
		}
		UpdateKmsConfigHealth = func(ctx context.Context, se database.Storage, configCheck *models.KmsConfigCheck) (*datamodel.KmsConfig, error) {
			return kmsConfig, nil
		}
		mockSE.On("GetKmsConfigByUUID", mock.Anything, kmsConfig.UUID).Return(nil, errors.NewNotFoundErr("some error", nil))
		_, err := env.ExecuteActivity(activity.VerifyVsaKmsReachabilityActivity, kmsConfig.UUID, true)
		assert.Error(t, err)
	})
}

func TestUpdateKmsConfigHealth(t *testing.T) {
	t.Run("UpdateKmsConfigHealthUpdatesStateToInUseWhenInErrorStateAndUsedBySvms", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateError,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(true, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-uuid", datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateAvailableDetails).Return(kmsConfig, nil)
		mockStorage.On("UpdateKmsConfigAttributes", ctx, "test-uuid", kmsConfig.KmsAttributes).Return(kmsConfig, nil)
		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("UpdateKmsConfigHealthKeepsStateInUseWhenHealthyAndInUse", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateInUse,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(false, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-uuid", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails).Return(kmsConfig, nil)
		mockStorage.On("UpdateKmsConfigAttributes", ctx, "test-uuid", kmsConfig.KmsAttributes).Return(kmsConfig, nil)

		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("UpdateKmsConfigHealthSetsStateToErrorWhenUnhealthy", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateAvailable,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(true, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-uuid", datamodel.LifeCycleStateError, "some error").Return(kmsConfig, nil)
		mockStorage.On("UpdateKmsConfigAttributes", ctx, "test-uuid", kmsConfig.KmsAttributes).Return(kmsConfig, nil)

		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   false,
			HealthError: "some error",
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("UpdateKmsConfigHealthSetsStateToCreatedWhenHealthErrorMatchesKeyNotFound", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateCreated,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		healthError := strings.Replace(strings.Replace(GcpKmsConfigHealthError, "<key_name>", "key1", 1), "<key_ring>", "ring1", 1)
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(false, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-uuid", datamodel.LifeCycleStateCreated, healthError).Return(kmsConfig, nil)
		mockStorage.On("UpdateKmsConfigAttributes", ctx, "test-uuid", kmsConfig.KmsAttributes).Return(kmsConfig, nil)

		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   false,
			HealthError: healthError,
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("WhenImpersonateHealthError", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateCreated,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		healthError := GcpKmsConfigImpersonationHealthError
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(false, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-uuid", datamodel.LifeCycleStateCreated, healthError).Return(kmsConfig, nil)
		mockStorage.On("UpdateKmsConfigAttributes", ctx, "test-uuid", kmsConfig.KmsAttributes).Return(kmsConfig, nil)

		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   false,
			HealthError: healthError,
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("UpdateKmsConfigHealthReturnsErrorWhenGetKmsConfigFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		mockStorage.On("GetKmsConfigByUUID", ctx, "test-uuid").Return(nil, errors.New("some error"))
		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
	t.Run("UpdateKmsConfigHealthReturnsErrorWhenIsKmsConfigInUseFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateAvailable,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(true, errors.New("some error"))

		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
	t.Run("UpdateKmsConfigHealthReturnsErrorWhenUpdateKmsConfigStateFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel: datamodel.BaseModel{UUID: "test-uuid"},
			State:     datamodel.LifeCycleStateError,
			KeyName:   "key1",
			KeyRing:   "ring1",
		}
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(true, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-uuid", datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateAvailableDetails).Return(nil, errors.New("update error"))

		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
	t.Run("UpdateKmsConfigHealthReturnsErrorUpdateKmsConfigAttributesFails", func(tt *testing.T) {
		mockStorage := new(database.MockStorage)
		ctx := context.Background()
		kmsConfig := &datamodel.KmsConfig{
			BaseModel:     datamodel.BaseModel{UUID: "test-uuid"},
			State:         datamodel.LifeCycleStateError,
			KeyName:       "key1",
			KeyRing:       "ring1",
			KmsAttributes: &datamodel.KmsAttributes{},
		}
		mockStorage.On("GetKmsConfigByUUID", ctx, kmsConfig.UUID).Return(kmsConfig, nil)
		mockStorage.On("IsKmsConfigInUse", ctx, kmsConfig.UUID).Return(true, nil)
		mockStorage.On("UpdateKmsConfigState", ctx, "test-uuid", datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateAvailableDetails).Return(kmsConfig, nil)
		mockStorage.On("UpdateKmsConfigAttributes", ctx, "test-uuid", kmsConfig.KmsAttributes).Return(nil, errors.New("some thing went wrong"))
		response := &models.KmsConfigCheck{
			KmsConfig:   &models.KmsConfig{BaseModel: models.BaseModel{UUID: "test-uuid"}},
			IsHealthy:   true,
			HealthError: "",
			ProxyType:   models.ProxyTypeCvp,
		}
		result, err := UpdateKmsConfigHealth(ctx, mockStorage, response)
		assert.Error(t, err)
		assert.Nil(t, result)
	})
}

func TestKmsPermissionDeniedVCPError(t *testing.T) {
	t.Run("VCPErrorHasCorrectHttpCode", func(t *testing.T) {
		// Verify that the VCP error has the correct HTTP code (412)
		vcpErr := errors2.NewVCPError(errors2.ErrKMSPermissionDenied, errors.New("Permission denied"))
		hasHttpCode, httpCode := vcpErr.GetHttpCode()
		assert.True(t, hasHttpCode)
		assert.Equal(t, 412, httpCode)
	})
}
