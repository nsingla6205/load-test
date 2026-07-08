package api

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/go-openapi/strfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/cvpapi/backups"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	coremodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/factory"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	gcpgenserver "github.com/vcp-vsa-control-Plane/vsa-control-plane/google-proxy/api/gcp-servergen"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
)

// Helper function for creating int64 pointers
func int64Ptr(i int64) *int64 {
	return &i
}

// V1betaGetMultipleBackups unittests
func TestV1GetMultipleBackups(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("WhenGetMultipleBackupsSuccess", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// Define request
		// Create a mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "test-backup-vault-id",
		}
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		backup := []*models.BackupV1beta{}
		// Define mock response
		createdAt := strfmt.DateTime(time.Now().UTC())
		description := "description"
		resourceId := "test-resource-id"
		volRegion := "us-east4"
		volUsageBytes := int64(123456)
		backupVaultId := "test-backup-vault-id"
		sourceSnapshotId := "snap-1"
		BackupChainBytes := int64(123)
		SatisfiesPzs := true
		SatisfiesPzi := true

		bv := models.BackupV1beta{
			BackupID:                 "backup-id-1",
			BackupRegion:             &volRegion,
			BackupType:               "adhoc",
			BackupVaultID:            &backupVaultId,
			Created:                  createdAt,
			Description:              &description,
			EnforcedRetentionEndTime: nil,
			ResourceID:               resourceId,
			SourceSnapshot:           &sourceSnapshotId,
			SourceVolume:             "available",
			State:                    "Available for use",
			VolumeID:                 "12345",
			VolumeRegion:             &volRegion,
			VolumeUsageBytes:         &volUsageBytes,
			BackupChainBytes:         &BackupChainBytes,
			SatisfiesPzs:             &SatisfiesPzs,
			SatisfiesPzi:             &SatisfiesPzi,
		}

		backup1 := append(backup, &bv)
		mockResponse := &backups.V1betaGetMultipleBackupsOK{
			Payload: &backups.V1betaGetMultipleBackupsOKBody{Backups: backup1},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackups(mock.Anything).
			Return(mockResponse, nil)
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		backupRegionName := "backup-region"
		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
			Name:             "test-backup-vault",
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
			SourceRegionName: nillable.ToPointer("rgn-test"),
			BackupRegionName: &backupRegionName,
		}
		b := []*datamodel.Backup{
			{
				BaseModel:     datamodel.BaseModel{UUID: "backup-uuid-1", CreatedAt: time.Now().UTC()},
				State:         "InProgress",
				Name:          "test-backup",
				VolumeUUID:    "test-vol",
				BackupVault:   backupVault,
				BackupVaultID: 1,
				Attributes:    &datamodel.BackupAttributes{},
				SizeInBytes:   1000,
				Description:   "test backup description",
				Type:          "adhoc",
			},

			{
				BaseModel:     datamodel.BaseModel{UUID: "backup-uuid-2", CreatedAt: time.Now().UTC()},
				State:         "InProgress",
				Name:          "test-backup-1",
				VolumeUUID:    "test-vol",
				BackupVault:   backupVault,
				BackupVaultID: 1,
				Attributes:    &datamodel.BackupAttributes{},
				SizeInBytes:   2000,
				Description:   "test backup 1 description",
				Type:          "adhoc",
			},
		}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the resource name is as expected
		assert.Equal(t, "backup-id-1", result.(*gcpgenserver.V1betaGetMultipleBackupsOK).Backups[0].BackupId.Value)
		assert.Equal(t, "test-backup", result.(*gcpgenserver.V1betaGetMultipleBackupsOK).Backups[1].ResourceId.Value)
		assert.Equal(t, "test-backup-1", result.(*gcpgenserver.V1betaGetMultipleBackupsOK).Backups[2].ResourceId.Value)
	})
	t.Run("WhenGetMultipleBackupsFailsWithVCPFails", func(t *testing.T) {
		// Define request
		// Create a mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupVaultId:  "test-backup-vault-id",
		}
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("Failed to get backups under backup vault"))
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaGetMultipleBackupsInternalServerError).Code)
	})

	t.Run("WhenGetMultipleBackupsFailsWithBadRequest", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// Create a mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		// Define mock error
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &backups.V1betaGetMultipleBackupsBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackups(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		backupRegionName := "backup-region"
		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
			Name:             "test-backup-vault",
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
			BackupRegionName: &backupRegionName,
		}
		b := []*datamodel.Backup{{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid-1", CreatedAt: time.Now().UTC()},
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
			SizeInBytes:   1000,
			Description:   "test backup description",
			Type:          "adhoc",
		}}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)

		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupsBadRequest).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupsBadRequest).Message)
	})

	t.Run("WhenGetMultipleBackupsWithNoUUIDs", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// Create a mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupUuidListV1beta{}

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		backupRegionName := "backup-region"
		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
			Name:             "test-backup-vault",
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
			SourceRegionName: nillable.ToPointer("rgn-test"),
			BackupRegionName: &backupRegionName,
		}
		b := []*datamodel.Backup{{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid-1", CreatedAt: time.Now().UTC()},
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
			SizeInBytes:   1000,
			Description:   "test backup description",
			Type:          "adhoc",
		}}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)

		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.NotNil(t, result.(*gcpgenserver.V1betaGetMultipleBackupsOK).Backups)
	})

	t.Run("WhenGetMultipleBackupsFailsWithUnAuthorized", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// Create a mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		// Define mock error
		errorCode := float64(401)
		errorMessage := "UnAuthorized"
		mockError := &backups.V1betaGetMultipleBackupsUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackups(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		backupRegionName := "backup-region"
		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
			Name:             "test-backup-vault",
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
			BackupRegionName: &backupRegionName,
		}
		backups := []*datamodel.Backup{{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid-1", CreatedAt: time.Now().UTC()},
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
			SizeInBytes:   1000,
			Description:   "test backup description",
			Type:          "adhoc",
		}}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backups, nil) // Call the method under test
		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupsUnauthorized).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupsUnauthorized).Message)
	})
	t.Run("WhenGetMultipleBackupsFailsWithForbidden", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		// Create a mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		// Define request
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		// Define mock error
		errorCode := float64(403)
		errorMessage := "Forbidden"
		mockError := &backups.V1betaGetMultipleBackupsForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}
		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackups(mock.Anything).
			Return(nil, mockError)
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		backupRegionName := "backup-region"
		backupVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "backup-vault-uuid"},
			Name:             "test-backup-vault",
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
			BackupRegionName: &backupRegionName,
		}
		b := []*datamodel.Backup{{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid-1", CreatedAt: time.Now().UTC()},
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
			SizeInBytes:   1000,
			Description:   "test backup description",
			Type:          "adhoc",
		}}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)
		// Call the method under test
		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		// Check if the code is as expected
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaGetMultipleBackupsForbidden).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaGetMultipleBackupsForbidden).Message)
	})
}

// Unit tests for V1betaUpdateBackup
func TestUpdateBackup(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenUpdateBackupFailsWithBadRequest", func(t *testing.T) {
		// Mock input parameters
		req := &gcpgenserver.BackupUpdateV1beta{}
		params := gcpgenserver.V1betaUpdateBackupParams{}

		// Mock handler
		handler := Handler{}

		// Call the method under test
		result, err := handler.V1betaUpdateBackup(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaUpdateBackupBadRequest).Code)
	})

	t.Run("WhenUpdateBackupFailsInParseAndValidateRegionAndZone", func(t *testing.T) {
		// Mock input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "test-backup-id",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			LocationId:    "test-location",
			ProjectNumber: "12345",
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}
		handler := Handler{}

		// Call the method under test
		result, err := handler.V1betaUpdateBackup(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaUpdateBackupBadRequest).Code)
	})
	t.Run("WhenUpdateBackupSucceeds", func(t *testing.T) {
		env.GetString("LOCAL_REGION", "us-east4")
		backupEnabled = true
		// Mock input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "12345",
			BackupVaultId: "test-backup-vault-id",
			BackupId:      "test-backup-id",
		}

		// Mock orchestrator
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		// Mock orchestrator behavior
		mockOrch.EXPECT().GetBackup(mock.Anything, mock.Anything).Return(&datamodel.Backup{}, nil)
		mockOrch.EXPECT().UpdateBackup(mock.Anything, mock.Anything).Return(&coremodels.Backup{}, "job-id", nil)

		// Call the method under test
		result, err := handler.V1betaUpdateBackup(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
		assert.Equal(t, "/v1beta/projects/12345/locations/us-east4/operations/job-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenBackupNotFoundAndUseVCPRegionEnabled_ShouldReturnNotFound", func(t *testing.T) {
		backupEnabled = true
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "12345",
			BackupVaultId: "test-backup-vault-id",
			BackupId:      "test-backup-id",
		}

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrch.EXPECT().GetBackup(mock.Anything, mock.Anything).Return(nil, errors.NewNotFoundErr("backup not found", nil))

		result, err := handler.V1betaUpdateBackup(context.Background(), req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupNotFound{}, result)
		assert.Equal(t, float64(404), result.(*gcpgenserver.V1betaUpdateBackupNotFound).Code)
		assert.Equal(t, "Backup with ID test-backup-id not found", result.(*gcpgenserver.V1betaUpdateBackupNotFound).Message)
	})
	t.Run("WhenOrchestratorGetBackupReturnsNonNotFoundError", func(t *testing.T) {
		backupEnabled = true
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "12345",
			BackupVaultId: "test-backup-vault-id",
			BackupId:      "test-backup-id",
		}

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrch.EXPECT().GetBackup(mock.Anything, mock.Anything).Return(nil, errors.New("internal server error"))

		result, err := handler.V1betaUpdateBackup(context.Background(), req, params)

		assert.Error(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaUpdateBackupInternalServerError).Code)
		assert.Equal(t, "Internal error while fetching backup from VSA", result.(*gcpgenserver.V1betaUpdateBackupInternalServerError).Message)
	})
}

func strPtr(s string) *string {
	return &s
}

func TestConvertToBackupsV1beta(t *testing.T) {
	backup := &models.BackupV1beta{
		ResourceID:       "test-backup",
		VolumeID:         "test-volume",
		State:            "READY",
		Created:          strfmt.DateTime(time.Now()),
		BackupID:         "backup-123",
		VolumeUsageBytes: func() *int64 { v := int64(1024); return &v }(),
		SourceVolume:     "projects/123/locations/us-central1/volumes/test-volume",
		BackupVaultID:    func() *string { s := "vault-123"; return &s }(),
		Description:      func() *string { s := "Test backup"; return &s }(),
		BackupType:       "MANUAL",
		BackupChainBytes: func() *int64 { v := int64(512); return &v }(),
		SatisfiesPzs:     func() *bool { b := true; return &b }(),
		SatisfiesPzi:     func() *bool { b := false; return &b }(),
		VolumeRegion:     func() *string { s := "us-central1"; return &s }(),
		BackupRegion:     func() *string { s := "us-east1"; return &s }(),
	}

	result := convertToBackupsV1beta(backup)

	assert.Equal(t, "test-backup", result.ResourceId.Value)
	assert.Equal(t, "test-volume", result.VolumeId.Value)
	assert.Equal(t, "READY", string(result.State.Value))
	assert.True(t, result.Created.IsSet())
	assert.True(t, result.BackupId.IsSet())
	assert.Equal(t, "backup-123", result.BackupId.Value)
	assert.True(t, result.VolumeUsageBytes.IsSet())
	assert.Equal(t, int64(1024), result.VolumeUsageBytes.Value)
	assert.True(t, result.SourceVolume.IsSet())
	assert.Equal(t, "projects/123/locations/us-central1/volumes/test-volume", result.SourceVolume.Value)
	assert.True(t, result.BackupVaultId.IsSet())
	assert.Equal(t, "vault-123", result.BackupVaultId.Value)
	assert.True(t, result.Description.IsSet())
	assert.Equal(t, "Test backup", result.Description.Value)
	assert.Equal(t, "MANUAL", string(result.BackupType.Value))
	assert.True(t, result.BackupChainBytes.IsSet())
	assert.Equal(t, int64(512), result.BackupChainBytes.Value)
	assert.True(t, result.SatisfiesPzs.IsSet())
	assert.True(t, result.SatisfiesPzs.Value)
	assert.True(t, result.SatisfiesPzi.IsSet())
	assert.False(t, result.SatisfiesPzi.Value)
	assert.True(t, result.VolumeRegion.IsSet())
	assert.Equal(t, "us-central1", result.VolumeRegion.Value)
	assert.True(t, result.BackupRegion.IsSet())
	assert.Equal(t, "us-east1", result.BackupRegion.Value)
	assert.False(t, result.AssetLocationMetadata.IsSet())
}

func TestConvertToBackupsV1beta_NilFields(t *testing.T) {
	backup := &models.BackupV1beta{
		ResourceID:    "test-backup",
		VolumeID:      "test-volume",
		State:         "READY",
		BackupID:      "backup-123",
		BackupVaultID: func() *string { s := "vault-123"; return &s }(),
		BackupType:    "MANUAL",
		// Only set pointer fields to nil, omit non-pointer fields
		EnforcedRetentionEndTime: nil,
		VolumeUsageBytes:         nil,
		Description:              nil,
		SourceSnapshot:           nil,
		BackupChainBytes:         nil,
		SatisfiesPzs:             nil,
		SatisfiesPzi:             nil,
		VolumeRegion:             nil,
		BackupRegion:             nil,
		AssetLocationMetadata:    nil,
	}

	result := convertToBackupsV1beta(backup)

	assert.Equal(t, "test-backup", result.ResourceId.Value)
	assert.Equal(t, "test-volume", result.VolumeId.Value)
	assert.Equal(t, "READY", string(result.State.Value))
	assert.True(t, result.BackupId.IsSet())
	assert.Equal(t, "backup-123", result.BackupId.Value)
	assert.True(t, result.BackupVaultId.IsSet())
	assert.Equal(t, "vault-123", result.BackupVaultId.Value)
	assert.Equal(t, "MANUAL", string(result.BackupType.Value))

	// Optional fields should not be set when nil
	assert.True(t, result.Created.IsSet())
	assert.False(t, result.EnforcedRetentionEndTime.IsSet())
	assert.False(t, result.VolumeUsageBytes.IsSet())
	assert.True(t, result.SourceVolume.IsSet())
	assert.False(t, result.Description.IsSet())
	assert.False(t, result.SourceSnapshot.IsSet())
	assert.False(t, result.BackupChainBytes.IsSet())
	assert.False(t, result.SatisfiesPzs.IsSet())
	assert.False(t, result.SatisfiesPzi.IsSet())
	assert.False(t, result.VolumeRegion.IsSet())
	assert.False(t, result.BackupRegion.IsSet())
	assert.False(t, result.AssetLocationMetadata.IsSet())
}

func TestCreateBackupParams(t *testing.T) {
	t.Run("WhenAllFieldsAreProvided", func(t *testing.T) {
		req := &gcpgenserver.BackupCreateV1beta{
			ResourceId:  "backup-name",
			VolumeId:    "volume-uuid",
			Description: gcpgenserver.NewOptString("backup-description"),
			SnapshotId:  gcpgenserver.NewOptString("snapshot-id"),
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			ProjectNumber: "project-number",
			BackupVaultId: "backup-vault-id",
		}

		result := createBackupParams(req, params, false, "GCNV")

		assert.Equal(t, "project-number", result.AccountName)
		assert.Equal(t, "backup-vault-id", result.BackupVaultID)
		assert.Equal(t, "volume-uuid", result.VolumeUUID)
		assert.Equal(t, "backup-name", result.BackupName)
		assert.Equal(t, "MANUAL", result.BackupType)
		assert.Equal(t, "backup-description", result.Description)
		assert.Equal(t, "snapshot-id", result.SnapshotID)
	})

	t.Run("WhenOptionalFieldsAreNotSet", func(t *testing.T) {
		req := &gcpgenserver.BackupCreateV1beta{
			ResourceId: "backup-name",
			VolumeId:   "volume-uuid",
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			ProjectNumber: "project-number",
			BackupVaultId: "backup-vault-id",
		}
		result := createBackupParams(req, params, false, "GCNV")
		assert.Equal(t, "project-number", result.AccountName)
		assert.Equal(t, "backup-vault-id", result.BackupVaultID)
		assert.Equal(t, "volume-uuid", result.VolumeUUID)
		assert.Equal(t, "backup-name", result.BackupName)
		assert.Equal(t, "MANUAL", result.BackupType)
		assert.Empty(t, result.Description)
		assert.Empty(t, result.SnapshotID)
	})
}

func TestV1betaCreateBackup(t *testing.T) {
	t.Run("WhenCreateBackupFailsWithBadRequest", func(t *testing.T) {
		// Mock input parameters
		req := &gcpgenserver.BackupCreateV1beta{}
		params := gcpgenserver.V1betaCreateBackupParams{}

		// Mock handler
		handler := Handler{}

		// Call the method under test
		result, err := handler.V1betaCreateBackup(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateBackupBadRequest).Code)
	})

	t.Run("WhenCreateBackupFailsWithBadRequest", func(t *testing.T) {
		// Mock input parameters
		req := &gcpgenserver.BackupCreateV1beta{}
		params := gcpgenserver.V1betaCreateBackupParams{}

		handler := Handler{}

		// Call the method under test
		result, err := handler.V1betaCreateBackup(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateBackupBadRequest).Code)
	})

	t.Run("WhenCreateBackupFailsInParseAndValidateRegionAndZone", func(t *testing.T) {
		// Mock input parameters
		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   "test-volume-id",
			ResourceId: "test-resource-id",
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:    "test-location",
			ProjectNumber: "12345",
		}

		defer func() {
			parseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()

		parseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "location-id", "location-id", &gcpgenserver.Error{
				Code:    400,
				Message: "Invalid location ID",
			}
		}
		handler := Handler{}

		// Call the method under test
		result, err := handler.V1betaCreateBackup(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateBackupBadRequest).Code)
	})

	t.Run("WhenVolumeExistsInVSA_LifeCycleStateCreating", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{VolumeId: "vol-id", ResourceId: "res-id"}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "proj",
			BackupVaultId: "vault",
		}

		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		originalCheckIfBackupExistInCVP := checkIfBackupExistInCVP
		origBackupEnabled := backupEnabled
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			checkIfBackupExistInCVP = originalCheckIfBackupExistInCVP
			backupEnabled = origBackupEnabled
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		checkIfBackupExistInCVP = func(ctx context.Context, backupID *string, params gcpgenserver.V1betaCreateBackupParams) (bool, error) {
			return false, nil // Backup doesn't exist in CVP
		}
		backupEnabled = true

		// Mock backup vault
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		// Mock volume exists in VSA
		volume := &coremodels.Volume{
			BaseModel: coremodels.BaseModel{
				UUID:      "vol-id",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			AccountName:           "proj",
			DisplayName:           "test-volume",
			LifeCycleState:        "READY",
			LifeCycleStateDetails: "All systems go",
			DataProtection: &coremodels.DataProtection{
				BackupVaultID: "vault",
			},
		}
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(volume, nil)

		// Mock successful backup creation with LifeCycleStateCreating

		backup := &coremodels.Backup{
			BackupID:       "backup-id",
			LifeCycleState: datamodel.LifeCycleStateCreating,
			Name:           "test-backup",
		}
		jobID := "job-123"
		mockOrch.EXPECT().CreateBackup(ctx, mock.Anything).Return(backup, jobID, nil)

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operation := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(t, "/v1beta/projects/proj/locations/us-east4/operations/job-123", operation.Name.Value)
		assert.False(t, operation.Done.Value) // Should be false for CREATING state
	})

	t.Run("WhenVolumeExistsInVSA_LifeCycleStateAvailable", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{VolumeId: "vol-id", ResourceId: "res-id"}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "proj",
			BackupVaultId: "vault",
		}

		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		originalCheckIfBackupExistInCVP := checkIfBackupExistInCVP
		origBackupEnabled := backupEnabled
		backupEnabled = true
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			checkIfBackupExistInCVP = originalCheckIfBackupExistInCVP
			backupEnabled = origBackupEnabled
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		checkIfBackupExistInCVP = func(ctx context.Context, backupID *string, params gcpgenserver.V1betaCreateBackupParams) (bool, error) {
			return false, nil // Backup doesn't exist in CVP
		}

		// Mock backup vault
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		// Mock volume exists in VSA
		volume := &coremodels.Volume{
			BaseModel: coremodels.BaseModel{
				UUID:      "vol-id",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			AccountName:           "proj",
			DisplayName:           "test-volume",
			LifeCycleState:        "READY",
			LifeCycleStateDetails: "All systems go",
			DataProtection: &coremodels.DataProtection{
				BackupVaultID: "vault",
			},
		}
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(volume, nil)

		// Mock successful backup creation with LifeCycleStateAvailable
		backup := &coremodels.Backup{
			BackupID:       "backup-id",
			LifeCycleState: datamodel.LifeCycleStateAvailable,
			Name:           "test-backup",
		}
		jobID := "job-123"
		mockOrch.EXPECT().CreateBackup(ctx, mock.Anything).Return(backup, jobID, nil)

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operation := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(t, "/v1beta/projects/proj/locations/us-east4/operations/job-123", operation.Name.Value)
		assert.True(t, operation.Done.Value) // Should be true for non-CREATING states
	})
}

// Test cases for V1betaGetMultipleBackups
func TestV1betaGetMultipleBackups_NotFound(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	mockClient := backups.NewMockClientService(t)
	params := gcpgenserver.V1betaGetMultipleBackupsParams{}
	req := &gcpgenserver.BackupUuidListV1beta{
		BackupUuids: []string{"backup-id-1"},
	}
	mockError := &backups.V1betaGetMultipleBackupsNotFound{
		Payload: &models.Error{Code: 404, Message: "Not Found"},
	}
	mockClient.EXPECT().V1betaGetMultipleBackups(mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{Backups: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *cvpClient }
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrch}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	b := []*datamodel.Backup{{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}}
	mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)
	result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
	assert.NoError(t, err)
	assert.Equal(t, float64(404), result.(*gcpgenserver.V1betaGetMultipleBackupsNotFound).Code)
	assert.Equal(t, "Not Found", result.(*gcpgenserver.V1betaGetMultipleBackupsNotFound).Message)
}

func TestV1betaGetMultipleBackups_InternalServerError(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	mockClient := backups.NewMockClientService(t)
	params := gcpgenserver.V1betaGetMultipleBackupsParams{}
	req := &gcpgenserver.BackupUuidListV1beta{
		BackupUuids: []string{"backup-id-1"},
	}
	mockError := &backups.V1betaGetMultipleBackupsInternalServerError{
		Payload: &models.Error{Code: 500, Message: "Internal Server Error"},
	}
	mockClient.EXPECT().V1betaGetMultipleBackups(mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{Backups: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *cvpClient }
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrch}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	b := []*datamodel.Backup{{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}}
	mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)
	result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
	assert.NoError(t, err)
	assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaGetMultipleBackupsInternalServerError).Code)
	assert.Contains(t, result.(*gcpgenserver.V1betaGetMultipleBackupsInternalServerError).Message, "Internal Server Error")
}

func TestV1betaGetMultipleBackups_TooManyRequests(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	mockClient := backups.NewMockClientService(t)
	params := gcpgenserver.V1betaGetMultipleBackupsParams{}
	req := &gcpgenserver.BackupUuidListV1beta{
		BackupUuids: []string{"backup-id-1"},
	}
	mockError := &backups.V1betaGetMultipleBackupsTooManyRequests{
		Payload: &models.Error{Code: 429, Message: "Too Many Requests"},
	}
	mockClient.EXPECT().V1betaGetMultipleBackups(mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{Backups: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *cvpClient }
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrch}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	b := []*datamodel.Backup{{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}}
	mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)
	result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
	assert.NoError(t, err)
	assert.Equal(t, float64(429), result.(*gcpgenserver.V1betaGetMultipleBackupsTooManyRequests).Code)
	assert.Equal(t, "Too Many Requests", result.(*gcpgenserver.V1betaGetMultipleBackupsTooManyRequests).Message)
}

func TestV1betaGetMultipleBackups_NotImplemented(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	mockClient := backups.NewMockClientService(t)
	params := gcpgenserver.V1betaGetMultipleBackupsParams{}
	req := &gcpgenserver.BackupUuidListV1beta{
		BackupUuids: []string{"backup-id-1"},
	}
	mockError := &backups.V1betaGetMultipleBackupsNotImplemented{
		Payload: &models.Error{Code: 501, Message: "Not Implemented"},
	}
	mockClient.EXPECT().V1betaGetMultipleBackups(mock.Anything).Return(nil, mockError)
	cvpClient := &cvpapi.Cvp{Backups: mockClient}
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *cvpClient }
	mockOrch := factory.NewMockOrchestratorFactory(t)
	handler := Handler{Orchestrator: mockOrch}
	backupVault := &datamodel.BackupVault{
		Name:          "test-backup-vault",
		BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
	}
	b := []*datamodel.Backup{{
		State:         "InProgress",
		Name:          "test-backup",
		VolumeUUID:    "test-vol",
		BackupVault:   backupVault,
		BackupVaultID: 1,
		Attributes:    &datamodel.BackupAttributes{},
	}}
	mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)
	result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)
	assert.NoError(t, err)
	assert.Equal(t, float64(501), result.(*gcpgenserver.V1betaGetMultipleBackupsNotImplemented).Code)
	assert.Equal(t, "Not Implemented", result.(*gcpgenserver.V1betaGetMultipleBackupsNotImplemented).Message)
}

// Test cases for missing lines in V1betaGetMultipleBackups
func TestV1betaGetMultipleBackups_MissingLines(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("WhenGetMultipleBackupsFailsWithInternalServerError", func(t *testing.T) {
		// Mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		// Define mock error
		mockError := &backups.V1betaGetMultipleBackupsInternalServerError{
			Payload: &models.Error{
				Code:    500,
				Message: "Internal Server Error",
			},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackups(mock.Anything).
			Return(nil, mockError)

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		backupVault := &datamodel.BackupVault{
			Name:          "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		b := []*datamodel.Backup{{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)

		// Call the method under test
		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaGetMultipleBackupsInternalServerError).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaGetMultipleBackupsInternalServerError).Message, "Internal Server Error")
	})

	t.Run("WhenGetMultipleBackupsHandlesEmptyResponse", func(t *testing.T) {
		// Mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		// Define mock response
		mockResponse := &backups.V1betaGetMultipleBackupsOK{
			Payload: &backups.V1betaGetMultipleBackupsOKBody{Backups: []*models.BackupV1beta{}},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackups(mock.Anything).
			Return(mockResponse, nil)

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		b := []*datamodel.Backup{}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)

		// Call the method under test
		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Empty(t, result.(*gcpgenserver.V1betaGetMultipleBackupsOK).Backups)
	})
	t.Run("WhenGetMultipleBackupsHandlesVCPResponse", func(t *testing.T) {
		// Mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		params := gcpgenserver.V1betaGetMultipleBackupsParams{
			LocationId:     "test-location",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}
		req := &gcpgenserver.BackupUuidListV1beta{
			BackupUuids: []string{"backup-id-1"},
		}

		// Define mock response
		mockResponse := &backups.V1betaGetMultipleBackupsOK{
			Payload: &backups.V1betaGetMultipleBackupsOKBody{Backups: []*models.BackupV1beta{}},
		}

		// Set up the mock client behavior
		mockClient.EXPECT().
			V1betaGetMultipleBackups(mock.Anything).
			Return(mockResponse, nil)

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}
		backupVault := &datamodel.BackupVault{
			Name:             "test-backup-vault",
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
			SourceRegionName: nillable.ToPointer("rgn-test"),
		}
		b := []*datamodel.Backup{{
			State:         "InProgress",
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
		}}
		mockOrch.EXPECT().GetBackupsUnderBackupVault(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(b, nil)

		// Call the method under test
		result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, result.(*gcpgenserver.V1betaGetMultipleBackupsOK).Backups[0].ResourceId.Value, "test-backup")
	})
}

func TestV1betaCreateBackup_CVPErrorCases(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	type cvpErrCase struct {
		name     string
		err      error
		expected any
	}
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	cases := []cvpErrCase{
		{
			name: "BadRequest",
			err: &backups.V1betaCreateBackupBadRequest{
				Payload: &models.Error{Code: 400, Message: "bad request"},
			},
			expected: &gcpgenserver.V1betaCreateBackupBadRequest{},
		},
		{
			name: "Unauthorized",
			err: &backups.V1betaCreateBackupUnauthorized{
				Payload: &models.Error{Code: 401, Message: "unauthorized"},
			},
			expected: &gcpgenserver.V1betaCreateBackupUnauthorized{},
		},
		{
			name: "Forbidden",
			err: &backups.V1betaCreateBackupForbidden{
				Payload: &models.Error{Code: 403, Message: "forbidden"},
			},
			expected: &gcpgenserver.V1betaCreateBackupForbidden{},
		},
		{
			name: "Conflict",
			err: &backups.V1betaCreateBackupConflict{
				Payload: &models.Error{Code: 409, Message: "conflict"},
			},
			expected: &gcpgenserver.V1betaCreateBackupConflict{},
		},
		{
			name: "UnprocessableEntity",
			err: &backups.V1betaCreateBackupUnprocessableEntity{
				Payload: &models.Error{Code: 422, Message: "unprocessable"},
			},
			expected: &gcpgenserver.V1betaCreateBackupUnprocessableEntity{},
		},
		{
			name: "TooManyRequests",
			err: &backups.V1betaCreateBackupTooManyRequests{
				Payload: &models.Error{Code: 429, Message: "too many"},
			},
			expected: &gcpgenserver.V1betaCreateBackupTooManyRequests{},
		},
		{
			name: "NotImplemented",
			err: &backups.V1betaCreateBackupNotImplemented{
				Payload: &models.Error{Code: 501, Message: "not implemented"},
			},
			expected: &gcpgenserver.V1betaCreateBackupNotImplemented{},
		},
		{
			name: "InternalServerError",
			err: &backups.V1betaCreateBackupInternalServerError{
				Payload: &models.Error{Code: 500, Message: "internal"},
			},
			expected: &gcpgenserver.V1betaCreateBackupInternalServerError{},
		},
		{
			name:     "DefaultError",
			err:      fmt.Errorf("unexpected error"),
			expected: &gcpgenserver.V1betaCreateBackupInternalServerError{},
		},
	}
	for _, c := range cases {
		t.Run("CVPError_"+c.name, func(t *testing.T) {
			logger := &log.MockLogger{}
			ctx := context.Background()
			ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)
			mockClient := backups.NewMockClientService(t)
			mockOrch := factory.NewMockOrchestratorFactory(t)
			req := &gcpgenserver.BackupCreateV1beta{VolumeId: "vol-id", ResourceId: "res-id"}
			params := gcpgenserver.V1betaCreateBackupParams{LocationId: "valid-location-id", ProjectNumber: "proj", BackupVaultId: "vault"}
			originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
			utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
				return "us-east4", "us-east4", nil
			}

			// Mock backup vault
			backupVault := &coremodels.BackupVaultV1beta{
				BackupVaultID: "vault",
				AccountName:   "proj",
				ServiceType:   "GCNV",
			}
			mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

			volumeID := "vol-id"
			mockOrch.EXPECT().
				GetVolume(ctx, "vol-id", false).
				Return(nil, errors.NewNotFoundErr("Volume", &volumeID))

			// Mock ListBackups call that happens when volume is not found
			mockOrch.EXPECT().
				ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
				Return([]*datamodel.Backup{}, nil)

			mockClient.EXPECT().V1betaCreateBackup(mock.Anything).Return(nil, nil, c.err)
			cvpClient := &cvpapi.Cvp{Backups: mockClient}
			originalCreateClient := createClient
			defer func() {
				createClient = originalCreateClient
				utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			}()
			createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp { return *cvpClient }
			handler := Handler{Orchestrator: mockOrch}
			result, err := handler.V1betaCreateBackup(ctx, req, params)
			assert.NoError(t, err)
			assert.IsType(t, c.expected, result)
		})
	}
}

func TestV1betaCreateBackup_CVPCreateBackupCreatedAndAccepted(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("ReturnsCreatedBackupWhenCVPBackupCreated", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)
		mockClient := backups.NewMockClientService(t)
		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{VolumeId: "vol-id", ResourceId: "res-id"}
		params := gcpgenserver.V1betaCreateBackupParams{LocationId: "valid-location-id", ProjectNumber: "proj", BackupVaultId: "vault"}
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		originalCreateClient := createClient
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			createClient = originalCreateClient
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		mockOrch.EXPECT().
			GetVolume(ctx, "vol-id", false).
			Return(nil, nil)

		// Mock ListBackups call that happens when volume is not found
		mockOrch.EXPECT().
			ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return([]*datamodel.Backup{}, nil)

		cvpBackupCreated := &backups.V1betaCreateBackupCreated{
			Payload: &models.BackupV1beta{BackupID: "backup-id", BackupRegion: strPtr("us-east4"), BackupType: "adhoc", Created: strfmt.DateTime(time.Now().UTC()), VolumeID: "vol-id"},
		}
		mockClient.EXPECT().
			V1betaCreateBackup(mock.Anything).
			Return(cvpBackupCreated, nil, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}
		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
	})

	t.Run("ReturnsAcceptedBackupWhenCVPBackupAccepted", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)
		mockClient := backups.NewMockClientService(t)
		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{VolumeId: "vol-id", ResourceId: "res-id"}
		params := gcpgenserver.V1betaCreateBackupParams{LocationId: "valid-location-id", ProjectNumber: "proj", BackupVaultId: "vault"}
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		originalCreateClient := createClient
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			createClient = originalCreateClient
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		mockOrch.EXPECT().
			GetVolume(ctx, "vol-id", false).
			Return(nil, nil)

		// Mock ListBackups call that happens when volume is not found
		mockOrch.EXPECT().
			ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return([]*datamodel.Backup{}, nil)

		cvpBackupAccepted := &backups.V1betaCreateBackupAccepted{
			Payload: &models.OperationV1beta{Name: "operation1"},
		}
		mockClient.EXPECT().
			V1betaCreateBackup(mock.Anything).
			Return(nil, cvpBackupAccepted, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}
		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
	})

	t.Run("ReturnsInternalServerErrorWhenUnexpectedFlow", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)
		mockClient := backups.NewMockClientService(t)
		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{VolumeId: "vol-id", ResourceId: "res-id"}
		params := gcpgenserver.V1betaCreateBackupParams{LocationId: "valid-location-id", ProjectNumber: "proj", BackupVaultId: "vault"}
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		originalCreateClient := createClient
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			createClient = originalCreateClient
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		mockOrch.EXPECT().
			GetVolume(ctx, "vol-id", false).
			Return(nil, nil)

		// Mock ListBackups call that happens when volume is not found
		mockOrch.EXPECT().
			ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return([]*datamodel.Backup{}, nil)

		mockClient.EXPECT().
			V1betaCreateBackup(mock.Anything).
			Return(nil, nil, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaCreateBackupInternalServerError).Code)
	})

	t.Run("ReturnsBadRequestWhenUserInputValidationError", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)
		mockClient := backups.NewMockClientService(t)
		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{VolumeId: "vol-id", ResourceId: "res-id"}
		params := gcpgenserver.V1betaCreateBackupParams{LocationId: "valid-location-id", ProjectNumber: "proj", BackupVaultId: "vault"}
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		originalCreateClient := createClient
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
			createClient = originalCreateClient
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}
		vol := &coremodels.Volume{
			BaseModel: coremodels.BaseModel{
				UUID:      "mock-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
				DeletedAt: nil,
			},
			PoolID:                "mock-pool-uuid",
			PoolName:              "mock-pool-name",
			AccountName:           "mock-account-name",
			DisplayName:           "mock-volume-name",
			Description:           "mock-description",
			QuotaInBytes:          1000000,
			LifeCycleState:        "READY",
			LifeCycleStateDetails: "All systems go",
			IsDataProtection:      false,
		}

		// Mock backup vault
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		mockOrch.EXPECT().
			GetVolume(ctx, "vol-id", false).
			Return(vol, errors.NewUserInputValidationErr("Invalid input parameters"))

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupBadRequest{}, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaCreateBackupBadRequest).Code)
	})
}

func TestV1betaDeleteBackupUnderBackupVault(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true
	t.Run("WhenParsingRegionAndZoneFails", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			LocationId: "invalid-location",
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{}, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest).Code)
	})
	t.Run("WhenBackupNotFoundInVSAAndSuccessInSDEWithJobNotDone", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, &backups.V1betaDeleteBackupUnderBackupVaultAccepted{
			Payload: &models.OperationV1beta{
				Done: nillable.ToPointer(false),
				Name: "/v1beta/projects/project-number/locations/us-east4/operations/job-id",
			},
		}, nil)

		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
		assert.False(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/job-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenBackupNotFoundInVSAAndSuccessInSDEWithJobDone", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(&backups.V1betaDeleteBackupUnderBackupVaultOK{
			Payload: &models.OperationV1beta{
				Done: nillable.ToPointer(true),
				Name: "job-id",
			},
		}, nil, nil)

		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
		assert.True(tt, result.(*gcpgenserver.OperationV1beta).Done.Value)
		assert.Equal(tt, "/v1beta/projects/project-number/locations/us-east4/operations/job-id", result.(*gcpgenserver.OperationV1beta).Name.Value)
	})
	t.Run("WhenBadRequestFromCVP", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, &backups.V1betaDeleteBackupUnderBackupVaultBadRequest{
			Payload: &models.Error{Code: 400, Message: "Bad Request"},
		})

		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.NotNil(tt, result)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{}, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest).Code)
		assert.Equal(tt, "Bad Request", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest).Message)
		assert.Nil(tt, err)
	})
	t.Run("WhenUnauthorizedErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		mockError := &backups.V1betaDeleteBackupUnderBackupVaultUnauthorized{
			Payload: &models.Error{
				Code:    401,
				Message: "Unauthorized",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnauthorized{}, result)
		assert.Equal(tt, float64(401), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnauthorized).Code)
		assert.Equal(tt, "Unauthorized", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnauthorized).Message)
	})
	t.Run("WhenForbiddenErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))
		mockError := &backups.V1betaDeleteBackupUnderBackupVaultForbidden{
			Payload: &models.Error{
				Code:    403,
				Message: "Forbidden",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultForbidden{}, result)
		assert.Equal(tt, float64(403), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultForbidden).Code)
		assert.Equal(tt, "Forbidden", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultForbidden).Message)
	})
	t.Run("WhenNotFoundErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))
		mockError := &backups.V1betaDeleteBackupUnderBackupVaultNotFound{
			Payload: &models.Error{
				Code:    404,
				Message: "Not Found",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound{}, result)
		assert.Equal(tt, float64(404), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound).Code)
		assert.Equal(tt, "Not Found", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound).Message)
	})
	t.Run("WhenBackupNotFoundAndUseVCPRegionEnabled_ShouldReturnNotFoundWithoutCVPFallback", func(tt *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound{}, result)
		assert.Equal(tt, float64(404), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound).Code)
		assert.Equal(tt, "Backup with ID backup-id not found", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotFound).Message)
	})
	t.Run("WhenInternalServerErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		mockError := &backups.V1betaDeleteBackupUnderBackupVaultInternalServerError{
			Payload: &models.Error{
				Code:    500,
				Message: "Internal Server Error",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{}, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Code)
		assert.Contains(tt, result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Message, "Internal Server Error")
	})
	t.Run("WhenConflictErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		mockError := &backups.V1betaDeleteBackupUnderBackupVaultConflict{
			Payload: &models.Error{
				Code:    409,
				Message: "Conflict",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultConflict{}, result)
		assert.Equal(tt, float64(409), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultConflict).Code)
		assert.Equal(tt, "Conflict", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultConflict).Message)
	})
	t.Run("WhenUnprocessableEntityErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		mockError := &backups.V1betaDeleteBackupUnderBackupVaultUnprocessableEntity{
			Payload: &models.Error{
				Code:    422,
				Message: "Unprocessable Entity",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnprocessableEntity{}, result)
		assert.Equal(tt, float64(422), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnprocessableEntity).Code)
		assert.Equal(tt, "Unprocessable Entity", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultUnprocessableEntity).Message)
	})
	t.Run("WhenTooManyRequestsErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		mockError := &backups.V1betaDeleteBackupUnderBackupVaultTooManyRequests{
			Payload: &models.Error{
				Code:    429,
				Message: "Too Many Requests",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultTooManyRequests{}, result)
		assert.Equal(tt, float64(429), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultTooManyRequests).Code)
		assert.Equal(tt, "Too Many Requests", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultTooManyRequests).Message)
	})
	t.Run("WhenNotImplementedErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		mockError := &backups.V1betaDeleteBackupUnderBackupVaultNotImplemented{
			Payload: &models.Error{
				Code:    501,
				Message: "Not Implemented",
			},
		}
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotImplemented{}, result)
		assert.Equal(tt, float64(501), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotImplemented).Code)
		assert.Equal(tt, "Not Implemented", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultNotImplemented).Message)
	})
	t.Run("WhenUnexpectedResponseFromCVPOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{}, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Code)
		assert.Equal(tt, "An unexpected error occurred while deleting the backup", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Message)
	})
	t.Run("WhenDefaultErrorOccurs", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		mockClient := backups.NewMockClientService(tt)
		handler := Handler{Orchestrator: mockOrchestrator}

		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))
		mockClient.EXPECT().V1betaDeleteBackupUnderBackupVault(mock.Anything).Return(nil, nil, fmt.Errorf("unexpected error"))

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{}, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Code)
		assert.Equal(tt, "unexpected error", result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Message)
	})
	t.Run("WhenGetBackupFailsWithError", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(&datamodel.Backup{}, errors.New("failed"))

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.NotNil(tt, result)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{}, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Code)
		assert.EqualError(tt, err, "failed")
	})
	t.Run("WhenDeleteBackupFailsInVSAWithUserValidationError", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackup(ctx, mock.Anything).Return(nil, "", errors.NewUserInputValidationErr("failed"))

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.NotNil(tt, result)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{}, result)
		assert.Equal(tt, float64(400), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest).Code)
		assert.Nil(tt, err)
	})
	t.Run("WhenDeleteBackupFailsInVSAWithInternalServerError", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "us-east4",
			ProjectNumber: "project-number",
		}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackup(ctx, mock.Anything).Return(nil, "", fmt.Errorf("VSA error"))

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.NotNil(tt, result)
		assert.IsType(tt, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError{}, result)
		assert.Equal(tt, float64(500), result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultInternalServerError).Code)
		assert.EqualError(tt, err, "VSA error")
	})
	t.Run("WhenDeleteBackupSuccessInVSA", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "valid-location",
			ProjectNumber: "project-number",
		}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackup(ctx, mock.Anything).Return(nil, "job-id", nil)

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
	})
	t.Run("WhenDeleteBackupSuccessInVSAWithEmptyJobId", func(tt *testing.T) {
		ctx := context.Background()
		mockOrchestrator := factory.NewMockOrchestratorFactory(tt)
		handler := Handler{Orchestrator: mockOrchestrator}
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{
			BackupVaultId: "vault-id",
			BackupId:      "backup-id",
			LocationId:    "valid-location",
			ProjectNumber: "project-number",
		}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(&datamodel.Backup{}, nil)
		mockOrchestrator.EXPECT().DeleteBackup(ctx, mock.Anything).Return(nil, "", nil) // Empty jobId

		result, err := handler.V1betaDeleteBackupUnderBackupVault(ctx, params)
		assert.Nil(tt, err)
		assert.IsType(tt, &gcpgenserver.OperationV1beta{}, result)
		operation := result.(*gcpgenserver.OperationV1beta)
		assert.True(tt, operation.Done.Value) // Should be done when jobId is empty
		assert.Contains(tt, operation.Name.Value, "/v1beta/projects/project-number/locations/valid-location/operations/")
	})
}

func TestFetchBackupUUIDWhichAreNotPartOfListBackups(t *testing.T) {
	t.Run("WhenAllUUIDsArePartOfListBackups", func(t *testing.T) {
		listBackups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "uuid1"}},
			{BaseModel: datamodel.BaseModel{UUID: "uuid2"}},
		}
		backupUUIDs := []string{"uuid1", "uuid2"}
		var expectedUUIDs []string

		result := fetchBackupUUIDWhichAreNotPartOfListBackups(listBackups, backupUUIDs)

		assert.Equal(t, expectedUUIDs, result)
	})

	t.Run("WhenSomeUUIDsAreNotPartOfListBackups", func(t *testing.T) {
		listBackups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "uuid1"}},
			{BaseModel: datamodel.BaseModel{UUID: "uuid2"}},
		}
		backupUUIDs := []string{"uuid1", "uuid2", "uuid3"}
		expectedUUIDs := []string{"uuid3"}

		result := fetchBackupUUIDWhichAreNotPartOfListBackups(listBackups, backupUUIDs)

		assert.Equal(t, expectedUUIDs, result)
	})

	t.Run("WhenListBackupsIsEmpty", func(t *testing.T) {
		listBackups := []*datamodel.Backup{}
		backupUUIDs := []string{"uuid1", "uuid2"}
		expectedUUIDs := []string{"uuid1", "uuid2"}

		result := fetchBackupUUIDWhichAreNotPartOfListBackups(listBackups, backupUUIDs)

		assert.Equal(t, expectedUUIDs, result)
	})

	t.Run("WhenBackupUUIDsIsEmpty", func(t *testing.T) {
		listBackups := []*datamodel.Backup{
			{BaseModel: datamodel.BaseModel{UUID: "uuid1"}},
			{BaseModel: datamodel.BaseModel{UUID: "uuid2"}},
		}
		backupUUIDs := []string{}
		var expectedUUIDs []string

		result := fetchBackupUUIDWhichAreNotPartOfListBackups(listBackups, backupUUIDs)

		assert.Equal(t, expectedUUIDs, result)
	})
}

func TestListBackupsToCVP(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	ctx := context.Background()
	params := gcpgenserver.V1betaListBackupsParams{
		BackupVaultId:  "vault-id",
		LocationId:     "location-id",
		ProjectNumber:  "project-number",
		XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
	}

	t.Run("WhenListBackupsFailsWithInternalError", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsInternalServerError{
			Payload: &models.Error{
				Code:    500,
				Message: "Internal Server Error",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.Error(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaListBackupsInternalServerError).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaListBackupsInternalServerError).Message, "Internal Server Error")
	})
	t.Run("WhenListBackupsFailsWithNotImplemented", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsNotImplemented{
			Payload: &models.Error{
				Code:    501,
				Message: "Not Implemented",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsNotImplemented{}, result)
		assert.Equal(t, float64(501), result.(*gcpgenserver.V1betaListBackupsNotImplemented).Code)
		assert.Equal(t, "Not Implemented", result.(*gcpgenserver.V1betaListBackupsNotImplemented).Message)
	})
	t.Run("WhenListBackupsFailsWithError", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsNotFound{
			Payload: &models.Error{
				Code:    404,
				Message: "Backups Not Found Error",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsNotFound{}, result)
		assert.Equal(t, float64(404), result.(*gcpgenserver.V1betaListBackupsNotFound).Code)
		assert.Equal(t, "Backups Not Found Error", result.(*gcpgenserver.V1betaListBackupsNotFound).Message)
	})
	t.Run("WhenListBackupsSucceeds", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockResponse := &backups.V1betaListBackupsOK{
			Payload: &backups.V1betaListBackupsOKBody{
				Backups: []*models.BackupV1beta{
					{BackupID: "backup-id-1"},
					{BackupID: "backup-id-2"},
				},
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(mockResponse, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)
		assert.Len(t, result.(*gcpgenserver.V1betaListBackupsOK).Backups, 2)
	})

	t.Run("WhenListBackupsReturnsUnexpectedResponse", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, errors.New("unexpected error"))

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.Error(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaListBackupsInternalServerError).Code)
		assert.Equal(t, "unexpected error", result.(*gcpgenserver.V1betaListBackupsInternalServerError).Message)
	})
	t.Run("WhenListBackupsFailsWithBadRequest", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsBadRequest{
			Payload: &models.Error{
				Code:    400,
				Message: "Bad Request",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsBadRequest{}, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaListBackupsBadRequest).Code)
		assert.Equal(t, "Bad Request", result.(*gcpgenserver.V1betaListBackupsBadRequest).Message)
	})

	t.Run("WhenListBackupsFailsWithUnauthorized", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsUnauthorized{
			Payload: &models.Error{
				Code:    401,
				Message: "Unauthorized",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsUnauthorized{}, result)
		assert.Equal(t, float64(401), result.(*gcpgenserver.V1betaListBackupsUnauthorized).Code)
		assert.Equal(t, "Unauthorized", result.(*gcpgenserver.V1betaListBackupsUnauthorized).Message)
	})

	t.Run("WhenListBackupsFailsWithForbidden", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsForbidden{
			Payload: &models.Error{
				Code:    403,
				Message: "Forbidden",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsForbidden{}, result)
		assert.Equal(t, float64(403), result.(*gcpgenserver.V1betaListBackupsForbidden).Code)
		assert.Equal(t, "Forbidden", result.(*gcpgenserver.V1betaListBackupsForbidden).Message)
	})

	t.Run("WhenListBackupsFailsWithTooManyRequests", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsTooManyRequests{
			Payload: &models.Error{
				Code:    429,
				Message: "Too Many Requests",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsTooManyRequests{}, result)
		assert.Equal(t, float64(429), result.(*gcpgenserver.V1betaListBackupsTooManyRequests).Code)
		assert.Equal(t, "Too Many Requests", result.(*gcpgenserver.V1betaListBackupsTooManyRequests).Message)
	})

	t.Run("WhenListBackupsFailsWithNotFound", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsNotFound{
			Payload: &models.Error{
				Code:    404,
				Message: "Not Found",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsNotFound{}, result)
		assert.Equal(t, float64(404), result.(*gcpgenserver.V1betaListBackupsNotFound).Code)
		assert.Equal(t, "Not Found", result.(*gcpgenserver.V1betaListBackupsNotFound).Message)
	})

	t.Run("WhenListBackupsFailsWithInternalServerError", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaListBackupsInternalServerError{
			Payload: &models.Error{
				Code:    500,
				Message: "Internal Server Error",
			},
		}
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.Error(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaListBackupsInternalServerError).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaListBackupsInternalServerError).Message, "Internal Server Error")
	})

	t.Run("WhenListBackupsFailsWithDefaultError", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockClient.EXPECT().V1betaListBackups(mock.Anything).Return(nil, fmt.Errorf("unexpected error"))

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, params)
		assert.Error(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaListBackupsInternalServerError).Code)
		assert.Equal(t, "unexpected error", result.(*gcpgenserver.V1betaListBackupsInternalServerError).Message)
	})

	t.Run("WhenFilteringByVolumeId_ForwardsToCVP", func(t *testing.T) {
		volumeID := "test-vol-1"
		volumeParams := params
		volumeParams.VolumeId = gcpgenserver.NewOptString(volumeID)

		mockClient := backups.NewMockClientService(t)
		mockClient.EXPECT().V1betaListBackups(mock.MatchedBy(func(p *backups.V1betaListBackupsParams) bool {
			return p != nil && p.VolumeID == volumeID
		})).Return(&backups.V1betaListBackupsOK{
			Payload: &backups.V1betaListBackupsOKBody{Backups: []*models.BackupV1beta{}},
		}, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := listBackupsToCVP(ctx, volumeParams)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)
	})
}

func TestGetBackupToCVP(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	ctx := context.Background()
	params := gcpgenserver.V1betaDescribeBackupParams{
		BackupVaultId:  "vault-id",
		BackupId:       "backup-id",
		LocationId:     "location-id",
		ProjectNumber:  "project-number",
		XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
	}

	t.Run("WhenDescribeBackupFailsWithBadRequest", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaDescribeBackupBadRequest{
			Payload: &models.Error{
				Code:    400,
				Message: "Bad Request",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupBadRequest{}, result)
		assert.Equal(t, float64(400), result.(*gcpgenserver.V1betaDescribeBackupBadRequest).Code)
		assert.Equal(t, "Bad Request", result.(*gcpgenserver.V1betaDescribeBackupBadRequest).Message)
	})

	t.Run("WhenDescribeBackupFailsWithUnauthorized", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaDescribeBackupUnauthorized{
			Payload: &models.Error{
				Code:    401,
				Message: "Unauthorized",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupUnauthorized{}, result)
		assert.Equal(t, float64(401), result.(*gcpgenserver.V1betaDescribeBackupUnauthorized).Code)
		assert.Equal(t, "Unauthorized", result.(*gcpgenserver.V1betaDescribeBackupUnauthorized).Message)
	})

	t.Run("WhenDescribeBackupFailsWithForbidden", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaDescribeBackupForbidden{
			Payload: &models.Error{
				Code:    403,
				Message: "Forbidden",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupForbidden{}, result)
		assert.Equal(t, float64(403), result.(*gcpgenserver.V1betaDescribeBackupForbidden).Code)
		assert.Equal(t, "Forbidden", result.(*gcpgenserver.V1betaDescribeBackupForbidden).Message)
	})

	t.Run("WhenDescribeBackupFailsWithNotFound", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaDescribeBackupNotFound{
			Payload: &models.Error{
				Code:    404,
				Message: "Not Found",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupNotFound{}, result)
		assert.Equal(t, float64(404), result.(*gcpgenserver.V1betaDescribeBackupNotFound).Code)
		assert.Equal(t, "Not Found", result.(*gcpgenserver.V1betaDescribeBackupNotFound).Message)
	})

	t.Run("WhenDescribeBackupFailsWithInternalServerError", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaDescribeBackupInternalServerError{
			Payload: &models.Error{
				Code:    500,
				Message: "Internal Server Error",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.Error(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaDescribeBackupInternalServerError).Code)
		assert.Contains(t, result.(*gcpgenserver.V1betaDescribeBackupInternalServerError).Message, "Internal Server Error")
	})

	t.Run("WhenDescribeBackupFailsWithTooManyRequests", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaDescribeBackupTooManyRequests{
			Payload: &models.Error{
				Code:    429,
				Message: "Too Many Requests",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupTooManyRequests{}, result)
		assert.Equal(t, float64(429), result.(*gcpgenserver.V1betaDescribeBackupTooManyRequests).Code)
		assert.Equal(t, "Too Many Requests", result.(*gcpgenserver.V1betaDescribeBackupTooManyRequests).Message)
	})

	t.Run("WhenDescribeBackupFailsWithNotImplemented", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockError := &backups.V1betaDescribeBackupNotImplemented{
			Payload: &models.Error{
				Code:    501,
				Message: "Not Implemented",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, mockError)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupNotImplemented{}, result)
		assert.Equal(t, float64(501), result.(*gcpgenserver.V1betaDescribeBackupNotImplemented).Code)
		assert.Equal(t, "Not Implemented", result.(*gcpgenserver.V1betaDescribeBackupNotImplemented).Message)
	})

	t.Run("WhenDescribeBackupFailsWithDefaultError", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, fmt.Errorf("unexpected error"))

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.Error(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupInternalServerError{}, result)
		assert.Equal(t, float64(500), result.(*gcpgenserver.V1betaDescribeBackupInternalServerError).Code)
		assert.Equal(t, "unexpected error", result.(*gcpgenserver.V1betaDescribeBackupInternalServerError).Message)
	})
	t.Run("WhenBackupIsNil", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(nil, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupInternalServerError{}, result)
		assert.Equal(t, "An unexpected error occurred while listing the backups", result.(*gcpgenserver.V1betaDescribeBackupInternalServerError).Message)
	})

	t.Run("WhenBackupPayloadIsNil", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockBackup := &backups.V1betaDescribeBackupOK{Payload: nil}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(mockBackup, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupInternalServerError{}, result)
		assert.Equal(t, "An unexpected error occurred while listing the backups", result.(*gcpgenserver.V1betaDescribeBackupInternalServerError).Message)
	})

	t.Run("WhenBackupPayloadIsValid", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)
		mockBackup := &backups.V1betaDescribeBackupOK{
			Payload: &models.BackupV1beta{
				ResourceID: "resource-id",
			},
		}
		mockClient.EXPECT().V1betaDescribeBackup(mock.Anything).Return(mockBackup, nil)

		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockClient}
		}

		result, err := getBackupsFromCVP(ctx, params)
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupOK{}, result)
		assert.Equal(t, "resource-id", result.(*gcpgenserver.V1betaDescribeBackupOK).Backups[0].ResourceId.Value)
	})

	t.Run("WhenListBackupsIsEmpty", func(t *testing.T) {
		listBackups := []*datamodel.Backup{}
		backupUUIDs := []string{"uuid1", "uuid2"}

		result := fetchBackupUUIDWhichAreNotPartOfListBackups(listBackups, backupUUIDs)
		assert.Equal(t, backupUUIDs, result)
	})
}

func TestV1betaDescribeBackup(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	ctx := context.Background()

	t.Run("WhenBackupIsFoundInOrchestrator", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		sourceRegionName := "us-east4"
		handler := Handler{Orchestrator: mockOrchestrator}
		backupVault := &datamodel.BackupVault{
			Name:             "test-backup-vault",
			SourceRegionName: &sourceRegionName,
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		backup := &datamodel.Backup{
			State:         "InProgress",
			Name:          "backup-123",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes:    &datamodel.BackupAttributes{},
			AssetMetadata: &datamodel.AssetMetadata{
				ChildAssets: []datamodel.ChildAsset{
					{
						AssetType:  common.BackupAssetType,
						AssetNames: []string{"//storage.googleapis.com/backup-123"},
					},
				},
			},
		}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(backup, nil)

		params := gcpgenserver.V1betaDescribeBackupParams{
			BackupVaultId: "vault-123",
			BackupId:      "backup-123",
			ProjectNumber: "project-123",
		}

		resp, err := handler.V1betaDescribeBackup(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, "backup-123", resp.(*gcpgenserver.V1betaDescribeBackupOK).Backups[0].ResourceId.Value)
	})

	t.Run("WhenBackupIsNotFoundInOrchestratorButFoundInCVP", func(t *testing.T) {
		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		handler := Handler{Orchestrator: mockOrchestrator}

		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		params := gcpgenserver.V1betaDescribeBackupParams{
			BackupVaultId: "vault-123",
			BackupId:      "backup-123",
			ProjectNumber: "project-123",
		}

		// Mock CVP client behavior here if needed

		resp, err := handler.V1betaDescribeBackup(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("WhenBackupNotFoundAndUseVCPRegionEnabled_ShouldReturnNotFound", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}

		handler := Handler{Orchestrator: mockOrchestrator}
		mockOrchestrator.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.NewNotFoundErr("backup", nil))

		params := gcpgenserver.V1betaDescribeBackupParams{
			BackupVaultId: "vault-123",
			BackupId:      "backup-123",
			ProjectNumber: "project-123",
		}

		resp, err := handler.V1betaDescribeBackup(ctx, params)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupNotFound{}, resp)
		assert.Equal(t, float64(404), resp.(*gcpgenserver.V1betaDescribeBackupNotFound).Code)
		assert.Equal(t, "Backup with ID backup-123 not found", resp.(*gcpgenserver.V1betaDescribeBackupNotFound).Message)
	})

	t.Run("WhenInternalServerErrorOccurs", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		defer func() {
			utilParseAndValidateRegionAndZone = utils.ParseAndValidateRegionAndZone
		}()
		utilParseAndValidateRegionAndZone = func(locationId string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "", nil
		}
		handler := Handler{Orchestrator: mockOrch}

		mockOrch.EXPECT().GetBackup(ctx, mock.Anything).Return(nil, errors.New("internal server error"))

		params := gcpgenserver.V1betaDescribeBackupParams{
			BackupVaultId: "vault-123",
			BackupId:      "backup-123",
			ProjectNumber: "project-123",
		}

		resp, err := handler.V1betaDescribeBackup(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.IsType(t, &gcpgenserver.V1betaDescribeBackupInternalServerError{}, resp)
	})
}

// Mock for listBackupsToCVP
var mockListBackupsToCVP = func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
	return nil, nil
}

func Test_checkIfBackupExistInCVP(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// Save original function and restore after test
	originalListBackupsToCVP := listBackupsToCVP
	defer func() { listBackupsToCVP = originalListBackupsToCVP }()
	listBackupsToCVP = mockListBackupsToCVP

	ctx := context.Background()
	backupID := "test-backup-id"
	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  "vault-id",
		LocationId:     "location-id",
		ProjectNumber:  "project-number",
		XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
	}

	t.Run("listBackupsToCVP returns error", func(t *testing.T) {
		listBackupsToCVP = func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return nil, errors.New("mock error")
		}

		exists, err := _checkIfBackupExistInCVP(ctx, &backupID, params)
		assert.False(t, exists)
		assert.Error(t, err)
		assert.Equal(t, "mock error", err.Error())
	})

	t.Run("listBackupsToCVP returns SDE error response (e.g. BadRequest)", func(t *testing.T) {
		listBackupsToCVP = func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsBadRequest{Code: 400, Message: "bad request"}, nil
		}

		exists, err := _checkIfBackupExistInCVP(ctx, &backupID, params)
		assert.False(t, exists)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected response type")
	})

	t.Run("backup ID exists in response", func(t *testing.T) {
		listBackupsToCVP = func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{
					{ResourceId: utils.GetOptString(&backupID)},
				},
			}, nil
		}

		exists, err := _checkIfBackupExistInCVP(ctx, &backupID, params)
		assert.True(t, exists)
		assert.NoError(t, err)
	})

	t.Run("backup ID does not exist in response", func(t *testing.T) {
		listBackupsToCVP = func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{
					{ResourceId: utils.GetOptString(nillable.GetStringPtr("other-backup-id"))},
				},
			}, nil
		}

		exists, err := _checkIfBackupExistInCVP(ctx, &backupID, params)
		assert.False(t, exists)
		assert.NoError(t, err)
	})
}

func TestV1betaListBackups(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenBackupVaultExistsInVSAAndListBackupsSucceeds", func(t *testing.T) {
		ctx := context.Background()
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock backup vault (needed for GCBDR check)
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: params.BackupVaultId,
			AccountName:   params.ProjectNumber,
			ServiceType:   "GCNV",
		}
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(backupVault, nil)

		// Mock ListBackups to return VSA backups
		backupVaultData := &datamodel.BackupVault{
			Name:             "test-backup-vault",
			SourceRegionName: nillable.ToPointer("us-east4"),
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		vsaBackups := []*datamodel.Backup{
			{
				State:         "InProgress",
				Name:          "vsa-backup-1",
				VolumeUUID:    "test-vol-1",
				BackupVault:   backupVaultData,
				BackupVaultID: 1,
				Attributes:    &datamodel.BackupAttributes{},
			},
			{
				State:         "Available",
				Name:          "vsa-backup-2",
				VolumeUUID:    "test-vol-2",
				BackupVault:   backupVaultData,
				BackupVaultID: 1,
				Attributes:    &datamodel.BackupAttributes{},
			},
		}
		mockOrchestrator.EXPECT().
			ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, mock.Anything).
			Return(vsaBackups, nil)

		// Mock CVP response
		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{
					{ResourceId: gcpgenserver.NewOptString("cvp-backup-1")},
					{ResourceId: gcpgenserver.NewOptString("cvp-backup-2")},
				},
			}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)

		response := result.(*gcpgenserver.V1betaListBackupsOK)
		assert.Len(t, response.Backups, 4) // 2 VSA + 2 CVP backups

		// Check that VSA backups are included
		assert.Equal(t, "vsa-backup-1", response.Backups[0].ResourceId.Value)
		assert.Equal(t, "vsa-backup-2", response.Backups[1].ResourceId.Value)

		// Check that CVP backups are included
		assert.Equal(t, "cvp-backup-1", response.Backups[2].ResourceId.Value)
		assert.Equal(t, "cvp-backup-2", response.Backups[3].ResourceId.Value)
	})

	t.Run("WhenBackupVaultNotFoundInVSA", func(t *testing.T) {
		ctx := context.Background()
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock GetBackupVaultByUUIDWithoutAccount to return NotFoundErr (vault doesn't exist in VSA)
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(nil, errors.NewNotFoundErr("backup vault", nil))

		// Mock CVP response
		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{
					{ResourceId: gcpgenserver.NewOptString("cvp-backup-1")},
				},
			}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)

		response := result.(*gcpgenserver.V1betaListBackupsOK)
		assert.Len(t, response.Backups, 1) // Only CVP backups
		assert.Equal(t, "cvp-backup-1", response.Backups[0].ResourceId.Value)
	})

	t.Run("WhenBackupVaultExistsButListBackupsFails", func(t *testing.T) {
		ctx := context.Background()
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock backup vault (needed for GCBDR check)
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: params.BackupVaultId,
			AccountName:   params.ProjectNumber,
			ServiceType:   "GCNV",
		}
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(backupVault, nil)

		// Mock ListBackups to return error
		mockOrchestrator.EXPECT().
			ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, mock.Anything).
			Return(nil, fmt.Errorf("failed to list backups"))

		// Mock CVP response
		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{},
			}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.Error(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsInternalServerError{}, result)

		internalError := result.(*gcpgenserver.V1betaListBackupsInternalServerError)
		assert.Equal(t, float64(500), internalError.Code)
		assert.Equal(t, "failed to list backups", internalError.Message)
	})

	t.Run("WhenBackupVaultExistsAndListBackupsReturnsEmpty", func(t *testing.T) {
		ctx := context.Background()
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock backup vault (needed for GCBDR check)
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: params.BackupVaultId,
			AccountName:   params.ProjectNumber,
			ServiceType:   "GCNV",
		}
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(backupVault, nil)

		// Mock ListBackups to return empty list
		mockOrchestrator.EXPECT().
			ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, mock.Anything).
			Return([]*datamodel.Backup{}, nil)

		// Mock CVP response
		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{
					{ResourceId: gcpgenserver.NewOptString("cvp-backup-1")},
				},
			}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)

		response := result.(*gcpgenserver.V1betaListBackupsOK)
		assert.Len(t, response.Backups, 1) // Only CVP backups (VSA list is empty)
		assert.Equal(t, "cvp-backup-1", response.Backups[0].ResourceId.Value)
	})

	t.Run("WhenListBackupsToCVPFails", func(t *testing.T) {
		ctx := context.Background()
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock GetBackupVaultByUUIDWithoutAccount - vault not found (will go to CVP)
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(nil, errors.NewNotFoundErr("backup vault", nil))

		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return nil, errors.New("failed to list backups")
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.Error(t, err)
		assert.Nil(t, result)
	})

	t.Run("WhenListBackupsToCVPReturnsUnexpectedResponseType", func(t *testing.T) {
		ctx := context.Background()
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock GetBackupVaultByUUIDWithoutAccount - vault not found (will go to CVP)
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(nil, errors.NewNotFoundErr("backup vault", nil))

		// Mock CVP to return an error response instead of OK
		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			return &gcpgenserver.V1betaListBackupsBadRequest{
				Code:    400,
				Message: "Bad Request",
			}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsBadRequest{}, result)

		badRequest := result.(*gcpgenserver.V1betaListBackupsBadRequest)
		assert.Equal(t, float64(400), badRequest.Code)
		assert.Equal(t, "Bad Request", badRequest.Message)
	})

	t.Run("WhenFilteringByBackupName_BackupFoundInVCP", func(t *testing.T) {
		ctx := context.Background()
		backupName := "test-backup-name"
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupName:     gcpgenserver.NewOptString(backupName),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock backup vault (needed for GCBDR check)
		backupVaultModel := &coremodels.BackupVaultV1beta{
			BackupVaultID: params.BackupVaultId,
			AccountName:   params.ProjectNumber,
			ServiceType:   "GCNV",
		}
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(backupVaultModel, nil)

		// Mock ListBackups to verify it's called with the correct filter and returns backup from VCP
		backupVaultData := &datamodel.BackupVault{
			Name:             "test-backup-vault",
			SourceRegionName: nillable.ToPointer("us-east4"),
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket", ServiceAccountName: "sa-test", VendorSubnetID: "subnet-12345"}},
		}
		filteredBackups := []*datamodel.Backup{
			{
				State:         "Available",
				Name:          backupName,
				VolumeUUID:    "test-vol-1",
				BackupVault:   backupVaultData,
				BackupVaultID: 1,
				Attributes:    &datamodel.BackupAttributes{},
			},
		}

		// Verify ListBackups is called with the correct filter
		mockOrchestrator.EXPECT().
			ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, [][]interface{}{{"name = ?", backupName}}).
			Return(filteredBackups, nil)

		// Mock CVP response - backup not found in CVP (returns empty list)
		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			// Verify that BackupName is passed to CVP
			assert.True(t, params.BackupName.IsSet())
			assert.Equal(t, backupName, params.BackupName.Value)
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{},
			}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)

		response := result.(*gcpgenserver.V1betaListBackupsOK)
		// Should have only 1 backup from VCP (backup cannot exist in both VCP and CVP)
		assert.Len(t, response.Backups, 1)
		// Verify VCP backup matches the filter
		assert.Equal(t, backupName, response.Backups[0].ResourceId.Value)
	})

	t.Run("WhenFilteringByBackupName_BackupFoundInCVP", func(t *testing.T) {
		ctx := context.Background()
		backupName := "test-backup-name"
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			BackupName:     gcpgenserver.NewOptString(backupName),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		// Mock GetBackupVaultByUUIDWithoutAccount to return NotFoundErr (vault doesn't exist in VCP, so backup is in CVP)
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(nil, errors.NewNotFoundErr("backup vault", nil))

		// Mock CVP response with BackupName filter - backup found in CVP
		mockListBackupsToCVP := func(ctx context.Context, params gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			// Verify that BackupName is passed to CVP
			assert.True(t, params.BackupName.IsSet())
			assert.Equal(t, backupName, params.BackupName.Value)
			return &gcpgenserver.V1betaListBackupsOK{
				Backups: []gcpgenserver.BackupV1beta{
					{ResourceId: gcpgenserver.NewOptString(backupName)},
				},
			}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)

		response := result.(*gcpgenserver.V1betaListBackupsOK)
		// Should have only 1 backup from CVP (backup cannot exist in both VCP and CVP)
		assert.Len(t, response.Backups, 1)
		// Verify CVP backup matches the filter
		assert.Equal(t, backupName, response.Backups[0].ResourceId.Value)
	})

	t.Run("WhenFilteringByVolumeId_AppliedToVCPAndForwardedToCVP", func(t *testing.T) {
		ctx := context.Background()
		volumeID := "test-vol-1"
		params := gcpgenserver.V1betaListBackupsParams{
			BackupVaultId:  "test-vault-id",
			LocationId:     "test-location-id",
			ProjectNumber:  "test-project-number",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
			VolumeId:       gcpgenserver.NewOptString(volumeID),
		}

		mockOrchestrator := factory.NewMockOrchestratorFactory(t)

		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: params.BackupVaultId,
			AccountName:   params.ProjectNumber,
			ServiceType:   "GCNV",
		}
		mockOrchestrator.EXPECT().
			GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
			Return(backupVault, nil)

		backupVaultData := &datamodel.BackupVault{
			Name:             "test-backup-vault",
			SourceRegionName: nillable.ToPointer("us-east4"),
			BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "test-bucket"}},
		}
		vsaBackups := []*datamodel.Backup{
			{
				State:         "Available",
				Name:          "vsa-backup-1",
				VolumeUUID:    volumeID,
				BackupVault:   backupVaultData,
				BackupVaultID: 1,
				Attributes:    &datamodel.BackupAttributes{},
			},
		}
		// Capture filters to verify the volume_uuid filter is applied to the VCP DB query.
		mockOrchestrator.EXPECT().
			ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, mock.Anything).
			RunAndReturn(func(_ context.Context, _, _ string, filters [][]interface{}) ([]*datamodel.Backup, error) {
				assert.Contains(t, filters, []interface{}{"volume_uuid = ?", volumeID})
				return vsaBackups, nil
			})

		// Verify volumeId is forwarded to CVP.
		mockListBackupsToCVP := func(_ context.Context, cvpParams gcpgenserver.V1betaListBackupsParams) (gcpgenserver.V1betaListBackupsRes, error) {
			assert.True(t, cvpParams.VolumeId.IsSet())
			assert.Equal(t, volumeID, cvpParams.VolumeId.Value)
			return &gcpgenserver.V1betaListBackupsOK{Backups: []gcpgenserver.BackupV1beta{}}, nil
		}

		originalListBackupsToCVP := listBackupsToCVP
		defer func() { listBackupsToCVP = originalListBackupsToCVP }()
		listBackupsToCVP = mockListBackupsToCVP

		handler := Handler{Orchestrator: mockOrchestrator}
		result, err := handler.V1betaListBackups(ctx, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)

		response := result.(*gcpgenserver.V1betaListBackupsOK)
		assert.Len(t, response.Backups, 1)
		assert.Equal(t, "vsa-backup-1", response.Backups[0].ResourceId.Value)
	})
}

// TestV1betaCreateBackup_BackupDisabled tests the scenario where backup creation is disabled.
func TestV1betaCreateBackup_BackupDisabled(t *testing.T) {
	// Save and restore the original value
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = false

	handler := Handler{}
	req := &gcpgenserver.BackupCreateV1beta{}
	params := gcpgenserver.V1betaCreateBackupParams{}

	result, err := handler.V1betaCreateBackup(context.Background(), req, params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.V1betaCreateBackupBadRequest{}, result)
	badRequest := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Equal(t, "Backup feature is currently not enabled.", badRequest.Message)
}

// TestV1betaDeleteBackupUnderBackupVault_BackupDisabled tests the scenario where backup deletion is disabled.
func TestV1betaDeleteBackupUnderBackupVault_BackupDisabled(t *testing.T) {
	// Save and restore the original value
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = false

	handler := Handler{}
	params := gcpgenserver.V1betaDeleteBackupUnderBackupVaultParams{}

	result, err := handler.V1betaDeleteBackupUnderBackupVault(context.Background(), params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest{}, result)
	badRequest := result.(*gcpgenserver.V1betaDeleteBackupUnderBackupVaultBadRequest)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Equal(t, "Backup feature is currently not enabled.", badRequest.Message)
}

// TestV1betaGetMultipleBackups_BackupDisabled tests the scenario where fetching multiple backups is disabled.
func TestV1betaGetMultipleBackups_BackupDisabled(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = false

	handler := Handler{}
	req := &gcpgenserver.BackupUuidListV1beta{}
	params := gcpgenserver.V1betaGetMultipleBackupsParams{}

	result, err := handler.V1betaGetMultipleBackups(context.Background(), req, params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.V1betaGetMultipleBackupsBadRequest{}, result)
	badRequest := result.(*gcpgenserver.V1betaGetMultipleBackupsBadRequest)
	assert.Equal(t, float64(400), badRequest.Code)
	assert.Equal(t, "Backup feature is currently not enabled.", badRequest.Message)
}

// Unit tests for updateBackupToCVP function
func TestUpdateBackupToCVP(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	t.Run("WhenUpdateBackupToCVPSucceeds", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock success response
		resourceID := "test-resource-id"
		mockResponse := &backups.V1betaUpdateBackupOK{
			Payload: &models.BackupV1beta{
				ResourceID: resourceID,
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.MatchedBy(func(p *backups.V1betaUpdateBackupParams) bool {
				return p.BackupVaultID == params.BackupVaultId &&
					p.BackupID == params.BackupId &&
					p.LocationID == params.LocationId &&
					p.ProjectNumber == params.ProjectNumber &&
					*p.Body.Description == "updated-description"
			})).
			Return(mockResponse, nil, nil, nil)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.True(t, operationResult.Done.Value)
	})

	t.Run("WhenUpdateBackupToCVPSucceedsWithEmptyDescription", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters with empty description
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock success response
		resourceID := "test-resource-id"
		mockResponse := &backups.V1betaUpdateBackupOK{
			Payload: &models.BackupV1beta{
				ResourceID: resourceID,
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.MatchedBy(func(p *backups.V1betaUpdateBackupParams) bool {
				return p.BackupVaultID == params.BackupVaultId &&
					p.BackupID == params.BackupId &&
					*p.Body.Description == ""
			})).
			Return(mockResponse, nil, nil, nil)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)
	})

	t.Run("WhenUpdateBackupToCVPReturnsBadRequest", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error response
		errorCode := float64(400)
		errorMessage := "Bad Request"
		mockError := &backups.V1betaUpdateBackupBadRequest{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupBadRequest{}, result)

		badRequestResult := result.(*gcpgenserver.V1betaUpdateBackupBadRequest)
		assert.Equal(t, errorCode, badRequestResult.Code)
		assert.Equal(t, errorMessage, badRequestResult.Message)
	})

	t.Run("WhenUpdateBackupToCVPReturnsUnauthorized", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error response
		errorCode := float64(401)
		errorMessage := "Unauthorized"
		mockError := &backups.V1betaUpdateBackupUnauthorized{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupUnauthorized{}, result)

		unauthorizedResult := result.(*gcpgenserver.V1betaUpdateBackupUnauthorized)
		assert.Equal(t, errorCode, unauthorizedResult.Code)
		assert.Equal(t, errorMessage, unauthorizedResult.Message)
	})

	t.Run("WhenUpdateBackupToCVPReturnsForbidden", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error response
		errorCode := float64(403)
		errorMessage := "Forbidden"
		mockError := &backups.V1betaUpdateBackupForbidden{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupForbidden{}, result)

		forbiddenResult := result.(*gcpgenserver.V1betaUpdateBackupForbidden)
		assert.Equal(t, errorCode, forbiddenResult.Code)
		assert.Equal(t, errorMessage, forbiddenResult.Message)
	})

	t.Run("WhenUpdateBackupToCVPReturnsNotFound", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error response
		errorCode := float64(404)
		errorMessage := "Not Found"
		mockError := &backups.V1betaUpdateBackupNotFound{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupNotFound{}, result)

		notFoundResult := result.(*gcpgenserver.V1betaUpdateBackupNotFound)
		assert.Equal(t, errorCode, notFoundResult.Code)
		assert.Equal(t, errorMessage, notFoundResult.Message)
	})

	t.Run("WhenUpdateBackupToCVPReturnsInternalServerError", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock error response
		errorCode := float64(500)
		errorMessage := "Internal Server Error"
		mockError := &backups.V1betaUpdateBackupInternalServerError{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupInternalServerError{}, result)

		internalServerErrorResult := result.(*gcpgenserver.V1betaUpdateBackupInternalServerError)
		assert.Equal(t, errorCode, internalServerErrorResult.Code)
		assert.Contains(t, internalServerErrorResult.Message, errorMessage)
	})

	t.Run("WhenUpdateBackupToCVPReturnsUnprocessableEntity", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		errorCode := float64(422)
		errorMessage := "Unprocessable Entity"
		mockError := &backups.V1betaUpdateBackupUnprocessableEntity{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		result, err := updateBackupToCVP(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupUnprocessableEntity{}, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaUpdateBackupUnprocessableEntity).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaUpdateBackupUnprocessableEntity).Message)
	})

	t.Run("WhenUpdateBackupToCVPReturnsTooManyRequests", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		errorCode := float64(429)
		errorMessage := "Too Many Requests"
		mockError := &backups.V1betaUpdateBackupTooManyRequests{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		result, err := updateBackupToCVP(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupTooManyRequests{}, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaUpdateBackupTooManyRequests).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaUpdateBackupTooManyRequests).Message)
	})

	t.Run("WhenUpdateBackupToCVPReturnsNotImplemented", func(t *testing.T) {
		mockClient := backups.NewMockClientService(t)

		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		errorCode := float64(501)
		errorMessage := "Not Implemented"
		mockError := &backups.V1betaUpdateBackupNotImplemented{
			Payload: &models.Error{
				Code:    errorCode,
				Message: errorMessage,
			},
		}

		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, mockError)

		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		result, err := updateBackupToCVP(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupNotImplemented{}, result)
		assert.Equal(t, errorCode, result.(*gcpgenserver.V1betaUpdateBackupNotImplemented).Code)
		assert.Equal(t, errorMessage, result.(*gcpgenserver.V1betaUpdateBackupNotImplemented).Message)
	})

	t.Run("WhenUpdateBackupToCVPReturnsUnknownError", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define unknown error
		unknownError := errors.New("unknown error occurred")

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, unknownError)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupInternalServerError{}, result)

		internalServerErrorResult := result.(*gcpgenserver.V1betaUpdateBackupInternalServerError)
		assert.Equal(t, float64(500), internalServerErrorResult.Code)
		assert.Equal(t, "unknown error occurred", internalServerErrorResult.Message)
	})
	t.Run("WhenUpdateBackupToCVPReturns202Accepted", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock 202 Accepted response with UPLOADING state
		operationName := "/v1beta/projects/12345/locations/us-east4/operations/test-operation-id"
		mockResponse := &backups.V1betaUpdateBackupAccepted{
			Payload: &models.OperationV1beta{
				Name: operationName,
				Done: func() *bool { b := false; return &b }(),
				Response: map[string]interface{}{
					"backupId":         "test-backup-id",
					"backupVaultId":    "test-backup-vault-id",
					"resourceId":       "test-resource-id",
					"state":            "UPLOADING",
					"description":      "updated-description",
					"backupType":       "MANUAL",
					"created":          "2025-01-01T00:00:00Z",
					"volumeId":         "test-volume-id",
					"volumeUsageBytes": 1024,
				},
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.MatchedBy(func(p *backups.V1betaUpdateBackupParams) bool {
				return p.BackupVaultID == params.BackupVaultId &&
					p.BackupID == params.BackupId &&
					p.LocationID == params.LocationId &&
					p.ProjectNumber == params.ProjectNumber &&
					*p.Body.Description == "updated-description"
			})).
			Return(nil, mockResponse, nil, nil)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(t, operationName, operationResult.Name.Value)
		assert.False(t, operationResult.Done.Value)

		// Verify state transformation from UPLOADING to UPDATING
		assert.NotNil(t, operationResult.Response)
		var responseData map[string]interface{}
		err = json.Unmarshal(operationResult.Response, &responseData)
		assert.NoError(t, err)
		assert.Equal(t, "UPLOADING", responseData["state"]) // Should be transformed
		assert.Equal(t, "test-backup-id", responseData["backupId"])
		assert.Equal(t, "updated-description", responseData["description"])
	})

	// Test for 204 No Content response
	t.Run("WhenUpdateBackupToCVPReturns204NoContent", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock 204 No Content response
		mockResponse := &backups.V1betaUpdateBackupNoContent{}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.MatchedBy(func(p *backups.V1betaUpdateBackupParams) bool {
				return p.BackupVaultID == params.BackupVaultId &&
					p.BackupID == params.BackupId &&
					p.LocationID == params.LocationId &&
					p.ProjectNumber == params.ProjectNumber &&
					*p.Body.Description == "updated-description"
			})).
			Return(nil, nil, mockResponse, nil)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.True(t, operationResult.Done.Value)
		assert.Contains(t, operationResult.Name.Value, "/v1beta/projects/12345/locations/us-east4/operations/")
	})

	// Test for unexpected response (fallback error)
	t.Run("WhenUpdateBackupToCVPReturnsUnexpectedResponse", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Set up mock client behavior - return all nil responses (unexpected)
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.Anything).
			Return(nil, nil, nil, nil)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaUpdateBackupInternalServerError{}, result)

		internalServerErrorResult := result.(*gcpgenserver.V1betaUpdateBackupInternalServerError)
		assert.Equal(t, float64(500), internalServerErrorResult.Code)
		assert.Equal(t, "An unexpected error occurred while updating the backup", internalServerErrorResult.Message)
	})

	// Test for 202 Accepted response without state transformation (when state is not UPLOADING)
	t.Run("WhenUpdateBackupToCVPReturns202AcceptedWithNonUploadingState", func(t *testing.T) {
		// Create mock client
		mockClient := backups.NewMockClientService(t)

		// Define input parameters
		req := &gcpgenserver.BackupUpdateV1beta{
			Description: "updated-description",
		}
		params := gcpgenserver.V1betaUpdateBackupParams{
			BackupVaultId:  "test-backup-vault-id",
			BackupId:       "test-backup-id",
			LocationId:     "us-east4",
			ProjectNumber:  "12345",
			XCorrelationID: gcpgenserver.NewOptString("test-correlation-id"),
		}

		// Define mock 202 Accepted response with READY state (should not be transformed)
		operationName := "/v1beta/projects/12345/locations/us-east4/operations/test-operation-id"
		mockResponse := &backups.V1betaUpdateBackupAccepted{
			Payload: &models.OperationV1beta{
				Name: operationName,
				Done: func() *bool { b := false; return &b }(),
				Response: map[string]interface{}{
					"backupId":         "test-backup-id",
					"backupVaultId":    "test-backup-vault-id",
					"resourceId":       "test-resource-id",
					"state":            "READY", // This should NOT be transformed
					"description":      "updated-description",
					"backupType":       "MANUAL",
					"created":          "2025-01-01T00:00:00Z",
					"volumeId":         "test-volume-id",
					"volumeUsageBytes": 1024,
				},
			},
		}

		// Set up mock client behavior
		mockClient.EXPECT().
			V1betaUpdateBackup(mock.MatchedBy(func(p *backups.V1betaUpdateBackupParams) bool {
				return p.BackupVaultID == params.BackupVaultId &&
					p.BackupID == params.BackupId &&
					p.LocationID == params.LocationId &&
					p.ProjectNumber == params.ProjectNumber &&
					*p.Body.Description == "updated-description"
			})).
			Return(nil, mockResponse, nil, nil)

		// Set up CVP client
		cvpClient := &cvpapi.Cvp{Backups: mockClient}
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return *cvpClient
		}

		// Create context with logger
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, &log.MockLogger{})

		// Call the function under test
		result, err := updateBackupToCVP(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operationResult := result.(*gcpgenserver.OperationV1beta)
		assert.Equal(t, operationName, operationResult.Name.Value)
		assert.False(t, operationResult.Done.Value)

		// Verify state is NOT transformed (should remain READY)
		assert.NotNil(t, operationResult.Response)
		var responseData map[string]interface{}
		err = json.Unmarshal(operationResult.Response, &responseData)
		assert.NoError(t, err)
		assert.Equal(t, "READY", responseData["state"]) // Should remain unchanged
		assert.Equal(t, "test-backup-id", responseData["backupId"])
		assert.Equal(t, "updated-description", responseData["description"])
	})
}

func TestConvertBackupDataModelToBackupsV1beta(t *testing.T) {
	backup := &datamodel.Backup{
		Name:        "test-backup",
		VolumeUUID:  "test-volume-uuid",
		State:       "READY",
		SizeInBytes: 1024,
		Description: "Test backup description",
		Type:        "MANUAL",
		Attributes: &datamodel.BackupAttributes{
			AccountIdentifier:   "test-account",
			BucketName:          "test-bucket",
			VolumeName:          "test-volume",
			SnapshotName:        "test-snapshot",
			UseExistingSnapshot: true,
		},
		BackupVault: &datamodel.BackupVault{
			SourceRegionName: func() *string { s := "us-central1"; return &s }(),
			BackupRegionName: func() *string { s := "us-east1"; return &s }(),
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:   "test-bucket",
					SatisfiesPzi: false,
					SatisfiesPzs: false,
				},
			},
		},
	}
	backup.UUID = "backup-uuid"
	backup.BackupVault.UUID = "vault-uuid"

	result := convertBackupDataModelToBackupsV1beta(backup)

	assert.Equal(t, "test-backup", result.ResourceId.Value)
	assert.Equal(t, "test-volume-uuid", result.VolumeId.Value)
	assert.Equal(t, "READY", string(result.State.Value))
	assert.True(t, result.Created.IsSet())
	assert.Equal(t, backup.CreatedAt, result.Created.Value)
	assert.True(t, result.BackupId.IsSet())
	assert.Equal(t, "backup-uuid", result.BackupId.Value)
	assert.True(t, result.VolumeUsageBytes.IsSet())
	assert.Equal(t, int64(1024), result.VolumeUsageBytes.Value)
	assert.True(t, result.BackupVaultId.IsSet())
	assert.Equal(t, "vault-uuid", result.BackupVaultId.Value)
	assert.True(t, result.Description.IsSet())
	assert.Equal(t, "Test backup description", result.Description.Value)
	assert.Equal(t, "MANUAL", string(result.BackupType.Value))
	assert.True(t, result.SourceVolume.IsSet())
	assert.True(t, result.SourceSnapshot.IsSet())
	assert.True(t, result.BackupRegion.IsSet())
	assert.Equal(t, "us-east1", result.BackupRegion.Value)
	assert.True(t, result.VolumeRegion.IsSet())
	assert.Equal(t, "us-central1", result.VolumeRegion.Value)
	assert.True(t, result.SatisfiesPzs.IsSet())
	assert.False(t, result.SatisfiesPzs.Value)
	assert.True(t, result.SatisfiesPzi.IsSet())
	assert.False(t, result.SatisfiesPzi.Value)
	assert.False(t, result.BackupChainBytes.IsSet())
	assert.Equal(t, int64(0), result.BackupChainBytes.Value)
	assert.False(t, result.AssetLocationMetadata.IsSet())
}

func TestConvertBackupDataModelToBackupsV1beta_OntapSourceVolumePath(t *testing.T) {
	t.Run("OntapBackup_ReplacesVolumeNameWithUUID", func(t *testing.T) {
		const volumeName = "ontap-vol-name"
		const volumeUUID = "vol-uuid-abc123"

		backup := &datamodel.Backup{
			Name:        "test-backup",
			VolumeUUID:  volumeUUID,
			State:       "READY",
			SizeInBytes: 1024,
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:  "test-account",
				VolumeName:         volumeName,
				SourceVolumeZone:   "us-central1-a",
				IsExpertModeBackup: true,
			},
			BackupVault: &datamodel.BackupVault{
				SourceRegionName: func() *string { s := "us-central1"; return &s }(),
				BackupRegionName: func() *string { s := "us-central1"; return &s }(),
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}
		backup.UUID = "backup-uuid"
		backup.BackupVault.UUID = "vault-uuid"

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.True(t, result.SourceVolume.IsSet())
		assert.Contains(t, result.SourceVolume.Value, volumeUUID)
		assert.NotContains(t, result.SourceVolume.Value, volumeName)
	})

	t.Run("NonOntapBackup_UsesVolumeName", func(t *testing.T) {
		const volumeName = "regular-vol-name"

		backup := &datamodel.Backup{
			Name:        "test-backup",
			VolumeUUID:  "vol-uuid",
			State:       "READY",
			SizeInBytes: 1024,
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:  "test-account",
				VolumeName:         volumeName,
				SourceVolumeZone:   "us-central1-a",
				IsExpertModeBackup: false,
			},
			BackupVault: &datamodel.BackupVault{
				SourceRegionName: func() *string { s := "us-central1"; return &s }(),
				BackupRegionName: func() *string { s := "us-central1"; return &s }(),
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}
		backup.UUID = "backup-uuid"
		backup.BackupVault.UUID = "vault-uuid"

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.True(t, result.SourceVolume.IsSet())
		assert.Contains(t, result.SourceVolume.Value, volumeName)
	})

	t.Run("OntapBackup_EmptyVolumeName_PathUnchanged", func(t *testing.T) {
		backup := &datamodel.Backup{
			Name:        "test-backup",
			VolumeUUID:  "vol-uuid",
			State:       "READY",
			SizeInBytes: 1024,
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				AccountIdentifier:  "test-account",
				VolumeName:         "", // empty — rewrite must be skipped
				SourceVolumeZone:   "us-central1-a",
				IsExpertModeBackup: true,
			},
			BackupVault: &datamodel.BackupVault{
				SourceRegionName: func() *string { s := "us-central1"; return &s }(),
				BackupRegionName: func() *string { s := "us-central1"; return &s }(),
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}
		backup.UUID = "backup-uuid"
		backup.BackupVault.UUID = "vault-uuid"

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.True(t, result.SourceVolume.IsSet())
		assert.Contains(t, result.SourceVolume.Value, "projects/test-account/locations/us-central1-a/volumes/")
	})
}

func TestConvertBackupDataModelToBackupsV1beta_NoSnapshot(t *testing.T) {
	backup := &datamodel.Backup{
		Name:        "test-backup",
		VolumeUUID:  "test-volume-uuid",
		State:       "READY",
		SizeInBytes: 1024,
		Description: "Test backup description",
		Type:        "MANUAL",
		Attributes: &datamodel.BackupAttributes{
			BucketName:          "test-bucket",
			AccountIdentifier:   "test-account",
			VolumeName:          "test-volume",
			UseExistingSnapshot: false,
		},
		BackupVault: &datamodel.BackupVault{
			SourceRegionName: func() *string { s := "us-central1"; return &s }(),
			BackupRegionName: func() *string { s := "us-central1"; return &s }(),
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:   "test-bucket",
					SatisfiesPzi: true,
					SatisfiesPzs: true,
				},
			},
		},
	}
	backup.UUID = "backup-uuid"
	backup.BackupVault.UUID = "vault-uuid"

	result := convertBackupDataModelToBackupsV1beta(backup)

	assert.Equal(t, "test-backup", result.ResourceId.Value)
	assert.Equal(t, "test-volume-uuid", result.VolumeId.Value)
	assert.Equal(t, "READY", string(result.State.Value))
	assert.True(t, result.Created.IsSet())
	assert.True(t, result.BackupId.IsSet())
	assert.Equal(t, "backup-uuid", result.BackupId.Value)
	assert.True(t, result.VolumeUsageBytes.IsSet())
	assert.Equal(t, int64(1024), result.VolumeUsageBytes.Value)
	assert.True(t, result.BackupVaultId.IsSet())
	assert.Equal(t, "vault-uuid", result.BackupVaultId.Value)
	assert.True(t, result.Description.IsSet())
	assert.Equal(t, "Test backup description", result.Description.Value)
	assert.Equal(t, "MANUAL", string(result.BackupType.Value))
	assert.True(t, result.SourceVolume.IsSet())
	assert.False(t, result.SourceSnapshot.IsSet())
	assert.True(t, result.BackupRegion.IsSet())
	assert.True(t, result.VolumeRegion.IsSet())
	assert.Equal(t, "us-central1", result.VolumeRegion.Value)
	assert.True(t, result.SatisfiesPzs.IsSet())
	assert.True(t, result.SatisfiesPzs.Value)
	assert.True(t, result.SatisfiesPzi.IsSet())
	assert.True(t, result.SatisfiesPzi.Value)
	assert.False(t, result.BackupChainBytes.IsSet())
	assert.Equal(t, int64(0), result.BackupChainBytes.Value)
	assert.False(t, result.AssetLocationMetadata.IsSet())
}

func TestConvertToBackupsV1beta_ConditionalFields(t *testing.T) {
	tests := []struct {
		name          string
		input         *models.BackupV1beta
		expectedSet   []string
		expectedUnset []string
	}{
		{
			name: "All optional fields are nil - should not be set",
			input: &models.BackupV1beta{
				ResourceID:    "test-backup",
				VolumeID:      "test-volume",
				State:         "READY",
				BackupID:      "backup-123",
				BackupVaultID: func() *string { s := "vault-123"; return &s }(),
				BackupType:    "MANUAL",
			},
			expectedUnset: []string{"EnforcedRetentionEndTime", "VolumeUsageBytes", "Description", "SourceSnapshot", "BackupChainBytes", "SatisfiesPzs", "SatisfiesPzi", "VolumeRegion", "BackupRegion", "AssetLocationMetadata"},
		},
		{
			name: "Some optional fields have values - should be set",
			input: &models.BackupV1beta{
				ResourceID:    "test-backup",
				VolumeID:      "test-volume",
				State:         "READY",
				BackupID:      "backup-123",
				BackupVaultID: func() *string { s := "vault-123"; return &s }(),
				BackupType:    "MANUAL",
				Description:   func() *string { s := "Test backup"; return &s }(),
				SatisfiesPzs:  func() *bool { b := true; return &b }(),
				VolumeRegion:  func() *string { s := "us-central1"; return &s }(),
			},
			expectedSet:   []string{"Description", "SatisfiesPzs", "VolumeRegion"},
			expectedUnset: []string{"EnforcedRetentionEndTime", "VolumeUsageBytes", "SourceSnapshot", "BackupChainBytes", "SatisfiesPzi", "BackupRegion", "AssetLocationMetadata"},
		},
		{
			name: "All optional fields have values - should all be set",
			input: &models.BackupV1beta{
				ResourceID:       "test-backup",
				VolumeID:         "test-volume",
				State:            "READY",
				Created:          strfmt.DateTime(time.Now()),
				BackupID:         "backup-123",
				VolumeUsageBytes: func() *int64 { v := int64(1024); return &v }(),
				SourceVolume:     "projects/123/locations/us-central1/volumes/test-volume",
				BackupVaultID:    func() *string { s := "vault-123"; return &s }(),
				Description:      func() *string { s := "Test backup"; return &s }(),
				SourceSnapshot:   func() *string { s := "snapshot-123"; return &s }(),
				BackupType:       "MANUAL",
				BackupChainBytes: func() *int64 { v := int64(512); return &v }(),
				SatisfiesPzs:     func() *bool { b := true; return &b }(),
				SatisfiesPzi:     func() *bool { b := false; return &b }(),
				VolumeRegion:     func() *string { s := "us-central1"; return &s }(),
				BackupRegion:     func() *string { s := "us-east1"; return &s }(),
			},
			expectedSet:   []string{"Created", "VolumeUsageBytes", "SourceVolume", "Description", "SourceSnapshot", "BackupChainBytes", "SatisfiesPzs", "SatisfiesPzi", "VolumeRegion", "BackupRegion"},
			expectedUnset: []string{"EnforcedRetentionEndTime", "AssetLocationMetadata"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToBackupsV1beta(tt.input)

			// Check expected fields are set
			for _, field := range tt.expectedSet {
				switch field {
				case "Created":
					assert.True(t, result.Created.IsSet())
				case "EnforcedRetentionEndTime":
					assert.True(t, result.EnforcedRetentionEndTime.IsSet())
				case "VolumeUsageBytes":
					assert.True(t, result.VolumeUsageBytes.IsSet())
				case "SourceVolume":
					assert.True(t, result.SourceVolume.IsSet())
				case "Description":
					assert.True(t, result.Description.IsSet())
				case "SourceSnapshot":
					assert.True(t, result.SourceSnapshot.IsSet())
				case "BackupChainBytes":
					assert.True(t, result.BackupChainBytes.IsSet())
				case "SatisfiesPzs":
					assert.True(t, result.SatisfiesPzs.IsSet())
				case "SatisfiesPzi":
					assert.True(t, result.SatisfiesPzi.IsSet())
				case "VolumeRegion":
					assert.True(t, result.VolumeRegion.IsSet())
				case "BackupRegion":
					assert.True(t, result.BackupRegion.IsSet())
				case "AssetLocationMetadata":
					assert.True(t, result.AssetLocationMetadata.IsSet())
				}
			}

			// Check unexpected fields are NOT set
			for _, field := range tt.expectedUnset {
				switch field {
				case "Created":
					assert.False(t, result.Created.IsSet())
				case "EnforcedRetentionEndTime":
					assert.False(t, result.EnforcedRetentionEndTime.IsSet())
				case "VolumeUsageBytes":
					assert.False(t, result.VolumeUsageBytes.IsSet())
				case "SourceVolume":
					assert.False(t, result.SourceVolume.IsSet())
				case "Description":
					assert.False(t, result.Description.IsSet())
				case "SourceSnapshot":
					assert.False(t, result.SourceSnapshot.IsSet())
				case "BackupChainBytes":
					assert.False(t, result.BackupChainBytes.IsSet())
				case "SatisfiesPzs":
					assert.False(t, result.SatisfiesPzs.IsSet())
				case "SatisfiesPzi":
					assert.False(t, result.SatisfiesPzi.IsSet())
				case "VolumeRegion":
					assert.False(t, result.VolumeRegion.IsSet())
				case "BackupRegion":
					assert.False(t, result.BackupRegion.IsSet())
				case "AssetLocationMetadata":
					assert.False(t, result.AssetLocationMetadata.IsSet())
				}
			}
		})
	}
}

func TestConvertBackupDataModelToBackupsV1beta_SnapshotRenaming(t *testing.T) {
	t.Run("WhenSourceSnapshotIsSetWithUseExistingSnapshot", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:        "test-backup",
			VolumeUUID:  "test-volume-uuid",
			State:       datamodel.LifeCycleStateAvailable,
			SizeInBytes: 1024,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-vault-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				SourceRegionName: nillable.GetStringPtr("us-east1"),
			},
			Description: "Test backup description",
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				SnapshotName:        "source-snapshot-123",
				VolumeName:          "test-volume",
				AccountIdentifier:   "123456789",
				UseExistingSnapshot: true,
			},
		}

		result := convertBackupDataModelToBackupsV1beta(backup)

		// Verify that SourceSnapshot field is correctly set
		assert.True(t, result.SourceSnapshot.Set)
		assert.Equal(t, "projects/123456789/locations/us-east1/volumes/test-volume/snapshots/source-snapshot-123", result.SourceSnapshot.Value)
	})

	t.Run("WhenSourceSnapshotNotSetWithoutUseExistingSnapshot", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:        "test-backup",
			VolumeUUID:  "test-volume-uuid",
			State:       datamodel.LifeCycleStateAvailable,
			SizeInBytes: 1024,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-vault-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				SourceRegionName: nillable.GetStringPtr("us-east1"),
			},
			Description: "Test backup description",
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				SnapshotName:        "snapshot-name",
				VolumeName:          "test-volume",
				AccountIdentifier:   "123456789",
				UseExistingSnapshot: false,
			},
		}

		result := convertBackupDataModelToBackupsV1beta(backup)

		// Verify that SourceSnapshot field is not set when UseExistingSnapshot is false
		assert.False(t, result.SourceSnapshot.Set)
	})

	t.Run("WhenSourceSnapshotFieldHandlesEmptySnapshotName", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:        "test-backup",
			VolumeUUID:  "test-volume-uuid",
			State:       datamodel.LifeCycleStateAvailable,
			SizeInBytes: 1024,
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-vault-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				SourceRegionName: nillable.GetStringPtr("us-east1"),
			},
			Description: "Test backup description",
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				SnapshotName:        "",
				VolumeName:          "test-volume",
				AccountIdentifier:   "123456789",
				UseExistingSnapshot: true,
			},
		}

		result := convertBackupDataModelToBackupsV1beta(backup)

		// Verify that SourceSnapshot is not set when snapshot name is empty
		assert.False(t, result.SourceSnapshot.Set)
	})

	t.Run("WhenSourceSnapshotFormattingIsCorrect", func(t *testing.T) {
		testCases := []struct {
			name             string
			snapshotName     string
			volumeName       string
			accountID        string
			region           string
			expectedSnapshot string
		}{
			{
				name:             "Standard snapshot path",
				snapshotName:     "my-snapshot",
				volumeName:       "my-volume",
				accountID:        "123456789",
				region:           "us-central1",
				expectedSnapshot: "projects/123456789/locations/us-central1/volumes/my-volume/snapshots/my-snapshot",
			},
			{
				name:             "Snapshot with special characters",
				snapshotName:     "snapshot-with-dashes-123",
				volumeName:       "volume-name-456",
				accountID:        "987654321",
				region:           "europe-west1",
				expectedSnapshot: "projects/987654321/locations/europe-west1/volumes/volume-name-456/snapshots/snapshot-with-dashes-123",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				backup := &datamodel.Backup{
					BaseModel: datamodel.BaseModel{
						UUID:      "test-backup-uuid",
						CreatedAt: time.Now(),
						UpdatedAt: time.Now(),
					},
					Name:        "test-backup",
					VolumeUUID:  "test-volume-uuid",
					State:       datamodel.LifeCycleStateAvailable,
					SizeInBytes: 1024,
					BackupVault: &datamodel.BackupVault{
						BaseModel: datamodel.BaseModel{
							UUID:      "test-vault-uuid",
							CreatedAt: time.Now(),
							UpdatedAt: time.Now(),
						},
						SourceRegionName: nillable.GetStringPtr(tc.region),
					},
					Description: "Test backup description",
					Type:        "MANUAL",
					Attributes: &datamodel.BackupAttributes{
						SnapshotName:        tc.snapshotName,
						VolumeName:          tc.volumeName,
						AccountIdentifier:   tc.accountID,
						UseExistingSnapshot: true,
					},
				}

				result := convertBackupDataModelToBackupsV1beta(backup)

				assert.True(t, result.SourceSnapshot.Set)
				assert.Equal(t, tc.expectedSnapshot, result.SourceSnapshot.Value)
			})
		}
	})

	t.Run("WhenBackupHasProtocols", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:        "test-backup",
			VolumeUUID:  "test-volume-uuid",
			State:       datamodel.LifeCycleStateAvailable,
			SizeInBytes: 1024,
			Description: "Test backup description",
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				AccountIdentifier:   "test-account",
				VolumeName:          "test-volume",
				SnapshotName:        "test-snapshot",
				UseExistingSnapshot: true,
				Protocols:           []string{"NFSV3", "NFSV4", "SMB"},
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-vault-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				SourceRegionName: nillable.GetStringPtr("us-east1"),
				BucketDetails: []*datamodel.BucketDetails{
					{
						BucketName:   "test-bucket",
						SatisfiesPzi: true,
						SatisfiesPzs: false,
					},
				},
			},
		}

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.Equal(t, "test-volume-uuid", result.VolumeId.Value)
		// Verify protocols are correctly converted
		assert.NotNil(t, result.Protocols)
		assert.Len(t, result.Protocols, 3)
		assert.Equal(t, gcpgenserver.ProtocolsV1beta("NFSV3"), result.Protocols[0])
		assert.Equal(t, gcpgenserver.ProtocolsV1beta("NFSV4"), result.Protocols[1])
		assert.Equal(t, gcpgenserver.ProtocolsV1beta("SMB"), result.Protocols[2])
	})

	t.Run("WhenBackupHasEmptyProtocols", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:        "test-backup",
			VolumeUUID:  "test-volume-uuid",
			State:       datamodel.LifeCycleStateAvailable,
			SizeInBytes: 1024,
			Description: "Test backup description",
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				AccountIdentifier:   "test-account",
				VolumeName:          "test-volume",
				SnapshotName:        "test-snapshot",
				UseExistingSnapshot: true,
				Protocols:           []string{},
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-vault-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				SourceRegionName: nillable.GetStringPtr("us-east1"),
				BucketDetails: []*datamodel.BucketDetails{
					{
						BucketName:   "test-bucket",
						SatisfiesPzi: true,
						SatisfiesPzs: false,
					},
				},
			},
		}

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		// Verify protocols is nil when empty
		assert.Nil(t, result.Protocols)
	})

	t.Run("WhenBackupHasNilProtocols", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:        "test-backup",
			VolumeUUID:  "test-volume-uuid",
			State:       datamodel.LifeCycleStateAvailable,
			SizeInBytes: 1024,
			Description: "Test backup description",
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				AccountIdentifier:   "test-account",
				VolumeName:          "test-volume",
				SnapshotName:        "test-snapshot",
				UseExistingSnapshot: true,
				Protocols:           nil,
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-vault-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				SourceRegionName: nillable.GetStringPtr("us-east1"),
				BucketDetails: []*datamodel.BucketDetails{
					{
						BucketName:   "test-bucket",
						SatisfiesPzi: true,
						SatisfiesPzs: false,
					},
				},
			},
		}

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		// Verify protocols is nil when nil
		assert.Nil(t, result.Protocols)
	})

	t.Run("WhenBackupHasSingleProtocol", func(t *testing.T) {
		backup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{
				UUID:      "test-backup-uuid",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			Name:        "test-backup",
			VolumeUUID:  "test-volume-uuid",
			State:       datamodel.LifeCycleStateAvailable,
			SizeInBytes: 1024,
			Description: "Test backup description",
			Type:        "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName:          "test-bucket",
				AccountIdentifier:   "test-account",
				VolumeName:          "test-volume",
				SnapshotName:        "test-snapshot",
				UseExistingSnapshot: true,
				Protocols:           []string{"ISCSI"},
			},
			BackupVault: &datamodel.BackupVault{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-vault-uuid",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
				SourceRegionName: nillable.GetStringPtr("us-east1"),
				BucketDetails: []*datamodel.BucketDetails{
					{
						BucketName:   "test-bucket",
						SatisfiesPzi: true,
						SatisfiesPzs: false,
					},
				},
			},
		}

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		// Verify single protocol is correctly converted
		assert.NotNil(t, result.Protocols)
		assert.Len(t, result.Protocols, 1)
		assert.Equal(t, gcpgenserver.ProtocolsV1beta("ISCSI"), result.Protocols[0])
	})

	t.Run("WhenBackupIsNotImmutable", func(t *testing.T) {
		backup := &coremodels.Backup{
			BackupID:       "test-backup-id",
			Name:           "test-backup",
			VolumeID:       "test-volume-id",
			LifeCycleState: "AVAILABLE",
			VolumeName:     "test-volume",
			BackupVaultID:  "test-vault-id",
			Description:    stringPtr("test description"),
			SnapshotName:   "test-snapshot",
			Type:           "MANUAL",
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.Equal(t, "test-volume-id", result.VolumeId.Value)
		assert.Equal(t, "test-backup-id", result.BackupId.Value)
		assert.Equal(t, "test-volume", result.SourceVolume.Value)
		assert.Equal(t, "test-vault-id", result.BackupVaultId.Value)
		assert.Equal(t, "test description", result.Description.Value)
		assert.Equal(t, "test-snapshot", result.SourceSnapshot.Value)
		assert.Equal(t, "MANUAL", string(result.BackupType.Value))
		assert.False(t, result.EnforcedRetentionEndTime.Set)
	})

	t.Run("WhenBackupIsImmutableAndRetentionNotExpired", func(t *testing.T) {
		// Set creation time to 15 days ago
		creationTime := time.Now().AddDate(0, 0, -15)
		minRetentionDuration := int64(30)
		backup := &coremodels.Backup{
			BackupID:                         "test-backup-id",
			Name:                             "test-backup",
			VolumeID:                         "test-volume-id",
			LifeCycleState:                   "AVAILABLE",
			VolumeName:                       "test-volume",
			BackupVaultID:                    "test-vault-id",
			Description:                      stringPtr("test description"),
			SnapshotName:                     "test-snapshot",
			Type:                             "MANUAL",
			CreationTime:                     creationTime,
			MinimumEnforcedRetentionDuration: &minRetentionDuration,
			IsBackupImmutable:                true,
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.Equal(t, "test-volume-id", result.VolumeId.Value)
		assert.Equal(t, "test-backup-id", result.BackupId.Value)
		assert.Equal(t, "test-volume", result.SourceVolume.Value)
		assert.Equal(t, "test-vault-id", result.BackupVaultId.Value)
		assert.Equal(t, "test description", result.Description.Value)
		assert.Equal(t, "test-snapshot", result.SourceSnapshot.Value)
		assert.Equal(t, "MANUAL", string(result.BackupType.Value))
		assert.True(t, result.EnforcedRetentionEndTime.Set)

		// Calculate expected expiration date (creation time + 30 days)
		expectedExpiration := creationTime.AddDate(0, 0, 30)
		assert.Equal(t, expectedExpiration.Year(), result.EnforcedRetentionEndTime.Value.Year())
		assert.Equal(t, expectedExpiration.Month(), result.EnforcedRetentionEndTime.Value.Month())
		assert.Equal(t, expectedExpiration.Day(), result.EnforcedRetentionEndTime.Value.Day())
	})

	t.Run("WhenBackupIsImmutableAndRetentionExpired", func(t *testing.T) {
		// Set creation time to 40 days ago (beyond retention period)
		creationTime := time.Now().AddDate(0, 0, -40)
		minRetentionDuration := int64(30)
		backup := &coremodels.Backup{
			BackupID:                         "test-backup-id",
			Name:                             "test-backup",
			VolumeID:                         "test-volume-id",
			LifeCycleState:                   "AVAILABLE",
			VolumeName:                       "test-volume",
			BackupVaultID:                    "test-vault-id",
			Description:                      stringPtr("test description"),
			SnapshotName:                     "test-snapshot",
			Type:                             "MANUAL",
			CreationTime:                     creationTime,
			MinimumEnforcedRetentionDuration: &minRetentionDuration,
			IsBackupImmutable:                true,
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.Equal(t, "test-volume-id", result.VolumeId.Value)
		assert.Equal(t, "test-backup-id", result.BackupId.Value)
		assert.Equal(t, "test-volume", result.SourceVolume.Value)
		assert.Equal(t, "test-vault-id", result.BackupVaultId.Value)
		assert.Equal(t, "test description", result.Description.Value)
		assert.Equal(t, "test-snapshot", result.SourceSnapshot.Value)
		assert.Equal(t, "MANUAL", string(result.BackupType.Value))
	})

	t.Run("WhenBackupIsImmutableButNoRetentionDuration", func(t *testing.T) {
		creationTime := time.Now().AddDate(0, 0, -15)
		backup := &coremodels.Backup{
			BackupID:                         "test-backup-id",
			Name:                             "test-backup",
			VolumeID:                         "test-volume-id",
			LifeCycleState:                   "AVAILABLE",
			VolumeName:                       "test-volume",
			BackupVaultID:                    "test-vault-id",
			Description:                      stringPtr("test description"),
			SnapshotName:                     "test-snapshot",
			Type:                             "MANUAL",
			CreationTime:                     creationTime,
			MinimumEnforcedRetentionDuration: nil,
			IsBackupImmutable:                true,
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.Equal(t, "test-volume-id", result.VolumeId.Value)
		assert.Equal(t, "test-backup-id", result.BackupId.Value)
		assert.Equal(t, "test-volume", result.SourceVolume.Value)
		assert.Equal(t, "test-vault-id", result.BackupVaultId.Value)
		assert.Equal(t, "test description", result.Description.Value)
		assert.Equal(t, "test-snapshot", result.SourceSnapshot.Value)
		assert.Equal(t, "MANUAL", string(result.BackupType.Value))
		assert.False(t, result.EnforcedRetentionEndTime.Set)
	})

	t.Run("WhenBackupIsImmutableButZeroRetentionDuration", func(t *testing.T) {
		creationTime := time.Now().AddDate(0, 0, -15)
		minRetentionDuration := int64(0)
		backup := &coremodels.Backup{
			BackupID:                         "test-backup-id",
			Name:                             "test-backup",
			VolumeID:                         "test-volume-id",
			LifeCycleState:                   "AVAILABLE",
			VolumeName:                       "test-volume",
			BackupVaultID:                    "test-vault-id",
			Description:                      stringPtr("test description"),
			SnapshotName:                     "test-snapshot",
			Type:                             "MANUAL",
			CreationTime:                     creationTime,
			MinimumEnforcedRetentionDuration: &minRetentionDuration,
			IsBackupImmutable:                true,
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.Equal(t, "test-volume-id", result.VolumeId.Value)
		assert.Equal(t, "test-backup-id", result.BackupId.Value)
		assert.Equal(t, "test-volume", result.SourceVolume.Value)
		assert.Equal(t, "test-vault-id", result.BackupVaultId.Value)
		assert.Equal(t, "test description", result.Description.Value)
		assert.Equal(t, "test-snapshot", result.SourceSnapshot.Value)
		assert.Equal(t, "MANUAL", string(result.BackupType.Value))
		assert.False(t, result.EnforcedRetentionEndTime.Set)
	})

	t.Run("WhenBackupHasProtocols", func(t *testing.T) {
		backup := &coremodels.Backup{
			BackupID:       "test-backup-id",
			Name:           "test-backup",
			VolumeID:       "test-volume-id",
			LifeCycleState: "AVAILABLE",
			VolumeName:     "test-volume",
			BackupVaultID:  "test-vault-id",
			Description:    stringPtr("test description"),
			SnapshotName:   "test-snapshot",
			Type:           "MANUAL",
			Protocols:      []string{"NFSV3", "NFSV4", "SMB"},
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		assert.Equal(t, "test-volume-id", result.VolumeId.Value)
		assert.Equal(t, "test-backup-id", result.BackupId.Value)
		assert.Equal(t, "test-volume", result.SourceVolume.Value)
		assert.Equal(t, "test-vault-id", result.BackupVaultId.Value)
		assert.Equal(t, "test description", result.Description.Value)
		assert.Equal(t, "test-snapshot", result.SourceSnapshot.Value)
		assert.Equal(t, "MANUAL", string(result.BackupType.Value))
		// Verify protocols are correctly converted
		assert.NotNil(t, result.Protocols)
		assert.Len(t, result.Protocols, 3)
		assert.Equal(t, gcpgenserver.ProtocolsV1beta("NFSV3"), result.Protocols[0])
		assert.Equal(t, gcpgenserver.ProtocolsV1beta("NFSV4"), result.Protocols[1])
		assert.Equal(t, gcpgenserver.ProtocolsV1beta("SMB"), result.Protocols[2])
	})

	t.Run("WhenBackupHasEmptyProtocols", func(t *testing.T) {
		backup := &coremodels.Backup{
			BackupID:       "test-backup-id",
			Name:           "test-backup",
			VolumeID:       "test-volume-id",
			LifeCycleState: "AVAILABLE",
			VolumeName:     "test-volume",
			BackupVaultID:  "test-vault-id",
			Description:    stringPtr("test description"),
			SnapshotName:   "test-snapshot",
			Type:           "MANUAL",
			Protocols:      []string{},
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		// Verify protocols is nil when empty
		assert.Nil(t, result.Protocols)
	})

	t.Run("WhenBackupHasNoProtocolsField", func(t *testing.T) {
		backup := &coremodels.Backup{
			BackupID:       "test-backup-id",
			Name:           "test-backup",
			VolumeID:       "test-volume-id",
			LifeCycleState: "AVAILABLE",
			VolumeName:     "test-volume",
			BackupVaultID:  "test-vault-id",
			Description:    stringPtr("test description"),
			SnapshotName:   "test-snapshot",
			Type:           "MANUAL",
			Protocols:      nil,
		}

		result := convertBackupModelToBackupsV1beta(backup)

		assert.Equal(t, "test-backup", result.ResourceId.Value)
		// Verify protocols is nil when nil
		assert.Nil(t, result.Protocols)
	})
}

// Helper function to create string pointers
func stringPtr(s string) *string {
	return &s
}

// TestV1betaCreateBackup_VolumeNotFoundInVSA tests the scenario where volume doesn't exist in VSA
// and the function falls back to CVP, including the backup existence check logic
func TestV1betaCreateBackup_VolumeNotFoundInVSA(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	t.Run("WhenVolumeNotFoundAndBackupExistsInVCP_ShouldReturnConflict", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("Error", "Backup with resource ID res-id already exists in backup vault vault").Return()
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   "vol-id",
			ResourceId: "res-id",
			Description: gcpgenserver.OptString{
				Value: "test description",
				Set:   true,
			},
			SnapshotId: gcpgenserver.OptString{
				Value: "snap-id",
				Set:   true,
			},
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:     "us-east4",
			ProjectNumber:  "proj",
			BackupVaultId:  "vault",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		// Mock ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault lookup
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		// Mock GetVolume to return NotFoundErr (volume doesn't exist in VSA)
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(nil, errors.NewNotFoundErr("Volume not found", nil))

		// Mock ListBackups to return existing backup (backup already exists in VCP)
		existingBackup := &datamodel.Backup{
			Name: "res-id",
		}
		mockOrch.EXPECT().ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return([]*datamodel.Backup{existingBackup}, nil)

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// Should return conflict error since backup already exists
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupConflict{}, result)

		conflict := result.(*gcpgenserver.V1betaCreateBackupConflict)
		assert.Equal(t, float64(409), conflict.Code)
		assert.Contains(t, conflict.Message, "Backup with resource ID res-id already exists in backup vault vault")
	})

	t.Run("WhenVolumeNotFoundAndListBackupsFails_ShouldReturnInternalServerError", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("Error", "Failed to check for existing backups", "error", "Database connection failed").Return()
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   "vol-id",
			ResourceId: "res-id",
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "proj",
			BackupVaultId: "vault",
		}

		// Mock ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault lookup
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		// Mock GetVolume to return NotFoundErr (volume doesn't exist in VSA)
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(nil, errors.NewNotFoundErr("Volume not found", nil))

		// Mock ListBackups to fail
		listBackupsErr := fmt.Errorf("Database connection failed")
		mockOrch.EXPECT().ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return(nil, listBackupsErr)

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// Should return internal server error since ListBackups failed
		assert.Error(t, err)
		assert.Equal(t, "Database connection failed", err.Error())
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupInternalServerError{}, result)

		internalErr := result.(*gcpgenserver.V1betaCreateBackupInternalServerError)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, "Database connection failed", internalErr.Message)
	})

	t.Run("WhenVolumeNotFoundAndUseVCPRegionEnabled_ShouldReturnBadRequest", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   "vol-id",
			ResourceId: "res-id",
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "proj",
			BackupVaultId: "vault",
		}

		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(nil, errors.NewNotFoundErr("Volume not found", nil))

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupBadRequest{}, result)

		badReq := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
		assert.Equal(t, float64(400), badReq.Code)
		assert.Equal(t, "Volume with ID vol-id not found", badReq.Message)
	})

	t.Run("WhenVolumeIsNilAndNoExistingBackup_ShouldProceedToCVP", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   "vol-id",
			ResourceId: "res-id",
			Description: gcpgenserver.OptString{
				Value: "test description",
				Set:   true,
			},
			SnapshotId: gcpgenserver.OptString{
				Value: "snap-id",
				Set:   true,
			},
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:     "us-east4",
			ProjectNumber:  "proj",
			BackupVaultId:  "vault",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		// Mock ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault lookup
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		// Mock GetVolume to return nil volume (volume doesn't exist in VSA)
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(nil, nil)

		// Mock ListBackups to return empty list (no existing backup)
		mockOrch.EXPECT().ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return([]*datamodel.Backup{}, nil)

		// Mock CVP client creation
		mockCVPClient := backups.NewMockClientService(t)
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockCVPClient}
		}

		// Mock CVP backup creation to return created backup
		cvpBackupCreated := &backups.V1betaCreateBackupCreated{
			Payload: &models.BackupV1beta{
				ResourceID: "res-id",
				VolumeID:   "vol-id",
				State:      "Available for use",
				BackupID:   "cvp-backup-id",
			},
		}
		mockCVPClient.EXPECT().V1betaCreateBackup(mock.Anything).Return(cvpBackupCreated, nil, nil)

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// Should return operation response from CVP
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operation := result.(*gcpgenserver.OperationV1beta)
		assert.True(t, operation.Done.Value) // Should be done since CVP returned created backup
	})

	t.Run("WhenVolumeNotFoundAndNoExistingBackup_ShouldProceedToCVP", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   "vol-id",
			ResourceId: "res-id",
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:    "us-east4",
			ProjectNumber: "proj",
			BackupVaultId: "vault",
		}

		// Mock ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault lookup
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		// Mock GetVolume to return NotFoundErr (volume doesn't exist in VSA)
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(nil, errors.NewNotFoundErr("Volume not found", nil))

		// Mock ListBackups to return empty list (no existing backup with same resource ID)
		mockOrch.EXPECT().ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return([]*datamodel.Backup{}, nil)

		// Mock CVP client creation
		mockCVPClient := backups.NewMockClientService(t)
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockCVPClient}
		}

		// Mock CVP backup creation to return accepted operation
		done := false
		cvpBackupAccepted := &backups.V1betaCreateBackupAccepted{
			Payload: &models.OperationV1beta{
				Name: "operations/cvp-operation-id",
				Done: &done,
			},
		}
		mockCVPClient.EXPECT().V1betaCreateBackup(mock.Anything).Return(nil, cvpBackupAccepted, nil)

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// Should return operation response from CVP
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operation := result.(*gcpgenserver.OperationV1beta)
		assert.False(t, operation.Done.Value)
	})

	t.Run("WhenVolumeNotFoundAndListBackupsReturnsNotFoundErr_ShouldProceedToCVP", func(t *testing.T) {
		logger := &log.MockLogger{}
		logger.On("Error", "No existing backups found in VCP", "resourceID", "res-id").Return()
		logger.On("Errorf", "Record not found").Return()
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   "vol-id",
			ResourceId: "res-id",
			Description: gcpgenserver.OptString{
				Value: "test description",
				Set:   true,
			},
			SnapshotId: gcpgenserver.OptString{
				Value: "snap-id",
				Set:   true,
			},
		}
		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:     "us-east4",
			ProjectNumber:  "proj",
			BackupVaultId:  "vault",
			XCorrelationID: gcpgenserver.NewOptString("correlation-id"),
		}

		// Mock ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4", nil
		}

		// Mock backup vault lookup
		backupVault := &coremodels.BackupVaultV1beta{
			BackupVaultID: "vault",
			AccountName:   "proj",
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, "vault").Return(backupVault, nil)

		// Mock GetVolume to return NotFoundErr (volume doesn't exist in VSA)
		mockOrch.EXPECT().GetVolume(ctx, "vol-id", false).Return(nil, errors.NewNotFoundErr("Volume not found", nil))

		// Mock ListBackups to return NotFoundErr (no existing backups found in VCP)
		mockOrch.EXPECT().ListBackups(ctx, "vault", "proj", [][]interface{}{{"name = ?", "res-id"}}).
			Return(nil, errors.NewNotFoundErr("No backups found", nil))

		// Mock CVP client and successful backup creation
		mockCVPClient := backups.NewMockClientService(t)
		cvpBackupCreated := &backups.V1betaCreateBackupCreated{
			Payload: &models.BackupV1beta{
				ResourceID: "res-id",
				VolumeID:   "vol-id",
				State:      "Available for use",
				BackupID:   "cvp-backup-id",
			},
		}
		mockCVPClient.EXPECT().V1betaCreateBackup(mock.Anything).Return(cvpBackupCreated, nil, nil)

		// Mock createClient function
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockCVPClient}
		}

		handler := Handler{Orchestrator: mockOrch}
		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// Should proceed to CVP and return successful operation
		assert.NoError(t, err)
		assert.IsType(t, &gcpgenserver.OperationV1beta{}, result)

		operation := result.(*gcpgenserver.OperationV1beta)
		assert.True(t, operation.Done.Value)
	})
}

func TestConvertBackupDataModelToBackupsV1beta_EnforcedRetentionEndTime(t *testing.T) {
	// Set up the time for consistent testing
	createdAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	retentionDays := int64(30)
	expectedExpirationDate := createdAt.AddDate(0, 0, int(retentionDays))

	tests := []struct {
		name                          string
		backup                        *datamodel.Backup
		expectedEnforcedRetentionSet  bool
		expectedEnforcedRetentionTime time.Time
	}{
		{
			name: "Should set EnforcedRetentionEndTime when backup is immutable with valid retention duration",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-backup-uuid",
					CreatedAt: createdAt,
				},
				Name:        "test-backup",
				VolumeUUID:  "test-volume-uuid",
				State:       "READY",
				SizeInBytes: 1024,
				Description: "Test backup",
				Type:        utils.BackupTypeMANUAL, // Make it a manual backup
				BackupVault: &datamodel.BackupVault{
					BaseModel: datamodel.BaseModel{
						UUID: "test-vault-uuid",
					},
					SourceRegionName: stringPtr("us-central1"),
					ImmutableAttributes: &datamodel.ImmutableAttributes{
						BackupMinimumEnforcedRetentionDuration: &retentionDays,
						IsAdhocBackupImmutable:                 true, // Enable adhoc/manual backup immutability
					},
					BucketDetails: []*datamodel.BucketDetails{
						{
							BucketName:   "test-bucket",
							SatisfiesPzi: true,
							SatisfiesPzs: false,
						},
					},
				},
				Attributes: &datamodel.BackupAttributes{
					BucketName:          "test-bucket",
					UseExistingSnapshot: false,
					SnapshotName:        "test-snapshot",
				},
				LatestLogicalBackupSize: 2048,
			},
			expectedEnforcedRetentionSet:  true,
			expectedEnforcedRetentionTime: expectedExpirationDate,
		},
		{
			name: "Should not set EnforcedRetentionEndTime when ImmutableAttributes is nil",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-backup-uuid",
					CreatedAt: createdAt,
				},
				Name:        "test-backup",
				VolumeUUID:  "test-volume-uuid",
				State:       "READY",
				SizeInBytes: 1024,
				BackupVault: &datamodel.BackupVault{
					BaseModel: datamodel.BaseModel{
						UUID: "test-vault-uuid",
					},
					SourceRegionName:    stringPtr("us-central1"),
					ImmutableAttributes: nil, // nil attributes
					BucketDetails: []*datamodel.BucketDetails{
						{
							BucketName:   "test-bucket",
							SatisfiesPzi: true,
							SatisfiesPzs: false,
						},
					},
				},
				Attributes: &datamodel.BackupAttributes{
					BucketName: "test-bucket",
				},
			},
			expectedEnforcedRetentionSet: false,
		},
		{
			name: "Should not set EnforcedRetentionEndTime when retention duration is 0",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-backup-uuid",
					CreatedAt: createdAt,
				},
				Name:        "test-backup",
				VolumeUUID:  "test-volume-uuid",
				State:       "READY",
				SizeInBytes: 1024,
				Type:        utils.BackupTypeMANUAL,
				BackupVault: &datamodel.BackupVault{
					BaseModel: datamodel.BaseModel{
						UUID: "test-vault-uuid",
					},
					SourceRegionName: stringPtr("us-central1"),
					ImmutableAttributes: &datamodel.ImmutableAttributes{
						BackupMinimumEnforcedRetentionDuration: int64Ptr(0), // zero duration
						IsAdhocBackupImmutable:                 true,        // backup would be immutable but retention is 0
					},
					BucketDetails: []*datamodel.BucketDetails{
						{
							BucketName:   "test-bucket",
							SatisfiesPzi: true,
							SatisfiesPzs: false,
						},
					},
				},
				Attributes: &datamodel.BackupAttributes{
					BucketName: "test-bucket",
				},
			},
			expectedEnforcedRetentionSet: false,
		},
		{
			name: "Should not set EnforcedRetentionEndTime when backup is not immutable",
			backup: &datamodel.Backup{
				BaseModel: datamodel.BaseModel{
					UUID:      "test-backup-uuid",
					CreatedAt: createdAt,
				},
				Name:        "test-backup",
				VolumeUUID:  "test-volume-uuid",
				State:       "READY",
				SizeInBytes: 1024,
				Type:        utils.BackupTypeMANUAL,
				BackupVault: &datamodel.BackupVault{
					BaseModel: datamodel.BaseModel{
						UUID: "test-vault-uuid",
					},
					SourceRegionName: stringPtr("us-central1"),
					ImmutableAttributes: &datamodel.ImmutableAttributes{
						BackupMinimumEnforcedRetentionDuration: &retentionDays,
						IsAdhocBackupImmutable:                 false, // backup is not immutable
					},
					BucketDetails: []*datamodel.BucketDetails{
						{
							BucketName:   "test-bucket",
							SatisfiesPzi: true,
							SatisfiesPzs: false,
						},
					},
				},
				Attributes: &datamodel.BackupAttributes{
					BucketName: "test-bucket",
				},
			},
			expectedEnforcedRetentionSet: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Call the function under test
			result := convertBackupDataModelToBackupsV1beta(tt.backup)

			// Verify the EnforcedRetentionEndTime field
			assert.Equal(t, tt.expectedEnforcedRetentionSet, result.EnforcedRetentionEndTime.Set,
				"EnforcedRetentionEndTime.Set should match expected value")

			if tt.expectedEnforcedRetentionSet {
				assert.Equal(t, tt.expectedEnforcedRetentionTime, result.EnforcedRetentionEndTime.Value,
					"EnforcedRetentionEndTime.Value should match expected expiration date")
			}

			// Verify other basic fields are set correctly
			assert.True(t, result.ResourceId.Set)
			assert.Equal(t, tt.backup.Name, result.ResourceId.Value)
			assert.True(t, result.VolumeId.Set)
			assert.Equal(t, tt.backup.VolumeUUID, result.VolumeId.Value)
			assert.True(t, result.BackupId.Set)
			assert.Equal(t, tt.backup.UUID, result.BackupId.Value)
		})
	}
}

// TestCheckAndFetchBackupVault tests the checkAndFetchBackupVault function
func TestCheckAndFetchBackupVault(t *testing.T) {
	ctx := context.Background()
	backupVaultID := "backup-vault-123"
	region := "us-central1"
	accountName := "test-account"

	expertModeVol := &datamodel.ExpertModeVolumes{
		BaseModel: datamodel.BaseModel{
			UUID: "expert-vol-123",
		},
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{
				UUID: "account-uuid",
			},
			Name: accountName,
		},
	}

	t.Run("WhenBackupVaultFoundLocally", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		backupVaultModel := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultID,
		}

		mockOrch.EXPECT().
			GetBackupVaultByUUID(ctx, backupVaultID, accountName).
			Return(backupVaultModel, nil)

		result, err := checkAndFetchBackupVault(ctx, &handler, expertModeVol, backupVaultID, region)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.Equal(t, backupVaultID, result.UUID)
	})

	t.Run("WhenGetBackupVaultByUUIDReturnsNonNotFoundError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		dbError := fmt.Errorf("database connection error")
		mockOrch.EXPECT().
			GetBackupVaultByUUID(ctx, backupVaultID, accountName).
			Return(nil, dbError)

		result, err := checkAndFetchBackupVault(ctx, &handler, expertModeVol, backupVaultID, region)

		assert.Error(t, err)
		assert.Nil(t, result)
		assert.Equal(t, dbError, err)
	})

	t.Run("WhenUseVCPRegionEnabledAndBackupVaultNotFound_ShouldReturnNotFoundError", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		notFoundErr := errors.NewNotFoundErr("Backup vault", &backupVaultID)
		mockOrch.EXPECT().
			GetBackupVaultByUUID(ctx, backupVaultID, accountName).
			Return(nil, notFoundErr)

		result, err := checkAndFetchBackupVault(ctx, &handler, expertModeVol, backupVaultID, region)

		assert.Nil(t, result)
		assert.Error(t, err)
		assert.True(t, errors.IsNotFoundErr(err))
		assert.Equal(t, "Backup vault 'backup-vault-123' not found", err.Error())
	})

	t.Run("WhenBackupVaultNotFoundLocallyAndCVPFetchSucceeds", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		notFoundErr := errors.NewNotFoundErr("Backup vault", &backupVaultID)
		mockOrch.EXPECT().
			GetBackupVaultByUUID(ctx, backupVaultID, accountName).
			Return(nil, notFoundErr)
		_, err := checkAndFetchBackupVault(ctx, &handler, expertModeVol, backupVaultID, region)
		assert.Error(t, err)
	})

	t.Run("WhenGetBackupVaultByUUIDReturnsNilWithNoError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		mockOrch.EXPECT().
			GetBackupVaultByUUID(ctx, backupVaultID, accountName).
			Return(nil, nil)

		_, err := checkAndFetchBackupVault(ctx, &handler, expertModeVol, backupVaultID, region)
		assert.Error(t, err)
	})
}

// TestV1betaCreateBackup_PoolAndExpertModeVolumeHandling tests the pool validation and expert mode volume handling logic
func TestV1betaCreateBackup_PoolAndExpertModeVolumeHandling(t *testing.T) {
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	ctx := context.Background()
	projectNumber := "test-project-123"
	locationId := "us-central1-a"
	backupVaultId := "backup-vault-123"
	volumeId := "volume-123"
	poolId := "pool-123"

	params := gcpgenserver.V1betaCreateBackupParams{
		ProjectNumber:  projectNumber,
		LocationId:     locationId,
		BackupVaultId:  backupVaultId,
		XCorrelationID: gcpgenserver.NewOptString("correlation-123"),
	}

	// Mock region parsing - the code uses utils.ParseAndValidateRegionAndZone directly
	originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
	defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "a", nil
	}

	t.Run("WhenPoolIdNotProvided", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.OptString{}, // Not set
		}

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Should call GetVolume since poolId is not provided
		mockOrch.EXPECT().
			GetVolume(ctx, volumeId, false).
			Return(nil, errors.NewNotFoundErr("Volume", &volumeId))

		// Mock ListBackups with exact filters
		mockOrch.EXPECT().
			ListBackups(ctx, backupVaultId, projectNumber, [][]interface{}{{"name = ?", "backup-123"}}).
			Return([]*datamodel.Backup{}, nil)

		// Mock CVP client and successful backup creation
		mockCVPClient := backups.NewMockClientService(t)
		cvpBackupCreated := &backups.V1betaCreateBackupCreated{
			Payload: &models.BackupV1beta{
				ResourceID: "backup-123",
				VolumeID:   volumeId,
				State:      "Available for use",
				BackupID:   "cvp-backup-id",
			},
		}
		mockCVPClient.EXPECT().V1betaCreateBackup(mock.Anything).Return(cvpBackupCreated, nil, nil)

		// Mock createClient function
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockCVPClient}
		}

		_, err := handler.V1betaCreateBackup(ctx, req, params)
		// Should proceed to CVP since volume not found
		assert.NoError(t, err)
	})

	t.Run("WhenPoolIdProvidedAndValidateAndDescribePoolReturnsErrorResponse", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		// Mock ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "a", nil
		}

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock validateAndDescribePool to return error response
		notFoundErr := errors.NewNotFoundErr("Pool", &poolId)
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(nil, notFoundErr)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		badRequest, ok := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(400), badRequest.Code)
		assert.Contains(t, badRequest.Message, fmt.Sprintf("Pool with ID %s not found", poolId))
	})

	t.Run("WhenPoolIdProvidedAndValidateAndDescribePoolReturnsError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		// Mock ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() {
			utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone
		}()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "a", nil
		}

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock validateAndDescribePool to return non-NotFound error
		dbError := fmt.Errorf("database connection error")
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(nil, dbError)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// For non-NotFound errors, validateAndDescribePool returns both errResponse and err
		// but V1betaCreateBackup checks errResponse first, so it returns errResponse, nil
		assert.NoError(t, err)
		assert.NotNil(t, result)
		internalServerError, ok := result.(*gcpgenserver.V1betaCreateBackupInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), internalServerError.Code)
		assert.Equal(t, dbError.Error(), internalServerError.Message)
	})

	t.Run("WhenONTAPModeAndExpertModeVolumeHasDifferentBackupVault", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock pool description to return ONTAP mode
		pool := &coremodels.Pool{
			BaseModel: coremodels.BaseModel{
				UUID: poolId,
			},
			APIAccessMode: ONTAPMode,
		}
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(pool, nil)

		// Mock GetExpertModeVolumeByUUID to return expert mode volume with different backup vault
		differentBackupVaultId := "different-vault-123"
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: volumeId,
			},
			BackupConfig: &datamodel.DataProtection{
				BackupVaultID: differentBackupVaultId,
			},
		}
		mockOrch.EXPECT().
			GetExpertModeVolumeByExternalUUID(ctx, volumeId).
			Return(expertModeVol, nil)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		badRequest, ok := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(400), badRequest.Code)
		assert.Contains(t, badRequest.Message, fmt.Sprintf("Expert mode volume %s is associated with a different backup vault %s", volumeId, differentBackupVaultId))
	})

	t.Run("WhenONTAPModeAndExpertModeVolumeNotFound_ShouldReturnBadRequest", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		pool := &coremodels.Pool{
			BaseModel: coremodels.BaseModel{
				UUID: poolId,
			},
			APIAccessMode: ONTAPMode,
		}
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(pool, nil)

		mockOrch.EXPECT().
			GetExpertModeVolumeByExternalUUID(ctx, volumeId).
			Return(nil, errors.NewNotFoundErr("Expert mode volume", &volumeId))

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupBadRequest{}, result)
		badRequest := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
		assert.Equal(t, float64(400), badRequest.Code)
		assert.Equal(t, fmt.Sprintf("Expert mode volume with ID %s not found", volumeId), badRequest.Message)
	})

	t.Run("WhenONTAPModeAndExpertModeVolumeLookupFailsWithInternalError_ShouldReturnInternalServerError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		pool := &coremodels.Pool{
			BaseModel: coremodels.BaseModel{
				UUID: poolId,
			},
			APIAccessMode: ONTAPMode,
		}
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(pool, nil)

		expertLookupErr := fmt.Errorf("expert mode lookup failed")
		mockOrch.EXPECT().
			GetExpertModeVolumeByExternalUUID(ctx, volumeId).
			Return(nil, expertLookupErr)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupInternalServerError{}, result)
		internalErr := result.(*gcpgenserver.V1betaCreateBackupInternalServerError)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, expertLookupErr.Error(), internalErr.Message)
	})

	t.Run("WhenONTAPModeAndExpertModeVolumeHasNoBackupVaultAndCheckAndFetchBackupVaultReturnsError", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock pool description to return ONTAP mode
		pool := &coremodels.Pool{
			BaseModel: coremodels.BaseModel{
				UUID: poolId,
			},
			APIAccessMode: ONTAPMode,
		}
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(pool, nil)

		// Mock GetExpertModeVolumeByUUID to return expert mode volume with no backup vault
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: volumeId,
			},
			BackupConfig: nil, // No backup vault attached
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{
					UUID: "account-uuid",
				},
				Name: projectNumber,
			},
		}
		mockOrch.EXPECT().
			GetExpertModeVolumeByExternalUUID(ctx, volumeId).
			Return(expertModeVol, nil)

		// Mock GetBackupVaultByUUID to return error (checkAndFetchBackupVault will fail)
		dbError := fmt.Errorf("database error")
		mockOrch.EXPECT().
			GetBackupVaultByUUID(ctx, backupVaultId, projectNumber).
			Return(nil, dbError)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.Error(t, err)
		assert.NotNil(t, result)
		internalServerError, ok := result.(*gcpgenserver.V1betaCreateBackupInternalServerError)
		assert.True(t, ok)
		assert.Equal(t, float64(500), internalServerError.Code)
		assert.Contains(t, internalServerError.Message, dbError.Error())
	})

	t.Run("WhenONTAPModeAndExpertModeVolumeHasNoBackupVaultAndCheckAndFetchBackupVaultReturnsNil", func(t *testing.T) {
		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		oldOntapModebackupEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = oldOntapModebackupEnabled }()
		ExpertModeBackupEnabled = true

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock pool description to return ONTAP mode
		pool := &coremodels.Pool{
			BaseModel: coremodels.BaseModel{
				UUID: poolId,
			},
			APIAccessMode: ONTAPMode,
		}
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(pool, nil)

		// Mock GetExpertModeVolumeByUUID to return expert mode volume with no backup vault
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: volumeId,
			},
			BackupConfig: nil, // No backup vault attached
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{
					UUID: "account-uuid",
				},
				Name: projectNumber,
			},
		}
		mockOrch.EXPECT().
			GetExpertModeVolumeByExternalUUID(ctx, volumeId).
			Return(expertModeVol, nil)

		// Mock GetBackupVaultByUUID to return NotFound; with CVP_HOST unset the handler falls
		// through to the nil-vault check and returns a 400 BadRequest (no error on the caller).
		notFoundErr := errors.NewNotFoundErr("Backup vault", &backupVaultId)
		mockOrch.EXPECT().
			GetBackupVaultByUUID(ctx, backupVaultId, projectNumber).
			Return(nil, notFoundErr)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.NoError(t, err)
		assert.NotNil(t, result)
		badRequest, ok := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
		assert.True(t, ok)
		assert.Equal(t, float64(400), badRequest.Code)
		assert.Contains(t, badRequest.Message, "not found")
	})

	t.Run("WhenNonONTAPModeCallsGetVolume", func(t *testing.T) {
		origCVPHost := cvp.CVP_HOST
		cvp.CVP_HOST = "localhost:8009"
		defer func() { cvp.CVP_HOST = origCVPHost }()

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock pool description to return non-ONTAP mode
		pool := &coremodels.Pool{
			BaseModel: coremodels.BaseModel{
				UUID: poolId,
			},
			APIAccessMode: "REST", // Non-ONTAP mode
		}
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(pool, nil)

		// Should call GetVolume for non-ONTAP mode
		mockOrch.EXPECT().
			GetVolume(ctx, volumeId, false).
			Return(nil, errors.NewNotFoundErr("Volume", &volumeId))

		// Mock checkIfBackupExistInCVP
		originalCheckIfBackupExistInCVP := checkIfBackupExistInCVP
		defer func() { checkIfBackupExistInCVP = originalCheckIfBackupExistInCVP }()
		checkIfBackupExistInCVP = func(ctx context.Context, backupID *string, params gcpgenserver.V1betaCreateBackupParams) (bool, error) {
			return false, nil
		}

		// Mock ListBackups with exact filters
		mockOrch.EXPECT().
			ListBackups(ctx, backupVaultId, projectNumber, [][]interface{}{{"name = ?", "backup-123"}}).
			Return([]*datamodel.Backup{}, nil)

		// Mock CVP client and successful backup creation
		mockCVPClient := backups.NewMockClientService(t)
		cvpBackupCreated := &backups.V1betaCreateBackupCreated{
			Payload: &models.BackupV1beta{
				ResourceID: "backup-123",
				VolumeID:   volumeId,
				State:      "Available for use",
				BackupID:   "cvp-backup-id",
			},
		}
		mockCVPClient.EXPECT().V1betaCreateBackup(mock.Anything).Return(cvpBackupCreated, nil, nil)

		// Mock createClient function
		originalCreateClient := createClient
		defer func() { createClient = originalCreateClient }()
		createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
			return cvpapi.Cvp{Backups: mockCVPClient}
		}

		_, err := handler.V1betaCreateBackup(ctx, req, params)
		// Should proceed to CVP since volume not found
		assert.NoError(t, err)
	})

	t.Run("WhenUseVCPRegionEnabledAndListBackupsFailsInVCPCheck_ShouldReturnInternalServerError", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = ""

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: "backup-123",
			PoolId:     gcpgenserver.OptString{},
		}

		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		vol := &coremodels.Volume{
			BaseModel: coremodels.BaseModel{
				UUID: volumeId,
			},
			AccountName: projectNumber,
		}
		mockOrch.EXPECT().GetVolume(ctx, volumeId, false).Return(vol, nil)

		listErr := fmt.Errorf("vcp list backups failed")
		mockOrch.EXPECT().
			ListBackups(ctx, backupVaultId, projectNumber, [][]interface{}{{"name = ?", "backup-123"}}).
			Return(nil, listErr)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		assert.Error(t, err)
		assert.NotNil(t, result)
		assert.IsType(t, &gcpgenserver.V1betaCreateBackupInternalServerError{}, result)
		internalErr := result.(*gcpgenserver.V1betaCreateBackupInternalServerError)
		assert.Equal(t, float64(500), internalErr.Code)
		assert.Equal(t, listErr.Error(), internalErr.Message)
		assert.Equal(t, listErr.Error(), err.Error())
	})
}

// TestV1betaCreateBackup_ConflictError tests the conflict error handling when creating a backup
func TestV1betaCreateBackup_ConflictError(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	t.Run("WhenCreateBackupReturnsConflictErr", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		volumeId := "volume-123"
		resourceId := "backup-123"
		projectNumber := "proj-123"
		backupVaultId := "vault-123"

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: resourceId,
		}

		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:     "us-east4",
			ProjectNumber:  projectNumber,
			BackupVaultId:  backupVaultId,
			XCorrelationID: gcpgenserver.NewOptString("corr-id"),
		}

		// Mock utils.ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-east4", "us-east4-a", nil
		}

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock GetVolume to succeed (volume exists in VSA)
		vol := &coremodels.Volume{
			BaseModel: coremodels.BaseModel{
				UUID: volumeId,
			},
			AccountName: projectNumber,
		}
		mockOrch.EXPECT().
			GetVolume(ctx, volumeId, false).
			Return(vol, nil)

		// Mock checkIfBackupExistInCVP to return false
		originalCheckIfBackupExistInCVP := checkIfBackupExistInCVP
		defer func() { checkIfBackupExistInCVP = originalCheckIfBackupExistInCVP }()
		checkIfBackupExistInCVP = func(ctx context.Context, backupID *string, params gcpgenserver.V1betaCreateBackupParams) (bool, error) {
			return false, nil
		}

		// Mock CreateBackup to return a conflict error
		conflictErr := errors.NewConflictErr("Backup with the same name already exists in the specified backup vault")
		mockOrch.EXPECT().
			CreateBackup(ctx, mock.AnythingOfType("*common.CreateBackupParams")).
			Return(nil, "", conflictErr)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)

		conflictResp, ok := result.(*gcpgenserver.V1betaCreateBackupConflict)
		assert.True(t, ok, "Expected V1betaCreateBackupConflict response")
		assert.Equal(t, float64(409), conflictResp.Code)
		assert.Contains(t, conflictResp.Message, "Backup with the same name already exists")
	})

	t.Run("WhenCreateBackupReturnsConflictErr_ExpertMode", func(t *testing.T) {
		logger := &log.MockLogger{}
		ctx := context.Background()
		ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, logger)

		// Enable expert mode backups
		origExpertModeEnabled := ExpertModeBackupEnabled
		defer func() { ExpertModeBackupEnabled = origExpertModeEnabled }()
		ExpertModeBackupEnabled = true

		mockOrch := factory.NewMockOrchestratorFactory(t)
		handler := Handler{Orchestrator: mockOrch}

		volumeId := "expert-volume-123"
		resourceId := "backup-456"
		projectNumber := "proj-456"
		backupVaultId := "vault-456"
		poolId := "pool-expert"

		req := &gcpgenserver.BackupCreateV1beta{
			VolumeId:   volumeId,
			ResourceId: resourceId,
			PoolId:     gcpgenserver.NewOptString(poolId),
		}

		params := gcpgenserver.V1betaCreateBackupParams{
			LocationId:     "us-central1",
			ProjectNumber:  projectNumber,
			BackupVaultId:  backupVaultId,
			XCorrelationID: gcpgenserver.NewOptString("corr-id-2"),
		}

		// Mock utils.ParseAndValidateRegionAndZone to succeed
		originalParseAndValidateRegionAndZone := utils.ParseAndValidateRegionAndZone
		defer func() { utils.ParseAndValidateRegionAndZone = originalParseAndValidateRegionAndZone }()
		utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
			return "us-central1", "us-central1-a", nil
		}

		// Mock backup vault lookup
		bv := &coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultId,
			AccountName:   projectNumber,
			ServiceType:   "GCNV",
		}
		mockOrch.EXPECT().GetBackupVaultByUUIDWithoutAccount(mock.Anything, backupVaultId).Return(bv, nil)

		// Mock pool description to return ONTAP mode
		pool := &coremodels.Pool{
			BaseModel: coremodels.BaseModel{
				UUID: poolId,
			},
			APIAccessMode: ONTAPMode,
		}
		mockOrch.EXPECT().
			DescribePool(ctx, poolId, projectNumber).
			Return(pool, nil)

		// Mock GetExpertModeVolumeByExternalUUID to succeed
		expertModeVol := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{
				UUID: volumeId,
			},
			BackupConfig: &datamodel.DataProtection{
				BackupVaultID: backupVaultId,
			},
			Account: &datamodel.Account{
				BaseModel: datamodel.BaseModel{
					UUID: "account-uuid",
				},
				Name: projectNumber,
			},
		}
		mockOrch.EXPECT().
			GetExpertModeVolumeByExternalUUID(ctx, volumeId).
			Return(expertModeVol, nil)

		// Mock checkIfBackupExistInCVP to return false
		originalCheckIfBackupExistInCVP := checkIfBackupExistInCVP
		defer func() { checkIfBackupExistInCVP = originalCheckIfBackupExistInCVP }()
		checkIfBackupExistInCVP = func(ctx context.Context, backupID *string, params gcpgenserver.V1betaCreateBackupParams) (bool, error) {
			return false, nil
		}

		// Mock CreateBackup to return a conflict error
		conflictErr := errors.NewConflictErr("Backup with the same name already exists in the specified backup vault")
		mockOrch.EXPECT().
			CreateBackup(ctx, mock.AnythingOfType("*common.CreateBackupParams")).
			Return(nil, "", conflictErr)

		result, err := handler.V1betaCreateBackup(ctx, req, params)

		// Assertions
		assert.NoError(t, err)
		assert.NotNil(t, result)

		conflictResp, ok := result.(*gcpgenserver.V1betaCreateBackupConflict)
		assert.True(t, ok, "Expected V1betaCreateBackupConflict response")
		assert.Equal(t, float64(409), conflictResp.Code)
		assert.Contains(t, conflictResp.Message, "Backup with the same name already exists")
	})
}

// ===== Tests for GCBDR coverage in V1betaCreateBackup =====

func TestV1betaCreateBackup_BackupVaultNotInVCP_FallsThroughToCVP(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// When vault is not in VCP (e.g. SDE-only vault), the handler should proceed
	// as a regular non-GCBDR vault and fall through to the CVP path.
	origBackupEnabled := backupEnabled
	origParse := utils.ParseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		utils.ParseAndValidateRegionAndZone = origParse
	}()
	backupEnabled = true
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1-a", nil
	}

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockOrch := factory.NewMockOrchestratorFactory(t)

	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  "sde-vault",
		LocationId:     "us-central1-a",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.BackupCreateV1beta{
		VolumeId:   "vol-id",
		ResourceId: "backup-id",
	}

	// Vault not in VCP — should NOT return 400, should proceed
	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
		Return(nil, errors.NewNotFoundErr("backup vault", nil))
	// Volume not found in VCP either — triggers CVP fallback
	mockOrch.EXPECT().
		GetVolume(ctx, "vol-id", false).
		Return(nil, errors.NewNotFoundErr("Volume not found", nil))
	// Duplicate check before CVP call
	mockOrch.EXPECT().
		ListBackups(ctx, params.BackupVaultId, params.ProjectNumber, mock.Anything).
		Return(nil, errors.NewNotFoundErr("no backups", nil))

	// Mock CVP client — backup created successfully in CVP
	mockCVPClient := backups.NewMockClientService(t)
	originalCreateClient := createClient
	defer func() { createClient = originalCreateClient }()
	createClient = func(logger log.Logger, jwtToken string) cvpapi.Cvp {
		return cvpapi.Cvp{Backups: mockCVPClient}
	}
	cvpBackupCreated := &backups.V1betaCreateBackupCreated{
		Payload: &models.BackupV1beta{
			ResourceID: "backup-id",
			VolumeID:   "vol-id",
			State:      "Available for use",
			BackupID:   "cvp-backup-id",
		},
	}
	mockCVPClient.EXPECT().V1betaCreateBackup(mock.Anything).Return(cvpBackupCreated, nil, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaCreateBackup(ctx, req, params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.OperationV1beta{}, result, "Should reach CVP path, not return 400 vault-not-found")
	operation := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, operation.Done.Value)
}

func TestV1betaCreateBackup_BackupVaultInternalError(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// Covers backup_endpoints.go line 172: GetBackupVaultByUUIDWithoutAccount returns non-NotFound error
	origBackupEnabled := backupEnabled
	origParse := utils.ParseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		utils.ParseAndValidateRegionAndZone = origParse
	}()
	backupEnabled = true
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1-a", nil
	}

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockOrch := factory.NewMockOrchestratorFactory(t)

	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  "vault-id",
		LocationId:     "us-central1-a",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.BackupCreateV1beta{
		VolumeId:   "vol-id",
		ResourceId: "backup-id",
	}

	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
		Return(nil, fmt.Errorf("database connection error"))

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaCreateBackup(ctx, req, params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.V1betaCreateBackupInternalServerError{}, result)
	serverErr := result.(*gcpgenserver.V1betaCreateBackupInternalServerError)
	assert.Equal(t, float64(500), serverErr.Code)
}

func TestV1betaCreateBackup_NonGCBDR_VaultNotBelongingToAccount(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// Covers backup_endpoints.go lines 183-190: non-GCBDR vault with mismatched account
	origBackupEnabled := backupEnabled
	origParse := utils.ParseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		utils.ParseAndValidateRegionAndZone = origParse
	}()
	backupEnabled = true
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1-a", nil
	}

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockOrch := factory.NewMockOrchestratorFactory(t)

	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  "vault-id",
		LocationId:     "us-central1-a",
		ProjectNumber:  "project-A",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.BackupCreateV1beta{
		VolumeId:   "vol-id",
		ResourceId: "backup-id",
	}

	// Return a GCNV vault that belongs to a different project
	vcpBv := &coremodels.BackupVaultV1beta{
		BackupVaultID: params.BackupVaultId,
		ServiceType:   "GCNV",
		AccountName:   "project-B", // different from ProjectNumber
	}
	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
		Return(vcpBv, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaCreateBackup(ctx, req, params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.V1betaCreateBackupBadRequest{}, result)
	badReq := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
	assert.Equal(t, float64(400), badReq.Code)
	assert.Contains(t, badReq.Message, "does not belong to account")
}

func TestV1betaCreateBackup_NonGCBDR_VolumeNotBelongingToAccount(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// Covers backup_endpoints.go lines 274-282: volume AccountName != ProjectNumber
	origBackupEnabled := backupEnabled
	origParse := utils.ParseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		utils.ParseAndValidateRegionAndZone = origParse
	}()
	backupEnabled = true
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1-a", nil
	}

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockOrch := factory.NewMockOrchestratorFactory(t)
	projectNumber := "project-A"
	volumeId := "vol-id"

	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  "vault-id",
		LocationId:     "us-central1-a",
		ProjectNumber:  projectNumber,
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.BackupCreateV1beta{
		VolumeId:   volumeId,
		ResourceId: "backup-id",
	}

	// GCNV vault that matches the project
	vcpBv := &coremodels.BackupVaultV1beta{
		BackupVaultID: params.BackupVaultId,
		ServiceType:   "GCNV",
		AccountName:   projectNumber,
	}
	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
		Return(vcpBv, nil)

	// Volume belongs to a different project
	vol := &coremodels.Volume{
		BaseModel:   coremodels.BaseModel{UUID: volumeId},
		AccountName: "project-B", // different from ProjectNumber
	}
	mockOrch.EXPECT().
		GetVolume(ctx, volumeId, false).
		Return(vol, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaCreateBackup(ctx, req, params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.V1betaCreateBackupBadRequest{}, result)
	badReq := result.(*gcpgenserver.V1betaCreateBackupBadRequest)
	assert.Equal(t, float64(400), badReq.Code)
	assert.Contains(t, badReq.Message, "does not belong to account")
}

func TestV1betaCreateBackup_GCBDR_CrossProjectPoolLookupByUUID(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	origBackupEnabled := backupEnabled
	origParse := utils.ParseAndValidateRegionAndZone
	origExpertModeBackupEnabled := ExpertModeBackupEnabled
	defer func() {
		backupEnabled = origBackupEnabled
		utils.ParseAndValidateRegionAndZone = origParse
		ExpertModeBackupEnabled = origExpertModeBackupEnabled
	}()
	backupEnabled = true
	ExpertModeBackupEnabled = true
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1-a", nil
	}

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockOrch := factory.NewMockOrchestratorFactory(t)
	vaultOwnerProject := "1075717434858"
	poolOwnerProject := "182893078521"
	poolID := "6cbc7f3d-ee9a-ab9e-39c3-a02dd1be4e67"
	volumeID := "51479a85-7a8f-11f1-83f5-99babe44685f"
	backupVaultID := "d69c1ca6-0333-d3d2-d760-4ccfa7d98855"

	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  backupVaultID,
		LocationId:     "us-central1-a",
		ProjectNumber:  vaultOwnerProject,
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.BackupCreateV1beta{
		VolumeId:   volumeID,
		ResourceId: "backup-resource-id",
		PoolId:     gcpgenserver.NewOptString(poolID),
	}

	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, backupVaultID).
		Return(&coremodels.BackupVaultV1beta{
			BackupVaultID: backupVaultID,
			ServiceType:   datamodel.ServiceTypeCrossProject,
			AccountName:   vaultOwnerProject,
		}, nil)

	ontapPool := &coremodels.Pool{
		BaseModel:     coremodels.BaseModel{UUID: poolID},
		APIAccessMode: ONTAPMode,
		AccountName:   poolOwnerProject,
	}
	mockOrch.EXPECT().
		GetPoolsByUUIDs(ctx, []string{poolID}, common.PoolFetchOptions{}).
		Return([]*coremodels.Pool{ontapPool}, nil)

	expertModeVol := &datamodel.ExpertModeVolumes{
		BaseModel: datamodel.BaseModel{UUID: volumeID},
		BackupConfig: &datamodel.DataProtection{
			BackupVaultID: backupVaultID,
		},
	}
	mockOrch.EXPECT().
		GetExpertModeVolumeByExternalUUID(ctx, volumeID).
		Return(expertModeVol, nil)

	filters := [][]interface{}{{"name = ?", req.ResourceId}}
	mockOrch.EXPECT().
		ListBackupsWithoutAccountFilter(ctx, backupVaultID, filters).
		Return(nil, nil)

	mockOrch.EXPECT().
		CreateBackup(ctx, mock.Anything).
		Return(&coremodels.Backup{}, "job-id", nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaCreateBackup(ctx, req, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	_, ok := result.(*gcpgenserver.OperationV1beta)
	assert.True(t, ok, "expected operation response, got %T", result)
}

func TestV1betaCreateBackup_GCBDR_SkipsCVPCheckAndDetectsConflictInVCP(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// Covers backup_endpoints.go lines 441-457: GCBDR path skips CVP and checks VCP
	origBackupEnabled := backupEnabled
	origParse := utils.ParseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		utils.ParseAndValidateRegionAndZone = origParse
	}()
	backupEnabled = true
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1-a", nil
	}

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockOrch := factory.NewMockOrchestratorFactory(t)
	projectNumber := "project-A"
	volumeId := "vol-id"

	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  "gcbdr-vault",
		LocationId:     "us-central1-a",
		ProjectNumber:  projectNumber,
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.BackupCreateV1beta{
		VolumeId:   volumeId,
		ResourceId: "existing-backup",
	}

	// GCBDR vault
	vcpBv := &coremodels.BackupVaultV1beta{
		BackupVaultID: params.BackupVaultId,
		ServiceType:   "CrossProject",
		AccountName:   "vault-owner",
	}
	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
		Return(vcpBv, nil)

	// Volume fetch
	vol := &coremodels.Volume{
		BaseModel:   coremodels.BaseModel{UUID: volumeId},
		AccountName: projectNumber,
	}
	mockOrch.EXPECT().
		GetVolume(ctx, volumeId, false).
		Return(vol, nil)

	// GCBDR path: ListBackupsWithoutAccountFilter returns existing backup (conflict)
	existingBackup := []*datamodel.Backup{
		{Name: "existing-backup"},
	}
	filters := [][]interface{}{{"name = ?", req.ResourceId}}
	mockOrch.EXPECT().
		ListBackupsWithoutAccountFilter(ctx, params.BackupVaultId, filters).
		Return(existingBackup, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaCreateBackup(ctx, req, params)

	assert.NoError(t, err)
	assert.IsType(t, &gcpgenserver.V1betaCreateBackupConflict{}, result)
	conflictResp := result.(*gcpgenserver.V1betaCreateBackupConflict)
	assert.Equal(t, float64(409), conflictResp.Code)
	assert.Contains(t, conflictResp.Message, "already exists")
}

func TestV1betaCreateBackup_GCBDR_VCPCheckError(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// Covers backup_endpoints.go lines 447-449: GCBDR VCP backup check error
	origBackupEnabled := backupEnabled
	origParse := utils.ParseAndValidateRegionAndZone
	defer func() {
		backupEnabled = origBackupEnabled
		utils.ParseAndValidateRegionAndZone = origParse
	}()
	backupEnabled = true
	utils.ParseAndValidateRegionAndZone = func(locationID string) (string, string, *gcpgenserver.Error) {
		return "us-central1", "us-central1-a", nil
	}

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	mockOrch := factory.NewMockOrchestratorFactory(t)
	projectNumber := "project-A"
	volumeId := "vol-id"

	params := gcpgenserver.V1betaCreateBackupParams{
		BackupVaultId:  "gcbdr-vault",
		LocationId:     "us-central1-a",
		ProjectNumber:  projectNumber,
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}
	req := &gcpgenserver.BackupCreateV1beta{
		VolumeId:   volumeId,
		ResourceId: "backup-id",
	}

	// GCBDR vault
	vcpBv := &coremodels.BackupVaultV1beta{
		BackupVaultID: params.BackupVaultId,
		ServiceType:   "CrossProject",
		AccountName:   "vault-owner",
	}
	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
		Return(vcpBv, nil)

	vol := &coremodels.Volume{BaseModel: coremodels.BaseModel{UUID: volumeId}, AccountName: projectNumber}
	mockOrch.EXPECT().GetVolume(ctx, volumeId, false).Return(vol, nil)

	// ListBackupsWithoutAccountFilter returns error
	filters := [][]interface{}{{"name = ?", req.ResourceId}}
	mockOrch.EXPECT().
		ListBackupsWithoutAccountFilter(ctx, params.BackupVaultId, filters).
		Return(nil, fmt.Errorf("database error"))

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaCreateBackup(ctx, req, params)

	assert.Error(t, err)
	assert.IsType(t, &gcpgenserver.V1betaCreateBackupInternalServerError{}, result)
}

// ===== Tests for GCBDR coverage in V1betaListBackups =====

func TestV1betaListBackups_GCBDR_UsesListBackupsWithoutAccountFilter(t *testing.T) {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()

	// Covers backup_endpoints.go lines 816-817
	origBackupEnabled := backupEnabled
	defer func() { backupEnabled = origBackupEnabled }()
	backupEnabled = true

	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, mockLogger)

	params := gcpgenserver.V1betaListBackupsParams{
		BackupVaultId:  "gcbdr-vault-id",
		LocationId:     "us-central1",
		ProjectNumber:  "12345",
		XCorrelationID: gcpgenserver.NewOptString("corr-id"),
	}

	mockOrch := factory.NewMockOrchestratorFactory(t)

	// Return GCBDR vault
	vcpBv := &coremodels.BackupVaultV1beta{
		BackupVaultID: params.BackupVaultId,
		ServiceType:   "CrossProject",
		AccountName:   "vault-owner",
	}
	mockOrch.EXPECT().
		GetBackupVaultByUUIDWithoutAccount(ctx, params.BackupVaultId).
		Return(vcpBv, nil)

	// GCBDR path: ListBackupsWithoutAccountFilter (no CVP call)
	sourceRegion := "us-central1"
	backupVaultData := &datamodel.BackupVault{
		Name:             "gcbdr-vault",
		ServiceType:      "CrossProject",
		SourceRegionName: &sourceRegion,
		BucketDetails:    datamodel.BucketDetailsArray{&datamodel.BucketDetails{BucketName: "gcbdr-bucket"}},
	}
	backups := []*datamodel.Backup{
		{
			State:         "Available",
			Name:          "gcbdr-backup-1",
			VolumeUUID:    "vol-1",
			BackupVault:   backupVaultData,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:        "gcbdr-bucket",
				AccountIdentifier: "12345",
				VolumeName:        "vol-1",
				SourceVolumeZone:  "us-central1",
			},
		},
	}
	mockOrch.EXPECT().
		ListBackupsWithoutAccountFilter(ctx, params.BackupVaultId, mock.Anything).
		Return(backups, nil)

	handler := Handler{Orchestrator: mockOrch}
	result, err := handler.V1betaListBackups(ctx, params)

	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.IsType(t, &gcpgenserver.V1betaListBackupsOK{}, result)
	response := result.(*gcpgenserver.V1betaListBackupsOK)
	assert.Len(t, response.Backups, 1)
	assert.Equal(t, "gcbdr-backup-1", response.Backups[0].ResourceId.Value)
}

func TestConvertBackupDataModelToBackupsV1beta_CrossRegionBackupId(t *testing.T) {
	t.Run("SetsCrossRegionBackupIdForCrossRegionVault", func(t *testing.T) {
		backup := &datamodel.Backup{
			Name:         "test-backup",
			VolumeUUID:   "vol-uuid",
			State:        "READY",
			ExternalUUID: "remote-backup-uuid",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName: "bucket",
				VolumeName: "vol",
			},
			BackupVault: &datamodel.BackupVault{
				BackupVaultType:  "CROSS_REGION",
				SourceRegionName: func() *string { s := "us-east4"; return &s }(),
				BackupRegionName: func() *string { s := "us-west1"; return &s }(),
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}
		backup.UUID = "backup-uuid"
		backup.BackupVault.UUID = "vault-uuid"

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.True(t, result.CrossRegionBackupId.Set)
		assert.Equal(t, "remote-backup-uuid", result.CrossRegionBackupId.Value)
	})

	t.Run("DoesNotSetCrossRegionBackupIdForInRegionVault", func(t *testing.T) {
		backup := &datamodel.Backup{
			Name:         "test-backup",
			VolumeUUID:   "vol-uuid",
			State:        "READY",
			ExternalUUID: "",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName: "bucket",
				VolumeName: "vol",
			},
			BackupVault: &datamodel.BackupVault{
				BackupVaultType:  "IN_REGION",
				SourceRegionName: func() *string { s := "us-east4"; return &s }(),
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}
		backup.UUID = "backup-uuid"
		backup.BackupVault.UUID = "vault-uuid"

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.False(t, result.CrossRegionBackupId.Set)
	})

	t.Run("DoesNotSetCrossRegionBackupIdWhenExternalUUIDEmptyOnCrossRegion", func(t *testing.T) {
		backup := &datamodel.Backup{
			Name:         "test-backup",
			VolumeUUID:   "vol-uuid",
			State:        "READY",
			ExternalUUID: "",
			Type:         "MANUAL",
			Attributes: &datamodel.BackupAttributes{
				BucketName: "bucket",
				VolumeName: "vol",
			},
			BackupVault: &datamodel.BackupVault{
				BackupVaultType:  "CROSS_REGION",
				SourceRegionName: func() *string { s := "us-east4"; return &s }(),
				BackupRegionName: func() *string { s := "us-west1"; return &s }(),
				BucketDetails:    datamodel.BucketDetailsArray{},
			},
		}
		backup.UUID = "backup-uuid"
		backup.BackupVault.UUID = "vault-uuid"

		result := convertBackupDataModelToBackupsV1beta(backup)

		assert.False(t, result.CrossRegionBackupId.Set)
	})
}
