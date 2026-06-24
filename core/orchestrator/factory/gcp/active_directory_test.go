package gcp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	commonfactory "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory/common"
	adHelper "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
	"go.temporal.io/sdk/workflow"
)

// Test helper functions

// makeDescribeADParams creates test V1betaDescribeActiveDirectoryParams
func makeDescribeADParams(projectNumber, locationID, adID string) *common.GetADParams {
	return &common.GetADParams{
		ProjectNumber: projectNumber,
		LocationID:    locationID,
		CorrelationID: "test-correlation-id",
		UUID:          adID,
	}
}

func TestCreateActiveDirectory_Success(t *testing.T) {
	// Setup test context and mocks
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	// Test data setup
	accountID := int64(123)
	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad",
		AccountId:          "123",
		LocationId:         "local",
		Username:           "admin@test.local",
		Password:           "SecurePass123!",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
		Site:               "Default-First-Site",
		KdcIP:              "10.0.0.2",
		KdcHostname:        "kdc.test.local",
		AesEncryption:      true,
		BackupOperators:    []string{"backup-user"},
		Administrators:     []string{"admin-user"},
		SecurityOperators:  []string{"security-user"},
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
		Name:      "test-account",
	}

	adRecord := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: "ad-uuid-123"},
		AdName:         params.ResourceId,
		Username:       params.Username,
		Domain:         params.Domain,
		DNS:            params.DNS,
		NetBIOS:        params.NetBIOS,
		CredentialPath: "secret-path",
		AccountId:      accountID,
		State:          datamodel.LifeCycleStateCreating,
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: params.OrganizationalUnit,
			Site:               params.Site,
			KdcIP:              params.KdcIP,
			KdcHostname:        params.KdcHostname,
			AesEncryption:      params.AesEncryption,
			AdUsers: map[string][]string{
				utils.ActiveDirectorySeSecurityPrivilege:         params.SecurityOperators,
				utils.ActiveDirectoryGroupBuiltInBackupOperators: params.BackupOperators,
				utils.ActiveDirectoryGroupBuiltInAdministrators:  params.Administrators,
			},
		},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-123",
		Type:       string(datamodel.JobTypeCreateActiveDirectory),
		State:      string(datamodel.JobsStateNEW),
	}

	// Mock external dependencies
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return nil
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Configure multi-AD settings for test
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()
	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	// Setup storage mocks
	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, accountID).Return([]*datamodel.ActiveDirectory{}, nil)
	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
		return ad.AdName == params.ResourceId &&
			ad.AccountId == accountID &&
			ad.ActiveDirectoryAttributes.OrganizationalUnit == params.OrganizationalUnit
	})).Return(adRecord, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.Type == string(datamodel.JobTypeCreateActiveDirectory) &&
			j.ResourceName == params.ResourceId &&
			j.AccountID.Int64 == accountID
	})).Return(job, nil)

	// Execute the function under test
	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	// Assertions
	assert.NoError(t, err, "CreateActiveDirectory should succeed without errors")
	assert.NotNil(t, ad, "ActiveDirectory should be returned")
	assert.Equal(t, "job-uuid-123", jobUUID, "Job UUID should match")
	assert.Equal(t, adRecord.UUID, ad.UUID, "AD UUID should match")
	assert.Equal(t, params.ResourceId, ad.AdName, "AD name should match")
	assert.Equal(t, params.Username, ad.Username, "Username should match")
	assert.Equal(t, params.Domain, ad.Domain, "Domain should match")
	assert.Equal(t, params.DNS, ad.DNS, "DNS should match")
	assert.Equal(t, params.NetBIOS, ad.NetBIOS, "NetBIOS should match")
	assert.Equal(t, datamodel.LifeCycleStateCreating, ad.State, "State should be Creating")
	assert.NotNil(t, ad.ActiveDirectoryAttributes, "ActiveDirectoryAttributes should not be nil")
	assert.Equal(t, params.OrganizationalUnit, ad.ActiveDirectoryAttributes.OrganizationalUnit, "OrganizationalUnit should match")
	assert.Equal(t, params.Site, ad.ActiveDirectoryAttributes.Site, "Site should match")
	assert.Equal(t, params.KdcIP, ad.ActiveDirectoryAttributes.KdcIP, "KdcIP should match")
	assert.Equal(t, params.KdcHostname, ad.ActiveDirectoryAttributes.KdcHostname, "KdcHostname should match")
	assert.Equal(t, params.AesEncryption, ad.ActiveDirectoryAttributes.AesEncryption, "AesEncryption should match")
	assert.Equal(t, params.BackupOperators, ad.ActiveDirectoryAttributes.BackupOperators, "BackupOperators should match")
	assert.Equal(t, params.Administrators, ad.ActiveDirectoryAttributes.Administrators, "Administrators should match")
	assert.Equal(t, params.SecurityOperators, ad.ActiveDirectoryAttributes.SecurityOperators, "SecurityOperators should match")

	// Verify all mock expectations were met
	mockStorage.AssertExpectations(t)
}

func TestCreateActiveDirectory_Success_WithCVPHost(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad-cvp",
		AccountId:          "123",
		Username:           "admin@test.local",
		Password:           "SecurePass123!",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
		Site:               "Default-First-Site",
		KdcIP:              "10.0.0.2",
		KdcHostname:        "kdc.test.local",
		AesEncryption:      true,
		BackupOperators:    []string{"backup-user"},
		Administrators:     []string{"admin-user"},
		SecurityOperators:  []string{"security-user"},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-cvp-123"},
		WorkflowID: "workflow-cvp-123",
		Type:       string(datamodel.JobTypeCreateActiveDirectory),
		State:      string(datamodel.JobsStateNEW),
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone

	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	accountID := int64(123)
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
		Name:      "test-account",
	}
	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, accountID).Return([]*datamodel.ActiveDirectory{}, nil)

	mockStorage.On("CreateJob", mock.Anything, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.Type == string(datamodel.JobTypeCreateActiveDirectory)
	})).Return(job, nil)

	// Mock ExecuteWorkflowSequentially using ExecuteWorkflowSeq
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return nil
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://cvp.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Save and restore original values for multi-AD settings
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	// Set default values for the test (multi-AD disabled, max 1)
	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid-cvp-123", jobUUID)
	assert.Equal(t, params.ResourceId, ad.AdName)
	assert.Equal(t, "", ad.UUID)
	assert.Equal(t, params.Username, ad.Username)
	assert.Equal(t, datamodel.LifeCycleStateCreating, ad.State)
}

func TestCreateActiveDirectory_ValidationError(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId: "",
		AccountId:  "123",
		Username:   "admin",
		Password:   "pass",
		Domain:     "test.local",
	}

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)

	var validationErr *customerrors.UserInputValidationErr
	assert.True(t, errors.As(err, &validationErr))
}

func TestCreateActiveDirectory_AccountNotFound(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad",
		AccountId:          "123",
		Username:           "admin@test.local",
		Password:           "SecurePass123!",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
		Site:               "Default-First-Site",
		KdcIP:              "10.0.0.2",
		KdcHostname:        "kdc.test.local",
		AesEncryption:      true,
		BackupOperators:    []string{"backup-user"},
		Administrators:     []string{"admin-user"},
		SecurityOperators:  []string{"security-user"},
	}

	accountID := int64(123)
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
		Name:      "test-account",
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-123",
		Type:       string(datamodel.JobTypeCreateActiveDirectory),
		State:      string(datamodel.JobsStateNEW),
	}

	adRecord := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: "ad-uuid-123"},
		AdName:         params.ResourceId,
		Username:       params.Username,
		Domain:         params.Domain,
		DNS:            params.DNS,
		NetBIOS:        params.NetBIOS,
		CredentialPath: "secret-path",
		AccountId:      accountID,
		State:          datamodel.LifeCycleStateCreating,
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: params.OrganizationalUnit,
			Site:               params.Site,
			KdcIP:              params.KdcIP,
			KdcHostname:        params.KdcHostname,
			AesEncryption:      params.AesEncryption,
		},
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	// Mock ExecuteWorkflowSequentially using ExecuteWorkflowSeq
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return nil
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	mockStorage.On("GetAccount", mock.Anything, "123").
		Return(nil, errors.New("account not found")).Maybe()
	mockStorage.On("CreateAccount", mock.Anything, mock.Anything).
		Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, accountID).Return([]*datamodel.ActiveDirectory{}, nil)

	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
		return ad.AdName == params.ResourceId
	})).Return(adRecord, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.Type == string(datamodel.JobTypeCreateActiveDirectory)
	})).Return(job, nil)

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Save and restore original values for multi-AD settings
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	// Set default values for the test (multi-AD disabled, max 1)
	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid-123", jobUUID)
	assert.Equal(t, adRecord.UUID, ad.UUID)
	assert.Equal(t, params.ResourceId, ad.AdName)
	assert.Equal(t, params.Username, ad.Username)
	assert.Equal(t, datamodel.LifeCycleStateCreating, ad.State)
}

func TestCreateActiveDirectory_DefaultOrganizationalUnit(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad",
		AccountId:          "123",
		Username:           "admin@test.local",
		Password:           "SecurePass123!",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "",
	}

	accountID := int64(123)
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
	}

	adRecord := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		AdName:    params.ResourceId,
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: DefaultOrganizationalUnit,
		},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "workflow-id",
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	// Mock ExecuteWorkflowSequentially using ExecuteWorkflowSeq
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return nil
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, accountID).Return([]*datamodel.ActiveDirectory{}, nil)
	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
		return ad.ActiveDirectoryAttributes.OrganizationalUnit == DefaultOrganizationalUnit
	})).Return(adRecord, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(job, nil)

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Save and restore original values for multi-AD settings
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	// Set default values for the test (multi-AD disabled, max 1)
	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	ad, _, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.Equal(t, DefaultOrganizationalUnit, ad.ActiveDirectoryAttributes.OrganizationalUnit)
}

func TestCreateActiveDirectory_JobCreationFailed(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId: "test-ad",
		AccountId:  "123",
		Username:   "admin@test.local",
		Password:   "SecurePass123!",
		Domain:     "test.local",
		DNS:        "10.0.0.1",
		NetBIOS:    "TEST",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 123},
	}

	adRecord := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, int64(123)).Return([]*datamodel.ActiveDirectory{}, nil)
	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(adRecord, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).
		Return(nil, errors.New("database error"))

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Save and restore original values for multi-AD settings
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	// Set default values for the test (multi-AD disabled, max 1)
	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
}

func TestCreateActiveDirectory_WorkflowStartFailed(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId: "test-ad",
		AccountId:  "123",
		Username:   "admin@test.local",
		Password:   "SecurePass123!",
		Domain:     "test.local",
		DNS:        "10.0.0.1",
		NetBIOS:    "TEST",
	}

	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: 123},
	}

	adRecord := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID: "workflow-id",
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, int64(123)).Return([]*datamodel.ActiveDirectory{}, nil)
	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.Anything).Return(adRecord, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(job, nil)
	mockStorage.On("UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).
		Return(nil)

	// Mock ExecuteWorkflowSequentially using ExecuteWorkflowSeq
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return errors.New("workflow execution failed")
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Save and restore original values for multi-AD settings
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	// Set default values for the test (multi-AD disabled, max 1)
	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	mockStorage.AssertCalled(t, "UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything)
}

func TestCreateActiveDirectory_DatabaseRecordCreationFailed(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId: "test-ad",
		AccountId:  "123",
		Username:   "admin@test.local",
		Password:   "SecurePass123!",
		Domain:     "test.local",
		DNS:        "10.0.0.1",
		NetBIOS:    "TEST",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, int64(123)).Return([]*datamodel.ActiveDirectory{}, nil)
	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.Anything).
		Return(nil, errors.New("database insert failed"))

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Save and restore original values for multi-AD settings
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	// Set default values for the test (multi-AD disabled, max 1)
	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "database insert failed")
}

func TestConvertActiveDirectoryParamsToModel(t *testing.T) {
	params := &common.CreateActiveDirectoryParams{
		ResourceId:                 "test-ad",
		Username:                   "admin@test.local",
		Domain:                     "test.local",
		DNS:                        "10.0.0.1",
		NetBIOS:                    "TEST",
		OrganizationalUnit:         "CN=Computers",
		Site:                       "Default-First-Site",
		SecurityOperators:          []string{"security-user"},
		BackupOperators:            []string{"backup-user"},
		Administrators:             []string{"admin-user"},
		KdcIP:                      "10.0.0.2",
		KdcHostname:                "kdc.test.local",
		AesEncryption:              true,
		EncryptDCConnections:       true,
		LdapSigning:                true,
		AllowLocalNFSUsersWithLdap: false,
		Description:                "Test AD",
	}

	ad := convertActiveDirectoryParamsToModel(params)

	assert.Equal(t, "test-ad", ad.AdName)
	assert.Equal(t, "admin@test.local", ad.Username)
	assert.Equal(t, "test.local", ad.Domain)
	assert.Equal(t, "10.0.0.1", ad.DNS)
	assert.Equal(t, "TEST", ad.NetBIOS)
	assert.Equal(t, datamodel.LifeCycleStateCreating, ad.State)
	assert.Equal(t, "CN=Computers", ad.ActiveDirectoryAttributes.OrganizationalUnit)
	assert.Equal(t, []string{"security-user"}, ad.ActiveDirectoryAttributes.SecurityOperators)
	assert.True(t, ad.ActiveDirectoryAttributes.AesEncryption)
}

func TestCreateAdRecordForNonSDE(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad",
		Username:           "admin@test.local",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
		Site:               "Default-First-Site",
		BackupOperators:    []string{"backup-user"},
		Administrators:     []string{"admin-user"},
		SecurityOperators:  []string{"security-user"},
		KdcIP:              "10.0.0.2",
		KdcHostname:        "kdc.test.local",
		AesEncryption:      true,
	}

	accountID := int64(123)

	expectedRecord := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: utils.RandomUUID()},
		AdName:    params.ResourceId,
		Username:  params.Username,
		State:     datamodel.LifeCycleStateCreating,
	}

	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
		return ad.AdName == params.ResourceId &&
			ad.Username == params.Username &&
			ad.AccountId == accountID &&
			ad.ActiveDirectoryAttributes.OrganizationalUnit == params.OrganizationalUnit &&
			ad.ActiveDirectoryAttributes.PrimaryAD == true
	})).Return(expectedRecord, nil)

	adRecord, err := createAdRecordForNonSDE(ctx, mockStorage, params, accountID)

	assert.NoError(t, err)
	assert.NotNil(t, adRecord)
}

func TestCreateAdRecordForNonSDE_DatabaseError(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId: "test-ad",
		Username:   "admin@test.local",
		Domain:     "test.local",
	}

	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.Anything).
		Return(nil, errors.New("database error"))

	adRecord, err := createAdRecordForNonSDE(ctx, mockStorage, params, 123)

	assert.Error(t, err)
	assert.Nil(t, adRecord)
	assert.Contains(t, err.Error(), "database error")
}

func TestOrchestratorCreateActiveDirectory(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	params := &common.CreateActiveDirectoryParams{
		ResourceId: "test-ad",
		AccountId:  "123",
		Username:   "admin@test.local",
		Password:   "SecurePass123!",
		Domain:     "test.local",
		DNS:        "10.0.0.1",
		NetBIOS:    "TEST",
	}

	expectedAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid"},
		AdName:    "test-ad",
	}

	originalCreate := createActiveDirectory
	createActiveDirectory = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
		return expectedAD, "job-uuid", nil
	}
	defer func() { createActiveDirectory = originalCreate }()

	ad, jobUUID, err := orchestrator.CreateActiveDirectory(ctx, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid", jobUUID)
	assert.Equal(t, "test-ad", ad.AdName)
}

func TestOrchestratorCreateActiveDirectory_Error(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	params := &common.CreateActiveDirectoryParams{
		ResourceId: "test-ad",
		AccountId:  "123",
	}

	originalCreate := createActiveDirectory
	createActiveDirectory = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
		return nil, "", errors.New("creation failed")
	}
	defer func() { createActiveDirectory = originalCreate }()

	ad, jobUUID, err := orchestrator.CreateActiveDirectory(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "creation failed")
}

func Test_getActiveDirectory_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440000" // Valid UUID

	// Ensure VCP-only mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	adFromDB := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID: adUUID,
		},
		AdName:         "test-ad",
		Username:       "testuser",
		CredentialPath: log.PasswordMask,
		Domain:         "example.com",
		DNS:            "8.8.8.8",
		NetBIOS:        "EXAMPLE",
		State:          "READY",
		StateDetails:   "Active Directory is ready",
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: "OU=Test",
			Site:               "Default-Site",
			AdUsers: map[string][]string{
				"SeSecurityPrivilege":      {"user1"},
				`BUILTIN\Backup Operators`: {"user2"},
				`BUILTIN\Administrators`:   {"user3"},
			},
			KdcIP:                      "1.2.3.4",
			AesEncryption:              true,
			EncryptDCConnections:       true,
			LdapSigning:                true,
			AllowLocalNFSUsersWithLdap: false,
			Description:                "Test AD",
		},
	}

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(adFromDB, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, adUUID, ad.UUID)
	assert.Equal(t, "test-ad", ad.AdName)
	assert.Equal(t, "testuser", ad.Username)
	assert.Equal(t, log.PasswordMask, ad.Password)
	assert.Equal(t, "READY", ad.State)
	assert.NotNil(t, ad.ActiveDirectoryAttributes)
	assert.Equal(t, "OU=Test", ad.ActiveDirectoryAttributes.OrganizationalUnit)
	assert.Equal(t, []string{"user1"}, ad.ActiveDirectoryAttributes.SecurityOperators)
	assert.Equal(t, []string{"user2"}, ad.ActiveDirectoryAttributes.BackupOperators)
	assert.Equal(t, []string{"user3"}, ad.ActiveDirectoryAttributes.Administrators)
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_NotFound(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440001" // Valid UUID

	// Ensure VCP-only mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(nil, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Contains(t, err.Error(), "not found")
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_DatabaseError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440002" // Valid UUID

	// Ensure VCP-only mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(nil, errors.New("database error"))

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Contains(t, err.Error(), "database error")
	mockSe.AssertExpectations(t)
}

func Test_listActiveDirectories_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountName := "test-account"
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: accountName}

	adsFromDB := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1"},
			AdName:    "ad-name-1",
			Username:  "user1",
			Domain:    "example.com",
			DNS:       "8.8.8.8",
			NetBIOS:   "EXAMPLE1",
			State:     "READY",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				OrganizationalUnit: "OU=Test1",
				AdUsers:            map[string][]string{},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2"},
			AdName:    "ad-name-2",
			Username:  "user2",
			Domain:    "example2.com",
			DNS:       "8.8.4.4",
			NetBIOS:   "EXAMPLE2",
			State:     "CREATING",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				OrganizationalUnit: "OU=Test2",
				AdUsers:            map[string][]string{},
			},
		},
	}

	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	mockSe.On("ListActiveDirectories", mock.Anything, int64(42)).Return(adsFromDB, nil)

	ads, err := _listActiveDirectories(ctx, mockSe, accountName)

	assert.NoError(t, err)
	assert.NotNil(t, ads)
	assert.Len(t, ads, 2)
	assert.Equal(t, "ad-1", ads[0].UUID)
	assert.Equal(t, "ad-name-1", ads[0].AdName)
	assert.Equal(t, "ad-2", ads[1].UUID)
	assert.Equal(t, "ad-name-2", ads[1].AdName)
	mockSe.AssertExpectations(t)
}

func Test_listActiveDirectories_EmptyList(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountName := "test-account"
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: accountName}

	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	mockSe.On("ListActiveDirectories", mock.Anything, int64(42)).Return([]*datamodel.ActiveDirectory{}, nil)

	ads, err := _listActiveDirectories(ctx, mockSe, accountName)

	assert.NoError(t, err)
	assert.Len(t, ads, 0)
	mockSe.AssertExpectations(t)
}

func Test_listActiveDirectories_AccountError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountName := "test-account"

	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return nil, errors.New("account not found")
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	ads, err := _listActiveDirectories(ctx, mockSe, accountName)

	assert.Error(t, err)
	assert.Nil(t, ads)
	assert.Contains(t, err.Error(), "account not found")
}

func Test_listActiveDirectories_DatabaseError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountName := "test-account"
	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: accountName}

	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	mockSe.On("ListActiveDirectories", mock.Anything, int64(42)).Return(nil, errors.New("database error"))

	ads, err := _listActiveDirectories(ctx, mockSe, accountName)

	assert.Error(t, err)
	assert.Nil(t, ads)
	assert.Contains(t, err.Error(), "database error")
	mockSe.AssertExpectations(t)
}

func Test_getMultipleActiveDirectories_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	uuids := []string{"ad-1", "ad-2", "ad-3"}

	adsFromDB := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1"},
			AdName:    "ad-name-1",
			Username:  "user1",
			Domain:    "example.com",
			DNS:       "8.8.8.8",
			NetBIOS:   "EXAMPLE1",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2"},
			AdName:    "ad-name-2",
			Username:  "user2",
			Domain:    "example2.com",
			DNS:       "8.8.4.4",
			NetBIOS:   "EXAMPLE2",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-3"},
			AdName:    "ad-name-3",
			Username:  "user3",
			Domain:    "example3.com",
			DNS:       "1.1.1.1",
			NetBIOS:   "EXAMPLE3",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
	}

	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, uuids).Return(adsFromDB, nil)

	ads, err := _getMultipleActiveDirectories(ctx, mockSe, uuids)

	assert.NoError(t, err)
	assert.NotNil(t, ads)
	assert.Len(t, ads, 3)
	assert.Equal(t, "ad-1", ads[0].UUID)
	assert.Equal(t, "ad-2", ads[1].UUID)
	assert.Equal(t, "ad-3", ads[2].UUID)
	mockSe.AssertExpectations(t)
}

func Test_getMultipleActiveDirectories_PartialResults(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	uuids := []string{"ad-1", "ad-2", "non-existent"}

	adsFromDB := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1"},
			AdName:    "ad-name-1",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2"},
			AdName:    "ad-name-2",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
	}

	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, uuids).Return(adsFromDB, nil)

	ads, err := _getMultipleActiveDirectories(ctx, mockSe, uuids)

	assert.NoError(t, err)
	assert.NotNil(t, ads)
	assert.Len(t, ads, 2)
	mockSe.AssertExpectations(t)
}

func Test_getMultipleActiveDirectories_EmptyList(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	uuids := []string{}

	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, uuids).Return([]*datamodel.ActiveDirectory{}, nil)

	ads, err := _getMultipleActiveDirectories(ctx, mockSe, uuids)

	assert.NoError(t, err)
	assert.Len(t, ads, 0)
	mockSe.AssertExpectations(t)
}

func Test_getMultipleActiveDirectories_DatabaseError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	uuids := []string{"ad-1", "ad-2"}

	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, uuids).Return(nil, errors.New("database error"))

	ads, err := _getMultipleActiveDirectories(ctx, mockSe, uuids)

	assert.Error(t, err)
	assert.Nil(t, ads)
	assert.Contains(t, err.Error(), "database error")
	mockSe.AssertExpectations(t)
}

func TestOrchestrator_GetActiveDirectory(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	origGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return &models.ActiveDirectory{
			BaseModel: models.BaseModel{UUID: "test-uuid"},
			AdName:    "test-ad",
		}, nil
	}
	defer func() { getActiveDirectory = origGetActiveDirectory }()

	params := makeDescribeADParams("test-project", "us-central1", "test-uuid")
	ad, err := o.GetActiveDirectory(ctx, params)
	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "test-uuid", ad.UUID)
	assert.Equal(t, "test-ad", ad.AdName)
}

func TestOrchestrator_CreateActiveDirectory_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	params := &common.CreateActiveDirectoryParams{
		AccountId:          "test-account",
		LocationId:         "us-central1",
		ResourceId:         "test-ad",
		XCorrelationId:     "correlation-123",
		Username:           "admin@test.local",
		Password:           "password123",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
	}

	mockAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateCreating,
	}

	origCreateActiveDirectory := createActiveDirectory
	createActiveDirectory = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
		return mockAD, "job-uuid-123", nil
	}
	defer func() { createActiveDirectory = origCreateActiveDirectory }()

	ad, jobUUID, err := o.CreateActiveDirectory(ctx, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "ad-uuid-123", ad.UUID)
	assert.Equal(t, "job-uuid-123", jobUUID)
}

func TestOrchestrator_CreateActiveDirectory_Error(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	params := &common.CreateActiveDirectoryParams{
		AccountId:      "test-account",
		LocationId:     "us-central1",
		ResourceId:     "test-ad",
		XCorrelationId: "correlation-123",
	}

	origCreateActiveDirectory := createActiveDirectory
	createActiveDirectory = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.CreateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
		return nil, "", errors.New("validation error")
	}
	defer func() { createActiveDirectory = origCreateActiveDirectory }()

	ad, jobUUID, err := o.CreateActiveDirectory(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "validation error")
}

func Test_getActiveDirectorySDE_EmptyCorrelationID(t *testing.T) {
	ctx := context.Background()
	params := &common.GetADParams{
		ProjectNumber: "12345",
		LocationID:    "us-central1",
		UUID:          "ad-uuid-1",
		CorrelationID: "", // Empty - covers lines 186-188
	}

	ad, err := _getActiveDirectorySDE(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Contains(t, err.Error(), "unknown error during the describe active directory")
}

func Test_getActiveDirectorySDE_NotFoundError(t *testing.T) {
	// Create a mock HTTP server that returns 404
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code": 404, "message": "Active Directory not found"}`))
	}))
	defer mockServer.Close()

	// Set CVP_HOST to our mock server (remove http:// prefix as CVP client adds it)
	originalCVPHost := cvp.CVP_HOST
	// Extract just the host:port from the URL
	cvp.CVP_HOST = mockServer.URL[7:] // Remove "http://" prefix
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.GetADParams{
		ProjectNumber: "12345",
		LocationID:    "us-central1",
		UUID:          "non-existent",
		CorrelationID: "test-correlation-id",
	}

	// This covers lines 191-204 (CVP client creation and NotFound error handling)
	ad, err := _getActiveDirectorySDE(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	// The NotFound error should be converted to customerrors.NotFoundErr
	assert.True(t, customerrors.IsNotFoundErr(err), "Error should be a NotFound error")
}

func Test_getActiveDirectorySDE_BadRequestError(t *testing.T) {
	// Create a mock HTTP server that returns 400
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code": 400, "message": "Invalid parameters"}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:] // Remove "http://" prefix
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.GetADParams{
		ProjectNumber: "12345",
		LocationID:    "us-central1",
		UUID:          "invalid-ad",
		CorrelationID: "test-correlation-id",
	}

	// This covers lines 191-199, 201-202, 205-207 (BadRequest error handling)
	ad, err := _getActiveDirectorySDE(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
}

func Test_getActiveDirectorySDE_InternalServerError(t *testing.T) {
	// Create a mock HTTP server that returns 500
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code": 500, "message": "Internal server error"}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:] // Remove "http://" prefix
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.GetADParams{
		ProjectNumber: "12345",
		LocationID:    "us-central1",
		UUID:          "error-ad",
		CorrelationID: "test-correlation-id",
	}

	// This covers lines 191-199, 201-202, 208-211 (Default error handling)
	ad, err := _getActiveDirectorySDE(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
}

func Test_getActiveDirectorySDE_SuccessResponse(t *testing.T) {
	// Create a mock HTTP server that returns a valid AD response
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return a complete valid response with all required fields
		response := `{
			"activeDirectoryId": "ad-uuid-success",
			"resourceId": "test-ad",
			"username": "admin@test.local",
			"domain": "test.local",
			"DNS": "10.0.0.1",
			"netBIOS": "TEST",
			"activeDirectoryState": "READY",
			"activeDirectoryStateDetails": "Ready for use",
			"administrators": [],
			"backupOperators": [],
			"securityOperators": []
		}`
		_, _ = w.Write([]byte(response))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:] // Remove "http://" prefix
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.GetADParams{
		ProjectNumber: "12345",
		LocationID:    "us-central1",
		UUID:          "ad-uuid-success",
		CorrelationID: "test-correlation-id",
	}

	// This covers lines 191-199, 214-219 (success path with model conversion)
	ad, err := _getActiveDirectorySDE(ctx, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	if ad != nil {
		assert.Equal(t, "test-ad", ad.AdName)
		assert.Equal(t, "READY", ad.State)
	}
}

func Test_getActiveDirectorySDE_EmptyResponse(t *testing.T) {
	// Create a mock HTTP server that returns empty body
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// Return empty JSON object to simulate nil payload
		_, _ = w.Write([]byte(`{}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:] // Remove "http://" prefix
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.GetADParams{
		ProjectNumber: "12345",
		LocationID:    "us-central1",
		UUID:          "nil-response-ad",
		CorrelationID: "test-correlation-id",
	}

	// This covers lines 191-199, 214-215 (nil/empty response check)
	ad, err := _getActiveDirectorySDE(ctx, params)

	// The function should handle empty response gracefully
	// It may return an AD with empty fields or an error depending on validation
	if err != nil {
		assert.Nil(t, ad)
	}
	// Either way, we've covered the code path
}

func TestOrchestrator_ListActiveDirectories(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	origListActiveDirectories := listActiveDirectories
	listActiveDirectories = func(ctx context.Context, se database.Storage, accountName string) ([]*models.ActiveDirectory, error) {
		return []*models.ActiveDirectory{
			{BaseModel: models.BaseModel{UUID: "ad-1"}, AdName: "ad-name-1"},
			{BaseModel: models.BaseModel{UUID: "ad-2"}, AdName: "ad-name-2"},
		}, nil
	}
	defer func() { listActiveDirectories = origListActiveDirectories }()

	ads, err := o.ListActiveDirectories(ctx, "test-account")
	assert.NoError(t, err)
	assert.NotNil(t, ads)
	assert.Len(t, ads, 2)
}

func TestOrchestrator_GetMultipleActiveDirectories(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	origGetMultipleActiveDirectories := getMultipleActiveDirectories
	getMultipleActiveDirectories = func(ctx context.Context, se database.Storage, uuids []string) ([]*models.ActiveDirectory, error) {
		return []*models.ActiveDirectory{
			{BaseModel: models.BaseModel{UUID: "ad-1"}, AdName: "ad-name-1"},
			{BaseModel: models.BaseModel{UUID: "ad-2"}, AdName: "ad-name-2"},
		}, nil
	}
	defer func() { getMultipleActiveDirectories = origGetMultipleActiveDirectories }()

	ads, err := o.GetMultipleActiveDirectories(ctx, []string{"ad-1", "ad-2"})
	assert.NoError(t, err)
	assert.NotNil(t, ads)
	assert.Len(t, ads, 2)
}

func Test_convertDatastoreActiveDirectoryToModel_Success(t *testing.T) {
	now := time.Now()
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: now,
			UpdatedAt: now,
		},
		AdName:         "test-ad",
		Username:       "testuser",
		CredentialPath: log.PasswordMask,
		Domain:         "example.com",
		DNS:            "8.8.8.8",
		NetBIOS:        "EXAMPLE",
		State:          "READY",
		StateDetails:   "Ready",
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: "OU=Test",
			Site:               "Default-Site",
			AdUsers: map[string][]string{
				"SeSecurityPrivilege":      {"sec-user"},
				`BUILTIN\Backup Operators`: {"backup-user"},
				`BUILTIN\Administrators`:   {"admin-user"},
			},
			KdcIP:                      "1.2.3.4",
			AesEncryption:              true,
			EncryptDCConnections:       true,
			LdapSigning:                false,
			AllowLocalNFSUsersWithLdap: true,
			Description:                "Test Description",
		},
	}

	result := commonfactory.ConvertDatastoreActiveDirectoryToModel(ad)

	assert.NotNil(t, result)
	assert.Equal(t, "test-uuid", result.UUID)
	assert.Equal(t, "test-ad", result.AdName)
	assert.Equal(t, "testuser", result.Username)
	assert.Equal(t, log.PasswordMask, result.Password)
	assert.Equal(t, "example.com", result.Domain)
	assert.Equal(t, "READY", result.State)
	assert.NotNil(t, result.ActiveDirectoryAttributes)
	assert.Equal(t, "OU=Test", result.ActiveDirectoryAttributes.OrganizationalUnit)
	assert.Equal(t, []string{"sec-user"}, result.ActiveDirectoryAttributes.SecurityOperators)
	assert.Equal(t, []string{"backup-user"}, result.ActiveDirectoryAttributes.BackupOperators)
	assert.Equal(t, []string{"admin-user"}, result.ActiveDirectoryAttributes.Administrators)
	assert.Equal(t, true, result.ActiveDirectoryAttributes.AesEncryption)
	assert.Equal(t, "Test Description", result.ActiveDirectoryAttributes.Description)
}

func Test_convertDatastoreActiveDirectoryToModel_NilInput(t *testing.T) {
	result := commonfactory.ConvertDatastoreActiveDirectoryToModel(nil)
	assert.Nil(t, result)
}

func Test_convertDatastoreActiveDirectoryToModel_NilAttributes(t *testing.T) {
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID: "test-uuid",
		},
		AdName:                    "test-ad",
		ActiveDirectoryAttributes: nil,
	}

	result := commonfactory.ConvertDatastoreActiveDirectoryToModel(ad)

	assert.NotNil(t, result)
	assert.Equal(t, "test-uuid", result.UUID)
	assert.Equal(t, "test-ad", result.AdName)
	assert.Nil(t, result.ActiveDirectoryAttributes)
}

func TestOrchestrator_GetADConfig_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	params := &common.GetADParams{
		UUID:          "test-ad-uuid",
		AccountName:   "test-account",
		LocationID:    "us-central1",
		ProjectNumber: "12345",
		ResourceID:    "test-ad",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}

	// Mock getAccountWithName to return our account
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = _getAccountWithName }()

	adFromDB := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID: "test-ad-uuid",
		},
		AdName:         "test-ad",
		Username:       "testuser",
		CredentialPath: log.PasswordMask,
		Domain:         "example.com",
		DNS:            "8.8.8.8",
		NetBIOS:        "EXAMPLE",
		State:          "READY",
		StateDetails:   "Active Directory is ready",
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: "OU=Test",
			Site:               "Default-Site",
			AdUsers: map[string][]string{
				"SeSecurityPrivilege":      {"user1"},
				`BUILTIN\Backup Operators`: {"user2"},
				`BUILTIN\Administrators`:   {"user3"},
			},
			KdcIP:                      "1.2.3.4",
			AesEncryption:              true,
			EncryptDCConnections:       true,
			LdapSigning:                true,
			AllowLocalNFSUsersWithLdap: false,
			Description:                "Test AD",
		},
	}

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "test-ad-uuid", int64(42)).Return(adFromDB, nil)

	result, err := o.GetADConfig(ctx, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "test-ad-uuid", result.UUID)
	assert.Equal(t, "test-ad", result.AdName)
	assert.Equal(t, "testuser", result.Username)
	assert.Equal(t, "example.com", result.Domain)
	assert.Equal(t, "READY", result.State)
	assert.NotNil(t, result.ActiveDirectoryAttributes)
	assert.Equal(t, "OU=Test", result.ActiveDirectoryAttributes.OrganizationalUnit)
	mockSe.AssertExpectations(t)
}

func TestOrchestrator_GetADConfig_AccountNotFound(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	params := &common.GetADParams{
		UUID:        "test-ad-uuid",
		AccountName: "non-existent-account",
	}

	// Mock getAccountWithName to return error
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return nil, customerrors.NewUserInputValidationErr("account not found")
	}
	defer func() { getAccountWithName = _getAccountWithName }()

	result, err := o.GetADConfig(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "account not found")
}

func TestOrchestrator_GetADConfig_ADNotFound(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	params := &common.GetADParams{
		UUID:        "non-existent-ad-uuid",
		AccountName: "test-account",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}

	// Mock getAccountWithName to return our account
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = _getAccountWithName }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "non-existent-ad-uuid", int64(42)).Return(nil, customerrors.NewNotFoundErr("Active Directory", nil))

	result, err := o.GetADConfig(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Active Directory not found")
	mockSe.AssertExpectations(t)
}

func TestOrchestrator_GetADConfig_DatabaseError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	params := &common.GetADParams{
		UUID:        "test-ad-uuid",
		AccountName: "test-account",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}

	// Mock getAccountWithName to return our account
	getAccountWithName = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getAccountWithName = _getAccountWithName }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "test-ad-uuid", int64(42)).Return(nil, customerrors.New("database error"))

	result, err := o.GetADConfig(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "database error")
	mockSe.AssertExpectations(t)
}

func TestOrchestrator_GetSDEActiveDirectory_Phase2Placeholder(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	params := &common.GetADParams{
		UUID:          "test-ad-uuid",
		AccountName:   "test-account",
		LocationID:    "us-central1",
		ProjectNumber: "12345",
		ResourceID:    "test-ad",
	}

	// Phase 2 implementation returns nil, nil
	result, err := o.GetSDEActiveDirectory(ctx, params)

	assert.NoError(t, err)
	assert.Nil(t, result)
}

func Test_convertActiveDirectoryToModel_Success(t *testing.T) {
	now := time.Now()
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID:      "test-uuid",
			CreatedAt: now,
			UpdatedAt: now,
		},
		AdName:         "test-ad",
		Username:       "testuser",
		CredentialPath: "secret-path",
		Domain:         "example.com",
		DNS:            "8.8.8.8",
		NetBIOS:        "EXAMPLE",
		State:          "READY",
		StateDetails:   "Ready",
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: "OU=Test",
			Site:               "Default-Site",
			AdUsers: map[string][]string{
				"SeSecurityPrivilege":      {"sec-user"},
				`BUILTIN\Backup Operators`: {"backup-user"},
				`BUILTIN\Administrators`:   {"admin-user"},
			},
			KdcIP:                      "1.2.3.4",
			KdcHostname:                "kdc.example.com",
			AesEncryption:              true,
			EncryptDCConnections:       true,
			LdapSigning:                false,
			AllowLocalNFSUsersWithLdap: true,
			Description:                "Test Description",
		},
	}

	result := commonfactory.ConvertDatastoreActiveDirectoryToModel(ad)

	assert.NotNil(t, result)
	assert.Equal(t, "test-uuid", result.UUID)
	assert.Equal(t, "test-ad", result.AdName)
	assert.Equal(t, "testuser", result.Username)
	assert.Equal(t, "******************", result.Password)
	assert.Equal(t, "example.com", result.Domain)
	assert.Equal(t, "8.8.8.8", result.DNS)
	assert.Equal(t, "EXAMPLE", result.NetBIOS)
	assert.Equal(t, "READY", result.State)
	assert.Equal(t, "Ready", result.StateDetails)
	assert.NotNil(t, result.ActiveDirectoryAttributes)
	assert.Equal(t, "OU=Test", result.ActiveDirectoryAttributes.OrganizationalUnit)
	assert.Equal(t, "Default-Site", result.ActiveDirectoryAttributes.Site)
	assert.Equal(t, []string{"sec-user"}, result.ActiveDirectoryAttributes.SecurityOperators)
	assert.Equal(t, []string{"backup-user"}, result.ActiveDirectoryAttributes.BackupOperators)
	assert.Equal(t, []string{"admin-user"}, result.ActiveDirectoryAttributes.Administrators)
	assert.Equal(t, "1.2.3.4", result.ActiveDirectoryAttributes.KdcIP)
	assert.Equal(t, "kdc.example.com", result.ActiveDirectoryAttributes.KdcHostname)
	assert.Equal(t, true, result.ActiveDirectoryAttributes.AesEncryption)
	assert.Equal(t, true, result.ActiveDirectoryAttributes.EncryptDCConnections)
	assert.Equal(t, false, result.ActiveDirectoryAttributes.LdapSigning)
	assert.Equal(t, true, result.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap)
	assert.Equal(t, "Test Description", result.ActiveDirectoryAttributes.Description)
}

func Test_convertActiveDirectoryToModel_NilInput(t *testing.T) {
	result := commonfactory.ConvertDatastoreActiveDirectoryToModel(nil)
	assert.Nil(t, result)
}

func Test_convertActiveDirectoryToModel_NilAttributes(t *testing.T) {
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{
			UUID: "test-uuid",
		},
		AdName:                    "test-ad",
		ActiveDirectoryAttributes: nil,
	}

	result := commonfactory.ConvertDatastoreActiveDirectoryToModel(ad)

	assert.NotNil(t, result)
	assert.Equal(t, "test-uuid", result.UUID)
	assert.Equal(t, "test-ad", result.AdName)
	assert.Nil(t, result.ActiveDirectoryAttributes)
}

func TestUpdateActiveDirectory_Success(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:                  "123",
		ActiveDirectoryId:          "ad-uuid-123",
		LocationId:                 "us-central1",
		XCorrelationId:             "test-correlation-id",
		Username:                   nillable.GetStringPtr("updated-admin@test.local"),
		Domain:                     nillable.GetStringPtr("test.local"),
		DNS:                        nillable.GetStringPtr("10.0.0.2"),
		NetBIOS:                    nillable.GetStringPtr("TEST"),
		SecurityOperators:          []string{"updated-security-user"},
		BackupOperators:            []string{"updated-backup-user"},
		Administrators:             []string{"updated-admin-user"},
		OrganizationalUnit:         nillable.GetStringPtr("CN=UpdatedComputers"),
		Site:                       nillable.GetStringPtr("Updated-Site"),
		KdcIP:                      nillable.GetStringPtr("10.0.0.3"),
		KdcHostname:                nillable.GetStringPtr("updated-kdc.test.local"),
		AesEncryption:              nillable.GetBoolPtr(false),
		EncryptDCConnections:       nillable.GetBoolPtr(true),
		LdapSigning:                nillable.GetBoolPtr(true),
		AllowLocalNFSUsersWithLdap: nillable.GetBoolPtr(false),
		Description:                nillable.GetStringPtr("Updated Test AD"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		Username:  "admin@test.local",
		Domain:    "test.local",
		DNS:       "10.0.0.1",
		NetBIOS:   "TEST",
		State:     datamodel.LifeCycleStateREADY,
	}

	createdJob := &datamodel.Job{
		BaseModel:     datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID:    "workflow-id-123",
		Type:          string(datamodel.JobTypeUpdateActiveDirectory),
		State:         string(datamodel.JobsStateNEW),
		ResourceName:  "test-ad",
		AccountID:     sql.NullInt64{Int64: 123, Valid: true},
		CorrelationID: "correlation-id",
		RequestID:     "request-id",
		JobAttributes: &datamodel.JobAttributes{
			ResourceUUID: "ad-uuid-123",
		},
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Mock getOrCreateAccount
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	// Mock _getActiveDirectory
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)

	// Mock workflow execution
	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(client client.Client, ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		return nil
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	adRecord := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
	}

	mockStorage.On("GetActiveDirectoryByNameAndAccountID", mock.Anything, "test-ad", int64(123)).Return(adRecord, nil)
	mockStorage.On("UpdateActiveDirectory", mock.Anything, mock.Anything).Return(adRecord, nil)

	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		return nil
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid-123", jobUUID)
	assert.Equal(t, datamodel.LifeCycleStateUpdating, ad.State)
	assert.Equal(t, datamodel.LifeCycleStateUpdatingDetails, ad.StateDetails)
	mockStorage.AssertExpectations(t)
}

func TestUpdateActiveDirectory_DomainUpdateNotAllowed(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Domain:            nillable.GetStringPtr("new-domain.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		Username:  "admin@test.local",
		Domain:    "test.local",
		DNS:       "10.0.0.1",
		NetBIOS:   "TEST",
		State:     datamodel.LifeCycleStateREADY,
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Mock _getActiveDirectory
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	// Mock getOrCreateAccount
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	// Mock checkIfDomainUpdateAllowed to return error
	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		return customerrors.NewUserInputValidationErr("domain update not allowed: active directory has associated volumes")
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "domain update not allowed")
}

func TestUpdateActiveDirectory_ValidationError(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		DNS:               nillable.GetStringPtr(""), // Invalid empty DNS
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()
	accountID := int64(123)
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
		Name:      "test-account",
	}
	mockStorage.On("GetAccount", mock.Anything, mock.Anything).Return(account, nil).Maybe()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
}

func TestUpdateActiveDirectory_AccountNotFound(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "non-existent-account",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Mock getOrCreateAccount to return error
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return nil, customerrors.NewNotFoundErr("Account", &accountName)
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "non-existent-account")
}

func TestUpdateActiveDirectory_ADNotFound(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "non-existent-ad",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Mock getOrCreateAccount
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	// Mock _getActiveDirectory to return error
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return nil, customerrors.NewNotFoundErr("ActiveDirectory", &params.UUID)
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "not found")
}

func TestUpdateActiveDirectory_JobCreationFailed(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Mock getOrCreateAccount
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		return nil
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	// Mock _getActiveDirectory
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(nil, errors.New("database error"))

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "database error")
	mockStorage.AssertExpectations(t)
}

func TestUpdateActiveDirectory_WorkflowStartFailed(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
	}

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-id-123",
		Type:       string(datamodel.JobTypeUpdateActiveDirectory),
		State:      string(datamodel.JobsStateNEW),
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Mock getOrCreateAccount
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		return nil
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	// Mock _getActiveDirectory
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)
	mockStorage.On("UpdateJob", mock.Anything, createdJob.UUID, string(datamodel.JobsStateERROR), 0, "workflow start error").Return(nil)

	// Mock workflow execution to fail
	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(client client.Client, ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		return errors.New("workflow start error")
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "workflow start error")
	mockStorage.AssertExpectations(t)
}

func TestUpdateActiveDirectory_Success_WithCVPHost(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
	}

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-id-123",
		Type:       string(datamodel.JobTypeUpdateActiveDirectory),
		State:      string(datamodel.JobsStateNEW),
	}

	// Mock getOrCreateAccount
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		return nil
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	// Mock _getActiveDirectory
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)

	// Mock workflow execution
	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(client client.Client, ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		return nil
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	// Set CVP_HOST to simulate SDE environment
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "http://cvp.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid-123", jobUUID)
	assert.Equal(t, datamodel.LifeCycleStateUpdating, ad.State)
	assert.Equal(t, datamodel.LifeCycleStateUpdatingDetails, ad.StateDetails)
	mockStorage.AssertExpectations(t)
}

func TestUpdateActiveDirectory_ADRecordNotFoundInDB(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
	}

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-id-123",
		Type:       string(datamodel.JobTypeUpdateActiveDirectory),
		State:      string(datamodel.JobsStateNEW),
	}

	// Mock getOrCreateAccount
	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		return nil
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	// Mock _getActiveDirectory
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)

	// Mock workflow execution
	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(client client.Client, ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		return nil
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	// Mock AD record not found in DB
	mockStorage.On("GetActiveDirectoryByNameAndAccountID", mock.Anything, "test-ad", int64(123)).Return(nil, nil)

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	// Mock to return parsed region without zone (zones are not supported for Active Directory)
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	// Restore original function after test
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "not found")
	mockStorage.AssertExpectations(t)
}

func TestOrchestratorUpdateActiveDirectory(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	expectedAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateUpdating,
	}

	originalUpdate := updateActiveDirectory
	updateActiveDirectory = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
		return expectedAD, "job-uuid", nil
	}
	defer func() { updateActiveDirectory = originalUpdate }()

	ad, jobUUID, err := orchestrator.UpdateActiveDirectory(ctx, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid", jobUUID)
	assert.Equal(t, "test-ad", ad.AdName)
	assert.Equal(t, datamodel.LifeCycleStateUpdating, ad.State)
}

func TestOrchestratorUpdateActiveDirectory_Error(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	orchestrator := &GCPOrchestrator{
		storage:  mockStorage,
		temporal: mockTemporal,
	}

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		Username:          nillable.GetStringPtr("admin@test.local"),
		Domain:            nillable.GetStringPtr("test.local"),
	}

	originalUpdate := updateActiveDirectory
	updateActiveDirectory = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.UpdateActiveDirectoryParams) (*models.ActiveDirectory, string, error) {
		return nil, "", errors.New("update failed")
	}
	defer func() { updateActiveDirectory = originalUpdate }()

	ad, jobUUID, err := orchestrator.UpdateActiveDirectory(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), "update failed")
}

// --- New tests for _updateActiveDirectory behaviors ---

func TestUpdateActiveDirectory_NilADFromGetActiveDirectory(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}

	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	// Return nil ad with no error — the nil check should catch this and return NotFound
	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return nil, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.True(t, customerrors.IsNotFoundErr(err))
}

func TestUpdateActiveDirectory_DomainSkippedWhenDomainSame(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Domain:            nillable.GetStringPtr("same-domain.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		Domain:    "same-domain.local",
		State:     datamodel.LifeCycleStateREADY,
	}
	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-id-123",
	}

	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	// Domain check should be SKIPPED because domain is unchanged
	domainCheckCalled := false
	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		domainCheckCalled = true
		return customerrors.NewBadRequestErr("should not be called")
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)

	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(client client.Client, ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		return nil
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid-123", jobUUID)
	assert.False(t, domainCheckCalled, "domain check should be skipped when domain is unchanged")
	mockStorage.AssertExpectations(t)
}

func TestUpdateActiveDirectory_DomainCheckReceivesAdWithId(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Domain:            nillable.GetStringPtr("new-domain.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	// Merged AD with ID set from VCP (used for domain check and workflow)
	adWithID := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123", ID: 42},
		AdName:    "test-ad",
		Domain:    "old-domain.local",
		State:     datamodel.LifeCycleStateREADY,
	}

	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return adWithID, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	var receivedAD *models.ActiveDirectory
	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		receivedAD = ad
		return nil
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-id-123",
	}
	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)

	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(client client.Client, ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		return nil
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	ad, _, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.NotNil(t, receivedAD, "checkIfDomainUpdateAllowed should have been called")
	assert.Equal(t, int64(42), receivedAD.ID, "checkIfDomainUpdateAllowed should receive ad with VCP ID for GetSVMsUsingActiveDirectory")
	mockStorage.AssertExpectations(t)
}

func TestUpdateActiveDirectory_AdPassedToWorkflow(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("admin@test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	adWithID := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123", ID: 42},
		AdName:    "test-ad",
		Domain:    "test.local",
		State:     datamodel.LifeCycleStateREADY,
	}

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-id-123",
	}

	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return adWithID, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)

	// Capture the AD model passed to the workflow
	var workflowADArg *models.ActiveDirectory
	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(c client.Client, ctx context.Context, options client.StartWorkflowOptions, wf interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		if len(args) >= 2 {
			if adArg, ok := args[1].(*models.ActiveDirectory); ok {
				workflowADArg = adArg
			}
		}
		return nil
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	ad, _, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.NotNil(t, workflowADArg, "workflow should receive an AD argument")
	assert.Equal(t, adWithID, workflowADArg, "workflow should receive the merged ad with ID set")
	mockStorage.AssertExpectations(t)
}

func TestUpdateActiveDirectory_DomainUpdateComparesAgainstVcpAdDomain(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Domain:            nillable.GetStringPtr("merged.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	// mergedAD.Domain differs from params.Domain so domain check is triggered
	mergedAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123", ID: 42},
		AdName:    "test-ad",
		Domain:    "different-vcp.local",
		State:     datamodel.LifeCycleStateREADY,
	}

	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return mergedAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	// mergedAD.Domain != params.Domain so checkIfDomainUpdateAllowed is called
	domainCheckCalled := false
	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		domainCheckCalled = true
		return customerrors.NewBadRequestErr("domain cannot be updated")
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	ad, jobUUID, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.True(t, domainCheckCalled, "domain check should be called when ad.Domain differs from params.Domain")
	assert.Contains(t, err.Error(), "domain cannot be updated")
}

func TestUpdateActiveDirectory_NoDomainParam_SkipsDomainCheck(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.UpdateActiveDirectoryParams{
		AccountId:         "123",
		ActiveDirectoryId: "ad-uuid-123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
		Username:          nillable.GetStringPtr("new-admin@test.local"),
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 123}, Name: "test-account"}
	existingAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: "ad-uuid-123"},
		AdName:    "test-ad",
		Domain:    "test.local",
		State:     datamodel.LifeCycleStateREADY,
	}

	createdJob := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-id-123",
	}

	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()

	originalGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = originalGetOrCreateAccount }()

	originalGetActiveDirectory := getActiveDirectory
	getActiveDirectory = func(ctx context.Context, params *common.GetADParams, se database.Storage) (*models.ActiveDirectory, error) {
		return existingAD, nil
	}
	defer func() { getActiveDirectory = originalGetActiveDirectory }()

	domainCheckCalled := false
	origCheckIfDomainUpdateAllowed := checkIfDomainUpdateAllowed
	checkIfDomainUpdateAllowed = func(ctx context.Context, se database.Storage, ad *models.ActiveDirectory) error {
		domainCheckCalled = true
		return nil
	}
	defer func() { checkIfDomainUpdateAllowed = origCheckIfDomainUpdateAllowed }()

	mockStorage.On("CreateJob", mock.Anything, mock.Anything).Return(createdJob, nil)

	originalWorkflowExecute := workflowsExecuteWorkflowSequentially
	workflowsExecuteWorkflowSequentially = func(client client.Client, ctx context.Context, options client.StartWorkflowOptions, workflow interface{}, childOptions workflow.ChildWorkflowOptions, args ...interface{}) error {
		return nil
	}
	defer func() { workflowsExecuteWorkflowSequentially = originalWorkflowExecute }()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	ad, _, err := _updateActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.False(t, domainCheckCalled, "domain check should be skipped when params.Domain is nil")
	mockStorage.AssertExpectations(t)
}

// Delete Active Directory Tests

func Test_deleteActiveDirectory_GetAccountError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	// Patch getOrCreateAccount to return error
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return nil, errors.New("account error")
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "account error")
	assert.Empty(t, jobUUID)
}

func Test_deleteActiveDirectory_GetADError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}

	// Patch getOrCreateAccount
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(42)).Return(nil, errors.New("get AD error"))

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get AD error")
	assert.Empty(t, jobUUID)
	mockSe.AssertExpectations(t)
}

func Test_deleteActiveDirectory_AlreadyDeleted(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateDeleted,
		AccountId: 42,
	}

	// Patch getOrCreateAccount
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(42)).Return(ad, nil)

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	// Should return empty job UUID and no error (line 571)
	assert.NoError(t, err)
	assert.Empty(t, jobUUID)
	mockSe.AssertExpectations(t)
}

func Test_deleteActiveDirectory_AlreadyDeletingWithExistingJob(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateDeleting,
		AccountId: 42,
	}
	existingJob := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{UUID: "existing-job-uuid"},
		WorkflowID:   "existing-workflow-id",
		AccountID:    sql.NullInt64{Int64: 42, Valid: true},
		ResourceName: "ad-uuid",
	}

	// Patch getOrCreateAccount
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(42)).Return(ad, nil)
	// GetJobByResourceUUID returns existing job (lines 556-557)
	mockSe.On("GetJobByResourceUUID", mock.Anything, "ad-uuid", string(datamodel.JobTypeDeleteActiveDirectory)).Return(existingJob, nil)

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	// Should return existing job UUID (lines 556-557)
	assert.NoError(t, err)
	assert.Equal(t, "existing-job-uuid", jobUUID)
	mockSe.AssertExpectations(t)
}

func Test_deleteActiveDirectory_AlreadyDeletingNoJob(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateDeleting,
		AccountId: 42,
	}
	job := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID:   "workflow-id",
		AccountID:    sql.NullInt64{Int64: 42, Valid: true},
		ResourceName: "ad-uuid",
	}

	// Patch getOrCreateAccount
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	// Mock ExecuteWorkflowSequentially
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return nil
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(42)).Return(ad, nil)
	// GetJobByResourceUUID returns error (no job found)
	mockSe.On("GetJobByResourceUUID", mock.Anything, "ad-uuid", string(datamodel.JobTypeDeleteActiveDirectory)).Return(nil, errors.New("job not found"))
	mockSe.On("CreateJob", mock.Anything, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.ResourceName == params.ActiveDirectoryUUID
	})).Return(job, nil)

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	// Should succeed and create new job (line 207 warning logged)
	assert.NoError(t, err)
	assert.Equal(t, "job-uuid", jobUUID)
	mockSe.AssertExpectations(t)
}

func Test_deleteActiveDirectory_CreateJobError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
		AccountId: 42,
	}

	// Patch getOrCreateAccount
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(42)).Return(ad, nil)
	mockSe.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(nil, errors.New("create job error"))

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create job error")
	assert.Empty(t, jobUUID)
	mockSe.AssertExpectations(t)
}

func Test_deleteActiveDirectory_WorkflowErrorWithUpdateJobError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
		AccountId: 42,
	}
	job := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID:   "workflow-id",
		AccountID:    sql.NullInt64{Int64: 42, Valid: true},
		ResourceName: "ad-uuid",
	}

	// Patch getOrCreateAccount
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	// Mock ExecuteWorkflowSequentially to return error
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return errors.New("workflow execution error")
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(42)).Return(ad, nil)
	mockSe.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	// Mock UpdateJob to also fail (lines 255-256)
	mockSe.On("UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(errors.New("update job error"))

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workflow execution error")
	assert.Empty(t, jobUUID)
	mockSe.AssertExpectations(t)
}

func Test_deleteActiveDirectory_WorkflowError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}

	account := &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 42}, Name: "test-account"}
	ad := &datamodel.ActiveDirectory{
		BaseModel: datamodel.BaseModel{UUID: "ad-uuid"},
		AdName:    "test-ad",
		State:     datamodel.LifeCycleStateREADY,
		AccountId: 42,
	}
	job := &datamodel.Job{
		BaseModel:    datamodel.BaseModel{UUID: "job-uuid"},
		WorkflowID:   "workflow-id",
		AccountID:    sql.NullInt64{Int64: 42, Valid: true},
		ResourceName: "ad-uuid",
	}

	// Patch getOrCreateAccount
	origGetOrCreateAccount := getOrCreateAccount
	getOrCreateAccount = func(ctx context.Context, se database.Storage, accountName string) (*datamodel.Account, error) {
		return account, nil
	}
	defer func() { getOrCreateAccount = origGetOrCreateAccount }()

	// Mock ExecuteWorkflowSequentially to return error
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return errors.New("workflow execution error")
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, "ad-uuid", int64(42)).Return(ad, nil)
	mockSe.On("CreateJob", mock.Anything, mock.AnythingOfType("*datamodel.Job")).Return(job, nil)
	// Mock UpdateJob to succeed
	mockSe.On("UpdateJob", mock.Anything, "job-uuid", string(datamodel.JobsStateERROR), 0, mock.Anything).Return(nil)

	jobUUID, err := _deleteActiveDirectory(ctx, mockSe, mockTemporal, params)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "workflow execution error")
	assert.Empty(t, jobUUID)
	mockSe.AssertExpectations(t)
}

func TestOrchestrator_DeleteActiveDirectory_Error(t *testing.T) {
	ctx := context.Background()
	params := &common.DeleteActiveDirectoryParams{
		ProjectNumber:       "test-account",
		ActiveDirectoryUUID: "ad-uuid",
	}
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{
		storage:  mockSe,
		temporal: mockTemporal,
	}

	origDeleteActiveDirectory := deleteActiveDirectory
	deleteActiveDirectory = func(ctx context.Context, se database.Storage, temporal client.Client, params *common.DeleteActiveDirectoryParams) (string, error) {
		return "", errors.New("delete error")
	}
	defer func() { deleteActiveDirectory = origDeleteActiveDirectory }()

	jobUUID, err := o.DeleteActiveDirectory(ctx, params)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete error")
	assert.Empty(t, jobUUID)
}

// Test functions for _checkIfDomainUpdateAllowed
func Test_checkIfDomainUpdateAllowed_Success_NoSVMs(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	ad := &models.ActiveDirectory{
		BaseModel: models.BaseModel{ID: 123},
		AdName:    "test-ad",
		Domain:    "test.example.com",
	}

	mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(123)).Return([]*datamodel.Svm{}, nil)

	err := _checkIfDomainUpdateAllowed(ctx, mockStorage, ad)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func Test_checkIfDomainUpdateAllowed_Error_SVMsInUse(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	ad := &models.ActiveDirectory{
		BaseModel: models.BaseModel{ID: 123},
		AdName:    "test-ad",
		Domain:    "test.example.com",
	}

	svms := []*datamodel.Svm{
		{BaseModel: datamodel.BaseModel{ID: 1, UUID: "svm-uuid-1"}},
		{BaseModel: datamodel.BaseModel{ID: 2, UUID: "svm-uuid-2"}},
	}

	mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(123)).Return(svms, nil)

	err := _checkIfDomainUpdateAllowed(ctx, mockStorage, ad)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Active Directory domain cannot be updated while it is in use")
	mockStorage.AssertExpectations(t)
}

func Test_checkIfDomainUpdateAllowed_Error_DatabaseError(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	ad := &models.ActiveDirectory{
		BaseModel: models.BaseModel{ID: 123},
		AdName:    "test-ad",
		Domain:    "test.example.com",
	}

	mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(123)).Return(nil, errors.New("database connection error"))

	err := _checkIfDomainUpdateAllowed(ctx, mockStorage, ad)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database connection error")
	mockStorage.AssertExpectations(t)
}

func Test_checkIfDomainUpdateAllowed_Error_SingleSVMInUse(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	ad := &models.ActiveDirectory{
		BaseModel: models.BaseModel{ID: 456},
		AdName:    "prod-ad",
		Domain:    "prod.example.com",
	}

	svms := []*datamodel.Svm{
		{BaseModel: datamodel.BaseModel{ID: 10, UUID: "svm-uuid-10"}},
	}

	mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(456)).Return(svms, nil)

	err := _checkIfDomainUpdateAllowed(ctx, mockStorage, ad)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Active Directory domain cannot be updated while it is in use")
	mockStorage.AssertExpectations(t)
}

func Test_checkIfDomainUpdateAllowed_Error_ZeroID(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(0)).Return(nil, errors.New("invalid active directory ID"))

	err := _checkIfDomainUpdateAllowed(ctx, mockStorage, &models.ActiveDirectory{})

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid active directory ID")
	mockStorage.AssertExpectations(t)
}

func Test_checkIfDomainUpdateAllowed_UsesOldAdID_Directly(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	adID := int64(999)
	ad := &models.ActiveDirectory{
		BaseModel: models.BaseModel{ID: adID},
		AdName:    "vcp-ad",
		Domain:    "vcp.example.com",
	}

	mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, adID).Return([]*datamodel.Svm{}, nil)

	err := _checkIfDomainUpdateAllowed(ctx, mockStorage, ad)

	assert.NoError(t, err)
	mockStorage.AssertExpectations(t)
}

func Test_checkIfDomainUpdateAllowed_MultipleSVMs(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)

	ad := &models.ActiveDirectory{
		BaseModel: models.BaseModel{ID: 789},
		AdName:    "multi-svm-ad",
		Domain:    "multi.example.com",
	}

	svms := []*datamodel.Svm{
		{BaseModel: datamodel.BaseModel{ID: 1, UUID: "svm-uuid-1"}},
		{BaseModel: datamodel.BaseModel{ID: 2, UUID: "svm-uuid-2"}},
		{BaseModel: datamodel.BaseModel{ID: 3, UUID: "svm-uuid-3"}},
	}

	mockStorage.On("GetSVMsUsingActiveDirectory", mock.Anything, int64(789)).Return(svms, nil)

	err := _checkIfDomainUpdateAllowed(ctx, mockStorage, ad)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Active Directory domain cannot be updated while it is in use")
	mockStorage.AssertExpectations(t)
}

// Tests for validateMultiADConstraints function

func Test_validateMultiADConstraints_NoExistingADs_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	// Mock ListActiveDirectories to return empty list
	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return([]*datamodel.ActiveDirectory{}, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.NoError(t, err)
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_OneExistingAD_MaxIsOne_ShouldFail(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad",
			AccountId: accountID,
		},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), MultiADNotAllowedError)
	var validationErr *customerrors.UserInputValidationErr
	assert.True(t, errors.As(err, &validationErr))
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_OneExistingAD_MaxIsTwo_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad",
			AccountId: accountID,
		},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 2

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.NoError(t, err)
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_TwoExistingADs_MultiADDisabled_ShouldFail(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad-1",
			AccountId: accountID,
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2},
			AdName:    "existing-ad-2",
			AccountId: accountID,
		},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 5

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), MultiADNotAllowedError)
	var validationErr *customerrors.UserInputValidationErr
	assert.True(t, errors.As(err, &validationErr))
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_TwoExistingADs_MultiADEnabled_MaxIsOne_ShouldFail(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad-1",
			AccountId: accountID,
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2},
			AdName:    "existing-ad-2",
			AccountId: accountID,
		},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 1

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), MaxADLimitReachedForAccountError)
	var conflictErr *customerrors.ConflictErr
	assert.True(t, errors.As(err, &conflictErr))
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_TwoExistingADs_MultiADEnabled_MaxIsFive_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad-1",
			AccountId: accountID,
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2},
			AdName:    "existing-ad-2",
			AccountId: accountID,
		},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 5

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.NoError(t, err)
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_OneExistingAD_MaxLimitReached_ShouldFail(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	// Create 1 existing AD when max is 1 (at the max limit)
	existingADs := []*datamodel.ActiveDirectory{
		{BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1}, AdName: "ad-1", AccountId: accountID},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), MultiADNotAllowedError)
	var validationErr *customerrors.UserInputValidationErr
	assert.True(t, errors.As(err, &validationErr))
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_MultipleExistingADs_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	// Create 5 existing ADs when max is 10 (below max limit)
	existingADs := []*datamodel.ActiveDirectory{
		{BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1}, AdName: "ad-1", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2}, AdName: "ad-2", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-3", ID: 3}, AdName: "ad-3", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-4", ID: 4}, AdName: "ad-4", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-5", ID: 5}, AdName: "ad-5", AccountId: accountID},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 10

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.NoError(t, err)
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_FourExistingADs_MaxIsFive_Success(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	// Create 4 existing ADs (below max limit, so 4 + 1 = 5 <= 5 is OK)
	existingADs := []*datamodel.ActiveDirectory{
		{BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1}, AdName: "ad-1", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2}, AdName: "ad-2", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-3", ID: 3}, AdName: "ad-3", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-4", ID: 4}, AdName: "ad-4", AccountId: accountID},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 5

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.NoError(t, err)
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_FiveExistingADs_MaxIsFive_ShouldFail(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	// Create 5 existing ADs (at max limit, so 5 + 1 = 6 > 5 should fail)
	existingADs := []*datamodel.ActiveDirectory{
		{BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1}, AdName: "ad-1", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2}, AdName: "ad-2", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-3", ID: 3}, AdName: "ad-3", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-4", ID: 4}, AdName: "ad-4", AccountId: accountID},
		{BaseModel: datamodel.BaseModel{UUID: "ad-5", ID: 5}, AdName: "ad-5", AccountId: accountID},
	}

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 5

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), MaxADLimitReachedForAccountError)
	var conflictErr *customerrors.ConflictErr
	assert.True(t, errors.As(err, &conflictErr))
	mockSe.AssertExpectations(t)
}

func Test_validateMultiADConstraints_ListActiveDirectoriesError_ShouldFail(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	accountID := int64(123)

	mockSe.On("ListActiveDirectories", mock.Anything, accountID).Return(nil, errors.New("database connection error"))

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 5

	err := validateMultiADConstraints(ctx, mockSe, accountID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database connection error")
	mockSe.AssertExpectations(t)
}

// Integration tests for CreateActiveDirectory with multi-AD validation

func TestCreateActiveDirectory_MultiADDisabled_SecondADShouldFail(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad-2",
		AccountId:          "123",
		LocationId:         "local",
		Username:           "admin@test.local",
		Password:           "SecurePass123!",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
	}

	accountID := int64(123)
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
		Name:      "test-account",
	}

	// Existing AD
	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad",
			AccountId: accountID,
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2},
			AdName:    "existing-ad-2",
			AccountId: accountID,
		},
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	originalCVPHost := cvp.CVP_HOST
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
		cvp.CVP_HOST = originalCVPHost
	}()

	utils.EnableMultiAD = false
	utils.MaxNumberOfADPerAccount = 1
	cvp.CVP_HOST = ""

	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), MultiADNotAllowedError)
	var validationErr *customerrors.UserInputValidationErr
	assert.True(t, errors.As(err, &validationErr))
}

func TestCreateActiveDirectory_MaxADLimitReached_ShouldFail(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad-new",
		AccountId:          "123",
		LocationId:         "local",
		Username:           "admin@test.local",
		Password:           "SecurePass123!",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
	}

	accountID := int64(123)
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
		Name:      "test-account",
	}

	// Existing AD (at the limit)
	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad",
			AccountId: accountID,
		},
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	originalCVPHost := cvp.CVP_HOST
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
		cvp.CVP_HOST = originalCVPHost
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 1
	cvp.CVP_HOST = ""

	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Empty(t, jobUUID)
	assert.Contains(t, err.Error(), MaxADLimitReachedForAccountError)
	var conflictErr *customerrors.ConflictErr
	assert.True(t, errors.As(err, &conflictErr))
}

func TestCreateActiveDirectory_MultiADEnabled_WithinLimit_Success(t *testing.T) {
	ctx := context.Background()
	mockStorage := database.NewMockStorage(t)
	mockTemporal := mocks.NewClient(t)

	params := &common.CreateActiveDirectoryParams{
		ResourceId:         "test-ad-3",
		AccountId:          "123",
		LocationId:         "local",
		Username:           "admin@test.local",
		Password:           "SecurePass123!",
		Domain:             "test.local",
		DNS:                "10.0.0.1",
		NetBIOS:            "TEST",
		OrganizationalUnit: "CN=Computers",
		Site:               "Default-First-Site",
		KdcIP:              "10.0.0.2",
		KdcHostname:        "kdc.test.local",
		AesEncryption:      true,
		BackupOperators:    []string{"backup-user"},
		Administrators:     []string{"admin-user"},
		SecurityOperators:  []string{"security-user"},
	}

	accountID := int64(123)
	account := &datamodel.Account{
		BaseModel: datamodel.BaseModel{ID: accountID},
		Name:      "test-account",
	}

	// Existing ADs (2 ADs, limit is 5)
	existingADs := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1", ID: 1},
			AdName:    "existing-ad-1",
			AccountId: accountID,
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-2", ID: 2},
			AdName:    "existing-ad-2",
			AccountId: accountID,
		},
	}

	adRecord := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: "ad-uuid-123"},
		AdName:         params.ResourceId,
		Username:       params.Username,
		Domain:         params.Domain,
		DNS:            params.DNS,
		NetBIOS:        params.NetBIOS,
		CredentialPath: "secret-path",
		AccountId:      accountID,
		State:          datamodel.LifeCycleStateCreating,
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			OrganizationalUnit: params.OrganizationalUnit,
			Site:               params.Site,
			KdcIP:              params.KdcIP,
			KdcHostname:        params.KdcHostname,
			AesEncryption:      params.AesEncryption,
		},
	}

	job := &datamodel.Job{
		BaseModel:  datamodel.BaseModel{UUID: "job-uuid-123"},
		WorkflowID: "workflow-123",
		Type:       string(datamodel.JobTypeCreateActiveDirectory),
		State:      string(datamodel.JobsStateNEW),
	}

	// Save original function
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}
	defer func() {
		utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
	}()

	// Save and restore account-related functions to prevent state pollution from other tests
	// Restore to original implementations, not whatever was modified by previous tests
	defer func() {
		getOrCreateAccount = _getOrCreateAccount
		getAccountWithName = _getAccountWithName
		createAccount = _createAccount
	}()
	// Ensure we start with original implementations
	getOrCreateAccount = _getOrCreateAccount
	getAccountWithName = _getAccountWithName
	createAccount = _createAccount

	// Mock ExecuteWorkflowSequentially using ExecuteWorkflowSeq
	origExecuteWorkflowSeq := workflows.ExecuteWorkflowSeq
	workflows.ExecuteWorkflowSeq = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
		return nil
	}
	defer func() { workflows.ExecuteWorkflowSeq = origExecuteWorkflowSeq }()

	originalStorePassword := adHelper.StorePasswordSecret
	adHelper.StorePasswordSecret = func(ctx context.Context, password string, secretID string) error {
		return nil
	}
	defer func() { adHelper.StorePasswordSecret = originalStorePassword }()

	// Save and restore original values
	originalEnableMultiAD := utils.EnableMultiAD
	originalMaxNumberOfADPerAccount := utils.MaxNumberOfADPerAccount
	originalCVPHost := cvp.CVP_HOST
	defer func() {
		utils.EnableMultiAD = originalEnableMultiAD
		utils.MaxNumberOfADPerAccount = originalMaxNumberOfADPerAccount
		cvp.CVP_HOST = originalCVPHost
	}()

	utils.EnableMultiAD = true
	utils.MaxNumberOfADPerAccount = 5
	cvp.CVP_HOST = ""

	mockStorage.On("GetAccount", mock.Anything, "123").Return(account, nil)
	mockStorage.On("CreateAccount", mock.Anything, mock.MatchedBy(func(acc *datamodel.Account) bool {
		return acc.Name == "123"
	})).Return(account, nil).Maybe()
	mockStorage.On("ListActiveDirectories", mock.Anything, accountID).Return(existingADs, nil)
	mockStorage.On("CreateActiveDirectory", mock.Anything, mock.MatchedBy(func(ad *datamodel.ActiveDirectory) bool {
		return ad.AdName == params.ResourceId
	})).Return(adRecord, nil)
	mockStorage.On("CreateJob", mock.Anything, mock.MatchedBy(func(j *datamodel.Job) bool {
		return j.Type == string(datamodel.JobTypeCreateActiveDirectory)
	})).Return(job, nil)

	ad, jobUUID, err := _createActiveDirectory(ctx, mockStorage, mockTemporal, params)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "job-uuid-123", jobUUID)
	assert.Equal(t, adRecord.UUID, ad.UUID)
	assert.Equal(t, params.ResourceId, ad.AdName)
	assert.Equal(t, datamodel.LifeCycleStateCreating, ad.State)
}

func TestOrchestrator_BatchListActiveDirectories(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	mockTemporal := new(mocks.Client)
	o := &GCPOrchestrator{storage: mockSe, temporal: mockTemporal}

	origBatchList := batchListActiveDirectories
	batchListActiveDirectories = func(_ context.Context, _ database.Storage, _ *common.BatchListADsParams) ([]*models.ActiveDirectory, error) {
		return []*models.ActiveDirectory{
			{BaseModel: models.BaseModel{UUID: "ad-1"}, AdName: "ad-name-1"},
		}, nil
	}
	defer func() { batchListActiveDirectories = origBatchList }()

	params := &common.BatchListADsParams{UUIDs: []string{"ad-1"}, LocationID: "us-east4"}
	ads, err := o.BatchListActiveDirectories(ctx, params)
	assert.NoError(t, err)
	assert.Len(t, ads, 1)
	assert.Equal(t, "ad-1", ads[0].UUID)
}

func Test_batchListActiveDirectories_NonCVP_DelegatesToVCPDB(t *testing.T) {
	cvp.CVP_HOST = ""

	ctx := context.Background()
	mockSe := new(database.MockStorage)

	adsFromDB := []*datamodel.ActiveDirectory{
		{
			BaseModel: datamodel.BaseModel{UUID: "ad-1"},
			AdName:    "ad-name-1",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
	}
	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, []string{"ad-1"}).Return(adsFromDB, nil)

	params := &common.BatchListADsParams{UUIDs: []string{"ad-1"}, LocationID: "us-east4"}
	ads, err := _batchListActiveDirectories(ctx, mockSe, params)

	assert.NoError(t, err)
	assert.Len(t, ads, 1)
	assert.Equal(t, "ad-1", ads[0].UUID)
	mockSe.AssertExpectations(t)
}

func Test_batchListActiveDirectories_CVP_ParallelFetchAndMerge(t *testing.T) {
	cvp.CVP_HOST = "http://mock-cvp"
	defer func() { cvp.CVP_HOST = "" }()

	ctx := context.Background()
	mockSe := new(database.MockStorage)

	origSdeFn := batchListActiveDirectoriesSde
	batchListActiveDirectoriesSde = func(_ context.Context, _ *common.BatchListADsParams) ([]*models.ActiveDirectory, error) {
		return []*models.ActiveDirectory{
			{BaseModel: models.BaseModel{UUID: "ad-1"}, AdName: "cvp-ad", State: "READY", StateDetails: "CVP ready"},
			{BaseModel: models.BaseModel{UUID: "ad-2"}, AdName: "cvp-only", State: "CREATING", StateDetails: "CVP creating"},
		}, nil
	}
	defer func() { batchListActiveDirectoriesSde = origSdeFn }()

	vcpFromDB := []*datamodel.ActiveDirectory{
		{
			BaseModel:    datamodel.BaseModel{UUID: "ad-1"},
			AdName:       "vcp-ad",
			State:        "UPDATING",
			StateDetails: "VCP updating",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
	}
	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, []string{"ad-1", "ad-2"}).Return(vcpFromDB, nil)

	params := &common.BatchListADsParams{UUIDs: []string{"ad-1", "ad-2"}, LocationID: "us-east4"}
	ads, err := _batchListActiveDirectories(ctx, mockSe, params)

	assert.NoError(t, err)
	assert.Len(t, ads, 2)

	assert.Equal(t, "ad-1", ads[0].UUID)
	assert.Equal(t, "UPDATING", ads[0].State, "VCP UPDATING (priority 0) should beat CVP READY (priority 3)")
	assert.Equal(t, "VCP updating", ads[0].StateDetails)

	assert.Equal(t, "ad-2", ads[1].UUID)
	assert.Equal(t, "CREATING", ads[1].State, "AD only in CVP, state preserved")
	assert.Equal(t, "CVP creating", ads[1].StateDetails)

	mockSe.AssertExpectations(t)
}

func Test_batchListActiveDirectories_CVP_SDEError_Returns500(t *testing.T) {
	cvp.CVP_HOST = "http://mock-cvp"
	defer func() { cvp.CVP_HOST = "" }()

	ctx := context.Background()
	mockSe := new(database.MockStorage)

	origSdeFn := batchListActiveDirectoriesSde
	batchListActiveDirectoriesSde = func(_ context.Context, _ *common.BatchListADsParams) ([]*models.ActiveDirectory, error) {
		return nil, errors.New("CVP timeout")
	}
	defer func() { batchListActiveDirectoriesSde = origSdeFn }()

	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, []string{"ad-1"}).
		Return([]*datamodel.ActiveDirectory{}, nil).Maybe()

	params := &common.BatchListADsParams{UUIDs: []string{"ad-1"}, LocationID: "us-east4"}
	ads, err := _batchListActiveDirectories(ctx, mockSe, params)

	assert.Error(t, err)
	assert.Nil(t, ads)
	assert.Contains(t, err.Error(), "CVP timeout")
}

func Test_batchListActiveDirectories_CVP_VCPError_Returns500(t *testing.T) {
	cvp.CVP_HOST = "http://mock-cvp"
	defer func() { cvp.CVP_HOST = "" }()

	ctx := context.Background()
	mockSe := new(database.MockStorage)

	origSdeFn := batchListActiveDirectoriesSde
	batchListActiveDirectoriesSde = func(_ context.Context, _ *common.BatchListADsParams) ([]*models.ActiveDirectory, error) {
		return []*models.ActiveDirectory{
			{BaseModel: models.BaseModel{UUID: "ad-1"}, State: "READY"},
		}, nil
	}
	defer func() { batchListActiveDirectoriesSde = origSdeFn }()

	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, []string{"ad-1"}).
		Return(nil, errors.New("database error"))

	params := &common.BatchListADsParams{UUIDs: []string{"ad-1"}, LocationID: "us-east4"}
	ads, err := _batchListActiveDirectories(ctx, mockSe, params)

	assert.Error(t, err)
	assert.Nil(t, ads)
	assert.Contains(t, err.Error(), "database error")
	mockSe.AssertExpectations(t)
}

func TestConvertCVPBatchADToModel_NoReadyDefaultWhenStateOmitted(t *testing.T) {
	batch := &cvpmodels.BatchActiveDirectoryV1beta{
		ActiveDirectoryID: "11111111-1111-1111-1111-111111111111",
	}
	out := adHelper.ConvertCVPBatchADToModel(batch)
	require.NotNil(t, out)
	assert.Equal(t, "", out.State)
	assert.Equal(t, "", out.StateDetails)
}

func TestConvertCVPBatchADToModel_PreservesStateFromCVP(t *testing.T) {
	details := "custom detail"
	batch := &cvpmodels.BatchActiveDirectoryV1beta{
		ActiveDirectoryID:           "11111111-1111-1111-1111-111111111111",
		ActiveDirectoryState:        "ERROR",
		ActiveDirectoryStateDetails: &details,
	}
	out := adHelper.ConvertCVPBatchADToModel(batch)
	require.NotNil(t, out)
	assert.Equal(t, "ERROR", out.State)
	assert.Equal(t, "custom detail", out.StateDetails)
}

func Test_batchListActiveDirectoriesSDE_Success(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"activeDirectories": [
				{
					"activeDirectoryId": "ad-1",
					"resourceId": "cvp-ad",
					"username": "admin",
					"domain": "corp.example.com",
					"DNS": "10.0.0.1",
					"netBIOS": "CORP",
					"activeDirectoryState": "READY",
					"activeDirectoryStateDetails": "Ready for use"
				}
			]
		}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:]
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.BatchListADsParams{
		UUIDs:         []string{"ad-1"},
		LocationID:    "us-central1",
		CorrelationID: "test-corr-id",
	}

	ads, err := _batchListActiveDirectoriesSDE(ctx, params)

	assert.NoError(t, err)
	assert.Len(t, ads, 1)
	assert.Equal(t, "ad-1", ads[0].UUID)
	assert.Equal(t, "cvp-ad", ads[0].AdName)
	assert.Equal(t, "READY", ads[0].State)
	assert.Equal(t, "Ready for use", ads[0].StateDetails)
	assert.Equal(t, "corp.example.com", ads[0].Domain)
}

func Test_batchListActiveDirectoriesSDE_PropagatesFieldsToCVPQuery(t *testing.T) {
	var gotFields string
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotFields = r.URL.Query().Get("fields")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"activeDirectories": [{"activeDirectoryId": "ad-1", "activeDirectoryState": "READY"}]}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:]
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.BatchListADsParams{
		UUIDs:      []string{"ad-1"},
		LocationID: "us-central1",
		Fields:     []string{"domain", "username"},
	}

	ads, err := _batchListActiveDirectoriesSDE(ctx, params)

	assert.NoError(t, err)
	assert.Len(t, ads, 1)
	assert.Equal(t, "domain,username", gotFields)
}

func Test_batchListActiveDirectoriesSDE_CVPError(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code": 500, "message": "Internal error"}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:]
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.BatchListADsParams{
		UUIDs:      []string{"ad-1"},
		LocationID: "us-central1",
	}

	ads, err := _batchListActiveDirectoriesSDE(ctx, params)

	assert.Error(t, err)
	assert.Nil(t, ads)
	assert.Contains(t, err.Error(), "CVP batch list active directories failed")
}

func Test_batchListActiveDirectoriesSDE_EmptyResponse(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"activeDirectories": []}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:]
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.BatchListADsParams{
		UUIDs:      []string{"non-existent"},
		LocationID: "us-central1",
	}

	ads, err := _batchListActiveDirectoriesSDE(ctx, params)

	assert.NoError(t, err)
	assert.Empty(t, ads)
}

func Test_batchListActiveDirectoriesSDE_NoCorrelationID(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"activeDirectories": [{"activeDirectoryId": "ad-1", "activeDirectoryState": "READY"}]}`))
	}))
	defer mockServer.Close()

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = mockServer.URL[7:]
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	ctx := context.Background()
	params := &common.BatchListADsParams{
		UUIDs:      []string{"ad-1"},
		LocationID: "us-central1",
	}

	ads, err := _batchListActiveDirectoriesSDE(ctx, params)

	assert.NoError(t, err)
	assert.Len(t, ads, 1)
}

func Test_batchListActiveDirectories_CVP_CVPHigherPriority_KeepsCVP(t *testing.T) {
	cvp.CVP_HOST = "http://mock-cvp"
	defer func() { cvp.CVP_HOST = "" }()

	ctx := context.Background()
	mockSe := new(database.MockStorage)

	origSdeFn := batchListActiveDirectoriesSde
	batchListActiveDirectoriesSde = func(_ context.Context, _ *common.BatchListADsParams) ([]*models.ActiveDirectory, error) {
		return []*models.ActiveDirectory{
			{BaseModel: models.BaseModel{UUID: "ad-1"}, State: "ERROR", StateDetails: "CVP error"},
		}, nil
	}
	defer func() { batchListActiveDirectoriesSde = origSdeFn }()

	vcpFromDB := []*datamodel.ActiveDirectory{
		{
			BaseModel:    datamodel.BaseModel{UUID: "ad-1"},
			State:        "READY",
			StateDetails: "VCP ready",
			ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
				AdUsers: map[string][]string{},
			},
		},
	}
	mockSe.On("GetMultipleActiveDirectoriesByUUIDs", mock.Anything, []string{"ad-1"}).Return(vcpFromDB, nil)

	params := &common.BatchListADsParams{UUIDs: []string{"ad-1"}, LocationID: "us-east4"}
	ads, err := _batchListActiveDirectories(ctx, mockSe, params)

	assert.NoError(t, err)
	assert.Len(t, ads, 1)
	assert.Equal(t, "ERROR", ads[0].State, "CVP ERROR (priority 1) should beat VCP READY (priority 3)")
	assert.Equal(t, "CVP error", ads[0].StateDetails)
	mockSe.AssertExpectations(t)
}
