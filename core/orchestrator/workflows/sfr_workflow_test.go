package workflows

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscaler "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	appenv "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestRestoreFilesFromBackupWorkflow(t *testing.T) {
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
		// Set default activity options to avoid "StartToCloseTimeout is not set" errors
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/backup.txt", "/restore.txt"},
			RestoreFilePath: "/restore_dir",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
			PoolID: 1,
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}

		// Mock activity responses
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2) // Called for PROCESSING and DONE in success case
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
			"/restore.txt": {
				Inode: "67890",
				Size:  2048,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(job, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	// After restore-count increment, the next activity fails; defer must decrement restore count and set volume READY.
	t.Run("postIncrementFailureTriggersDecrementAndVolumeReady", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				Protocols:    []string{"nfs"},
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil).Times(1)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil).Times(1)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).
			Return(errors.New("intentional failure to exercise defer: decrement restore count"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/backup.txt"}, nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("backupMetadataNotFoundReturnsBadRequest", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
			BackupPath:     "projects/test-project/locations/us-east1/backupVaults/test-vault/backups/missing-backup",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			Account:   account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		// Simulate exactly what the activity emits for a not-found backup: a CustomError carrying
		// ErrResourceNotFound wrapped as a Temporal ApplicationError so the tracking ID survives.
		notFoundErr := vsaerrors.WrapAsTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.New("Backup 'missing-backup' not found")))

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/backup.txt"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, notFoundErr)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		// Workflow maps the not-found to a Bad Request (400) rather than a 500.
		assert.Contains(t, wfErr.Error(), "Bad Request")
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		// Mock activity responses - GenerateResourceTimestamp fails
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe() // May be called if workflow gets far enough
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("", errors.New("failed to generate timestamp"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		// Mock activity responses - CreateServiceAccount fails
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe() // May be called if workflow gets far enough
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to create service account"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetFileInodeNumbersFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		// Mock activity responses - GetFileInodeNumbers fails
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe() // May be called if workflow gets far enough
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/test-operation").Return(true, nil).Once()
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get inode numbers"))
		// Rollback manager calls CleanupADCCloudRunService but doesn't wait for operation status
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("MissingFilesInBackup", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/missing-file.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:       datamodel.BaseModel{ID: 1},
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		// Mock storage method for GetNode activity
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return([]*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{UUID: "node-uuid"},
			},
		}, nil).Maybe()

		// Mock activity responses - file not found in backup
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe() // May be called if workflow gets far enough
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/test-operation").Return(true, nil).Once()
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		// File not found in backup
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		// Rollback manager calls cleanup activities with an extra error message parameter appended
		// CleanupADCCloudRunService: (ctx, projectID, region, serviceName, errorMessage)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		// RemoveRolesFromServiceAccount: (ctx, projectID, saAccountID, roles, errorMessage)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// DeleteSA: (ctx, projectID, saAccountID, errorMessage)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution - should fail because no files found
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("LargeVolumeRestoreCheck_AllConditionsTrue", func(t *testing.T) {
		// Test that when all conditions are true (LargeVolumeAttributes != nil, LargeCapacity == true, volume.UUID == backup.VolumeUUID),
		// the source path uses the _large format with backup.VolumeUUID
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid", // Matches volume.UUID
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
				LargeCapacity: true, // All conditions are true
			},
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:       datamodel.BaseModel{ID: 1},
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		objStoreName := "obj-store-name-test"
		expectedLargeVolumeSourcePath := fmt.Sprintf("%s:/objstore/%s", objStoreName, backup.VolumeUUID)

		// Mock storage method for GetNode activity
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return([]*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{UUID: "node-uuid"},
			},
		}, nil).Maybe()

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return(objStoreName, nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/destination/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		// Verify that SnapmirrorGetOrCreate is called with the large volume source path
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.MatchedBy(func(params *common.SnapmirrorRelationshipParams) bool {
			return params.SourcePath == expectedLargeVolumeSourcePath &&
				params.DestinationPath == "/destination/path" &&
				params.IsRestore == true &&
				params.SourceUUID != nil &&
				*params.SourceUUID == backup.Attributes.EndpointUUID
		})).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil).Once()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("LargeVolumeRestoreCheck_LargeVolumeAttributesNil", func(t *testing.T) {
		// Test that when LargeVolumeAttributes is nil, it uses regular source path with SnapshotID
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel:             datamodel.BaseModel{UUID: "volume-uuid"},
			Account:               account,
			PoolID:                1,
			LargeVolumeAttributes: nil, // LargeVolumeAttributes is nil
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:       datamodel.BaseModel{ID: 1},
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		objStoreName := "obj-store-name-test"
		expectedRegularSourcePath := fmt.Sprintf("%s:/objstore/%s", objStoreName, backup.Attributes.SnapshotID)

		// Mock storage method for GetNode activity
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return([]*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{UUID: "node-uuid"},
			},
		}, nil).Maybe()

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return(objStoreName, nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/destination/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		// Verify that SnapmirrorGetOrCreate is called with the regular source path (using SnapshotID)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.MatchedBy(func(params *common.SnapmirrorRelationshipParams) bool {
			return params.SourcePath == expectedRegularSourcePath &&
				params.DestinationPath == "/destination/path" &&
				params.IsRestore == true &&
				params.SourceUUID != nil &&
				*params.SourceUUID == backup.Attributes.EndpointUUID
		})).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil).Once()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("LargeVolumeRestoreCheck_LargeCapacityFalse", func(t *testing.T) {
		// Test that when LargeCapacity is false, it uses regular source path with SnapshotID
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
				LargeCapacity: false, // LargeCapacity is false
			},
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:       datamodel.BaseModel{ID: 1},
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		objStoreName := "obj-store-name-test"
		expectedRegularSourcePath := fmt.Sprintf("%s:/objstore/%s", objStoreName, backup.Attributes.SnapshotID)

		// Mock storage method for GetNode activity
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return([]*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{UUID: "node-uuid"},
			},
		}, nil).Maybe()

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return(objStoreName, nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/destination/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		// Verify that SnapmirrorGetOrCreate is called with the regular source path (using SnapshotID)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.MatchedBy(func(params *common.SnapmirrorRelationshipParams) bool {
			return params.SourcePath == expectedRegularSourcePath &&
				params.DestinationPath == "/destination/path" &&
				params.IsRestore == true &&
				params.SourceUUID != nil &&
				*params.SourceUUID == backup.Attributes.EndpointUUID
		})).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil).Once()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("LargeVolumeRestoreCheck_VolumeUUIDMismatch", func(t *testing.T) {
		// Test that when volume.UUID != backup.VolumeUUID, it uses regular source path with SnapshotID
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "different-volume-uuid", // Different from volume.UUID
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
				LargeCapacity: true,
			},
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:       datamodel.BaseModel{ID: 1},
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		objStoreName := "obj-store-name-test"
		expectedRegularSourcePath := fmt.Sprintf("%s:/objstore/%s", objStoreName, backup.Attributes.SnapshotID)

		// Mock storage method for GetNode activity
		mockStorage.On("GetNodesByPoolID", mock.Anything, int64(1)).Return([]*datamodel.Node{
			{
				BaseModel: datamodel.BaseModel{UUID: "node-uuid"},
			},
		}, nil).Maybe()

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return(objStoreName, nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/destination/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		// Verify that SnapmirrorGetOrCreate is called with the regular source path (using SnapshotID)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.MatchedBy(func(params *common.SnapmirrorRelationshipParams) bool {
			return params.SourcePath == expectedRegularSourcePath &&
				params.DestinationPath == "/destination/path" &&
				params.IsRestore == true &&
				params.SourceUUID != nil &&
				*params.SourceUUID == backup.Attributes.EndpointUUID
		})).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil).Once()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SnapmirrorTransferFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Mock activity responses - SnapmirrorTransferWithFiles fails
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe() // May be called if workflow gets far enough
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/test-operation").Return(true, nil).Once()
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to initiate snapmirror transfer"))
		// Rollback manager calls CleanupADCCloudRunService but doesn't wait for operation status
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SnapmirrorTransferStatusFailed", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "account-uuid", ID: 1},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Mock activity responses - transfer status returns failed
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe() // May be called if workflow gets far enough
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/test-operation").Return(true, nil).Once()
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusFailed, BytesTransferred: nil}, nil)
		// Rollback manager activities may not execute in test environment
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil).Maybe()
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetJobFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		// Mock GetJob to fail - the activity will retry multiple times according to its retry policy
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(nil, errors.New("failed to get job")).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution - should fail when GetJob fails
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("EnsureJobState_JobNotFound", func(t *testing.T) {
		// Test lines 36-38: EnsureJobState fails when job is not found (GetJob returns nil, nil)
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		// Mock GetJob to return nil, nil (job not found)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution - should fail when job is not found
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("EnsureJobState_WrongState", func(t *testing.T) {
		// Test lines 36-38: EnsureJobState fails when job is in wrong state (not NEW)
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		// Mock GetJob to return a job with wrong state (PROCESSING instead of NEW)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStatePROCESSING), // Wrong state
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution - should fail when job is in wrong state
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		// Verify the error message contains state mismatch information
		errMsg := env.GetWorkflowError().Error()
		assert.Contains(t, errMsg, "state")
		env.AssertExpectations(t)
	})

	t.Run("PopulateSfrMetadataFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Set up test data
		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}

		// Mock activity responses - PopulateSfrMetadataActivity fails but workflow should continue
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2) // Called for PROCESSING and DONE
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(job, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to populate SfrMetadata"))

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Assert workflow execution - should succeed even if PopulateSfrMetadataActivity fails
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	// Additional tests for missing coverage lines
	t.Run("UpdateJobStatusProcessingFailure", func(t *testing.T) {
		// Test lines 38-39: UpdateJobStatus failure when setting to PROCESSING
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
		}
		backup := &datamodel.Backup{
			State:     datamodel.LifeCycleStateAvailable,
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
		}

		// Mock UpdateJobStatus to fail - signature is (context.Context, *datamodel.Job)
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(errors.New("failed to update job status"))

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("UpdateJobStatusErrorFailure", func(t *testing.T) {
		// Test lines 53-54: UpdateJobStatus failure when setting to ERROR
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("", errors.New("test error"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// UpdateJobStatus should fail when trying to set ERROR status
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStateERROR)
		})).Return(errors.New("failed to update job status to ERROR"))

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("UpdateJobStatusDoneFailure", func(t *testing.T) {
		// Test lines 62-63: UpdateJobStatus failure when setting to DONE
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Setup all mocks for successful workflow execution
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStatePROCESSING)
		})).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		// UpdateJobStatus should fail when trying to set DONE status
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStateDONE)
		})).Return(errors.New("failed to update job status to DONE"))

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("BackupVaultNotFound", func(t *testing.T) {
		// Test: FetchBackupVaultMetadataForRestore returns nil backupVault
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVaultID: 1,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.BackupVault)(nil), nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.BackupVault)(nil), nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ExpertModeRestore_deferRestoresVolumeState_success", func(t *testing.T) {
		// Covers sfr_workflow.go lines 225-227: defer branch when IsExpertModeRestore and UpdateExpertModeVolumeStateInDB succeeds.
		// Defer runs only after the defer is registered (after backup vault check); fail at CrossPoolOrVPCRestorationActivity so defer runs.
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

		params := &common.RestoreFilesFromBackupParams{
			AccountName:         "test-account",
			SourceFileList:      []string{"/backup.txt"},
			IsExpertModeRestore: true,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket", EndpointUUID: "endpoint-uuid", SnapshotID: "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "emv-volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("cross pool restoration failed"))
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ExpertModeRestore_deferRestoreVolumeState_updateFails", func(t *testing.T) {
		// Covers sfr_workflow.go lines 228-229: defer when IsExpertModeRestore and UpdateExpertModeVolumeStateInDB returns error
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

		params := &common.RestoreFilesFromBackupParams{
			AccountName:         "test-account",
			SourceFileList:      []string{"/backup.txt"},
			IsExpertModeRestore: true,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket", EndpointUUID: "endpoint-uuid", SnapshotID: "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "emv-volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("cross pool restoration failed"))
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("db update failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	// CV count check for expert mode flexgroup backup (after GetNode in SFR Run)
	t.Run("ExpertModeRestore_CVCheck_Flexgroup_Success", func(t *testing.T) {
		// IsExpertModeRestore + backup OntapVolumeStyle flexgroup: CV check runs and succeeds, workflow completes.
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&activities.OntapModeRestoreActivity{SE: mockStorage})
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

		params := &common.RestoreFilesFromBackupParams{
			AccountName:         "test-account",
			SourceFileList:      []string{"/backup.txt"},
			RestoreFilePath:     "/restore_dir",
			IsExpertModeRestore: true,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		constituentCount := int32(4)
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:               "test-bucket",
				EndpointUUID:             "endpoint-uuid",
				SnapshotID:               "snapshot-uuid",
				SnapshotName:             "snapshot-name",
				OntapVolumeStyle:         "flexgroup",
				ConstituentCountOfBackup: constituentCount,
			},
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
			Name:             "test-volume",
			Account:          account,
			PoolID:           1,
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ext-uuid"},
			Svm:              &datamodel.Svm{Name: "svm1"},
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password: "password", SecretID: "secret-id", CertificateID: "cert-id", AuthType: 1,
				},
			},
		}

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/test-operation", Status: "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc.run.app", nil)
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {Inode: "12345", Size: 1024},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchConstituentCountForLargeVolume", mock.Anything, mock.Anything, mock.Anything).Return(constituentCount, nil)
		env.OnActivity("VerifyCVCountForLargeVolume", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/dest/path", nil)
		env.OnActivity("GetOrCreateObjectStoreForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "sm-uuid"}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "sm-uuid", Healthy: &healthy}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "op-cleanup", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "op-cleanup").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ExpertModeRestore_CVCheck_VerifyCVCountFails", func(t *testing.T) {
		// Expert + flexgroup: VerifyCVCountForLargeVolume returns error, workflow fails.
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&activities.OntapModeRestoreActivity{SE: mockStorage})
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

		params := &common.RestoreFilesFromBackupParams{
			AccountName:         "test-account",
			SourceFileList:      []string{"/backup.txt"},
			IsExpertModeRestore: true,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:               "test-bucket",
				EndpointUUID:             "endpoint-uuid",
				SnapshotID:               "snapshot-uuid",
				SnapshotName:             "snapshot-name",
				OntapVolumeStyle:         "flexgroup",
				ConstituentCountOfBackup: 4,
			},
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
			Account:          account,
			PoolID:           1,
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ext-uuid"},
			Svm:              &datamodel.Svm{Name: "svm1"},
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:       datamodel.BaseModel{ID: 1},
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc@test.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{AccessKey: "ak", SecretKey: "sk"}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "op", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]*activities.FileInodeAndSize{"/backup.txt": {Inode: "1", Size: 100}}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchConstituentCountForLargeVolume", mock.Anything, mock.Anything, mock.Anything).Return(int32(2), nil) // mismatch: 2 vs backup 4
		env.OnActivity("VerifyCVCountForLargeVolume", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("restore target volume constituent count (2) does not match backup constituent count (4)"))
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ExpertModeRestore_CVCheck_FetchConstituentCountFails", func(t *testing.T) {
		// Expert + flexgroup: FetchConstituentCountForLargeVolume returns error, workflow fails.
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		env.RegisterActivity(&activities.OntapModeRestoreActivity{SE: mockStorage})
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

		params := &common.RestoreFilesFromBackupParams{
			AccountName:         "test-account",
			SourceFileList:      []string{"/backup.txt"},
			IsExpertModeRestore: true,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:               "test-bucket",
				EndpointUUID:             "endpoint-uuid",
				SnapshotID:               "snapshot-uuid",
				SnapshotName:             "snapshot-name",
				OntapVolumeStyle:         "flexgroup",
				ConstituentCountOfBackup: 4,
			},
		}
		volume := &datamodel.Volume{
			BaseModel:        datamodel.BaseModel{UUID: "volume-uuid"},
			Account:          account,
			PoolID:           1,
			VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "ext-uuid"},
			Svm:              &datamodel.Svm{Name: "svm1"},
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:       datamodel.BaseModel{ID: 1},
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc@test.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{AccessKey: "ak", SecretKey: "sk"}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "op", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]*activities.FileInodeAndSize{"/backup.txt": {Inode: "1", Size: 100}}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("FetchConstituentCountForLargeVolume", mock.Anything, mock.Anything, mock.Anything).Return(int32(0), errors.New("failed to get volume from ONTAP"))
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ExpertModeRestore_NoCVCheck_NotFlexgroup", func(t *testing.T) {
		// IsExpertModeRestore true but backup is not flexgroup: CV check block skipped, no Fetch/Verify activities called.
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})
		// Do NOT register OntapModeRestoreActivity: CV check is skipped when OntapVolumeStyle != "flexgroup"
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

		params := &common.RestoreFilesFromBackupParams{
			AccountName:         "test-account",
			SourceFileList:      []string{"/backup.txt"},
			RestoreFilePath:     "/restore_dir",
			IsExpertModeRestore: true,
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "volume-uuid",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:       "test-bucket",
				EndpointUUID:     "endpoint-uuid",
				SnapshotID:       "snapshot-uuid",
				SnapshotName:     "snapshot-name",
				OntapVolumeStyle: "flexvol",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password: "password", SecretID: "secret-id", CertificateID: "cert-id", AuthType: 1,
				},
			},
		}

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/test-operation", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc.run.app", nil)
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {Inode: "12345", Size: 1024},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/dest/path", nil)
		env.OnActivity("GetOrCreateObjectStoreForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{UUID: "obj-store-uuid"}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "sm-uuid"}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "sm-uuid", Healthy: &healthy}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{OperationName: "op-cleanup", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "op-cleanup").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetBucketDetailsFailure", func(t *testing.T) {
		// Test lines 194-195: getBucketDetailsForBucket failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "different-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket", // Different from bucket in backupVault
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("EndpointUUIDOrBucketNameEmpty", func(t *testing.T) {
		// Test line 199: Endpoint UUID or bucket name is empty
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "", // Empty bucket name
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "", // Empty endpoint UUID
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("IsServiceAccountCreatedFailure", func(t *testing.T) {
		// Test lines 219-220: IsServiceAccountCreated failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(false, errors.New("failed to check service account"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// RemoveRolesFromServiceAccount may be called in cleanup, but might be skipped if service account wasn't created
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		attemptCount := 0
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) { attemptCount++ }).
			Return(false, errors.New("failed to check service account"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Equal(t, SARetryMaximumAttempts, attemptCount, "IsServiceAccountCreated should use SA retry attempts")
		env.AssertExpectations(t)
	})

	t.Run("ServiceAccountNotCreated", func(t *testing.T) {
		// Test lines 224-225: Service account is not created
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(false, nil) // Service account not created
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// DeleteSA is added to rollback manager after service account creation, but rollback may not execute in test environment
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("AttachRolesToServiceAccountFailure", func(t *testing.T) {
		// Test lines 232-233: AttachRolesToServiceAccount failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to attach roles"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// RemoveRolesFromServiceAccount may be called in cleanup, but might fail
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("CreateHmacKeysFailure", func(t *testing.T) {
		// Test lines 244-245: CreateHmacKeys failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(nil, errors.New("failed to create HMAC keys"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeployADCCloudRunServiceFailure", func(t *testing.T) {
		// Test lines 300-301: DeployADCCloudRunService failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test-project.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(nil, errors.New("failed to deploy Cloud Run service"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("CloudRunDeploymentTimeout", func(t *testing.T) {
		// Test lines 312-313, 316-319, 325: Cloud Run deployment timeout
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		// Return false 20 times to trigger timeout
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(false, nil).Times(20)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Rollback manager activities may not execute in test environment
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetADCServiceURLFailure", func(t *testing.T) {
		// Test lines 332-333: GetADCServiceURL failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("failed to get ADC service URL"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetNodeFailure", func(t *testing.T) {
		// Test lines 368-369: GetNode failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(nil, errors.New("failed to get nodes"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SnapmirrorTransferTimeout", func(t *testing.T) {
		// Test lines 508-510, 513-515: Snapmirror transfer timeout
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
		env.SetTestTimeout(time.Hour * 25) // Set timeout longer than maxWaitTime
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Return "transferring" status many times to simulate timeout - use Maybe() to allow unlimited calls
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Rollback manager activities may not execute in test environment
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil).Maybe()
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SomeFilesMissing", func(t *testing.T) {
		// Test lines 441-443, 477: Some files missing but continue with found files
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt", "/missing.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		// Only one file found in backup
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
			// /missing.txt is not in the map
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// GetJob may or may not be called depending on workflow path
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		// Should fail because some files are missing (lines 564-567)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetSnapmirrorTransferStatusFailure", func(t *testing.T) {
		// Test lines 495-496: GetSnapmirrorTransferStatus failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Mock GetSnapmirrorTransferStatus to fail on first call (activity will retry 3 times, then workflow fails)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get transfer status")).Times(3)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetSnapmirrorFailure", func(t *testing.T) {
		// Test lines 550-553: GetSnapmirror activity failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		// Mock GetSnapmirror to fail
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get snapmirror relationship"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Rollback manager activities - executed when workflow fails
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil).Maybe()
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		// UpdateBackupRestoreCount with decrement is called in defer function
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to get snapmirror relationship")
		env.AssertExpectations(t)
	})

	t.Run("GetSnapmirrorNotFound", func(t *testing.T) {
		// Test lines 548-560: GetSnapmirror returns NotFound error - workflow should continue
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Track UpdateJobStatus calls to verify workflow completes successfully
		var jobStatusCalls []string
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
			job := args.Get(1).(*datamodel.Job)
			jobStatusCalls = append(jobStatusCalls, job.State)
		}).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		// Mock GetSnapmirror to return NotFound error - workflow should continue
		notFoundErr := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.New("snapmirror relationship not found")))
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, notFoundErr)
		// Mock subsequent activities that are called after GetSnapmirror NotFound is handled
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// UpdateBackupRestoreCount with decrement is called in defer function
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		// Assertions - workflow should complete successfully despite NotFound error
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		// Verify that the workflow processed through to completion
		assert.Contains(t, jobStatusCalls, "PROCESSING")
		assert.Contains(t, jobStatusCalls, "DONE")
		env.AssertExpectations(t)
	})

	t.Run("GetSnapmirrorUnhealthy", func(t *testing.T) {
		// Test lines 555-560: Unhealthy snapmirror relationship
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		// Mock GetSnapmirror to return unhealthy relationship with reason
		unhealthy := false
		unhealthyReasons := []string{"Transfer failed", "Connection timeout"}
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:            "snapmirror-uuid",
			Healthy:         &unhealthy,
			UnhealthyReason: &unhealthyReasons,
		}, nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// Rollback manager activities - executed when workflow fails
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil).Maybe()
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		// UpdateBackupRestoreCount with decrement is called in defer function
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "snapmirror relationship is unhealthy")
		env.AssertExpectations(t)
	})

	t.Run("GenerateObjectStoreNameForRestoreFailure", func(t *testing.T) {
		// Test lines 389-393: GenerateObjectStoreNameForRestore failure scenario
		// This test verifies that when GenerateObjectStoreNameForRestore activity fails,
		// the error is properly converted to VSAError as per line 392: ConvertToVSAError(err)
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		// Setup mocks for activities that should succeed before reaching GenerateObjectStoreNameForRestore
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("failed to generate object store name for restore"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		// Execute workflow
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)

		// Assertions
		assert.True(t, env.IsWorkflowCompleted())

		// The workflow should fail due to the GenerateObjectStoreNameForRestore error
		// The error should be converted to VSAError as per line 392: ConvertToVSAError(err)
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "failed to generate object store name for restore")

		env.AssertExpectations(t)
	})

	t.Run("GetBucketDetailsFromBackupActivityFailure", func(t *testing.T) {
		// Test line 390: GetBucketDetailsFromBackupActivity failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get bucket details"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetSmSourcePathActivityFailure", func(t *testing.T) {
		// Test line 404: GetSmSourcePathActivity failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("", errors.New("failed to get SM source path"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetOrCreateObjectStoreFailure", func(t *testing.T) {
		// Test line 416: GetOrCreateObjectStore failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get or create object store"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SnapmirrorGetOrCreateFailure", func(t *testing.T) {
		// Test line 429: SnapmirrorGetOrCreate failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get or create snapmirror"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("CloudRunCleanupCheckStatusFailure", func(t *testing.T) {
		// Test lines 533-534: Cloud Run cleanup check status failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/test-operation").Return(true, nil).Once()
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://adc-svc-20231201120000abcd-abc123.run.app", nil)
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(false, errors.New("failed to check cleanup status"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// GetJob may or may not be called depending on workflow path
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("RemoveRolesFromServiceAccountFailure", func(t *testing.T) {
		// Test lines 552-553: RemoveRolesFromServiceAccount failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to remove roles"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// GetJob may or may not be called depending on workflow path
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteSAFailure", func(t *testing.T) {
		// Test lines 559-560: DeleteSA failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			PoolID:    1,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to delete service account"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// GetJob may or may not be called depending on workflow path
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("CrossPoolOrVPCRestorationActivityFailure", func(t *testing.T) {
		// Test line 191: CrossPoolOrVPCRestorationActivity failure
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to setup cross VPC restoration"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("UpdateBackupRestoreCountDecrementFailure", func(t *testing.T) {
		// Test line 161: UpdateBackupRestoreCount decrement failure in defer function
		// The workflow should complete successfully, but the decrement fails (just logs error)
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/backup.txt"},
			RestoreFilePath: "/restore_dir",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
			PoolID: 1,
		}
		job := &datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "job-uuid", ID: 100},
			State:     string(datamodel.JobsStateNEW),
		}

		// Mock activity responses - workflow succeeds but decrement fails
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Times(2)
		// First call (increment) succeeds, second call (decrement in defer) fails
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(op string) bool {
			return op == string(activities.BackupRestoreCountIncrement)
		})).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.MatchedBy(func(op string) bool {
			return op == string(activities.BackupRestoreCountDecrement)
		})).Return(errors.New("failed to decrement backup restore count"))
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{UUID: "test-job-uuid", State: "success"}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, "operations/cleanup-operation-123").Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetJob", mock.Anything, mock.Anything).Return(job, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		_, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		if err != nil {
			t.Fatalf("Failed to query workflow: %v", err)
		}
		// Workflow should complete successfully even if decrement fails (just logs error)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("DeleteRestoreObjectStoreFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/backup.txt"},
			RestoreFilePath: "/restore_dir",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
			PoolID: 1,
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to delete object store for cross VPC"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("WaitForONTAPJobFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/backup.txt"},
			RestoreFilePath: "/restore_dir",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
					AuthType:      1,
				},
			},
			PoolID: 1,
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
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
		fileInodeSizeMap := map[string]*activities.FileInodeAndSize{
			"/backup.txt": {
				Inode: "12345",
				Size:  1024,
			},
		}
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fileInodeSizeMap, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name-abcd", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName: "test-bucket",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		// DeleteRestoreObjectStore returns non-nil response, but WaitForONTAPJob fails
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get ONTAP job"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SleepBeforeDeleteObjectStoreSuccess", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateJobState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/test.txt"},
			RestoreFilePath: "/restore_dir",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
				},
			},
			PoolID: 1,
		}

		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("123456", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{
			Email: "test-sa@project.iam.gserviceaccount.com",
		}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "access-key",
			SecretKey: "secret-key",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/deploy-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://test-service.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]*activities.FileInodeAndSize{
			"/test.txt": {Inode: "12345", Size: 1024},
		}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName:          "test-bucket",
			TenantProjectNumber: "123456789",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		// After transfer completes, workflow sleeps for 60 seconds
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())

		// Workflow should complete successfully with the 60-second sleep before DeleteRestoreObjectStore
		env.AssertExpectations(t)
	})

	t.Run("SleepBeforeDeleteObjectStoreFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateJobState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/test.txt"},
			RestoreFilePath: "/restore_dir",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
				},
			},
			PoolID: 1,
		}

		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("123456", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{
			Email: "test-sa@project.iam.gserviceaccount.com",
		}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "access-key",
			SecretKey: "secret-key",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/deploy-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://test-service.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]*activities.FileInodeAndSize{
			"/test.txt": {Inode: "12345", Size: 1024},
		}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName:          "test-bucket",
			TenantProjectNumber: "123456789",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		// Register a callback to cancel the workflow during the second 60-second sleep (after transfer completes)
		timerCount := 0
		env.SetOnTimerScheduledListener(func(timerID string, duration time.Duration) {
			if duration == 60*time.Second {
				timerCount++
				// Cancel the workflow on the second 60-second timer
				if timerCount == 2 {
					env.CancelWorkflow()
				}
			}
		})

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		// Verify the error is related to cancellation/context cancellation
		assert.Contains(t, env.GetWorkflowError().Error(), "canceled")
	})
	t.Run("DuplicateFilesInSourceFileList", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateJobState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		// Test with duplicate files in SourceFileList
		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/file1.txt", "/file2.txt", "/file1.txt", "/file3.txt", "/file2.txt", "/file1.txt"},
			RestoreFilePath: "/restore_dir",
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
				PoolCredentials: &datamodel.PoolCredentials{
					Password:      "password",
					SecretID:      "secret-id",
					CertificateID: "cert-id",
				},
			},
			PoolID: 1,
		}

		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("123456", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{
			Email: "test-sa@project.iam.gserviceaccount.com",
		}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "access-key",
			SecretKey: "secret-key",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/deploy-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://test-service.run.app", nil)

		// Mock GetFileInodeNumbers and verify it receives deduplicated file list
		var actualFileList []string
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Run(func(args mock.Arguments) {
				// Capture the file list passed to GetFileInodeNumbers (4th argument)
				if fileList, ok := args.Get(3).([]string); ok {
					actualFileList = fileList
				}
			}).
			Return(map[string]*activities.FileInodeAndSize{
				"/file1.txt": {Inode: "12345", Size: 1024},
				"/file2.txt": {Inode: "12346", Size: 2048},
				"/file3.txt": {Inode: "12347", Size: 3072},
			}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName:          "test-bucket",
			TenantProjectNumber: "123456789",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)

		healthy := true
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())

		// Verify that duplicates were removed by checking the file list passed to GetFileInodeNumbers
		// Original list had 6 files ([/file1.txt, /file2.txt, /file1.txt, /file3.txt, /file2.txt, /file1.txt])
		// After deduplication, only 3 unique files should be processed ([/file1.txt, /file2.txt, /file3.txt])
		assert.Equal(t, 3, len(actualFileList), "Expected GetFileInodeNumbers to be called with 3 unique files")
		assert.Contains(t, actualFileList, "/file1.txt")
		assert.Contains(t, actualFileList, "/file2.txt")
		assert.Contains(t, actualFileList, "/file3.txt")

		env.AssertExpectations(t)
	})

	t.Run("GetSnapmirrorHealthCheck_UnhealthyWithReasons", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)

		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateJobState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/file1.txt"},
			RestoreFilePath: "/restore_dir",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
			},
			PoolID: 1,
		}

		// Mock activities up to GetSnapmirror
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("123456", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{
			Email: "test-sa@project.iam.gserviceaccount.com",
		}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "access-key",
			SecretKey: "secret-key",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/deploy-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://test-service.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]*activities.FileInodeAndSize{
			"/file1.txt": {Inode: "12345", Size: 1024},
		}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName:          "test-bucket",
			TenantProjectNumber: "123456789",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)

		// Mock GetSnapmirror to return an UNHEALTHY relationship WITH reasons
		healthy := false
		reasons := []string{"Destination volume is offline", "Transfer failed"}
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:            "snapmirror-uuid",
			Healthy:         &healthy,
			UnhealthyReason: &reasons,
		}, nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)

		// Verify workflow failed with unhealthy error
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "snapmirror relationship is unhealthy")
		assert.Contains(t, env.GetWorkflowError().Error(), "Destination volume is offline")
		assert.Contains(t, env.GetWorkflowError().Error(), "Transfer failed")
	})

	t.Run("GetSnapmirrorHealthCheck_UnhealthyWithoutReasons", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)

		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateJobState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/file1.txt"},
			RestoreFilePath: "/restore_dir",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
			},
			PoolID: 1,
		}

		// Mock activities up to GetSnapmirror
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("123456", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{
			Email: "test-sa@project.iam.gserviceaccount.com",
		}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "access-key",
			SecretKey: "secret-key",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/deploy-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://test-service.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]*activities.FileInodeAndSize{
			"/file1.txt": {Inode: "12345", Size: 1024},
		}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName:          "test-bucket",
			TenantProjectNumber: "123456789",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)

		// Mock GetSnapmirror to return an UNHEALTHY relationship WITHOUT reasons
		healthy := false
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:    "snapmirror-uuid",
			Healthy: &healthy,
		}, nil)

		// Mock remaining activities since workflow will continue without error when no reasons provided
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/file1.txt"}, nil)
		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{
			JobUUID: "job-uuid",
		}, nil)
		env.OnActivity("GetOntapJob", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapJob{
			State: "success",
		}, nil)
		env.OnActivity("CleanupADCCloudRunService", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/cleanup-operation-123",
			Status:        "DONE",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("RemoveRolesFromServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("DeleteSA", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)

		// Verify workflow completes successfully when unhealthy without reasons (no default error message)
		assert.True(t, env.IsWorkflowCompleted())
		assert.NoError(t, env.GetWorkflowError())
	})
	t.Run("GetSnapmirrorHealthCheck_UnhealthyWithIncompletePathError", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)

		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		mockStorage.On("UpdateJobState", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:     "test-account",
			SourceFileList:  []string{"/file1.txt"},
			RestoreFilePath: "/restore_dir",
		}

		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
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
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			VolumeUUID:    "test-vol",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Name:      "test-volume",
			AccountID: 1,
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:       "/projects/test-project/locations/us-east1-b/pools/test-pool",
				BaseModel:      datamodel.BaseModel{ID: 1},
				DeploymentName: "deployment-name",
			},
			PoolID: 1,
		}

		// Mock all activities up to GetSnapmirror
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("123456", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&hyperscaler.ServiceAccount{
			Email: "test-sa@project.iam.gserviceaccount.com",
		}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "access-key",
			SecretKey: "secret-key",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).Return(&hyperscaler.CloudRunOperationResponse{
			OperationName: "operations/deploy-operation-123",
			Status:        "RUNNING",
		}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return("https://test-service.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(map[string]*activities.FileInodeAndSize{
			"/file1.txt": {Inode: "12345", Size: 1024},
		}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
			BucketName:          "test-bucket",
			TenantProjectNumber: "123456789",
		}, nil)
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"}, nil).Maybe()
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/source/path", nil)
		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{
			UUID: "obj-store-uuid",
		}, nil)
		env.OnActivity("SnapmirrorGetOrCreate", mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID: "snapmirror-uuid",
		}, nil)
		env.OnActivity("SnapmirrorTransferWithFiles", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GetSnapmirrorTransferStatus", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/file1.txt"}, nil)

		// Mock GetSnapmirror to return unhealthy with "Incomplete path to file" error
		// This should trigger the pattern matching and return an Incorrect destination path error
		healthy := false
		reasons := []string{"Incomplete path to file \"/tmp/22.txt\" on destination volume"}
		env.OnActivity("GetSnapmirror", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
			UUID:            "snapmirror-uuid",
			Healthy:         &healthy,
			UnhealthyReason: &reasons,
		}, nil)

		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil).Maybe()
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil).Maybe()

		env.OnActivity("DeleteRestoreObjectStore", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{JobUUID: "test-job-uuid"}, nil).Maybe()
		env.OnActivity("DeleteSnapmirror", mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)

		// Verify workflow failed with Incorrect destination path error (400)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "Incorrect destination path")
	})

	t.Run("GetAuthJWTTokenFailure", func(t *testing.T) {
		origUseVCPRegion := cvp.CVP_HOST
		defer func() { cvp.CVP_HOST = origUseVCPRegion }()
		cvp.CVP_HOST = "http://cvp-host"

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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("", errors.New("token fetch failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("GetLocationFromVendorIDFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "invalid-vendor-id",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ParseRegionAndZoneFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/INVALID_LOCATION/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("FetchBackupVaultMetadataForRestoreFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.BackupVault)(nil), errors.New("vault metadata fetch failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("FetchBackupMetadataForRestoreFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			Account:   account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Backup)(nil), errors.New("backup metadata fetch failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("BackupMetadataNil", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			Account:   account,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Backup)(nil), nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("BackupStateNotAvailable", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			Account:   account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateDeleting,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SanProtocolValidation", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			Account:   account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				Protocols:    []string{"ISCSI"},
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("FetchBucketMetadataForRestoreFailure", func(t *testing.T) {
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
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "test-account",
			SourceFileList: []string{"/backup.txt"},
		}
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			Account:   account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "test-backup",
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
			},
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}

		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.BackupVault)(nil), errors.New("bucket metadata fetch failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("ValidateAndDeduplicateFileListFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}}
		volume := &datamodel.Volume{
			Account: &datamodel.Account{Name: "a"},
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return(([]string)(nil), errors.New("dedupe failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SFR_backupAttributesNil_ErrSFRSnapshotIDMissing", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}}
		account := &datamodel.Account{Name: "a"}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account}
		backup := &datamodel.Backup{Name: "b", State: datamodel.LifeCycleStateAvailable, Attributes: nil}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)
		env.OnActivity("PopulateSfrMetadataActivity", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		assert.Contains(t, wfErr.Error(), "Backup snapshot UUID is missing")
		env.AssertExpectations(t)
	})

	t.Run("SFR_emptySnapshotID_missingBackupNameForFallback", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}, BackupPath: "bad/path"}
		account := &datamodel.Account{Name: "a"}
		cross := "projects/p/locations/us-west1/backupVaults/src"
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account, CrossRegionBackupVaultName: &cross}
		backup := &datamodel.Backup{
			Name:       "",
			State:      datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e"},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		assert.Contains(t, wfErr.Error(), "Backup snapshot UUID is missing")
		env.AssertExpectations(t)
	})

	t.Run("SFR_emptySnapshotID_noCrossRegionVaultPath", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}}
		account := &datamodel.Account{Name: "a"}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account}
		backup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e"},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		assert.Contains(t, wfErr.Error(), "Backup snapshot UUID is missing")
		env.AssertExpectations(t)
	})

	t.Run("SFR_emptySnapshotID_fallbackVaultFetchError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{
			AccountName:    "a",
			SourceFileList: []string{"/f"},
			BackupPath:     "projects/1/locations/us-east1/backupVaults/dest-vault/backups/my-backup",
		}
		account := &datamodel.Account{Name: "a"}
		cross := "projects/p/locations/us-west1/backupVaults/source-vault"
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account, CrossRegionBackupVaultName: &cross}
		backup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e"},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything).Return((*datamodel.BackupVault)(nil), errors.New("vault fetch failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		assert.Contains(t, wfErr.Error(), "Backup snapshot UUID is missing")
		env.AssertExpectations(t)
	})

	t.Run("SFR_emptySnapshotID_fallbackVaultNilResponse", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}, BackupPath: "projects/1/locations/us-east1/backupVaults/dest-vault/backups/my-backup"}
		account := &datamodel.Account{Name: "a"}
		cross := "projects/1/locations/us-west1/backupVaults/source-vault"
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account, CrossRegionBackupVaultName: &cross}
		backup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateDeleting,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e"},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything).Return((*datamodel.BackupVault)(nil), nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		assert.Contains(t, wfErr.Error(), "Backup snapshot UUID is missing")
		env.AssertExpectations(t)
	})

	t.Run("SFR_emptySnapshotID_fallbackBackupFetchError", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}, BackupPath: "projects/1/locations/us-east1/backupVaults/dest-vault/backups/my-backup"}
		account := &datamodel.Account{Name: "a"}
		cross := "projects/1/locations/us-west1/backupVaults/source-vault"
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account, CrossRegionBackupVaultName: &cross}
		sourceVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "sv"}, Name: "source-vault", Account: account}
		backup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateDeleting,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e"},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything).Return(sourceVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return((*datamodel.Backup)(nil), errors.New("backup fetch failed"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		assert.Contains(t, wfErr.Error(), "Backup snapshot UUID is missing")
		env.AssertExpectations(t)
	})

	t.Run("SFR_emptySnapshotID_sourceBackupStillMissingSnapshotID", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}, BackupPath: "projects/1/locations/us-east1/backupVaults/dest-vault/backups/my-backup"}
		account := &datamodel.Account{Name: "a"}
		cross := "projects/1/locations/us-west1/backupVaults/source-vault"
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account, CrossRegionBackupVaultName: &cross}
		sourceVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "sv"}, Name: "source-vault", Account: account}
		destBackup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateDeleting,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e"},
		}
		sourceBackup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e"},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return(destBackup, nil)
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything).Return(sourceVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return(sourceBackup, nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		wfErr := env.GetWorkflowError()
		assert.Error(t, wfErr)
		assert.Contains(t, wfErr.Error(), "Backup snapshot UUID is missing")
		env.AssertExpectations(t)
	})

	t.Run("SFR_crossRegionSnapshotIDFallbackThenFailsStateCheck", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}, BackupPath: "projects/1/locations/us-east1/backupVaults/dest-vault/backups/my-backup"}
		account := &datamodel.Account{Name: "a"}
		cross := "projects/1/locations/us-west1/backupVaults/source-vault"
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account, CrossRegionBackupVaultName: &cross}
		sourceVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "sv"}, Name: "source-vault", Account: account}
		destBackup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateDeleting,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "", BucketName: "b", EndpointUUID: "e", Protocols: []string{"nfs"}},
		}
		sourceBackup := &datamodel.Backup{
			Name:       "my-backup",
			State:      datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "resolved-snapshot-uuid", BucketName: "b", EndpointUUID: "e"},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "dest-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return(destBackup, nil)
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything).Return(sourceVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.MatchedBy(func(p string) bool {
			return strings.Contains(p, "source-vault")
		}), mock.Anything, mock.Anything, mock.Anything).Return(sourceBackup, nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SFR_UpdateBackupRestoreCountFailure", func(t *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}}
		account := &datamodel.Account{Name: "a"}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "vault-uuid"}, Name: "vault", Account: account}
		backup := &datamodel.Backup{
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			Name:          "b",
			State:         datamodel.LifeCycleStateAvailable,
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName: "test-bucket", EndpointUUID: "e", SnapshotID: "snap", Protocols: []string{"nfs"},
			},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("increment failed"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("t", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})

	t.Run("SFR_GetAuthJWTTokenWhenNotLocalAndNotVCPOnlyRegion", func(t *testing.T) {
		origLocal := appenv.IsLocalEnv
		origVCP := cvp.CVP_HOST
		defer func() {
			appenv.IsLocalEnv = origLocal
			cvp.CVP_HOST = origVCP
		}()
		appenv.IsLocalEnv = func() bool { return false }
		cvp.CVP_HOST = "http://cvp-host"

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{Fields: map[string]*commonpb.Payload{"logParam": encodedValue}})
		env.SetTestTimeout(time.Hour)
		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		env.RegisterActivity(&activities.VolumeCreateActivity{})

		params := &common.RestoreFilesFromBackupParams{AccountName: "a", SourceFileList: []string{"/f"}}
		account := &datamodel.Account{Name: "a"}
		backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "v"}, Name: "vault", Account: account}
		backup := &datamodel.Backup{
			Name:       "b",
			State:      datamodel.LifeCycleStateAvailable,
			Attributes: &datamodel.BackupAttributes{SnapshotID: "s", BucketName: "b", EndpointUUID: "e", Protocols: []string{"nfs"}},
		}
		volume := &datamodel.Volume{
			Account: account,
			Pool:    &datamodel.Pool{VendorID: "/projects/p/locations/us-east1-b/pools/pool", PoolCredentials: &datamodel.PoolCredentials{}},
		}
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).Return([]string{"/f"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, "a").Return("jwt-from-activity", nil).Once()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(backup, nil)
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).Return(backupVault, nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("stop after jwt path"))

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)
		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
	})
}

func TestRestoreFilesFromBackupWorkflow_PopulateBucketDetailsGate(t *testing.T) {
	setupEnv := func(t *testing.T) *testsuite.TestWorkflowEnvironment {
		t.Helper()
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
		encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
		env.SetHeader(&commonpb.Header{
			Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
		})
		env.SetTestTimeout(time.Hour)

		mockStorage := database.NewMockStorage(t)
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil).Maybe()
		commonActivity := &activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity)
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.RegisterActivity(&activities.ADCActivity{})
		env.RegisterActivity(&activities.BackupActivity{})
		env.RegisterActivity(&activities.SFRActivity{})
		volumeCreateActivity := &activities.VolumeCreateActivity{}
		env.RegisterActivity(volumeCreateActivity)
		env.RegisterActivity(volumeCreateActivity.PopulateBucketDetailsForTemporaryRestoreActivity)
		expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}
		env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)
		return env
	}

	mockThroughInodeLookup := func(env *testsuite.TestWorkflowEnvironment, backupVault *datamodel.BackupVault, backup *datamodel.Backup) {
		env.OnActivity("UpdateJobStatus", mock.Anything, mock.Anything).Return(nil).Maybe()
		env.OnActivity("ValidateAndDeduplicateFileList", mock.Anything, mock.Anything).
			Return([]string{"/backup.txt"}, nil)
		env.OnActivity("GetAuthJWTToken", mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
		env.OnActivity("FetchBackupVaultMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(backupVault, nil)
		env.OnActivity("FetchBackupMetadataForRestore", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(backup, nil)
		env.OnActivity("FetchBucketMetadataForRestore", mock.Anything, mock.Anything, mock.Anything).
			Return(backupVault, nil)
		env.OnActivity("UpdateBackupRestoreCount", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity("CrossPoolOrVPCRestorationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("GenerateResourceTimestamp", mock.Anything).Return("20231201120000abcd", nil)
		env.OnActivity("CreateServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscaler.ServiceAccount{Email: "adc-sa@test.iam.gserviceaccount.com"}, nil)
		env.OnActivity("IsServiceAccountCreated", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("AttachRolesToServiceAccount", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateHmacKeys", mock.Anything, mock.Anything).Return(&common.HmacKeys{
			AccessKey: "dGVzdC1hY2Nlc3Mta2V5",
			SecretKey: "dGVzdC1zZWNyZXQta2V5",
		}, nil)
		env.OnActivity("DeployADCCloudRunService", mock.Anything, mock.Anything).
			Return(&hyperscaler.CloudRunOperationResponse{OperationName: "operations/test-operation", Status: "RUNNING"}, nil)
		env.OnActivity("CheckOperationStatus", mock.Anything, mock.Anything).Return(true, nil)
		env.OnActivity("GetADCServiceURL", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return("https://adc-svc.run.app", nil)
		env.OnActivity("GetFileInodeNumbers", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(map[string]*activities.FileInodeAndSize{"/backup.txt": {Inode: "12345", Size: 1024}}, nil)
		env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).
			Return(&models.Node{EndpointAddress: "127.0.0.1", EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"}}, nil)
		env.OnActivity("GenerateObjectStoreNameForRestore", mock.Anything, mock.Anything, mock.Anything).
			Return("obj-store-name", nil)
		env.OnActivity("GetBucketDetailsFromBackupActivity", mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{BucketName: "test-bucket", TenantProjectNumber: "123456789"}, nil)
		env.OnActivity("GetSmSourcePathActivity", mock.Anything, mock.Anything).Return("/dest/path", nil)
	}

	baseFixtures := func(expertMode bool, serviceLevel string) (*common.RestoreFilesFromBackupParams, *datamodel.Volume, *datamodel.BackupVault, *datamodel.Backup) {
		account := &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1, UUID: "account-uuid"},
			Name:      "test-account",
		}
		backupVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{UUID: "vault-uuid"},
			Name:      "test-backup-vault",
			BucketDetails: datamodel.BucketDetailsArray{
				&datamodel.BucketDetails{
					BucketName:          "test-bucket",
					TenantProjectNumber: "123456789",
				},
			},
			Account: account,
		}
		backup := &datamodel.Backup{
			State:         datamodel.LifeCycleStateAvailable,
			BaseModel:     datamodel.BaseModel{UUID: "backup-uuid"},
			BackupVault:   backupVault,
			BackupVaultID: 1,
			Attributes: &datamodel.BackupAttributes{
				BucketName:   "test-bucket",
				EndpointUUID: "endpoint-uuid",
				SnapshotID:   "snapshot-uuid",
				SnapshotName: "snapshot-name",
				Protocols:    []string{"nfs"},
			},
		}
		params := &common.RestoreFilesFromBackupParams{
			AccountName:         "test-account",
			SourceFileList:      []string{"/backup.txt"},
			IsExpertModeRestore: expertMode,
		}
		volume := &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid"},
			Account:   account,
			Pool: &datamodel.Pool{
				VendorID:        "/projects/test-project/locations/us-east1-b/pools/test-pool",
				ServiceLevel:    serviceLevel,
				PoolCredentials: &datamodel.PoolCredentials{},
			},
		}
		return params, volume, backupVault, backup
	}

	t.Run("NormalFlexSkipsPopulateBucketDetailsForTemporaryRestore", func(t *testing.T) {
		env := setupEnv(t)
		params, volume, backupVault, backup := baseFixtures(false, "FLEX")
		mockThroughInodeLookup(env, backupVault, backup)

		env.OnActivity("GetOrCreateObjectStore", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("intentional stop after populate-bucket gate"))
		env.OnActivity("UpdateVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertNotCalled(t, "PopulateBucketDetailsForTemporaryRestoreActivity",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		env.AssertNotCalled(t, "GetOrCreateObjectStoreForRestore",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("ExpertModeDedicatedCapacityCallsPopulateBucketDetailsForTemporaryRestore", func(t *testing.T) {
		env := setupEnv(t)
		params, volume, backupVault, backup := baseFixtures(true, "STANDARD")
		mockThroughInodeLookup(env, backupVault, backup)

		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{
				BucketName:          "test-bucket",
				TenantProjectNumber: "123456789",
				HmacSecretID:        "hardware-bv-hmac-sa",
			}, nil).Times(1)
		env.OnActivity("GetOrCreateObjectStoreForRestore",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, errors.New("intentional stop after populate-bucket gate"))
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		env.AssertExpectations(t)
		env.AssertNotCalled(t, "GetOrCreateObjectStore",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("ExpertModeDedicatedCapacityPopulateBucketDetailsFailure", func(t *testing.T) {
		env := setupEnv(t)
		params, volume, backupVault, backup := baseFixtures(true, "STANDARD")
		mockThroughInodeLookup(env, backupVault, backup)

		populateErr := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, errors.New("failed to fetch source backup vault bucket details")))
		env.OnActivity("PopulateBucketDetailsForTemporaryRestoreActivity",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, populateErr).Once()
		env.OnActivity("UpdateExpertModeVolumeStateInDB", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

		env.ExecuteWorkflow(RestoreFilesFromBackupWorkflow, params, volume)

		assert.True(t, env.IsWorkflowCompleted())
		assert.Error(t, env.GetWorkflowError())
		assert.ErrorContains(t, env.GetWorkflowError(), "failed to fetch source backup vault bucket details")
		env.AssertNotCalled(t, "GetOrCreateObjectStoreForRestore",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		env.AssertNotCalled(t, "GetOrCreateObjectStore",
			mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestMatchErrorPattern(t *testing.T) {
	t.Run("MatchingPattern_ReturnsCorrectError", func(t *testing.T) {
		// Arrange
		patternMap := map[string]ontapErrorMapping{
			"Incomplete path to file": {
				ErrorCode:   vsaerrors.ErrSFRIncorrectDestinationPath,
				UserMessage: "Incorrect destination path",
			},
		}
		errMsg := "snapmirror relationship is unhealthy. Reasons: [Incomplete path to file \"/tmp/22.txt\" on destination volume]"

		// Act
		result := matchErrorPattern(errMsg, patternMap)

		// Assert
		assert.NotNil(t, result)
		assert.Equal(t, vsaerrors.ErrSFRIncorrectDestinationPath, result.TrackingID)
		assert.Equal(t, "Incorrect destination path", result.Message)
		assert.NotNil(t, result.OriginalErr)
		assert.Equal(t, "Incorrect destination path", result.OriginalErr.Error())

		// Verify HTTP code 400
		hasHttpCode, httpCode := result.GetHttpCode()
		assert.True(t, hasHttpCode)
		assert.Equal(t, 400, httpCode)
	})

	t.Run("NonMatchingPattern_ReturnsNil", func(t *testing.T) {
		// Arrange
		patternMap := map[string]ontapErrorMapping{
			"Incomplete path to file": {
				ErrorCode:   vsaerrors.ErrSFRIncorrectDestinationPath,
				UserMessage: "Incorrect destination path",
			},
		}
		errMsg := "snapmirror relationship is unhealthy. Reasons: [Some other error]"

		// Act
		result := matchErrorPattern(errMsg, patternMap)

		// Assert
		assert.Nil(t, result)
	})

	t.Run("EmptyOrNilPatternMap_ReturnsNil", func(t *testing.T) {
		// Test with nil map
		var nilMap map[string]ontapErrorMapping
		result := matchErrorPattern("some error", nilMap)
		assert.Nil(t, result)

		// Test with empty map
		emptyMap := map[string]ontapErrorMapping{}
		result = matchErrorPattern("some error", emptyMap)
		assert.Nil(t, result)
	})
}
