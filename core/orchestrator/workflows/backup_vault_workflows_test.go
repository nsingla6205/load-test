package workflows

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/converter"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type BackupVaultWorkflowsTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *BackupVaultWorkflowsTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	s.env.RegisterWorkflow(CreateBackupVaultWorkflow)
	s.env.RegisterWorkflow(UpdateBackupVaultWorkflow)
	s.env.RegisterWorkflow(DeleteBackupVaultWorkflow)
	s.env.RegisterWorkflow(RotateCmekBackupsWorkflow)
}

func (s *BackupVaultWorkflowsTestSuite) AfterTest(_ string, _ string) {
	s.env.AssertExpectations(s.T())
}

func TestBackupVaultWorkflowsTestSuite(t *testing.T) {
	suite.Run(t, new(BackupVaultWorkflowsTestSuite))
}

func (s *BackupVaultWorkflowsTestSuite) setupMockStorage() *database.MockStorage {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "test-job-uuid"},
		State:     string(datamodel.JobsStateNEW),
	}, nil).Maybe()
	return mockStorage
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_Success() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateBackupVaultWorkflow_ClearsDescriptionWhenCVSOmitsIt covers the CVS response
// omitting a cleared description. The requested empty string must still reach the VCP save.
func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_ClearsDescriptionWhenCVSOmitsIt() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "Updated description"
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1), UUID: "vault-uuid"},
		Name:        "backup_vault_test",
		Description: &des,
	}
	sdeBackupVault := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{ID: int64(1), UUID: "vault-uuid"},
		Name:      "backup_vault_test",
	}
	empty := ""

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(sdeBackupVault, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.MatchedBy(func(updated *datamodel.BackupVault) bool {
		return updated != nil && updated.Description != nil && *updated.Description == ""
	}), mock.Anything).Return(sdeBackupVault, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{Description: &empty}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_CreateBackupVaultWorkflow_Success covers the cross-region hydration workflow:
// it hydrates the destination region via CreateRemoteBackupVaultInVCP and completes successfully.
func (s *BackupVaultWorkflowsTestSuite) Test_CreateBackupVaultWorkflow_Success() {
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupRegion := "us-west1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1), UUID: "src-vault-uuid"},
		Name:             "backup_vault_test",
		RegionName:       "us-east4",
		BackupRegionName: &backupRegion,
		BackupVaultType:  "CROSS_REGION",
	}
	remoteBV := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "remote-vault-uuid"},
		Name:      "backup_vault_test",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupVaultActivity.CreateRemoteBackupVault)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultFields)

	s.env.OnActivity(backupVaultActivity.CreateRemoteBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(remoteBV, nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultFields, mock.Anything, "src-vault-uuid", mock.MatchedBy(func(updates map[string]interface{}) bool {
		val, ok := updates["external_uuid"]
		return ok && val == "remote-vault-uuid"
	})).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateBackupVaultWorkflow, &common.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_CreateBackupVaultWorkflow_RemoteHydrationError verifies the workflow fails when the
// destination-region hydration activity returns an error.
func (s *BackupVaultWorkflowsTestSuite) Test_CreateBackupVaultWorkflow_RemoteHydrationError() {
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupRegion := "us-west1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1), UUID: "src-vault-uuid"},
		Name:             "backup_vault_test",
		RegionName:       "us-east4",
		BackupRegionName: &backupRegion,
		BackupVaultType:  "CROSS_REGION",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupVaultActivity.CreateRemoteBackupVault)

	s.env.OnActivity(backupVaultActivity.CreateRemoteBackupVault, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to create remote backup vault in VCP"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateBackupVaultWorkflow, &common.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_CreateBackupVaultWorkflow_UpdateFieldsError verifies the workflow fails when persisting
// the remote vault UUID on the source vault fails.
func (s *BackupVaultWorkflowsTestSuite) Test_CreateBackupVaultWorkflow_UpdateFieldsError() {
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupRegion := "us-west1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1), UUID: "src-vault-uuid"},
		Name:             "backup_vault_test",
		RegionName:       "us-east4",
		BackupRegionName: &backupRegion,
		BackupVaultType:  "CROSS_REGION",
	}
	remoteBV := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "remote-vault-uuid"},
		Name:      "backup_vault_test",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupVaultActivity.CreateRemoteBackupVault)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultFields)

	s.env.OnActivity(backupVaultActivity.CreateRemoteBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(remoteBV, nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultFields, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("failed to update backup vault fields"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(CreateBackupVaultWorkflow, &common.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateBackupVaultWorkflow_UseVCPRegion_ApplyBackupVaultUpdateParams_Success covers the
// useVCPRegion branch: ApplyBackupVaultUpdateParams then UpdateBackupVaultInVCP (no SDE/JWT path).
func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_UseVCPRegion_ApplyBackupVaultUpdateParams_Success() {
	origUseVCPRegion := cvp.CVP_HOST
	cvp.CVP_HOST = ""
	defer func() { cvp.CVP_HOST = origUseVCPRegion }()

	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
	mergedFromApply := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: int64(1), UUID: "merged-from-apply"},
		Name:            bv.Name,
		Description:     bv.Description,
		BackupVaultType: "IN_REGION",
	}
	dbBackupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: int64(1), UUID: "db-bv-uuid"},
		Name:            bv.Name,
		BackupVaultType: "IN_REGION",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.ApplyBackupVaultUpdateParams)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(backupvaultUpdateActivity.ApplyBackupVaultUpdateParams, mock.Anything, mock.Anything, mock.Anything).Return(mergedFromApply, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(dbBackupVault, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{AccountName: "test-account"}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_UpdateSDEError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, errors.New("failed to update backup vault in SDE"))
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_UpdateVCPError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to update backup vault in VCP"))
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_SignedTokenError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get signed JWT token"))
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_Success() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_DeleteSDEError() {
	mockStorage := s.setupMockStorage()
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, errors.New("failed to update backup vault in SDE"))
	s.env.OnActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_DeleteVCPError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError)

	// GetAuthJWTToken is never reached because DeleteBackupVaultInVCP fails first.
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil).Maybe()
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, errors.New("failed to update backup vault in VCP"))
	s.env.OnActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_SignedTokenError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError)

	// DeleteBackupVaultInVCP must succeed so the workflow reaches GetAuthJWTToken.
	// GetAuthJWTToken then fails, aborting before DeleteBackupVaultInSDE/DeleteBackupVaultBuckets.
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(&datamodel.BackupVault{}, nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return(nil, errors.New("failed to get signed JWT token"))
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil).Maybe()
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.env.OnActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_DeleteBucketsError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(errors.New("failed to delete backup vault buckets"))
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	// Bucket deletion is non-fatal — workflow should succeed even when bucket delete fails.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_CrossRegion_Success() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault"
	mrd := int64(30)
	backupRegion := "us-west1"
	sourceRegion := "us-east1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
		SourceRegionName: &sourceRegion,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
	remoteBackupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: int64(2)},
		Name:            "backup_vault_remote",
		BackupVaultType: "CROSS_REGION",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteRemoteBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteRemoteBackupVaultInVCP, mock.Anything, mock.Anything).Return(remoteBackupVault, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_CrossRegion_BlockedFromDestinationRegion() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault"
	mrd := int64(30)
	backupRegion := "us-west1"
	sourceRegion := "us-east1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
		SourceRegionName: &sourceRegion,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
	// VCP may not yet have a local record of this vault; the destination check must rely on the
	// record fetched from SDE (queried in the requesting region, i.e. the destination region here).
	sdeBv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
		SourceRegionName: &sourceRegion,
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError)
	// DeleteBackupVaultInSDE, DeleteRemoteBackupVaultInVCP, and DeleteBackupVaultBuckets must NOT be
	// registered/called: the workflow must return early once the destination-region check fails.

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(sdeBv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	var errorJob *datamodel.Job
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			job := args.Get(1).(*datamodel.Job)
			if job.State == string(datamodel.JobsStateERROR) {
				errorJob = job
			}
		}).Return(nil)

	// Region matches the vault's BackupRegionName (destination region) — deletion must be blocked.
	params := &common.BackupVaultParams{Region: backupRegion}
	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, params, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	wfErr := s.env.GetWorkflowError()
	assert.Error(s.T(), wfErr)
	assert.Contains(s.T(), wfErr.Error(), "Remote backup vaults cannot be deleted")
	if assert.NotNil(s.T(), errorJob) {
		catalogMsg := vsaerrors.GetErrorMessageByTrackingID(vsaerrors.ErrCrossRegionBackupVaultDeleteFromDestinationRegion).Message
		assert.Equal(s.T(), "Remote backup vaults cannot be deleted", catalogMsg)
		assert.Equal(s.T(), vsaerrors.ErrCrossRegionBackupVaultDeleteFromDestinationRegion, errorJob.TrackingID)
		assert.Equal(s.T(), catalogMsg, errorJob.ErrorDetails)
	}
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_CrossRegion_DeleteRemoteError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault"
	mrd := int64(30)
	backupRegion := "us-west1"
	sourceRegion := "us-east1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
		SourceRegionName: &sourceRegion,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteRemoteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteRemoteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, errors.New("failed to delete remote backup vault"))
	s.env.OnActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_CrossRegion_EmptyBackupRegionName() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault with empty region"
	mrd := int64(30)
	emptyRegion := ""
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &emptyRegion, // Empty string, should skip remote deletion.
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	// NOTE: DeleteRemoteBackupVaultInVCP should NOT be called due to empty BackupRegionName.

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	// Remote deletion skipped due to empty BackupRegionName.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_CrossRegion_NilBackupRegionName() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault with nil region"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: nil,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	// NOTE: DeleteRemoteBackupVaultInVCP should NOT be called due to nil BackupRegionName.

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	// Remote deletion skipped, no nil pointer panic.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_NonCrossRegion_SkipRemoteDeletion() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "non-cross-region backup vault"
	mrd := int64(30)
	backupRegion := "us-west1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_in_region",
		Description:      &des,
		BackupVaultType:  "IN_REGION", // Not CROSS_REGION.
		BackupRegionName: &backupRegion,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	// NOTE: DeleteRemoteBackupVaultInVCP should NOT be called for non-cross-region type.

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	// Remote deletion skipped for non-cross-region.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_CrossRegion_Success() {
	mockStorage := s.setupMockStorage()
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault"
	mrd := int64(30)
	backupRegion := "us-west1"
	sourceRegion := "us-east1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
		SourceRegionName: &sourceRegion,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
	sdeBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region_sde",
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
	}
	dbBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region_vcp",
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
	}
	remoteBackupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{ID: int64(2)},
		Name:            "backup_vault_remote",
		BackupVaultType: "CROSS_REGION",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateRemoteBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(sdeBackupVault, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(dbBackupVault, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(remoteBackupVault, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_CrossRegion_UpdateRemoteError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault"
	mrd := int64(30)
	backupRegion := "us-west1"
	sourceRegion := "us-east1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
		SourceRegionName: &sourceRegion,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
	sdeBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region_sde",
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
	}
	dbBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region_vcp",
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &backupRegion,
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateRemoteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultStateInCaseOfError)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(sdeBackupVault, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(dbBackupVault, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to update remote backup vault"))
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_CrossRegion_EmptyBackupRegionName() {
	mockStorage := s.setupMockStorage()
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "cross-region backup vault with empty region"
	mrd := int64(30)
	emptyRegion := ""
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region",
		Description:      &des,
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &emptyRegion, // Empty string, should skip remote update.
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
	sdeBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region_sde",
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &emptyRegion,
	}
	dbBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_cross_region_vcp",
		BackupVaultType:  "CROSS_REGION",
		BackupRegionName: &emptyRegion,
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)
	// NOTE: UpdateRemoteBackupVaultInVCP should NOT be called due to empty BackupRegionName.

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(sdeBackupVault, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(dbBackupVault, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	// Remote update skipped due to empty BackupRegionName.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_NonCrossRegion_SkipRemoteUpdate() {
	mockStorage := s.setupMockStorage()
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	des := "non-cross-region backup vault"
	mrd := int64(30)
	backupRegion := "us-west1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_in_region",
		Description:      &des,
		BackupVaultType:  "IN_REGION", // Not CROSS_REGION.
		BackupRegionName: &backupRegion,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
	sdeBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_in_region_sde",
		BackupVaultType:  "IN_REGION",
		BackupRegionName: &backupRegion,
	}
	dbBackupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1)},
		Name:             "backup_vault_in_region_vcp",
		BackupVaultType:  "IN_REGION",
		BackupRegionName: &backupRegion,
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)
	// NOTE: UpdateRemoteBackupVaultInVCP should NOT be called for non-cross-region type.

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(sdeBackupVault, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(dbBackupVault, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	// Remote update skipped for non-cross-region.
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateBackupVaultWorkflow_EnsureJobStateError tests the error path when EnsureJobState fails.
func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_EnsureJobStateError() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error.
	}, nil).Maybe()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING),
	}, nil)
	// UpdateJobStatus is NOT called when EnsureJobState fails — workflow returns immediately.
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{
		Name:        bv.Name,
		Description: bv.Description,
		AccountName: "test-account",
	}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteBackupVaultWorkflow_EnsureJobStateError tests the error path when EnsureJobState fails.
func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_EnsureJobStateError() {
	mockStorage := database.NewMockStorage(s.T())
	mockStorage.On("GetJob", mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING), // Wrong state to trigger error.
	}, nil).Maybe()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	des := "description of backup vault"
	mrd := int64(30)
	bv := &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING),
	}, nil)
	// UpdateJobStatus is NOT called when EnsureJobState fails — workflow returns immediately.
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{
		Name:        bv.Name,
		Description: bv.Description,
		AccountName: "test-account",
	}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_InRegion_Success() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
		// Include nil and empty bucket entries to exercise the branch that skips invalid bucket details.
		BucketDetails: datamodel.BucketDetailsArray{
			nil,
			&datamodel.BucketDetails{BucketName: ""},
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)
	s.env.RegisterActivity(backupVaultActivity.WaitForSDECmekRotationCompletion)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.WaitForSDECmekRotationCompletion, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	heartbeatTimeouts := map[string]time.Duration{}
	s.env.SetOnActivityStartedListener(func(info *activity.Info, _ context.Context, _ converter.EncodedValues) {
		heartbeatTimeouts[info.ActivityType.Name] = info.HeartbeatTimeout
	})

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	_, err := s.env.QueryWorkflowByID("default-test-workflow-id", "status")
	assert.Nil(s.T(), err)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
	retryPolicy, err := PopulateRetryPolicyParams()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), retryPolicy.HeartBeatTimeout, heartbeatTimeouts["WaitForSDECmekRotationCompletion"],
		"the SDE wait activity must have a heartbeat timeout so an evicted worker is detected before StartToCloseTimeout")
	assert.Zero(s.T(), heartbeatTimeouts["RotateBucketCmekActivity"],
		"RotateBucketCmekActivity does not heartbeat and must not be given a heartbeat timeout")
}

func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_InRegion_SDEFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)
	s.env.RegisterActivity(backupVaultActivity.WaitForSDECmekRotationCompletion)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// SDE rotation completes with failure.
	s.env.OnActivity(backupVaultActivity.WaitForSDECmekRotationCompletion, mock.Anything, mock.Anything).Return(false, nil)

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_CRB_Success() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	sourceRegion := "us-east1"
	sourceVaultUUID := "source-bv-uuid"
	destVaultUUID := "dest-bv-uuid"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: destVaultUUID},
		Name:             "crb-vault",
		AccountID:        1,
		BackupVaultType:  activities.CrossRegionBackupType,
		SourceRegionName: &sourceRegion,
		ExternalUUID:     &sourceVaultUUID,
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: destVaultUUID,
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)
	s.env.RegisterActivity(backupVaultActivity.WaitForSDECmekRotationCompletion)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.UpdateRemoteBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.WaitForSDECmekRotationCompletion, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	// First hydration: propagate IN_PROGRESS state (without key version) to the
	// source-region VCP backup vault.
	s.env.OnActivity(
		backupVaultActivity.UpdateRemoteBackupVaultInVCP,
		mock.Anything,
		mock.MatchedBy(func(p *common.BackupVaultParams) bool {
			if !assert.Equal(s.T(), sourceVaultUUID, p.BackupVaultID) {
				return false
			}
			if !assert.NotNil(s.T(), p.BackupRegion) {
				return false
			}
			if !assert.Equal(s.T(), sourceRegion, *p.BackupRegion) {
				return false
			}
			return true
		}),
		mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			// IN_PROGRESS call should have nil BackupsPrimaryKeyVersion.
			if bv.CmekAttributes == nil || bv.CmekAttributes.BackupsPrimaryKeyVersion != nil {
				return false
			}
			if !assert.NotNil(s.T(), bv.CmekAttributes.EncryptionState) {
				return false
			}
			return assert.Equal(s.T(), datamodel.EncryptionStateInProgress, *bv.CmekAttributes.EncryptionState)
		}),
	).Return(&datamodel.BackupVault{}, nil)

	// Second hydration: after successful rotation, propagate COMPLETED state and
	// the new primary key version to the source-region VCP backup vault.
	s.env.OnActivity(
		backupVaultActivity.UpdateRemoteBackupVaultInVCP,
		mock.Anything,
		mock.MatchedBy(func(p *common.BackupVaultParams) bool {
			if !assert.Equal(s.T(), sourceVaultUUID, p.BackupVaultID) {
				return false
			}
			if !assert.NotNil(s.T(), p.BackupRegion) {
				return false
			}
			return assert.Equal(s.T(), sourceRegion, *p.BackupRegion)
		}),
		mock.MatchedBy(func(bv *datamodel.BackupVault) bool {
			// COMPLETED call should have non-nil BackupsPrimaryKeyVersion.
			if bv.CmekAttributes == nil || bv.CmekAttributes.BackupsPrimaryKeyVersion == nil {
				return false
			}
			if !assert.Equal(s.T(), primaryKeyVersion, *bv.CmekAttributes.BackupsPrimaryKeyVersion) {
				return false
			}
			if !assert.NotNil(s.T(), bv.CmekAttributes.EncryptionState) {
				return false
			}
			return assert.Equal(s.T(), datamodel.EncryptionStateCompleted, *bv.CmekAttributes.EncryptionState)
		}),
	).Return(&datamodel.BackupVault{}, nil)

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_CRB_VCPBucketFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	sourceRegion := "us-east1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
		Name:             "crb-vault",
		AccountID:        1,
		BackupVaultType:  activities.CrossRegionBackupType,
		SourceRegionName: &sourceRegion,
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// First bucket rotation fails.
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("bucket rotation failed"))

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_EnsureJobStateError verifies that the workflow
// returns an error when the backing job is not in the expected NEW state.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_EnsureJobStateError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	// Return a job in PROCESSING state instead of NEW to trigger the EnsureJobState error path.
	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStatePROCESSING),
	}, nil)
	// UpdateJobStatus is NOT called when EnsureJobState fails — workflow returns immediately.
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_PopulateRetryPolicyError forces PopulateRetryPolicyParams
// to fail so that the early error return from Run is exercised.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_PopulateRetryPolicyError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	// UpdateBackupVaultEncryptionStateInVCPActivity is called AFTER PopulateRetryPolicyParams,
	// so it is never reached when PopulateRetryPolicyParams fails.

	// Force PopulateRetryPolicyParams to fail by setting an invalid timeout, then restore.
	originalTimeout := StartToCloseTimeout
	StartToCloseTimeout = "invalid-duration"
	defer func() { StartToCloseTimeout = originalTimeout }()

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_InRegion_GetAuthJWTTokenFailure verifies that
// a failure to obtain the auth token marks encryption FAILED and aborts.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_InRegion_GetAuthJWTTokenFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Force GetAuthJWTToken to fail.
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("", errors.New("failed to get auth token"))

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_InRegion_StartSDEFailure verifies that failure
// to start SDE CMEK rotation marks encryption FAILED and aborts.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_InRegion_StartSDEFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Force StartSDECmekRotationForBackupVault to fail.
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to start SDE CMEK rotation"))

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_InRegion_WaitForSDEError verifies the branch
// where the wait activity itself returns an error.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_InRegion_WaitForSDEError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)
	s.env.RegisterActivity(backupVaultActivity.WaitForSDECmekRotationCompletion)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Wait activity returns an error; the workflow should treat this as SDE failure.
	s.env.OnActivity(backupVaultActivity.WaitForSDECmekRotationCompletion, mock.Anything, mock.Anything).Return(false, errors.New("wait for SDE rotation failed"))

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_CRB_UpdateCmekInVCPFailure verifies that
// failure to update VCP CMEK metadata for a CRB vault marks encryption FAILED
// and hydrates the source-region vault with FAILED state.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_CRB_UpdateCmekInVCPFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	sourceRegion := "us-east1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
		Name:             "crb-vault",
		AccountID:        1,
		BackupVaultType:  activities.CrossRegionBackupType,
		SourceRegionName: &sourceRegion,
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)
	s.env.RegisterActivity(backupVaultActivity.WaitForSDECmekRotationCompletion)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.UpdateRemoteBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.WaitForSDECmekRotationCompletion, mock.Anything, mock.Anything).Return(true, nil)
	// Fail the VCP CMEK metadata update.
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to update CMEK metadata"))
	// The source-region hydration on failure is best-effort.
	s.env.OnActivity(backupVaultActivity.UpdateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to hydrate FAILED state"))

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_CRB_HydrationFailure verifies the final
// hydration step failure path after successful rotations.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_CRB_HydrationFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	sourceRegion := "us-east1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
		Name:             "crb-vault",
		AccountID:        1,
		BackupVaultType:  activities.CrossRegionBackupType,
		SourceRegionName: &sourceRegion,
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)
	s.env.RegisterActivity(backupVaultActivity.WaitForSDECmekRotationCompletion)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.UpdateRemoteBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.WaitForSDECmekRotationCompletion, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Fail the final hydration to the source-region VCP backup vault — best-effort,
	// should not cause the workflow to fail.
	s.env.OnActivity(backupVaultActivity.UpdateRemoteBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("failed to hydrate source-region vault"))

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_UpdateJobStatusErrorOnFailure verifies that an
// error while updating the job status during failure is logged but does not
// mask the original workflow error.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_UpdateJobStatusErrorOnFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	sourceRegion := "us-east1"
	backupVault := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{UUID: "bv-uuid"},
		Name:             "crb-vault",
		AccountID:        1,
		BackupVaultType:  activities.CrossRegionBackupType,
		SourceRegionName: &sourceRegion,
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)

	// First UpdateJobStatus call (PROCESSING) succeeds.
	s.env.OnActivity(commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStatePROCESSING)
		}),
	).Return(nil)

	// Second UpdateJobStatus call (ERROR) fails to trigger the logging path.
	s.env.OnActivity(commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStateERROR)
		}),
	).Return(errors.New("failed to update job status"))

	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	// Cause bucket rotation to fail so that the workflow errors.
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("bucket rotation failed"))

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

// Test_RotateCmekBackupsWorkflow_UpdateJobStatusErrorOnCompletion verifies that
// an error while updating the job status to DONE after a successful run is
// surfaced as a workflow error.
func (s *BackupVaultWorkflowsTestSuite) Test_RotateCmekBackupsWorkflow_UpdateJobStatusErrorOnCompletion() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := database.NewMockStorage(s.T())
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupVault := &datamodel.BackupVault{
		BaseModel:       datamodel.BaseModel{UUID: "bv-uuid"},
		Name:            "inregion-vault",
		AccountID:       1,
		BackupVaultType: "IN_REGION",
		BucketDetails: datamodel.BucketDetailsArray{
			&datamodel.BucketDetails{BucketName: "bucket-1"},
		},
	}
	params := &common.BackupVaultParams{
		BackupVaultID: "bv-uuid",
		AccountName:   "12345",
		OwnerID:       "12345",
		Region:        "us-central1",
		Name:          backupVault.Name,
	}
	primaryKeyVersion := "projects/p/locations/r/keyRings/ring/cryptoKeys/key/cryptoKeyVersions/1"

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity)
	s.env.RegisterActivity(backupVaultActivity.RotateBucketCmekActivity)
	s.env.RegisterActivity(backupVaultActivity.StartSDECmekRotationForBackupVault)
	s.env.RegisterActivity(backupVaultActivity.WaitForSDECmekRotationCompletion)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity)

	s.env.OnActivity(commonActivity.GetJob, mock.Anything, mock.Anything).Return(&datamodel.Job{
		BaseModel: datamodel.BaseModel{UUID: "default-test-workflow-id"},
		State:     string(datamodel.JobsStateNEW),
	}, nil)

	// First UpdateJobStatus call (PROCESSING) succeeds.
	s.env.OnActivity(commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStatePROCESSING)
		}),
	).Return(nil)

	// Second UpdateJobStatus call (DONE) fails.
	s.env.OnActivity(commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStateDONE)
		}),
	).Return(errors.New("failed to update job status to DONE"))

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("jwt-token", nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.RotateBucketCmekActivity, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.StartSDECmekRotationForBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupVaultActivity.WaitForSDECmekRotationCompletion, mock.Anything, mock.Anything).Return(true, nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultCmekInVCPActivity, mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(RotateCmekBackupsWorkflow, params, backupVault, primaryKeyVersion)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
}

func newTestBackupVault() *datamodel.BackupVault {
	des := "description of backup vault"
	mrd := int64(30)
	return &datamodel.BackupVault{
		BaseModel:   datamodel.BaseModel{ID: int64(1)},
		Name:        "backup_vault_test",
		Description: &des,
		ImmutableAttributes: &datamodel.ImmutableAttributes{
			IsDailyBackupImmutable:                 true,
			IsWeeklyBackupImmutable:                true,
			IsMonthlyBackupImmutable:               true,
			IsAdhocBackupImmutable:                 true,
			BackupMinimumEnforcedRetentionDuration: &mrd,
		},
	}
}

// onProcessingSucceedThenFail mocks UpdateJobStatus so the initial PROCESSING
// transition succeeds and the given terminal transition fails, exercising the
// "Error when updating the job status" logging paths.
func (s *BackupVaultWorkflowsTestSuite) onProcessingSucceedThenFail(
	commonActivity activities.CommonActivities, terminalState datamodel.JobState,
) {
	s.env.OnActivity(commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(datamodel.JobsStatePROCESSING)
		}),
	).Return(nil)

	s.env.OnActivity(commonActivity.UpdateJobStatus,
		mock.Anything,
		mock.MatchedBy(func(job *datamodel.Job) bool {
			return job.State == string(terminalState)
		}),
	).Return(errors.New("failed to update job status"))
}

// Test_CreateBackupVaultWorkflow_UpdateJobStatusErrorOnCompletion verifies that a
// failure to record the DONE transition is logged without failing the workflow.
func (s *BackupVaultWorkflowsTestSuite) Test_CreateBackupVaultWorkflow_UpdateJobStatusErrorOnCompletion() {
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupRegion := "us-west1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1), UUID: "src-vault-uuid"},
		Name:             "backup_vault_test",
		RegionName:       "us-east4",
		BackupRegionName: &backupRegion,
		BackupVaultType:  "CROSS_REGION",
	}
	remoteBV := &datamodel.BackupVault{
		BaseModel: datamodel.BaseModel{UUID: "remote-vault-uuid"},
		Name:      "backup_vault_test",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupVaultActivity.CreateRemoteBackupVault)
	s.env.RegisterActivity(backupVaultActivity.UpdateBackupVaultFields)

	s.env.OnActivity(backupVaultActivity.CreateRemoteBackupVault, mock.Anything, mock.Anything, mock.Anything).Return(remoteBV, nil)
	s.env.OnActivity(backupVaultActivity.UpdateBackupVaultFields, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.onProcessingSucceedThenFail(commonActivity, datamodel.JobsStateDONE)

	s.env.ExecuteWorkflow(CreateBackupVaultWorkflow, &common.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_CreateBackupVaultWorkflow_UpdateJobStatusErrorOnFailure verifies that a
// failure to record the ERROR transition is logged but does not mask the
// original workflow error.
func (s *BackupVaultWorkflowsTestSuite) Test_CreateBackupVaultWorkflow_UpdateJobStatusErrorOnFailure() {
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupVaultActivity := activities.BackupVaultActivity{SE: mockStorage}

	backupRegion := "us-west1"
	bv := &datamodel.BackupVault{
		BaseModel:        datamodel.BaseModel{ID: int64(1), UUID: "src-vault-uuid"},
		Name:             "backup_vault_test",
		RegionName:       "us-east4",
		BackupRegionName: &backupRegion,
		BackupVaultType:  "CROSS_REGION",
	}

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupVaultActivity.CreateRemoteBackupVault)

	s.env.OnActivity(backupVaultActivity.CreateRemoteBackupVault, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to create remote backup vault in VCP"))
	s.onProcessingSucceedThenFail(commonActivity, datamodel.JobsStateERROR)

	s.env.ExecuteWorkflow(CreateBackupVaultWorkflow, &common.BackupVaultParams{AccountName: "test-account", OwnerID: "owner-uuid"}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateBackupVaultWorkflow_UpdateJobStatusErrorOnCompletion verifies that a
// failure to record the DONE transition is logged without failing the workflow.
func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_UpdateJobStatusErrorOnCompletion() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	bv := newTestBackupVault()

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, nil)
	s.onProcessingSucceedThenFail(commonActivity, datamodel.JobsStateDONE)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_UpdateBackupVaultWorkflow_UpdateJobStatusErrorOnFailure verifies that a
// failure to record the ERROR transition is logged but does not mask the
// original workflow error.
func (s *BackupVaultWorkflowsTestSuite) Test_UpdateBackupVaultWorkflow_UpdateJobStatusErrorOnFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultUpdateActivity := activities.BackupVaultActivity{SE: mockStorage}
	bv := newTestBackupVault()

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultUpdateActivity.UpdateBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultInSDE, mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to update backup vault in SDE"))
	s.env.OnActivity(backupvaultUpdateActivity.UpdateBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.onProcessingSucceedThenFail(commonActivity, datamodel.JobsStateERROR)

	s.env.ExecuteWorkflow(UpdateBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteBackupVaultWorkflow_UpdateJobStatusErrorOnCompletion verifies that a
// failure to record the DONE transition is logged without failing the workflow.
func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_UpdateJobStatusErrorOnCompletion() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	bv := newTestBackupVault()

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, nil)
	s.onProcessingSucceedThenFail(commonActivity, datamodel.JobsStateDONE)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Nil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteBackupVaultWorkflow_UpdateJobStatusErrorOnFailure verifies that a
// failure to record the ERROR transition is logged but does not mask the
// original workflow error.
func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_UpdateJobStatusErrorOnFailure() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	bv := newTestBackupVault()

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultBuckets)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).Return(bv, nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInSDE, mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to delete backup vault in SDE"))
	s.env.OnActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.onProcessingSucceedThenFail(commonActivity, datamodel.JobsStateERROR)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}

// Test_DeleteBackupVaultWorkflow_GetBackupVaultFromSDEError verifies the workflow
// fails when the backup vault cannot be described in SDE.
func (s *BackupVaultWorkflowsTestSuite) Test_DeleteBackupVaultWorkflow_GetBackupVaultFromSDEError() {
	origCVPHost := cvp.CVP_HOST
	cvp.CVP_HOST = "localhost:8009"
	defer func() { cvp.CVP_HOST = origCVPHost }()
	mockStorage := s.setupMockStorage()
	commonActivity := activities.CommonActivities{SE: mockStorage}
	backupvaultDeleteActivity := activities.BackupVaultActivity{SE: mockStorage}
	bv := newTestBackupVault()

	s.env.RegisterActivity(commonActivity.GetJob)
	s.env.RegisterActivity(commonActivity.UpdateJobStatus)
	s.env.RegisterActivity(commonActivity.GetAuthJWTToken)
	s.env.RegisterActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP)
	s.env.RegisterActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError)

	s.env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, mock.Anything).Return("test-jwt-token", nil)
	s.env.OnActivity(backupvaultDeleteActivity.DeleteBackupVaultInVCP, mock.Anything, mock.Anything).Return(nil, nil)
	s.env.OnActivity(backupvaultDeleteActivity.GetBackupVaultFromSDE, mock.Anything, mock.Anything).
		Return(nil, errors.New("failed to describe backup vault in SDE"))
	s.env.OnActivity(backupvaultDeleteActivity.UpdateDeletedBackupVaultStateInCaseOfError, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.env.OnActivity(commonActivity.UpdateJobStatus, mock.Anything, mock.Anything).Return(nil)

	s.env.ExecuteWorkflow(DeleteBackupVaultWorkflow, &common.BackupVaultParams{}, bv)

	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NotNil(s.T(), s.env.GetWorkflowError())
}
