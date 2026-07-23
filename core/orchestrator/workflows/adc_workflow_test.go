package workflows

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestADCWorkflow(t *testing.T) {
	t.Run("onSuccess", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Logical size calculation activities
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GenerateResourceTimestampFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - GenerateResourceTimestamp fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("", errors.New("failed to generate timestamp"))

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("IsServiceAccountCreatedFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - IsServiceAccountCreated fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(false, errors.New("failed to check service account"))
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("IsServiceAccountCreatedRetriesUseServiceAccountPolicy", func(t *testing.T) {
		origRetryMaxAttempts := RetryMaxAttempts
		origSARetryStartToCloseTimeout := SARetryStartToCloseTimeout
		origSARetryInitialInterval := SARetryInitialInterval
		origSARetryBackoffCoefficient := SARetryBackoffCoefficient
		origSARetryMaximumInterval := SARetryMaximumInterval
		origSARetryMaximumAttempts := SARetryMaximumAttempts
		defer func() {
			RetryMaxAttempts = origRetryMaxAttempts
			SARetryStartToCloseTimeout = origSARetryStartToCloseTimeout
			SARetryInitialInterval = origSARetryInitialInterval
			SARetryBackoffCoefficient = origSARetryBackoffCoefficient
			SARetryMaximumInterval = origSARetryMaximumInterval
			SARetryMaximumAttempts = origSARetryMaximumAttempts
		}()

		// Keep generic retry low and SA retry high to prove SA context is used.
		RetryMaxAttempts = 1
		SARetryStartToCloseTimeout = "5m"
		SARetryInitialInterval = "1s"
		SARetryBackoffCoefficient = "1.0"
		SARetryMaximumInterval = "1s"
		SARetryMaximumAttempts = 3

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		attemptCount := 0
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) { attemptCount++ }).
			Return(false, errors.New("failed to check service account"))
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Equal(t, SARetryMaximumAttempts, attemptCount, "IsServiceAccountCreated should use SA retry attempts")
		env.AssertExpectations(t)
	})

	t.Run("AttachRolesToServiceAccountFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - AttachRolesToServiceAccount fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to attach roles"))
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("CreateHmacKeysFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - CreateHmacKeys fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(nil, errors.New("failed to create HMAC keys"))
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeployADCCloudRunServiceFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - DeployADCCloudRunService fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(nil, errors.New("failed to deploy Cloud Run service"))
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("CheckOperationStatusFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - CheckOperationStatus fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		// First call to CheckOperationStatus (for deployment) fails
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/test-operation").Return(false, errors.New("failed to check operation status"))
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetADCServiceURLFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - GetADCServiceURL fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("failed to get service URL"))
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("InitialDeleteRequestWithCloudRunFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - InitialDeleteRequestWithCloudRun fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to initiate delete request"))
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("RetryPolicyFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - GenerateResourceTimestamp fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("", errors.New("failed to generate timestamp"))

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("CleanupTimeout", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - all succeed until cleanup
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		// Mock cleanup operation to timeout (never complete)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(false, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Logical size calculation activities
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution - should complete successfully even with cleanup timeout
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SleepFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - all succeed until sleep
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Logical size calculation activities
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
	t.Run("GetBucketDetailsFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data with non-matching bucket name
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "different-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket", // This doesn't match the bucket in backupVault
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("CreateServiceAccountFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - CreateServiceAccount fails
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to create service account"))

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ServiceAccountNotCreated", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - IsServiceAccountCreated returns false
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(false, nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("FetchLogicalSizeAndUpdateActivityFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - all succeed until logical size calculation
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Logical size refresh failure is non-blocking.
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to fetch logical size"))

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution - should complete successfully even with logical size error
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("LatestBackupDelete_CallsFetchLogicalSizeAndUpdateActivity", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Always refresh logical size; pass endpoint UUID from the deleted backup.
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, backup.VolumeUUID, "endpoint-uuid", mock.Anything, mock.Anything).
			Return(&activities.LogicalSizeUpdateResult{OldSize: 2048, NewSize: 1024, LatestBackupID: 42}, nil)

		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("LastBackupDelete_CompletesWithoutLogicalSizeError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}, Name: "test-account"}
		backupVault := &datamodel.BackupVault{
			Name:        "test-backup-vault",
			ServiceType: datamodel.ServiceTypeGCNV,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{AccessKey: "dGVzdC1hY2Nlc3Mta2V5", SecretKey: "dGVzdC1zZWNyZXQta2V5"}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/test-operation", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{StatusCode: http.StatusOK}, nil)
		// Step 9: No remaining backup on endpoint; fetch error is non-blocking and recalc still runs.
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, backup.VolumeUUID, "endpoint-uuid", mock.Anything, mock.Anything).
			Return(nil, errors.New("no latest backup for endpoint endpoint-uuid on volume test-vol"))
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, backup.VolumeUUID).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/cleanup-123", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertNotCalled(t, "BackPropagateGcbdrChainSizesForDeletedBackupActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		env.AssertExpectations(t)
	})

	t.Run("ADCWorkflowWithBackupVaultSwitchingEnabled_UsesPerVaultLatest", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(true)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// When backup vault switching is enabled, logical size refresh still runs with deleted backup excluded.
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("OrphanBackupDelete_FlagOff_CallsFetchLogicalSizeAndUpdateActivity", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(false)

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}, Name: "test-account"}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{AccessKey: "dGVzdC1hY2Nlc3Mta2V5", SecretKey: "dGVzdC1zZWNyZXQta2V5"}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/test-operation", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{StatusCode: http.StatusOK}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/cleanup-123", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Flag off: use single-vault activity
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ProgressiveSleepWithRedirect", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - InitialDeleteRequestWithCloudRun returns redirect
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		// Initial delete request returns redirect
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode:  http.StatusTemporaryRedirect,
			RedirectURL: "https://adc-svc-20231201120000abcd-abc123.run.app/status/123",
		}, nil)
		// Status check returns OK after progressive sleep
		env.OnActivity("CheckDeleteStatusWithCloudRun", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Logical size calculation activities
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ProgressiveSleepWithMultipleRedirects", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - InitialDeleteRequestWithCloudRun returns redirect
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		// Initial delete request returns redirect
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode:  http.StatusTemporaryRedirect,
			RedirectURL: "https://adc-svc-20231201120000abcd-abc123.run.app/status/123",
		}, nil)
		// First status check returns another redirect (triggers progressive sleep)
		env.OnActivity("CheckDeleteStatusWithCloudRun", mock.Anything, mock.Anything, mock.Anything, "https://adc-svc-20231201120000abcd-abc123.run.app/status/123").Return(&common.ADCResponse{
			StatusCode:  http.StatusTemporaryRedirect,
			RedirectURL: "https://adc-svc-20231201120000abcd-abc123.run.app/status/456",
		}, nil)
		// Second status check returns OK
		env.OnActivity("CheckDeleteStatusWithCloudRun", mock.Anything, mock.Anything, mock.Anything, "https://adc-svc-20231201120000abcd-abc123.run.app/status/456").Return(&common.ADCResponse{
			StatusCode: http.StatusOK,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Logical size calculation activities
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ProgressiveSleepWithNotFound", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		mockHeader := &commonpb.Header{
			Fields: map[string]*commonpb.Payload{
				"logParam": encodedValue,
			},
		}
		env.SetHeader(mockHeader)
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})

		// Set up test data
		params := &common.DeleteBackupParams{
			BackupVaultUUID: "vault-uuid",
			BackupUUID:      "backup-uuid",
			AccountName:     "test-account",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}

		// Mock activity responses - InitialDeleteRequestWithCloudRun returns redirect
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		// Initial delete request returns redirect
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode:  http.StatusTemporaryRedirect,
			RedirectURL: "https://adc-svc-20231201120000abcd-abc123.run.app/status/123",
		}, nil)
		// Status check returns not found (triggers progressive sleep logic)
		env.OnActivity("CheckDeleteStatusWithCloudRun", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{
			StatusCode: http.StatusNotFound,
		}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Step 9: Logical size calculation activities
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)

		// Execute workflow
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestCalculateProgressiveSleepDuration(t *testing.T) {
	t.Run("FirstPhase", func(t *testing.T) {
		// Test first phase: elapsed < 5 minutes
		duration := calculateProgressiveSleepDuration(2 * time.Minute)
		assert.Equal(t, firstPhaseSleepDuration, duration)
	})

	t.Run("SecondPhase", func(t *testing.T) {
		// Test second phase: 5 minutes <= elapsed < 15 minutes
		duration := calculateProgressiveSleepDuration(10 * time.Minute)
		assert.Equal(t, secondPhaseSleepDuration, duration)
	})

	t.Run("ThirdPhase", func(t *testing.T) {
		// Test third phase: 15 minutes <= elapsed < 75 minutes
		duration := calculateProgressiveSleepDuration(30 * time.Minute)
		assert.Equal(t, thirdPhaseSleepDuration, duration)
	})

	t.Run("FourthPhase", func(t *testing.T) {
		// Test fourth phase: elapsed >= 75 minutes
		duration := calculateProgressiveSleepDuration(2 * time.Hour)
		assert.Equal(t, fourthPhaseSleepDuration, duration)
	})

	t.Run("BoundaryValues", func(t *testing.T) {
		// Test boundary values
		assert.Equal(t, firstPhaseSleepDuration, calculateProgressiveSleepDuration(4*time.Minute+59*time.Second))
		assert.Equal(t, secondPhaseSleepDuration, calculateProgressiveSleepDuration(5*time.Minute))
		assert.Equal(t, secondPhaseSleepDuration, calculateProgressiveSleepDuration(14*time.Minute+59*time.Second))
		assert.Equal(t, thirdPhaseSleepDuration, calculateProgressiveSleepDuration(15*time.Minute))
		assert.Equal(t, thirdPhaseSleepDuration, calculateProgressiveSleepDuration(74*time.Minute+59*time.Second))
		assert.Equal(t, fourthPhaseSleepDuration, calculateProgressiveSleepDuration(75*time.Minute))
	})
}

// TestADCWorkflow_GcbdrBackprop verifies the ADC (orphan/detached) delete path back-propagates the
// chain-size drop to intermediate backups only for GCBDR vaults with a real drop.
func TestADCWorkflow_GcbdrBackprop(t *testing.T) {
	const (
		volumeUUID = "test-vol"
		pivotID    = int64(3)
		latestID   = int64(5)
	)

	setupCommonADCMocks := func(env *testsuite.TestWorkflowEnvironment) {
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{AccessKey: "dGVzdC1hY2Nlc3Mta2V5", SecretKey: "dGVzdC1zZWNyZXQta2V5"}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/test-operation", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc.run.app", nil)
		env.OnActivity("InitialDeleteRequestWithCloudRun", mock.Anything, mock.Anything, mock.Anything).Return(&common.ADCResponse{StatusCode: http.StatusOK}, nil)
		env.OnActivity("RecalculateAndUpdateVolumeBackupChainBytesActivity", mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/cleanup-123", Status: "RUNNING"}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	}

	makeInputs := func(serviceType string) (*common.DeleteBackupParams, *datamodel.BackupVault, *datamodel.Backup, *datamodel.Account) {
		account := &datamodel.Account{BaseModel: datamodel.BaseModel{UUID: "account-uuid"}, Name: "test-account"}
		backupVault := &datamodel.BackupVault{
			Name:        "test-backup-vault",
			ServiceType: serviceType,
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					ServiceAccountName:  "sa-test",
					VendorSubnetID:      "subnet-12345",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{ID: pivotID, UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    volumeUUID,
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		params := &common.DeleteBackupParams{BackupVaultUUID: "vault-uuid", BackupUUID: "backup-uuid", AccountName: "test-account"}
		return params, backupVault, backup, account
	}

	newEnv := func(t *testing.T) *testsuite.TestWorkflowEnvironment {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.RegisterActivity(&activities.CommonActivities{})
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		return env
	}

	t.Run("GCBDR vault with drop back-propagates", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(false)

		env := newEnv(t)
		setupCommonADCMocks(env)
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&activities.LogicalSizeUpdateResult{OldSize: 100, NewSize: 80, LatestBackupID: latestID}, nil)
		env.OnActivity("BackPropagateGcbdrChainSizesForDeletedBackupActivity", mock.Anything, volumeUUID, mock.Anything, pivotID, latestID, int64(20)).
			Return(nil).Once()

		params, backupVault, backup, account := makeInputs(activities.GCBDRServiceType)
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertCalled(t, "BackPropagateGcbdrChainSizesForDeletedBackupActivity", mock.Anything, volumeUUID, mock.Anything, pivotID, latestID, int64(20))
	})

	t.Run("Non-GCBDR vault does not back-propagate", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(false)

		env := newEnv(t)
		setupCommonADCMocks(env)
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&activities.LogicalSizeUpdateResult{OldSize: 100, NewSize: 80, LatestBackupID: latestID}, nil)

		params, backupVault, backup, account := makeInputs(datamodel.ServiceTypeGCNV)
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertNotCalled(t, "BackPropagateGcbdrChainSizesForDeletedBackupActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("GCBDR vault with no drop does not back-propagate", func(t *testing.T) {
		originalFlag := utils.EnableBackupVaultSwitching
		defer utils.SetEnableBackupVaultSwitchingForTest(originalFlag)
		utils.SetEnableBackupVaultSwitchingForTest(false)

		env := newEnv(t)
		setupCommonADCMocks(env)
		env.OnActivity("FetchLogicalSizeAndUpdateActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&activities.LogicalSizeUpdateResult{OldSize: 80, NewSize: 80, LatestBackupID: latestID}, nil)

		params, backupVault, backup, account := makeInputs(activities.GCBDRServiceType)
		env.ExecuteWorkflow(ADCWorkflow, params, backupVault, backup, account)

		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertNotCalled(t, "BackPropagateGcbdrChainSizesForDeletedBackupActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}
