package active_directory_activities

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/active_directories"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	adHelper "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	vsaerror "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/temporal"
)

func TestActiveDirectoryCreateActivity_CreateVcpActiveDirectory(t *testing.T) {
	t.Run("SuccessfulCreation", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)

		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()
		params := &common.CreateActiveDirectoryParams{
			ResourceId:                 "test-ad",
			Domain:                     "test.example.com",
			DNS:                        "8.8.8.8",
			NetBIOS:                    "TESTNETBIOS",
			OrganizationalUnit:         "OU=test",
			Username:                   "admin",
			Password:                   "password123",
			BackupOperators:            []string{"user1", "user2"},
			Administrators:             []string{"admin1"},
			SecurityOperators:          []string{"security1"},
			KdcIP:                      "192.168.1.1",
			KdcHostname:                "example.com",
			AesEncryption:              true,
			EncryptDCConnections:       true,
			LdapSigning:                true,
			AllowLocalNFSUsersWithLdap: true,
			Description:                "Test AD",
		}
		adUUID := "test-ad-uuid"
		accountId := int64(123)

		encryptedPassword := "encrypted-test-password"
		params.Password = encryptedPassword

		ad := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: adUUID},
			AdName:    params.ResourceId,
			Username:  params.Username,
			Domain:    params.Domain,
			DNS:       params.DNS,
			NetBIOS:   params.NetBIOS,
			AccountId: accountId,
			State:     string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING),
		}

		// Mock password decryption
		originalDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(password log.Secret) (*string, error) {
			decrypted := "decrypted-password"
			return &decrypted, nil
		}
		defer func() { utils.DecryptPassword = originalDecryptPassword }()

		// Mock StorePasswordSecret
		originalStorePasswordSecret := adHelper.StorePasswordSecret
		adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
			return nil
		}
		defer func() { adHelper.StorePasswordSecret = originalStorePasswordSecret }()

		// Only test that the activity updates the AD state to READY
		mockStorage.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
			return ad.State == string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateREADY)
		})).Return(ad, nil)

		_ = activity.CreateVcpActiveDirectory(ctx, params, ad)
	})

	t.Run("DatabaseErrorDuringCreate", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()
		params := &common.CreateActiveDirectoryParams{
			ResourceId: "test-ad",
			Domain:     "test.example.com",
			DNS:        "8.8.8.8",
			NetBIOS:    "TESTNETBIOS",
			Username:   "admin",
			Password:   "password123",
		}
		adUUID := "test-ad-uuid"
		accountId := int64(123)

		encryptedPassword := "encrypted-test-password"
		params.Password = encryptedPassword

		ad := &datamodel.ActiveDirectory{
			BaseModel: datamodel.BaseModel{UUID: adUUID},
			AdName:    params.ResourceId,
			Username:  params.Username,
			Domain:    params.Domain,
			DNS:       params.DNS,
			NetBIOS:   params.NetBIOS,
			AccountId: accountId,
			State:     string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING),
		}

		// Mock password decryption
		originalDecryptPassword := utils.DecryptPassword
		utils.DecryptPassword = func(password log.Secret) (*string, error) {
			decrypted := "decrypted-password"
			return &decrypted, nil
		}
		defer func() { utils.DecryptPassword = originalDecryptPassword }()

		// Mock StorePasswordSecret
		originalStorePasswordSecret := adHelper.StorePasswordSecret
		adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
			return nil
		}
		defer func() { adHelper.StorePasswordSecret = originalStorePasswordSecret }()

		updateError := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, vsaerrors.New("database update failed"))
		mockStorage.On("UpdateActiveDirectory", ctx, mock.Anything).Return(nil, updateError)

		err := activity.CreateVcpActiveDirectory(ctx, params, ad)

		assert.Error(t, err)
		assert.Equal(t, updateError, err)
		mockStorage.AssertExpectations(t)
	})
}

func TestActiveDirectoryCreateActivity_CreateSdeActiveDirectory_Comprehensive(t *testing.T) {
	t.Run("SuccessfulCreation", func(t *testing.T) {
		mockActiveDirectoriesClient := active_directories.NewMockClientService(t)

		activity := &ActiveDirectoryCreateActivity{}

		ctx := context.Background()
		params := &common.CreateActiveDirectoryParams{
			ResourceId:                 "test-sde-ad",
			Domain:                     "sde.example.com",
			DNS:                        "8.8.8.8",
			NetBIOS:                    "SDETEST",
			OrganizationalUnit:         "OU=sde",
			Username:                   "sdeadmin",
			Password:                   "sdepass123",
			BackupOperators:            []string{"backup1", "backup2"},
			Administrators:             []string{"admin1", "admin2"},
			SecurityOperators:          []string{"security1"},
			KdcIP:                      "192.168.1.10",
			KdcHostname:                "kdc.sde.example.com",
			AesEncryption:              true,
			EncryptDCConnections:       true,
			LdapSigning:                true,
			AllowLocalNFSUsersWithLdap: true,
			Description:                "SDE Test AD",
			Site:                       "site1",
			AccountId:                  "456",
			LocationId:                 "us-central1",
			XCorrelationId:             "test-correlation-id",
		}

		expectedBody := &cvpModels.ActiveDirectoryV1beta{
			DNS:                        &params.DNS,
			Domain:                     &params.Domain,
			NetBIOS:                    &params.NetBIOS,
			Username:                   &params.Username,
			Password:                   &params.Password,
			ResourceID:                 &params.ResourceId,
			Administrators:             params.Administrators,
			SecurityOperators:          params.SecurityOperators,
			AesEncryption:              &params.AesEncryption,
			AllowLocalNFSUsersWithLdap: &params.AllowLocalNFSUsersWithLdap,
			BackupOperators:            params.BackupOperators,
			Description:                &params.Description,
			EncryptDCConnections:       &params.EncryptDCConnections,
			KdcIP:                      params.KdcIP,
			KdcHostname:                params.KdcHostname,
			Site:                       &params.Site,
			LdapSigning:                &params.LdapSigning,
			OrganizationalUnit:         &params.OrganizationalUnit,
		}

		mockActiveDirectoriesClient.On("V1betaCreateActiveDirectory", mock.MatchedBy(func(createParams *active_directories.V1betaCreateActiveDirectoryParams) bool {
			return createParams.LocationID == params.LocationId &&
				createParams.ProjectNumber == params.AccountId &&
				*createParams.XCorrelationID == params.XCorrelationId &&
				*createParams.Body.DNS == *expectedBody.DNS &&
				*createParams.Body.Domain == *expectedBody.Domain &&
				*createParams.Body.NetBIOS == *expectedBody.NetBIOS &&
				*createParams.Body.Username == *expectedBody.Username &&
				*createParams.Body.Password == *expectedBody.Password &&
				*createParams.Body.ResourceID == *expectedBody.ResourceID &&
				*createParams.Body.AesEncryption == *expectedBody.AesEncryption &&
				*createParams.Body.EncryptDCConnections == *expectedBody.EncryptDCConnections &&
				*createParams.Body.LdapSigning == *expectedBody.LdapSigning &&
				*createParams.Body.AllowLocalNFSUsersWithLdap == *expectedBody.AllowLocalNFSUsersWithLdap &&
				len(createParams.Body.BackupOperators) == len(expectedBody.BackupOperators) &&
				len(createParams.Body.Administrators) == len(expectedBody.Administrators) &&
				len(createParams.Body.SecurityOperators) == len(expectedBody.SecurityOperators)
		})).Return(&active_directories.V1betaCreateActiveDirectoryAccepted{
			Payload: &cvpModels.OperationV1beta{},
		}, nil)

		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockActiveDirectoriesClient}
		originalCvpClient := CvpClient
		defer func() { CvpClient = originalCvpClient }()
		CvpClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		err := activity.CreateSdeActiveDirectory(ctx, params)

		assert.NoError(t, err)
		mockActiveDirectoriesClient.AssertExpectations(t)
	})

	t.Run("CVPClientError", func(t *testing.T) {
		mockActiveDirectoriesClient := active_directories.NewMockClientService(t)

		activity := &ActiveDirectoryCreateActivity{}

		ctx := context.Background()
		params := &common.CreateActiveDirectoryParams{
			ResourceId:     "error-ad",
			Domain:         "error.example.com",
			DNS:            "8.8.8.8",
			NetBIOS:        "ERROR",
			Username:       "erroruser",
			Password:       "errorpass",
			AccountId:      "789",
			LocationId:     "us-east1",
			XCorrelationId: "error-corr-id",
		}

		expectedError := vsaerrors.New("CVP API error")

		mockActiveDirectoriesClient.On("V1betaCreateActiveDirectory", mock.Anything).
			Return(nil, expectedError)

		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockActiveDirectoriesClient}
		originalCvpClient := CvpClient
		defer func() { CvpClient = originalCvpClient }()
		CvpClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		err := activity.CreateSdeActiveDirectory(ctx, params)

		require.Error(t, err)
		var appErr *temporal.ApplicationError
		require.True(t, errors.As(err, &appErr), "expected Temporal ApplicationError")
		assert.False(t, appErr.NonRetryable(), "non-structured CVP errors should be retryable (transient)")
		customErr := vsaerrors.ExtractCustomError(err)
		require.NotNil(t, customErr)
		assert.True(t, customErr.IsError(vsaerrors.ErrCVPInternalServerError))
		mockActiveDirectoriesClient.AssertExpectations(t)
	})

	t.Run("NilPayloadResponse", func(t *testing.T) {
		mockActiveDirectoriesClient := active_directories.NewMockClientService(t)

		activity := &ActiveDirectoryCreateActivity{}

		ctx := context.Background()
		params := &common.CreateActiveDirectoryParams{
			ResourceId:     "nil-payload-ad",
			Domain:         "nilpayload.example.com",
			DNS:            "8.8.8.8",
			NetBIOS:        "NILPAY",
			Username:       "niluser",
			Password:       "nilpass",
			AccountId:      "111",
			LocationId:     "europe-west1",
			XCorrelationId: "nil-payload-corr-id",
		}

		mockActiveDirectoriesClient.On("V1betaCreateActiveDirectory", mock.Anything).
			Return(&active_directories.V1betaCreateActiveDirectoryAccepted{Payload: nil}, nil)

		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockActiveDirectoriesClient}
		originalCvpClient := CvpClient
		defer func() { CvpClient = originalCvpClient }()
		CvpClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		err := activity.CreateSdeActiveDirectory(ctx, params)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown error during the create active directory")
		mockActiveDirectoriesClient.AssertExpectations(t)
	})

	t.Run("NilResponseObject", func(t *testing.T) {
		mockActiveDirectoriesClient := active_directories.NewMockClientService(t)

		activity := &ActiveDirectoryCreateActivity{}

		ctx := context.Background()
		params := &common.CreateActiveDirectoryParams{
			ResourceId:     "nil-response-ad",
			Domain:         "nilresponse.example.com",
			DNS:            "8.8.8.8",
			NetBIOS:        "NILRESP",
			Username:       "nilrespuser",
			Password:       "nilresppass",
			AccountId:      "222",
			LocationId:     "asia-southeast1",
			XCorrelationId: "nil-response-corr-id",
		}

		mockActiveDirectoriesClient.On("V1betaCreateActiveDirectory", mock.Anything).
			Return(nil, nil)

		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockActiveDirectoriesClient}
		originalCvpClient := CvpClient
		defer func() { CvpClient = originalCvpClient }()
		CvpClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		err := activity.CreateSdeActiveDirectory(ctx, params)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unknown error during the create active directory")
		mockActiveDirectoriesClient.AssertExpectations(t)
	})

	t.Run("MultipleOperatorsAndAdmins", func(t *testing.T) {
		mockActiveDirectoriesClient := active_directories.NewMockClientService(t)

		activity := &ActiveDirectoryCreateActivity{}

		ctx := context.Background()
		params := &common.CreateActiveDirectoryParams{
			ResourceId:        "multi-ops-ad",
			Domain:            "multiops.example.com",
			DNS:               "8.8.8.8",
			NetBIOS:           "MULTIOPS",
			Username:          "multiuser",
			Password:          "multipass",
			BackupOperators:   []string{"backup1", "backup2", "backup3", "backup4"},
			Administrators:    []string{"admin1", "admin2", "admin3"},
			SecurityOperators: []string{"security1", "security2", "security3", "security4", "security5"},
			AccountId:         "444",
			LocationId:        "australia-southeast1",
			XCorrelationId:    "multi-corr-id",
		}

		mockActiveDirectoriesClient.On("V1betaCreateActiveDirectory", mock.MatchedBy(func(createParams *active_directories.V1betaCreateActiveDirectoryParams) bool {
			return len(createParams.Body.BackupOperators) == 4 &&
				len(createParams.Body.Administrators) == 3 &&
				len(createParams.Body.SecurityOperators) == 5 &&
				createParams.Body.BackupOperators[0] == "backup1" &&
				createParams.Body.Administrators[0] == "admin1" &&
				createParams.Body.SecurityOperators[0] == "security1"
		})).Return(&active_directories.V1betaCreateActiveDirectoryAccepted{
			Payload: &cvpModels.OperationV1beta{},
		}, nil)

		cvpClient := &cvpapi.Cvp{ActiveDirectories: mockActiveDirectoriesClient}
		originalCvpClient := CvpClient
		defer func() { CvpClient = originalCvpClient }()
		CvpClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		err := activity.CreateSdeActiveDirectory(ctx, params)

		assert.NoError(t, err)
		mockActiveDirectoriesClient.AssertExpectations(t)
	})
}

func TestActiveDirectoryCreateActivity_RollbackActiveDirectory(t *testing.T) {
	t.Run("SuccessfulRollbackWithCredentials", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()
		credentialPath := "projects/test-project/secrets/test-secret"
		ad := &datamodel.ActiveDirectory{
			BaseModel:      datamodel.BaseModel{UUID: "test-uuid"},
			AdName:         "test-ad",
			CredentialPath: credentialPath,
			State:          string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING),
		}

		// Mock successful secret deletion
		deleteSecretCalled := false
		originalDeleteSecret := adHelper.DeleteSecretFromGCP
		defer func() { adHelper.DeleteSecretFromGCP = originalDeleteSecret }()
		adHelper.DeleteSecretFromGCP = func(ctx context.Context, gcpService hyperscaler.Services, path string) error {
			deleteSecretCalled = true
			assert.Equal(t, credentialPath, path)
			return nil
		}

		// Mock successful database update
		mockStorage.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(updatedAd *datamodel.ActiveDirectory) bool {
			return updatedAd.UUID == ad.UUID && updatedAd.State == datamodel.LifeCycleStateError
		})).Return(ad, nil)

		err := activity.RollbackActiveDirectory(ctx, ad)

		assert.NoError(t, err)
		assert.True(t, deleteSecretCalled)
		assert.Equal(t, datamodel.LifeCycleStateError, ad.State)
		mockStorage.AssertExpectations(t)
	})

	t.Run("SuccessfulRollbackWithoutCredentials", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()
		ad := &datamodel.ActiveDirectory{
			BaseModel:      datamodel.BaseModel{UUID: "test-uuid"},
			AdName:         "test-ad",
			CredentialPath: "", // No credentials to delete
			State:          string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING),
		}

		// Verify DeleteSecretFromGCP is not called
		deleteSecretCalled := false
		originalDeleteSecret := adHelper.DeleteSecretFromGCP
		defer func() { adHelper.DeleteSecretFromGCP = originalDeleteSecret }()
		adHelper.DeleteSecretFromGCP = func(ctx context.Context, gcpService hyperscaler.Services, path string) error {
			deleteSecretCalled = true
			return nil
		}

		mockStorage.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(updatedAd *datamodel.ActiveDirectory) bool {
			return updatedAd.UUID == ad.UUID && updatedAd.State == datamodel.LifeCycleStateError
		})).Return(ad, nil)

		err := activity.RollbackActiveDirectory(ctx, ad)

		assert.NoError(t, err)
		assert.False(t, deleteSecretCalled)
		assert.Equal(t, datamodel.LifeCycleStateError, ad.State)
		mockStorage.AssertExpectations(t)
	})

	t.Run("NilActiveDirectory", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()

		err := activity.RollbackActiveDirectory(ctx, nil)

		assert.NoError(t, err)
		// No mock expectations needed as nothing should be called
	})

	t.Run("SecretDeletionFailure", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()
		credentialPath := "projects/test-project/secrets/test-secret"
		ad := &datamodel.ActiveDirectory{
			BaseModel:      datamodel.BaseModel{UUID: "test-uuid"},
			AdName:         "test-ad",
			CredentialPath: credentialPath,
			State:          string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING),
		}

		// Mock failed secret deletion
		expectedSecretError := vsaerror.New("failed to delete secret from GCP during AD creation rollback")
		originalDeleteSecret := adHelper.DeleteSecretFromGCP
		defer func() { adHelper.DeleteSecretFromGCP = originalDeleteSecret }()
		adHelper.DeleteSecretFromGCP = func(ctx context.Context, gcpService hyperscaler.Services, path string) error {
			return vsaerror.New("GCP secret deletion error")
		}

		// Database update should still be called due to defer, even when secret deletion fails
		mockStorage.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(updatedAd *datamodel.ActiveDirectory) bool {
			return updatedAd.UUID == ad.UUID && updatedAd.State == datamodel.LifeCycleStateError
		})).Return(ad, nil)

		err := activity.RollbackActiveDirectory(ctx, ad)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), expectedSecretError.Error())
		assert.Equal(t, datamodel.LifeCycleStateError, ad.State)
		mockStorage.AssertExpectations(t)
	})

	t.Run("DatabaseUpdateFailure", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()
		credentialPath := "projects/test-project/secrets/test-secret"
		ad := &datamodel.ActiveDirectory{
			BaseModel:      datamodel.BaseModel{UUID: "test-uuid"},
			AdName:         "test-ad",
			CredentialPath: credentialPath,
			State:          string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING),
		}

		// Mock successful secret deletion
		originalDeleteSecret := adHelper.DeleteSecretFromGCP
		defer func() { adHelper.DeleteSecretFromGCP = originalDeleteSecret }()
		adHelper.DeleteSecretFromGCP = func(ctx context.Context, gcpService hyperscaler.Services, path string) error {
			return nil
		}

		// Mock failed database update - error is logged but not returned
		updateError := vsaerrors.NewVCPError(vsaerrors.ErrDatabaseDataUpdateError, vsaerrors.New("database update failed"))
		mockStorage.On("UpdateActiveDirectory", ctx, mock.Anything).Return(nil, updateError)

		// No error should be returned as the defer catches and logs update errors
		err := activity.RollbackActiveDirectory(ctx, ad)

		assert.NoError(t, err)
		assert.Equal(t, datamodel.LifeCycleStateError, ad.State)
		mockStorage.AssertExpectations(t)
	})

	t.Run("StateTransitionValidation", func(t *testing.T) {
		mockStorage := database.NewMockStorage(t)
		activity := &ActiveDirectoryCreateActivity{
			SE: mockStorage,
		}

		ctx := context.Background()
		originalState := string(gcpgenserver.ActiveDirectoryV1betaActiveDirectoryStateCREATING)
		ad := &datamodel.ActiveDirectory{
			BaseModel:      datamodel.BaseModel{UUID: "test-uuid"},
			AdName:         "test-ad",
			CredentialPath: "",
			State:          originalState,
		}

		mockStorage.On("UpdateActiveDirectory", ctx, mock.MatchedBy(func(updatedAd *datamodel.ActiveDirectory) bool {
			// Verify state transition from CREATING to ERROR
			return updatedAd.State == datamodel.LifeCycleStateError
		})).Return(ad, nil)

		err := activity.RollbackActiveDirectory(ctx, ad)

		assert.NoError(t, err)
		assert.Equal(t, datamodel.LifeCycleStateError, ad.State)
		assert.NotEqual(t, originalState, ad.State)
		mockStorage.AssertExpectations(t)
	})
}
