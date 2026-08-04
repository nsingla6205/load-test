package backgroundworkflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type SyncBackupZiZsWorkflowTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite

	env *testsuite.TestWorkflowEnvironment
}

func (s *SyncBackupZiZsWorkflowTestSuite) SetupTest() {
	s.env = s.NewTestWorkflowEnvironment()
	s.env.SetContextPropagators([]workflow.ContextPropagator{util.NewContextMapPropagator()})
	s.env.RegisterWorkflow(SyncBackupZiZsWorkflow)
}

func (s *SyncBackupZiZsWorkflowTestSuite) AfterTest() {
	s.env.AssertExpectations(s.T())
}

func TestSyncBackupZiZsWorkflowTestSuite(t *testing.T) {
	suite.Run(t, new(SyncBackupZiZsWorkflowTestSuite))
}

// Helper function to register all activities needed for SyncBackupZiZsWorkflow tests
func (s *SyncBackupZiZsWorkflowTestSuite) registerSyncBackupZiZsActivities() {
	// Register the activity struct
	s.env.RegisterActivity(&backgroundactivities.SyncBackupZiZsActivity{})
}

// Helper function to get activity instance for mocking
func (s *SyncBackupZiZsWorkflowTestSuite) getActivity() *backgroundactivities.SyncBackupZiZsActivity {
	return &backgroundactivities.SyncBackupZiZsActivity{}
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_Success() {
	s.registerSyncBackupZiZsActivities()

	// Mock backup vaults with bucket details
	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault-1",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-1",
					ServiceAccountName:  "sa-1",
					VendorSubnetID:      "subnet-1",
					TenantProjectNumber: "123456789",
					SatisfiesPzi:        false,
					SatisfiesPzs:        false,
				},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "vault-2"},
			Name:      "test-vault-2",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-2",
					ServiceAccountName:  "sa-2",
					VendorSubnetID:      "subnet-2",
					TenantProjectNumber: "987654321",
					SatisfiesPzi:        false,
					SatisfiesPzs:        false,
				},
			},
		},
	}

	activity := s.getActivity()

	// Mock GetAllBackupVaults activity
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return(backupVaults, nil)

	// Mock SyncBucketDetails activity for each bucket
	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "bucket-1",
		ServiceAccountName:  "sa-1",
		VendorSubnetID:      "subnet-1",
		TenantProjectNumber: "123456789",
		SatisfiesPzi:        true,
		SatisfiesPzs:        false,
	}, nil).Maybe()

	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "bucket-2",
		ServiceAccountName:  "sa-2",
		VendorSubnetID:      "subnet-2",
		TenantProjectNumber: "987654321",
		SatisfiesPzi:        false,
		SatisfiesPzs:        true,
	}, nil).Maybe()

	// Mock UpdateBackupVault activity for each vault
	s.env.OnActivity(activity.UpdateBackupVault, mock.Anything, mock.Anything).Return(nil).Maybe()

	heartbeatTimeout := captureActivityHeartbeatTimeout(s.env, "GetAllBackupVaults")
	expectedPolicy, policyErr := workflows.PopulateRetryPolicyParams()
	s.Require().NoError(policyErr)

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
	assert.Equal(s.T(), expectedPolicy.HeartBeatTimeout, *heartbeatTimeout)
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_GetAllBackupVaultsFails() {
	s.registerSyncBackupZiZsActivities()

	activity := s.getActivity()

	// Mock GetAllBackupVaults activity to return error
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return(nil, errors.New("database error"))

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow failed
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.Error(s.T(), s.env.GetWorkflowError())
	assert.Contains(s.T(), s.env.GetWorkflowError().Error(), "database error")
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_EmptyBackupVaults() {
	s.registerSyncBackupZiZsActivities()

	activity := s.getActivity()

	// Mock GetAllBackupVaults activity to return empty list
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_BackupVaultWithNoBucketDetails() {
	s.registerSyncBackupZiZsActivities()

	// Mock backup vault with no bucket details
	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel:     datamodel.BaseModel{UUID: "vault-1"},
			Name:          "test-vault-1",
			BucketDetails: datamodel.BucketDetailsArray{}, // Empty bucket details
		},
	}

	activity := s.getActivity()
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return(backupVaults, nil)

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully (should skip vault with no bucket details)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_SyncBucketDetailsFails() {
	s.registerSyncBackupZiZsActivities()

	// Mock backup vault with bucket details
	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault-1",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-1",
					ServiceAccountName:  "sa-1",
					VendorSubnetID:      "subnet-1",
					TenantProjectNumber: "123456789",
				},
			},
		},
	}

	activity := s.getActivity()
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return(backupVaults, nil)

	// Mock SyncBucketDetails activity to return error
	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(nil, errors.New("cloud service error"))

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully (should continue despite bucket sync failure)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_UpdateBackupVaultFails() {
	s.registerSyncBackupZiZsActivities()

	// Mock backup vault with bucket details
	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault-1",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-1",
					ServiceAccountName:  "sa-1",
					VendorSubnetID:      "subnet-1",
					TenantProjectNumber: "123456789",
				},
			},
		},
	}

	activity := s.getActivity()
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return(backupVaults, nil)

	// Mock SyncBucketDetails activity to return success
	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "bucket-1",
		ServiceAccountName:  "sa-1",
		VendorSubnetID:      "subnet-1",
		TenantProjectNumber: "123456789",
		SatisfiesPzi:        true,
		SatisfiesPzs:        false,
	}, nil)

	// Mock UpdateBackupVault activity to return error
	s.env.OnActivity(activity.UpdateBackupVault, mock.Anything, mock.Anything).Return(errors.New("database update error"))

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully (should continue despite update failure)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_MultipleBucketsPerVault() {
	s.registerSyncBackupZiZsActivities()

	// Mock backup vault with multiple bucket details
	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault-1",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-1",
					ServiceAccountName:  "sa-1",
					VendorSubnetID:      "subnet-1",
					TenantProjectNumber: "123456789",
				},
				{
					BucketName:          "bucket-2",
					ServiceAccountName:  "sa-2",
					VendorSubnetID:      "subnet-2",
					TenantProjectNumber: "123456789",
				},
			},
		},
	}

	activity := s.getActivity()
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return(backupVaults, nil)

	// Mock SyncBucketDetails activity for each bucket
	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "bucket-1",
		ServiceAccountName:  "sa-1",
		VendorSubnetID:      "subnet-1",
		TenantProjectNumber: "123456789",
		SatisfiesPzi:        true,
		SatisfiesPzs:        false,
	}, nil).Maybe()

	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "bucket-2",
		ServiceAccountName:  "sa-2",
		VendorSubnetID:      "subnet-2",
		TenantProjectNumber: "123456789",
		SatisfiesPzi:        false,
		SatisfiesPzs:        true,
	}, nil).Maybe()

	// Mock UpdateBackupVault activity
	s.env.OnActivity(activity.UpdateBackupVault, mock.Anything, mock.Anything).Return(nil).Maybe()

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_PartialFailures() {
	s.registerSyncBackupZiZsActivities()

	// Mock backup vaults with mixed success/failure scenarios
	backupVaults := []*datamodel.BackupVault{
		{
			BaseModel: datamodel.BaseModel{UUID: "vault-1"},
			Name:      "test-vault-1",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-1",
					ServiceAccountName:  "sa-1",
					VendorSubnetID:      "subnet-1",
					TenantProjectNumber: "123456789",
				},
			},
		},
		{
			BaseModel: datamodel.BaseModel{UUID: "vault-2"},
			Name:      "test-vault-2",
			BucketDetails: datamodel.BucketDetailsArray{
				{
					BucketName:          "bucket-2",
					ServiceAccountName:  "sa-2",
					VendorSubnetID:      "subnet-2",
					TenantProjectNumber: "987654321",
				},
			},
		},
	}

	activity := s.getActivity()
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return(backupVaults, nil)

	// Mock SyncBucketDetails - success for first bucket, failure for second
	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(&datamodel.BucketDetails{
		BucketName:          "bucket-1",
		ServiceAccountName:  "sa-1",
		VendorSubnetID:      "subnet-1",
		TenantProjectNumber: "123456789",
		SatisfiesPzi:        true,
		SatisfiesPzs:        false,
	}, nil).Maybe()

	s.env.OnActivity(activity.SyncBucketDetails, mock.Anything, mock.Anything).Return(nil, errors.New("cloud service error")).Maybe()

	// Mock UpdateBackupVault - success for first vault, failure for second
	s.env.OnActivity(activity.UpdateBackupVault, mock.Anything, mock.Anything).Return(nil).Maybe()

	s.env.OnActivity(activity.UpdateBackupVault, mock.Anything, mock.Anything).Return(errors.New("database update error")).Maybe()

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully (should continue despite partial failures)
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())
}

// Test workflow setup and status query
func (s *SyncBackupZiZsWorkflowTestSuite) TestSyncBackupZiZsWorkflow_SetupAndStatusQuery() {
	s.registerSyncBackupZiZsActivities()

	activity := s.getActivity()

	// Mock activities to return empty results for setup test
	s.env.OnActivity(activity.GetAllBackupVaults, mock.Anything).Return([]*datamodel.BackupVault{}, nil)

	// Execute the workflow
	s.env.ExecuteWorkflow(SyncBackupZiZsWorkflow)

	// Assert workflow completed successfully
	assert.True(s.T(), s.env.IsWorkflowCompleted())
	assert.NoError(s.T(), s.env.GetWorkflowError())

	// Test status query
	status, err := s.env.QueryWorkflow("status")
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), status)

	var workflowStatus *workflows.WorkflowStatus
	err = status.Get(&workflowStatus)
	assert.NoError(s.T(), err)
	assert.NotNil(s.T(), workflowStatus)
	assert.Equal(s.T(), workflows.WorkflowStatusCompleted, workflowStatus.Status)
}
