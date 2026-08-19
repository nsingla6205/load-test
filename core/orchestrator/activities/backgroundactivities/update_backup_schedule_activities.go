package backgroundactivities

import (
	"context"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	workflowEngine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/client"
)

var (
	DefaultBackupPolicyBatchSize = 200
)

// UpdateBackupScheduleActivity represents activities related to updating backup schedules.
type UpdateBackupScheduleActivity struct {
	SE             database.Storage
	ScheduleClient client.ScheduleClient
}

// GetBackupPolicies retrieves a batch of backup policies with pagination support.
// Returns a slice of BackupPolicy objects for the specified offset and limit.
func (a *UpdateBackupScheduleActivity) GetBackupPolicies(ctx context.Context, pagination *utils.Pagination) ([]*datamodel.BackupPolicy, error) {
	logger := util.GetLogger(ctx)
	se := a.SE

	// Use default batch size if not specified
	if pagination == nil {
		pagination = &utils.Pagination{
			Offset: 0,
			Limit:  DefaultBackupPolicyBatchSize,
		}
	} else if pagination.Limit <= 0 {
		pagination.Limit = DefaultBackupPolicyBatchSize
	}

	// Fetch backup policies with pagination
	conditions := [][]interface{}{}
	backupPolicies, err := se.ListBackupPoliciesWithPagination(ctx, conditions, pagination)
	if err != nil {
		logger.Errorf("Failed to fetch backup policies: %v", err)
		return nil, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Retrieved backup policies batch: offset=%d, limit=%d, returned=%d",
		pagination.Offset, pagination.Limit, len(backupPolicies))

	return backupPolicies, nil
}

// UpdateBackupScheduleTaskQueue updates the task queue for a temporal schedule identified by the backup policy UUID.
// The new task queue is read from the vcp-background-worker-task-queue environment variable.
func (a *UpdateBackupScheduleActivity) UpdateBackupScheduleTaskQueue(ctx context.Context, backupPolicyUUID string) error {
	logger := util.GetLogger(ctx)

	// Get the schedule handle
	scheduleHandler := a.ScheduleClient.GetHandle(ctx, backupPolicyUUID)

	// Update the schedule with the new task queue
	err := scheduleHandler.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(schedule client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			// Update the task queue in the Action
			if schedule.Description.Schedule.Action != nil {
				if workflowAction, ok := schedule.Description.Schedule.Action.(*client.ScheduleWorkflowAction); ok {
					// Create a new Action with updated TaskQueue while preserving other fields
					updatedAction := &client.ScheduleWorkflowAction{
						ID:                       workflowAction.ID,
						TaskQueue:                workflowEngine.BackgroundTaskQueue,
						Workflow:                 workflowAction.Workflow,
						Args:                     workflowAction.Args,
						WorkflowExecutionTimeout: workflowEngine.ScheduledBackupWorkflowExecutionTimeout,
					}
					schedule.Description.Schedule.Action = updatedAction
				}
			}
			return &client.ScheduleUpdate{
				Schedule: &schedule.Description.Schedule,
			}, nil
		},
	})

	if err != nil {
		logger.Errorf("Failed to update task queue for schedule %s: %v", backupPolicyUUID, err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Infof("Successfully updated task queue to %s for schedule %s", workflowEngine.BackgroundTaskQueue, backupPolicyUUID)
	return nil
}
