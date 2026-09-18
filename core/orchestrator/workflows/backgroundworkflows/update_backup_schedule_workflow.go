package backgroundworkflows

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	databaseUtils "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// updateBackupScheduleWorkflow implements the WorkflowInterface for updating backup schedule task queues.
type updateBackupScheduleWorkflow struct {
	workflows.BaseWorkflow
}

// Enforcing the WorkflowInterface on updateBackupScheduleWorkflow
var _ workflows.WorkflowInterface = &updateBackupScheduleWorkflow{}

// UpdateBackupScheduleWorkflow is the entry point for the backup schedule update workflow.
// It sets up the workflow and runs the main backup schedule update logic.
func UpdateBackupScheduleWorkflow(ctx workflow.Context) error {
	updateBackupScheduleWF := new(updateBackupScheduleWorkflow)

	err := updateBackupScheduleWF.Setup(ctx, nil)
	if err != nil {
		return err
	}
	updateBackupScheduleWF.Status = workflows.WorkflowStatusRunning

	_, customErr := updateBackupScheduleWF.Run(ctx)
	if customErr != nil {
		updateBackupScheduleWF.Status = workflows.WorkflowStatusFailed
		return customErr
	}
	updateBackupScheduleWF.Status = workflows.WorkflowStatusCompleted
	return nil
}

// Setup initializes the workflow context, logger, and query handlers.
// It must be called before running the workflow logic.
func (wf *updateBackupScheduleWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.Status = workflows.WorkflowStatusCreated
	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

// Run executes the main backup schedule update logic: paginates through backup policies and updates their task queues.
func (wf *updateBackupScheduleWorkflow) Run(ctx workflow.Context, _ ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting UpdateBackupScheduleWorkflow")

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logger.Error("Failed to populate retry policy params", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrWorkflowConfigurationError, err)
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Create activity instance
	updateBackupScheduleActivity := &backgroundactivities.UpdateBackupScheduleActivity{}

	// Paginate through backup policies
	offset := 0
	for {
		pagination := &databaseUtils.Pagination{
			Offset: offset,
			Limit:  backgroundactivities.DefaultBackupPolicyBatchSize,
		}

		// Store the output from GetBackupPolicies in a local variable
		var backupPolicies []*datamodel.BackupPolicy
		err = workflow.ExecuteActivity(ctx, updateBackupScheduleActivity.GetBackupPolicies, pagination).Get(ctx, &backupPolicies)
		if err != nil {
			logger.Error("Failed to get backup policies", "offset", offset, "error", err)
			return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("failed to get backup policies: %w", err))
		}

		// If no more policies returned, break the loop
		if len(backupPolicies) == 0 {
			logger.Info("No more backup policies to process", "completedOffset", offset)
			break
		}

		logger.Info("Retrieved backup policies", "count", len(backupPolicies), "offset", offset)

		// Update task queue for each backup policy schedule
		for _, backupPolicy := range backupPolicies {
			err = workflow.ExecuteActivity(ctx, updateBackupScheduleActivity.UpdateBackupScheduleTaskQueue, backupPolicy.UUID).Get(ctx, nil)
			if err != nil {
				logger.Error("Failed to update task queue for backup policy", "backupPolicyUUID", backupPolicy.UUID, "error", err)
				// Continue processing other policies even if one fails
			}
		}

		// If we got fewer policies than the batch size, we've reached the end
		if len(backupPolicies) < backgroundactivities.DefaultBackupPolicyBatchSize {
			logger.Info("Reached end of backup policies", "totalProcessed", offset+len(backupPolicies))
			break
		}

		// Move to next batch
		offset += backgroundactivities.DefaultBackupPolicyBatchSize
	}

	logger.Info("UpdateBackupScheduleWorkflow completed successfully")
	return nil, nil
}
