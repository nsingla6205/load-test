package backgroundactivities

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowEngine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/mocks"
)

func setupContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, log.Fields{})
	return ctx
}

func TestGetBackupPolicies_Success_WithPagination(t *testing.T) {
	ctx := setupContext()
	mockStorage := database.NewMockStorage(t)
	activity := &UpdateBackupScheduleActivity{
		SE: mockStorage,
	}

	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  100,
	}

	backupPolicies := []*datamodel.BackupPolicy{
		{BaseModel: datamodel.BaseModel{UUID: "policy-1"}},
		{BaseModel: datamodel.BaseModel{UUID: "policy-2"}},
		{BaseModel: datamodel.BaseModel{UUID: "policy-3"}},
	}

	mockStorage.On("ListBackupPoliciesWithPagination", ctx, [][]interface{}{}, pagination).
		Return(backupPolicies, nil)

	result, err := activity.GetBackupPolicies(ctx, pagination)

	assert.NoError(t, err)
	assert.Equal(t, 3, len(result))
	assert.Equal(t, "policy-1", result[0].UUID)
	mockStorage.AssertExpectations(t)
}

func TestGetBackupPolicies_Success_NilPagination(t *testing.T) {
	ctx := setupContext()
	mockStorage := database.NewMockStorage(t)
	activity := &UpdateBackupScheduleActivity{
		SE: mockStorage,
	}

	// Default pagination should be used (Offset: 0, Limit: 200)
	defaultPagination := &utils.Pagination{
		Offset: 0,
		Limit:  200,
	}

	backupPolicies := []*datamodel.BackupPolicy{
		{BaseModel: datamodel.BaseModel{UUID: "policy-1"}},
	}

	mockStorage.On("ListBackupPoliciesWithPagination", ctx, [][]interface{}{}, defaultPagination).
		Return(backupPolicies, nil)

	result, err := activity.GetBackupPolicies(ctx, nil)

	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "policy-1", result[0].UUID)
	mockStorage.AssertExpectations(t)
}

func TestGetBackupPolicies_Success_InvalidLimit(t *testing.T) {
	ctx := setupContext()
	mockStorage := database.NewMockStorage(t)
	activity := &UpdateBackupScheduleActivity{
		SE: mockStorage,
	}

	// Invalid pagination with limit <= 0 should default to 200
	pagination := &utils.Pagination{
		Offset: 10,
		Limit:  0,
	}

	// Expected pagination after default is applied
	expectedPagination := &utils.Pagination{
		Offset: 10,
		Limit:  200,
	}

	backupPolicies := []*datamodel.BackupPolicy{
		{BaseModel: datamodel.BaseModel{UUID: "policy-1"}},
	}

	mockStorage.On("ListBackupPoliciesWithPagination", ctx, [][]interface{}{}, expectedPagination).
		Return(backupPolicies, nil)

	result, err := activity.GetBackupPolicies(ctx, pagination)

	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))
	mockStorage.AssertExpectations(t)
}

func TestGetBackupPolicies_Success_EmptyResults(t *testing.T) {
	ctx := setupContext()
	mockStorage := database.NewMockStorage(t)
	activity := &UpdateBackupScheduleActivity{
		SE: mockStorage,
	}

	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  100,
	}

	mockStorage.On("ListBackupPoliciesWithPagination", ctx, [][]interface{}{}, pagination).
		Return([]*datamodel.BackupPolicy{}, nil)

	result, err := activity.GetBackupPolicies(ctx, pagination)

	assert.NoError(t, err)
	assert.Equal(t, 0, len(result))
	mockStorage.AssertExpectations(t)
}

func TestGetBackupPolicies_Error_DatabaseFailure(t *testing.T) {
	ctx := setupContext()
	mockStorage := database.NewMockStorage(t)
	activity := &UpdateBackupScheduleActivity{
		SE: mockStorage,
	}

	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  100,
	}

	dbError := errors.New("database connection failed")
	mockStorage.On("ListBackupPoliciesWithPagination", ctx, [][]interface{}{}, pagination).
		Return(nil, dbError)

	result, err := activity.GetBackupPolicies(ctx, pagination)

	assert.Error(t, err)
	assert.Nil(t, result)
	mockStorage.AssertExpectations(t)
}

func TestUpdateBackupScheduleTaskQueue_Success(t *testing.T) {
	ctx := setupContext()
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	activity := &UpdateBackupScheduleActivity{
		ScheduleClient: mockScheduleClient,
	}

	backupPolicyUUID := "policy-uuid-1"

	// Setup schedule description with workflow action
	scheduleDescription := &client.ScheduleDescription{
		Schedule: client.Schedule{
			Action: &client.ScheduleWorkflowAction{
				ID:        "workflow-id",
				TaskQueue: "old-task-queue",
				Workflow:  "backup-workflow",
				Args:      []interface{}{"arg1"},
			},
		},
	}

	mockScheduleClient.On("GetHandle", ctx, backupPolicyUUID).Return(mockScheduleHandle)
	mockScheduleHandle.On("Update", ctx, mock.Anything).Run(func(args mock.Arguments) {
		opts := args.Get(1).(client.ScheduleUpdateOptions)
		scheduleInput := client.ScheduleUpdateInput{
			Description: *scheduleDescription,
		}
		update, err := opts.DoUpdate(scheduleInput)
		assert.NoError(t, err)
		assert.NotNil(t, update)
		require.NotNil(t, update.Schedule)
		workflowAction, ok := update.Schedule.Action.(*client.ScheduleWorkflowAction)
		require.True(t, ok, "updated action must be a ScheduleWorkflowAction")
		assert.Equal(t, workflowEngine.BackgroundTaskQueue, workflowAction.TaskQueue)
		assert.Equal(t, "workflow-id", workflowAction.ID)
		assert.Equal(t, "backup-workflow", workflowAction.Workflow)
		assert.Equal(t, workflowEngine.ScheduledBackupWorkflowExecutionTimeout, workflowAction.WorkflowExecutionTimeout)
	}).Return(nil)

	err := activity.UpdateBackupScheduleTaskQueue(ctx, backupPolicyUUID)

	assert.NoError(t, err)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestUpdateBackupScheduleTaskQueue_Error_UpdateFailure(t *testing.T) {
	ctx := setupContext()
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	activity := &UpdateBackupScheduleActivity{
		ScheduleClient: mockScheduleClient,
	}

	backupPolicyUUID := "policy-uuid-1"

	updateError := errors.New("failed to update schedule")
	mockScheduleClient.On("GetHandle", ctx, backupPolicyUUID).Return(mockScheduleHandle)
	mockScheduleHandle.On("Update", ctx, mock.Anything).Return(updateError)

	err := activity.UpdateBackupScheduleTaskQueue(ctx, backupPolicyUUID)

	assert.Error(t, err)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestUpdateBackupScheduleTaskQueue_Success_NoAction(t *testing.T) {
	ctx := setupContext()
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	activity := &UpdateBackupScheduleActivity{
		ScheduleClient: mockScheduleClient,
	}

	backupPolicyUUID := "policy-uuid-1"

	// Schedule without action
	scheduleDescription := &client.ScheduleDescription{
		Schedule: client.Schedule{
			Action: nil,
		},
	}

	mockScheduleClient.On("GetHandle", ctx, backupPolicyUUID).Return(mockScheduleHandle)
	mockScheduleHandle.On("Update", ctx, mock.Anything).Run(func(args mock.Arguments) {
		opts := args.Get(1).(client.ScheduleUpdateOptions)
		scheduleInput := client.ScheduleUpdateInput{
			Description: *scheduleDescription,
		}
		update, err := opts.DoUpdate(scheduleInput)
		assert.NoError(t, err)
		assert.NotNil(t, update)
		// Action should remain nil
		assert.Nil(t, update.Schedule.Action)
	}).Return(nil)

	err := activity.UpdateBackupScheduleTaskQueue(ctx, backupPolicyUUID)

	assert.NoError(t, err)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

func TestUpdateBackupScheduleTaskQueue_Success_PreservesOtherFields(t *testing.T) {
	ctx := setupContext()
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	activity := &UpdateBackupScheduleActivity{
		ScheduleClient: mockScheduleClient,
	}

	backupPolicyUUID := "policy-uuid-1"

	originalWorkflowID := "original-workflow-id"
	originalWorkflow := "backup-workflow"
	originalArgs := []interface{}{"arg1", "arg2"}

	scheduleDescription := &client.ScheduleDescription{
		Schedule: client.Schedule{
			Action: &client.ScheduleWorkflowAction{
				ID:        originalWorkflowID,
				TaskQueue: "old-task-queue",
				Workflow:  originalWorkflow,
				Args:      originalArgs,
			},
		},
	}

	mockScheduleClient.On("GetHandle", ctx, backupPolicyUUID).Return(mockScheduleHandle)
	mockScheduleHandle.On("Update", ctx, mock.Anything).Run(func(args mock.Arguments) {
		opts := args.Get(1).(client.ScheduleUpdateOptions)
		scheduleInput := client.ScheduleUpdateInput{
			Description: *scheduleDescription,
		}
		update, err := opts.DoUpdate(scheduleInput)
		assert.NoError(t, err)
		assert.NotNil(t, update)
		require.NotNil(t, update.Schedule)
		workflowAction, ok := update.Schedule.Action.(*client.ScheduleWorkflowAction)
		require.True(t, ok, "updated action must be a ScheduleWorkflowAction")
		// TaskQueue and execution timeout should be updated
		assert.Equal(t, workflowEngine.BackgroundTaskQueue, workflowAction.TaskQueue)
		assert.Equal(t, workflowEngine.ScheduledBackupWorkflowExecutionTimeout, workflowAction.WorkflowExecutionTimeout)
		// Other fields should be preserved
		assert.Equal(t, originalWorkflowID, workflowAction.ID)
		assert.Equal(t, originalWorkflow, workflowAction.Workflow)
		assert.Equal(t, originalArgs, workflowAction.Args)
	}).Return(nil)

	err := activity.UpdateBackupScheduleTaskQueue(ctx, backupPolicyUUID)

	assert.NoError(t, err)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}

// Existing schedules were created with divergent (or unset) execution timeouts, so the update path
// must always rewrite the action to the standardized value shared with the schedule creation path.
func TestUpdateBackupScheduleTaskQueue_StandardizesWorkflowExecutionTimeout(t *testing.T) {
	tests := []struct {
		name            string
		existingTimeout time.Duration
	}{
		{"unset timeout", 0},
		{"shorter timeout", 1 * time.Hour},
		{"longer timeout", 48 * time.Hour},
		{"already standardized", workflowEngine.ScheduledBackupWorkflowExecutionTimeout},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := setupContext()
			mockScheduleClient := &mocks.ScheduleClient{}
			mockScheduleHandle := &mocks.ScheduleHandle{}
			activity := &UpdateBackupScheduleActivity{
				ScheduleClient: mockScheduleClient,
			}

			backupPolicyUUID := "policy-uuid-1"

			scheduleDescription := &client.ScheduleDescription{
				Schedule: client.Schedule{
					Action: &client.ScheduleWorkflowAction{
						ID:                       "workflow-id",
						TaskQueue:                "old-task-queue",
						Workflow:                 "CreateScheduledBackupInitWorkflow",
						Args:                     []interface{}{"arg1"},
						WorkflowExecutionTimeout: tt.existingTimeout,
					},
				},
			}

			mockScheduleClient.On("GetHandle", ctx, backupPolicyUUID).Return(mockScheduleHandle)
			mockScheduleHandle.On("Update", ctx, mock.Anything).Run(func(args mock.Arguments) {
				opts := args.Get(1).(client.ScheduleUpdateOptions)
				update, err := opts.DoUpdate(client.ScheduleUpdateInput{Description: *scheduleDescription})
				require.NoError(t, err)
				require.NotNil(t, update)
				require.NotNil(t, update.Schedule)

				workflowAction, ok := update.Schedule.Action.(*client.ScheduleWorkflowAction)
				require.True(t, ok, "updated action must be a ScheduleWorkflowAction")
				assert.Equal(t, workflowEngine.ScheduledBackupWorkflowExecutionTimeout, workflowAction.WorkflowExecutionTimeout)
			}).Return(nil)

			err := activity.UpdateBackupScheduleTaskQueue(ctx, backupPolicyUUID)

			assert.NoError(t, err)
			mockScheduleClient.AssertExpectations(t)
			mockScheduleHandle.AssertExpectations(t)
		})
	}
}

func TestGetBackupPolicies_Error_WrappedAsTemporalError(t *testing.T) {
	ctx := setupContext()
	mockStorage := database.NewMockStorage(t)
	activity := &UpdateBackupScheduleActivity{
		SE: mockStorage,
	}

	pagination := &utils.Pagination{
		Offset: 0,
		Limit:  100,
	}

	dbError := errors.New("database error")
	mockStorage.On("ListBackupPoliciesWithPagination", ctx, [][]interface{}{}, pagination).
		Return(nil, dbError)

	result, err := activity.GetBackupPolicies(ctx, pagination)

	assert.Error(t, err)
	assert.Nil(t, result)
	mockStorage.AssertExpectations(t)
}

func TestUpdateBackupScheduleTaskQueue_Error_WrappedAsTemporalError(t *testing.T) {
	ctx := setupContext()
	mockScheduleClient := &mocks.ScheduleClient{}
	mockScheduleHandle := &mocks.ScheduleHandle{}
	activity := &UpdateBackupScheduleActivity{
		ScheduleClient: mockScheduleClient,
	}

	backupPolicyUUID := "policy-uuid-1"

	updateError := errors.New("update failed")
	mockScheduleClient.On("GetHandle", ctx, backupPolicyUUID).Return(mockScheduleHandle)
	mockScheduleHandle.On("Update", ctx, mock.Anything).Return(updateError)

	err := activity.UpdateBackupScheduleTaskQueue(ctx, backupPolicyUUID)

	assert.Error(t, err)
	mockScheduleClient.AssertExpectations(t)
	mockScheduleHandle.AssertExpectations(t)
}
