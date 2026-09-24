package backgroundworkflows

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type ScheduledBackupsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *ScheduledBackupsTestSuite) SetupTest() {
	// Set environment to local to avoid Google Cloud credentials issues in tests
	err := os.Setenv("ENV", "local")
	if err != nil {
		s.T().Fatalf("Failed to set ENV variable: %v", err)
	}

	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	s.env.RegisterWorkflow(CreateScheduledBackupInitWorkflow)
	s.env.RegisterWorkflow(CreateScheduledBackupWorkflow)
	s.env.RegisterWorkflow(CreateScheduledBackupWorkflowWithContext)
	s.env.RegisterWorkflow(DeleteScheduledBackupWorkflow)
}

func (s *ScheduledBackupsTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func TestScheduledBackupsTestSuite(t *testing.T) {
	suite.Run(t, new(ScheduledBackupsTestSuite))
}

// Helper function to register all activities needed for CreateScheduledBackupWorkflow tests
func (s *ScheduledBackupsTestSuite) registerCreateScheduledBackupActivities(commonActivity *activities.CommonActivities, backupActivity *activities.BackupActivity, scheduledBackupActivity *backgroundactivities.ScheduledBackupActivity) {
	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
	s.env.RegisterActivity(backupActivity.PrepareObjectStoreActivity)
	s.env.RegisterActivity(backupActivity.GetOrCreateObjectStoreActivity)
	s.env.RegisterActivity(backupActivity.SnapmirrorGetOrCreate)
	s.env.RegisterActivity(scheduledBackupActivity.CreateBackupSnapshotInDB)
	s.env.RegisterActivity(backupActivity.SnapshotCreate)
	s.env.RegisterActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB)
	s.env.RegisterActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume)
	s.env.RegisterActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB)
	s.env.RegisterActivity(backupActivity.CreateSnapshotActivity)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(backupActivity.SnapmirrorTransfer)
	s.env.RegisterActivity(backupActivity.GetSnapmirrorTransferStatus)
	s.env.RegisterActivity(backupActivity.FinishBackup)
	s.env.RegisterActivity(scheduledBackupActivity.CreateScheduledBackup)
	s.env.RegisterActivity(scheduledBackupActivity.GenerateScheduledSnapshotName)
	s.env.RegisterActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE)
	s.env.RegisterActivity(backupActivity.HydrateSnapshotToCCFEActivity)
	s.env.RegisterActivity(backupActivity.DeleteBackup)
	s.env.RegisterActivity(backupActivity.GetObjectStoreEndpointInfo)
	s.env.RegisterActivity(backupActivity.GetSnapshotFromObjectStore)
	s.env.RegisterActivity(scheduledBackupActivity.UpdateBackupSize)
	s.env.RegisterActivity(backupActivity.DeleteBackupSnapshot)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirror)
	s.env.RegisterActivity(backupActivity.DeleteSnapmirrorIfUnused)
	s.env.RegisterActivity(backupActivity.UpdateBackupError)
	s.env.RegisterActivity(backupActivity.DeleteSnapshotFromObjectStore)
	s.env.RegisterActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity)
	s.env.RegisterActivity(backupActivity.GetSnapmirror)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(scheduledBackupActivity.CreateRemoteScheduledBackupsFromVCPActivity)
	s.env.RegisterActivity(backupActivity.GetVolumeProtocolsFromOntapActivity)
	s.env.RegisterActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity)
	s.env.RegisterActivity(backupActivity.UpdateConstituentCountForBackup)
}

func defaultScheduledBackupNode() *models.Node {
	return &models.Node{
		Name: "test-node",
	}
}

func defaultScheduledBackupBucketDetails() *datamodel.BucketDetails {
	return &datamodel.BucketDetails{
		BucketName:         "vsa-backup-bucket",
		VendorSubnetID:     "test-vendor-subnet-id",
		ServiceAccountName: "test-service-account",
	}
}

func (s *ScheduledBackupsTestSuite) mockScheduledBackupNodeAndObjectStore(backupActivity *activities.BackupActivity) {
	s.env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).
		Return(defaultScheduledBackupNode(), nil)
	s.env.OnActivity(backupActivity.PrepareObjectStoreActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, backupCtx *activities.BackupActivitiesContext) (*activities.BackupActivitiesContext, error) {
			backupCtx.ObjStoreName = "vsa-backup-bucket"
			backupCtx.BucketName = "vsa-backup-bucket"
			backupCtx.BucketDetails = defaultScheduledBackupBucketDetails()
			return backupCtx, nil
		})
	s.env.OnActivity(backupActivity.GetOrCreateObjectStoreActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, backupCtx *activities.BackupActivitiesContext) (*activities.BackupActivitiesContext, error) {
			backupCtx.ObjStore = &common.CloudTarget{Name: "vsa-backup-bucket"}
			return backupCtx, nil
		})
	s.env.OnActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

func (s *ScheduledBackupsTestSuite) mockScheduledBackupNodeResolution(_ *activities.BackupActivity) {
	s.env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).
		Return(defaultScheduledBackupNode(), nil)
}

// Helper function to register all activities needed for DeleteScheduledBackupWorkflow tests
func (s *ScheduledBackupsTestSuite) registerDeleteScheduledBackupActivities(commonActivity *activities.CommonActivities, backupActivity *activities.BackupActivity, scheduledBackupActivity *backgroundactivities.ScheduledBackupActivity) {
	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
	s.env.RegisterActivity(backupActivity.CleanupOldBackupSnapshotsActivity)
	s.env.RegisterActivity(backupActivity.CleanupOldExpertModeSnapshotActivity)
	s.env.RegisterActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion)
	s.env.RegisterActivity(backupActivity.GetObjStoreNameActivity)
	s.env.RegisterActivity(backupActivity.GetObjectStore)
	s.env.RegisterActivity(backupActivity.IsBackupShared)
	s.env.RegisterActivity(backupActivity.DeleteSnapshotFromObjectStore)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(backupActivity.DeleteBackup)
	s.env.RegisterActivity(scheduledBackupActivity.HydrateDeletedBackupsToCCFE)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupActivity.UpdateBackupError)
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_Success() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	volumes := []*datamodel.Volume{
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
			Name:      "test-volume-1",
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid-2"},
			Name:      "test-volume-2",
		},
	}
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// Mock GetBackupPolicyByUUID returning READY state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	// Mock first batch returning volumes (2 volumes < 20 batch size, so workflow stops after this)
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(volumes, nil).Once()
	// Mock child workflows for each volume
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(len(volumes))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_Success_JobStatusUpdateFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	volumes := []*datamodel.Volume{
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
			Name:      "test-volume-1",
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-uuid-2"},
			Name:      "test-volume-2",
		},
	}
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// Mock GetBackupPolicyByUUID returning READY state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	// Mock first batch returning volumes (2 volumes < 20 batch size, so workflow stops after this)
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(volumes, nil).Once()
	// Mock child workflows for each volume
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(len(volumes))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not update job"))

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.ErrorContains(s.T(), s.env.GetWorkflowError(), "could not update job")
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_CreateJobFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		nil, errors.New("could not create job"))

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create job", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_GetVolumesByBackupPolicyUUIDFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// Mock GetBackupPolicyByUUID returning READY state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(nil, errors.New("could not fetch volumes attached to the backup policy"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not update job"))

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not fetch volumes attached to the backup policy", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_NoVolumesAttached() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// Mock GetBackupPolicyByUUID returning READY state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	// Mock returning empty volumes list (no volumes attached to this backup policy)
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return([]*datamodel.Volume{}, nil).Once()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_PaginationWithLargeVolumeCount() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	// Simulate 50 volumes (will be fetched in 3 batches: 20, 20, 10)
	batch1 := make([]*datamodel.Volume, 20)
	for i := 0; i < 20; i++ {
		batch1[i] = &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("volume-uuid-%d", i)},
			Name:      fmt.Sprintf("test-volume-%d", i),
		}
	}

	batch2 := make([]*datamodel.Volume, 20)
	for i := 0; i < 20; i++ {
		batch2[i] = &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("volume-uuid-%d", i+20)},
			Name:      fmt.Sprintf("test-volume-%d", i+20),
		}
	}

	batch3 := make([]*datamodel.Volume, 10)
	for i := 0; i < 10; i++ {
		batch3[i] = &datamodel.Volume{
			BaseModel: datamodel.BaseModel{UUID: fmt.Sprintf("volume-uuid-%d", i+40)},
			Name:      fmt.Sprintf("test-volume-%d", i+40),
		}
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// Mock GetBackupPolicyByUUID returning READY state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	// Mock first batch (20 volumes)
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(batch1, nil).Once()
	// Mock child workflows for first batch
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(20)

	// Mock second batch (20 volumes)
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 20).
		Return(batch2, nil).Once()
	// Mock child workflows for second batch
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(20)

	// Mock third batch (10 volumes, less than batch size so workflow stops after this)
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 40).
		Return(batch3, nil).Once()
	// Mock child workflows for third batch
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(10)

	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// Test cases for backup policy polling logic (lines 143-195)
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_BackupPolicyReadyImmediately() {
	// Test case: Backup policy is already in READY state, no polling needed
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	// Override polling timeout and interval for faster tests
	originalPollTimeout := pollBackupPolicyTimeout
	originalPollInterval := pollBackupPolicyInterval
	pollBackupPolicyTimeout = 1 * time.Minute
	pollBackupPolicyInterval = 1 * time.Second
	defer func() {
		pollBackupPolicyTimeout = originalPollTimeout
		pollBackupPolicyInterval = originalPollInterval
	}()

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// First call: GetBackupPolicyByUUID returns READY state immediately
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicy.UUID, backupPolicy.AccountID).
		Return(backupPolicy, nil).Once()
	// No volumes to process
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return([]*datamodel.Volume{}, nil).Once()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_BackupPolicyUpdatingThenReady() {
	// Test case: Backup policy starts in UPDATING state, then transitions to READY
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	// Override polling timeout and interval for faster tests
	originalPollTimeout := pollBackupPolicyTimeout
	originalPollInterval := pollBackupPolicyInterval
	pollBackupPolicyTimeout = 1 * time.Minute
	pollBackupPolicyInterval = 1 * time.Second
	defer func() {
		pollBackupPolicyTimeout = originalPollTimeout
		pollBackupPolicyInterval = originalPollInterval
	}()

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyUpdating := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateUpdating,
	}
	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// First call: GetBackupPolicyByUUID returns UPDATING state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyUpdating.UUID, backupPolicyUpdating.AccountID).
		Return(backupPolicyUpdating, nil).Once()
	// Second call: GetBackupPolicyByUUID returns READY state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	// No volumes to process
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return([]*datamodel.Volume{}, nil).Once()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_BackupPolicyDeleted() {
	// Test case: Backup policy is in DELETED state, workflow should exit gracefully
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	// Override polling timeout and interval for faster tests
	originalPollTimeout := pollBackupPolicyTimeout
	originalPollInterval := pollBackupPolicyInterval
	pollBackupPolicyTimeout = 1 * time.Minute
	pollBackupPolicyInterval = 1 * time.Second
	defer func() {
		pollBackupPolicyTimeout = originalPollTimeout
		pollBackupPolicyInterval = originalPollInterval
	}()

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyDeleted := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateDeleted,
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyDeleted.UUID, backupPolicyDeleted.AccountID).
		Return(backupPolicyDeleted, nil).Once()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_BackupPolicyDeletingThenDeleted() {
	// Test case: Backup policy starts in DELETING state, then transitions to DELETED
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	// Override polling timeout and interval for faster tests
	originalPollTimeout := pollBackupPolicyTimeout
	originalPollInterval := pollBackupPolicyInterval
	pollBackupPolicyTimeout = 1 * time.Minute
	pollBackupPolicyInterval = 1 * time.Second
	defer func() {
		pollBackupPolicyTimeout = originalPollTimeout
		pollBackupPolicyInterval = originalPollInterval
	}()

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyDeleting := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateDeleting,
	}
	backupPolicyDeleted := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateDeleted,
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// First call: GetBackupPolicyByUUID returns DELETING state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyDeleting.UUID, backupPolicyDeleting.AccountID).
		Return(backupPolicyDeleting, nil).Once()
	// Second call: GetBackupPolicyByUUID returns DELETED state
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyDeleted.UUID, backupPolicyDeleted.AccountID).
		Return(backupPolicyDeleted, nil).Once()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_BackupPolicyNotFound() {
	// Test case: Backup policy not found (NotFound error), workflow should exit gracefully
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	// Override polling timeout and interval for faster tests
	originalPollTimeout := pollBackupPolicyTimeout
	originalPollInterval := pollBackupPolicyInterval
	pollBackupPolicyTimeout = 1 * time.Minute
	pollBackupPolicyInterval = 1 * time.Second
	defer func() {
		pollBackupPolicyTimeout = originalPollTimeout
		pollBackupPolicyInterval = originalPollInterval
	}()

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	notFoundErr := vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, fmt.Errorf("backup policy not found"))
	temporalErr := vsaerrors.WrapAsTemporalApplicationError(notFoundErr)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// NotFound error is retryable, so it may be called multiple times before the workflow handles it
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, temporalErr)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_BackupPolicyTimeout() {
	// Test case: Backup policy doesn't reach READY state within timeout
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	// Override polling timeout and interval for faster tests
	originalPollTimeout := pollBackupPolicyTimeout
	originalPollInterval := pollBackupPolicyInterval
	pollBackupPolicyTimeout = 100 * time.Millisecond
	pollBackupPolicyInterval = 10 * time.Millisecond
	defer func() {
		pollBackupPolicyTimeout = originalPollTimeout
		pollBackupPolicyInterval = originalPollInterval
	}()

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyUpdating := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateUpdating,
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	// Keep returning UPDATING state until timeout - mock multiple calls
	// The workflow will call GetBackupPolicyByUUID, then sleep, then call again until timeout
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyUpdating.UUID, backupPolicyUpdating.AccountID).
		Return(backupPolicyUpdating, nil)
	// Mock UpdateJobStatus for when the workflow fails due to timeout
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	// Set test timeout to be longer than pollBackupPolicyTimeout to allow the workflow to timeout naturally
	s.env.SetTestTimeout(pollBackupPolicyTimeout + 100*time.Millisecond)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	// Verify it's a timeout error
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "did not reach READY state within")
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_BackupPolicyUnexpectedState() {
	// Test case: Backup policy is in an unexpected state
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	// Override polling timeout and interval for faster tests
	originalPollTimeout := pollBackupPolicyTimeout
	originalPollInterval := pollBackupPolicyInterval
	pollBackupPolicyTimeout = 1 * time.Minute
	pollBackupPolicyInterval = 1 * time.Second
	defer func() {
		pollBackupPolicyTimeout = originalPollTimeout
		pollBackupPolicyInterval = originalPollInterval
	}()

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	// Use an unexpected state (e.g., a state that's not handled in the switch)
	backupPolicyUnexpected := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: "UNEXPECTED_STATE",
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyUnexpected.UUID, backupPolicyUnexpected.AccountID).
		Return(backupPolicyUnexpected, nil).Once()
	// Mock UpdateJobStatus for when the workflow fails due to unexpected state
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	// Verify it's a state conflict error - check for the actual error message content
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "is in UNEXPECTED_STATE state, expected READY state")
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_SkipsSplittingVolume() {
	// One volume in SPLITTING state should be skipped; the other should trigger a child workflow.
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	splittingState := datamodel.CloneStateSplitting
	volumes := []*datamodel.Volume{
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-splitting"},
			Name:      "test-volume-splitting",
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{
					State: splittingState,
				},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-normal"},
			Name:      "test-volume-normal",
		},
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(volumes, nil).Once()
	// Only the non-splitting volume should trigger a child workflow.
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_AllVolumesSplitting_NoChildWorkflows() {
	// All volumes in SPLITTING state — no child workflows should be triggered.
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	splittingState := datamodel.CloneStateSplitting
	volumes := []*datamodel.Volume{
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-splitting-1"},
			Name:      "test-volume-splitting-1",
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{State: splittingState},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-splitting-2"},
			Name:      "test-volume-splitting-2",
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{State: splittingState},
			},
		},
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(volumes, nil).Once()
	// No child workflows should be triggered.
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_NilVolumeAttributes_Proceeds() {
	// Volume with nil VolumeAttributes should not be skipped.
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	volumes := []*datamodel.Volume{
		{
			BaseModel:        datamodel.BaseModel{UUID: "volume-nil-attrs"},
			Name:             "test-volume-nil-attrs",
			VolumeAttributes: nil,
		},
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(volumes, nil).Once()
	// Child workflow should be triggered since VolumeAttributes is nil (no skip condition met).
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_NilCloneParentInfo_Proceeds() {
	// Volume with non-nil VolumeAttributes but nil CloneParentInfo should not be skipped.
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	volumes := []*datamodel.Volume{
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-nil-clone"},
			Name:      "test-volume-nil-clone",
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: nil,
			},
		},
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(volumes, nil).Once()
	// Child workflow should be triggered since CloneParentInfo is nil (no skip condition met).
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupInitWorkflow_NonSplittingClone_Proceeds() {
	// Volume with CloneParentInfo in a non-SPLITTING state (e.g. CLONED) should not be skipped.
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(scheduledBackupActivity.GetBackupPolicyByUUID)
	s.env.RegisterActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	backupPolicyReady := &datamodel.BackupPolicy{
		BaseModel:      datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID:      1,
		LifeCycleState: datamodel.LifeCycleStateREADY,
	}

	volumes := []*datamodel.Volume{
		{
			BaseModel: datamodel.BaseModel{UUID: "volume-cloned"},
			Name:      "test-volume-cloned",
			VolumeAttributes: &datamodel.VolumeAttributes{
				CloneParentInfo: &datamodel.CloneParentInfo{
					State: datamodel.CloneStateCloned,
				},
			},
		},
	}

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.GetBackupPolicyByUUID, mock.Anything, backupPolicyReady.UUID, backupPolicyReady.AccountID).
		Return(backupPolicyReady, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GetVolumesByBackupPolicyUUID, mock.Anything, mock.Anything, mock.Anything, 20, 0).
		Return(volumes, nil).Once()
	// Child workflow should be triggered since state is CLONED, not SPLITTING.
	s.env.OnWorkflow(CreateScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	backupPolicy := &datamodel.BackupPolicy{
		BaseModel: datamodel.BaseModel{UUID: "backup-policy-uuid"},
		AccountID: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupInitWorkflow, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_Success() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)

	s.mockScheduledBackupNodeAndObjectStore(backupActivity)
	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	// Regression: DestinationUUID must propagate to backup.Attributes.EndpointUUID (nil guard must not break happy path).
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.MatchedBy(func(backup *datamodel.Backup) bool {
		return backup != nil && backup.Attributes != nil && backup.Attributes.EndpointUUID == destinationUUID
	})).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.HydrateSnapshotToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func matchSnapmirrorDestinationPath(expected string) interface{} {
	return mock.MatchedBy(func(arg interface{}) bool {
		switch p := arg.(type) {
		case *common.SnapmirrorRelationshipParams:
			return p != nil && p.DestinationPath == expected
		case **common.SnapmirrorRelationshipParams:
			return p != nil && *p != nil && (*p).DestinationPath == expected
		case common.SnapmirrorRelationshipParams:
			return p.DestinationPath == expected
		default:
			return false
		}
	})
}

// TestCreateScheduledBackupWorkflow_ExpertModeUsesExternalUUIDInSmDestinationPath
// verifies GetSmDestinationPath is used and expert-mode pools key the destination by ExternalUUID.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_ExpertModeUsesExternalUUIDInSmDestinationPath() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() { hydrationEnabled = originalhydrationEnabled }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	const (
		poolNetwork      = "pool-network-subnet"
		bucketName       = "vsa-backup-bucket"
		externalVolUUID  = "ontap-external-uuid-1"
		expectedDestPath = bucketName + ":/objstore/" + externalVolUUID
	)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         bucketName,
					VendorSubnetID:     poolNetwork,
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, matchSnapmirrorDestinationPath(expectedDestPath), mock.Anything).
		Return(&common.SnapmirrorRelationship{UUID: "test-uuid-1", DestinationUUID: &destinationUUID}, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(backupActivity.GetVolumeProtocolsFromOntapActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, backupCtx *activities.BackupActivitiesContext) (*activities.BackupActivitiesContext, error) {
			backupCtx.BackupWorkflowInit.Volume.VolumeAttributes.Protocols = []string{"nfs"}
			return backupCtx, nil
		})
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-uuid-1"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{TransferStatus: activities.SmStatusSuccess},
		TransferComplete:        true,
		ShouldContinueAsNew:     false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID: "test-uuid-1", Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			APIAccessMode: PoolAccessModeOntap,
			Network:       poolNetwork,
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "test-zone-1"},
		},
		DataProtection: &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   externalVolUUID,
			VendorSubnetID: "stale-subnet-overwritten-by-pool-network",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID: 1, DailyBackupsToKeep: 3, WeeklyBackupsToKeep: 1, MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_FlexModeUsesVolumeUUIDInSmDestinationPath
// verifies GetSmDestinationPath keys the destination by VCP volume UUID for non-expert pools.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_FlexModeUsesVolumeUUIDInSmDestinationPath() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() { hydrationEnabled = originalhydrationEnabled }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	const (
		volumeUUID       = "volume-uuid-1"
		bucketName       = "vsa-backup-bucket"
		expectedDestPath = bucketName + ":/objstore/" + volumeUUID
	)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         bucketName,
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, matchSnapmirrorDestinationPath(expectedDestPath), mock.Anything).
		Return(&common.SnapmirrorRelationship{UUID: "test-uuid-1", DestinationUUID: &destinationUUID}, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-uuid-1"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{TransferStatus: activities.SmStatusSuccess},
		TransferComplete:        true,
		ShouldContinueAsNew:     false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID: "test-uuid-1", Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.HydrateSnapshotToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: volumeUUID},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{PrimaryZone: "test-zone-1"},
		},
		DataProtection: &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-ignored-for-flex-dest",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID: 1, DailyBackupsToKeep: 3, WeeklyBackupsToKeep: 1, MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_NilVolumeAttributesFails verifies expert-mode volumes
// without attributes fail before object-store / snapmirror setup.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_NilVolumeAttributesFails() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		PoolID:    1,
		Pool: &datamodel.Pool{
			APIAccessMode: PoolAccessModeOntap,
			Network:       "pool-network-subnet",
		},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: nil,
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID: 1, DailyBackupsToKeep: 3, WeeklyBackupsToKeep: 0, MonthlyBackupsToKeep: 0,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.ErrorContains(s.T(), s.env.GetWorkflowError(), "has no volume attributes")
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_GetSmDestinationPathFailure verifies a missing matching
// vault bucket causes the workflow to fail before SnapmirrorGetOrCreate.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetSmDestinationPathFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "vault-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "mismatched-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID: 1, DailyBackupsToKeep: 3, WeeklyBackupsToKeep: 1, MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.ErrorContains(s.T(), s.env.GetWorkflowError(), "no matching bucket details")
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_VaultSwitching_ObjectStoreSizeUpdate_WorkflowCompletes
// verifies workflow still completes with regular size-update path when vault switching is enabled.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_VaultSwitching_ObjectStoreSizeUpdate_WorkflowCompletes() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{BucketName: "vsa-backup-bucket", VendorSubnetID: "test-vendor-subnet-id", ServiceAccountName: "test-service-account"},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)
	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{UUID: "test-uuid-1", DestinationUUID: &destinationUUID}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "test-uuid-1"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{TransferStatus: activities.SmStatusSuccess},
		TransferComplete:        true, ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid-1", Healthy: &healthy}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.HydrateSnapshotToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{Password: "pool-password", SecretID: "pool-credential-secret-id"},
			DeploymentName:  "test-pool-deployment",
			PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "test-zone-1"},
		},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-uuid-1", VendorSubnetID: "test-vendor-subnet-id"},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID: 1, DailyBackupsToKeep: 3, WeeklyBackupsToKeep: 1, MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_VaultSwitching_UpdateBackupSizeRuns
// verifies the scheduled backup workflow completes UpdateBackupSize after object-store size hydration.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_VaultSwitching_UpdateBackupSizeRuns() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{BucketName: "vsa-backup-bucket", VendorSubnetID: "test-vendor-subnet-id", ServiceAccountName: "test-service-account"},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)
	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{UUID: "test-uuid-1", DestinationUUID: &destinationUUID}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "test-uuid-1"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{TransferStatus: activities.SmStatusSuccess},
		TransferComplete:        true, ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{UUID: "test-uuid-1", Healthy: &healthy}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.HydrateSnapshotToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{Password: "pool-password", SecretID: "pool-credential-secret-id"},
			DeploymentName:  "test-pool-deployment",
			PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "test-zone-1"},
		},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-uuid-1", VendorSubnetID: "test-vendor-subnet-id"},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID: 1, DailyBackupsToKeep: 3, WeeklyBackupsToKeep: 1, MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_Success_JobStatusUpdateFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not update job"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.ErrorContains(s.T(), s.env.GetWorkflowError(), "could not update job")
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_CreateJobFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		nil, errors.New("could not create job"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create job", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetBackupVaultFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not fetch backup vault"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not update job status"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not fetch backup vault", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_DailyScheduledBackupFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not create daily scheduled backup"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create daily scheduled backup", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_WeeklyScheduledBackupFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil).Once()
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not create weekly scheduled backup")).Times(3)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create weekly scheduled backup", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_MonthlyScheduledBackupFailure() {
	originalScheduledWeeklyBackupDay := scheduledWeeklyBackupDay
	originalScheduledMonthlyBackupDay := scheduledMonthlyBackupDay
	defer func() {
		scheduledWeeklyBackupDay = originalScheduledWeeklyBackupDay
		scheduledMonthlyBackupDay = originalScheduledMonthlyBackupDay
	}()

	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil).Twice()
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not create monthly scheduled backup")).Times(3)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(2)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create monthly scheduled backup", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_NoBackupsToBeCreated() {
	originalScheduledWeeklyBackupDay := scheduledWeeklyBackupDay
	originalScheduledMonthlyBackupDay := scheduledMonthlyBackupDay
	defer func() {
		scheduledWeeklyBackupDay = originalScheduledWeeklyBackupDay
		scheduledMonthlyBackupDay = originalScheduledMonthlyBackupDay
	}()

	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday()) + 1 // Set to a different day to ensure no weekly backup is not created
	scheduledMonthlyBackupDay = time.Now().UTC().Day() + 1         // Set to a different day to ensure no monthly backup is created

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   0,
		WeeklyBackupsToKeep:  0,
		MonthlyBackupsToKeep: 0,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetNodeFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).
		Return(nil, errors.New("could not fetch nodes of the cluster"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		PoolID: 1,
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not fetch nodes of the cluster", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetObjStoreNameFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id-1",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.PrepareObjectStoreActivity, mock.Anything, mock.Anything).
		Return(nil, fmt.Errorf("no matching bucket details found for volume test-volume-1 in backup vault test-backup-vault"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id-2",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var workflowExecutionError *temporal.WorkflowExecutionError
	if errors.As(s.env.GetWorkflowError(), &workflowExecutionError) {
		assert.ErrorContains(s.T(), workflowExecutionError, "no matching bucket details found for volume test-volume-1 in backup vault test-backup-vault")
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected WorkflowExecutionError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetOrCreateObjectStoreFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.PrepareObjectStoreActivity, mock.Anything, mock.Anything).
		Return(func(_ context.Context, backupCtx *activities.BackupActivitiesContext) (*activities.BackupActivitiesContext, error) {
			backupCtx.ObjStoreName = "vsa-backup-bucket"
			backupCtx.BucketName = "vsa-backup-bucket"
			backupCtx.BucketDetails = defaultScheduledBackupBucketDetails()
			return backupCtx, nil
		})
	s.env.OnActivity(backupActivity.GetOrCreateObjectStoreActivity, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not get or create cloud target"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not get or create cloud target", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_SnapmirrorGetOrCreateFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not get or create snapmirror relationship"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not get or create snapmirror relationship", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GenerateSnapshotNameFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("", errors.New("could not generate snapshot name"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not generate snapshot name", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

// scheduledSnapmirrorRollback records the SnapMirror teardown a failed scheduled backup ran.
type scheduledSnapmirrorRollback struct {
	called       bool
	volumeUUID   string
	endpointUUID string
	excludes     []string
}

// runSnapmirrorRollbackScenario fails a scheduled backup right after the SnapMirror
// relationship step and reports the SnapMirror teardown it ran as a rollback.
func (s *ScheduledBackupsTestSuite) runSnapmirrorRollbackScenario(isExpertMode bool) scheduledSnapmirrorRollback {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	// Expert-mode backup rows key off the ONTAP volume UUID, GCNV-managed ones off the VCP UUID.
	backupVolumeUUID := "volume-uuid-1"
	if isExpertMode {
		backupVolumeUUID = "external-uuid-1"
	}

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "pool-network-subnet",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(func(_ context.Context, _ *datamodel.Volume, _ *datamodel.BackupVault, _, scheduleTag string, _ bool) (*datamodel.Backup, error) {
			return &datamodel.Backup{
				BaseModel:  datamodel.BaseModel{UUID: "scheduled-backup-" + scheduleTag},
				VolumeUUID: backupVolumeUUID,
				Attributes: &datamodel.BackupAttributes{},
			}, nil
		})
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-snapmirror-uuid",
			DestinationUUID: &destinationUUID,
		}, nil)

	rollback := scheduledSnapmirrorRollback{}
	s.env.OnActivity(backupActivity.DeleteSnapmirrorIfUnused, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "test-snapmirror-uuid").
		Return(func(_ context.Context, _ *models.Node, volumeUUID, endpointUUID string, excludeBackupUUIDs []string, _ string) (*vsa.OntapAsyncResponse, error) {
			rollback.called = true
			rollback.volumeUUID = volumeUUID
			rollback.endpointUUID = endpointUUID
			rollback.excludes = excludeBackupUUIDs
			return &vsa.OntapAsyncResponse{}, nil
		}).Maybe()

	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("", errors.New("could not generate snapshot name"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	apiAccessMode := ""
	if isExpertMode {
		apiAccessMode = PoolAccessModeOntap
	}
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			APIAccessMode: apiAccessMode,
			Network:       "pool-network-subnet",
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "pool-network-subnet",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())

	return rollback
}

// ONTAP-mode volume delete is blocked by a leftover relationship, so a failed backup tears it down.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_ExpertMode_DeletesSnapmirrorOnFailure() {
	rollback := s.runSnapmirrorRollbackScenario(true)

	assert.True(s.T(), rollback.called)
	// The relationship is shared by the backups on its endpoint, so the teardown is scoped to the
	// volume and endpoint and told to ignore every backup this run created.
	assert.Equal(s.T(), "external-uuid-1", rollback.volumeUUID)
	assert.Equal(s.T(), "test-destination-uuid-1", rollback.endpointUUID)
	assert.ElementsMatch(s.T(), []string{
		"scheduled-backup-" + common.ScheduleTagDaily,
		"scheduled-backup-" + common.ScheduleTagWeekly,
		"scheduled-backup-" + common.ScheduleTagMonthly,
	}, rollback.excludes)
}

// GCNV-managed volumes keep their existing behaviour: no SnapMirror teardown on backup failure.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_NonExpertMode_KeepsSnapmirrorOnFailure() {
	assert.False(s.T(), s.runSnapmirrorRollbackScenario(false).called)
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_ExpertModeContinuationFailure_DeletesSnapmirror() {
	destinationUUID := "test-destination-uuid-1"
	backupVolumeUUID := "external-uuid-1"
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			APIAccessMode:   PoolAccessModeOntap,
			Network:         "pool-network-subnet",
			PoolCredentials: &datamodel.PoolCredentials{Password: "pool-password", SecretID: "pool-credential-secret-id"},
			DeploymentName:  "test-pool-deployment",
		},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: backupVolumeUUID, VendorSubnetID: "pool-network-subnet"},
	}
	backups := []*datamodel.Backup{
		{BaseModel: datamodel.BaseModel{UUID: "scheduled-backup-daily"}, VolumeUUID: backupVolumeUUID, Attributes: &datamodel.BackupAttributes{}},
		{BaseModel: datamodel.BaseModel{UUID: "scheduled-backup-weekly"}, VolumeUUID: backupVolumeUUID, Attributes: &datamodel.BackupAttributes{}},
	}
	ctx := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{Volume: volume},
		Node:               &models.Node{EndpointAddress: "127.0.0.1"},
		IsExpertMode:       true,
		SnapshotName:       "scheduled-snapshot-name",
		SnapmirrorRelationship: &common.SnapmirrorRelationship{
			UUID:            "test-snapmirror-uuid",
			DestinationUUID: &destinationUUID,
		},
		ScheduledBackupParams: &activities.ScheduledBackupParams{
			BackupPolicy:  &datamodel.BackupPolicy{AccountID: 1, DailyBackupsToKeep: 3},
			Backups:       backups,
			OntapSnapshot: &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-uuid"}},
			Job:           &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"}},
		},
	}

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}
	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.SetContinuedExecutionRunID("previous-run-id")
	s.env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("transfer poll failed after continuation"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	rollback := scheduledSnapmirrorRollback{}
	s.env.OnActivity(backupActivity.DeleteSnapmirrorIfUnused, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, "test-snapmirror-uuid").
		Return(func(_ context.Context, _ *models.Node, volumeUUID, endpointUUID string, excludeBackupUUIDs []string, _ string) (*vsa.OntapAsyncResponse, error) {
			rollback.called = true
			rollback.volumeUUID = volumeUUID
			rollback.endpointUUID = endpointUUID
			rollback.excludes = excludeBackupUUIDs
			return &vsa.OntapAsyncResponse{}, nil
		})

	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflowWithContext, ctx)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.ErrorContains(s.T(), s.env.GetWorkflowError(), "transfer poll failed after continuation")
	assert.True(s.T(), rollback.called)
	assert.Equal(s.T(), backupVolumeUUID, rollback.volumeUUID)
	assert.Equal(s.T(), destinationUUID, rollback.endpointUUID)
	assert.ElementsMatch(s.T(), []string{"scheduled-backup-daily", "scheduled-backup-weekly"}, rollback.excludes)
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_CreateBackupSnapshotInDBFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not create snapshot in database"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create snapshot in database", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_SnapshotCreateFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not create snapshot"))
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create snapshot", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_UpdateSnapshotInDBFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not update snapshot in database"))
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not update snapshot in database", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_SnapmirrorTransferFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not transfer snapshot to object store"))
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not transfer snapshot to object store", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetSnapmirrorTransferStatusFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Times(3)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Times(1)
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("could not get the status of snapmirror transfer"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not get the status of snapmirror transfer", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetSnapmirrorFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	// Rollback activities - these may be called during error handling
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	// Rollback activities - these may be called during error handling
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get snapmirror relationship"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "failed to get snapmirror relationship", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetSnapmirrorUnhealthy() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	// Rollback activities - these may be called during error handling
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	// Rollback activities - these may be called during error handling
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	unhealthy := false
	unhealthyReasons := []string{"Transfer failed", "Connection timeout"}
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:            "test-uuid-1",
		Healthy:         &unhealthy,
		UnhealthyReason: &unhealthyReasons,
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	// The error is wrapped in a WorkflowExecutionError, so check the error message directly
	// The error message should contain the main error about unhealthy snapmirror relationship
	errMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errMsg, "snapmirror relationship is unhealthy")
	// The reasons are logged but may not be in the error message when wrapped
	// Verify it's an internal server error type
	assert.Contains(s.T(), errMsg, "An internal error occurred")
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_GetSnapmirrorStateNotSnapmirrored() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	unhealthy := false
	brokenOffState := "broken_off"
	unhealthyReasons := []string{"Transfer failed", "Connection timeout"}
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:            "test-uuid-1",
		Healthy:         &unhealthy,
		UnhealthyReason: &unhealthyReasons,
		State:           &brokenOffState,
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	errMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errMsg, "snapmirror relationship state is not snapmirrored")
	assert.Contains(s.T(), errMsg, "An internal error occurred")
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_NilDestinationUUID_ReturnsVCPError ensures a SnapMirror
// relationship missing DestinationUUID fails with a recoverable preTransfer ErrInternalServerError
// instead of panicking.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_NilDestinationUUID_ReturnsVCPError() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	// Relationship exists but DestinationUUID is nil — fail immediately after GetOrCreate.
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID: "test-uuid-1",
		}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	errMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errMsg, "DestinationUUID not found in snapmirror relationship")
	assert.Contains(s.T(), errMsg, "An internal error occurred")
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflowWithContext_NilSnapmirrorRelationship_ReturnsVCPError ensures a
// continuation with a nil SnapmirrorRelationship fails with a recoverable preTransfer
// ErrInternalServerError instead of panicking.
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflowWithContext_NilSnapmirrorRelationship_ReturnsVCPError() {
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{Password: "pool-password", SecretID: "pool-credential-secret-id"},
			DeploymentName:  "test-pool-deployment",
			PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "test-zone-1"},
		},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-uuid-1", VendorSubnetID: "test-vendor-subnet-id"},
	}
	backup := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}
	ctx := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: &datamodel.BackupVault{Name: "vault"},
			Volume:      volume,
		},
		Node:                   &models.Node{EndpointAddress: "127.0.0.1"},
		BucketDetails:          &datamodel.BucketDetails{BucketName: "bucket", ServiceAccountName: "svc"},
		ObjStoreName:           "objstore",
		ObjStore:               &common.CloudTarget{Name: "target", UUID: "obj-store-uuid"},
		SnapshotName:           "scheduled-snapshot-name",
		SmSourcePath:           "svm:/vol",
		SmDestinationPath:      "objstore:/path",
		SnapmirrorRelationship: nil,
		ScheduledBackupParams: &activities.ScheduledBackupParams{
			BackupPolicy:  &datamodel.BackupPolicy{AccountID: 1, DailyBackupsToKeep: 3},
			Backups:       []*datamodel.Backup{backup},
			OntapSnapshot: &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-uuid"}},
			Job:           &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"}},
		},
		DbSnapshot: &datamodel.Snapshot{},
	}

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}
	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.SetContinuedExecutionRunID("previous-run-id")
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflowWithContext, ctx)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	errMsg := s.env.GetWorkflowError().Error()
	assert.Contains(s.T(), errMsg, "DestinationUUID not found in snapmirror relationship")
	assert.Contains(s.T(), errMsg, "An internal error occurred")
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_UpdateConstituentCountForBackupFail() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	mockStorage.On("UpdateBackupState", mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.UpdateConstituentCountForBackup, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("could not update constituent count for backup"))
	cv_count := int32(6)
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
		LargeVolumeAttributes: &datamodel.LargeVolumeAttributes{
			LargeCapacity:               true,
			LargeVolumeConstituentCount: &cv_count,
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not update constituent count for backup", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_FinishBackupFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(errors.New("could not update backup status"))
	s.env.OnActivity(backupActivity.UpdateBackupError, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not update backup status", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_NonCriticalActivityFailures() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)

	// Mock the non-critical activities to fail - these should not cause workflow failure
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{}, errors.New("failed to get object store endpoint info"))
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get snapshot from object store"))
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("failed to update backup size"))
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	// The workflow should complete successfully despite the non-critical activity failures
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_UpdateBackupSizeFailure() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = true
	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not update backup size"))
	s.env.OnActivity(backupActivity.HydrateSnapshotToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflowSuccess() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-2",
				},
				Name:        "Monthly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagMonthly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-3",
				},
				Name:        "Daily-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagDaily),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{
			JobUUID: "test-job-uuid-1",
		}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateDeletedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflowSuccess_JobStatusUpdateFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-2",
				},
				Name:        "Monthly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagMonthly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-3",
				},
				Name:        "Daily-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagDaily),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{
			JobUUID: "test-job-uuid-1",
		}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateDeletedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not update job"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var workflowExecutionError *temporal.WorkflowExecutionError
	if errors.As(s.env.GetWorkflowError(), &workflowExecutionError) {
		assert.ErrorContains(s.T(), s.env.GetWorkflowError(), "could not update job")
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_CreateJobFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}

	s.env.RegisterActivity(commonActivity.CreateJob)
	s.env.RegisterActivity(commonActivity.GetJob)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		nil, errors.New("could not create job"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not create job", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_GetBackupVaultFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not get backup vault"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not update job"))

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_FetchScheduledBackupForDeletionFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not fetch scheduled backups for deletion"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}

	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_NoBackupsToBeDeleted() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_GetNodeFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).
		Return(nil, errors.New("could not get node details"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_GetObjectStoreFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not get object store details"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}

	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_IsBackupSharedFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-2",
				},
				Name:        "Monthly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagMonthly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-3",
				},
				Name:        "Daily-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagDaily),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, errors.New("could not determine if backup is shared"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_DeleteSnapshotFromObjectStoreFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-2",
				},
				Name:        "Monthly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagMonthly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-3",
				},
				Name:        "Daily-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagDaily),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not delete snapshot from object store"))
	s.env.OnActivity(backupActivity.UpdateBackupError, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_GetOntapJobFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-2",
				},
				Name:        "Monthly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagMonthly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-3",
				},
				Name:        "Daily-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagDaily),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{
			JobUUID: "test-job-uuid-1",
		}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not get ONTAP job details"))
	s.env.OnActivity(backupActivity.UpdateBackupError, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_DeleteBackupFailure() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-2",
				},
				Name:        "Monthly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagMonthly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-3",
				},
				Name:        "Daily-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagDaily),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{
			JobUUID: "test-job-uuid-1",
		}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, errors.New("could not delete backup"))
	s.env.OnActivity(backupActivity.UpdateBackupError, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())

	var activityError *temporal.ActivityError
	if errors.As(s.env.GetWorkflowError(), &activityError) {
		assert.Equal(s.T(), "could not delete backup", activityError.Unwrap().Error())
	} else {
		assert.Fail(s.T(), fmt.Sprintf("Expected ActivityError but got: %v", s.env.GetWorkflowError()))
	}
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_HydrateDeletedBackupsToCCFE() {
	// Disable hydration for tests
	hydrationEnabled = true
	defer func() { hydrationEnabled = false }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-1",
				},
				Name:        "Weekly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagWeekly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-2",
				},
				Name:        "Monthly-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagMonthly),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
			{
				Attributes: &datamodel.BackupAttributes{
					SnapshotID: "test-snapshot-id-3",
				},
				Name:        "Daily-backup1",
				ScheduleTag: nillable.ToPointer(common.ScheduleTagDaily),
				BackupVault: &datamodel.BackupVault{
					RegionName: "us-central1",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{
			JobUUID: "test-job-uuid-1",
		}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateDeletedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("could not hydrate deleted backups to CCFE"))
	s.env.OnActivity(backupActivity.UpdateBackupError, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_SharedBackupScenario() {
	// Disable hydration for tests
	hydrationEnabled = false
	defer func() { hydrationEnabled = true }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Name: "test-backup-1",
				Attributes: &datamodel.BackupAttributes{
					SnapshotName:       "test-snapshot-1",
					SnapshotID:         "test-snapshot-id-1",
					EndpointUUID:       "test-endpoint-uuid-1",
					BucketName:         "vsa-backup-bucket",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	// This is the key test: IsBackupShared returns true, so DeleteSnapshotFromObjectStore should be skipped
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(true, nil)
	// DeleteSnapshotFromObjectStore should NOT be called when backup is shared
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateDeletedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

// TestDeleteScheduledBackupWorkflow_WaitForONTAPJobFailure tests the failure scenario
// when DeleteSnapshotFromObjectStore succeeds but WaitForONTAPJob fails
func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_WaitForONTAPJobFailure() {
	// Disable hydration for tests
	hydrationEnabled = false
	defer func() { hydrationEnabled = true }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{
			{
				Name: "test-backup-1",
				Attributes: &datamodel.BackupAttributes{
					SnapshotName:       "test-snapshot-1",
					SnapshotID:         "test-snapshot-id-1",
					EndpointUUID:       "test-endpoint-uuid-1",
					BucketName:         "vsa-backup-bucket",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("vsa-backup-bucket", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{
			Name: "vsa-backup-bucket",
			UUID: "test-cloud-target-uuid",
		}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{
			JobUUID: "test-job-uuid-1",
		}, nil)
	// This is the key test: GetOntapJob indicates failure
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapJob{
			State: "failure",
			Error: &vsa.OntapError{
				Message: "failed to delete cloud endpoint",
			},
		}, nil)
	s.env.OnActivity(backupActivity.UpdateBackupError, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name:   "test-volume-1",
		PoolID: 1,
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "failed to delete cloud endpoint")
}

// Test snapshot hydration in CreateScheduledBackupWorkflow
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_SnapshotHydration() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	// Mock all the required activities
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.HydrateSnapshotToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "test-password",
				SecretID:      "test-secret-id",
				CertificateID: "test-cert-id",
				AuthType:      0, // USERNAME_PWD
			},
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "test-external-uuid",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		DailyBackupsToKeep: 3,
	}

	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// Test snapshot de-hydration in DeleteScheduledBackupWorkflow
func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_SnapshotDehydration() {
	// Disable hydration for tests
	hydrationEnabled = true
	defer func() { hydrationEnabled = false }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	// Mock all the required activities
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid-1"},
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: "test-snapshot-id-1",
			},
			Name:        "Weekly-backup1",
			SizeInBytes: 1024,
			ScheduleTag: nillable.ToPointer("weekly"),
		}}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("test-obj-store", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{UUID: "test-cloud-target-uuid"}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{JobUUID: "test-delete-job-uuid"}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateDeletedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "test-password",
				SecretID:      "test-secret-id",
				CertificateID: "test-cert-id",
				AuthType:      0, // USERNAME_PWD
			},
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}

	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// Test snapshot hydration failure handling in CreateScheduledBackupWorkflow
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_SnapshotHydrationFailure() {
	hydrationEnabled = true
	defer func() { hydrationEnabled = false }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	// Mock all the required activities
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
			RegionName: "test-region",
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.HydrateSnapshotToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("could not hydrate snapshot to CCFE"))
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// HydrateCreatedBackupsToCCFE is not called when hydrationEnabled = false
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "test-password",
				SecretID:      "test-secret-id",
				CertificateID: "test-cert-id",
				AuthType:      0, // USERNAME_PWD
			},
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "us-central1-a",
			},
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "test-external-uuid",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		DailyBackupsToKeep: 3,
	}

	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	// Workflow should still complete successfully even if snapshot hydration fails
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// Test snapshot de-hydration failure handling in DeleteScheduledBackupWorkflow
func (s *ScheduledBackupsTestSuite) TestDeleteScheduledBackupWorkflow_SnapshotDehydrationFailure() {
	// Disable hydration for tests
	hydrationEnabled = true
	defer func() { hydrationEnabled = false }()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerDeleteScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	// Mock all the required activities
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			BaseModel:             datamodel.BaseModel{UUID: "test-backup-vault-uuid"},
			Name:                  "test-backup-vault",
			RegionName:            "us-central1",
			LifeCycleState:        "available",
			LifeCycleStateDetails: "available",
			BackupVaultType:       "standard",
			AccountVendorID:       "test-vendor-id",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:     "vsa-backup-bucket",
					VendorSubnetID: "test-vendor-subnet-id",
				},
			},
		}, nil)
	s.mockScheduledBackupNodeResolution(backupActivity)
	s.env.OnActivity(backupActivity.CleanupOldBackupSnapshotsActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.FetchScheduledBackupForDeletion, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]*datamodel.Backup{{
			BaseModel: datamodel.BaseModel{UUID: "test-backup-uuid-1"},
			Attributes: &datamodel.BackupAttributes{
				SnapshotID: "test-snapshot-id-1",
			},
			Name:        "Weekly-backup1",
			SizeInBytes: 1024,
			ScheduleTag: nillable.ToPointer("weekly"),
		}}, nil)
	s.env.OnActivity(backupActivity.GetObjStoreNameActivity, mock.Anything, mock.Anything, mock.Anything).
		Return("test-obj-store", nil)
	s.env.OnActivity(backupActivity.GetObjectStore, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.CloudTarget{UUID: "test-cloud-target-uuid"}, nil)
	s.env.OnActivity(backupActivity.IsBackupShared, mock.Anything, mock.Anything).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{JobUUID: "test-delete-job-uuid"}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapJob{State: "success"}, nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateDeletedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "test-volume-uuid"},
		Name:      "test-volume",
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{UUID: "test-account-uuid"},
			Name:      "test-account",
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "test-backup-vault-uuid",
		},
		Pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{UUID: "test-pool-uuid"},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password:      "test-password",
				SecretID:      "test-secret-id",
				CertificateID: "test-cert-id",
				AuthType:      0, // USERNAME_PWD
			},
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}

	s.env.ExecuteWorkflow(DeleteScheduledBackupWorkflow, volume, backupPolicy)

	// Workflow should still complete successfully even if snapshot de-hydration fails
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflowWithContext_Success() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	// Minimal context for a successful run
	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	backup := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}
	backupVault := &datamodel.BackupVault{Name: "vault"}
	modelsNode := &models.Node{
		EndpointAddress: "127.0.0.1",
	}
	node := modelsNode
	job := &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"}}
	bucketDetails := &datamodel.BucketDetails{BucketName: "bucket", ServiceAccountName: "svc"}
	cloudTarget := &common.CloudTarget{Name: "target"}
	snapmirror := &common.SnapmirrorRelationship{UUID: "sm-uuid"}
	snapshot := &datamodel.Snapshot{}
	ontapSnapshot := &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-uuid"}}
	ctx := &activities.BackupActivitiesContext{
		BackupWorkflowInit: &activities.BackupWorkflowInput{
			Backup:      backup,
			BackupVault: backupVault,
			Volume:      volume,
		},
		Node:                   node,
		BucketDetails:          bucketDetails,
		ObjStoreName:           "objstore",
		ObjStore:               cloudTarget,
		SnapmirrorRelationship: snapmirror,
		ScheduledBackupParams: &activities.ScheduledBackupParams{
			BackupPolicy:  backupPolicy,
			Backups:       []*datamodel.Backup{backup},
			OntapSnapshot: ontapSnapshot,
			Job:           job,
		},
		DbSnapshot: snapshot,
	}
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: ctx,
		TransferComplete:        true,
		ShouldContinueAsNew:     false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.HydrateCreatedBackupsToCCFE, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(scheduledBackupActivity.CreateRemoteScheduledBackupsFromVCPActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflowWithContext, ctx)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflowWithContext_Continuation() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	backup := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}
	backupVault := &datamodel.BackupVault{Name: "vault"}
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusTransferring,
		},
		TransferComplete:    false,
		ShouldContinueAsNew: true,
		ContinueAsNewReason: "Event history limit reached",
		NextWaitTime:        5 * time.Millisecond,
	}, nil)

	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)
	// Assert that the workflow was executed and ContinueAsNew was triggered
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.True(s.T(), workflow.IsContinueAsNewError(s.env.GetWorkflowError()))
	s.env.AssertExpectations(s.T())
}

func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflowWithContext_PollTransferStatusError() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	backup := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}
	backupVault := &datamodel.BackupVault{Name: "vault"}
	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity("PollTransferStatusWithHistoryCheckActivity", mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			BackupWorkflowInit: &activities.BackupWorkflowInput{
				Backup:      backup,
				BackupVault: backupVault,
				Volume:      volume,
			},
			Node:         &models.Node{EndpointAddress: "127.0.0.1"},
			SnapshotName: "test-backup",
			SnapmirrorRelationship: &common.SnapmirrorRelationship{
				UUID: "test-snapmirror-uuid",
			},
			TransferStatus: activities.SmStatusTransferring,
		},
		TransferComplete:    false,
		ShouldContinueAsNew: true,
		ContinueAsNewReason: "Event history limit reached",
		NextWaitTime:        5 * time.Millisecond,
	}, errors.New("transfer status error"))
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).
		Return(nil, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "transfer status error")
	s.env.AssertExpectations(s.T())
}
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflowWithContext_SleepBeforeGetSnapmirrorError() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{UUID: "volume-uuid-1"},
		Name:      "test-volume-1",
		Account:   &datamodel.Account{Name: "test-account"},
		Svm:       &datamodel.Svm{Name: "test-svm-1"},
		PoolID:    1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{Password: "pool-password", SecretID: "pool-credential-secret-id"},
			DeploymentName:  "test-pool-deployment",
			PoolAttributes:  &datamodel.PoolAttributes{PrimaryZone: "test-zone-1"},
		},
		DataProtection:   &datamodel.DataProtection{BackupVaultID: "backup-vault-uuid-1"},
		VolumeAttributes: &datamodel.VolumeAttributes{ExternalUUID: "external-uuid-1", VendorSubnetID: "test-vendor-subnet-id"},
	}
	backupPolicy := &datamodel.BackupPolicy{AccountID: 1, DailyBackupsToKeep: 3, WeeklyBackupsToKeep: 1, MonthlyBackupsToKeep: 1}
	backup := &datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}
	backupVault := &datamodel.BackupVault{Name: "vault"}
	ctx := &activities.BackupActivitiesContext{
		BackupWorkflowInit:     &activities.BackupWorkflowInput{Backup: backup, BackupVault: backupVault, Volume: volume},
		Node:                   &models.Node{EndpointAddress: "127.0.0.1"},
		BucketDetails:          &datamodel.BucketDetails{BucketName: "bucket", ServiceAccountName: "svc"},
		ObjStoreName:           "objstore",
		ObjStore:               &common.CloudTarget{Name: "target"},
		SnapmirrorRelationship: &common.SnapmirrorRelationship{UUID: "sm-uuid"},
		ScheduledBackupParams: &activities.ScheduledBackupParams{
			BackupPolicy:  backupPolicy,
			Backups:       []*datamodel.Backup{backup},
			OntapSnapshot: &vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "snap-uuid"}},
			Job:           &datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"}},
		},
		DbSnapshot: &datamodel.Snapshot{},
	}

	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{{
				BucketName: "vsa-backup-bucket", VendorSubnetID: "test-vendor-subnet-id", ServiceAccountName: "test-service-account",
			}},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)
	destUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{UUID: "test-uuid-1", DestinationUUID: &destUUID}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{ProviderResponse: vsa.ProviderResponse{ExternalUUID: "test-uuid-1"}}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&activities.PollTransferStatusOutput{
			BackupActivitiesContext: ctx,
			TransferComplete:        true,
			ShouldContinueAsNew:     false,
		}, nil)
	// Cancel the workflow when the 30s sleep timer is scheduled so workflow.Sleep returns error and we hit line 553
	s.env.SetOnTimerScheduledListener(func(timerID string, duration time.Duration) {
		if duration == 30*time.Second {
			s.env.CancelWorkflow()
		}
	})

	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflowWithContext, ctx)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	// Cancelling during the 30s sleep causes workflow.Sleep to return; the error may appear as "failed to sleep before getting the snapmirror" or be wrapped (e.g. "internal error" from UpdateJobStatus after cancel)
	errStr := s.env.GetWorkflowError().Error()
	assert.True(s.T(),
		strings.Contains(errStr, "failed to sleep before getting the snapmirror") || strings.Contains(errStr, "canceled") || strings.Contains(errStr, "internal error"),
		"workflow error should reflect sleep failure or cancellation: %s", errStr)
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_CheckBackupInCreatingState_RetrySuccess tests the scenario where
// a backup is initially in CREATING state, but the check succeeds after retries
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_CheckBackupInCreatingState_RetrySuccess() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = false
	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	// First call returns error (backup in CREATING state), second call succeeds
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("another backup operation is already in progress")).Once()
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	s.env.OnActivity(backupActivity.SnapmirrorTransfer, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.PollTransferStatusWithHistoryCheckActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&activities.PollTransferStatusOutput{
		BackupActivitiesContext: &activities.BackupActivitiesContext{
			TransferStatus: activities.SmStatusSuccess,
		},
		TransferComplete:    true,
		ShouldContinueAsNew: false,
	}, nil)
	healthy := true
	s.env.OnActivity(backupActivity.GetSnapmirror, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&common.SnapmirrorRelationship{
		UUID:    "test-uuid-1",
		Healthy: &healthy,
	}, nil)
	s.env.OnActivity(backupActivity.FinishBackup, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.GetObjectStoreEndpointInfo, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointt{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(backupActivity.GetSnapshotFromObjectStore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(&vsa.SmObjectStoreEndpointSnapshot{LogicalSize: &[]int64{1024000}[0]}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSize, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.CreateBackupMetadataIfFirstBackupActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnWorkflow(DeleteScheduledBackupWorkflow, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_CheckBackupInCreatingState_DatabaseError tests the scenario where
// the database check fails with an error
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_CheckBackupInCreatingState_DatabaseError() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = false

	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	// Database error - should retry but eventually fail after retries exhausted
	// Note: Temporal test framework may not execute all retries, so we use Maybe() to allow any number of calls
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("database connection failed")).Maybe()
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "database connection failed")
	s.env.AssertExpectations(s.T())
}

// TestCreateScheduledBackupWorkflow_CheckBackupInCreatingState_RetriesExhausted tests the scenario where
// a backup is in CREATING state and all retries are exhausted
func (s *ScheduledBackupsTestSuite) TestCreateScheduledBackupWorkflow_CheckBackupInCreatingState_RetriesExhausted() {
	scheduledWeeklyBackupDay = int(time.Now().UTC().Weekday())
	scheduledMonthlyBackupDay = time.Now().UTC().Day()
	originalhydrationEnabled := hydrationEnabled
	hydrationEnabled = false

	defer func() {
		hydrationEnabled = originalhydrationEnabled
	}()

	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	commonActivity := &activities.CommonActivities{SE: mockStorage}
	backupActivity := &activities.BackupActivity{SE: mockStorage}
	scheduledBackupActivity := &backgroundactivities.ScheduledBackupActivity{SE: mockStorage}

	s.registerCreateScheduledBackupActivities(commonActivity, backupActivity, scheduledBackupActivity)

	s.env.OnActivity(commonActivity.CreateJob, mock.Anything, mock.Anything).Return(
		&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{
			Name: "test-backup-vault",
			BucketDetails: []*datamodel.BucketDetails{
				{
					BucketName:         "vsa-backup-bucket",
					VendorSubnetID:     "test-vendor-subnet-id",
					ServiceAccountName: "test-service-account",
				},
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.CreateScheduledBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Backup{Attributes: &datamodel.BackupAttributes{}}, nil)
	s.mockScheduledBackupNodeAndObjectStore(backupActivity)

	destinationUUID := "test-destination-uuid-1"
	s.env.OnActivity(backupActivity.SnapmirrorGetOrCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&common.SnapmirrorRelationship{
			UUID:            "test-uuid-1",
			DestinationUUID: &destinationUUID,
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.GenerateScheduledSnapshotName, mock.Anything, mock.Anything).
		Return("scheduled-snapshot-name", nil)
	s.env.OnActivity(scheduledBackupActivity.CreateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	s.env.OnActivity(backupActivity.SnapshotCreate, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.SnapshotProviderResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-uuid-1",
			},
		}, nil)
	s.env.OnActivity(scheduledBackupActivity.UpdateBackupSnapshotInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.Snapshot{}, nil)
	// Backup always in CREATING state - should retry but eventually fail after retries exhausted
	// Note: Temporal test framework may not execute all retries, so we use Maybe() to allow any number of calls
	s.env.OnActivity(scheduledBackupActivity.CheckBackupsInProgressByVolume, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("another backup operation is already in progress")).Maybe()
	s.env.OnActivity(scheduledBackupActivity.DeleteBackupSnapshotInDB, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshot, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackup, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	volume := &datamodel.Volume{
		BaseModel: datamodel.BaseModel{
			UUID: "volume-uuid-1",
		},
		Name: "test-volume-1",
		Account: &datamodel.Account{
			Name: "test-account",
		},
		Svm: &datamodel.Svm{
			Name: "test-svm-1",
		},
		PoolID: 1,
		Pool: &datamodel.Pool{
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "pool-password",
				SecretID: "pool-credential-secret-id",
			},
			DeploymentName: "test-pool-deployment",
			PoolAttributes: &datamodel.PoolAttributes{
				PrimaryZone: "test-zone-1",
			},
		},
		DataProtection: &datamodel.DataProtection{
			BackupVaultID: "backup-vault-uuid-1",
		},
		VolumeAttributes: &datamodel.VolumeAttributes{
			ExternalUUID:   "external-uuid-1",
			VendorSubnetID: "test-vendor-subnet-id",
		},
	}
	backupPolicy := &datamodel.BackupPolicy{
		AccountID:            1,
		DailyBackupsToKeep:   3,
		WeeklyBackupsToKeep:  1,
		MonthlyBackupsToKeep: 1,
	}
	s.env.ExecuteWorkflow(CreateScheduledBackupWorkflow, volume, backupPolicy)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "another backup operation is already in progress")
	s.env.AssertExpectations(s.T())
}
