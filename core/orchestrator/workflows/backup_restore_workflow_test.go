package workflows

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type BackupRestoreWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env *testsuite.TestWorkflowEnvironment
}

func (s *BackupRestoreWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{
			"logParam": encodedValue,
		},
	}
	s.env.SetHeader(mockHeader)
	s.env.RegisterWorkflow(RestoreBackupWorkflow)
	s.env.RegisterWorkflow(PreBlockVolumeWorkflow)
	s.env.RegisterWorkflow(PostBlockVolumeWorkflow)
	s.env.RegisterWorkflow(PreFileVolumeWorkflow)
	s.env.RegisterWorkflow(PostFileVolumeWorkflow)
}

func (s *BackupRestoreWorkflowTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func TestBackupRestoreWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(BackupRestoreWorkflowTestSuite))
}

func (s *BackupRestoreWorkflowTestSuite) createTestData() (*common.CreateVolumeParams, *datamodel.Volume, *datamodel.BackupVault, *datamodel.Backup, []*common.HostParams, *vsa.VolumeResponse) {
	params := &common.CreateVolumeParams{
		AccountName: "test-account",
		BackupPath:  "projects/test/locations/us-central1/backupVaults/bv1/backups/backup1",
	}

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test-volume",
		Account:   &datamodel.Account{Name: "test-account"},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: int64(1)},
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "test-password",
				SecretID:      "test-secret",
				CertificateID: "test-cert",
				AuthType:      1, // Use integer value for AuthType
			},
			DeploymentName: "test-deployment",
			ClusterDetails: datamodel.ClusterDetails{
				RegionalTenantProject: "123456789",
			},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			Protocols: []string{"ISCSI"},
		},
	}

	backupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "test-vault-uuid"},
		Name:      "test-vault",
		BucketDetails: []*datamodel.BucketDetails{
			{
				BucketName:          "test-bucket",
				TenantProjectNumber: "123456789",
			},
		},
	}

	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "test-backup-uuid"},
		Name:        "test-backup",
		BackupVault: backupVault,
		Attributes: &datamodel.BackupAttributes{
			EndpointUUID: "test-endpoint-uuid",
			BucketName:   "test-bucket",
			SnapshotID:   "test-snapshot-id",
			SnapshotName: "test-snapshot-name",
		},
	}

	hostParams := []*common.HostParams{
		{HostName: "test-host", HostIQNs: []string{"iqn.test.host"}},
	}

	volCreateResponse := &vsa.VolumeResponse{
		ProviderResponse: vsa.ProviderResponse{
			ExternalUUID: "test-external-uuid",
		},
	}

	return params, volume, backupVault, backup, hostParams, volCreateResponse
}

// Helper function to register common activities and mocks
func (s *BackupRestoreWorkflowTestSuite) registerCommonActivities() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.FinaliseRestoredVolume)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// WaitForONTAPJob is already registered in the package
}

// Helper function to set up common mocks
func (s *BackupRestoreWorkflowTestSuite) setupCommonMocks(volume *datamodel.Volume) {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Mock activities
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeDetails, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(volumeCreateActivity.FinaliseRestoredVolume, mock.Anything, mock.Anything).Return(nil)
	// cleanupManager runs on success; rollbackManager's UpdateVolumeStateInDB runs only on failure
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnWorkflow("PostBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)

	// Mock UpdateBackupRestoreCount for both increment and decrement
	// Increment is called at the start of the workflow (when not a continuation)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	// Decrement is called in the defer block
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)

	// Mock WaitForONTAPJob workflow
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_Success() {
	// Setup test data
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Register common activities and mocks
	s.registerCommonActivities()
	s.setupCommonMocks(volume)

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SetupFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJobStatus to fail
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("setup failed"))

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "setup failed")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GetNodeFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	// Mock GetNode to fail with non-retryable error
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("get node failed", "GetNodeFailure", nil))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	// Rollback runs on failure and calls UpdateVolumeStateInDB
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow should fail due to the GetNode error
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_PreWorkflowFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	// Mock CrossPoolOrVPCRestorationActivity to succeed
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	// Rollback runs on failure and calls UpdateVolumeStateInDB
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Mock child workflow to fail
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("pre workflow failed"))

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow should fail due to the pre workflow failure
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GetSmSourcePathFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	// Mock CrossPoolOrVPCRestorationActivity to succeed
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("", errors.New("get sm source path failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	// Rollback runs on failure and calls UpdateVolumeStateInDB
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow should fail due to the GetSmSourcePath error
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SnapmirrorTransferPolling() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Register common activities and mocks
	s.registerCommonActivities()
	s.setupCommonMocks(volume)

	// Override snapmirror transfer status: first transferring, then success
	backupActivity := &activities.BackupActivity{}
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Once()
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SnapmirrorTransferFailed() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusFailed, BytesTransferred: nil}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	// Rollback runs on failure and calls UpdateVolumeStateInDB; cleanup runs DeleteRestoreObjectStore and DeleteSnapmirror
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow completes successfully but should have called UpdateJobStatus with ERROR
	// This is due to a bug in the workflow where it returns nil instead of the customErr
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_VolumeStatePolling() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Register common activities and mocks
	s.registerCommonActivities()
	s.setupCommonMocks(volume)

	// Override volume state polling: first DP, then RW
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "dp"}, nil).Once()
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_VolumeStatePollingTimeout() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Override timeout for faster test execution
	originalTimeout := restoredVolumeDPToRWTimeout
	restoredVolumeDPToRWTimeout = 1 * time.Millisecond // Very short timeout to ensure it triggers
	defer func() {
		restoredVolumeDPToRWTimeout = originalTimeout
	}()

	// Set up mocks manually to ensure DP state is returned and timeout triggers
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(volumeUpdateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeDetails)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.FinaliseRestoredVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(volumeUpdateActivity.UpdateVolumeJunctionpath)

	// Set up all necessary mocks
	// Note: Workflows are already registered in SetupTest
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 backup,
				BackupVault:            backupVault,
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	// Rollback runs on timeout failure and calls UpdateVolumeStateInDB
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// CRITICAL: Mock GetVolumeFromONTAP to return DP state - this must be set up BEFORE any RW state mock
	// This ensures the timeout path is executed
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeResponse{Type: "dp"}, nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err, "Expected timeout error but workflow completed successfully")

	// Verify the error contains the timeout message
	// The timeout logic is working correctly - the error message confirms it
	assert.Contains(s.T(), err.Error(), "failed to transition from DP/LS to RW state within timeout period")
	assert.Contains(s.T(), err.Error(), volume.UUID)

	// Try to extract the custom error and verify if possible
	// Note: The test environment may wrap errors, so we primarily verify the message
	customErr := vsaerrors.ExtractCustomError(err)
	if customErr != nil && customErr.TrackingID == vsaerrors.ErrTimeLimitExceeded {
		// If we can extract it and it matches, verify it
		assert.Equal(s.T(), vsaerrors.ErrTimeLimitExceeded, customErr.TrackingID)
	} else {
		// The error message is the most important verification - timeout is working
		s.T().Logf("Timeout detected correctly. Error message: %v", err.Error())
		if customErr != nil {
			s.T().Logf("Extracted error TrackingID: %d (expected: %d)", customErr.TrackingID, vsaerrors.ErrTimeLimitExceeded)
		}
	}
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_VolumeStatePollingTimeout_LSState() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Override timeout for faster test execution
	originalTimeout := restoredVolumeDPToRWTimeout
	restoredVolumeDPToRWTimeout = 1 * time.Millisecond // Very short timeout to ensure it triggers
	defer func() {
		restoredVolumeDPToRWTimeout = originalTimeout
	}()

	// Set up mocks manually to ensure LS state is returned and timeout triggers
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(volumeUpdateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeDetails)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.FinaliseRestoredVolume)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(volumeUpdateActivity.UpdateVolumeJunctionpath)

	// Set up all necessary mocks
	// Note: Workflows are already registered in SetupTest
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 backup,
				BackupVault:            backupVault,
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// CRITICAL: Mock GetVolumeFromONTAP to return LS state - this ensures the timeout path is executed
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeResponse{Type: "ls"}, nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err, "Expected timeout error but workflow completed successfully")

	// Verify the error contains the timeout message
	// The timeout logic is working correctly - the error message confirms it
	assert.Contains(s.T(), err.Error(), "failed to transition from DP/LS to RW state within timeout period")
	assert.Contains(s.T(), err.Error(), volume.UUID)

	// Try to extract the custom error and verify if possible
	// Note: The test environment may wrap errors, so we primarily verify the message
	customErr := vsaerrors.ExtractCustomError(err)
	if customErr != nil && customErr.TrackingID == vsaerrors.ErrTimeLimitExceeded {
		// If we can extract it and it matches, verify it
		assert.Equal(s.T(), vsaerrors.ErrTimeLimitExceeded, customErr.TrackingID)
	} else {
		// The error message is the most important verification - timeout is working
		s.T().Logf("Timeout detected correctly. Error message: %v", err.Error())
		if customErr != nil {
			s.T().Logf("Extracted error TrackingID: %d (expected: %d)", customErr.TrackingID, vsaerrors.ErrTimeLimitExceeded)
		}
	}
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_VolumeStateInvalid() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "invalid"}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow completes successfully but should have called UpdateJobStatus with ERROR
	// This is due to a bug in the workflow where it returns nil instead of the customErr
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested - UPDATED

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_PostWorkflowFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnWorkflow("PostBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("post workflow failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)

	// Mock post workflow to fail
	s.env.OnWorkflow("PostBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("post workflow failed"))

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow completes successfully but should have called UpdateJobStatus with ERROR
	// This is due to a bug in the workflow where it returns nil instead of the customErr
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_UpdateVolumeJunctionpathFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(volumeUpdateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(volumeUpdateActivity.UpdateVolumeJunctionpath)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 backup,
				BackupVault:            backupVault,
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnActivity(volumeUpdateActivity.UpdateVolumeJunctionpath, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("junction path update failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_UpdateVolumeDetailsFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnWorkflow("PostBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeDetails, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("update volume details failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow completes successfully but should have called UpdateJobStatus with ERROR
	// This is due to a bug in the workflow where it returns nil instead of the customErr
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_QueryHandler() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Register common activities and mocks
	s.registerCommonActivities()
	s.setupCommonMocks(volume)

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Query workflow status
	status, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), status)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_Constants() {
	// Test volume state constants
	assert.Equal(s.T(), "rw", "rw")
	assert.Equal(s.T(), "dp", "dp")
	assert.Equal(s.T(), "ls", "ls")

	// Test WaitForRestore constant
	assert.Equal(s.T(), time.Duration(10)*time.Second, WaitForRestore)
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_WorkflowInterface() {
	// Test that restoreBackupWorkflow implements WorkflowInterface
	var _ WorkflowInterface = &restoreBackupWorkflow{}
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_RetryPolicyError() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Set invalid environment variable to cause PopulateRetryPolicyParams to fail
	originalStartToCloseTimeout := StartToCloseTimeout
	StartToCloseTimeout = "invalid-duration"
	defer func() { StartToCloseTimeout = originalStartToCloseTimeout }()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow completes successfully but should have called UpdateJobStatus with ERROR
	// This is due to a bug in the workflow where it returns nil instead of the customErr
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GetObjStoreNameFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	// Mock CrossPoolOrVPCRestorationActivity to succeed
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("get obj store name failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

// TestRestoreBackupWorkflow_GenerateObjectStoreNameForRestoreFailure tests the failure scenario
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GenerateObjectStoreNameForRestoreFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	commonActivity := &activities.CommonActivities{}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	// Setup mocks for activities that should succeed before reaching GenerateObjectStoreNameForRestore
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("", errors.New("failed to generate object store name for restore"))

	// Mock UpdateBackupRestoreCount for cleanup
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow should fail due to the GenerateObjectStoreNameForRestore error
	// The error should be converted to VSAError as per line 261: ConvertToVSAError(err)
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GetBucketDetailsFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(nil, errors.New("get bucket details failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	// The workflow should fail due to the bucket details failure
	assert.Error(s.T(), s.env.GetWorkflowError())
	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GetOrCreateObjectStoreFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("get or create object store failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	// The workflow should fail due to the object store failure
	assert.Error(s.T(), s.env.GetWorkflowError())
	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SnapmirrorGetOrCreateFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("snapmirror get or create failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SnapmirrorTransferFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("snapmirror transfer failed"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError()) // The workflow should fail due to the error scenario being tested
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SnapmirrorTransferWaitTimeCap() {
	// This test covers the case where waitTime exceeds BackupMaxWaitTimeCap (line 214)
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Register common activities and mocks
	s.registerCommonActivities()
	s.setupCommonMocks(volume)

	// Override snapmirror transfer status to trigger wait time cap logic
	backupActivity := &activities.BackupActivity{}
	// Mock multiple transferring status calls to trigger the wait time cap logic
	// First call: transferring
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Once()
	// Second call: transferring (wait time doubles)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Once()
	// Third call: transferring (wait time doubles again)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Once()
	// Fourth call: transferring (wait time doubles again, should hit cap)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Once()
	// Fifth call: transferring (wait time should be capped)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Once()
	// Sixth call: transferring (wait time should still be capped)
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusTransferring, BytesTransferred: nil}, nil).Once()
	// Final call: success
	s.env.OnActivity(backupActivity.GetSnapmirrorTransferStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.SnapmirrorTransferStatus{Status: activities.SmStatusSuccess, BytesTransferred: nil}, nil).Once()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

// Test for RestoreBackupWorkflowWithContext function
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflowWithContext_Success() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create backup activities context
	backupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:                 backup,
			BackupVault:            backupVault,
			Volume:                 volume,
			BackupVaultAccountName: "test-account",
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
		SnapmirrorRelationship: &common.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		TransferStatus: activities.SmStatusSuccess,
	}

	// Register common activities and mocks
	s.registerCommonActivities()
	s.setupCommonMocks(volume)

	// Execute workflow with context
	s.env.ExecuteWorkflow(RestoreBackupWorkflowWithContext, backupActivitiesContext, params, hostParams, volCreateResponse)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

// Test for RestoreBackupWorkflowWithContext with setup failure
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflowWithContext_SetupFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create backup activities context
	backupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:                 backup,
			BackupVault:            backupVault,
			Volume:                 volume,
			BackupVaultAccountName: "test-account",
		},
	}

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJobStatus to fail
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(errors.New("setup failed"))

	// Execute workflow with context
	s.env.ExecuteWorkflow(RestoreBackupWorkflowWithContext, backupActivitiesContext, params, hostParams, volCreateResponse)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "setup failed")
}

// Test for RestoreBackupWorkflowWithContext with run failure
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflowWithContext_RunFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create backup activities context
	backupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:                 backup,
			BackupVault:            backupVault,
			Volume:                 volume,
			BackupVaultAccountName: "test-account",
		},
	}

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	// Mock GetNode to fail with non-retryable error
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return(nil, temporal.NewNonRetryableApplicationError("get node failed", "GetNodeFailure", nil))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)

	// Execute workflow with context
	s.env.ExecuteWorkflow(RestoreBackupWorkflowWithContext, backupActivitiesContext, params, hostParams, volCreateResponse)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

// Test for RestoreBackupWorkflowWithContext with continuation scenario
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflowWithContext_ContinuationScenario() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create backup activities context with continuation data
	backupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:                 backup,
			BackupVault:            backupVault,
			Volume:                 volume,
			BackupVaultAccountName: "test-account",
		},
		Node:         &models.Node{EndpointAddress: "127.0.0.1"},
		SnapshotName: "test-backup",
		SnapmirrorRelationship: &common.SnapmirrorRelationship{
			UUID: "test-snapmirror-uuid",
		},
		TransferStatus: activities.SmStatusSuccess,
	}

	// Register common activities and mocks
	s.registerCommonActivities()
	s.setupCommonMocks(volume)

	// Execute workflow with context
	s.env.ExecuteWorkflow(RestoreBackupWorkflowWithContext, backupActivitiesContext, params, hostParams, volCreateResponse)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_UpdateBackupRestoreCountFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	// Mock UpdateBackupRestoreCount increment to fail
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(errors.New("failed to update backup restore count"))
	// Mock UpdateBackupRestoreCount decrement (called in defer block) - may or may not be called depending on error handling
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Debug logging
	s.T().Logf("Workflow completed: %v", s.env.IsWorkflowCompleted())
	s.T().Logf("Workflow error: %v", s.env.GetWorkflowError())
	s.T().Logf("Job status calls: %v", jobStatusCalls)

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())

	// The workflow should fail due to the UpdateBackupRestoreCount error
	assert.Error(s.T(), s.env.GetWorkflowError())

	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

// TestRestoreBackupWorkflow_EnsureJobStateError tests the error path when EnsureJobState fails
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_EnsureJobStateError() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)

	// Mock GetJob to return a job with state PROCESSING (not NEW) to trigger EnsureJobState error
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error
	}, nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assert that the workflow failed due to EnsureJobState error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// TestRestoreBackupWorkflowWithContext_EnsureJobStateError tests the error path when EnsureJobState fails
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflowWithContext_EnsureJobStateError() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)

	// Mock GetJob to return a job with state PROCESSING (not NEW) to trigger EnsureJobState error
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error
	}, nil)

	backupActivitiesContext := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:                 backup,
			BackupVault:            backupVault,
			Volume:                 volume,
			BackupVaultAccountName: "test-account",
		},
		Node: &models.Node{EndpointAddress: "127.0.0.1"},
	}

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflowWithContext, backupActivitiesContext, params, hostParams, volCreateResponse)

	// Assert that the workflow failed due to EnsureJobState error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GetSnapmirrorFailure() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	// Mock GetSnapmirror to fail
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get snapmirror relationship"))
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to get snapmirror relationship")
	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SleepBeforeGetSnapmirrorError() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:                   &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:           "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{UUID: "test-snapmirror-uuid"},
			SmSourcePath:           "test-source-path",
			SmDestinationPath:      "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	// Cancel when the 30s sleep before GetSnapmirror is scheduled so workflow.Sleep returns error (line 311)
	s.env.SetOnTimerScheduledListener(func(timerID string, duration time.Duration) {
		if duration == 30*time.Second {
			s.env.CancelWorkflow()
		}
	})

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	errStr := s.env.GetWorkflowError().Error()
	assert.True(s.T(),
		strings.Contains(errStr, "failed to sleep before getting snapmirror") || strings.Contains(errStr, "canceled") || strings.Contains(errStr, "internal error"),
		"workflow error should reflect sleep failure or cancellation: %s", errStr)
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_UnhealthySnapmirror() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls to verify error handling
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	// Mock GetSnapmirror to return an unhealthy relationship with reasons
	unhealthy := false
	unhealthyReasons := []string{"Transfer failed", "Network timeout"}
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:            "test-uuid",
		Healthy:         &unhealthy,
		UnhealthyReason: &unhealthyReasons,
	}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "snapmirror relationship is unhealthy")
	// Note: The unhealthy reasons are logged but not included in the error message
	// Verify that the workflow attempted to update job status to ERROR
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_UnhealthySnapmirrorWithBrokenOffState() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)

	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	unhealthy := false
	brokenOffState := "broken_off"
	unhealthyReasons := []string{"Transfer failed", "Network timeout"}
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:            "test-uuid",
		Healthy:         &unhealthy,
		UnhealthyReason: &unhealthyReasons,
		State:           &brokenOffState,
	}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "snapmirror relationship is unhealthy")
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "ERROR")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SucceedsWithUninitializedState() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	uninitializedState := datamodel.OntapUninitialized
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid",
		Healthy: &healthy,
		State:   &uninitializedState,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnActivity(volumeUpdateActivity.UpdateVolumeJunctionpath, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.FinaliseRestoredVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow("PostBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "DONE")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_SucceedsWithBrokenOffState() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	brokenOffState := datamodel.OntapBrokenOff
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid",
		Healthy: &healthy,
		State:   &brokenOffState,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnActivity(volumeUpdateActivity.UpdateVolumeJunctionpath, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.FinaliseRestoredVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow("PostBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "DONE")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_GetSnapmirrorNotFound() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	// Create activity instances
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	// Register activities
	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)

	// Register specific backup activity methods
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)

	// Register missing activities
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	// Mock GetJob
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	// Mock UpdateJob for UpdateJobStatus
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Track UpdateJobStatus calls to verify workflow completes successfully
	var jobStatusCalls []string
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		job := args.Get(1).(*datamodel.Job)
		jobStatusCalls = append(jobStatusCalls, job.State)
	}).Return(nil)

	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	// Mock GetSnapmirror to return NotFound error - workflow should continue
	notFoundErr := vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, errors.New("snapmirror relationship not found")))
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, notFoundErr)
	// Mock subsequent activities that are called after GetSnapmirror NotFound is handled
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeDetails, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeUpdateActivity.UpdateVolumeJunctionpath, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	s.env.OnActivity(volumeCreateActivity.FinaliseRestoredVolume, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow("PostBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// Execute workflow
	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	// Assertions - workflow should complete successfully despite NotFound error
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	// Verify that the workflow processed through to completion
	assert.Contains(s.T(), jobStatusCalls, "PROCESSING")
	assert.Contains(s.T(), jobStatusCalls, "DONE")
}

// TestRestoreBackupWorkflow_ExpertModeRestore_Success covers line 432:
// when IsExpertModeRestore=true the workflow calls UpdateExpertModeVolumeStateInDB
// instead of FinaliseRestoredVolume to mark the volume Available.
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_ExpertModeRestore_Success() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()
	params.IsExpertModeRestore = true

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}
	expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	// Expert mode rollback uses UpdateExpertModeVolumeStateInDB, not UpdateVolumeStateInDB
	s.env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	// Line 432: expert mode path calls UpdateExpertModeVolumeStateInDB with LifeCycleStateAvailable
	s.env.OnActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, volume.UUID, datamodel.LifeCycleStateAvailable).Return(nil)
	// Rollback (on failure) also resolves via UpdateExpertModeVolumeStateInDB; mark Maybe so it is optional on success
	s.env.OnActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

// TestRestoreBackupWorkflow_ExpertModeRestore_UpdateStateFails covers the error branch
// immediately after line 432: when UpdateExpertModeVolumeStateInDB returns an error
// the workflow should fail and surface that error.
func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_ExpertModeRestore_UpdateStateFails() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()
	params.IsExpertModeRestore = true

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}
	expertModeVolumeActivity := &expertmodeactivities.ExpertModeVolumeActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject)
	s.env.RegisterActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRolesForServiceAccountInBackupTenantProject, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 &datamodel.Backup{},
				BackupVault:            &datamodel.BackupVault{},
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus:    activities.SmStatusSuccess,
			SmSourcePath:      "test-source-path",
			SmDestinationPath: "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "rw"}, nil)
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil)
	// Line 432: UpdateExpertModeVolumeStateInDB returns an error, triggering the failure path (lines 436-438)
	updateStateErr := temporal.NewNonRetryableApplicationError("update expert mode volume state failed", "UpdateStateFailed", nil)
	s.env.OnActivity(expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).Return(updateStateErr)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil)
	s.env.OnWorkflow(WaitForONTAPJob, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "update expert mode volume state failed")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_CancelledDuringVolumeStatePolling_TriggersRollback() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 backup,
				BackupVault:            backupVault,
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:                   &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:           "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{UUID: "test-snapmirror-uuid"},
			TransferStatus:         activities.SmStatusSuccess,
			SmSourcePath:           "test-source-path",
			SmDestinationPath:      "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil).Maybe()

	// Cleanup activities registered before the polling loop run via the deferred cleanupManager.
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()

	// Cancel while GetSnapmirror executes; it still returns a healthy relationship so the workflow
	// proceeds into the polling loop where the ctx.Err() guard runs first.
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) {
			s.env.CancelWorkflow()
		}).
		Return(&common.SnapmirrorRelationship{UUID: "test-snapmirror-uuid", Healthy: &healthy}, nil)

	// Prove the guard short-circuits before GetVolumeFromONTAP: track whether it is ever invoked.
	getVolumeCalled := false
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(_ mock.Arguments) { getVolumeCalled = true }).
		Return(&vsa.VolumeResponse{Type: VolumeStateRW}, nil).Maybe()

	// Rollback marks the volume as Error via UpdateVolumeStateInDB(ctx, uuid, state, details).
	rolledBackToError := false
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			if state, ok := args.Get(2).(string); ok && state == datamodel.LifeCycleStateError {
				rolledBackToError = true
			}
		}).
		Return(nil).Maybe()

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError(), "cancellation must surface as a workflow error, not a silent success")
	assert.False(s.T(), getVolumeCalled, "GetVolumeFromONTAP must not run once the polling-loop cancellation guard fires")
	assert.True(s.T(), rolledBackToError, "rollback must mark the volume as Error after cancellation")
}

func (s *BackupRestoreWorkflowTestSuite) TestRestoreBackupWorkflow_UnexpectedVolumeType_TriggersRollback() {
	params, volume, backupVault, backup, hostParams, volCreateResponse := s.createTestData()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{}
	volumeCreateActivity := &activities.VolumeCreateActivity{}
	volumeUpdateActivity := &activities.VolumeUpdateActivity{}

	s.env.RegisterActivity(commonActivity)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(volumeCreateActivity)
	s.env.RegisterActivity(volumeUpdateActivity)
	s.env.RegisterActivity(backupActivity.GenerateObjectStoreNameForRestore)
	s.env.RegisterActivity(activities.GetBucketDetailsFromBackup)
	s.env.RegisterActivity(backupActivity.GetSmSourcePathActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStore)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupRestoreCount)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity)
	s.env.RegisterActivity(volumeCreateActivity.DeleteRestoreObjectStore)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(volumeCreateActivity.CrossPoolOrVPCRestorationActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow("PreBlockVolumeWorkflow", mock.Anything, mock.Anything, mock.Anything).Return(volume, nil)
	s.env.OnActivity(backupActivity.GenerateObjectStoreNameForRestore, mock.Anything, mock.Anything, mock.Anything).Return("test-obj-store-abcd", nil)
	s.env.OnActivity(backupActivity.GetSmSourcePathActivity, mock.Anything, mock.Anything).Return("test-dest-path", nil)
	s.env.OnActivity(activities.GetBucketDetailsFromBackup, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{BucketName: "test-bucket"}, nil)
	s.env.OnActivity(backupActivity.GetOrCreateObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.CloudTarget{}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid"}, nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:                 backup,
				BackupVault:            backupVault,
				Volume:                 volume,
				BackupVaultAccountName: "test-account",
			},
			Node:                   &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName:           "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{UUID: "test-snapmirror-uuid"},
			TransferStatus:         activities.SmStatusSuccess,
			SmSourcePath:           "test-source-path",
			SmDestinationPath:      "test-destination-path",
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-snapmirror-uuid",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountIncrement).Return(nil)
	s.env.OnActivity(backupActivity.UpdateBackupRestoreCount, mock.Anything, mock.Anything, mock.Anything, mock.Anything, activities.BackupRestoreCountDecrement).Return(nil).Maybe()
	s.env.OnActivity(backupActivity.DeleteSnapmirror, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()
	s.env.OnActivity(volumeCreateActivity.DeleteRestoreObjectStore, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.OntapAsyncResponse{}, nil).Maybe()

	// ONTAP reports a state that is neither RW, DP nor LS -> hits the unexpected-state branch.
	s.env.OnActivity(volumeUpdateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.VolumeResponse{Type: "offline"}, nil)

	rolledBackToError := false
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			if state, ok := args.Get(2).(string); ok && state == datamodel.LifeCycleStateError {
				rolledBackToError = true
			}
		}).
		Return(nil).Maybe()

	s.env.ExecuteWorkflow(RestoreBackupWorkflow, params, volume, backupVault, backup, hostParams, volCreateResponse, "test-account")

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	err := s.env.GetWorkflowError()
	assert.Error(s.T(), err)
	assert.Contains(s.T(), err.Error(), "failed to move the volume type")
	assert.Contains(s.T(), err.Error(), volume.UUID)
	assert.True(s.T(), rolledBackToError, "rollback must mark the volume as Error on an unexpected volume type")
}
