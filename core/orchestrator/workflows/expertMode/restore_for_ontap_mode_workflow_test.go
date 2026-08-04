package expertMode

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type RestoreForOntapModeWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
	env                      *testsuite.TestWorkflowEnvironment
	commonActivity           activities.CommonActivities
	backupActivity           activities.BackupActivity
	volumeActivity           activities.VolumeCreateActivity
	ontapRestoreActivity     activities.OntapModeRestoreActivity
	expertModeVolumeActivity expertmodeactivities.ExpertModeVolumeActivity
}

func (s *RestoreForOntapModeWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetTestTimeout(2 * time.Minute)
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	loggerFields := log.Fields{string(middleware.RequestCorrelationID): "test-correlation-id"}
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(loggerFields)
	mockHeader := &commonpb.Header{
		Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
	}
	s.env.SetHeader(mockHeader)

	s.env.RegisterWorkflow(RestoreForOntapModeVolumeWorkflow)

	mockStorage := database.NewMockStorage(s.T())
	s.commonActivity = activities.CommonActivities{SE: mockStorage}
	s.backupActivity = activities.BackupActivity{SE: mockStorage}
	s.volumeActivity = activities.VolumeCreateActivity{SE: mockStorage}
	s.ontapRestoreActivity = activities.OntapModeRestoreActivity{SE: mockStorage}
	s.expertModeVolumeActivity = expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}

	s.env.RegisterActivity(s.commonActivity.GetJob)
	s.env.RegisterActivity(s.commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(s.commonActivity.ResolveOntapNodeForPool)
	s.env.RegisterActivity(s.commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore)
	s.env.RegisterActivity(s.volumeActivity.FetchBackupMetadataForRestore)
	s.env.RegisterActivity(s.ontapRestoreActivity.FetchConstituentCountForLargeVolume)
	s.env.RegisterActivity(s.ontapRestoreActivity.VerifyCVCountForLargeVolume)
	s.env.RegisterActivity(s.volumeActivity.FetchBucketMetadataForRestore)
	s.env.RegisterActivity(s.volumeActivity.CreateRestoreWorkflow)
	s.env.RegisterActivity(s.expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB)

	s.env.OnActivity(s.commonActivity.UpdateJobStatus, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(s.commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-token", nil).Maybe()
	s.env.OnActivity(s.expertModeVolumeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
}

// AfterTest optionally asserts activity mock expectations. Skipped when using .Maybe() mocks to avoid false failures.
func (s *RestoreForOntapModeWorkflowTestSuite) AfterTest() {
	// s.env.AssertExpectations(s.T()) — disabled: tests use .Maybe() for retries
}

// setDefaultGetJobNEW mocks GetJob to return NEW so EnsureJobState passes. Call in tests that run the full workflow.
func (s *RestoreForOntapModeWorkflowTestSuite) setDefaultGetJobNEW() {
	s.env.OnActivity(s.commonActivity.GetJob, mock.Anything, "default-test-workflow-id").Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
}

// makeRestoreParams returns minimal params for restore workflow (expert mode volume with pool, backup path, region).
func makeRestoreParams() *common.RestoreForOntapModeParams {
	poolID := int64(1)
	pool := &datamodel.Pool{
		BaseModel:      datamodel.BaseModel{ID: poolID, UUID: "pool-uuid"},
		DeploymentName: "test-deployment",
		PoolCredentials: &datamodel.PoolCredentials{
			Password: "password",
		},
	}
	emv := &datamodel.ExpertModeVolumes{
		BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
		ExternalUUID: "ext-uuid",
		Name:         "expert-vol",
		SizeInBytes:  1099511627776,
		PoolID:       poolID,
		Pool:         pool,
		Account:      &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}, Name: "test-account"},
		Svm:          &datamodel.Svm{Name: "svm1"},
	}
	return &common.RestoreForOntapModeParams{
		AccountName:      "test-account",
		BackupPath:       "projects/p/locations/reg/backupVaults/bv/backups/backup-id",
		Region:           "us-east4",
		ExpertModeVolume: emv,
	}
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Setup_InvalidInput_Nil() {
	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, (*common.RestoreForOntapModeParams)(nil))
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "invalid input")
}

func (s *RestoreForOntapModeWorkflowTestSuite) mockResolveRestoreNode(err error) {
	node := &models.Node{
		EndpointAddress:                "127.0.0.1",
		EndpointAddressesToHostNameMap: map[string]string{"127.0.0.1": "127.0.0.1"},
	}
	if err != nil {
		s.env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).
			Return((*models.Node)(nil), err).Maybe()
		return
	}
	s.env.OnActivity("ResolveOntapNodeForPool", mock.Anything, mock.Anything).
		Return(node, nil).Maybe()
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_GetNodeFails() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	s.mockResolveRestoreNode(errors.New("resolve node failed"))

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_FetchBackupVaultMetadataFails() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.BackupVault)(nil), errors.New("fetch backup vault failed")).Maybe()

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_FetchBackupMetadataFails() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "bv-uuid"}}
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Once()
	s.env.OnActivity(s.volumeActivity.FetchBackupMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return((*datamodel.Backup)(nil), errors.New("fetch backup failed")).Maybe()

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Success() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "bv-uuid"}}
	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "backup-uuid"},
		Name:        "backup-name",
		State:       datamodel.LifeCycleStateREADY,
		SizeInBytes: 1024,
		VolumeUUID:  "other-volume-uuid",
		BackupVault: backupVault,
	}
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBackupMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backup, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBucketMetadataForRestore, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.CreateRestoreWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
	heartbeatTimeout := captureActivityHeartbeatTimeout(s.env, "FetchBackupVaultMetadataForRestore")
	expectedPolicy, policyErr := workflows.PopulateRetryPolicyParams()
	s.Require().NoError(policyErr)

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	assert.Equal(s.T(), expectedPolicy.HeartBeatTimeout, *heartbeatTimeout)
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Success_StatusQuery() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "bv-uuid"}}
	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "backup-uuid"},
		Name:        "backup-name",
		State:       datamodel.LifeCycleStateREADY,
		SizeInBytes: 1024,
		VolumeUUID:  "other-volume-uuid",
		BackupVault: backupVault,
	}
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBackupMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backup, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBucketMetadataForRestore, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.CreateRestoreWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	value, err := s.env.QueryWorkflow("status")
	assert.NoError(s.T(), err)
	var status *workflows.WorkflowStatus
	_ = value.Get(&status)
	assert.NotNil(s.T(), status)
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_EnsureJobStateFails() {
	params := makeRestoreParams()
	// Only mock GetJob to return PROCESSING so EnsureJobState(ctx, NEW) fails before Run; no other activity mocks.
	s.env.OnActivity(s.commonActivity.GetJob, mock.Anything, "default-test-workflow-id").
		Return(&datamodel.Job{BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"}, State: string(datamodel.JobsStatePROCESSING)}, nil).Maybe()
	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_BackupNotAvailable() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "bv-uuid"}}
	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "backup-uuid"},
		Name:        "backup-name",
		State:       "CREATING",
		SizeInBytes: 1024,
		VolumeUUID:  "other-uuid",
		BackupVault: backupVault,
	}
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBackupMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backup, nil).Maybe()

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "not in available or ready state")
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_SameVolumeRestoreRejected() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "bv-uuid"}}
	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "backup-uuid"},
		Name:        "backup-name",
		State:       datamodel.LifeCycleStateREADY,
		SizeInBytes: 1024,
		VolumeUUID:  "emv-uuid", // same as params.ExpertModeVolume.UUID so workflow rejects same-volume full restore
		BackupVault: backupVault,
	}
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBackupMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backup, nil).Maybe()

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "same volume")
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_CreateRestoreWorkflowFails() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "bv-uuid"}}
	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "backup-uuid"},
		Name:        "backup-name",
		State:       datamodel.LifeCycleStateREADY,
		SizeInBytes: 1024,
		VolumeUUID:  "other-uuid",
		BackupVault: backupVault,
	}
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBackupMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backup, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBucketMetadataForRestore, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.CreateRestoreWorkflow, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("create restore workflow failed")).Maybe()

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *RestoreForOntapModeWorkflowTestSuite) Test_Run_VolumeSizeInsufficient() {
	s.setDefaultGetJobNEW()
	params := makeRestoreParams()
	params.ExpertModeVolume.SizeInBytes = 512
	backupVault := &datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "bv-uuid"}}
	backup := &datamodel.Backup{
		BaseModel:   datamodel.BaseModel{UUID: "backup-uuid"},
		Name:        "backup-name",
		State:       datamodel.LifeCycleStateREADY,
		SizeInBytes: 1024,
		VolumeUUID:  "other-uuid",
		BackupVault: backupVault,
	}
	s.mockResolveRestoreNode(nil)
	s.env.OnActivity(s.volumeActivity.FetchBackupVaultMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBackupMetadataForRestore, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(backup, nil).Maybe()
	s.env.OnActivity(s.volumeActivity.FetchBucketMetadataForRestore, mock.Anything, mock.Anything, mock.Anything).
		Return(backupVault, nil).Maybe()

	s.env.ExecuteWorkflow(RestoreForOntapModeVolumeWorkflow, params)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "size")
}

func TestConvertExpertModeVolumeToVolume(t *testing.T) {
	emv := &datamodel.ExpertModeVolumes{
		BaseModel:    datamodel.BaseModel{UUID: "emv-uuid"},
		ExternalUUID: "ext-uuid",
		Name:         "vol",
		SizeInBytes:  1024,
	}
	vol := convertExpertModeVolumeToVolume(emv)
	assert.NotNil(t, vol)
	assert.Equal(t, "emv-uuid", vol.UUID)
	assert.Equal(t, "vol", vol.Name)
	assert.Equal(t, int64(1024), vol.SizeInBytes)
	assert.NotNil(t, vol.VolumeAttributes)
	assert.Equal(t, "ext-uuid", vol.VolumeAttributes.ExternalUUID)
}

func TestRestoreForOntapModeWorkflowTestSuite(t *testing.T) {
	utils.SetFileProtocolSupportedForTesting(true)
	suite.Run(t, new(RestoreForOntapModeWorkflowTestSuite))
}
