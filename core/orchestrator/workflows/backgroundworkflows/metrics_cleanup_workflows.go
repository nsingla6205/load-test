package backgroundworkflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// CleanupHydratedMetricsTableWorkflow performs cleanup of hydrated_metrics records older than 1 day
func CleanupHydratedMetricsTableWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting CleanupHydratedMetricsTableWorkflow")

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logger.Error("Failed to populate retry policy params", "error", err)
		return err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.StartToCloseTimeout / 2, // For progress reporting
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
	metricsCleanupActivity := &backgroundactivities.MetricsCleanupActivity{}

	// Execute the hydrated metrics table cleanup activity
	err = workflow.ExecuteActivity(ctx, metricsCleanupActivity.CleanupHydratedMetricsTableActivity).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to execute hydrated metrics table cleanup", "error", err)
		return err
	}

	logger.Info("CleanupHydratedMetricsTableWorkflow completed successfully")
	return nil
}

// CleanupAggregatedUsageTableWorkflow performs cleanup of aggregated_usage records older than the configured retention period.
func CleanupAggregatedUsageTableWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting CleanupAggregatedUsageTableWorkflow")

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logger.Error("Failed to populate retry policy params", "error", err)
		return err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.StartToCloseTimeout / 2, // For progress reporting
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
	metricsCleanupActivity := &backgroundactivities.MetricsCleanupActivity{}

	// Execute the aggregated usage table cleanup activity
	err = workflow.ExecuteActivity(ctx, metricsCleanupActivity.CleanupAggregatedUsageTableActivity).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to execute aggregated usage table cleanup", "error", err)
		return err
	}

	logger.Info("CleanupAggregatedUsageTableWorkflow completed successfully")
	return nil
}

// CleanupJobsTableWorkflow performs cleanup of jobs records older than 1 day
func CleanupJobsTableWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting CleanupJobsTableWorkflow")

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logger.Error("Failed to populate retry policy params", "error", err)
		return err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.StartToCloseTimeout / 2, // For progress reporting
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
	metricsCleanupActivity := &backgroundactivities.MetricsCleanupActivity{}

	// Execute the jobs table cleanup activity
	err = workflow.ExecuteActivity(ctx, metricsCleanupActivity.CleanupJobsTableActivity).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to execute jobs table cleanup", "error", err)
		return err
	}

	logger.Info("CleanupJobsTableWorkflow completed successfully")
	return nil
}

// CleanupBackupChainHistoryWorkflow performs cleanup of tombstoned backup chain history
// records older than BackupChainHistoryRetentionDays.
func CleanupBackupChainHistoryWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Starting CleanupBackupChainHistoryWorkflow")

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logger.Error("Failed to populate retry policy params", "error", err)
		return err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.StartToCloseTimeout / 2, // For progress reporting
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
	metricsCleanupActivity := &backgroundactivities.MetricsCleanupActivity{}

	// Execute the backup chain history cleanup activity
	err = workflow.ExecuteActivity(ctx, metricsCleanupActivity.CleanupBackupChainHistoryActivity).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to execute backup chain history cleanup", "error", err)
		return err
	}

	logger.Info("CleanupBackupChainHistoryWorkflow completed successfully")
	return nil
}
