package workflows

import (
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/vsa"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

const operationBudgetTestStartTime = "2006-01-02T15:04:05Z"

// VolumeUpdateOperationBudgetTestSuite holds focused operation-budget tests without the broad
// SetupTest mocks from VolumeUpdateTestSuite (which would override GetJob timing).
type VolumeUpdateOperationBudgetTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env                  *testsuite.TestWorkflowEnvironment
	savedOperationBudget time.Duration
}

func TestVolumeUpdateOperationBudgetTestSuite(t *testing.T) {
	suite.Run(t, new(VolumeUpdateOperationBudgetTestSuite))
}

func (s *VolumeUpdateOperationBudgetTestSuite) SetupTest() {
	s.savedOperationBudget = operationBudget
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	s.env.SetHeader(&commonpb.Header{
		Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
	})
	s.env.RegisterWorkflow(UpdateVolumeWorkflow)
}

func (s *VolumeUpdateOperationBudgetTestSuite) AfterTest(_ string, _ string) {
	operationBudget = s.savedOperationBudget
	s.env.AssertExpectations(s.T())
}

func operationBudgetTestVolume() *datamodel.Volume {
	return &datamodel.Volume{
		BaseModel: datamodel.BaseModel{ID: 2, UUID: "operation-budget-test-volume"},
		Pool: &datamodel.Pool{
			BaseModel:      datamodel.BaseModel{ID: 1},
			DeploymentName: "test-deployment",
			PoolCredentials: &datamodel.PoolCredentials{
				Password: "password",
			},
		},
		Account:     &datamodel.Account{Name: "test_account"},
		SizeInBytes: 100,
		VolumeAttributes: &datamodel.VolumeAttributes{
			IsDataProtection: false,
		},
	}
}

func parseOperationBudgetTestStart(t *testing.T) time.Time {
	t.Helper()
	start, err := time.Parse(time.RFC3339, operationBudgetTestStartTime)
	require.NoError(t, err)
	return start
}

func (s *VolumeUpdateOperationBudgetTestSuite) registerJobActivities(
	mockStorage *database.MockStorage,
	commonActivity activities.CommonActivities,
	getJobDelay time.Duration,
) {
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetJob)

	call := s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).
		Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
	if getJobDelay > 0 {
		call.After(getJobDelay)
	}
}

func (s *VolumeUpdateOperationBudgetTestSuite) Test_OperationBudget_QueueTooLong() {
	operationBudget = 30 * time.Minute
	s.env.SetStartTime(parseOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 31*time.Minute)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil).
		Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, &common.UpdateVolumeParams{QuotaInBytes: 200}, operationBudgetTestVolume())

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "GetNode", 0)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdateVolumeStateInDB", 1)
}

func (s *VolumeUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededAfterActivity() {
	operationBudget = 30 * time.Minute
	s.env.SetStartTime(parseOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		After(31*time.Minute).
		Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, &common.UpdateVolumeParams{QuotaInBytes: 200}, operationBudgetTestVolume())

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdateVolumeStateInDB", 1)
}

func (s *VolumeUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededWithRollback() {
	operationBudget = 30 * time.Minute
	s.env.SetStartTime(parseOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInONTAP)
	s.env.RegisterActivity(updateActivity.UpdateLun)
	s.env.RegisterActivity(updateActivity.UpdateVolumeInDB)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeResponse{
			ProviderResponse: vsa.ProviderResponse{
				ExternalUUID: "test-external-uuid",
				Name:         "test_volume",
			},
			AvailableSpace: 1000,
			Size:           100,
			State:          "online",
		}, nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		After(31 * time.Minute).
		Return(nil)
	s.env.OnActivity(updateActivity.UpdateVolumeInONTAP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).
		Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	volume := operationBudgetTestVolume()
	volume.BaseModel = datamodel.BaseModel{ID: 2, UUID: "operation-budget-test-volume"}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, &common.UpdateVolumeParams{QuotaInBytes: 200}, volume)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	if wfErr := s.env.GetWorkflowError(); wfErr != nil {
		s.T().Logf("workflow error: %v", wfErr)
	}
	s.env.AssertActivityNumberOfCalls(s.T(), "GetVolumeFromONTAP", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdateVolumeInONTAP", 2)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdateVolumeStateInDB", 1)
}

func (s *VolumeUpdateOperationBudgetTestSuite) Test_OperationBudget_ZeroWorkflowStartTime() {
	operationBudget = 30 * time.Minute
	// Intentionally omit SetStartTime so WorkflowStartTime is zero and the budget
	// anchors to orchestrator.Now (Temporal test env may leave start time unset).

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 31*time.Minute)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil).
		Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, &common.UpdateVolumeParams{QuotaInBytes: 200}, operationBudgetTestVolume())

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "GetNode", 0)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdateVolumeStateInDB", 1)
}

func (s *VolumeUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededAfterSnapmirrorWait() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	operationBudget = 30 * time.Minute
	s.env.SetStartTime(parseOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{JobUUID: "snapmirror-job-uuid"}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "snapmirror-job-uuid", mock.Anything).
		After(31*time.Minute).
		Return(&vsa.OntapJob{State: "success", UUID: "snapmirror-job-uuid"}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 99}}, nil).
		Maybe()
	s.env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Backup)(nil), nil).
		Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	newVaultID := "new-vault-uuid"
	volume := operationBudgetTestVolume()
	volume.DataProtection = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   200,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &newVaultID},
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "DeleteSnapmirrorInONTAP", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "GetOntapJob", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdateVolumeStateInDB", 1)
}

func (s *VolumeUpdateOperationBudgetTestSuite) Test_OperationBudget_ExceededAfterBackupVaultSwitch() {
	origFlag := utils.EnableBackupVaultSwitching
	defer utils.SetEnableBackupVaultSwitchingForTest(origFlag)
	utils.SetEnableBackupVaultSwitchingForTest(true)

	operationBudget = 30 * time.Minute
	s.env.SetStartTime(parseOperationBudgetTestStart(s.T()))

	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}
	volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}

	s.registerJobActivities(mockStorage, commonActivity, 0)
	s.env.RegisterActivity(commonActivity.GetNode)
	s.env.RegisterActivity(commonActivity.GetOntapJob)
	s.env.RegisterActivity(updateActivity.GetVolumeFromONTAP)
	s.env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	s.env.RegisterActivity(backupActivity.GetBackupVault)
	s.env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
	s.env.RegisterActivity(backupActivity.IsExpertModeVolume)
	s.env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)
	s.env.RegisterActivity(backupActivity.DeleteBackupSnapshotFromDB)
	s.env.RegisterActivity(backupActivity.HydrateSnapshotDeletionToCCFEActivity)
	s.env.RegisterActivity(volumeCreateActivity.UpdateVolumeStateInDB)

	volumeUUID := "operation-budget-vault-switch"
	snapshotExtID := "snapshot-ext-id"
	volumeExtUUID := "volume-ext-id"
	latestBackup := &datamodel.Backup{
		BaseModel: datamodel.BaseModel{UUID: "backup-latest"},
		Attributes: &datamodel.BackupAttributes{
			SnapshotID:          snapshotExtID,
			UseExistingSnapshot: false,
		},
	}

	s.env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
		Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
	s.env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.OntapAsyncResponse{JobUUID: "snapmirror-job-uuid"}, nil)
	s.env.OnActivity(commonActivity.GetOntapJob, mock.Anything, "snapmirror-job-uuid", mock.Anything).
		Return(&vsa.OntapJob{State: "success", UUID: "snapmirror-job-uuid"}, nil)
	s.env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").
		Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 99}}, nil)
	s.env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, volumeUUID, int64(99)).
		Return(latestBackup, nil)
	s.env.OnActivity(backupActivity.IsExpertModeVolume, mock.Anything, volumeUUID).
		Return(false, nil)
	s.env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, snapshotExtID, volumeExtUUID, false).
		Return(nil)
	s.env.OnActivity(backupActivity.DeleteBackupSnapshotFromDB, mock.Anything, latestBackup).
		Return(nil)
	s.env.OnActivity(backupActivity.HydrateSnapshotDeletionToCCFEActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		After(31 * time.Minute).
		Return(nil)
	s.env.OnActivity(updateActivity.GetVolumeFromONTAP, mock.Anything, mock.Anything, mock.Anything).
		Return(&vsa.VolumeResponse{
			ProviderResponse: vsa.ProviderResponse{ExternalUUID: volumeExtUUID, Name: "vol"},
			AvailableSpace:   1000, Size: 1000, State: "online",
		}, nil).
		Maybe()
	s.env.OnActivity(volumeCreateActivity.UpdateVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	newVaultID := "new-vault-uuid"
	volume := operationBudgetTestVolume()
	volume.BaseModel = datamodel.BaseModel{UUID: volumeUUID}
	volume.VolumeAttributes = &datamodel.VolumeAttributes{
		IsDataProtection: false,
		ExternalUUID:     volumeExtUUID,
	}
	volume.DataProtection = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
	params := &common.UpdateVolumeParams{
		QuotaInBytes:   200,
		DataProtection: &models.UpdateDataProtection{BackupVaultID: &newVaultID},
	}

	s.env.ExecuteWorkflow(UpdateVolumeWorkflow, params, volume)

	require.True(s.T(), s.env.IsWorkflowCompleted())
	require.Error(s.T(), s.env.GetWorkflowError())
	s.env.AssertActivityNumberOfCalls(s.T(), "HydrateSnapshotDeletionToCCFEActivity", 1)
	s.env.AssertActivityNumberOfCalls(s.T(), "UpdateVolumeStateInDB", 1)
}
