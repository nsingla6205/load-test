package gcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	cvpModels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	adHelper "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/helper"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	customerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
)

// Test ConvertUpdateParamsToDescribeParams
func TestConvertUpdateParamsToDescribeParams(t *testing.T) {
	updateParams := &common.UpdateActiveDirectoryParams{
		ActiveDirectoryId: "ad-uuid-123",
		AccountId:         "123",
		LocationId:        "us-central1",
		XCorrelationId:    "test-correlation-id",
	}
	projectNumber := "test-project-123"

	result, err := adHelper.ConvertUpdateParamsToDescribeParams(updateParams, projectNumber)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, projectNumber, result.ProjectNumber)
	assert.Equal(t, "us-central1", result.LocationID)
	assert.Equal(t, "ad-uuid-123", result.UUID)
	assert.Equal(t, "test-correlation-id", result.CorrelationID)
}

func TestConvertUpdateParamsToDescribeParams_EmptyCorrelationID(t *testing.T) {
	updateParams := &common.UpdateActiveDirectoryParams{
		ActiveDirectoryId: "ad-uuid-123",
		AccountId:         "123",
		LocationId:        "us-central1",
		XCorrelationId:    "", // Empty
	}
	projectNumber := "test-project-123"

	result, err := adHelper.ConvertUpdateParamsToDescribeParams(updateParams, projectNumber)

	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "Correlation ID is empty")
}

// Test ConvertCVPActiveDirectoryV1BetaToModel
func TestConvertCVPActiveDirectoryV1BetaToModel(t *testing.T) {
	username := "testuser"
	password := "testpass"
	domain := "example.com"
	dns := "8.8.8.8"
	netbios := "EXAMPLE"
	resourceID := "test-ad"
	orgUnit := "OU=Test"
	site := "Test-Site"
	description := "Test AD"

	createdAt := strfmt.DateTime(time.Now())
	updatedAt := strfmt.DateTime(time.Now().Add(time.Hour))
	deletedAt := strfmt.DateTime(time.Now().Add(2 * time.Hour))

	cvpAD := &cvpModels.ActiveDirectoryV1beta{
		ActiveDirectoryID:           "ad-uuid-123",
		ResourceID:                  &resourceID,
		Username:                    &username,
		Password:                    &password,
		Domain:                      &domain,
		DNS:                         &dns,
		NetBIOS:                     &netbios,
		ActiveDirectoryState:        "IN_USE",
		ActiveDirectoryStateDetails: "Active Directory in use",
		CreatedAt:                   createdAt,
		UpdatedAt:                   updatedAt,
		DeletedAt:                   &deletedAt,
		OrganizationalUnit:          &orgUnit,
		Site:                        &site,
		KdcIP:                       "10.0.0.1",
		KdcHostname:                 "kdc.example.com",
		AesEncryption:               nillableBool(true),
		EncryptDCConnections:        nillableBool(true),
		LdapSigning:                 nillableBool(true),
		AllowLocalNFSUsersWithLdap:  nillableBool(false),
		Description:                 &description,
		BackupOperators:             []string{"backup1", "backup2"},
		SecurityOperators:           []string{"sec1"},
		Administrators:              []string{"admin1", "admin2", "admin3"},
	}

	result := adHelper.ConvertCVPActiveDirectoryV1BetaToModel(cvpAD)

	assert.NotNil(t, result)
	assert.Equal(t, "ad-uuid-123", result.UUID)
	assert.Equal(t, "test-ad", result.AdName)
	assert.Equal(t, "testuser", result.Username)
	assert.Equal(t, "testpass", result.Password)
	assert.Equal(t, "example.com", result.Domain)
	assert.Equal(t, "8.8.8.8", result.DNS)
	assert.Equal(t, "EXAMPLE", result.NetBIOS)
	assert.Equal(t, "IN_USE", result.State)
	assert.Equal(t, "Active Directory in use", result.StateDetails)
	assert.Equal(t, time.Time(createdAt), result.CreatedAt)
	assert.Equal(t, time.Time(updatedAt), result.UpdatedAt)
	assert.NotNil(t, result.DeletedAt)
	assert.Equal(t, time.Time(deletedAt), *result.DeletedAt)

	assert.NotNil(t, result.ActiveDirectoryAttributes)
	assert.Equal(t, "OU=Test", result.ActiveDirectoryAttributes.OrganizationalUnit)
	assert.Equal(t, "Test-Site", result.ActiveDirectoryAttributes.Site)
	assert.Equal(t, "10.0.0.1", result.ActiveDirectoryAttributes.KdcIP)
	assert.Equal(t, "kdc.example.com", result.ActiveDirectoryAttributes.KdcHostname)
	assert.True(t, result.ActiveDirectoryAttributes.AesEncryption)
	assert.True(t, result.ActiveDirectoryAttributes.EncryptDCConnections)
	assert.True(t, result.ActiveDirectoryAttributes.LdapSigning)
	assert.False(t, result.ActiveDirectoryAttributes.AllowLocalNFSUsersWithLdap)
	assert.Equal(t, "Test AD", result.ActiveDirectoryAttributes.Description)
	assert.Equal(t, []string{"backup1", "backup2"}, result.ActiveDirectoryAttributes.BackupOperators)
	assert.Equal(t, []string{"sec1"}, result.ActiveDirectoryAttributes.SecurityOperators)
	assert.Equal(t, []string{"admin1", "admin2", "admin3"}, result.ActiveDirectoryAttributes.Administrators)
}

func TestConvertCVPActiveDirectoryV1BetaToModel_Nil(t *testing.T) {
	result := adHelper.ConvertCVPActiveDirectoryV1BetaToModel(nil)
	assert.Nil(t, result)
}

func TestConvertCVPActiveDirectoryV1BetaToModel_MinimalFields(t *testing.T) {
	cvpAD := &cvpModels.ActiveDirectoryV1beta{
		ActiveDirectoryID:           "ad-uuid-123",
		ActiveDirectoryState:        "READY",
		ActiveDirectoryStateDetails: "",
	}

	result := adHelper.ConvertCVPActiveDirectoryV1BetaToModel(cvpAD)

	assert.NotNil(t, result)
	assert.Equal(t, "ad-uuid-123", result.UUID)
	assert.Equal(t, "READY", result.State)
	assert.Equal(t, "Active Directory is ready", result.StateDetails) // Default
	assert.NotNil(t, result.ActiveDirectoryAttributes)
	assert.Empty(t, result.ActiveDirectoryAttributes.BackupOperators)
	assert.Empty(t, result.ActiveDirectoryAttributes.SecurityOperators)
	assert.Empty(t, result.ActiveDirectoryAttributes.Administrators)
}

// Test CompareADStateHierarchy
func TestCompareADStateHierarchy_UpdatingWins(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateUpdating,
		StateDetails: datamodel.LifeCycleStateUpdatingDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateError,
		StateDetails: datamodel.LifeCycleStateDeletionErrorDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, datamodel.LifeCycleStateUpdating, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateUpdatingDetails, sdeAD.StateDetails, "StateDetails should come from SDE (winner)")
}

func TestCompareADStateHierarchy_ErrorWins(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateReadyDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateError,
		StateDetails: datamodel.LifeCycleStateDeletionErrorDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, datamodel.LifeCycleStateError, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateDeletionErrorDetails, sdeAD.StateDetails, "StateDetails should come from VCP (winner)")
}

func TestCompareADStateHierarchy_InUseWins(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateReadyDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateInUse,
		StateDetails: datamodel.LifeCycleStateInUseDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, datamodel.LifeCycleStateInUse, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateInUseDetails, sdeAD.StateDetails, "StateDetails should come from VCP (winner)")
}

func TestCompareADStateHierarchy_KeepsReady(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateReadyDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateAvailableDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, datamodel.LifeCycleStateREADY, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateReadyDetails, sdeAD.StateDetails, "StateDetails should come from SDE (equal priority, SDE wins)")
}

func TestCompareADStateHierarchy_BothUpdating(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateUpdating,
		StateDetails: datamodel.LifeCycleStateUpdatingDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateUpdating,
		StateDetails: datamodel.LifeCycleStateSyncDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	assert.Equal(t, datamodel.LifeCycleStateUpdating, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateUpdatingDetails, sdeAD.StateDetails, "StateDetails should come from SDE (equal priority, SDE wins)")
}

func TestCompareADStateHierarchy_NilSDE(t *testing.T) {
	vcpAD := &models.ActiveDirectory{
		State: "ERROR",
	}

	// Should not panic
	adHelper.CompareADStateHierarchy(nil, vcpAD)
}

func TestCompareADStateHierarchy_NilVCP(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State: "READY",
	}

	// Should not panic
	adHelper.CompareADStateHierarchy(sdeAD, nil)

	// State should remain unchanged
	assert.Equal(t, "READY", sdeAD.State)
}

func TestCompareADStateHierarchy_BothNil(t *testing.T) {
	// Should not panic
	adHelper.CompareADStateHierarchy(nil, nil)
}

func TestCompareADStateHierarchy_UnknownState(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        "UNKNOWN_STATE",
		StateDetails: "",
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateError,
		StateDetails: datamodel.LifeCycleStateCreationErrorDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	// VCP state should win because SDE state is not in hierarchy
	assert.Equal(t, datamodel.LifeCycleStateError, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateCreationErrorDetails, sdeAD.StateDetails, "StateDetails should come from VCP (winner)")
}

func TestCompareADStateHierarchy_SDEErrorVsVCPReady(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateError,
		StateDetails: datamodel.LifeCycleStateCreationErrorDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateReadyDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	// ERROR has higher priority than READY
	assert.Equal(t, datamodel.LifeCycleStateError, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateCreationErrorDetails, sdeAD.StateDetails,
		"StateDetails should preserve the SDE error message")
}

func TestCompareADStateHierarchy_VCPErrorVsSDEReady(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateREADY,
		StateDetails: datamodel.LifeCycleStateReadyDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateError,
		StateDetails: datamodel.LifeCycleStateUpdateErrorDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	// ERROR has higher priority than READY
	assert.Equal(t, datamodel.LifeCycleStateError, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateUpdateErrorDetails, sdeAD.StateDetails,
		"StateDetails should preserve the VCP error message")
}

func TestCompareADStateHierarchy_BothError(t *testing.T) {
	sdeAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateError,
		StateDetails: datamodel.LifeCycleStateCreationErrorDetails,
	}
	vcpAD := &models.ActiveDirectory{
		State:        datamodel.LifeCycleStateError,
		StateDetails: datamodel.LifeCycleStateDeletionErrorDetails,
	}

	adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

	// Equal priority, SDE wins
	assert.Equal(t, datamodel.LifeCycleStateError, sdeAD.State)
	assert.Equal(t, datamodel.LifeCycleStateCreationErrorDetails, sdeAD.StateDetails,
		"StateDetails should come from SDE (equal priority)")
}

func TestCompareADStateHierarchy_AllStates(t *testing.T) {
	tests := []struct {
		name             string
		sdeState         string
		sdeStateDetails  string
		vcpState         string
		vcpStateDetails  string
		wantState        string
		wantStateDetails string
	}{
		{
			"UPDATING vs ERROR", datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, datamodel.LifeCycleStateError, datamodel.LifeCycleStateCreationErrorDetails,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails,
		},
		{
			"UPDATING vs IN_USE", datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateSyncDetails, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateSyncDetails,
		},
		{
			"UPDATING vs READY", datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateUpdatingDetails,
		},
		{
			"ERROR vs IN_USE", datamodel.LifeCycleStateError, datamodel.LifeCycleStateCreationErrorDetails, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails,
			datamodel.LifeCycleStateError, datamodel.LifeCycleStateCreationErrorDetails,
		},
		{
			"ERROR vs READY", datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails,
			datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails,
		},
		{
			"IN_USE vs READY", datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails,
			datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails,
		},
		{
			"READY vs UPDATING", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails, datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateSyncDetails,
			datamodel.LifeCycleStateUpdating, datamodel.LifeCycleStateSyncDetails,
		},
		{
			"READY vs ERROR", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails, datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails,
			datamodel.LifeCycleStateError, datamodel.LifeCycleStateUpdateErrorDetails,
		},
		{
			"READY vs IN_USE", datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateReadyDetails, datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails,
			datamodel.LifeCycleStateInUse, datamodel.LifeCycleStateInUseDetails,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sdeAD := &models.ActiveDirectory{
				State:        tt.sdeState,
				StateDetails: tt.sdeStateDetails,
			}
			vcpAD := &models.ActiveDirectory{
				State:        tt.vcpState,
				StateDetails: tt.vcpStateDetails,
			}

			adHelper.CompareADStateHierarchy(sdeAD, vcpAD)

			assert.Equal(t, tt.wantState, sdeAD.State,
				"For %s vs %s, expected state %s but got %s",
				tt.sdeState, tt.vcpState, tt.wantState, sdeAD.State)
			assert.Equal(t, tt.wantStateDetails, sdeAD.StateDetails,
				"For %s vs %s, expected StateDetails '%s' but got '%s'",
				tt.sdeState, tt.vcpState, tt.wantStateDetails, sdeAD.StateDetails)
		})
	}
}

// Helper function for nillable bool
func nillableBool(b bool) *bool {
	return &b
}

func Test_getActiveDirectory_VCPOnlyMode(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440000"

	// Ensure VCP-only mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	adFromDB := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: adUUID},
		AdName:         "test-ad",
		Username:       "admin",
		CredentialPath: "path/to/secret",
		Domain:         "example.com",
		DNS:            "8.8.8.8",
		NetBIOS:        "TEST",
		State:          "READY",
		StateDetails:   "Ready",
		ActiveDirectoryAttributes: &datamodel.ActiveDirectoryAttributes{
			AdUsers: map[string][]string{
				`BUILTIN\Backup Operators`: {"backup1"},
			},
		},
	}

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(adFromDB, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, adUUID, ad.UUID)
	assert.Equal(t, "READY", ad.State)
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_VCPOnlyMode_NotFound(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440001"

	// Ensure VCP-only mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = originalCVPHost }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(nil, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.True(t, customerrors.IsNotFoundErr(err))
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_SDEMode_VCPNotFound(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440002"

	// Enable SDE mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Mock SDE fetch to return AD
	originalGetActiveDirectorySde := getActiveDirectorySde
	sdeAD := &models.ActiveDirectory{
		BaseModel:    models.BaseModel{UUID: adUUID},
		AdName:       "sde-ad",
		Username:     "sdeuser",
		Password:     "sdepass",
		Domain:       "sde.com",
		DNS:          "8.8.8.8",
		NetBIOS:      "SDE",
		State:        "READY",
		StateDetails: "SDE AD ready",
	}
	getActiveDirectorySde = func(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
		return sdeAD, nil
	}
	defer func() { getActiveDirectorySde = originalGetActiveDirectorySde }()

	// Mock VCP not found
	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(nil, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	// Should return SDE AD when VCP not found
	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "sde-ad", ad.AdName)
	assert.Equal(t, "READY", ad.State)
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_SDEMode_StateComparison(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440003"

	// Enable SDE mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Mock SDE fetch - returns READY state
	originalGetActiveDirectorySde := getActiveDirectorySde
	sdeAD := &models.ActiveDirectory{
		BaseModel:    models.BaseModel{UUID: adUUID},
		AdName:       "test-ad",
		State:        "READY",
		StateDetails: "SDE ready",
	}
	getActiveDirectorySde = func(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
		return sdeAD, nil
	}
	defer func() { getActiveDirectorySde = originalGetActiveDirectorySde }()

	// Mock VCP fetch - returns ERROR state (higher priority)
	vcpADFromDB := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: adUUID},
		AdName:         "test-ad",
		State:          "ERROR",
		StateDetails:   "VCP error",
		CredentialPath: "path/to/secret",
	}
	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(vcpADFromDB, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	// Should return SDE AD with VCP's higher priority ERROR state
	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "test-ad", ad.AdName)
	assert.Equal(t, "ERROR", ad.State, "Should use VCP ERROR state over SDE READY")
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_SDEMode_SetsIDFromVCP(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440007"

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	originalGetActiveDirectorySde := getActiveDirectorySde
	sdeAD := &models.ActiveDirectory{
		BaseModel:    models.BaseModel{UUID: adUUID},
		AdName:       "test-ad",
		State:        "READY",
		StateDetails: "SDE ready",
	}
	getActiveDirectorySde = func(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
		return sdeAD, nil
	}
	defer func() { getActiveDirectorySde = originalGetActiveDirectorySde }()

	vcpID := int64(99)
	vcpADFromDB := &datamodel.ActiveDirectory{
		BaseModel:      datamodel.BaseModel{UUID: adUUID, ID: vcpID},
		AdName:         "test-ad",
		State:          "READY",
		StateDetails:   "VCP ready",
		CredentialPath: "path/to/secret",
	}
	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(vcpADFromDB, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, vcpID, ad.ID, "merged AD must have ID set from VCP for domain check and GetSvmsForAd")
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_SDEMode_IgnoresCreateCommonResourcesFlag(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440008"

	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	// Even when true, SDE path should be used when CVP_HOST is set.
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	originalGetActiveDirectorySde := getActiveDirectorySde
	sdeAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: adUUID},
		AdName:    "sde-ad",
		State:     "READY",
	}
	getActiveDirectorySde = func(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
		return sdeAD, nil
	}
	defer func() { getActiveDirectorySde = originalGetActiveDirectorySde }()

	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(nil, nil)

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	assert.NoError(t, err)
	assert.NotNil(t, ad)
	assert.Equal(t, "sde-ad", ad.AdName)
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_SDEMode_SDEFetchError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440004"

	// Enable SDE mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Mock SDE fetch to return error
	originalGetActiveDirectorySde := getActiveDirectorySde
	getActiveDirectorySde = func(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
		return nil, customerrors.NewNotFoundErr("ActiveDirectory", &params.UUID)
	}
	defer func() { getActiveDirectorySde = originalGetActiveDirectorySde }()

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	// Should return error from SDE
	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.True(t, customerrors.IsNotFoundErr(err))
}

func Test_getActiveDirectory_SDEMode_VCPFetchError(t *testing.T) {
	ctx := context.Background()
	mockSe := new(database.MockStorage)
	adUUID := "550e8400-e29b-41d4-a716-446655440005"

	// Enable SDE mode
	originalCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "https://sde.example.com"
	defer func() {
		cvp.CVP_HOST = originalCVPHost
	}()

	// Mock SDE fetch succeeds
	originalGetActiveDirectorySde := getActiveDirectorySde
	sdeAD := &models.ActiveDirectory{
		BaseModel: models.BaseModel{UUID: adUUID},
		State:     "READY",
	}
	getActiveDirectorySde = func(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
		return sdeAD, nil
	}
	defer func() { getActiveDirectorySde = originalGetActiveDirectorySde }()

	// Mock VCP fetch returns non-NotFound error
	mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).
		Return(nil, errors.New("database connection error"))

	params := makeDescribeADParams("test-project", "us-central1", adUUID)
	ad, err := _getActiveDirectory(ctx, params, mockSe)

	// Should return VCP error (not NotFound)
	assert.Error(t, err)
	assert.Nil(t, ad)
	assert.Contains(t, err.Error(), "database connection error")
	mockSe.AssertExpectations(t)
}

func Test_getActiveDirectory_SDEMode_StatePriorityScenarios(t *testing.T) {
	tests := []struct {
		name          string
		sdeState      string
		vcpState      string
		expectedState string
	}{
		{"SDE UPDATING vs VCP ERROR", "UPDATING", "ERROR", "UPDATING"},
		{"SDE READY vs VCP IN_USE", "READY", "IN_USE", "IN_USE"},
		{"SDE ERROR vs VCP READY", "ERROR", "READY", "ERROR"},
		{"SDE IN_USE vs VCP UPDATING", "IN_USE", "UPDATING", "UPDATING"},
		{"Both READY", "READY", "READY", "READY"},
		{"Both ERROR", "ERROR", "ERROR", "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockSe := new(database.MockStorage)
			adUUID := "550e8400-e29b-41d4-a716-446655440006"

			// Enable SDE mode
			originalCVPHost := cvp.CVP_HOST
					cvp.CVP_HOST = "https://sde.example.com"
					defer func() {
				cvp.CVP_HOST = originalCVPHost
					}()

			// Mock SDE fetch
			originalGetActiveDirectorySde := getActiveDirectorySde
			sdeAD := &models.ActiveDirectory{
				BaseModel:    models.BaseModel{UUID: adUUID},
				AdName:       "test-ad",
				State:        tt.sdeState,
				StateDetails: "SDE state",
			}
			getActiveDirectorySde = func(ctx context.Context, params *common.GetADParams) (*models.ActiveDirectory, error) {
				// Return a copy to avoid mutation issues
				return &models.ActiveDirectory{
					BaseModel:    sdeAD.BaseModel,
					AdName:       sdeAD.AdName,
					State:        sdeAD.State,
					StateDetails: sdeAD.StateDetails,
				}, nil
			}
			defer func() { getActiveDirectorySde = originalGetActiveDirectorySde }()

			// Mock VCP fetch
			vcpADFromDB := &datamodel.ActiveDirectory{
				BaseModel:      datamodel.BaseModel{UUID: adUUID},
				AdName:         "test-ad",
				State:          tt.vcpState,
				StateDetails:   "VCP state",
				CredentialPath: "path/to/secret",
			}
			mockSe.On("GetActiveDirectoryByUuidAndAccountId", mock.Anything, adUUID, int64(0)).Return(vcpADFromDB, nil)

			params := makeDescribeADParams("test-project", "us-central1", adUUID)
			ad, err := _getActiveDirectory(ctx, params, mockSe)

			assert.NoError(t, err)
			assert.NotNil(t, ad)
			assert.Equal(t, tt.expectedState, ad.State,
				"Expected state %s when SDE=%s and VCP=%s",
				tt.expectedState, tt.sdeState, tt.vcpState)
			mockSe.AssertExpectations(t)
		})
	}
}
