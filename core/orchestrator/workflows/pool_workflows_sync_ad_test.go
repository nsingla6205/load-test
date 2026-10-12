package workflows

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	cvpmodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/cvp/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/active_directory_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestSyncActiveDirectoryInVcp(t *testing.T) {
	input := adSyncInput{
		ActiveDirectoryID: "ad-id",
		AccountName:       "acct",
		Region:            "loc",
		XCorrelationID:    "corr",
		ActiveDirectory:   &models.ActiveDirectory{AdName: "ad-name"},
		LargeCapacity:     false,
	}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
	}

	t.Run("ReturnsErrorWhenActiveDirectoryIsNil", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		env.RegisterWorkflow(testSyncADWorkflow)

		invalid := input
		invalid.ActiveDirectory = nil

		env.ExecuteWorkflow(testSyncADWorkflow, invalid, pool)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("ReturnsErrorWhenCreatedADIsNil", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(tt)
		env.RegisterActivity(&active_directory_activities.ActiveDirectorySyncActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, input.AccountName).Return("test-jwt-token", nil)
		env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).Return(&active_directory_activities.PushActiveDirectoryPasswordResult{
			Operation:  &cvpmodels.OperationV1beta{Name: "op"},
			SecretName: "secret-path",
		}, nil)
		env.OnActivity("PollPushPasswordOperationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateActiveDirectoryInVCPActivity", mock.Anything, mock.Anything, "secret-path").Return((*datamodel.ActiveDirectory)(nil), nil)
		env.OnActivity("UpdatePoolActiveDirectoryIDActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.RegisterWorkflow(testSyncADWorkflow)

		env.ExecuteWorkflow(testSyncADWorkflow, input, pool)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("ReturnsErrorWhenPushPasswordResultIsNil", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(tt)
		env.RegisterActivity(&active_directory_activities.ActiveDirectorySyncActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, input.AccountName).Return("test-jwt-token", nil)
		env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).Return((*active_directory_activities.PushActiveDirectoryPasswordResult)(nil), nil)
		env.RegisterWorkflow(testSyncADWorkflow)

		env.ExecuteWorkflow(testSyncADWorkflow, input, pool)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.Error(tt, env.GetWorkflowError())
	})

	t.Run("ReturnsSuccess", func(tt *testing.T) {
		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(tt)
		env.RegisterActivity(&active_directory_activities.ActiveDirectorySyncActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, input.AccountName).Return("test-jwt-token", nil)
		env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).Return(&active_directory_activities.PushActiveDirectoryPasswordResult{
			Operation:  &cvpmodels.OperationV1beta{Name: "op"},
			SecretName: "secret-path",
		}, nil)
		env.OnActivity("PollPushPasswordOperationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateActiveDirectoryInVCPActivity", mock.Anything, mock.Anything, "secret-path").Return(&datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 10}}, nil)
		env.OnActivity("UpdatePoolActiveDirectoryIDActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.RegisterWorkflow(testSyncADWorkflow)

		env.ExecuteWorkflow(testSyncADWorkflow, input, pool)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})

	t.Run("SucceedsWithInvalidScheduleToCloseTimeout", func(tt *testing.T) {
		origTimeout := AdScheduleToCloseTimeout
		AdScheduleToCloseTimeout = "invalid"
		defer func() { AdScheduleToCloseTimeout = origTimeout }()

		var ts testsuite.WorkflowTestSuite
		env := ts.NewTestWorkflowEnvironment()
		mockStorage := database.NewMockStorage(tt)
		env.RegisterActivity(&active_directory_activities.ActiveDirectorySyncActivity{})
		commonActivity := activities.CommonActivities{SE: mockStorage}
		env.RegisterActivity(commonActivity.GetAuthJWTToken)
		env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, input.AccountName).Return("test-jwt-token", nil)
		env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).Return(&active_directory_activities.PushActiveDirectoryPasswordResult{
			Operation:  &cvpmodels.OperationV1beta{Name: "op"},
			SecretName: "secret-path",
		}, nil)
		env.OnActivity("PollPushPasswordOperationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.OnActivity("CreateActiveDirectoryInVCPActivity", mock.Anything, mock.Anything, "secret-path").Return(&datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 10}}, nil)
		env.OnActivity("UpdatePoolActiveDirectoryIDActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
		env.RegisterWorkflow(testSyncADWorkflow)

		env.ExecuteWorkflow(testSyncADWorkflow, input, pool)
		assert.True(tt, env.IsWorkflowCompleted())
		assert.NoError(tt, env.GetWorkflowError())
	})
}

func testSyncADWorkflow(ctx workflow.Context, input adSyncInput, pool *datamodel.Pool) error {
	return syncActiveDirectoryInVcp(ctx, input, pool)
}

func TestSyncActiveDirectoryInVcp_ActivityFailures(t *testing.T) {
	input := adSyncInput{
		ActiveDirectoryID: "ad-id",
		AccountName:       "acct",
		Region:            "loc",
		XCorrelationID:    "corr",
		ActiveDirectory:   &models.ActiveDirectory{AdName: "ad-name"},
	}
	pool := &datamodel.Pool{
		BaseModel: datamodel.BaseModel{UUID: "pool-uuid"},
		Name:      "pool-name",
	}
	pushResult := &active_directory_activities.PushActiveDirectoryPasswordResult{
		Operation:  &cvpmodels.OperationV1beta{Name: "op"},
		SecretName: "secret-path",
	}
	createdAD := &datamodel.ActiveDirectory{BaseModel: datamodel.BaseModel{ID: 10}}

	tests := []struct {
		name           string
		expectedError  string
		mockActivities func(*testsuite.TestWorkflowEnvironment)
	}{
		{
			name:          "push password fails",
			expectedError: "push password failed",
			mockActivities: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).
					Return((*active_directory_activities.PushActiveDirectoryPasswordResult)(nil), errors.New("push password failed"))
			},
		},
		{
			name:          "push password returns nil operation",
			expectedError: "failed to push Active Directory password to cvp",
			mockActivities: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).
					Return(&active_directory_activities.PushActiveDirectoryPasswordResult{}, nil)
			},
		},
		{
			name:          "poll operation fails",
			expectedError: "poll operation failed",
			mockActivities: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).Return(pushResult, nil)
				env.OnActivity("PollPushPasswordOperationActivity", mock.Anything, mock.Anything, mock.Anything).
					Return(errors.New("poll operation failed"))
			},
		},
		{
			name:          "create active directory fails",
			expectedError: "create active directory failed",
			mockActivities: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).Return(pushResult, nil)
				env.OnActivity("PollPushPasswordOperationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				env.OnActivity("CreateActiveDirectoryInVCPActivity", mock.Anything, mock.Anything, pushResult.SecretName).
					Return((*datamodel.ActiveDirectory)(nil), errors.New("create active directory failed"))
			},
		},
		{
			name:          "update pool active directory fails",
			expectedError: "update pool active directory failed",
			mockActivities: func(env *testsuite.TestWorkflowEnvironment) {
				env.OnActivity("PushActiveDirectoryPasswordActivity", mock.Anything, mock.Anything).Return(pushResult, nil)
				env.OnActivity("PollPushPasswordOperationActivity", mock.Anything, mock.Anything, mock.Anything).Return(nil)
				env.OnActivity("CreateActiveDirectoryInVCPActivity", mock.Anything, mock.Anything, pushResult.SecretName).Return(createdAD, nil)
				env.OnActivity("UpdatePoolActiveDirectoryIDActivity", mock.Anything, mock.Anything, createdAD.ID).
					Return(errors.New("update pool active directory failed"))
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var ts testsuite.WorkflowTestSuite
			env := ts.NewTestWorkflowEnvironment()
			mockStorage := database.NewMockStorage(t)
			env.RegisterActivity(&active_directory_activities.ActiveDirectorySyncActivity{})
			commonActivity := activities.CommonActivities{SE: mockStorage}
			env.RegisterActivity(commonActivity.GetAuthJWTToken)
			env.OnActivity(commonActivity.GetAuthJWTToken, mock.Anything, input.AccountName).Return("test-jwt-token", nil)
			test.mockActivities(env)
			env.RegisterWorkflow(testSyncADWorkflow)

			env.ExecuteWorkflow(testSyncADWorkflow, input, pool)

			assert.True(t, env.IsWorkflowCompleted())
			if assert.Error(t, env.GetWorkflowError()) {
				assert.Contains(t, env.GetWorkflowError().Error(), test.expectedError)
			}
			env.AssertExpectations(t)
		})
	}
}
