package expertMode

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

// newManageBackupConfigTestEnv creates a fresh workflow test environment with a mock
// header (required for logger propagation). It returns the env and a MockStorage
// so callers can set up activity mocks.
func newManageBackupConfigTestEnv(t *testing.T) (*testsuite.TestWorkflowEnvironment, *database.MockStorage) {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	encodedValue, _ := converter.GetDefaultDataConverter().ToPayload(log.Fields{})
	env.SetHeader(&commonpb.Header{
		Fields: map[string]*commonpb.Payload{"logParam": encodedValue},
	})
	mockStorage := database.NewMockStorage(t)
	return env, mockStorage
}

// baseVolume returns a minimal expert-mode volume with all associations populated,
// which satisfies the Setup() precondition (non-nil Account).
func baseVolume() *datamodel.ExpertModeVolumes {
	return &datamodel.ExpertModeVolumes{
		BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
		Name:      "test-volume",
		AccountID: 1,
		PoolID:    1,
		State:     datamodel.LifeCycleStateCreating,
		Account: &datamodel.Account{
			BaseModel: datamodel.BaseModel{ID: 1},
			Name:      "test-account",
		},
		Pool: &datamodel.Pool{
			BaseModel: datamodel.BaseModel{ID: 1},
			VendorID:  "vendor-id",
			Network:   "test-network",
		},
		Svm: &datamodel.Svm{
			BaseModel: datamodel.BaseModel{ID: 2},
			Name:      "test-svm",
		},
	}
}

// baseParams returns params with a backup vault and region, no policy.
func baseParams() *commonparams.ManageBackupConfigForExpertModeVolumeParams {
	return &commonparams.ManageBackupConfigForExpertModeVolumeParams{
		AccountName:   "test-account",
		BackupVaultID: nillable.ToPointer("bv-uuid"),
		Region:        "us-east1",
	}
}

// standardBackupVault returns a non-GCBDR, non-cross-region backup vault.
func standardBackupVault() *datamodel.BackupVault {
	return &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		BackupVaultType: "IN_REGION",
		ServiceType:     "NETAPP",
	}
}

// populatedBucketDetails returns BucketDetails that indicate an existing bucket.
func populatedBucketDetails() *commonparams.BucketDetails {
	return &commonparams.BucketDetails{
		BucketName:          "existing-bucket",
		ServiceAccountName:  "sa@project.iam.gserviceaccount.com",
		TenantProjectNumber: "1234567890",
	}
}

// registerCoreActivities registers the job/auth activities that every test path needs.
func registerCoreActivities(env *testsuite.TestWorkflowEnvironment, mockStorage *database.MockStorage) (
	activities.CommonActivities,
	activities.VolumeUpdateActivity,
	expertmodeactivities.ExpertModeVolumeActivity,
) {
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.GetAuthJWTToken)
	env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB)
	return commonActivity, updateActivity, expertModeActivity
}

// mockStateRestore stubs the UpdateExpertModeVolumeStateInDB activity with .Maybe()
// so that the failure-path defer (err != nil) can fire without panicking.
// Call this in every test whose failure point is inside Run() (after the defer is set).
func mockStateRestore(env *testsuite.TestWorkflowEnvironment, expertModeActivity expertmodeactivities.ExpertModeVolumeActivity) {
	env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
}

// mockJobFlow stubs GetJob (returns NEW) and UpdateJob (returns nil) for the given
// number of UpdateJob calls expected.
func mockJobFlow(mockStorage *database.MockStorage, updateJobCalls int) {
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Times(updateJobCalls)
}

// ─────────────────────────────────────────────────────────────────────────────
// Success tests
// ─────────────────────────────────────────────────────────────────────────────

func TestManageBackupConfigWorkflow(t *testing.T) {
	t.Run("Success_ExistingBucket_StandardVault_PolicyExists", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)

		mockJobFlow(mockStorage, 2)

		// Step 1: auth token
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		// Step 2: vault exists
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		// Step 3: tenancy
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		// Step 4: existing bucket
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		// Step 6: policy exists
		volume := baseVolume()
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(true, nil)
		// Step 7: persist
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		// Step 8: restore volume state to READY on success
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NewBucket_StandardVault_NoPolicyID", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
		env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		// empty bucket triggers creation path
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "new-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{BucketName: "existing-bucket"}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "remote-bv"}}, nil)
		env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := baseParams() // no BackupPolicyID

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NewBucket_StandardVault_KmsGrantSet", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
		env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)

		mockJobFlow(mockStorage, 2)

		kmsGrant := "projects/p/cryptoKeyVersions/1"
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "new-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "remote-bv"}}, nil)
		env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := baseParams()
		params.KmsGrant = &kmsGrant

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NewBucket_GCBDRVault_PoolAvailable", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)
		env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
		env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)

		mockJobFlow(mockStorage, 2)

		gcbdrVault := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{UUID: "bv-uuid"},
			ServiceType: activities.GCBDRServiceType,
			BucketDetails: []*datamodel.BucketDetails{
				{TenantProjectNumber: "gcbdr-project"},
			},
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(gcbdrVault, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "gcbdr-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{BucketName: "gcbdr-bucket", ServiceAccountName: "sa", TenantProjectNumber: "proj"}, nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupVault{}, nil)
		env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := baseParams()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_CrossRegionVault_ExistingBucket_PolicyNotExists_PolicyEnabled", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)
		env.RegisterActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE)
		env.RegisterActivity(updateActivity2.CreateScheduleForBackupPolicy)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity)

		mockJobFlow(mockStorage, 2)

		backupRegion := "us-west1"
		crossRegionVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: &backupRegion,
			ServiceType:      "NETAPP",
		}
		enabledPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "bp-uuid"},
			PolicyEnabled: true,
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(crossRegionVault, nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(false, nil)
		env.OnActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).
			Return(enabledPolicy, nil)
		env.OnActivity(updateActivity2.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_CrossRegionVault_ExistingBucket_PolicyNotExists_PolicyDisabled", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)
		env.RegisterActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE)
		env.RegisterActivity(updateActivity2.CreateScheduleForBackupPolicy)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity)

		backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}
		env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)

		mockJobFlow(mockStorage, 2)

		backupRegion := "us-west1"
		crossRegionVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: &backupRegion,
			ServiceType:      "NETAPP",
		}
		disabledPolicy := &datamodel.BackupPolicy{
			BaseModel:     datamodel.BaseModel{UUID: "bp-uuid"},
			PolicyEnabled: false,
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(crossRegionVault, nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(false, nil)
		env.OnActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).
			Return(disabledPolicy, nil)
		env.OnActivity(updateActivity2.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_ScheduledBackupEnabled_NonNil_IsPersistedToBackupConfig", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := baseParams()
		enabled := true
		params.ScheduledBackupEnabled = &enabled

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_BackupConfigNilOnVolume_IsInitialized", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.BackupConfig = nil // explicitly nil; workflow must initialize it
		params := baseParams()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_QueryWorkflowStatus", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := baseParams()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		status, err := env.QueryWorkflowByID("default-test-workflow-id", "status")
		assert.NoError(tt, err)
		assert.NotNil(tt, status)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_BackupVaultIDNil_StepsSkipped_ConfigPreserved", func(tt *testing.T) {
		// BackupVaultID = nil → provisionVaultResources (steps 2-6) is not called.
		// BackupPolicyID is set, so hasBackupConfigPatch = true and step 7 persists
		// the policy without modifying the stored vault ID (BackupVaultID stays "").
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity := registerCoreActivities(env, mockStorage)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		// No OnActivity expectations for vault activities (steps 2-6) — any unexpected
		// call would panic the test environment, verifying they are not reached.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything,
			mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
				// vault ID must not have been modified (stays "")
				return v.BackupConfig != nil && v.BackupConfig.BackupVaultID == ""
			})).Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:    "test-account",
			BackupVaultID:  nil, // absent → steps 2-6 skipped
			BackupPolicyID: nillable.ToPointer("bp-uuid"),
			Region:         "us-east1",
		}

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("When_BackupVaultIDEmptyString_DetachPath_VaultCleared", func(tt *testing.T) {
		// BackupVaultID = &"" (explicit detach) → provisionVaultResources skipped.
		// BackupVaultID != nil so hasBackupConfigPatch = true; step 7 must set
		// volume.BackupConfig.BackupVaultID to "" (clearing the previous vault).
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity := registerCoreActivities(env, mockStorage)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		// Steps 2-6 must NOT be called.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything,
			mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
				// vault ID must have been cleared
				return v.BackupConfig != nil && v.BackupConfig.BackupVaultID == ""
			})).Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.BackupConfig = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			BackupVaultID: nillable.ToPointer(""), // explicit detach
			Region:        "us-east1",
		}

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Failure: early workflow scaffolding
	// ─────────────────────────────────────────────────────────────────────────

	t.Run("Failure_SetupWithNilAccount", func(tt *testing.T) {
		env, _ := newManageBackupConfigTestEnv(tt)

		volume := baseVolume()
		volume.Account = nil // causes panic in Setup()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("Failure_EnsureJobStateFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetJob)

		// Job is already PROCESSING, not NEW — EnsureJobState will reject it.
		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStatePROCESSING),
		}, nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateJobStatusToProcessingFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		// First UpdateJob (PROCESSING) fails; second (ERROR) may or may not be called.
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError).Once()
		mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil).Maybe()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Failure: Run() activity failures
	// ─────────────────────────────────────────────────────────────────────────

	t.Run("Failure_GetAuthJWTTokenFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity := registerCoreActivities(env, mockStorage)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("", assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_CheckBackupVaultExistInVCPFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_GCBDRVault_NoBucketDetails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		gcbdrVaultNoBuckets := &datamodel.BackupVault{
			BaseModel:     datamodel.BaseModel{UUID: "bv-uuid"},
			ServiceType:   activities.GCBDRServiceType,
			BucketDetails: []*datamodel.BucketDetails{}, // empty — no tenant project
		}
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(gcbdrVaultNoBuckets, nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_FindTenancyDetailsFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_CheckBucketResourceNameFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_GenerateResourceNamesForBackupVaultFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil) // empty → triggers creation path
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_CreateBucketForBackupVaultFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "new-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateBucketDetailsOfBackupVaultFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "new-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_GCBDRVault_NilPool", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		gcbdrVault := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{UUID: "bv-uuid"},
			ServiceType: activities.GCBDRServiceType,
			BucketDetails: []*datamodel.BucketDetails{
				{TenantProjectNumber: "gcbdr-project"},
			},
		}
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(gcbdrVault, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "gcbdr-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{BucketName: "gcbdr-bucket", ServiceAccountName: "sa", TenantProjectNumber: "proj"}, nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.Pool = nil // missing pool → GCBDR permissions check fails

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_SetupCrossProjectBackupPermissionsFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		gcbdrVault := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{UUID: "bv-uuid"},
			ServiceType: activities.GCBDRServiceType,
			BucketDetails: []*datamodel.BucketDetails{
				{TenantProjectNumber: "gcbdr-project"},
			},
		}
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(gcbdrVault, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "gcbdr-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{BucketName: "gcbdr-bucket", ServiceAccountName: "sa", TenantProjectNumber: "proj"}, nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_CheckOrCreateRemoteBackupVaultInVCPFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "new-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateRemoteBackupVaultWithBucketDetailsFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
		env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "new-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupVault{}, nil)
		env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_SetupCrossRegionBackupPermissionsFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		backupRegion := "us-west1"
		crossRegionVault := &datamodel.BackupVault{
			BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
			BackupVaultType:  activities.CrossRegionBackupType,
			BackupRegionName: &backupRegion,
			ServiceType:      "NETAPP",
		}
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(crossRegionVault, nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError)

		volume := baseVolume()
		params := baseParams()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_VerifyIfBackupPolicyExistsInVCPFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(false, assert.AnError)

		volume := baseVolume()
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_FetchAndCreateBackupPolicyFromSDEFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)
		env.RegisterActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(false, nil)
		env.OnActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		volume := baseVolume()
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_CreateScheduleForBackupPolicyFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)
		env.RegisterActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE)
		env.RegisterActivity(updateActivity2.CreateScheduleForBackupPolicy)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(false, nil)
		env.OnActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupPolicy{BaseModel: datamodel.BaseModel{UUID: "bp-uuid"}, PolicyEnabled: true}, nil)
		env.OnActivity(updateActivity2.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError)

		volume := baseVolume()
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_PauseBackupPolicyScheduleFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)
		env.RegisterActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE)
		env.RegisterActivity(updateActivity2.CreateScheduleForBackupPolicy)

		backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}
		env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(false, nil)
		env.OnActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupPolicy{
				BaseModel:     datamodel.BaseModel{UUID: "bp-uuid"},
				PolicyEnabled: false, // disabled → PauseBackupPolicySchedule is called
			}, nil)
		env.OnActivity(updateActivity2.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).
			Return(assert.AnError)

		volume := baseVolume()
		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateExpertModeVolumeBackupConfigInDBFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Vault switch tests
	// ─────────────────────────────────────────────────────────────────────────

	t.Run("VaultSwitch_Success_TeardownAndNewVaultSetup", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
		backupActivity := activities.BackupActivity{SE: mockStorage}
		env.RegisterActivity(backupActivity.GetBackupVault)
		env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
		env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		oldVault := &datamodel.BackupVault{
			BaseModel:       datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"},
			BackupVaultType: "IN_REGION",
			ServiceType:     "NETAPP",
		}
		latestBackup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Attributes: &datamodel.BackupAttributes{
				SnapshotID:          "snap-uuid",
				UseExistingSnapshot: false,
			},
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
			Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)
		env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").
			Return(oldVault, nil)
		env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, "ext-uuid", int64(10)).
			Return(latestBackup, nil)
		env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, "snap-uuid", "ext-uuid", false).
			Return(nil)
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.ExternalUUID = "ext-uuid"
		volume.BackupConfig = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
		volume.Pool.PoolCredentials = &datamodel.PoolCredentials{Password: "pass"}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			BackupVaultID: nillable.ToPointer("bv-uuid"),
			Region:        "us-east1",
		}

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_NoSwitch_SameVault_SkipsTeardown", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.BackupConfig = &datamodel.DataProtection{BackupVaultID: "bv-uuid"}
		params := baseParams()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_Failure_DeleteSnapmirrorFails_VolumeRestored", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity := registerCoreActivities(env, mockStorage)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
			Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, temporal.NewNonRetryableApplicationError("snapmirror delete failed", "DeleteError", nil))

		volume := baseVolume()
		volume.BackupConfig = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
		volume.Pool.PoolCredentials = &datamodel.PoolCredentials{Password: "pass"}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			BackupVaultID: nillable.ToPointer("bv-uuid"),
			Region:        "us-east1",
		}

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_Failure_DeleteSnapshotFails_VolumeRestored", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity := registerCoreActivities(env, mockStorage)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
		backupActivity := activities.BackupActivity{SE: mockStorage}
		env.RegisterActivity(backupActivity.GetBackupVault)
		env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
		env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		oldVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"},
		}
		latestBackup := &datamodel.Backup{
			BaseModel: datamodel.BaseModel{UUID: "backup-uuid"},
			Attributes: &datamodel.BackupAttributes{
				SnapshotID:          "snap-uuid",
				UseExistingSnapshot: false,
			},
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
			Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)
		env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").
			Return(oldVault, nil)
		env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, "ext-uuid", int64(10)).
			Return(latestBackup, nil)
		env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, "snap-uuid", "ext-uuid", false).
			Return(temporal.NewNonRetryableApplicationError("snapshot delete failed", "DeleteError", nil))

		volume := baseVolume()
		volume.ExternalUUID = "ext-uuid"
		volume.BackupConfig = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
		volume.Pool.PoolCredentials = &datamodel.PoolCredentials{Password: "pass"}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			BackupVaultID: nillable.ToPointer("bv-uuid"),
			Region:        "us-east1",
		}

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultSwitch_Success_NoLatestBackup_SkipsSnapshotDeletion", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
		backupActivity := activities.BackupActivity{SE: mockStorage}
		env.RegisterActivity(backupActivity.GetBackupVault)
		env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		oldVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"},
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(commonActivity.GetNode, mock.Anything, mock.Anything).
			Return([]*datamodel.Node{{EndpointAddress: "127.0.0.1"}}, nil)
		env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)
		env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").
			Return(oldVault, nil)
		env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, "ext-uuid", int64(10)).
			Return(nil, nil)
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.ExternalUUID = "ext-uuid"
		volume.BackupConfig = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
		volume.Pool.PoolCredentials = &datamodel.PoolCredentials{Password: "pass"}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			BackupVaultID: nillable.ToPointer("bv-uuid"),
			Region:        "us-east1",
		}

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

// TestManageBackupConfigWorkflow_StateManagement covers the volume-state lifecycle
// introduced by the "follow update-volume style" changes:
//   - step 8 (explicit AVAILABLE on success)
//   - defer (AVAILABLE on failure only)
func TestManageBackupConfigWorkflow_StateManagement(t *testing.T) {
	t.Run("Success_UpdateExpertModeVolumeStateInDB_CalledWithAVAILABLE", func(tt *testing.T) {
		// Verify that on the success path, UpdateExpertModeVolumeStateInDB is called
		// with the volume UUID and LifeCycleStateAvailable (step 8).
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		// Capture the arguments to assert UUID and state.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB,
			mock.Anything, "vol-uuid", datamodel.LifeCycleStateAvailable).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_UpdateExpertModeVolumeStateInDB_FailsOnSuccessPath", func(tt *testing.T) {
		// If UpdateExpertModeVolumeStateInDB (step 8) fails, the workflow should fail.
		// The failure defer then also fires, calling UpdateExpertModeVolumeStateInDB again.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		// Step 8 fails with a non-retryable error so no retry consumes the defer mock.
		nonRetryErr := temporal.NewNonRetryableApplicationError("state update failed", "StateUpdateError", nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nonRetryErr).Once()
		// Defer fires because err != nil; its call should succeed (error is swallowed).
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil).Maybe()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_DeferRestoresVolumeToAvailable_WhenActivityFails", func(tt *testing.T) {
		// When an activity fails mid-workflow, the defer (err != nil branch) must call
		// UpdateExpertModeVolumeStateInDB with AVAILABLE.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		// Step 7 fails → defer fires.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(assert.AnError)
		// Defer must call UpdateExpertModeVolumeStateInDB with READY.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB,
			mock.Anything, "vol-uuid", datamodel.LifeCycleStateAvailable).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure-function tests for buildVolumeFromExpertMode
// ─────────────────────────────────────────────────────────────────────────────

func TestBuildVolumeFromExpertMode(t *testing.T) {
	t.Run("When_BackupVaultIDNil_DefaultsToEmpty", func(tt *testing.T) {
		// When BackupVaultID is nil (not provided), nillable.GetString defaults to ""
		// so DataProtection.BackupVaultID must be the empty string, never a pointer address.
		em := &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: "vol-uuid"}}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			BackupVaultID: nil,
		}

		vol := buildVolumeFromExpertMode(em, params)

		assert.NotNil(tt, vol.DataProtection)
		assert.Equal(tt, "", vol.DataProtection.BackupVaultID)
	})

	t.Run("NilPool_EmptyVendorSubnetID", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{
			BaseModel:    datamodel.BaseModel{UUID: "vol-uuid", ID: 42},
			Name:         "vol",
			ExternalUUID: "ext-uuid",
			AccountID:    1,
			Account:      &datamodel.Account{BaseModel: datamodel.BaseModel{ID: 1}},
			Pool:         nil,
		}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			BackupVaultID:  nillable.ToPointer("bv"),
			BackupPolicyID: nillable.ToPointer("bp"),
		}

		vol := buildVolumeFromExpertMode(em, params)

		assert.Equal(tt, em.UUID, vol.UUID)
		assert.Equal(tt, em.ID, vol.ID)
		assert.Equal(tt, em.Name, vol.Name)
		assert.Equal(tt, em.AccountID, vol.AccountID)
		assert.Equal(tt, em.ExternalUUID, vol.VolumeAttributes.ExternalUUID)
		assert.Empty(tt, vol.VolumeAttributes.VendorSubnetID) // nil pool → no vendor subnet
		assert.Nil(tt, vol.Svm)
		assert.Equal(tt, "bv", vol.DataProtection.BackupVaultID)
		assert.Equal(tt, "bp", vol.DataProtection.BackupPolicyID)
		assert.Nil(tt, vol.DataProtection.ScheduledBackupEnabled)
	})

	t.Run("WithPool_VendorSubnetIDPopulated", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Name:      "vol",
			Pool: &datamodel.Pool{
				VendorID: "vendor-id",
				Network:  "subnet-abc",
			},
		}
		params := baseParams()

		vol := buildVolumeFromExpertMode(em, params)

		assert.Equal(tt, "subnet-abc", vol.VolumeAttributes.VendorSubnetID)
		assert.Equal(tt, em.Pool, vol.Pool)
	})

	t.Run("PoolWithEmptyVendorID_VendorSubnetIDNotSet", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Pool: &datamodel.Pool{
				VendorID: "", // empty → condition false → no VendorSubnetID
				Network:  "should-not-be-used",
			},
		}
		params := baseParams()

		vol := buildVolumeFromExpertMode(em, params)

		assert.Empty(tt, vol.VolumeAttributes.VendorSubnetID)
	})

	t.Run("WithSvm_SvmIDAndSvmSet", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Svm: &datamodel.Svm{
				BaseModel: datamodel.BaseModel{ID: 7},
				Name:      "test-svm",
			},
		}
		params := baseParams()

		vol := buildVolumeFromExpertMode(em, params)

		assert.Equal(tt, int64(7), vol.SvmID)
		assert.Equal(tt, em.Svm, vol.Svm)
	})

	t.Run("NilSvm_SvmIDZeroAndSvmNil", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "vol-uuid"},
			Svm:       nil,
		}
		params := baseParams()

		vol := buildVolumeFromExpertMode(em, params)

		assert.Equal(tt, int64(0), vol.SvmID)
		assert.Nil(tt, vol.Svm)
	})

	t.Run("ScheduledBackupEnabled_NonNil_PropagatedToDataProtection", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: "vol-uuid"}}
		enabled := true
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			BackupVaultID:          nillable.ToPointer("bv"),
			ScheduledBackupEnabled: &enabled,
		}

		vol := buildVolumeFromExpertMode(em, params)

		assert.NotNil(tt, vol.DataProtection.ScheduledBackupEnabled)
		assert.True(tt, *vol.DataProtection.ScheduledBackupEnabled)
	})

	t.Run("ScheduledBackupEnabled_Nil_NotSetInDataProtection", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: "vol-uuid"}}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			BackupVaultID:          nillable.ToPointer("bv"),
			ScheduledBackupEnabled: nil,
		}

		vol := buildVolumeFromExpertMode(em, params)

		assert.Nil(tt, vol.DataProtection.ScheduledBackupEnabled)
	})

	t.Run("KmsGrant_PropagatedToDataProtection", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{BaseModel: datamodel.BaseModel{UUID: "vol-uuid"}}
		kms := "projects/p/cryptoKeyVersions/1"
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			BackupVaultID: nillable.ToPointer("bv"),
			KmsGrant:      nillable.ToPointer(kms),
		}

		vol := buildVolumeFromExpertMode(em, params)

		assert.NotNil(tt, vol.DataProtection.KmsGrant)
		assert.Equal(tt, kms, *vol.DataProtection.KmsGrant)
	})

	t.Run("BaseModelFieldsCopied", func(tt *testing.T) {
		em := &datamodel.ExpertModeVolumes{
			BaseModel: datamodel.BaseModel{UUID: "u", ID: 99},
			AccountID: 5,
			PoolID:    3,
			State:     datamodel.LifeCycleStateAvailable,
		}
		params := baseParams()

		vol := buildVolumeFromExpertMode(em, params)

		assert.Equal(tt, em.UUID, vol.UUID)
		assert.Equal(tt, em.ID, vol.ID)
		assert.Equal(tt, em.AccountID, vol.AccountID)
		assert.Equal(tt, em.PoolID, vol.PoolID)
		assert.Equal(tt, em.State, vol.State)
	})
}
