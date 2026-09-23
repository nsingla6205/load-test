package expertMode

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	commonparams "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/nillable"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/activity"
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
			BaseModel:    datamodel.BaseModel{ID: 1},
			VendorID:     "vendor-id",
			Network:      "test-network",
			ServiceLevel: "FLEX",
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
	expertmodeactivities.ExpertModeBackupConfigActivity,
) {
	commonActivity := activities.CommonActivities{SE: mockStorage}
	updateActivity := activities.VolumeUpdateActivity{SE: mockStorage}
	expertModeActivity := expertmodeactivities.ExpertModeVolumeActivity{SE: mockStorage}
	backupConfigActivity := expertmodeactivities.ExpertModeBackupConfigActivity{}
	env.RegisterActivity(commonActivity.GetJob)
	env.RegisterActivity(commonActivity.UpdateJobStatus)
	env.RegisterActivity(commonActivity.GetAuthJWTToken)
	env.RegisterActivity(commonActivity.ResolveOntapNodeForPool)
	env.RegisterActivity(updateActivity.CheckBackupVaultExistInVCP)
	env.RegisterActivity(updateActivity.CheckBucketResourceName)
	env.RegisterActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB)
	return commonActivity, updateActivity, expertModeActivity, backupConfigActivity
}

// mockStateRestore stubs the UpdateExpertModeVolumeStateInDB activity with .Maybe()
// so that the failure-path defer (err != nil) can fire without panicking.
// Call this in every test whose failure point is inside Run() (after the defer is set).
func mockStateRestore(env *testsuite.TestWorkflowEnvironment, expertModeActivity expertmodeactivities.ExpertModeVolumeActivity) {
	env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
}

// stubRemoteBackupVaultActivities registers and stubs the remote-vault activities that run
// during new-bucket provisioning. Both stubs are .Maybe() so tests whose failure point
// comes earlier, or that attach to an already-provisioned bucket, are unaffected.
func stubRemoteBackupVaultActivities(env *testsuite.TestWorkflowEnvironment) {
	volumeCreateActivity := activities.VolumeCreateActivity{}
	env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
	env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)
	env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "remote-bv-uuid"}}, nil).Maybe()
	env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails,
		mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(expertModeBackupConfigVaultParityVersion)

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
		// Step 6: policy already exists in VCP – persist the ID; schedule work is deferred.
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
		heartbeatTimeout := captureActivityHeartbeatTimeout(env, "CheckBackupVaultExistInVCP")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		assert.Equal(tt, (3*time.Minute)/2, *heartbeatTimeout)
		env.AssertActivityNumberOfCalls(tt, "CheckOrCreateRemoteBackupVaultInVCP", 0)
		env.AssertActivityNumberOfCalls(tt, "UpdateRemoteBackupVaultWithBucketDetails", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NewBucket_StandardVault_NoPolicyID", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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

	t.Run("Success_NewBucket_DedicatedCapacityPool_ProvisionsSAAndHMAC", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := activities.ADCActivity{}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount)
		env.RegisterActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret)
		env.RegisterActivity(backupConfigActivity.StoreDedicatedCapacityBackupHmacSecret)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(adcActivity.IsServiceAccountCreated)
		env.RegisterActivity(adcActivity.AttachRolesToServiceAccount)
		env.RegisterActivity(adcActivity.CreateHmacKeys)

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
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{
				BucketName:       "dedicated-bucket",
				Email:            "sa@project.iam.gserviceaccount.com",
				ServiceAccountId: "bv-sa-id",
			}, nil)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything).
			Return(nil, nil)
		env.OnActivity(adcActivity.CreateHmacKeys, mock.Anything, mock.Anything).
			Return(&commonparams.HmacKeys{AccessKey: "YWNrZXk=", SecretKey: "c2VjcmV0"}, nil)
		env.OnActivity(backupConfigActivity.StoreDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{
				ServiceAccountName: "sa@project.iam.gserviceaccount.com",
				HmacSecretID:       "hardware-bv-hmac-bv-sa-id",
			}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{
				BucketName:          "dedicated-bucket",
				TenantProjectNumber: "project-123",
			}, nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{BucketName: "dedicated-bucket"}, nil)
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
		volume.Pool.ServiceLevel = "STANDARD"
		params := baseParams()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_DedicatedCapacityPool_ReusesExistingVaultHmacSecret", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := activities.ADCActivity{}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount)
		env.RegisterActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret)
		env.RegisterActivity(backupConfigActivity.StoreDedicatedCapacityBackupHmacSecret)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(adcActivity.IsServiceAccountCreated)
		env.RegisterActivity(adcActivity.AttachRolesToServiceAccount)
		env.RegisterActivity(adcActivity.CreateHmacKeys)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
		env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)

		mockJobFlow(mockStorage, 2)

		existingFlexBucket := &commonparams.BucketDetails{
			BucketName:          "shared-bucket",
			TenantProjectNumber: "project-123",
			VendorSubnetID:      "subnet-1",
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(existingFlexBucket, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{
				BucketName:       "shared-bucket",
				Email:            "sa@project.iam.gserviceaccount.com",
				ServiceAccountId: "bv-sa-id",
			}, nil)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{
				ServiceAccountName: "sa@project.iam.gserviceaccount.com",
				HmacSecretID:       "hardware-bv-hmac-bv-sa-id",
			}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{
				BucketName:          "shared-bucket",
				TenantProjectNumber: "project-123",
			}, nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{BucketName: "shared-bucket"}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.MatchedBy(func(bd *commonparams.BucketDetails) bool {
			return bd != nil &&
				bd.BucketName == "shared-bucket" &&
				bd.ServiceAccountName == "sa@project.iam.gserviceaccount.com" &&
				bd.HmacSecretID == "hardware-bv-hmac-bv-sa-id"
		})).Return(nil)
		env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "remote-bv"}}, nil)
		env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.Pool.ServiceLevel = "STANDARD"
		volume.Pool.Network = "subnet-1"
		params := baseParams()

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_NewBucket_StandardVault_KmsGrantSet", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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

	t.Run("Success_ExistingBucket_GCBDRVault_RemoteVaultAndCrossProjectPermissionsStillRun", func(tt *testing.T) {
		// Regular volumes also re-sync the remote vault on an existing-bucket attach.
		// ONTAP mode still skips that; only the GCBDR pool-SA grant is re-applied here.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(expertModeBackupConfigVaultParityVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP)
		env.RegisterActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails)
		env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)

		mockJobFlow(mockStorage, 2)

		gcbdrVault := &datamodel.BackupVault{
			BaseModel:   datamodel.BaseModel{UUID: "bv-uuid"},
			ServiceType: activities.GCBDRServiceType,
			BucketDetails: []*datamodel.BucketDetails{
				{TenantProjectNumber: "gcbdr-project"},
			},
		}
		isExistingBucket := func(bucketDetails *commonparams.BucketDetails) bool {
			return bucketDetails != nil && bucketDetails.BucketName == "existing-bucket"
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(gcbdrVault, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		// Stubbed but must never run: the bucket already exists.
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil).Maybe()
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil).Maybe()
		env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything,
			mock.MatchedBy(func(pool *datamodel.Pool) bool { return pool != nil && pool.VendorID == "vendor-id" }),
			mock.MatchedBy(isExistingBucket)).
			Return(nil).Once()
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "CheckOrCreateRemoteBackupVaultInVCP", 0)
		env.AssertActivityNumberOfCalls(tt, "UpdateRemoteBackupVaultWithBucketDetails", 0)
		env.AssertActivityNumberOfCalls(tt, "SetupCrossProjectBackupPermissions", 1)
		env.AssertActivityNumberOfCalls(tt, "CreateBucketForBackupVault", 0)
		env.AssertActivityNumberOfCalls(tt, "UpdateBucketDetailsOfBackupVault", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_CrossRegionVault_ExistingBucket_PolicyNotExists_PolicyEnabled", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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

	t.Run("Success_PolicyExists_LeavesScheduleUntouched", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)
		env.RegisterActivity(updateActivity2.FetchAndCreateBackupPolicyFromSDE)
		env.RegisterActivity(updateActivity2.CreateScheduleForBackupPolicy)

		backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}
		env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
		env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
		env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)
		env.RegisterActivity(backupPolicyActivity.UnpauseBackupPolicySchedule)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, "bp-uuid", int64(1)).
			Return(true, nil)
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
		env.AssertActivityNumberOfCalls(tt, "GetBackupPolicyByUUIDAndAccountID", 0)
		env.AssertActivityNumberOfCalls(tt, "CheckIfBackupPolicyScheduleExists", 0)
		env.AssertActivityNumberOfCalls(tt, "CreateScheduleForBackupPolicy", 0)
		env.AssertActivityNumberOfCalls(tt, "PauseBackupPolicySchedule", 0)
		env.AssertActivityNumberOfCalls(tt, "UnpauseBackupPolicySchedule", 0)
		env.AssertActivityNumberOfCalls(tt, "FetchAndCreateBackupPolicyFromSDE", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Success_ScheduledBackupEnabled_NonNil_IsPersistedToBackupConfig", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
		backupActivity := activities.BackupActivity{SE: mockStorage}
		env.RegisterActivity(backupActivity.GetBackupVault)
		env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
		env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)
		env.RegisterActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity)

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
		env.OnActivity(commonActivity.ResolveOntapNodeForPool, mock.Anything, mock.Anything).
			Return(&models.Node{EndpointAddress: "127.0.0.1"}, nil)
		env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)
		env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").
			Return(oldVault, nil)
		env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, "ext-uuid", int64(10)).
			Return(latestBackup, nil)
		env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, "snap-uuid", "ext-uuid", false).
			Return(nil)
		env.OnActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, mock.Anything, "ext-uuid").
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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(commonActivity.ResolveOntapNodeForPool, mock.Anything, mock.Anything).
			Return(&models.Node{EndpointAddress: "127.0.0.1"}, nil)
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
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

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
		env.OnActivity(commonActivity.ResolveOntapNodeForPool, mock.Anything, mock.Anything).
			Return(&models.Node{EndpointAddress: "127.0.0.1"}, nil)
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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
		backupActivity := activities.BackupActivity{SE: mockStorage}
		env.RegisterActivity(backupActivity.GetBackupVault)
		env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
		env.RegisterActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)

		mockJobFlow(mockStorage, 2)

		oldVault := &datamodel.BackupVault{
			BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"},
		}

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(commonActivity.ResolveOntapNodeForPool, mock.Anything, mock.Anything).
			Return(&models.Node{EndpointAddress: "127.0.0.1"}, nil)
		env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)
		env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").
			Return(oldVault, nil)
		env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, "ext-uuid", int64(10)).
			Return(nil, nil)
		env.OnActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, mock.Anything, "ext-uuid").
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

	// ─────────────────────────────────────────────────────────────────────────
	// Explicit detach (leaving a vault without attaching a new one)
	// ─────────────────────────────────────────────────────────────────────────

	t.Run("VaultChange_ExplicitDetach_TeardownAndSkipsProvisioning", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		// Detach teardown only exists from version 1 onwards.
		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(expertModeBackupConfigVaultParityVersion)

		deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
		env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
		backupActivity := activities.BackupActivity{SE: mockStorage}
		env.RegisterActivity(backupActivity.GetBackupVault)
		env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
		env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)
		env.RegisterActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity)

		mockJobFlow(mockStorage, 2)

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
		env.OnActivity(commonActivity.ResolveOntapNodeForPool, mock.Anything, mock.Anything).
			Return(&models.Node{EndpointAddress: "127.0.0.1"}, nil)
		env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, nil)
		env.OnActivity(backupActivity.GetBackupVault, mock.Anything, "old-vault-uuid").
			Return(oldVault, nil)
		env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, "ext-uuid", int64(10)).
			Return(latestBackup, nil)
		env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, "snap-uuid", "ext-uuid", false).
			Return(nil)
		env.OnActivity(backupActivity.RecalculateAndUpdateVolumeBackupChainBytesActivity, mock.Anything, "ext-uuid").
			Return(nil)
		// No expectation for CheckBackupVaultExistInVCP: an explicit detach must not
		// enter new-vault provisioning. Any call would fail the test.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything,
			mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
				return v.BackupConfig != nil && v.BackupConfig.BackupVaultID == "" &&
					v.BackupConfig.KmsGrant != nil &&
					*v.BackupConfig.KmsGrant == "projects/p/locations/l/keyRings/kr/cryptoKeys/k"
			})).Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.ExternalUUID = "ext-uuid"
		volume.BackupConfig = &datamodel.DataProtection{
			BackupVaultID: "old-vault-uuid",
			KmsGrant:      nillable.ToPointer("projects/p/locations/l/keyRings/kr/cryptoKeys/k"),
		}
		volume.Pool.PoolCredentials = &datamodel.PoolCredentials{Password: "pass"}
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

	t.Run("VaultChange_ExplicitDetach_FlagDisabled_SkipsTeardown", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(false)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(expertModeBackupConfigVaultParityVersion)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		// No teardown expectations: the feature flag gates the SnapMirror teardown.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything,
			mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
				return v.BackupConfig != nil && v.BackupConfig.BackupVaultID == "" &&
					v.BackupConfig.KmsGrant != nil &&
					*v.BackupConfig.KmsGrant == "projects/p/locations/l/keyRings/kr/cryptoKeys/k"
			})).Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.ExternalUUID = "ext-uuid"
		volume.BackupConfig = &datamodel.DataProtection{
			BackupVaultID: "old-vault-uuid",
			KmsGrant:      nillable.ToPointer("projects/p/locations/l/keyRings/kr/cryptoKeys/k"),
		}
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			BackupVaultID: nillable.ToPointer(""),
			Region:        "us-east1",
		}

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("VaultChange_ExplicitDetach_NoExistingVault_SkipsTeardown", func(tt *testing.T) {
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(expertModeBackupConfigVaultParityVersion)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		// Nothing to tear down when no vault was attached.
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything,
			mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
				return v.BackupConfig != nil && v.BackupConfig.BackupVaultID == "" && v.BackupConfig.KmsGrant == nil
			})).Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := baseVolume()
		volume.ExternalUUID = "ext-uuid"
		params := &commonparams.ManageBackupConfigForExpertModeVolumeParams{
			AccountName:   "test-account",
			BackupVaultID: nillable.ToPointer(""),
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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

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

func dedicatedCapacityPool() *datamodel.Pool {
	return &datamodel.Pool{
		BaseModel:    datamodel.BaseModel{ID: 1},
		VendorID:     "vendor-id",
		Network:      "test-network",
		ServiceLevel: "STANDARD",
	}
}

func dedicatedCapacityVolumeForAttach() *datamodel.ExpertModeVolumes {
	volume := baseVolume()
	volume.Pool.ServiceLevel = "STANDARD"
	return volume
}

// registerDedicatedCapacityActivities registers the dedicated-capacity provisioning activities
// and leaves GetDedicatedCapacityBackupHmacSecret unstubbed, so a test can control its result.
func registerDedicatedCapacityActivities(
	env *testsuite.TestWorkflowEnvironment,
	backupConfigActivity expertmodeactivities.ExpertModeBackupConfigActivity,
	updateActivity2 activities.VolumeUpdateActivity,
) activities.ADCActivity {
	adcActivity := activities.ADCActivity{}
	env.RegisterActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount)
	env.RegisterActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret)
	env.RegisterActivity(backupConfigActivity.StoreDedicatedCapacityBackupHmacSecret)
	env.RegisterActivity(updateActivity2.FindTenancyDetails)
	env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
	env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
	env.RegisterActivity(adcActivity.IsServiceAccountCreated)
	env.RegisterActivity(adcActivity.AttachRolesToServiceAccount)
	env.RegisterActivity(adcActivity.CreateHmacKeys)
	stubRemoteBackupVaultActivities(env)
	return adcActivity
}

func registerDedicatedCapacityProvisioningActivities(
	env *testsuite.TestWorkflowEnvironment,
	backupConfigActivity expertmodeactivities.ExpertModeBackupConfigActivity,
	updateActivity2 activities.VolumeUpdateActivity,
) activities.ADCActivity {
	adcActivity := registerDedicatedCapacityActivities(env, backupConfigActivity, updateActivity2)
	// No secret stored yet, so the provisioning path creates HMAC keys.
	env.OnActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything).
		Return(nil, nil).Maybe()
	return adcActivity
}

func mockDedicatedCapacityProvisioningPrereqs(
	env *testsuite.TestWorkflowEnvironment,
	commonActivity activities.CommonActivities,
	updateActivity activities.VolumeUpdateActivity,
	updateActivity2 activities.VolumeUpdateActivity,
) {
	env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
		Return("test-token", nil).Maybe()
	env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
		Return(standardBackupVault(), nil)
	env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
	env.OnActivity(updateActivity2.CheckBucketResourceName, mock.Anything, mock.Anything).
		Return(&commonparams.BucketDetails{}, nil)
	env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(&commonparams.ResourceNames{
			BucketName:       "dedicated-bucket",
			Email:            "sa@project.iam.gserviceaccount.com",
			ServiceAccountId: "bv-sa-id",
		}, nil)
}

func TestNeedsExpertModeBackupBucketProvisioning(t *testing.T) {
	dedicatedPool := dedicatedCapacityPool()
	flexPool := &datamodel.Pool{ServiceLevel: "FLEX"}

	t.Run("nil bucket always provisions", func(t *testing.T) {
		assert.True(t, needsExpertModeBackupBucketProvisioning(nil, flexPool))
		assert.True(t, needsExpertModeBackupBucketProvisioning(nil, dedicatedPool))
	})

	t.Run("dedicated pool provisions when any credential field is missing", func(t *testing.T) {
		full := &commonparams.BucketDetails{
			BucketName:          "bucket",
			ServiceAccountName:  "sa@project.iam",
			TenantProjectNumber: "123",
			HmacSecretID:        "hardware-bv-hmac-sa",
		}
		assert.False(t, needsExpertModeBackupBucketProvisioning(full, dedicatedPool))

		missingHMAC := *full
		missingHMAC.HmacSecretID = ""
		assert.True(t, needsExpertModeBackupBucketProvisioning(&missingHMAC, dedicatedPool))
	})

	t.Run("flex pool provisions only when all core fields are empty", func(t *testing.T) {
		partial := &commonparams.BucketDetails{BucketName: "bucket-only"}
		assert.False(t, needsExpertModeBackupBucketProvisioning(partial, flexPool))

		empty := &commonparams.BucketDetails{}
		assert.True(t, needsExpertModeBackupBucketProvisioning(empty, flexPool))
	})
}

func TestIsDedicatedCapacityExpertModePool(t *testing.T) {
	assert.False(t, isDedicatedCapacityExpertModePool(nil))
	assert.False(t, isDedicatedCapacityExpertModePool(&datamodel.Pool{ServiceLevel: "FLEX"}))
	assert.True(t, isDedicatedCapacityExpertModePool(dedicatedCapacityPool()))
}

func TestServiceAccountActivityContext_Success(t *testing.T) {
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		saCtx, err := serviceAccountActivityContext(ctx)
		require.NoError(t, err)
		require.NotNil(t, saCtx)
		return nil
	})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

func TestServiceAccountActivityContext_InvalidRetryConfiguration(t *testing.T) {
	tests := []struct {
		name string
		key  string
		val  string
	}{
		{name: "invalid start to close timeout", key: "SA_RETRY_START_TO_CLOSE_TIMEOUT", val: "not-a-duration"},
		{name: "invalid initial interval", key: "SA_RETRY_INITIAL_INTERVAL", val: "not-a-duration"},
		{name: "invalid maximum interval", key: "SA_RETRY_MAXIMUM_INTERVAL", val: "not-a-duration"},
		{name: "invalid maximum attempts", key: "SA_RETRY_MAXIMUM_ATTEMPTS", val: "not-a-number"},
		{name: "invalid backoff coefficient", key: "SA_RETRY_BACKOFF_COEFFICIENT", val: "not-a-float"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(tt.key, tt.val)

			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			env.SetWorkflowRunTimeout(5 * time.Second)

			env.ExecuteWorkflow(func(ctx workflow.Context) error {
				_, err := serviceAccountActivityContext(ctx)
				assert.Error(t, err)
				return err
			})

			assert.True(t, env.IsWorkflowCompleted())
			assert.Error(t, env.GetWorkflowError())
		})
	}
}

func TestManageBackupConfigWorkflow_DedicatedCapacityProvisioningFailures(t *testing.T) {
	t.Run("Failure_CreateDedicatedCapacityBackupServiceAccountFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		registerDedicatedCapacityProvisioningActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("Failure_IsServiceAccountCreatedCheckFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityProvisioningActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(false, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("Failure_ServiceAccountNotCreated", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityProvisioningActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(false, nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		assert.Contains(tt, env.GetWorkflowError().Error(), "service account sa@project.iam.gserviceaccount.com is not created")
		env.AssertExpectations(tt)
	})

	t.Run("Failure_AttachRolesToServiceAccountFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityProvisioningActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("Failure_GetDedicatedCapacityBackupHmacSecretFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "CreateHmacKeys", 0)
		env.AssertExpectations(tt)
	})

	t.Run("Failure_CreateHmacKeysFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityProvisioningActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(adcActivity.CreateHmacKeys, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("Failure_StoreDedicatedCapacityBackupHmacSecretFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityProvisioningActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(adcActivity.CreateHmacKeys, mock.Anything, mock.Anything).
			Return(&commonparams.HmacKeys{AccessKey: "YWNrZXk=", SecretKey: "c2VjcmV0"}, nil)
		env.OnActivity(backupConfigActivity.StoreDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})

	t.Run("Failure_CreateBucketForDedicatedCapacityFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityProvisioningActivities(env, backupConfigActivity, updateActivity2)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(adcActivity.CreateHmacKeys, mock.Anything, mock.Anything).
			Return(&commonparams.HmacKeys{AccessKey: "YWNrZXk=", SecretKey: "c2VjcmV0"}, nil)
		env.OnActivity(backupConfigActivity.StoreDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{
				ServiceAccountName: "sa@project.iam.gserviceaccount.com",
				HmacSecretID:       "hardware-bv-hmac-bv-sa-id",
			}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil, fmt.Errorf("bucket create failed"))

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Workflow versioning
//
// Histories started before the backup-vault parity release replay under
// workflow.DefaultVersion and must see the exact activity graph that was
// deployed at the time. Version 1 carries all parity behaviour.
// ─────────────────────────────────────────────────────────────────────────────

// vaultTeardownActivityNames are the activities that only run when a volume leaves
// the vault it is currently attached to.
var vaultTeardownActivityNames = []string{
	"ResolveOntapNodeForPool",
	"DeleteSnapmirrorInONTAP",
	"GetBackupVault",
	"GetLatestBackupByVolumeAndVault",
	"DeleteSnapshotForBackup",
}

// stubVaultTeardownActivities registers the SnapMirror teardown activities with permissive
// stubs so a test can assert on whether the workflow scheduled them at all.
func stubVaultTeardownActivities(env *testsuite.TestWorkflowEnvironment, mockStorage *database.MockStorage) {
	commonActivity := activities.CommonActivities{SE: mockStorage}
	deleteActivity := activities.VolumeDeleteActivity{SE: mockStorage}
	backupActivity := activities.BackupActivity{SE: mockStorage}
	env.RegisterActivity(deleteActivity.DeleteSnapmirrorInONTAP)
	env.RegisterActivity(backupActivity.GetBackupVault)
	env.RegisterActivity(backupActivity.GetLatestBackupByVolumeAndVault)
	env.RegisterActivity(backupActivity.DeleteSnapshotForBackup)

	env.OnActivity(commonActivity.ResolveOntapNodeForPool, mock.Anything, mock.Anything).
		Return(&models.Node{EndpointAddress: "127.0.0.1"}, nil).Maybe()
	env.OnActivity(deleteActivity.DeleteSnapmirrorInONTAP, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Maybe()
	env.OnActivity(backupActivity.GetBackupVault, mock.Anything, mock.Anything).
		Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{ID: 10, UUID: "old-vault-uuid"}}, nil).Maybe()
	env.OnActivity(backupActivity.GetLatestBackupByVolumeAndVault, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil).Maybe()
	env.OnActivity(backupActivity.DeleteSnapshotForBackup, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
}

func assertNoVaultTeardown(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	for _, name := range vaultTeardownActivityNames {
		env.AssertActivityNumberOfCalls(t, name, 0)
	}
}

// recordActivityOrder captures the order in which activities start, so a test can
// assert on the relative position of two activities in the graph.
func recordActivityOrder(env *testsuite.TestWorkflowEnvironment) *[]string {
	order := make([]string, 0, 16)
	env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		order = append(order, info.ActivityType.Name)
	})
	return &order
}

// mockGCBDRBucketProvisioning stubs the attach path for a GCBDR vault whose bucket is not
// provisioned yet, up to but excluding the cross-project IAM grant.
func mockGCBDRBucketProvisioning(
	env *testsuite.TestWorkflowEnvironment,
	commonActivity activities.CommonActivities,
	updateActivity activities.VolumeUpdateActivity,
	updateActivity2 activities.VolumeUpdateActivity,
	syncActivity *backgroundactivities.SyncBackupZiZsActivity,
) {
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
}

// volumeWithAttachedVault returns a volume already attached to "old-vault-uuid".
func volumeWithAttachedVault() *datamodel.ExpertModeVolumes {
	volume := baseVolume()
	volume.ExternalUUID = "ext-uuid"
	volume.BackupConfig = &datamodel.DataProtection{BackupVaultID: "old-vault-uuid"}
	volume.Pool.PoolCredentials = &datamodel.PoolCredentials{Password: "pass"}
	return volume
}

func detachParams() *commonparams.ManageBackupConfigForExpertModeVolumeParams {
	return &commonparams.ManageBackupConfigForExpertModeVolumeParams{
		AccountName:   "test-account",
		BackupVaultID: nillable.ToPointer(""),
		Region:        "us-east1",
	}
}

func TestManageBackupConfigWorkflow_Versioning(t *testing.T) {
	t.Run("DefaultVersion_ExplicitDetach_SkipsTeardown", func(tt *testing.T) {
		// Historical graph: a detach (&"") never tore down SnapMirror, even with the
		// feature flag on. Replaying such a history must not schedule new activities.
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubVaultTeardownActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volumeWithAttachedVault(), detachParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		assertNoVaultTeardown(tt, env)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_ExplicitDetach_NilKmsGrantParam_PreservesExistingGrant", func(tt *testing.T) {
		// Historical patch: a detach with params.KmsGrant == nil left the persisted
		// grant untouched. Replaying that history must send the original grant in
		// the UpdateExpertModeVolumeBackupConfigInDB payload.
		const existingGrant = "projects/p/locations/l/keyRings/kr/cryptoKeys/k"

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything,
			mock.MatchedBy(func(v *datamodel.ExpertModeVolumes) bool {
				return v.BackupConfig != nil &&
					v.BackupConfig.BackupVaultID == "" &&
					v.BackupConfig.KmsGrant != nil &&
					*v.BackupConfig.KmsGrant == existingGrant
			})).Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		volume := volumeWithAttachedVault()
		volume.BackupConfig.KmsGrant = nillable.ToPointer(existingGrant)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, detachParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_VaultSwitch_StillTearsDown", func(tt *testing.T) {
		// Switching between two real vaults is historical behaviour and must be preserved,
		// including reading the feature flag straight off the global (no new side effect).
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubVaultTeardownActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)
		// A history this old necessarily predates the chain-bytes-recalc changeID too.
		env.OnGetVersion(expertModeBackupConfigChainBytesRecalcChangeID,
			workflow.DefaultVersion, expertModeBackupConfigChainBytesRecalcVersion).
			Return(workflow.DefaultVersion)
		env.OnSideEffect().Return(true).Maybe()

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volumeWithAttachedVault(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "ResolveOntapNodeForPool", 1)
		env.AssertActivityNumberOfCalls(tt, "DeleteSnapmirrorInONTAP", 1)
		env.AssertActivityNumberOfCalls(tt, "GetBackupVault", 1)
		env.AssertActivityNumberOfCalls(tt, "GetLatestBackupByVolumeAndVault", 1)
		// The flag stays a plain global read on the historical path.
		env.AssertWorkflowNumberOfCalls(tt, "workflow.SideEffect", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_ExistingBucket_SkipsRemoteVaultActivities", func(tt *testing.T) {
		// Historically the remote-vault activities only ran inside new-bucket provisioning.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "CheckOrCreateRemoteBackupVaultInVCP", 0)
		env.AssertActivityNumberOfCalls(tt, "UpdateRemoteBackupVaultWithBucketDetails", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_NewBucket_RemoteVaultRunsAfterBucketProvisioning", func(tt *testing.T) {
		// Historical order inside new-bucket provisioning:
		// UpdateBucketDetailsOfBackupVault → CheckOrCreateRemoteBackupVaultInVCP (with the
		// resolved bucket details) → UpdateRemoteBackupVaultWithBucketDetails.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
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
		env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(&commonparams.BucketDetails{}, nil)
		env.OnActivity(updateActivity2.GenerateResourceNamesForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.ResourceNames{BucketName: "new-bucket"}, nil)
		env.OnActivity(updateActivity2.CreateBucketForBackupVault, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(syncActivity.SyncBucketDetails, mock.Anything, mock.Anything).
			Return(&datamodel.BucketDetails{BucketName: "existing-bucket"}, nil)
		env.OnActivity(updateActivity2.UpdateBucketDetailsOfBackupVault, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		// Historically the remote vault was created with the bucket details already known.
		env.OnActivity(volumeCreateActivity.CheckOrCreateRemoteBackupVaultInVCP,
			mock.Anything, mock.Anything, mock.Anything,
			mock.MatchedBy(func(bucketDetails *commonparams.BucketDetails) bool {
				return bucketDetails != nil && bucketDetails.BucketName == "existing-bucket"
			})).
			Return(&datamodel.BackupVault{BaseModel: datamodel.BaseModel{UUID: "remote-bv"}}, nil).Once()
		env.OnActivity(volumeCreateActivity.UpdateRemoteBackupVaultWithBucketDetails,
			mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil).Once()
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		order := recordActivityOrder(env)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		bucketUpdated := slices.Index(*order, "UpdateBucketDetailsOfBackupVault")
		remoteCreated := slices.Index(*order, "CheckOrCreateRemoteBackupVaultInVCP")
		remoteUpdated := slices.Index(*order, "UpdateRemoteBackupVaultWithBucketDetails")
		require.NotEqual(tt, -1, bucketUpdated)
		require.NotEqual(tt, -1, remoteCreated)
		require.NotEqual(tt, -1, remoteUpdated)
		assert.Less(tt, bucketUpdated, remoteCreated, "remote vault must be created after bucket provisioning")
		assert.Less(tt, remoteCreated, remoteUpdated)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_GCBDRVault_ExistingBucket_SkipsCrossProjectPermissions", func(tt *testing.T) {
		// Historically the GCBDR IAM grant only ran inside new-bucket provisioning.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)

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
		env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).
			Return(nil).Maybe()
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "SetupCrossProjectBackupPermissions", 0)
		env.AssertActivityNumberOfCalls(tt, "CheckOrCreateRemoteBackupVaultInVCP", 0)
		env.AssertActivityNumberOfCalls(tt, "UpdateRemoteBackupVaultWithBucketDetails", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_PolicyAlreadyExists_IsNoOp", func(tt *testing.T) {
		// Historically an already-existing backup policy was left untouched.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)
		env.RegisterActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP)
		env.RegisterActivity(updateActivity2.CreateScheduleForBackupPolicy)

		backupPolicyActivity := activities.BackupPolicyActivity{SE: mockStorage}
		env.RegisterActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID)
		env.RegisterActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists)
		env.RegisterActivity(backupPolicyActivity.PauseBackupPolicySchedule)
		env.RegisterActivity(backupPolicyActivity.UnpauseBackupPolicySchedule)

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(updateActivity.CheckBackupVaultExistInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(standardBackupVault(), nil)
		env.OnActivity(updateActivity2.FindTenancyDetails, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(&commonparams.TenancyInfo{RegionalTenantProject: "project-123"}, nil)
		env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(updateActivity2.VerifyIfBackupPolicyExistsInVCP, mock.Anything, mock.Anything, mock.Anything).
			Return(true, nil)
		// Stubbed but must never run on the historical graph.
		env.OnActivity(backupPolicyActivity.GetBackupPolicyByUUIDAndAccountID, mock.Anything, mock.Anything, mock.Anything).
			Return(&datamodel.BackupPolicy{BaseModel: datamodel.BaseModel{UUID: "vcp-bp-uuid"}, PolicyEnabled: true}, nil).Maybe()
		env.OnActivity(backupPolicyActivity.CheckIfBackupPolicyScheduleExists, mock.Anything, mock.Anything).
			Return(true, nil).Maybe()
		env.OnActivity(updateActivity2.CreateScheduleForBackupPolicy, mock.Anything, mock.Anything, mock.Anything).
			Return(nil).Maybe()
		env.OnActivity(backupPolicyActivity.PauseBackupPolicySchedule, mock.Anything, mock.Anything).
			Return(nil).Maybe()
		env.OnActivity(backupPolicyActivity.UnpauseBackupPolicySchedule, mock.Anything, mock.Anything).
			Return(nil).Maybe()
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		params := baseParams()
		params.BackupPolicyID = nillable.ToPointer("bp-uuid")

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), params)

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "GetBackupPolicyByUUIDAndAccountID", 0)
		env.AssertActivityNumberOfCalls(tt, "CheckIfBackupPolicyScheduleExists", 0)
		env.AssertActivityNumberOfCalls(tt, "CreateScheduleForBackupPolicy", 0)
		env.AssertActivityNumberOfCalls(tt, "PauseBackupPolicySchedule", 0)
		env.AssertActivityNumberOfCalls(tt, "UnpauseBackupPolicySchedule", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Version1_ExistingBucket_DoesNotSyncRemoteVault", func(tt *testing.T) {
		// Regular volumes always-run CheckOrCreateRemoteBackupVaultInVCP on attach.
		// ONTAP mode still only syncs the remote vault during new-bucket provisioning;
		// that always-run path is deferred from this PR.
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(expertModeBackupConfigVaultParityVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)

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
		env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "CheckOrCreateRemoteBackupVaultInVCP", 0)
		env.AssertActivityNumberOfCalls(tt, "UpdateRemoteBackupVaultWithBucketDetails", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_NewBucket_GCBDRVault_GrantsPoolAccessBeforeRemoteVault", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)
		stubRemoteBackupVaultActivities(env)

		mockJobFlow(mockStorage, 2)
		mockGCBDRBucketProvisioning(env, commonActivity, updateActivity, updateActivity2, syncActivity)
		env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		activityOrder := recordActivityOrder(env)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		// The historical graph granted bucket access before creating the remote vault.
		grantIndex := slices.Index(*activityOrder, "SetupCrossProjectBackupPermissions")
		remoteVaultIndex := slices.Index(*activityOrder, "CheckOrCreateRemoteBackupVaultInVCP")
		assert.NotEqual(tt, -1, grantIndex)
		assert.Less(tt, grantIndex, remoteVaultIndex)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_NewBucket_GCBDRVault_NilPool", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)
		stubRemoteBackupVaultActivities(env)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockGCBDRBucketProvisioning(env, commonActivity, updateActivity, updateActivity2, syncActivity)

		volume := baseVolume()
		volume.Pool = nil // missing pool → GCBDR permissions check fails

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volume, baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "CheckOrCreateRemoteBackupVaultInVCP", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DefaultVersion_NewBucket_GCBDRVault_SetupCrossProjectBackupPermissionsFails", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(workflow.DefaultVersion)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.CheckBucketResourceName)
		env.RegisterActivity(updateActivity2.GenerateResourceNamesForBackupVault)
		env.RegisterActivity(updateActivity2.CreateBucketForBackupVault)
		env.RegisterActivity(updateActivity2.UpdateBucketDetailsOfBackupVault)

		syncActivity := &backgroundactivities.SyncBackupZiZsActivity{}
		env.RegisterActivity(syncActivity.SyncBucketDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions)
		stubRemoteBackupVaultActivities(env)

		mockJobFlow(mockStorage, 2)
		mockStateRestore(env, expertModeActivity)
		mockGCBDRBucketProvisioning(env, commonActivity, updateActivity, updateActivity2, syncActivity)
		env.OnActivity(volumeCreateActivity.SetupCrossProjectBackupPermissions, mock.Anything, mock.Anything, mock.Anything).
			Return(assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "CheckOrCreateRemoteBackupVaultInVCP", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Version1_VaultSwitchingFlagCapturedBySideEffect", func(tt *testing.T) {
		// The feature flag is a process global, so version 1 pins it with a side effect at
		// workflow start. A replay that recorded "false" must skip teardown even though the
		// worker now reads "true" from the global.
		orig := utils.EnableBackupVaultSwitching
		utils.SetEnableBackupVaultSwitchingForTest(true)
		defer utils.SetEnableBackupVaultSwitchingForTest(orig)

		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubVaultTeardownActivities(env, mockStorage)

		env.OnGetVersion(expertModeBackupConfigVaultParityChangeID,
			workflow.DefaultVersion, expertModeBackupConfigVaultParityVersion).
			Return(expertModeBackupConfigVaultParityVersion)
		env.OnSideEffect().Return(false).Once()

		mockJobFlow(mockStorage, 2)

		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("test-token", nil).Maybe()
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeStateInDB, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, volumeWithAttachedVault(), detachParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
		assertNoVaultTeardown(tt, env)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Job status bookkeeping
//
// The workflow logs and moves on when it cannot record the job state itself.
// UpdateJobStatus has no attempt cap, so these tests fail it with a
// non-retryable error to keep the activity from being retried.
// ─────────────────────────────────────────────────────────────────────────────

func nonRetryableTestError(msg string) error {
	return temporal.NewNonRetryableApplicationError(msg, "TestPermanentError", nil)
}

// mockJobStatusUpdates stubs UpdateJobStatus per target state, so a test can fail one
// transition while letting the others through.
func mockJobStatusUpdates(env *testsuite.TestWorkflowEnvironment, commonActivity activities.CommonActivities,
	state datamodel.JobState, err error) {
	env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool { return job.State == string(state) }),
	).Return(err)
}

func TestManageBackupConfigWorkflow_JobStatusBookkeepingFailures(t *testing.T) {
	t.Run("Failure_ProcessingAndErrorJobUpdatesBothFail", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetJob)
		env.RegisterActivity(commonActivity.UpdateJobStatus)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockJobStatusUpdates(env, commonActivity, datamodel.JobsStatePROCESSING,
			nonRetryableTestError("processing update failed"))
		mockJobStatusUpdates(env, commonActivity, datamodel.JobsStateERROR,
			nonRetryableTestError("error update failed"))

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		// The run never started, so no vault work was scheduled.
		env.AssertActivityNumberOfCalls(tt, "UpdateJobStatus", 2)
		env.AssertActivityNumberOfCalls(tt, "CheckBackupVaultExistInVCP", 0)
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("Failure_ErrorJobUpdateFailsAfterRunFailure", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, _, expertModeActivity, _ := registerCoreActivities(env, mockStorage)

		mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
			BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
			State:     string(datamodel.JobsStateNEW),
		}, nil)
		mockJobStatusUpdates(env, commonActivity, datamodel.JobsStatePROCESSING, nil)
		mockJobStatusUpdates(env, commonActivity, datamodel.JobsStateERROR,
			nonRetryableTestError("error update failed"))
		mockStateRestore(env, expertModeActivity)
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).
			Return("", assert.AnError)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		// The bookkeeping failure is logged; the original run error is what surfaces.
		assert.ErrorContains(tt, env.GetWorkflowError(), assert.AnError.Error())
		env.AssertExpectations(tt)
		mockStorage.AssertExpectations(tt)
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// Interrupted sleeps
//
// Both waits in the attach path tolerate cancellation: the wait for the
// cross-region service account only logs, while the wait before HMAC key
// creation aborts so no un-recorded GCP key is created.
// ─────────────────────────────────────────────────────────────────────────────

// mockOptionalJobFlow stubs the job activities without pinning the number of status
// updates, for tests where cancellation decides how far the workflow gets.
func mockOptionalJobFlow(mockStorage *database.MockStorage) {
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	mockStorage.On("UpdateJob", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Maybe()
}

func TestManageBackupConfigWorkflow_SleepInterrupted(t *testing.T) {
	t.Run("CrossRegionPermissions_SleepCanceled_IsLoggedOnly", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, _ := registerCoreActivities(env, mockStorage)
		stubRemoteBackupVaultActivities(env)

		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		env.RegisterActivity(updateActivity2.FindTenancyDetails)

		volumeCreateActivity := activities.VolumeCreateActivity{SE: mockStorage}
		env.RegisterActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity)

		mockOptionalJobFlow(mockStorage)
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
		env.OnActivity(updateActivity.CheckBucketResourceName, mock.Anything, mock.Anything).
			Return(populatedBucketDetails(), nil)
		env.OnActivity(volumeCreateActivity.SetupCrossRegionBackupPermissionsActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		env.OnActivity(expertModeActivity.UpdateExpertModeVolumeBackupConfigInDB, mock.Anything, mock.Anything).
			Return(nil).Maybe()

		// Cancel while the workflow is parked in the post-permissions wait.
		env.RegisterDelayedCallback(func() { env.CancelWorkflow() }, time.Second)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, baseVolume(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "SetupCrossRegionBackupPermissionsActivity", 1)
		mockStorage.AssertExpectations(tt)
	})

	t.Run("DedicatedCapacityHmac_SleepCanceled_SkipsKeyCreation", func(tt *testing.T) {
		env, mockStorage := newManageBackupConfigTestEnv(tt)
		commonActivity, updateActivity, expertModeActivity, backupConfigActivity := registerCoreActivities(env, mockStorage)
		updateActivity2 := activities.VolumeUpdateActivity{SE: mockStorage}
		adcActivity := registerDedicatedCapacityActivities(env, backupConfigActivity, updateActivity2)

		mockOptionalJobFlow(mockStorage)
		mockStateRestore(env, expertModeActivity)
		mockDedicatedCapacityProvisioningPrereqs(env, commonActivity, updateActivity, updateActivity2)
		env.OnActivity(backupConfigActivity.CreateDedicatedCapacityBackupServiceAccount, mock.Anything, mock.Anything, mock.Anything).
			Return(&hyperscalermodels.ServiceAccount{Email: "sa@project.iam.gserviceaccount.com"}, nil)
		env.OnActivity(adcActivity.IsServiceAccountCreated, mock.Anything, mock.Anything).
			Return(true, nil)
		env.OnActivity(adcActivity.AttachRolesToServiceAccount, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
			Return(nil)
		// No stored secret, so the workflow waits before creating HMAC keys.
		env.OnActivity(backupConfigActivity.GetDedicatedCapacityBackupHmacSecret, mock.Anything, mock.Anything).
			Return(nil, nil)

		env.RegisterDelayedCallback(func() { env.CancelWorkflow() }, time.Second)

		env.ExecuteWorkflow(ManageBackupConfigWorkflow, dedicatedCapacityVolumeForAttach(), baseParams())

		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
		env.AssertActivityNumberOfCalls(tt, "CreateHmacKeys", 0)
		env.AssertActivityNumberOfCalls(tt, "StoreDedicatedCapacityBackupHmacSecret", 0)
		mockStorage.AssertExpectations(tt)
	})
}
