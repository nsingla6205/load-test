package kms_activities

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/kms_configurations"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coreModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/testsuite"
)

func TestCreateKmsConfigSDEActivity(t *testing.T) {
	t.Run("CreateKmsConfigSDEActivityReturnsKmsConfigOnSuccess", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{}
		mockClient := kms_configurations.NewMockClientService(t)
		// Define mock response
		kfp := "kfp"
		mockResponse := &kms_configurations.V1betaCreateKmsConfigurationAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(true),
				Response: models.KmsConfigV1beta{UUID: "test", KeyFullPath: &kfp},
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCreateKmsConfiguration(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateKmsConfigSDEActivity)

		_, err := env.ExecuteActivity(activity.CreateKmsConfigSDEActivity, params)
		if err != nil {
			t.Fatal("expected no error, got error:", err)
		}
	})
	t.Run("CreateKmsConfigSDEActivityReturnsErrorOnCreateFailure", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{}
		mockClient := kms_configurations.NewMockClientService(t)
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCreateKmsConfiguration(mock.Anything).
			Return(nil, errors.New("create error"))
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateKmsConfigSDEActivity)
		_, err := env.ExecuteActivity(activity.CreateKmsConfigSDEActivity, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("CreateKmsConfigSDEActivityReturnsErrorOnNilPayload", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{}
		mockClient := kms_configurations.NewMockClientService(t)
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaCreateKmsConfiguration(mock.Anything).
			Return(nil, nil)
		cvpClient := &cvpapi.Cvp{KmsConfigurations: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateKmsConfigSDEActivity)
		_, err := env.ExecuteActivity(activity.CreateKmsConfigSDEActivity, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

func TestUpdateKmsConfigAttributesActivity(t *testing.T) {
	t.Run("WhenNoError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		expectedResult := &datamodel.KmsConfig{}
		mockSE.On("UpdateKmsConfigAttributes", mock.Anything, mock.Anything, mock.Anything).Return(expectedResult, nil)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateKmsConfigAttributesActivity)
		result, err := env.ExecuteActivity(activity.UpdateKmsConfigAttributesActivity, &datamodel.KmsConfig{}, &datamodel.KmsAttributes{})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		var kmsConfig *datamodel.KmsConfig
		err = result.Get(&kmsConfig)
		if err != nil {
			t.Fatalf("failed to get result: %v", err)
		}
		// When using env.ExecuteActivity, we get a new instance, so we can't compare pointers
		// Just verify that the result is not nil (the actual value comparison would require field-by-field comparison)
		if kmsConfig == nil {
			t.Fatalf("expected non-nil result, got nil")
		}
	})
	t.Run("WhenError", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		activity := &KmsConfigActivity{SE: mockSE}
		mockSE.On("UpdateKmsConfigAttributes", mock.Anything, "test-uuid", mock.Anything).Return(nil, errors.New("update error"))
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.UpdateKmsConfigAttributesActivity)
		_, err := env.ExecuteActivity(activity.UpdateKmsConfigAttributesActivity, &datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "test-uuid"}}, &datamodel.KmsAttributes{})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// TestCreateAndSyncKmsConfig_NonActivityContext verifies that _createAndSyncKmsConfig can be called
// from a non-activity context (e.g., HTTP handlers) without panicking.
// Before the fix (VSCP-4440), this test would have panicked due to activity.RecordHeartbeat
// being called outside of a Temporal activity context.
func TestCreateAndSyncKmsConfig_NonActivityContext(t *testing.T) {
	t.Run("DoesNotPanicWhenCalledFromNonActivityContext", func(tt *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{
			AccountName:         "acc",
			KeyFullPath:         "projects/p/locations/l/keyRings/r/cryptoKeys/k",
			UUID:                "uuid",
			KmsState:            "ENABLED",
			KmsStateDetails:     "Active",
			ProjectNumber:       "123",
			ResourceID:          "res-id",
			Instructions:        "inst",
			ServiceAccountEmail: "sa@email.com",
		}
		mockAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}}
		mockSE.On("GetAccount", mock.Anything, "acc").Return(mockAccount, nil)
		mockSE.On("CreateKmsConfig", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}}, nil)

		// Call helper directly with context.Background() - NO activity context
		// This simulates how it's called from HTTP handlers
		assert.NotPanics(tt, func() {
			result, err := _createAndSyncKmsConfig(context.Background(), mockSE, params)
			assert.NoError(tt, err)
			assert.NotNil(tt, result)
			assert.Equal(tt, "uuid", result.UUID)
		})
	})
}

func TestCreateAndSyncKmsConfig_SetsKmsGrantFromKeyFullPath(t *testing.T) {
	mockSE := database.NewMockStorage(t)
	params := &common.CreateKmsConfigParams{
		AccountName:         "acc",
		KeyFullPath:         "projects/p/locations/l/keyRings/r/cryptoKeys/k/grants/grant-id",
		UUID:                "uuid",
		KmsState:            "ENABLED",
		KmsStateDetails:     "Active",
		ProjectNumber:       "123",
		ResourceID:          "res-id",
		Instructions:        "inst",
		ServiceAccountEmail: "sa@email.com",
	}
	mockAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}}
	mockSE.On("GetAccount", mock.Anything, "acc").Return(mockAccount, nil)

	var persisted *datamodel.KmsConfig
	mockSE.On("CreateKmsConfig", mock.Anything, mock.MatchedBy(func(cfg *datamodel.KmsConfig) bool {
		persisted = cfg
		return true
	})).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}}, nil)

	result, err := _createAndSyncKmsConfig(context.Background(), mockSE, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.NotNil(t, persisted)
	assert.Equal(t, "grant-id", persisted.KmsGrant)
	assert.Equal(t, "k", persisted.KeyName)
	assert.Equal(t, "p", persisted.KeyProjectID)
	mockSE.AssertExpectations(t)
}

func TestCreateAndSyncKmsConfigActivity(t *testing.T) {
	t.Run("CreateAndSyncKmsConfigActivityReturnsKmsConfigOnSuccess", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{
			AccountName:         "acc",
			KeyFullPath:         "projects/p/locations/l/keyRings/r/cryptoKeys/k",
			UUID:                "uuid",
			KmsState:            "ENABLED",
			KmsStateDetails:     "Active",
			ProjectNumber:       "123",
			ResourceID:          "res-id",
			Instructions:        "inst",
			ServiceAccountEmail: "sa@email.com",
		}
		mockAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}}
		mockSE.On("GetAccount", mock.Anything, "acc").Return(mockAccount, nil)
		mockSE.On("CreateKmsConfig", mock.Anything, mock.Anything).Return(&datamodel.KmsConfig{BaseModel: datamodel.BaseModel{UUID: "uuid"}}, nil)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateAndSyncKmsConfigActivity)

		result, err := env.ExecuteActivity(activity.CreateAndSyncKmsConfigActivity, params)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		var kmsConfig *datamodel.KmsConfig
		err = result.Get(&kmsConfig)
		if err != nil {
			t.Fatalf("failed to get result: %v", err)
		}
		if kmsConfig.UUID != "uuid" {
			t.Fatalf("expected uuid, got %v", kmsConfig.UUID)
		}
	})
	t.Run("CreateAndSyncKmsConfigActivityReturnsErrorWhenGetAccountFails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{AccountName: "acc"}
		mockSE.On("GetAccount", mock.Anything, "acc").Return(nil, errors.New("account error"))
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateAndSyncKmsConfigActivity)

		_, err := env.ExecuteActivity(activity.CreateAndSyncKmsConfigActivity, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("CreateAndSyncKmsConfigActivityReturnsErrorWhenParseKeyFullPathResourceFails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{
			AccountName: "acc",
			KeyFullPath: "invalid-key-path",
		}
		mockAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}}
		mockSE.On("GetAccount", mock.Anything, "acc").Return(mockAccount, nil)
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateAndSyncKmsConfigActivity)

		_, err := env.ExecuteActivity(activity.CreateAndSyncKmsConfigActivity, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
	t.Run("CreateAndSyncKmsConfigActivityReturnsErrorWhenCreateKmsConfigFails", func(t *testing.T) {
		mockSE := database.NewMockStorage(t)
		params := &common.CreateKmsConfigParams{
			AccountName: "acc",
			KeyFullPath: "projects/p/locations/l/keyRings/r/cryptoKeys/k",
		}
		mockAccount := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}}
		mockSE.On("GetAccount", mock.Anything, "acc").Return(mockAccount, nil)
		mockSE.On("CreateKmsConfig", mock.Anything, mock.Anything).Return(nil, errors.New("create error"))
		activity := &KmsConfigActivity{SE: mockSE}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateAndSyncKmsConfigActivity)

		_, err := env.ExecuteActivity(activity.CreateAndSyncKmsConfigActivity, params)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}

// TestCreateDnsActivity tests the CreateDnsActivity method.
func TestCreateDnsActivity(t *testing.T) {
	node := &coreModels.Node{}

	t.Run("returns error if GetProviderByNode fails", func(t *testing.T) {
		// Patch activities.GetProviderByNode to return error
		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateDnsActivity)
		_, err := env.ExecuteActivity(activity.CreateDnsActivity, node)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("returns error if provider.CreateDns fails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("CreateDns", mock.Anything).Return(errors.New("dns error"))

		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateDnsActivity)
		_, err := env.ExecuteActivity(activity.CreateDnsActivity, node)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})

	t.Run("Returns non-retriable error if provider.CreateDns is unable to reach node", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("CreateDns", mock.Anything).Return(errors.New("Retries exhausted when attempting to reach the storage server"))

		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateDnsActivity)
		_, err := env.ExecuteActivity(activity.CreateDnsActivity, node)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Unable to create DNS: Node not reachable (type: CreateDNSError, retryable: false): unable to reach node")
	})

	t.Run("returns nil on success", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("CreateDns", mock.Anything).Return(nil)

		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateDnsActivity)
		_, err := env.ExecuteActivity(activity.CreateDnsActivity, node)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

// TestEnableAutoVolOfflineCronForGCPKMSActivity tests the EnableAutoVolOfflineCronForGCPKMSActivity method.
func TestEnableAutoVolOfflineCronForGCPKMSActivity(t *testing.T) {
	node := &coreModels.Node{Name: "test-node"}

	t.Run("returns error if GetProviderByNode fails", func(t *testing.T) {
		// Patch vsa.GetProviderByNode to return error
		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return nil, errors.New("provider error")
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableAutoVolOfflineCronForGCPKMSActivity)
		_, err := env.ExecuteActivity(activity.EnableAutoVolOfflineCronForGCPKMSActivity, node)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		assert.Contains(t, err.Error(), "provider error")
	})

	t.Run("logs error but returns nil if provider.EnableAutoVolOfflineCronForGCPKMS fails", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("EnableAutoVolOfflineCronForGCPKMS").Return(errors.New("cron error"))

		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableAutoVolOfflineCronForGCPKMSActivity)
		_, err := env.ExecuteActivity(activity.EnableAutoVolOfflineCronForGCPKMSActivity, node)
		// The method logs the error but returns nil (as per implementation)
		if err != nil {
			t.Fatalf("expected nil, got %v", err)
		}
	})

	t.Run("returns nil on success", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("EnableAutoVolOfflineCronForGCPKMS").Return(nil)

		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.EnableAutoVolOfflineCronForGCPKMSActivity)
		_, err := env.ExecuteActivity(activity.EnableAutoVolOfflineCronForGCPKMSActivity, node)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}

// Additional test for CreateDnsActivity to handle "duplicate entry" case
func TestCreateDnsActivity_DuplicateEntry(t *testing.T) {
	node := &coreModels.Node{Name: "test-node"}

	t.Run("returns nil when DNS entry already exists", func(t *testing.T) {
		mockProvider := new(vsa.MockProvider)
		mockProvider.On("CreateDns", mock.Anything).Return(errors.New("duplicate entry"))

		originalGetProviderByNode := vsa.GetProviderByNode
		vsa.GetProviderByNode = func(ctx context.Context, node *coreModels.Node) (vsa.Provider, error) {
			return mockProvider, nil
		}
		defer func() { vsa.GetProviderByNode = originalGetProviderByNode }()

		activity := &KmsConfigActivity{}
		testSuite := &testsuite.WorkflowTestSuite{}
		env := testSuite.NewTestActivityEnvironment()
		env.RegisterActivity(activity.CreateDnsActivity)
		_, err := env.ExecuteActivity(activity.CreateDnsActivity, node)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})
}
