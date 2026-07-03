package api

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backup_vault"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	sde "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/sde"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	errors2 "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// V1betaListBackupVaults
func TestV1betaListBackupVaults(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	t.Run("WhenListBackupVaultsSuccessViaCVP", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)

		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("ListBackupVaults", mock.Anything, "12345").
			Return([]*coremodels.BackupVaultV1beta{}, nil)

		mockResponse := &backup_vault.V1betaListBackupVaultsOK{
			Payload: &backup_vault.V1betaListBackupVaultsOKBody{
				BackupVaults: []*models.BackupVaultV1beta{
					{
						ResourceID:    nillable.GetStringPtr("bv-1"),
						BackupRegion:  nillable.GetStringPtr("backup-region"),
						BackupVaultID: "backup-id",
					},
				},
			},
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, 1, len(result.(*gcpgenserver.V1betaListBackupVaultsOK).BackupVaults))
		assert.Equal(t, "backup-id", result.(*gcpgenserver.V1betaListBackupVaultsOK).BackupVaults[0].BackupVaultId.Value)
	})

	t.Run("WhenUseVCPRegion_ReturnsInternalServerErrorWhenListFails", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("ListBackupVaults", mock.Anything, "12345").
			Return(nil, fmt.Errorf("orchestrator list failed"))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaListBackupVaultsInternalServerError)
		require.True(t, ok)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, "failed to list backup vaults", internalErr.Message)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUseVCPRegion_ReturnsConvertedBackupVaults", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "us-east4",
			ProjectNumber:  "proj-99",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		bvs := []*coremodels.BackupVaultV1beta{
			{Name: "bv-a", BackupVaultID: "id-a", LifeCycleState: "READY"},
			{Name: "bv-b", BackupVaultID: "id-b", LifeCycleState: "CREATING"},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("ListBackupVaults", mock.Anything, "proj-99").Return(bvs, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		okBody, ok := result.(*gcpgenserver.V1betaListBackupVaultsOK)
		require.True(t, ok)
		require.Len(t, okBody.BackupVaults, 2)
		assert.Equal(t, "id-a", okBody.BackupVaults[0].BackupVaultId.Value)
		assert.Equal(t, "id-b", okBody.BackupVaults[1].BackupVaultId.Value)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUseVCPRegion_ReturnsEmptyListWhenNoVaults", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("ListBackupVaults", mock.Anything, "12345").
			Return([]*coremodels.BackupVaultV1beta{}, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		okBody, ok := result.(*gcpgenserver.V1betaListBackupVaultsOK)
		require.True(t, ok)
		assert.Empty(t, okBody.BackupVaults)
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestUpdateBackupVaultStateDetails_OverlaysCmekFromVCP(t *testing.T) {
	encryptionState := "ENCRYPTION_STATE_FAILED"
	backupsKeyVersion := "projects/p/locations/r/kmsConfigs/c/cryptoKeys/k/cryptoKeyVersions/2"

	// CVP view before overlay.
	cvpBv := &models.BackupVaultV1beta{
		ResourceID:               nillable.GetStringPtr("bv-1"),
		BackupVaultID:            "backup-id",
		State:                    "READY",
		StateDetails:             "ready",
		EncryptionState:          nillable.GetStringPtr("ENCRYPTION_STATE_COMPLETED"),
		BackupsPrimaryKeyVersion: nillable.GetStringPtr("projects/p/locations/r/kmsConfigs/c/cryptoKeys/k/cryptoKeyVersions/1"),
	}

	// VCP view with updated lifecycle and CMEK fields.
	vcpBv := &coremodels.BackupVaultV1beta{
		Name:                     "bv-1",
		LifeCycleState:           "UPDATING",
		LifeCycleStateDetails:    "updating",
		EncryptionState:          &encryptionState,
		BackupsPrimaryKeyVersion: &backupsKeyVersion,
	}

	res := updateBackupVaultStateDetails([]*coremodels.BackupVaultV1beta{vcpBv}, []*models.BackupVaultV1beta{cvpBv})
	require.Len(t, res, 1)

	updated := res[0]
	assert.Equal(t, "UPDATING", updated.State)
	assert.Equal(t, "updating", updated.StateDetails)
	if assert.NotNil(t, updated.EncryptionState) {
		assert.Equal(t, encryptionState, *updated.EncryptionState)
	}
	if assert.NotNil(t, updated.BackupsPrimaryKeyVersion) {
		assert.Equal(t, backupsKeyVersion, *updated.BackupsPrimaryKeyVersion)
	}
}

func TestUpdateBackupVaultStateDetails_SetsCrossProjectVaultForCrossProject(t *testing.T) {
	cvpBv := &models.BackupVaultV1beta{
		ResourceID:    nillable.GetStringPtr("cross-project-vault"),
		BackupVaultID: "vault-id",
		State:         "READY",
		StateDetails:  "ready",
	}

	vcpBv := &coremodels.BackupVaultV1beta{
		Name:                  "cross-project-vault",
		LifeCycleState:        "READY",
		LifeCycleStateDetails: "ready",
		ServiceType:           datamodel.ServiceTypeCrossProject,
	}

	res := updateBackupVaultStateDetails([]*coremodels.BackupVaultV1beta{vcpBv}, []*models.BackupVaultV1beta{cvpBv})
	require.Len(t, res, 1)

	updated := res[0]
	require.NotNil(t, updated.CrossProjectVault)
	assert.True(t, *updated.CrossProjectVault)
}

func TestUpdateBackupVaultStateDetails_NoCrossProjectVaultForGCNV(t *testing.T) {
	cvpBv := &models.BackupVaultV1beta{
		ResourceID:    nillable.GetStringPtr("gcnv-vault"),
		BackupVaultID: "vault-id",
		State:         "READY",
		StateDetails:  "ready",
	}

	vcpBv := &coremodels.BackupVaultV1beta{
		Name:                  "gcnv-vault",
		LifeCycleState:        "READY",
		LifeCycleStateDetails: "ready",
		ServiceType:           datamodel.ServiceTypeGCNV,
	}

	res := updateBackupVaultStateDetails([]*coremodels.BackupVaultV1beta{vcpBv}, []*models.BackupVaultV1beta{cvpBv})
	require.Len(t, res, 1)

	updated := res[0]
	assert.Nil(t, updated.CrossProjectVault)
}

func TestConvertBackupVaultV1Beta_CrossProjectVaultTrue(t *testing.T) {
	crossProject := true
	bv := &models.BackupVaultV1beta{
		BackupVaultID:     "vault-id",
		ResourceID:        nillable.GetStringPtr("gcbdr-vault"),
		State:             "READY",
		StateDetails:      "ready",
		CrossProjectVault: &crossProject,
	}

	result := convertBackupVaultV1Beta(bv)
	assert.True(t, result.CrossProjectVault.IsSet())
	assert.True(t, result.CrossProjectVault.Value)
}

func TestConvertBackupVaultV1Beta_CrossProjectVaultFalse(t *testing.T) {
	crossProject := false
	bv := &models.BackupVaultV1beta{
		BackupVaultID:     "vault-id",
		ResourceID:        nillable.GetStringPtr("gcbdr-vault"),
		State:             "READY",
		StateDetails:      "ready",
		CrossProjectVault: &crossProject,
	}

	result := convertBackupVaultV1Beta(bv)
	assert.True(t, result.CrossProjectVault.IsSet())
	assert.False(t, result.CrossProjectVault.Value)
}
func TestConvertBackupVaultV1Beta_CrossProjectVaultNotSetForNonCrossProject(t *testing.T) {
	bv := &models.BackupVaultV1beta{
		BackupVaultID: "vault-id",
		ResourceID:    nillable.GetStringPtr("normal-vault"),
		State:         "READY",
		StateDetails:  "ready",
	}

	result := convertBackupVaultV1Beta(bv)
	assert.False(t, result.CrossProjectVault.IsSet())
}

func TestConvertCoreToCvpBackupVault_SetsCrossProjectVaultForCrossProject(t *testing.T) {
	coreBV := &coremodels.BackupVaultV1beta{
		BackupVaultID:  "vault-id",
		Name:           "cross-project-vault",
		LifeCycleState: "READY",
		ServiceType:    datamodel.ServiceTypeCrossProject,
		CreatedAt:      time.Now(),
	}

	result := convertCoreToCvpBackupVault(coreBV)
	require.NotNil(t, result.CrossProjectVault)
	assert.True(t, *result.CrossProjectVault)
}

func TestConvertCoreToCvpBackupVault_DoesNotSetCrossProjectVaultForGCNV(t *testing.T) {
	coreBV := &coremodels.BackupVaultV1beta{
		BackupVaultID:  "vault-id",
		Name:           "gcnv-vault",
		LifeCycleState: "READY",
		ServiceType:    datamodel.ServiceTypeGCNV,
		CreatedAt:      time.Now(),
	}

	result := convertCoreToCvpBackupVault(coreBV)
	assert.Nil(t, result.CrossProjectVault)
}

func TestConvertCoreToCvpBackupVault_MapsCMEKFields(t *testing.T) {
	kmsPath := "projects/my-project/locations/us-central1/kmsConfigs/my-config"
	keyVersion := "projects/my-project/locations/us-central1/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"
	encState := "ENCRYPTION_STATE_COMPLETED"

	coreBV := &coremodels.BackupVaultV1beta{
		BackupVaultID:            "vault-id",
		Name:                     "cmek-vault",
		LifeCycleState:           "READY",
		CreatedAt:                time.Now(),
		KmsConfigResourcePath:    &kmsPath,
		BackupsPrimaryKeyVersion: &keyVersion,
		EncryptionState:          &encState,
	}

	result := convertCoreToCvpBackupVault(coreBV)
	require.NotNil(t, result.KmsConfigResourcePath)
	assert.Equal(t, kmsPath, *result.KmsConfigResourcePath)
	require.NotNil(t, result.BackupsPrimaryKeyVersion)
	assert.Equal(t, keyVersion, *result.BackupsPrimaryKeyVersion)
	require.NotNil(t, result.EncryptionState)
	assert.Equal(t, encState, *result.EncryptionState)
}

func TestConvertCoreToCvpBackupVault_OmitsCMEKFieldsWhenNil(t *testing.T) {
	coreBV := &coremodels.BackupVaultV1beta{
		BackupVaultID:  "vault-id",
		Name:           "no-cmek-vault",
		LifeCycleState: "READY",
		CreatedAt:      time.Now(),
	}

	result := convertCoreToCvpBackupVault(coreBV)
	assert.Nil(t, result.KmsConfigResourcePath)
	assert.Nil(t, result.BackupsPrimaryKeyVersion)
	assert.Nil(t, result.EncryptionState)
}

func TestConvertCoreToCvpBackupVault_MapsCrossRegionFieldsWhenCrossRegionType(t *testing.T) {
	bvType := activities.CrossRegionBackupType
	backupRegion := "us-east4"
	sourceRegion := "us-central1"
	destBV := "projects/1/locations/us-east4/backupVaults/dest-bv"
	srcBV := "projects/1/locations/us-central1/backupVaults/src-bv"

	coreBV := &coremodels.BackupVaultV1beta{
		BackupVaultID:          "vault-id",
		Name:                   "cr-bv",
		LifeCycleState:         "READY",
		CreatedAt:              time.Now(),
		BackupVaultType:        &bvType,
		BackupRegion:           &backupRegion,
		SourceRegion:           &sourceRegion,
		DestinationBackupVault: &destBV,
		SourceBackupVault:      &srcBV,
	}

	result := convertCoreToCvpBackupVault(coreBV)
	require.NotNil(t, result.BackupRegion)
	assert.Equal(t, backupRegion, *result.BackupRegion)
	require.NotNil(t, result.SourceRegion)
	assert.Equal(t, sourceRegion, *result.SourceRegion)
	require.NotNil(t, result.DestinationBackupVault)
	assert.Equal(t, destBV, *result.DestinationBackupVault)
	require.NotNil(t, result.SourceBackupVault)
	assert.Equal(t, srcBV, *result.SourceBackupVault)
}

func TestBuildCreateBackupVaultParams(t *testing.T) {
	t.Run("BuildsAllOptionalFieldsWhenSet", func(t *testing.T) {
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:               gcpgenserver.NewOptString("vault-1"),
			Description:              gcpgenserver.NewOptString("test vault"),
			KmsConfigResourcePath:    gcpgenserver.NewOptString("projects/p/locations/l/keyRings/r/cryptoKeys/k"),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString("projects/p/locations/l/kmsConfigs/c/cryptoKeys/k/cryptoKeyVersions/1"),
			TenantProject:            gcpgenserver.NewOptString("tenant-project-1"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(true),
			}),
		}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "12345",
		}
		backupRegion := "us-east4"

		result := buildCreateBackupVaultParams(req, params, &backupRegion)

		require.NotNil(t, result)
		assert.Equal(t, "vault-1", result.ResourceId)
		assert.Equal(t, "test vault", result.Description)
		require.NotNil(t, result.BackupRegion)
		assert.Equal(t, "us-east4", *result.BackupRegion)
		assert.Equal(t, "us-central1", result.LocationId)
		assert.Equal(t, "12345", result.ProjectNumber)
		require.NotNil(t, result.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration)
		assert.Equal(t, int64(30), *result.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration)
		require.NotNil(t, result.BackupRetentionPolicy.IsDailyBackupImmutable)
		assert.True(t, *result.BackupRetentionPolicy.IsDailyBackupImmutable)
		require.NotNil(t, result.BackupRetentionPolicy.IsAdhocBackupImmutable)
		assert.True(t, *result.BackupRetentionPolicy.IsAdhocBackupImmutable)
		require.NotNil(t, result.BackupRetentionPolicy.IsMonthlyBackupImmutable)
		assert.False(t, *result.BackupRetentionPolicy.IsMonthlyBackupImmutable)
		require.NotNil(t, result.BackupRetentionPolicy.IsWeeklyBackupImmutable)
		assert.True(t, *result.BackupRetentionPolicy.IsWeeklyBackupImmutable)
		require.NotNil(t, result.KmsConfigResourcePath)
		assert.Equal(t, "projects/p/locations/l/keyRings/r/cryptoKeys/k", *result.KmsConfigResourcePath)
		require.NotNil(t, result.BackupsPrimaryKeyVersion)
		assert.Equal(t, "projects/p/locations/l/kmsConfigs/c/cryptoKeys/k/cryptoKeyVersions/1", *result.BackupsPrimaryKeyVersion)
		require.NotNil(t, result.TenantProject)
		assert.Equal(t, "tenant-project-1", *result.TenantProject)
	})

	t.Run("LeavesOptionalFieldsUnsetWhenNotProvided", func(t *testing.T) {
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("vault-2"),
			Description: gcpgenserver.NewOptString("minimal vault"),
		}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-west1",
			ProjectNumber: "99999",
		}

		result := buildCreateBackupVaultParams(req, params, nil)

		require.NotNil(t, result)
		assert.Equal(t, "vault-2", result.ResourceId)
		assert.Equal(t, "minimal vault", result.Description)
		assert.Nil(t, result.BackupRegion)
		assert.Equal(t, "us-west1", result.LocationId)
		assert.Equal(t, "99999", result.ProjectNumber)
		assert.Nil(t, result.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration)
		assert.Nil(t, result.BackupRetentionPolicy.IsDailyBackupImmutable)
		assert.Nil(t, result.BackupRetentionPolicy.IsAdhocBackupImmutable)
		assert.Nil(t, result.BackupRetentionPolicy.IsMonthlyBackupImmutable)
		assert.Nil(t, result.BackupRetentionPolicy.IsWeeklyBackupImmutable)
		assert.Nil(t, result.KmsConfigResourcePath)
		assert.Nil(t, result.BackupsPrimaryKeyVersion)
		assert.Nil(t, result.TenantProject)
	})
}

func TestV1betaListBackupVaultsOrchError(t *testing.T) {
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
	}()
	backupEnabled = true
	cvp.CVP_HOST = ""

	params := gcpgenserver.V1betaListBackupVaultsParams{
		LocationId:     "test-location",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	mockOrchestrator.On("ListBackupVaults", mock.Anything, "12345").Return(nil, errors2.New("orchestrator error"))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaListBackupVaults(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &gcpgenserver.V1betaListBackupVaultsInternalServerError{}, result)
	mockOrchestrator.AssertExpectations(t)
}

// V1betaDescribeBackupVault unittests
func TestV1betaDescribeBackupVault(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("WhenDescribeBackupVaultSuccess", func(t *testing.T) {
		// Define request
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}
		bvRetentionPolicy := models.BackupRetentionPolicyV1beta{
			DailyBackupImmutable:               false,
			MonthlyBackupImmutable:             false,
			ManualBackupImmutable:              false,
			WeeklyBackupImmutable:              false,
			BackupMinimumEnforcedRetentionDays: nillable.GetInt64Ptr(2),
		}

		mockResponse := &backup_vault.V1betaDescribeBackupVaultOK{
			Payload: &models.BackupVaultV1beta{
				ResourceID:             nillable.GetStringPtr(gcpgenserver.NewOptString("bv-1").Value),
				BackupRegion:           nillable.GetStringPtr("br-1"),
				BackupVaultID:          "bvid-1",
				BackupVaultType:        nillable.GetStringPtr("bvtype-1"),
				Description:            nillable.GetStringPtr("Test Description"),
				DestinationBackupVault: nillable.GetStringPtr("dbv-1"),
				SourceBackupVault:      nillable.GetStringPtr("sbv-1"),
				SourceRegion:           nillable.GetStringPtr("sr-1"),
				State:                  "ACTIVE",
				StateDetails:           "DETAILS",
				BackupRetentionPolicy:  &bvRetentionPolicy,
			},
		}
		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the resource name is as expected
		assert.Equal(t, "bvid-1", result.(*gcpgenserver.BackupVaultV1beta).BackupVaultId.Value)
	})

	t.Run("WhenDescribeBackupVaultSetsCrossProjectVaultForCrossProject", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true

		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-cross-project",
		}

		vcpBv := &coremodels.BackupVaultV1beta{
			Name:                  "bv-cross-project",
			BackupVaultID:         "bvid-cross-project",
			LifeCycleState:        "READY",
			LifeCycleStateDetails: "ready",
			ServiceType:           datamodel.ServiceTypeCrossProject,
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-cross-project", "12345").Return(vcpBv, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		bv := result.(*gcpgenserver.BackupVaultV1beta)
		assert.True(t, bv.CrossProjectVault.IsSet())
		assert.True(t, bv.CrossProjectVault.Value)

		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUseVCPRegion_ReturnsNotFoundWhenBackupVaultMissing", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-missing",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-missing", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-missing")))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		notFound, ok := result.(*gcpgenserver.V1betaDescribeBackupVaultNotFound)
		require.True(t, ok)
		assert.Equal(t, float64(404), notFound.Code)
		assert.Equal(t, "Backup vault not found", notFound.Message)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUseVCPRegion_ReturnsInternalServerErrorWhenGetFails", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-err",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-err", "12345").
			Return(nil, fmt.Errorf("database connection failed"))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaDescribeBackupVaultInternalServerError)
		require.True(t, ok)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, "database connection failed", internalErr.Message)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUseVCPRegion_ReturnsBackupVaultFromVCP", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-vcp-only",
		}

		vcpBv := &coremodels.BackupVaultV1beta{
			Name:                  "bv-vcp-only",
			BackupVaultID:         "bvid-vcp",
			LifeCycleState:        "READY",
			LifeCycleStateDetails: "ready",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-vcp-only", "12345").Return(vcpBv, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		bv := result.(*gcpgenserver.BackupVaultV1beta)
		assert.Equal(t, "bvid-vcp", bv.BackupVaultId.Value)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenDescribeBackupVaultFailsWithBadRequest", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true

		// Define input parameters
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-1", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-1")))

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &backup_vault.V1betaDescribeBackupVaultBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeBackupVaultBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeBackupVaultBadRequest).Message)
	})

	t.Run("WhenDescribeBackupVaultFailsWithUnprocessableEntry", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-1", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-1")))
		// Define mock error
		errorMessage := "Unprocessable error"
		errorCode := float64(422)
		mockError := &backup_vault.V1betaDescribeBackupVaultUnprocessableEntity{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeBackupVaultUnprocessableEntity).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeBackupVaultUnprocessableEntity).Message)
	})

	t.Run("WhenDescribeBackupVaultFailsWithUnauthorized", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-1", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-1")))
		// Define mock error
		errorMessage := "Unauthorized error"
		errorCode := float64(401)
		mockError := &backup_vault.V1betaDescribeBackupVaultUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeBackupVaultUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeBackupVaultUnauthorized).Message)
	})

	t.Run("WhenDescribeBackupVaultFailsWithForbidden", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-1", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-1")))
		// Define mock error
		errorMessage := "Forbidden error"
		errorCode := float64(403)
		mockError := &backup_vault.V1betaDescribeBackupVaultForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeBackupVaultForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeBackupVaultForbidden).Message)
	})

	t.Run("WhenDescribeBackupVaultFailsWithTooManyRequests", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-1", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-1")))
		// Define mock error
		errorMessage := "Too many requests error"
		errorCode := float64(401)
		mockError := &backup_vault.V1betaDescribeBackupVaultTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeBackupVaultTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeBackupVaultTooManyRequests).Message)
	})

	t.Run("WhenDescribeBackupVaultFailsWithDefault", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-1", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-1")))
		// Define mock error
		errorMessage := "default error"
		errorCode := float64(500)
		mockError := &backup_vault.V1betaDescribeBackupVaultDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeBackupVaultInternalServerError).Code)
	})

	t.Run("WhenDescribeBackupVaultFailsWithNotImplemented", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaDescribeBackupVaultParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "bv-1",
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-1", "12345").
			Return(nil, errors2.NewNotFoundErr("backup vault", nillable.ToPointer("bv-1")))
		errorMessage := "Not implemented"
		errorCode := float64(501)
		mockError := &backup_vault.V1betaDescribeBackupVaultNotImplemented{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaDescribeBackupVault(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaDescribeBackupVault(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaDescribeBackupVaultInternalServerError).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaDescribeBackupVaultInternalServerError).Message)
	})
}

// V1betaGetMultipleBackupVaults unittests
func TestV1betaGetMultipleBackupVaults(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("WhenGetMultipleBackupVaultsSuccess", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"bvid-1"},
		}

		bvs := make([]*models.BackupVaultV1beta, 0)

		bvs = append(bvs, &models.BackupVaultV1beta{
			ResourceID:             nillable.GetStringPtr("bv-1"),
			BackupRegion:           nillable.GetStringPtr("br-1"),
			SourceRegion:           nillable.GetStringPtr("sr-1"),
			BackupVaultID:          "bvid-1",
			BackupVaultType:        nillable.GetStringPtr("bvtype-1"),
			Description:            nillable.GetStringPtr("test description"),
			SourceBackupVault:      nillable.GetStringPtr("sbv-1"),
			DestinationBackupVault: nillable.GetStringPtr("dbv-1"),
		})

		// Define mock response
		mockResponse := &backup_vault.V1betaGetMultipleBackupVaultsOK{
			Payload: &backup_vault.V1betaGetMultipleBackupVaultsOKBody{
				BackupVaults: bvs,
			},
		}

		mockOrchestrator.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(
			nil, nil)
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, "bvid-1", result.(*gcpgenserver.V1betaGetMultipleBackupVaultsOK).BackupVaults[0].BackupVaultId.Value)
		assert.Equal(t, 1, len(result.(*gcpgenserver.V1betaGetMultipleBackupVaultsOK).BackupVaults))
	})

	t.Run("WhenUseVCPRegion_ReturnsInternalServerErrorWhenOrchestratorFails", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"bvid-1"},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetMultipleBackupVaults", mock.Anything, []string{"bvid-1"}).
			Return(nil, fmt.Errorf("storage unavailable"))

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError)
		require.True(t, ok)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, "storage unavailable", internalErr.Message)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUseVCPRegion_ReturnsConvertedBackupVaults", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"uuid-a", "uuid-b"},
		}

		vaults := []*coremodels.BackupVaultV1beta{
			{
				Name:           "bv-a",
				BackupVaultID:  "vault-id-a",
				LifeCycleState: "READY",
			},
			{
				Name:           "bv-b",
				BackupVaultID:  "vault-id-b",
				LifeCycleState: "CREATING",
			},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetMultipleBackupVaults", mock.Anything, []string{"uuid-a", "uuid-b"}).Return(vaults, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		okBody, ok := result.(*gcpgenserver.V1betaGetMultipleBackupVaultsOK)
		require.True(t, ok)
		require.Len(t, okBody.BackupVaults, 2)
		assert.Equal(t, "vault-id-a", okBody.BackupVaults[0].BackupVaultId.Value)
		assert.Equal(t, "vault-id-b", okBody.BackupVaults[1].BackupVaultId.Value)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenUseVCPRegion_ReturnsEmptyListWhenNoVaults", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"missing-1"},
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetMultipleBackupVaults", mock.Anything, []string{"missing-1"}).
			Return([]*coremodels.BackupVaultV1beta{}, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		okBody, ok := result.(*gcpgenserver.V1betaGetMultipleBackupVaultsOK)
		require.True(t, ok)
		assert.Empty(t, okBody.BackupVaults)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenGetMultipleBackupVaultsReturnErrorsVCP", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"bvid-1"},
		}

		bvs := make([]*models.BackupVaultV1beta, 0)

		bvs = append(bvs, &models.BackupVaultV1beta{
			ResourceID:             nillable.GetStringPtr("bv-1"),
			BackupRegion:           nillable.GetStringPtr("br-1"),
			SourceRegion:           nillable.GetStringPtr("sr-1"),
			BackupVaultID:          "bvid-1",
			BackupVaultType:        nillable.GetStringPtr("bvtype-1"),
			Description:            nillable.GetStringPtr("test description"),
			SourceBackupVault:      nillable.GetStringPtr("sbv-1"),
			DestinationBackupVault: nillable.GetStringPtr("dbv-1"),
		})

		// Define mock response
		mockResponse := &backup_vault.V1betaGetMultipleBackupVaultsOK{
			Payload: &backup_vault.V1betaGetMultipleBackupVaultsOKBody{
				BackupVaults: bvs,
			},
		}

		mockOrchestrator.On("GetMultipleBackupVaults", mock.Anything, mock.Anything).Return(
			nil, errors2.New("VCP error"))
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})

	t.Run("WhenGetMultipleBackupVaultsFailsWithBadRequest", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsBadRequest).Message)
	})
	t.Run("WhenGetMultipleBackupVaultsFailsWithNotFound", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}

		// Define mock error
		errorCode := float64(404)
		errorMessage := "Bad Request"
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsNotFound{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsNotFound).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsNotFound).Message)
	})

	t.Run("WhenGetMultipleBackupVaultsFailsWithUnauthorized", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}

		// Define mock error
		errorMessage := "Unauthorized error"
		errorCode := float64(401)
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsUnauthorized).Message)
	})

	t.Run("WhenGetMultipleBackupVaultsFailsWithForbidden", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}

		// Define mock error
		errorMessage := "Forbidden error"
		errorCode := float64(403)
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsForbidden).Message)
	})

	t.Run("WhenGetMultipleBackupVaultsFailsWithDefault", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}

		// Define mock error
		errorMessage := "default error"
		errorCode := float64(500)
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsDefault{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError).Code)
	})

	t.Run("WhenGetMultipleBackupVaultsFailsWithTooManyRequests", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}

		// Define mock error
		errorMessage := "Too many requests"
		errorCode := float64(429)
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsTooManyRequests).Message)
	})

	t.Run("WhenGetMultipleBackupVaultsFailsWithUnknownError", func(t *testing.T) {
		// Create a mock client
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}

		// Define mock error
		errorMessage := "unknown error during the get multiple backup vaults"
		errorCode := float64(500)
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsInternalServerError{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsInternalServerError).Message, errorMessage)
	})

	t.Run("WhenGetMultipleBackupVaultsFailsWithNotImplemented", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaGetMultipleBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.BackupVaultUuidListV1beta{
			BackupVaultUuids: []string{"BV0"},
		}
		errorMessage := "Not implemented"
		errorCode := float64(501)
		mockError := &backup_vault.V1betaGetMultipleBackupVaultsNotImplemented{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaGetMultipleBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaGetMultipleBackupVaults(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsNotImplemented).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupVaultsNotImplemented).Message)
	})
}

func Test_validateBackupPoliciesForBackupVaultWithRetry(t *testing.T) {
	ctx := context.Background()
	backupVault := &coremodels.BackupVaultV1beta{
		BackupVaultID: "vault-123",
		AccountName:   "test-project",
		OwnerID:       "test-project",
		BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
			BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                false,
			IsMonthlyBackupImmutable:               false,
			IsAdhocBackupImmutable:                 false,
		},
	}
	newRetentionParams := &commonparams.BackupRetentionPolicyParams{
		BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
		IsDailyBackupImmutable:                 nillable.GetBoolPtr(true),
	}

	t.Run("Retries on retryable error and succeeds on second attempt", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		// Mock GetBackupPolicyUUIDsFromBackupVaultUUID to return policy IDs
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", ctx, "vault-123", "test-project").Return([]string{"policy-1"}, nil)

		// First call returns backup policies with updating state, second call returns ready state
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(
			map[string]int64{"policy-1": 1},
			map[string]*coremodels.BackupPolicy{
				"policy-1": {
					BackupPolicyUUID: "policy-1",
					State:            datamodel.LifeCycleStateUpdating,
					DailyBackupLimit: 60,
				},
			}, nil).Once()
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(
			map[string]int64{"policy-1": 1},
			map[string]*coremodels.BackupPolicy{
				"policy-1": {
					BackupPolicyUUID: "policy-1",
					State:            datamodel.LifeCycleStateREADY,
					DailyBackupLimit: 60,
				},
			}, nil).Once()

		// Patch time.Sleep to avoid real delay
		origSleep := commonparams.SleepFn
		sleepCalled := 0
		commonparams.SleepFn = func(d time.Duration) { sleepCalled++ }
		defer func() { commonparams.SleepFn = origSleep }()

		err := _validateBackupPoliciesForBackupVaultWithRetry(ctx, backupVault, newRetentionParams, mockOrchestrator, 3, 1*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, 1, sleepCalled, "Should sleep once for one retry")
	})

	t.Run("Stops after max retries on persistent retryable error", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		// Mock GetBackupPolicyUUIDsFromBackupVaultUUID to return policy IDs
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", ctx, "vault-123", "test-project").Return([]string{"policy-1"}, nil)

		// Always return backup policy with updating state (retryable error)
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(
			map[string]int64{"policy-1": 1},
			map[string]*coremodels.BackupPolicy{
				"policy-1": {
					BackupPolicyUUID: "policy-1",
					State:            datamodel.LifeCycleStateUpdating,
					DailyBackupLimit: 60,
				},
			}, nil)

		origSleep := commonparams.SleepFn
		sleepCalled := 0
		commonparams.SleepFn = func(d time.Duration) { sleepCalled++ }
		defer func() { commonparams.SleepFn = origSleep }()

		err := _validateBackupPoliciesForBackupVaultWithRetry(ctx, backupVault, newRetentionParams, mockOrchestrator, 3, 1*time.Millisecond)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Error while creating immutable backup when backup policy is in updating state")
		assert.Equal(t, 2, sleepCalled, "Should sleep between retry attempts (2 sleeps for 3 attempts)")
	})

	t.Run("Does not retry on non-retryable error", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		// Mock GetBackupPolicyUUIDsFromBackupVaultUUID to return policy IDs
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", ctx, "vault-123", "test-project").Return([]string{"policy-1"}, nil)

		// Return an error from ListBackupPoliciesAndVolumeCount (non-retryable error)
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(
			map[string]int64{}, map[string]*coremodels.BackupPolicy{}, errors.New("some non-retryable error"))

		origSleep := commonparams.SleepFn
		sleepCalled := 0
		commonparams.SleepFn = func(d time.Duration) { sleepCalled++ }
		defer func() { commonparams.SleepFn = origSleep }()

		err := _validateBackupPoliciesForBackupVaultWithRetry(ctx, backupVault, newRetentionParams, mockOrchestrator, 3, 1*time.Millisecond)
		assert.Error(t, err) // Should error because ListBackupPoliciesAndVolumeCount failed
		assert.Equal(t, 0, sleepCalled, "Should not sleep for non-retryable errors")
	})

	t.Run("First attempt succeeds, no retries", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		// Mock GetBackupPolicyUUIDsFromBackupVaultUUID to return policy IDs
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", ctx, "vault-123", "test-project").Return([]string{"policy-1"}, nil)

		// Return backup policy in ready state (should succeed immediately)
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(
			map[string]int64{"policy-1": 1},
			map[string]*coremodels.BackupPolicy{
				"policy-1": {
					BackupPolicyUUID: "policy-1",
					State:            datamodel.LifeCycleStateREADY,
					DailyBackupLimit: 60,
				},
			}, nil)

		origSleep := commonparams.SleepFn
		sleepCalled := 0
		commonparams.SleepFn = func(d time.Duration) { sleepCalled++ }
		defer func() { commonparams.SleepFn = origSleep }()

		err := _validateBackupPoliciesForBackupVaultWithRetry(ctx, backupVault, newRetentionParams, mockOrchestrator, 3, 1*time.Millisecond)
		assert.NoError(t, err)
		assert.Equal(t, 0, sleepCalled, "Should not sleep if no retry needed")
	})

	t.Run("Retryable error then non-retryable error (should stop on non-retryable)", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		// Mock GetBackupPolicyUUIDsFromBackupVaultUUID to return policy IDs
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", ctx, "vault-123", "test-project").Return([]string{"policy-1"}, nil)

		// First call returns updating state (retryable), second call returns ready state but with validation error
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(
			map[string]int64{"policy-1": 1},
			map[string]*coremodels.BackupPolicy{
				"policy-1": {
					BackupPolicyUUID: "policy-1",
					State:            datamodel.LifeCycleStateUpdating,
					DailyBackupLimit: 60,
				},
			}, nil).Once()
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(
			map[string]int64{"policy-1": 1},
			map[string]*coremodels.BackupPolicy{
				"policy-1": {
					BackupPolicyUUID: "policy-1",
					State:            datamodel.LifeCycleStateREADY,
					DailyBackupLimit: 5, // Too low for immutable period, will cause validation error
				},
			}, nil).Once()

		origSleep := commonparams.SleepFn
		sleepCalled := 0
		commonparams.SleepFn = func(d time.Duration) { sleepCalled++ }
		defer func() { commonparams.SleepFn = origSleep }()

		err := _validateBackupPoliciesForBackupVaultWithRetry(ctx, backupVault, newRetentionParams, mockOrchestrator, 3, 1*time.Millisecond)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "validation failed")
		assert.Equal(t, 1, sleepCalled, "Should sleep only once before non-retryable error")
	})

	t.Run("All attempts return non-retryable errors (should not retry at all)", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", ctx, "vault-123", "test-project").Return([]string{"policy-1"}, nil)
		mockOrchestrator.On("ListBackupPoliciesAndVolumeCount", ctx, "test-project", []string{"policy-1"}).Return(map[string]int64(nil), map[string]*coremodels.BackupPolicy(nil), errors.New("always non-retryable error"))

		origSleep := commonparams.SleepFn
		sleepCalled := 0
		commonparams.SleepFn = func(d time.Duration) { sleepCalled++ }
		defer func() { commonparams.SleepFn = origSleep }()

		err := _validateBackupPoliciesForBackupVaultWithRetry(ctx, backupVault, newRetentionParams, mockOrchestrator, 3, 1*time.Millisecond)
		assert.Error(t, err) // Should return error because ListBackupPoliciesAndVolumeCount failed
		assert.Equal(t, 0, sleepCalled, "Should not sleep for non-retryable errors")
	})
}

// TestV1betaCreateBackupVaultWithImmutableBackups tests backup vault creation with various immutable backup scenarios
func TestV1betaCreateBackupVaultWithVCPOrchestration(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCVPHost := cvp.CVP_HOST
	origOrchestration := env.EnableBackupVaultVCPOrchestration
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origCVPHost
		env.EnableBackupVaultVCPOrchestration = origOrchestration
	}()
	backupEnabled = true
	cvp.CVP_HOST = "localhost:8009"
	env.EnableBackupVaultVCPOrchestration = true

	originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
	parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "", nil
	}

	t.Run("CrossRegionCreate_SendsUUIDToSDEAndPersistsInVCP", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("test-backup-vault"),
			Description:  gcpgenserver.NewOptString("desc"),
			BackupRegion: gcpgenserver.NewOptString("us-east1"),
		}

		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").
			Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Capture the UUID VCP generates and sends to SDE so we can assert the same UUID is persisted in VCP.
		var sdeUUID string
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Name: "operations/op-1",
				Response: &models.BackupVaultV1beta{
					ResourceID:    nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID: "sde-generated-should-be-ignored",
				},
			},
		}
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(p *backup_vault.V1betaCreateBackupVaultParams) bool {
				if p.Body == nil || p.Body.BackupVaultID == nil || *p.Body.BackupVaultID == "" {
					return false
				}
				sdeUUID = *p.Body.BackupVaultID
				return p.Body.ResourceID == "test-backup-vault" &&
					p.LocationID == "us-central1" &&
					p.ProjectNumber == "test-project-123"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		mockOrchestrator.On("CreateBackupVault", mock.Anything, mock.MatchedBy(func(p *commonparams.CreateBackupVaultParams) bool {
			return p.BackupVaultID != "" && p.BackupVaultID == sdeUUID && p.ResourceId == "test-backup-vault"
		})).Return(&coremodels.BackupVaultV1beta{
			Name:           "test-backup-vault",
			BackupVaultID:  "will-be-set-below",
			LifeCycleState: "READY",
		}, nil)

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok, "expected OperationV1beta response")
		assert.True(t, op.Done.Value)
		mockOrchestrator.AssertExpectations(t)
		mockClient.AssertExpectations(t)
	})

	t.Run("SDEFailure_ReturnsErrorAndDoesNotPersistInVCP", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-456"),
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("desc"),
		}

		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").
			Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultConflict{
				Payload: &models.Error{Code: 409, Message: "already exists"},
			})

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupVaultConflict{}, result)
		// CreateBackupVault must not be called when SDE fails.
		mockOrchestrator.AssertNotCalled(t, "CreateBackupVault", mock.Anything, mock.Anything)
		mockClient.AssertExpectations(t)
	})

	// sdeSuccessSetup wires a minimal SDE mock that returns success so tests can focus on the post-SDE path.
	sdeSuccessSetup := func(t *testing.T, mockClient *backup_vault.MockClientService) {
		t.Helper()
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(&backup_vault.V1betaCreateBackupVaultAccepted{
				Payload: &models.OperationV1beta{
					Name: "operations/op-1",
					Response: &models.BackupVaultV1beta{
						ResourceID:    nillable.GetStringPtr("test-vault"),
						BackupVaultID: "sde-uuid",
					},
				},
			}, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalClient := sde.BackupVaultCreateClient
		t.Cleanup(func() { sde.BackupVaultCreateClient = originalClient })
		sde.BackupVaultCreateClient = func(_ log.Logger, _ string) cvpapi.Cvp { return *cvpClient }
	}

	t.Run("WhenVCPCreateReturnsValidationError_Returns400", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber: "test-project-123",
			LocationId:    "us-central1",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-vault"),
			Description: gcpgenserver.NewOptString("desc"),
		}

		vaultName := "test-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-vault", "test-project-123").
			Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
		sdeSuccessSetup(t, mockClient)

		mockOrchestrator.On("CreateBackupVault", mock.Anything, mock.Anything).
			Return(nil, errors2.NewUserInputValidationErr("invalid kms config"))

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		resp, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		require.True(t, ok, "expected V1betaCreateBackupVaultBadRequest")
		assert.Equal(t, float64(400), resp.Code)
		assert.Contains(t, resp.Message, "invalid kms config")
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenVCPCreateReturnsInternalError_Returns500", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber: "test-project-123",
			LocationId:    "us-central1",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-vault"),
			Description: gcpgenserver.NewOptString("desc"),
		}

		vaultName := "test-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-vault", "test-project-123").
			Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
		sdeSuccessSetup(t, mockClient)

		mockOrchestrator.On("CreateBackupVault", mock.Anything, mock.Anything).
			Return(nil, errors.New("database unavailable"))

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.Error(t, err)
		resp, ok := result.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		require.True(t, ok, "expected V1betaCreateBackupVaultInternalServerError")
		assert.Equal(t, float64(500), resp.Code)
		assert.Contains(t, resp.Message, "database unavailable")
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("WhenJsonMarshalFailsAfterVCPCreate_Returns500", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber: "test-project-123",
			LocationId:    "us-central1",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-vault"),
			Description: gcpgenserver.NewOptString("desc"),
		}

		vaultName := "test-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-vault", "test-project-123").
			Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
		sdeSuccessSetup(t, mockClient)

		mockOrchestrator.On("CreateBackupVault", mock.Anything, mock.Anything).
			Return(&coremodels.BackupVaultV1beta{
				Name:           "test-vault",
				BackupVaultID:  "generated-uuid",
				LifeCycleState: "READY",
			}, nil)

		origJSONMarshal := jsonMarshal
		t.Cleanup(func() { jsonMarshal = origJSONMarshal })
		jsonMarshal = func(_ any) ([]byte, error) {
			return nil, errors.New("marshal error")
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.Error(t, err)
		resp, ok := result.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		require.True(t, ok, "expected V1betaCreateBackupVaultInternalServerError")
		assert.Equal(t, float64(500), resp.Code)
		assert.Equal(t, "Failed to marshal Backup vault", resp.Message)
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestV1betaCreateBackupVaultWithImmutableBackups2(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	origGCBDRVaultEnabled := GCBDRVaultEnabled
	originalValue := utils.IsImmutableBackupEnabled()
	defer func() {
		backupEnabled = origBackupEnabled
		GCBDRVaultEnabled = origGCBDRVaultEnabled
		utils.SetImmutableBackupEnabledForTest(originalValue)
	}()
	backupEnabled = true
	GCBDRVaultEnabled = false
	utils.SetImmutableBackupEnabledForTest(true)

	t.Run("SuccessfulCreateWithImmutableDailyBackups", func(t *testing.T) {
		// Test successful creation with valid immutable daily backup settings

		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("test-backup-vault"),
			Description:  gcpgenserver.NewOptString("Test backup vault for immutable backups"),
			BackupRegion: gcpgenserver.NewOptString("us-central1-backup"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Mock successful CVP response
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:    nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID: "bv-uuid-123",
					Description:   nillable.GetStringPtr("Test backup vault for immutable backups"),
					BackupRegion:  nillable.GetStringPtr("us-central1-backup"),
					State:         "CREATING",
					StateDetails:  "Creation in progress",
					BackupRetentionPolicy: &models.BackupRetentionPolicyV1beta{
						BackupMinimumEnforcedRetentionDays: nillable.GetInt64Ptr(30),
						DailyBackupImmutable:               true,
						WeeklyBackupImmutable:              false,
						MonthlyBackupImmutable:             false,
						ManualBackupImmutable:              false,
					},
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.True(t, operationResult.Done.Value)
	})

	t.Run("FailureCreateWithInvalidRetentionPeriodForDailyImmutable", func(t *testing.T) {
		// Test validation failure when retention period exceeds limit for daily immutable backups
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-invalid"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("test-invalid-retention"),
			Description:  gcpgenserver.NewOptString("Invalid retention period"),
			BackupRegion: gcpgenserver.NewOptString("us-central1-backup"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(1500), // Exceeds daily limit
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err) // Handler returns error responses, not Go errors
		assert.NotNil(t, result)

		// Should return BadRequest due to validation failure
		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "Retention period")
	})

	t.Run("SuccessCreateWhenBackupVaultAlreadyExists", func(t *testing.T) {
		// Test successful response when backup vault with same name already exists
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-exists"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("existing-backup-vault"),
			Description: gcpgenserver.NewOptString("Already exists"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		existingVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "existing-uuid",
			Name:          "existing-backup-vault",
		}

		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "existing-backup-vault", "test-project-123").Return(existingVault, nil)

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Should return OperationV1beta when backup vault already exists (idempotent behavior)
		operationResult, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(t, ok, "Expected OperationV1beta response type when backup vault already exists")
		assert.True(t, operationResult.Done.Value, "Expected operation to be done")
	})

	t.Run("SuccessfulCreateWithBoundaryRetentionValues", func(t *testing.T) {
		// Test creation with boundary retention values (exactly at limits)
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-boundary"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("test-boundary-values"),
			Description:  gcpgenserver.NewOptString("Boundary retention values"),
			BackupRegion: gcpgenserver.NewOptString("us-central1-backup"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(1000), // Exactly at daily limit
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		vaultName := "test-boundary-values"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-boundary-values", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Name: "operations/backup-vault-create-boundary",
				Response: &models.BackupVaultV1beta{
					ResourceID:    nillable.GetStringPtr("test-boundary-values"),
					BackupVaultID: "bv-uuid-boundary",
					BackupRetentionPolicy: &models.BackupRetentionPolicyV1beta{
						BackupMinimumEnforcedRetentionDays: nillable.GetInt64Ptr(1000),
						DailyBackupImmutable:               true,
					},
				},
			},
		}

		mockClient.EXPECT().V1betaCreateBackupVault(mock.Anything).Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(t, "operations/backup-vault-create-boundary", operationResult.Name.Value)
	})

	t.Run("CreateBackupVaultBackupDisabled", func(t *testing.T) {
		// Test creation when backup feature is disabled globally
		origBackupEnabledLocal := backupEnabled
		backupEnabled = false
		defer func() { backupEnabled = origBackupEnabledLocal }()

		handler := Handler{}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-disabled"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("disabled-feature-vault"),
			Description: gcpgenserver.NewOptString("Backup feature disabled"),
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Should return BadRequest when feature is disabled
		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok)
		assert.Contains(t, badRequestResult.Message, "not enabled")
	})
}

// TestV1betaUpdateBackupVaultWithImmutableBackups tests backup vault updates with immutable backup scenarios
func TestV1betaUpdateBackupVaultWithImmutableBackups2(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	originalValue := utils.IsImmutableBackupEnabled()
	defer func() {
		backupEnabled = origBackupEnabled
		utils.SetImmutableBackupEnabledForTest(originalValue)
	}()
	backupEnabled = true
	utils.SetImmutableBackupEnabledForTest(true)

	t.Run("SuccessfulUpdateEnableImmutableBackups", func(t *testing.T) {
		// Test enabling immutable backups on existing vault
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			BackupVaultId:  "bv-uuid-123",
			XCorrelationID: gcpgenserver.NewOptString("correlation-update-enable"),
		}

		req := &gcpgenserver.BackupVaultUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated with immutable backups"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(45),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock existing backup vault without immutable backups
		existingVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "bv-uuid-123",
			Name:          "test-vault",
			OwnerID:       "test-project-123",
			AccountName:   "test-project-123",
			BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
				BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
				IsDailyBackupImmutable:                 false,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-uuid-123", "test-project-123").Return(existingVault, nil)
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", mock.Anything, "bv-uuid-123", "test-project-123").Return([]string{}, nil)

		updatedVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "bv-uuid-123",
			Name:          "test-vault",
			OwnerID:       "test-project-123",
			AccountName:   "test-project-123",
			Description:   nillable.GetStringPtr("Updated with immutable backups"),
			BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
				BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(45),
				IsDailyBackupImmutable:                 true,
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               true,
				IsAdhocBackupImmutable:                 false,
			},
		}

		mockOrchestrator.On("UpdateBackupVault", mock.Anything, mock.MatchedBy(func(params *commonparams.BackupVaultParams) bool {
			return params.BackupVaultID == "bv-uuid-123" &&
				*params.BackupRetentionPolicy.BackupMinimumEnforcedRetentionDuration == 45 &&
				*params.BackupRetentionPolicy.IsDailyBackupImmutable == true &&
				*params.BackupRetentionPolicy.IsMonthlyBackupImmutable == true
		})).Return(updatedVault, "operation-123", nil)

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.NotEmpty(t, operationResult.Name.Value)
	})

	t.Run("FailureUpdateAttemptToDisableImmutableBackups", func(t *testing.T) {
		// Test that attempting to disable immutable backups fails
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			BackupVaultId:  "bv-uuid-immutable",
			XCorrelationID: gcpgenserver.NewOptString("correlation-disable-fail"),
		}

		req := &gcpgenserver.BackupVaultUpdateV1beta{
			Description: gcpgenserver.NewOptString("Attempt to disable immutable"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(20),     // Attempting to reduce
				DailyBackupImmutable:               gcpgenserver.NewOptBool(false), // Attempting to disable
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock existing vault with immutable backups enabled
		existingVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "bv-uuid-immutable",
			Name:          "immutable-vault",
			OwnerID:       "test-project-123",
			BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
				BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(60),
				IsDailyBackupImmutable:                 true, // Currently enabled
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               true, // Currently enabled
				IsAdhocBackupImmutable:                 false,
			},
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-uuid-immutable", "test-project-123").Return(existingVault, nil)

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Should return BadRequest due to validation failure
		badRequestResult, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultBadRequest)
		assert.True(t, ok)
		assert.Contains(t, badRequestResult.Message, "cannot")
	})

	t.Run("FailureUpdateWithInvalidInputValidation", func(t *testing.T) {
		// Test update failure when validation fails due to invalid input
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			BackupVaultId:  "bv-uuid-validation-fail",
			XCorrelationID: gcpgenserver.NewOptString("correlation-validation-fail"),
		}

		req := &gcpgenserver.BackupVaultUpdateV1beta{
			Description: gcpgenserver.NewOptString("Invalid retention update"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(5),      // Too low for immutable vault
				DailyBackupImmutable:               gcpgenserver.NewOptBool(false), // Trying to disable
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock existing vault with immutable backups that can't be reduced
		existingVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "bv-uuid-validation-fail",
			Name:          "validation-fail-vault",
			OwnerID:       "test-project-123",
			BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
				BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
				IsDailyBackupImmutable:                 true, // Currently enabled
				IsWeeklyBackupImmutable:                false,
				IsMonthlyBackupImmutable:               false,
				IsAdhocBackupImmutable:                 false,
			},
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-uuid-validation-fail", "test-project-123").Return(existingVault, nil)

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultBadRequest)
		assert.True(t, ok)
		assert.Contains(t, badRequestResult.Message, "cannot")
	})

	t.Run("UpdateBackupVaultBackupDisabled", func(t *testing.T) {
		// Test update when backup feature is disabled globally
		origBackupEnabledLocal := backupEnabled
		backupEnabled = false
		defer func() { backupEnabled = origBackupEnabledLocal }()

		handler := Handler{}
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			BackupVaultId:  "bv-uuid-disabled",
			XCorrelationID: gcpgenserver.NewOptString("correlation-disabled-update"),
		}

		req := &gcpgenserver.BackupVaultUpdateV1beta{
			Description: gcpgenserver.NewOptString("Update with feature disabled"),
		}

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultBadRequest)
		assert.True(t, ok)
		assert.Contains(t, badRequestResult.Message, "not enabled")
	})
}

// TestV1betaImmutableBackupEdgeCases tests various edge cases and error conditions
func TestV1betaImmutableBackupEdgeCases2(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	originalValue := utils.IsImmutableBackupEnabled()
	defer func() {
		backupEnabled = origBackupEnabled
		utils.SetImmutableBackupEnabledForTest(originalValue)
	}()
	backupEnabled = true
	utils.SetImmutableBackupEnabledForTest(true)

	t.Run("CreateWithAllImmutableTypesEnabled", func(t *testing.T) {
		// Test creation with all backup types as immutable
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-all-immutable"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("all-immutable-vault"),
			Description:  gcpgenserver.NewOptString("All backup types immutable"),
			BackupRegion: gcpgenserver.NewOptString("us-central1-backup"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(60),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(true),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		vaultName := "all-immutable-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "all-immutable-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Name: "operations/backup-vault-create-all-immutable",
				Response: &models.BackupVaultV1beta{
					ResourceID:    nillable.GetStringPtr("all-immutable-vault"),
					BackupVaultID: "bv-uuid-all-immutable",
					BackupRetentionPolicy: &models.BackupRetentionPolicyV1beta{
						BackupMinimumEnforcedRetentionDays: nillable.GetInt64Ptr(60),
						DailyBackupImmutable:               true,
						WeeklyBackupImmutable:              true,
						MonthlyBackupImmutable:             true,
						ManualBackupImmutable:              true,
					},
				},
			},
		}

		mockClient.EXPECT().V1betaCreateBackupVault(mock.Anything).Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(t, "operations/backup-vault-create-all-immutable", operationResult.Name.Value)
	})

	t.Run("CreateWithMaximumRetentionPeriod", func(t *testing.T) {
		// Test creation with maximum allowed retention period
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-max-retention"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("max-retention-vault"),
			Description:  gcpgenserver.NewOptString("Maximum retention period"),
			BackupRegion: gcpgenserver.NewOptString("us-central1-backup"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(5475),   // Maximum allowed
				DailyBackupImmutable:               gcpgenserver.NewOptBool(false), // Not daily so can use max
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		vaultName := "max-retention-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "max-retention-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Name: "operations/backup-vault-create-max-retention",
				Response: &models.BackupVaultV1beta{
					ResourceID:    nillable.GetStringPtr("max-retention-vault"),
					BackupVaultID: "bv-uuid-max-retention",
					BackupRetentionPolicy: &models.BackupRetentionPolicyV1beta{
						BackupMinimumEnforcedRetentionDays: nillable.GetInt64Ptr(5475),
						WeeklyBackupImmutable:              true,
						MonthlyBackupImmutable:             true,
					},
				},
			},
		}

		mockClient.EXPECT().V1betaCreateBackupVault(mock.Anything).Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(t, "operations/backup-vault-create-max-retention", operationResult.Name.Value)
	})

	t.Run("FailureCreateWithExcessiveRetentionPeriod", func(t *testing.T) {
		// Test failure when retention period exceeds absolute maximum
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-excessive"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("excessive-retention-vault"),
			Description:  gcpgenserver.NewOptString("Excessive retention period"),
			BackupRegion: gcpgenserver.NewOptString("us-central1-backup"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(6000), // Exceeds maximum
				DailyBackupImmutable:               gcpgenserver.NewOptBool(false),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		// Should return BadRequest due to validation failure
		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "Retention period")
	})

	t.Run("UpdateWithPartialImmutableChanges", func(t *testing.T) {
		// Test updating some but not all immutable backup types
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			BackupVaultId:  "bv-uuid-partial",
			XCorrelationID: gcpgenserver.NewOptString("correlation-partial"),
		}

		req := &gcpgenserver.BackupVaultUpdateV1beta{
			Description: gcpgenserver.NewOptString("Partial immutable update"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				WeeklyBackupImmutable:  gcpgenserver.NewOptBool(true), // Enabling weekly
				MonthlyBackupImmutable: gcpgenserver.NewOptBool(true), // Enabling monthly
				// Not changing daily or manual
			}),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		existingVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "bv-uuid-partial",
			Name:          "partial-vault",
			OwnerID:       "test-project-123",
			AccountName:   "test-project-123",
			BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
				BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
				IsDailyBackupImmutable:                 true,  // Already enabled
				IsWeeklyBackupImmutable:                false, // Will be enabled
				IsMonthlyBackupImmutable:               false, // Will be enabled
				IsAdhocBackupImmutable:                 false, // Unchanged
			},
		}

		mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "bv-uuid-partial", "test-project-123").Return(existingVault, nil)
		mockOrchestrator.On("GetBackupPolicyUUIDsFromBackupVaultUUID", mock.Anything, "bv-uuid-partial", "test-project-123").Return([]string{}, nil)

		updatedVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "bv-uuid-partial",
			Name:          "partial-vault",
			OwnerID:       "test-project-123",
			AccountName:   "test-project-123",
			Description:   nillable.GetStringPtr("Partial immutable update"),
			BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
				BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
				IsDailyBackupImmutable:                 true,  // Unchanged
				IsWeeklyBackupImmutable:                true,  // Now enabled
				IsMonthlyBackupImmutable:               true,  // Now enabled
				IsAdhocBackupImmutable:                 false, // Unchanged
			},
		}

		mockOrchestrator.On("UpdateBackupVault", mock.Anything, mock.MatchedBy(func(params *commonparams.BackupVaultParams) bool {
			return params.BackupVaultID == "bv-uuid-partial" &&
				*params.BackupRetentionPolicy.IsWeeklyBackupImmutable == true &&
				*params.BackupRetentionPolicy.IsMonthlyBackupImmutable == true
		})).Return(updatedVault, "operation-partial", nil)

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.NotEmpty(t, operationResult.Name.Value)
	})
}

func Test_CreateBackupVaultV1beta(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenNotEnabled", func(t *testing.T) {
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "local",
			ProjectNumber: "project-number",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("vault1"),
			BackupRegion: gcpgenserver.NewOptString("invalid-region"), // Invalid region to trigger error
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(
				gcpgenserver.BackupRetentionPolicyV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
					DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
					WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
					MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				}),
		}
		handler := Handler{}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("ReturnsBadRequestWhenTenantProjectWithoutCrossProjectVault", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:    gcpgenserver.NewOptString("cross-project-vault"),
			TenantProject: gcpgenserver.NewOptString("tenant-project-123"),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "crossProjectVault must be set to true when tenantProject is provided")
	})
	t.Run("ReturnsBadRequestWhenCrossProjectVaultWithoutTenantProject", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:        gcpgenserver.NewOptString("cross-project-vault"),
			CrossProjectVault: gcpgenserver.NewOptBool(true),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "tenantProject is required when creating a cross-project backup vault")
	})
	t.Run("ReturnsBadRequestWhenCrossProjectDisabled", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = false

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:        gcpgenserver.NewOptString("cross-project-vault"),
			CrossProjectVault: gcpgenserver.NewOptBool(true),
			TenantProject:     gcpgenserver.NewOptString("tenant-project-123"),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "Cross-project backup vault creation is not enabled")
	})
	t.Run("ReturnsBadRequestWhenCrossProjectWithCMEK", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = true

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:            gcpgenserver.NewOptString("cross-project-cmek-vault"),
			CrossProjectVault:     gcpgenserver.NewOptBool(true),
			TenantProject:         gcpgenserver.NewOptString("tenant-project-123"),
			KmsConfigResourcePath: gcpgenserver.NewOptString("projects/p/locations/l/keyRings/r/cryptoKeys/k"),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "CMEK is not supported for cross-project backup vaults")
	})
	t.Run("ReturnsBadRequestWhenCrossProjectWithImmutable", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = true

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:        gcpgenserver.NewOptString("cross-project-immutable-vault"),
			CrossProjectVault: gcpgenserver.NewOptBool(true),
			TenantProject:     gcpgenserver.NewOptString("tenant-project-123"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(
				gcpgenserver.BackupRetentionPolicyV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
					DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				}),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "Immutable backup vaults are not supported for cross-project backup vaults")
	})
	t.Run("ReturnsBadRequestWhenRegionParsingFails", func(t *testing.T) {
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "local",
			ProjectNumber: "project-number",
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:   gcpgenserver.NewOptString("vault1"),
			BackupRegion: gcpgenserver.NewOptString("invalid-region"), // Invalid region to trigger error
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(
				gcpgenserver.BackupRetentionPolicyV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
					DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
					WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
					MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				}),
		}

		handler := Handler{}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	t.Run("ReturnsExistingBackupVaultWhenAlreadyExists", func(t *testing.T) {
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("existing-vault"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "1234567890",
		}
		desc := "New backup vault"
		minEnforcedRetentionDuration := int64(30)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "existing-vault", "1234567890").
			Return(&coremodels.BackupVaultV1beta{
				Name:        "existing-vault",
				Description: &desc,
				BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
					BackupMinimumEnforcedRetentionDuration: &minEnforcedRetentionDuration,
					IsDailyBackupImmutable:                 false,
					IsMonthlyBackupImmutable:               false,
					IsWeeklyBackupImmutable:                false,
					IsAdhocBackupImmutable:                 false,
				},
			}, nil)
		handler := Handler{Orchestrator: mockOrchestrator}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
		assert.True(t, result.(*gcpgenserver.OperationV1beta).Done.Value)
		assert.Equal(t, "operation-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("ReturnsInternalServerErrorWhenBackupVaultCheckFails", func(t *testing.T) {
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("vault1"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "1234567890",
		}
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1", nil
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "vault1", "1234567890").
			Return(nil, fmt.Errorf("unexpected error"))

		handler := Handler{Orchestrator: mockOrchestrator}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.Error(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupVaultInternalServerError{}, result)

		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenUseVCPRegionCreateSucceeds_ReturnsDoneOperationWithVaultPayload", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origUseVCPRegion := cvp.CVP_HOST
		origParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			backupEnabled = origBackupEnabled
			cvp.CVP_HOST = origUseVCPRegion
			parseAndValidateRegionAndZone = origParseAndValidateRegionAndZone
		}()
		backupEnabled = true
		cvp.CVP_HOST = ""
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		bvName := "new-vault"
		createdVault := &coremodels.BackupVaultV1beta{
			Name:          bvName,
			BackupVaultID: "bv-123",
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
		mockOrchestrator.On("CreateBackupVault", mock.Anything, mock.Anything).
			Return(createdVault, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		require.NotNil(t, result)
		op, ok := result.(*gcpgenserver.OperationV1beta)
		require.True(t, ok)
		assert.True(t, op.Done.IsSet())
		assert.True(t, op.Done.Value)
		assert.Equal(t, createdVault.Name, op.Name.Value)
		assert.NotNil(t, op.Response)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenUseVCPRegionCreateFails_ReturnsInternalServerError", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origUseVCPRegion := cvp.CVP_HOST
		origParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() {
			backupEnabled = origBackupEnabled
			cvp.CVP_HOST = origUseVCPRegion
			parseAndValidateRegionAndZone = origParseAndValidateRegionAndZone
		}()
		backupEnabled = true
		cvp.CVP_HOST = ""
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		bvName := "new-vault"
		createErr := fmt.Errorf("orchestrator create failed")

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
		mockOrchestrator.On("CreateBackupVault", mock.Anything, mock.Anything).
			Return(nil, createErr)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.Error(t, err)
		require.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		require.True(t, ok)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, createErr.Error(), internalErr.Message)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenUseVCPRegionMarshalFails_ReturnsInternalServerError", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origUseVCPRegion := cvp.CVP_HOST
		origParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		origJSONMarshal := jsonMarshal
		defer func() {
			backupEnabled = origBackupEnabled
			cvp.CVP_HOST = origUseVCPRegion
			parseAndValidateRegionAndZone = origParseAndValidateRegionAndZone
			jsonMarshal = origJSONMarshal
		}()
		backupEnabled = true
		cvp.CVP_HOST = ""
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		jsonMarshal = func(v interface{}) ([]byte, error) {
			return nil, fmt.Errorf("marshal failed")
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		bvName := "new-vault"

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
		mockOrchestrator.On("CreateBackupVault", mock.Anything, mock.Anything).
			Return(&coremodels.BackupVaultV1beta{Name: bvName}, nil)

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.Error(t, err)
		require.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		require.True(t, ok)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, "Failed to marshal Backup vault", internalErr.Message)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeBadRequestError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultBadRequest{
				Payload: &models.Error{
					Code:    400,
					Message: "SDE error: Invalid request",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Nil(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeUnprocessableError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}

		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultUnprocessableEntity{
				Payload: &models.Error{
					Code:    422,
					Message: "SDE error: Unprocessable Entity",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Nil(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeConflictError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}

		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultConflict{
				Payload: &models.Error{
					Code:    409,
					Message: "SDE error: Conflict",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Nil(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeUnAuthorizedError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultUnauthorized{
				Payload: &models.Error{
					Code:    401,
					Message: "SDE error: UnAuthorized",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Nil(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeForbiddenError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultForbidden{
				Payload: &models.Error{
					Code:    403,
					Message: "SDE error: Forbidden",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Nil(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeTooManyRequestError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultTooManyRequests{
				Payload: &models.Error{
					Code:    429,
					Message: "SDE error: TooManyRequest",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Nil(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeDefaultError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultDefault{
				Payload: &models.Error{
					Code:    500,
					Message: "SDE error: Default",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Nil(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSdeNotImplementedError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultNotImplemented{
				Payload: &models.Error{
					Code:    501,
					Message: "SDE error: NotImplemented",
				},
			})
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		assert.Nil(t, err)
		assert.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(501), internalErr.Code)
		assert.Equal(t, "SDE error: NotImplemented", internalErr.Message)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultConversionError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Define mock response
		mockResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Name: "operation-id",
				Done: nillable.GetBoolPtr(true),
			},
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
			utilsConvertJsonToModel = utils.ConvertJsonToModel
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		utilsConvertJsonToModel = func(data []byte, model interface{}) error {
			return fmt.Errorf("JSON conversion error")
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.Error(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCreatesBackupVaultSuccess", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId: gcpgenserver.NewOptString("new-vault"),
		}
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = false
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Define mock response
		mockResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Name: "operation-id",
				Done: nillable.GetBoolPtr(true),
			},
		}

		bvName := "new-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
			utilsConvertJsonToModel = utils.ConvertJsonToModel
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		utilsConvertJsonToModel = func(data []byte, model interface{}) error {
			return nil
		}

		handler := Handler{Orchestrator: mockOrchestrator}

		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		// Assert
		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
	t.Run("WhenCrossProjectVaultTrueButTenantProjectMissing_ReturnsBadRequest", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:        gcpgenserver.NewOptString("cross-project-vault-no-tenant"),
			CrossProjectVault: gcpgenserver.NewOptBool(true),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "tenantProject is required when creating a cross-project backup vault")
	})
	t.Run("WhenCrossProjectVaultTrueAndDisabled_ReturnsBadRequest", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = false

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:        gcpgenserver.NewOptString("cross-project-vault"),
			CrossProjectVault: gcpgenserver.NewOptBool(true),
			TenantProject:     gcpgenserver.NewOptString("tenant-project-123"),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "Cross-project backup vault creation is not enabled")
	})
	t.Run("WhenCrossProjectVaultTrueWithCMEK_ReturnsBadRequest", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = true

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:            gcpgenserver.NewOptString("cross-project-vault-cmek"),
			CrossProjectVault:     gcpgenserver.NewOptBool(true),
			TenantProject:         gcpgenserver.NewOptString("tenant-project-123"),
			KmsConfigResourcePath: gcpgenserver.NewOptString("projects/p/locations/l/keyRings/r/cryptoKeys/k"),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "CMEK is not supported for cross-project backup vaults")
	})
	t.Run("WhenCrossProjectVaultTrueWithImmutable_ReturnsBadRequest", func(t *testing.T) {
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = true

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-central1",
			ProjectNumber: "test-project-123",
		}
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:        gcpgenserver.NewOptString("cross-project-vault-immutable"),
			CrossProjectVault: gcpgenserver.NewOptBool(true),
			TenantProject:     gcpgenserver.NewOptString("tenant-project-123"),
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyV1beta(
				gcpgenserver.BackupRetentionPolicyV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
					DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				}),
		}

		handler := Handler{}
		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)

		badRequestResult, ok := result.(*gcpgenserver.V1betaCreateBackupVaultBadRequest)
		assert.True(t, ok, "Expected BadRequest response type")
		assert.Contains(t, badRequestResult.Message, "Immutable backup vaults are not supported for cross-project backup vaults")
	})
	t.Run("WhenCrossProjectVaultFalse_DoesNotCreateCrossProjectVault", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:        gcpgenserver.NewOptString("regular-vault"),
			CrossProjectVault: gcpgenserver.NewOptBool(false),
		}
		origBackupEnabled := backupEnabled
		origGCBDRVaultEnabled := GCBDRVaultEnabled
		defer func() {
			backupEnabled = origBackupEnabled
			GCBDRVaultEnabled = origGCBDRVaultEnabled
		}()
		backupEnabled = true
		GCBDRVaultEnabled = true
		params := gcpgenserver.V1betaCreateBackupVaultParams{
			LocationId:    "us-east4",
			ProjectNumber: "1234567890",
		}
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		bvResponse := &models.BackupVaultV1beta{
			BackupVaultID: "bv-uuid-regular",
			ResourceID:    nillable.GetStringPtr("regular-vault"),
			State:         "READY",
			StateDetails:  "Available for use",
		}
		mockResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Name:     "operation-id",
				Done:     nillable.GetBoolPtr(true),
				Response: bvResponse,
			},
		}
		bvName := "regular-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, bvName, "1234567890").
			Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
		// CreateBackupVaultEntryInVCPFromCVP should NOT be called for regular vaults
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
			utilsConvertJsonToModel = utils.ConvertJsonToModel
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		utilsConvertJsonToModel = func(data []byte, model interface{}) error {
			return utils.ConvertJsonToModel(data, model)
		}
		handler := Handler{Orchestrator: mockOrchestrator}
		ctx := context.Background()
		result, err := handler.V1betaCreateBackupVault(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		opResult, ok := result.(*gcpgenserver.OperationV1beta)
		require.True(t, ok, "expected OperationV1beta response")
		var responseMap map[string]interface{}
		require.NoError(t, utils.ConvertJsonToModel(opResult.Response, &responseMap))
		// crossProjectVault should not be set for regular vaults
		_, hasCrossProjectVault := responseMap["crossProjectVault"]
		assert.False(t, hasCrossProjectVault, "crossProjectVault should not be set for regular vaults")
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestConvertBackupRetentionPolicyToCvpModelForCreate(t *testing.T) {
	t.Run("ReturnsNilWhenPolicyIsNotSet", func(t *testing.T) {
		brPolicy := gcpgenserver.OptBackupRetentionPolicyV1beta{}
		result := convertBackupRetentionPolicyToCvpModelForCreate(brPolicy)
		assert.Nil(t, result)
	})

	t.Run("ReturnsModelWhenAllFieldsAreSet", func(t *testing.T) {
		brPolicy := gcpgenserver.OptBackupRetentionPolicyV1beta{
			Value: gcpgenserver.BackupRetentionPolicyV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
			},
			Set: true,
		}
		result := convertBackupRetentionPolicyToCvpModelForCreate(brPolicy)
		assert.NotNil(t, result)
		assert.Equal(t, int64(30), *result.BackupMinimumEnforcedRetentionDays)
		assert.True(t, result.DailyBackupImmutable)
		assert.False(t, result.ManualBackupImmutable)
		assert.True(t, result.MonthlyBackupImmutable)
		assert.False(t, result.WeeklyBackupImmutable)
	})

	t.Run("ReturnsModelWhenSomeFieldsAreUnset", func(t *testing.T) {
		brPolicy := gcpgenserver.OptBackupRetentionPolicyV1beta{
			Value: gcpgenserver.BackupRetentionPolicyV1beta{
				DailyBackupImmutable: gcpgenserver.NewOptBool(true),
			},
			Set: true,
		}
		result := convertBackupRetentionPolicyToCvpModelForCreate(brPolicy)
		assert.NotNil(t, result)
		assert.Nil(t, result.BackupMinimumEnforcedRetentionDays)
		assert.True(t, result.DailyBackupImmutable)
		assert.False(t, result.ManualBackupImmutable)
		assert.False(t, result.MonthlyBackupImmutable)
		assert.False(t, result.WeeklyBackupImmutable)
	})
}

func TestV1betaCreateBackupVaultWithKmsConfigResourcePathAndBackupsPrimaryKeyVersion(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	origGCBDRVaultEnabled := GCBDRVaultEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		GCBDRVaultEnabled = origGCBDRVaultEnabled
	}()
	backupEnabled = true
	GCBDRVaultEnabled = false

	t.Run("CreateWithBothKmsFieldsSet", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		kmsConfigPath := "projects/test-project-123/locations/us-central1/kmsConfigs/test-kms-config"
		backupsPrimaryKeyVersion := "projects/test-project-123/locations/us-central1/kmsConfigs/test-kms-config/cryptoKeys/test-key/cryptoKeyVersions/1"

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:               gcpgenserver.NewOptString("test-backup-vault"),
			Description:              gcpgenserver.NewOptString("Test backup vault with KMS fields"),
			KmsConfigResourcePath:    gcpgenserver.NewOptString(kmsConfigPath),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString(backupsPrimaryKeyVersion),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Mock successful CVP response
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:               nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:            "bv-uuid-123",
					Description:              nillable.GetStringPtr("Test backup vault with KMS fields"),
					KmsConfigResourcePath:    nillable.GetStringPtr(kmsConfigPath),
					BackupsPrimaryKeyVersion: nillable.GetStringPtr(backupsPrimaryKeyVersion),
					State:                    "CREATING",
					StateDetails:             "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault" &&
					params.Body.KmsConfigResourcePath != nil &&
					*params.Body.KmsConfigResourcePath == kmsConfigPath &&
					params.Body.BackupsPrimaryKeyVersion != nil &&
					*params.Body.BackupsPrimaryKeyVersion == backupsPrimaryKeyVersion
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("CreateWithOnlyKmsConfigResourcePathSet", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		kmsConfigPath := "projects/test-project-123/locations/us-central1/kmsConfigs/test-kms-config"

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:            gcpgenserver.NewOptString("test-backup-vault"),
			Description:           gcpgenserver.NewOptString("Test backup vault with KMS config only"),
			KmsConfigResourcePath: gcpgenserver.NewOptString(kmsConfigPath),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Mock successful CVP response
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:               nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:            "bv-uuid-123",
					Description:              nillable.GetStringPtr("Test backup vault with KMS config only"),
					KmsConfigResourcePath:    nillable.GetStringPtr(kmsConfigPath),
					BackupsPrimaryKeyVersion: nil,
					State:                    "CREATING",
					StateDetails:             "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault" &&
					params.Body.KmsConfigResourcePath != nil &&
					*params.Body.KmsConfigResourcePath == kmsConfigPath &&
					params.Body.BackupsPrimaryKeyVersion == nil
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("CreateWithOnlyBackupsPrimaryKeyVersionSet", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		backupsPrimaryKeyVersion := "projects/test-project-123/locations/us-central1/kmsConfigs/test-kms-config/cryptoKeys/test-key/cryptoKeyVersions/1"

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:               gcpgenserver.NewOptString("test-backup-vault"),
			Description:              gcpgenserver.NewOptString("Test backup vault with primary key version only"),
			BackupsPrimaryKeyVersion: gcpgenserver.NewOptString(backupsPrimaryKeyVersion),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Mock successful CVP response
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:               nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:            "bv-uuid-123",
					Description:              nillable.GetStringPtr("Test backup vault with primary key version only"),
					KmsConfigResourcePath:    nil,
					BackupsPrimaryKeyVersion: nillable.GetStringPtr(backupsPrimaryKeyVersion),
					State:                    "CREATING",
					StateDetails:             "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault" &&
					params.Body.KmsConfigResourcePath == nil &&
					params.Body.BackupsPrimaryKeyVersion != nil &&
					*params.Body.BackupsPrimaryKeyVersion == backupsPrimaryKeyVersion
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestV1betaCreateBackupVaultWithEncryptionState(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	origGCBDRVaultEnabled := GCBDRVaultEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		GCBDRVaultEnabled = origGCBDRVaultEnabled
	}()
	backupEnabled = true
	GCBDRVaultEnabled = false

	t.Run("CreateWithEncryptionStatePending", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("Test backup vault with encryption state"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		encryptionState := "ENCRYPTION_STATE_PENDING"
		// Mock successful CVP response with encryption state
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:      nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:   "bv-uuid-123",
					Description:     nillable.GetStringPtr("Test backup vault with encryption state"),
					EncryptionState: nillable.GetStringPtr(encryptionState),
					State:           "CREATING",
					StateDetails:    "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("CreateWithEncryptionStateCompleted", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("Test backup vault with encryption completed"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		encryptionState := "ENCRYPTION_STATE_COMPLETED"
		// Mock successful CVP response with encryption state
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:      nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:   "bv-uuid-123",
					Description:     nillable.GetStringPtr("Test backup vault with encryption completed"),
					EncryptionState: nillable.GetStringPtr(encryptionState),
					State:           "CREATING",
					StateDetails:    "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("CreateWithEncryptionStateInProgress", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("Test backup vault with encryption in progress"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		encryptionState := "ENCRYPTION_STATE_IN_PROGRESS"
		// Mock successful CVP response with encryption state
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:      nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:   "bv-uuid-123",
					Description:     nillable.GetStringPtr("Test backup vault with encryption in progress"),
					EncryptionState: nillable.GetStringPtr(encryptionState),
					State:           "CREATING",
					StateDetails:    "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("CreateWithEncryptionStateFailed", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("Test backup vault with encryption failed"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		encryptionState := "ENCRYPTION_STATE_FAILED"
		// Mock successful CVP response with encryption state
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:      nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:   "bv-uuid-123",
					Description:     nillable.GetStringPtr("Test backup vault with encryption failed"),
					EncryptionState: nillable.GetStringPtr(encryptionState),
					State:           "CREATING",
					StateDetails:    "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("CreateWithNoEncryptionState", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("Test backup vault without encryption state"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Mock successful CVP response without encryption state
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:      nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID:   "bv-uuid-123",
					Description:     nillable.GetStringPtr("Test backup vault without encryption state"),
					EncryptionState: nil,
					State:           "CREATING",
					StateDetails:    "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestV1betaCreateBackupVaultWithKmsGrant(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	origGCBDRVaultEnabled := GCBDRVaultEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		GCBDRVaultEnabled = origGCBDRVaultEnabled
	}()
	backupEnabled = true
	GCBDRVaultEnabled = false

	t.Run("CreateWithKmsGrantSet", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("Test backup vault with KMS grant"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Mock successful CVP response
		// Note: KmsGrant is not in the CVP client model, it's only in the API response model
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:    nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID: "bv-uuid-123",
					Description:   nillable.GetStringPtr("Test backup vault with KMS grant"),
					State:         "CREATING",
					StateDetails:  "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})

	t.Run("CreateWithNoKmsGrant", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaCreateBackupVaultParams{
			ProjectNumber:  "test-project-123",
			LocationId:     "us-central1",
			XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
		}

		req := &gcpgenserver.BackupVaultCreateV1beta{
			ResourceId:  gcpgenserver.NewOptString("test-backup-vault"),
			Description: gcpgenserver.NewOptString("Test backup vault without KMS grant"),
		}

		// Mock region parsing
		originalParseAndValidateRegionAndZone := parseAndValidateRegionAndZone
		defer func() { parseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		parseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "", nil
		}

		// Mock orchestrator calls - backup vault doesn't exist
		vaultName := "test-backup-vault"
		mockOrchestrator.On("GetBackupVaultByNameAndOwnerID", mock.Anything, "test-backup-vault", "test-project-123").Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))

		// Mock successful CVP response
		// Note: KmsGrant is not in the CVP client model, it's only in the API response model
		cvpResponse := &backup_vault.V1betaCreateBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Response: &models.BackupVaultV1beta{
					ResourceID:    nillable.GetStringPtr("test-backup-vault"),
					BackupVaultID: "bv-uuid-123",
					Description:   nillable.GetStringPtr("Test backup vault without KMS grant"),
					State:         "CREATING",
					StateDetails:  "Creation in progress",
				},
			},
		}

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(params *backup_vault.V1betaCreateBackupVaultParams) bool {
				return params.LocationID == "us-central1" &&
					params.ProjectNumber == "test-project-123" &&
					params.Body.ResourceID == "test-backup-vault"
			})).
			Return(cvpResponse, nil)

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCvpCreateClient := sde.BackupVaultCreateClient
		defer func() { sde.BackupVaultCreateClient = originalCvpCreateClient }()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := handler.V1betaCreateBackupVault(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		mockOrchestrator.AssertExpectations(t)
	})
}

func TestV1betaUpdateBackupVaultNotEnabled(t *testing.T) {
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "", "", &gcpgenserver.Error{Code: 400, Message: "LocationID represents neither a region nor a zone"}
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaUpdateBackupVaultReturnsInvalidLocation(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "", "", &gcpgenserver.Error{Code: 400, Message: "LocationID represents neither a region nor a zone"}
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaUpdateBackupVaultReturnsNotFound(t *testing.T) {
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
	}()
	backupEnabled = true
	cvp.CVP_HOST = "http://cvp-host"
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}
	updateBackupVaultInSDE = func(ctx context.Context, req *gcpgenserver.BackupVaultUpdateV1beta, params gcpgenserver.V1betaUpdateBackupVaultParams, description string) (r gcpgenserver.V1betaUpdateBackupVaultRes, _ error) {
		return nil, errors2.NewBadRequestErr("Update failed in SDE")
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestV1betaUpdateBackupVaultReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}
	updateBackupVaultInSDE = func(ctx context.Context, req *gcpgenserver.BackupVaultUpdateV1beta, params gcpgenserver.V1betaUpdateBackupVaultParams, description string) (r gcpgenserver.V1betaUpdateBackupVaultRes, _ error) {
		return nil, errors2.NewBadRequestErr("Update failed in SDE")
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(nil, errors2.NewConflictErr("Backup vault already exists"))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaUpdateBackupVaultReturnsNotFoundSDESuccessful(t *testing.T) {
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
	}()
	backupEnabled = true
	cvp.CVP_HOST = "http://cvp-host"
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}
	updateBackupVaultInSDE = func(ctx context.Context, req *gcpgenserver.BackupVaultUpdateV1beta, params gcpgenserver.V1betaUpdateBackupVaultParams, description string) (r gcpgenserver.V1betaUpdateBackupVaultRes, _ error) {
		return &gcpgenserver.OperationV1beta{}, nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaUpdateBackupVaultReturnsNotFoundWhenUseVCPRegion(t *testing.T) {
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
	}()
	backupEnabled = true
	cvp.CVP_HOST = ""

	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, bvName, "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

	handler := Handler{Orchestrator: mockOrchestrator}
	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	require.NoError(t, err)
	require.NotNil(t, result)
	esc, ok := result.(*gcpgenserver.ErrorStatusCode)
	require.True(t, ok)
	assert.Equal(t, 404, esc.StatusCode)
	assert.Equal(t, float64(404), esc.Response.Code)
	assert.Equal(t, "Backup vault not found", esc.Response.Message)
	mockOrchestrator.AssertExpectations(t)
}

func TestV1betaUpdateBackupVaultReturnsFoundWithbackupVaultSuccessful(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{
		Description: gcpgenserver.NewOptString("desc"),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
			gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
			},
		),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	// Mock IsBackupVaultAttachedToVolume since this test has backup retention policy update
	mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
		Return(false, nil)

	mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
		Return(&coremodels.BackupVaultV1beta{}, "operation-id", nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
	assert.Equal(t, "/v1beta/projects/1234567890/locations/valid-location/operations/operation-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
}

func TestV1betaUpdateBackupVaultReturnsFoundWithbackupVaultSuccessfulWithNoOperation(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{
		Description: gcpgenserver.NewOptString("desc"),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
			gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
			},
		),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	// Mock IsBackupVaultAttachedToVolume since this test has backup retention policy update
	mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
		Return(false, nil)

	mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
		Return(&coremodels.BackupVaultV1beta{}, "", nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
	assert.Equal(t, "", result.(*gcpgenserver.OperationV1beta).Name.Value)
}

func TestV1betaUpdateBackupVaultReturnsFoundWithbackupVaultJsonFails(t *testing.T) {
	origBackupEnabled := backupEnabled
	origJSONMarshal := jsonMarshal
	defer func() {
		backupEnabled = origBackupEnabled
		jsonMarshal = origJSONMarshal
	}()
	backupEnabled = true
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{
		Description: gcpgenserver.NewOptString("desc"),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
			gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(false),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
			},
		),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	// Mock IsBackupVaultAttachedToVolume since this test has backup retention policy update
	mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
		Return(false, nil)

	mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
		Return(&coremodels.BackupVaultV1beta{}, "operation-id", nil)

	jsonMarshal = func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("JSON marshal error")
	}

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaUpdateBackupVaultReturnsFoundWithbackupVaultFails(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{
		Description: gcpgenserver.NewOptString("desc"),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
			gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				DailyBackupImmutable:   gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable: gcpgenserver.NewOptBool(false),
			},
		),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	// Mock IsBackupVaultAttachedToVolume since this test has backup retention policy update
	mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
		Return(false, nil)

	mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
		Return(nil, "", errors2.NewBadRequestErr("Update failed in SDE"))

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaUpdateBackupVaultReturnsFoundWithBackupVaultFailsWithBadRequest(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultUpdateV1beta{
		Description: gcpgenserver.NewOptString("desc"),
		BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
			gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				DailyBackupImmutable:   gcpgenserver.NewOptBool(true),
				MonthlyBackupImmutable: gcpgenserver.NewOptBool(false),
			},
		),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	// Mock IsBackupVaultAttachedToVolume since this test has backup retention policy update
	mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
		Return(false, nil)

	mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
		Return(nil, "", errors2.NewUserInputValidationErr("Update failed in SDE"))

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestConvertBackupRetentionPolicyToCvpModelForUpdate(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("ReturnsNilWhenPolicyIsNotSet", func(t *testing.T) {
		brPolicy := gcpgenserver.OptBackupRetentionPolicyUpdateV1beta{}
		result := convertBackupRetentionPolicyToCvpModelForUpdate(brPolicy)
		assert.Nil(t, result)
	})
	t.Run("ReturnsModelWhenAllFieldsAreSet", func(t *testing.T) {
		brPolicy := gcpgenserver.OptBackupRetentionPolicyUpdateV1beta{
			Value: gcpgenserver.BackupRetentionPolicyUpdateV1beta{
				BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
				ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
				MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				WeeklyBackupImmutable:              gcpgenserver.NewOptBool(false),
			},
			Set: true,
		}
		result := convertBackupRetentionPolicyToCvpModelForUpdate(brPolicy)
		assert.NotNil(t, result)
		assert.Equal(t, int64(30), *result.BackupMinimumEnforcedRetentionDays)
		assert.True(t, *result.DailyBackupImmutable)
		assert.False(t, *result.ManualBackupImmutable)
		assert.True(t, *result.MonthlyBackupImmutable)
		assert.False(t, *result.WeeklyBackupImmutable)
	})
}

func Test_updateBackupVaultInSDE(tt *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	tt.Run("WhenSuccessful", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
					DailyBackupImmutable:               gcpgenserver.NewOptBool(true),
					ManualBackupImmutable:              gcpgenserver.NewOptBool(false),
					MonthlyBackupImmutable:             gcpgenserver.NewOptBool(true),
				}),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockResp := &models.OperationV1beta{Name: "operation-id", Done: nillable.GetBoolPtr(true)}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			&backup_vault.V1betaUpdateBackupVaultAccepted{
				Payload: mockResp,
			}, nil).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenBadRequest", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultBadRequest{
			Payload: &models.Error{
				Code:    400,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenDefaultError", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultInternalServerError{
			Payload: &models.Error{
				Code:    500,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUnprocessableEntity", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultUnprocessableEntity{
			Payload: &models.Error{
				Code:    503,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenConflict", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultConflict{
			Payload: &models.Error{
				Code:    409,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUpdateBackupVaultUnauthorized", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultUnauthorized{
			Payload: &models.Error{
				Code:    501,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUpdateBackupVaultForbidden", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultForbidden{
			Payload: &models.Error{
				Code:    403,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUpdateBackupVaultTooManyRequests", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultTooManyRequests{
			Payload: &models.Error{
				Code:    429,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUpdateBackupVaultDefault", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultDefault{
			Payload: &models.Error{
				Code:    500,
				Message: "SDE error: Invalid request",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUpdateBackupVaultNotImplemented", func(t *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-id",
		}
		req := &gcpgenserver.BackupVaultUpdateV1beta{}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaUpdateBackupVaultNotImplemented{
			Payload: &models.Error{
				Code:    501,
				Message: "SDE error: Not Implemented",
			},
		}

		mockClient.On("V1betaUpdateBackupVault", mock.Anything).Return(
			nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() {
			createClient = originalCreateClient
		}()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		result, err := _updateBackupVaultInSDE(context.Background(), req, params, "description")
		assert.NoError(t, err)
		assert.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(501), internalErr.Code)
		assert.Equal(t, "SDE error: Not Implemented", internalErr.Message)
	})
}

func TestReturnsSuccessfulDeletion(tt *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	tt.Run("WhenSuccessful", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockResp := &models.OperationV1beta{Name: "operation-id", Done: nillable.GetBoolPtr(true)}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			&backup_vault.V1betaDeleteBackupVaultAccepted{
				Payload: mockResp,
			}, nil, nil).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenNotFoundError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultNotFound{
			Payload: &models.Error{
				Code:    400,
				Message: "Backup vault not found",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenForbiddenError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultForbidden{
			Payload: &models.Error{
				Code:    403,
				Message: "forbidden",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenBadRequestError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultBadRequest{
			Payload: &models.Error{
				Code:    400,
				Message: "Invalid request",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUnauthorizedError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultUnauthorized{
			Payload: &models.Error{
				Code:    401,
				Message: "Unauthorized access",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenTooManyRequestsError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultTooManyRequests{
			Payload: &models.Error{
				Code:    429,
				Message: "Too many requests",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenConflictError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultConflict{
			Payload: &models.Error{
				Code:    409,
				Message: "Conflict error",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenUnprocessableEntityError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultUnprocessableEntity{
			Payload: &models.Error{
				Code:    422,
				Message: "Unprocessable entity",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenDeleteBackupVaultDefault", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultDefault{
			Payload: &models.Error{
				Code:    500,
				Message: "Internal server error",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
	})
	tt.Run("WhenNotImplementedError", func(t *testing.T) {
		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "valid-location",
			ProjectNumber:  "1234567890",
			BackupVaultId:  "vault-id",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		mockClient := backup_vault.NewMockClientService(t)
		mockError := &backup_vault.V1betaDeleteBackupVaultNotImplemented{
			Payload: &models.Error{
				Code:    501,
				Message: "Not implemented",
			},
		}

		mockClient.On("V1betaDeleteBackupVault", mock.Anything).Return(
			nil, nil, mockError).Once()

		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := sde.BackupVaultCreateClient
		defer func() {
			sde.BackupVaultCreateClient = originalCreateClient
		}()
		sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		logger := log.NewLogger()
		result, err := _deleteBackupVaultInSDE(context.Background(), params, logger)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		internalErr, ok := result.(*gcpgenserver.V1betaDeleteBackupVaultInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(501), internalErr.Code)
		assert.Equal(t, "Not implemented", internalErr.Message)
	})
}

func TestV1betaDeleteBackupVaultReturnsInvalidLocation(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "", "", &gcpgenserver.Error{Code: 400, Message: "LocationID represents neither a region nor a zone"}
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaDeleteBackupVaultNotEnabled(t *testing.T) {
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "", "", &gcpgenserver.Error{Code: 400, Message: "LocationID represents neither a region nor a zone"}
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaDeleteBackupVaultReturnsNotFound(t *testing.T) {
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
	}()
	backupEnabled = true
	cvp.CVP_HOST = "http://cvp-host"
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}
	deleteBackupVaultInSDE = func(ctx context.Context, params gcpgenserver.V1betaDeleteBackupVaultParams, logger log.Logger) (r gcpgenserver.V1betaDeleteBackupVaultRes, _ error) {
		return nil, errors2.NewNotFoundErr("backup vault", &params.BackupVaultId)
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestV1betaDeleteBackupVaultReturnsError(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	deleteBackupVaultInSDE = func(ctx context.Context, params gcpgenserver.V1betaDeleteBackupVaultParams, logger log.Logger) (r gcpgenserver.V1betaDeleteBackupVaultRes, _ error) {
		return nil, errors2.NewBadRequestErr("Update failed in SDE")
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(nil, errors2.NewConflictErr("Backup vault already exists"))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaDeleteBackupVaultReturnsNotFoundSDESuccessful(t *testing.T) {
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
	}()
	backupEnabled = true
	cvp.CVP_HOST = "http://cvp-host"
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	deleteBackupVaultInSDE = func(ctx context.Context, params gcpgenserver.V1betaDeleteBackupVaultParams, logger log.Logger) (r gcpgenserver.V1betaDeleteBackupVaultRes, _ error) {
		return &gcpgenserver.OperationV1beta{}, nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaDeleteBackupVaultReturnsNotFoundWhenUseVCPRegion(t *testing.T) {
	origBackupEnabled := backupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	defer func() {
		backupEnabled = origBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
	}()
	backupEnabled = true
	cvp.CVP_HOST = ""

	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, bvName, "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

	handler := Handler{Orchestrator: mockOrchestrator}
	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	require.NoError(t, err)
	require.NotNil(t, result)
	notFound, ok := result.(*gcpgenserver.V1betaDeleteBackupVaultNotFound)
	require.True(t, ok)
	assert.Equal(t, float64(404), notFound.Code)
	assert.Equal(t, "Backup vault not found", notFound.Message)
	mockOrchestrator.AssertExpectations(t)
}

func TestV1betaDeleteBackupVaultReturnsFoundWithbackupVaultSuccessful(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	mockOrchestrator.On("DeleteBackupVault", ctx, mock.Anything).
		Return(&coremodels.BackupVaultV1beta{}, "operation-id", nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
	assert.Equal(t, "/v1beta/projects/1234567890/locations/valid-location/operations/operation-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
}

func TestV1betaDeleteBackupVaultReturnsFoundWithbackupVaultSuccessfulWithNoOperation(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	mockOrchestrator.On("DeleteBackupVault", ctx, mock.Anything).
		Return(&coremodels.BackupVaultV1beta{}, "", nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
	assert.Equal(t, "", result.(*gcpgenserver.OperationV1beta).Name.Value)
}

func TestV1betaDeleteBackupVaultReturnsFoundWithbackupVaultJsonFails(t *testing.T) {
	origBackupEnabled := backupEnabled
	origJSONMarshal := jsonMarshal
	defer func() {
		backupEnabled = origBackupEnabled
		jsonMarshal = origJSONMarshal
	}()
	backupEnabled = true
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	mockOrchestrator.On("DeleteBackupVault", ctx, mock.Anything).
		Return(&coremodels.BackupVaultV1beta{}, "operation-id", nil)

	jsonMarshal = func(v interface{}) ([]byte, error) {
		return nil, fmt.Errorf("JSON marshal error")
	}

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaDeleteBackupVaultReturnsFoundWithbackupVaultFails(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	mockOrchestrator.On("DeleteBackupVault", ctx, mock.Anything).
		Return(nil, "", errors2.NewBadRequestErr("Update failed in SDE"))

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestV1betaDeleteBackupVaultReturnsFoundWithbackupVaultBadRequestFails(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	params := gcpgenserver.V1betaDeleteBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:        resID,
		AccountName: "1234567890",
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	mockOrchestrator.On("DeleteBackupVault", ctx, mock.Anything).
		Return(nil, "", errors2.NewUserInputValidationErr("Update failed in SDE"))

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaDeleteBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
}

func TestReturnsBackupVaultV1betaWhenAllFieldsAreSet(t *testing.T) {
	beta := &coremodels.BackupVaultV1beta{
		BackupVaultID:          "vault-id",
		LifeCycleState:         "ACTIVE",
		LifeCycleStateDetails:  "All good",
		CreatedAt:              time.Now(),
		Description:            nillable.GetStringPtr("Test description"),
		Name:                   "resource-id",
		SourceBackupVault:      nillable.GetStringPtr("source-vault"),
		DestinationBackupVault: nillable.GetStringPtr("destination-vault"),
		SourceRegion:           nillable.GetStringPtr("us-central1"),
		BackupRegion:           nillable.GetStringPtr("us-east1"),
		BackupVaultType:        nillable.GetStringPtr("CROSS_REGION"),
		BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{
			BackupMinimumEnforcedRetentionDuration: nillable.GetInt64Ptr(30),
			IsDailyBackupImmutable:                 true,
			IsAdhocBackupImmutable:                 false,
			IsMonthlyBackupImmutable:               true,
			IsWeeklyBackupImmutable:                false,
		},
		KmsConfigResourcePath:    nillable.GetStringPtr("kms-config-path"),
		BackupsPrimaryKeyVersion: nillable.GetStringPtr("backups-primary-key-version"),
		EncryptionState:          nillable.GetStringPtr("encryption-state"),
		ServiceType:              datamodel.ServiceTypeCrossProject,
	}

	result := convertCoreModelsToBackupVaultV1beta(beta)

	assert.NotNil(t, result)
	assert.Equal(t, "vault-id", result.BackupVaultId.Value)
	assert.Equal(t, "ACTIVE", string(result.State.Value))
	assert.Equal(t, "All good", result.StateDetails.Value)
	assert.Equal(t, "Test description", result.Description.Value)
	assert.Equal(t, "resource-id", result.ResourceId)
	assert.Equal(t, "source-vault", result.SourceBackupVault.Value)
	assert.Equal(t, "destination-vault", result.DestinationBackupVault.Value)
	assert.Equal(t, "us-central1", result.SourceRegion.Value)
	assert.Equal(t, "us-east1", result.BackupRegion.Value)
	assert.Equal(t, "CROSS_REGION", string(result.BackupVaultType.Value))
	assert.Equal(t, 30, result.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.Value)
	assert.True(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.IsSet())
	assert.True(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.Value)
	assert.True(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.IsSet())
	assert.True(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.IsSet())
	assert.True(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.Value)
	assert.True(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.IsSet())
	assert.False(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.Value) // This is false
	assert.True(t, result.BackupRetentionPolicy.Value.MonthlyBackupImmutable.IsSet())
	assert.True(t, result.BackupRetentionPolicy.Value.MonthlyBackupImmutable.Value)
	assert.True(t, result.BackupRetentionPolicy.Value.WeeklyBackupImmutable.IsSet())
	assert.False(t, result.BackupRetentionPolicy.Value.WeeklyBackupImmutable.Value) // This is false
}

func TestConvertCoreModelsToBackupVaultV1beta_CrossRegionBackupVaultId(t *testing.T) {
	t.Run("SetsCrossRegionBackupVaultIdWhenExternalUUIDPresent", func(t *testing.T) {
		beta := &coremodels.BackupVaultV1beta{
			BackupVaultID:          "src-vault-id",
			Name:                   "my-vault",
			LifeCycleState:         "READY",
			LifeCycleStateDetails:  "Available",
			BackupVaultType:        nillable.GetStringPtr("CROSS_REGION"),
			SourceRegion:           nillable.GetStringPtr("us-east4"),
			BackupRegion:           nillable.GetStringPtr("us-west1"),
			SourceBackupVault:      nillable.GetStringPtr("source"),
			DestinationBackupVault: nillable.GetStringPtr("dest"),
			ExternalUUID:           nillable.GetStringPtr("remote-vault-uuid"),
		}

		result := convertCoreModelsToBackupVaultV1beta(beta)

		assert.True(t, result.CrossRegionBackupVaultId.IsSet())
		assert.Equal(t, "remote-vault-uuid", result.CrossRegionBackupVaultId.Value)
	})

	t.Run("DoesNotSetCrossRegionBackupVaultIdForInRegionVault", func(t *testing.T) {
		beta := &coremodels.BackupVaultV1beta{
			BackupVaultID:         "vault-id",
			Name:                  "my-vault",
			LifeCycleState:        "READY",
			LifeCycleStateDetails: "Available",
			BackupVaultType:       nillable.GetStringPtr("IN_REGION"),
			ExternalUUID:          nillable.GetStringPtr("some-uuid"),
		}

		result := convertCoreModelsToBackupVaultV1beta(beta)

		assert.False(t, result.CrossRegionBackupVaultId.IsSet())
	})

	t.Run("DoesNotSetCrossRegionBackupVaultIdWhenExternalUUIDEmpty", func(t *testing.T) {
		beta := &coremodels.BackupVaultV1beta{
			BackupVaultID:          "vault-id",
			Name:                   "my-vault",
			LifeCycleState:         "READY",
			LifeCycleStateDetails:  "Available",
			BackupVaultType:        nillable.GetStringPtr("CROSS_REGION"),
			SourceRegion:           nillable.GetStringPtr("us-east4"),
			BackupRegion:           nillable.GetStringPtr("us-west1"),
			SourceBackupVault:      nillable.GetStringPtr("source"),
			DestinationBackupVault: nillable.GetStringPtr("dest"),
			ExternalUUID:           nillable.GetStringPtr(""),
		}

		result := convertCoreModelsToBackupVaultV1beta(beta)

		assert.False(t, result.CrossRegionBackupVaultId.IsSet())
	})
}

func TestReturnsBackupVaultV1betaWithDefaultsWhenOptionalFieldsAreNil(t *testing.T) {
	beta := &coremodels.BackupVaultV1beta{
		BackupVaultID:         "vault-id",
		LifeCycleState:        "ACTIVE",
		LifeCycleStateDetails: "All good",
		CreatedAt:             time.Now(),
		Name:                  "resource-id",
		BackupRetentionPolicy: coremodels.BackupRetentionPolicyparams{},
	}

	result := convertCoreModelsToBackupVaultV1beta(beta)

	assert.NotNil(t, result)
	assert.Equal(t, "vault-id", result.BackupVaultId.Value)
	assert.Equal(t, "ACTIVE", string(result.State.Value))
	assert.Equal(t, "All good", result.StateDetails.Value)
	assert.Equal(t, "resource-id", result.ResourceId)
	assert.NotNil(t, result.Description)
	assert.NotNil(t, result.SourceBackupVault)
	assert.NotNil(t, result.DestinationBackupVault)
	assert.NotNil(t, result.SourceRegion)
	assert.NotNil(t, result.BackupRegion)
	assert.NotNil(t, result.BackupVaultType)
	assert.NotNil(t, result.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays)
	assert.True(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.IsSet())
	assert.False(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.Value)
	assert.True(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.IsSet())
	assert.False(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.Value)
	assert.True(t, result.BackupRetentionPolicy.Value.MonthlyBackupImmutable.IsSet())
	assert.False(t, result.BackupRetentionPolicy.Value.MonthlyBackupImmutable.Value)
	assert.True(t, result.BackupRetentionPolicy.Value.WeeklyBackupImmutable.IsSet())
	assert.False(t, result.BackupRetentionPolicy.Value.WeeklyBackupImmutable.Value)
}

func TestConvertBackupVaultV1Beta_OnlyTrueFieldsIncluded(t *testing.T) {
	tests := []struct {
		name             string
		input            *models.BackupVaultV1beta
		expectedFields   []string
		unexpectedFields []string
	}{
		{
			name: "Only daily backup immutable is true",
			input: &models.BackupVaultV1beta{
				BackupVaultID: "test-vault",
				State:         "READY",
				StateDetails:  "Available",
				CreatedAt:     strfmt.DateTime(time.Now()),
				BackupRetentionPolicy: &models.BackupRetentionPolicyV1beta{
					BackupMinimumEnforcedRetentionDays: func() *int64 { v := int64(7); return &v }(),
					DailyBackupImmutable:               true,
					WeeklyBackupImmutable:              false,
					MonthlyBackupImmutable:             false,
					ManualBackupImmutable:              false,
				},
			},
			expectedFields:   []string{"BackupMinimumEnforcedRetentionDays", "DailyBackupImmutable"},
			unexpectedFields: []string{"WeeklyBackupImmutable", "MonthlyBackupImmutable", "ManualBackupImmutable"},
		},
		{
			name: "All fields are false - only retention days should be included",
			input: &models.BackupVaultV1beta{
				BackupVaultID: "test-vault",
				State:         "READY",
				StateDetails:  "Available",
				CreatedAt:     strfmt.DateTime(time.Now()),
				BackupRetentionPolicy: &models.BackupRetentionPolicyV1beta{
					BackupMinimumEnforcedRetentionDays: func() *int64 { v := int64(7); return &v }(),
					DailyBackupImmutable:               false,
					WeeklyBackupImmutable:              false,
					MonthlyBackupImmutable:             false,
					ManualBackupImmutable:              false,
				},
			},
			expectedFields:   []string{"BackupMinimumEnforcedRetentionDays"},
			unexpectedFields: []string{"DailyBackupImmutable", "WeeklyBackupImmutable", "MonthlyBackupImmutable", "ManualBackupImmutable"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertBackupVaultV1Beta(tt.input)

			// Check expected fields are set
			for _, field := range tt.expectedFields {
				switch field {
				case "BackupMinimumEnforcedRetentionDays":
					assert.True(t, result.BackupRetentionPolicy.Value.BackupMinimumEnforcedRetentionDays.IsSet())
				case "DailyBackupImmutable":
					assert.True(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.IsSet())
					assert.True(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.Value)
				case "WeeklyBackupImmutable":
					assert.True(t, result.BackupRetentionPolicy.Value.WeeklyBackupImmutable.IsSet())
					assert.True(t, result.BackupRetentionPolicy.Value.WeeklyBackupImmutable.Value)
				case "MonthlyBackupImmutable":
					assert.True(t, result.BackupRetentionPolicy.Value.MonthlyBackupImmutable.IsSet())
					assert.True(t, result.BackupRetentionPolicy.Value.MonthlyBackupImmutable.Value)
				case "ManualBackupImmutable":
					assert.True(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.IsSet())
					assert.True(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.Value)
				}
			}

			// Check unexpected fields are NOT set
			for _, field := range tt.unexpectedFields {
				switch field {
				case "DailyBackupImmutable":
					assert.False(t, result.BackupRetentionPolicy.Value.DailyBackupImmutable.IsSet())
				case "WeeklyBackupImmutable":
					assert.False(t, result.BackupRetentionPolicy.Value.WeeklyBackupImmutable.IsSet())
				case "MonthlyBackupImmutable":
					assert.False(t, result.BackupRetentionPolicy.Value.MonthlyBackupImmutable.IsSet())
				case "ManualBackupImmutable":
					assert.False(t, result.BackupRetentionPolicy.Value.ManualBackupImmutable.IsSet())
				}
			}
		})
	}
}

// Tests for new validation logic (VSCP-2333)
func TestV1betaUpdateBackupVault_AttachmentValidation(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	t.Run("WhenBackupRetentionPolicyUpdateAndVaultAttachedToVolumes_ReturnsBadRequest", func(tt *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-with-volumes",
		}

		// Create request with backup retention policy update
		req := &gcpgenserver.BackupVaultUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				},
			),
		}

		// Mock region validation to pass
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "valid-region", "valid-zone", nil
		}

		// Mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		bvName := "vault-with-volumes"
		ctx := context.Background()

		// Mock GetBackupVaultByUUID to return a valid vault
		mockBackupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: bvName,
			OwnerID:       "1234567890",
		}
		mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
			Return(mockBackupVault, nil)

		// Mock IsBackupVaultAttachedToVolume to return true (has attached volumes)
		mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
			Return(true, nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(tt, err)
		badRequestResponse, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultBadRequest)
		assert.True(tt, ok, "Expected BadRequest response type")
		assert.Equal(tt, 400, int(badRequestResponse.Code))
		assert.Equal(tt, utils.ImmutableBackupVaultErrMsg, badRequestResponse.Message)
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenBackupRetentionPolicyUpdateAndVaultNotAttached_ProceedsNormally", func(tt *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-without-volumes",
		}

		// Create request with backup retention policy update
		req := &gcpgenserver.BackupVaultUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				},
			),
		}

		// Mock region validation to pass
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "valid-region", "valid-zone", nil
		}

		// Mock SDE update function to succeed
		updateBackupVaultInSDE = func(ctx context.Context, req *gcpgenserver.BackupVaultUpdateV1beta, params gcpgenserver.V1betaUpdateBackupVaultParams, description string) (r gcpgenserver.V1betaUpdateBackupVaultRes, _ error) {
			return &gcpgenserver.OperationV1beta{
				Name: gcpgenserver.NewOptString("operation-id"),
				Done: gcpgenserver.NewOptBool(false),
			}, nil
		}

		// Mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		bvName := "vault-without-volumes"
		ctx := context.Background()

		// Mock GetBackupVaultByUUID to return a valid vault
		mockBackupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: bvName,
			OwnerID:       "1234567890",
		}
		mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
			Return(mockBackupVault, nil)

		// Mock IsBackupVaultAttachedToVolume to return false (no attached volumes)
		mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
			Return(false, nil)

		// Mock UpdateBackupVault to return success
		mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
			Return(&coremodels.BackupVaultV1beta{BackupVaultID: bvName}, "operation-id", nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(tt, err)
		operationResponse, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "Expected Operation response type")
		assert.True(tt, operationResponse.Name.IsSet())
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenNoBackupRetentionPolicyUpdate_SkipsValidation", func(tt *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "any-vault",
		}

		// Create request without backup retention policy update
		req := &gcpgenserver.BackupVaultUpdateV1beta{
			Description: gcpgenserver.NewOptString("Updated description only"),
		}

		// Mock region validation to pass
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "valid-region", "valid-zone", nil
		}

		// Mock SDE update function to succeed
		updateBackupVaultInSDE = func(ctx context.Context, req *gcpgenserver.BackupVaultUpdateV1beta, params gcpgenserver.V1betaUpdateBackupVaultParams, description string) (r gcpgenserver.V1betaUpdateBackupVaultRes, _ error) {
			return &gcpgenserver.OperationV1beta{
				Name: gcpgenserver.NewOptString("operation-id"),
				Done: gcpgenserver.NewOptBool(false),
			}, nil
		}

		// Mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		bvName := "any-vault"
		ctx := context.Background()

		// Mock GetBackupVaultByUUID to return a valid vault
		mockBackupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: bvName,
			OwnerID:       "1234567890",
		}
		mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
			Return(mockBackupVault, nil)

		// Note: No IsBackupVaultAttachedToVolume call should be made since no retention policy update

		// Mock UpdateBackupVault to return success
		mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
			Return(&coremodels.BackupVaultV1beta{BackupVaultID: bvName}, "operation-id", nil)

		handler := Handler{Orchestrator: mockOrchestrator}

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(tt, err)
		operationResponse, ok := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "Expected Operation response type")
		assert.True(tt, operationResponse.Name.IsSet())
		mockOrchestrator.AssertExpectations(tt)
	})

	t.Run("WhenIsBackupVaultAttachedToVolumeReturnsError_ReturnsInternalServerError", func(tt *testing.T) {
		params := gcpgenserver.V1betaUpdateBackupVaultParams{
			LocationId:    "valid-location",
			ProjectNumber: "1234567890",
			BackupVaultId: "vault-with-error",
		}

		// Create request with backup retention policy update
		req := &gcpgenserver.BackupVaultUpdateV1beta{
			BackupRetentionPolicy: gcpgenserver.NewOptBackupRetentionPolicyUpdateV1beta(
				gcpgenserver.BackupRetentionPolicyUpdateV1beta{
					BackupMinimumEnforcedRetentionDays: gcpgenserver.NewOptInt(30),
				},
			),
		}

		// Mock region validation to pass
		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "valid-region", "valid-zone", nil
		}

		// Mock orchestrator
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		bvName := "vault-with-error"
		ctx := context.Background()

		// Mock GetBackupVaultByUUID to return a valid vault
		mockBackupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: bvName,
			OwnerID:       "1234567890",
		}
		mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
			Return(mockBackupVault, nil)

		// Mock IsBackupVaultAttachedToVolume to return an error
		mockOrchestrator.On("IsBackupVaultAttachedToVolume", ctx, bvName).
			Return(false, fmt.Errorf("database connection failed"))

		handler := Handler{Orchestrator: mockOrchestrator}

		result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

		assert.NoError(tt, err)
		errorResponse, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultInternalServerError)
		assert.True(tt, ok, "Expected InternalServerError response type")
		assert.Equal(tt, 500, int(errorResponse.Code))
		assert.Equal(tt, "Failed to check backup vault attachment status", errorResponse.Message)
		mockOrchestrator.AssertExpectations(tt)
	})
}

func TestV1betaUpdateBackupVault_WithKmsConfigResourcePathAndBackupsPrimaryKeyVersion(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	kmsConfigPath := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"
	backupsPrimaryKeyVersion := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1"

	req := &gcpgenserver.BackupVaultUpdateV1beta{
		KmsConfigResourcePath:    gcpgenserver.NewOptString(kmsConfigPath),
		BackupsPrimaryKeyVersion: gcpgenserver.NewOptString(backupsPrimaryKeyVersion),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:                  resID,
		AccountName:           "1234567890",
		KmsConfigResourcePath: &kmsConfigPath, // Set existing KmsConfigResourcePath to match request (validation will pass)
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	mockOrchestrator.On("UpdateBackupVault", ctx, mock.Anything).
		Return(&coremodels.BackupVaultV1beta{}, "operation-id", nil).Once()

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	mockOrchestrator.AssertExpectations(t)
}

func TestV1betaUpdateBackupVault_CMEK_AddCMEKToNonCMEKVault(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	kmsConfigPath := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"
	req := &gcpgenserver.BackupVaultUpdateV1beta{
		KmsConfigResourcePath: gcpgenserver.NewOptString(kmsConfigPath),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:                  resID,
		AccountName:           "1234567890",
		KmsConfigResourcePath: nil, // No existing CMEK
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	errorResponse, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultBadRequest)
	assert.True(t, ok, "Expected BadRequest response type")
	assert.Equal(t, 400, int(errorResponse.Code))
	assert.Equal(t, "CMEK Policy cannot be updated on Backup vault", errorResponse.Message)
	mockOrchestrator.AssertExpectations(t)
}

func TestV1betaUpdateBackupVault_CMEK_ChangeKmsConfigResourcePath(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	params := gcpgenserver.V1betaUpdateBackupVaultParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}

	currentKmsConfigPath := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"
	requestedKmsConfigPath := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/different-key"
	req := &gcpgenserver.BackupVaultUpdateV1beta{
		KmsConfigResourcePath: gcpgenserver.NewOptString(requestedKmsConfigPath),
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "valid-region", "valid-zone", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	ctx := context.Background()
	resID := "vault-id"
	bvResp := coremodels.BackupVaultV1beta{
		Name:                  resID,
		AccountName:           "1234567890",
		KmsConfigResourcePath: &currentKmsConfigPath, // Existing CMEK with different path
	}
	mockOrchestrator.On("GetBackupVaultByUUID", ctx, bvName, "1234567890").
		Return(&bvResp, nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaUpdateBackupVault(context.Background(), req, params)

	assert.NoError(t, err)
	errorResponse, ok := result.(*gcpgenserver.V1betaUpdateBackupVaultBadRequest)
	assert.True(t, ok, "Expected BadRequest response type")
	assert.Equal(t, 400, int(errorResponse.Code))
	assert.Equal(t, "CMEK Policy cannot be updated on Backup vault", errorResponse.Message)
	mockOrchestrator.AssertExpectations(t)
}

func TestConvertBackupVaultV1Beta_WithKmsConfigResourcePathAndBackupsPrimaryKeyVersionAndEncryptionState(t *testing.T) {
	kmsConfigPath := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key"
	backupsPrimaryKeyVersion := "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1"
	encryptionState := "ENCRYPTED"

	bv := &models.BackupVaultV1beta{
		BackupVaultID:            "test-vault-id",
		KmsConfigResourcePath:    &kmsConfigPath,
		BackupsPrimaryKeyVersion: &backupsPrimaryKeyVersion,
		EncryptionState:          &encryptionState,
	}

	result := convertBackupVaultV1Beta(bv)

	assert.NotNil(t, result)
	assert.True(t, result.KmsConfigResourcePath.IsSet())
	assert.Equal(t, kmsConfigPath, result.KmsConfigResourcePath.Value)
	assert.True(t, result.BackupsPrimaryKeyVersion.IsSet())
	assert.Equal(t, backupsPrimaryKeyVersion, result.BackupsPrimaryKeyVersion.Value)
	assert.True(t, result.EncryptionState.IsSet())
	assert.Equal(t, gcpgenserver.BackupVaultV1betaEncryptionState(encryptionState), result.EncryptionState.Value)
}

// V1betaRotateCmekBackups tests
func TestV1betaRotateCmekBackupsNotEnabled(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = false

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "valid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	badRequest, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Equal(t, "Backup feature is currently not enabled.", badRequest.Message)
}

func TestV1betaRotateCmekBackupsInvalidLocation(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "invalid-location",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "", "", &gcpgenserver.Error{Code: 400, Message: "LocationID represents neither a region nor a zone"}
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	badRequest, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequest.Code)
}

func TestV1betaRotateCmekBackupsSuccess(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:     "us-west1",
		ProjectNumber:  "1234567890",
		BackupVaultId:  "vault-id",
		XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	// Simulate backup vault existing in VCP.
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(&coremodels.BackupVaultV1beta{
			BackupVaultID:         "vault-id",
			Name:                  "bv-name",
			LifeCycleState:        "READY",
			LifeCycleStateDetails: "Available",
		}, nil)

	// Expect orchestrator CMEK rotation to be invoked and return an operation ID.
	mockOrchestrator.On("RotateCmekBackupsForBackupVault", mock.Anything, mock.AnythingOfType("*common.BackupVaultParams"), "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1").
		Return("job-uuid", nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	operation, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok)
	assert.Equal(t, fmt.Sprintf("/v1beta/projects/%s/locations/%s/operations/%s", params.ProjectNumber, params.LocationId, "job-uuid"), operation.Name.Value)
}

func TestV1betaRotateCmekBackups_ConflictWhenVaultInTransition(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	// Simulate backup vault in transition state (DELETING).
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(&coremodels.BackupVaultV1beta{
			BackupVaultID:         "vault-id",
			Name:                  "bv-name",
			LifeCycleState:        datamodel.LifeCycleStateDeleting,
			LifeCycleStateDetails: "Deleting",
		}, nil)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	conflict, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflict.Code)
}

// TestV1betaRotateCmekBackups_InternalServerErrorFromGetBackupVault verifies that
// non-NotFound errors from GetBackupVaultByUUID are mapped to 500.
func TestV1betaRotateCmekBackups_InternalServerErrorFromGetBackupVault(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	// Force GetBackupVaultByUUID to return a non-NotFound error so the handler
	// returns an internal server error instead of falling back to SDE.
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return((*coremodels.BackupVaultV1beta)(nil), fmt.Errorf("unexpected lookup failure"))

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	internalErr, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalErr.Code)
	assert.Equal(t, "unexpected lookup failure", internalErr.Message)
}

func TestV1betaRotateCmekBackups_NotFoundWhenUseVCPRegion(t *testing.T) {
	origBackupEnabled := backupEnabled
	origCmekBackupEnabled := cmekBackupEnabled
	origUseVCPRegion := cvp.CVP_HOST
	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		cmekBackupEnabled = origCmekBackupEnabled
		cvp.CVP_HOST = origUseVCPRegion
		parseAndValidateRegionAndZone = origParseAndValidate
	}()
	backupEnabled = true
	cmekBackupEnabled = true
	cvp.CVP_HOST = ""

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	bvName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &bvName))

	handler := Handler{Orchestrator: mockOrchestrator}
	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	require.NoError(t, err)
	require.NotNil(t, result)
	notFound, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsNotFound)
	require.True(t, ok)
	assert.Equal(t, float64(404), notFound.Code)
	assert.Equal(t, "Backup vault not found", notFound.Message)
	mockOrchestrator.AssertExpectations(t)
}

func TestV1betaRotateCmekBackups_MapsUserInputValidationErrorToBadRequest(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(&coremodels.BackupVaultV1beta{
			BackupVaultID:         "vault-id",
			Name:                  "bv-name",
			LifeCycleState:        datamodel.LifeCycleStateREADY,
			LifeCycleStateDetails: "Available",
		}, nil)

	validationErr := errors2.NewUserInputValidationErr("validation failed")
	mockOrchestrator.On("RotateCmekBackupsForBackupVault", mock.Anything, mock.AnythingOfType("*common.BackupVaultParams"), req.PrimaryKeyVersion).
		Return("", validationErr)

	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	badReq, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badReq.Code)
	assert.Equal(t, validationErr.Error(), badReq.Message)
}

func TestV1betaRotateCmekBackupsBadRequest(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "invalid-key-version",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)

	// Simulate backup vault lookup in VCP.
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(&coremodels.BackupVaultV1beta{
			BackupVaultID: "vault-id",
			Name:          "bv-name",
		}, nil)

	// Cause orchestrator rotation call to fail with a user input validation error.
	mockOrchestrator.On("RotateCmekBackupsForBackupVault", mock.Anything, mock.AnythingOfType("*common.BackupVaultParams"), "invalid-key-version").
		Return("", errors2.NewUserInputValidationErr("Invalid primary key version"))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	badRequest, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsBadRequest)
	assert.True(t, ok)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Equal(t, "Invalid primary key version", badRequest.Message)
}

func TestV1betaRotateCmekBackupsUnauthorized(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsUnauthorized{
		Payload: &models.Error{
			Code:    401,
			Message: "Unauthorized",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	// VCP lookup should fail so handler falls back to SDE/CVP path.
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	unauthorized, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsUnauthorized)
	assert.True(t, ok)
	assert.Equal(t, float64(401), unauthorized.Code)
}

func TestV1betaRotateCmekBackupsForbidden(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsForbidden{
		Payload: &models.Error{
			Code:    403,
			Message: "Forbidden",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	forbidden, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsForbidden)
	assert.True(t, ok)
	assert.Equal(t, float64(403), forbidden.Code)
}

func TestV1betaRotateCmekBackupsNotFound(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsNotFound{
		Payload: &models.Error{
			Code:    404,
			Message: "Backup vault not found",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	notFound, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsNotFound)
	assert.True(t, ok)
	assert.Equal(t, float64(404), notFound.Code)
}

func TestV1betaRotateCmekBackupsConflict(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsConflict{
		Payload: &models.Error{
			Code:    409,
			Message: "Conflict",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	conflict, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsConflict)
	assert.True(t, ok)
	assert.Equal(t, float64(409), conflict.Code)
}

func TestV1betaRotateCmekBackupsUnprocessableEntity(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsUnprocessableEntity{
		Payload: &models.Error{
			Code:    422,
			Message: "Unprocessable entity",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	unprocessable, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsUnprocessableEntity)
	assert.True(t, ok)
	assert.Equal(t, float64(422), unprocessable.Code)
}

func TestV1betaRotateCmekBackupsTooManyRequests(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsTooManyRequests{
		Payload: &models.Error{
			Code:    429,
			Message: "Too many requests",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	tooManyRequests, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsTooManyRequests)
	assert.True(t, ok)
	assert.Equal(t, float64(429), tooManyRequests.Code)
}

func TestV1betaRotateCmekBackupsDefaultError(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsDefault{
		Payload: &models.Error{
			Code:    500,
			Message: "Internal server error",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	internalError, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalError.Code)
}

func TestV1betaRotateCmekBackupsUnknownError(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := errors.New("unknown error")

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	internalError, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsInternalServerError)
	assert.True(t, ok)
	assert.Equal(t, float64(500), internalError.Code)
	assert.Equal(t, "unknown error", internalError.Message)
}

func TestV1betaRotateCmekBackupsNotImplemented(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	origCmekBackupEnabled := cmekBackupEnabled
	defer func() { cmekBackupEnabled = origCmekBackupEnabled }()
	cmekBackupEnabled = true

	origParseAndValidate := parseAndValidateRegionAndZone
	defer func() { parseAndValidateRegionAndZone = origParseAndValidate }()

	origCvpCreateClient := sde.BackupVaultCreateClient
	defer func() { sde.BackupVaultCreateClient = origCvpCreateClient }()

	params := gcpgenserver.V1betaRotateCmekBackupsParams{
		LocationId:    "us-west1",
		ProjectNumber: "1234567890",
		BackupVaultId: "vault-id",
	}
	req := &gcpgenserver.BackupVaultRotateCMEKBackupsV1beta{
		PrimaryKeyVersion: "projects/test-project/locations/us-west1/keyRings/test-keyring/cryptoKeys/test-key/cryptoKeyVersions/1",
	}

	parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-west1", "", nil
	}

	mockClient := backup_vault.NewMockClientService(t)
	mockError := &backup_vault.V1betaRotateCmekBackupsNotImplemented{
		Payload: &models.Error{
			Code:    501,
			Message: "Not implemented",
		},
	}

	mockClient.EXPECT().
		V1betaRotateCmekBackups(mock.Anything).
		Return(nil, mockError)

	cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
	sde.BackupVaultCreateClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return *cvpClient
	}

	mockOrchestrator := factory.NewMockOrchestratorFactory(t)
	stubPersistAccountTrialMetadataForCreate(mockOrchestrator)
	vaultName := "vault-id"
	mockOrchestrator.On("GetBackupVaultByUUID", mock.Anything, "vault-id", "1234567890").
		Return(nil, errors2.NewNotFoundErr("backup vault", &vaultName))
	handler := Handler{Orchestrator: mockOrchestrator}

	result, err := handler.V1betaRotateCmekBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	notImpl, ok := result.(*gcpgenserver.V1betaRotateCmekBackupsNotImplemented)
	assert.True(t, ok)
	assert.Equal(t, float64(501), notImpl.Code)
	assert.Equal(t, "Not implemented", notImpl.Message)
}

func TestV1betaListBackupVaultsErrors(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenListBackupVaultsFailsWithBadRequest", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &backup_vault.V1betaListBackupVaultsBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListBackupVaultsBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListBackupVaultsBadRequest).Message)
	})

	t.Run("WhenListBackupVaultsFailsWithUnauthorized", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		errorCode := float64(401)
		errorMessage := "Unauthorized"
		mockError := &backup_vault.V1betaListBackupVaultsUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListBackupVaultsUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListBackupVaultsUnauthorized).Message)
	})

	t.Run("WhenListBackupVaultsFailsWithForbidden", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		errorCode := float64(403)
		errorMessage := "Forbidden"
		mockError := &backup_vault.V1betaListBackupVaultsForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListBackupVaultsForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListBackupVaultsForbidden).Message)
	})

	t.Run("WhenListBackupVaultsFailsWithNotFound", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		errorCode := float64(404)
		errorMessage := "Not Found"
		mockError := &backup_vault.V1betaListBackupVaultsNotFound{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListBackupVaultsNotFound).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListBackupVaultsNotFound).Message)
	})

	t.Run("WhenListBackupVaultsFailsWithTooManyRequests", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		errorCode := float64(429)
		errorMessage := "Too many requests"
		mockError := &backup_vault.V1betaListBackupVaultsTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListBackupVaultsTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListBackupVaultsTooManyRequests).Message)
	})

	t.Run("WhenListBackupVaultsFailsWithNotImplemented", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		errorCode := float64(501)
		errorMessage := "Not implemented"
		mockError := &backup_vault.V1betaListBackupVaultsNotImplemented{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaListBackupVaultsNotImplemented).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaListBackupVaultsNotImplemented).Message)
	})

	t.Run("WhenListBackupVaultsFailsWithDefaultError", func(t *testing.T) {
		mockClient := backup_vault.NewMockClientService(t)
		origBackupEnabled := backupEnabled
		defer func() { backupEnabled = origBackupEnabled }()
		backupEnabled = true
		params := gcpgenserver.V1betaListBackupVaultsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		mockError := errors.New("unknown error")
		mockClient.EXPECT().
			V1betaListBackupVaults(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		handler := Handler{}
		result, err := handler.V1betaListBackupVaults(context.Background(), params)
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaListBackupVaultsInternalServerError).Code)
		assert.Equal(t, "unknown error", result.(*gcpgenserver.V1betaListBackupVaultsInternalServerError).Message)
	})
}

func TestDeleteBackupVaultInSDE(t *testing.T) {
	setupSDEClient := func(t *testing.T, mockClient *backup_vault.MockClientService) {
		t.Helper()
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		orig := sde.BackupVaultCreateClient
		t.Cleanup(func() { sde.BackupVaultCreateClient = orig })
		sde.BackupVaultCreateClient = func(_ log.Logger, _ string) cvpapi.Cvp { return *cvpClient }
	}

	t.Run("WhenSDESucceeds_ReturnsOperationAndNilError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		setupSDEClient(tt, mockClient)

		done := true
		mockClient.EXPECT().
			V1betaDeleteBackupVault(mock.MatchedBy(func(p *backup_vault.V1betaDeleteBackupVaultParams) bool {
				return p.LocationID == "us-west1" &&
					p.ProjectNumber == "test-project" &&
					p.BackupVaultID == "vault-uuid" &&
					p.XCorrelationID != nil && *p.XCorrelationID == "corr-123"
			})).
			Return(&backup_vault.V1betaDeleteBackupVaultAccepted{
				Payload: &models.OperationV1beta{Name: "operations/op-1", Done: &done},
			}, nil, nil)

		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "us-west1",
			ProjectNumber:  "test-project",
			BackupVaultId:  "vault-uuid",
			XCorrelationID: gcpgenserver.NewOptString("corr-123"),
		}

		res, err := _deleteBackupVaultInSDE(context.Background(), params, log.NewLogger())

		assert.NoError(tt, err)
		op, ok := res.(*gcpgenserver.OperationV1beta)
		assert.True(tt, ok, "expected *OperationV1beta")
		assert.Equal(tt, "operations/op-1", op.Name.Value)
		assert.True(tt, op.Done.Value)
	})

	t.Run("WhenSDEReturnsNotFound_ReturnsNotFoundResAndNilError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		setupSDEClient(tt, mockClient)

		code := float64(404)
		msg := "backup vault not found"
		mockClient.EXPECT().
			V1betaDeleteBackupVault(mock.Anything).
			Return(nil, nil, &backup_vault.V1betaDeleteBackupVaultNotFound{
				Payload: &models.Error{Code: code, Message: msg},
			})

		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:     "us-west1",
			ProjectNumber:  "test-project",
			BackupVaultId:  "vault-uuid",
			XCorrelationID: gcpgenserver.NewOptString("corr-123"),
		}

		res, err := _deleteBackupVaultInSDE(context.Background(), params, log.NewLogger())

		assert.NoError(tt, err)
		result, ok := res.(*gcpgenserver.V1betaDeleteBackupVaultNotFound)
		assert.True(tt, ok, "expected *V1betaDeleteBackupVaultNotFound")
		assert.Equal(tt, float64(404), result.Code)
		assert.Equal(tt, "backup vault not found", result.Message)
	})

	t.Run("WhenSDEReturnsUnknownError_ReturnsInternalServerError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		setupSDEClient(tt, mockClient)

		mockClient.EXPECT().
			V1betaDeleteBackupVault(mock.Anything).
			Return(nil, nil, errors.New("transport error"))

		params := gcpgenserver.V1betaDeleteBackupVaultParams{
			LocationId:    "us-west1",
			ProjectNumber: "test-project",
			BackupVaultId: "vault-uuid",
		}

		res, err := _deleteBackupVaultInSDE(context.Background(), params, log.NewLogger())

		assert.NoError(tt, err)
		result, ok := res.(*gcpgenserver.V1betaDeleteBackupVaultInternalServerError)
		assert.True(tt, ok, "expected *V1betaDeleteBackupVaultInternalServerError")
		assert.Equal(tt, float64(500), result.Code)
		assert.Contains(tt, result.Message, "transport error")
	})
}

func TestCreateBackupVaultInSDE(t *testing.T) {
	setupSDEClient := func(t *testing.T, mockClient *backup_vault.MockClientService) {
		t.Helper()
		cvpClient := &cvpapi.Cvp{BackupVault: mockClient}
		orig := sde.BackupVaultCreateClient
		t.Cleanup(func() { sde.BackupVaultCreateClient = orig })
		sde.BackupVaultCreateClient = func(_ log.Logger, _ string) cvpapi.Cvp { return *cvpClient }
	}

	body := &models.BackupVaultCreateV1beta{
		ResourceID:   "my-vault",
		BackupRegion: nillable.GetStringPtr("us-east1"),
	}

	t.Run("WhenSDESucceeds_ReturnsAcceptedAndNilErrRes", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		setupSDEClient(tt, mockClient)

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.MatchedBy(func(p *backup_vault.V1betaCreateBackupVaultParams) bool {
				return p.LocationID == "us-west1" &&
					p.ProjectNumber == "test-project" &&
					p.Body == body
			})).
			Return(&backup_vault.V1betaCreateBackupVaultAccepted{
				Payload: &models.OperationV1beta{
					Name: "operations/op-create",
					Response: &models.BackupVaultV1beta{
						ResourceID:    nillable.GetStringPtr("my-vault"),
						BackupVaultID: "generated-uuid",
					},
				},
			}, nil)

		accepted, errRes, err := _createBackupVaultInSDE(context.Background(), body, "us-west1", "test-project", log.NewLogger())

		assert.NoError(tt, err)
		assert.Nil(tt, errRes)
		assert.NotNil(tt, accepted)
		assert.Equal(tt, "operations/op-create", accepted.Payload.Name)
	})

	t.Run("WhenSDEReturnsConflict_ReturnsConflictErrResAndNilError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		setupSDEClient(tt, mockClient)

		code := float64(409)
		msg := "backup vault already exists"
		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, &backup_vault.V1betaCreateBackupVaultConflict{
				Payload: &models.Error{Code: code, Message: msg},
			})

		accepted, errRes, err := _createBackupVaultInSDE(context.Background(), body, "us-west1", "test-project", log.NewLogger())

		assert.NoError(tt, err)
		assert.Nil(tt, accepted)
		result, ok := errRes.(*gcpgenserver.V1betaCreateBackupVaultConflict)
		assert.True(tt, ok, "expected *V1betaCreateBackupVaultConflict")
		assert.Equal(tt, float64(409), result.Code)
		assert.Equal(tt, "backup vault already exists", result.Message)
	})

	t.Run("WhenSDEReturnsUnknownError_ReturnsInternalServerErrResAndNilError", func(tt *testing.T) {
		mockClient := backup_vault.NewMockClientService(tt)
		setupSDEClient(tt, mockClient)

		mockClient.EXPECT().
			V1betaCreateBackupVault(mock.Anything).
			Return(nil, errors.New("network timeout"))

		accepted, errRes, err := _createBackupVaultInSDE(context.Background(), body, "us-west1", "test-project", log.NewLogger())

		assert.NoError(tt, err)
		assert.Nil(tt, accepted)
		result, ok := errRes.(*gcpgenserver.V1betaCreateBackupVaultInternalServerError)
		assert.True(tt, ok, "expected *V1betaCreateBackupVaultInternalServerError")
		assert.Equal(tt, float64(500), result.Code)
		assert.Contains(tt, result.Message, "network timeout")
	})
}
