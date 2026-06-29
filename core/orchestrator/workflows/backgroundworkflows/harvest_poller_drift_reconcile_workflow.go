package backgroundworkflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var enableHarvestPollerDriftReconcile = env.GetBool("ENABLE_HARVEST_POLLER_DRIFT_RECONCILE", false)

// HarvestPollerDriftReconcileWorkflow is the scheduled entrypoint for Harvest poller drift reconciliation.
// Current implementation is a safe scaffold so the schedule and cron configuration can be wired first.
func HarvestPollerDriftReconcileWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("harvest drift reconcile workflow started")
	if !enableHarvestPollerDriftReconcile {
		logger.Info("harvest drift reconcile workflow skipped; feature flag disabled")
		return nil
	}
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logger.Error("harvest drift reconcile workflow failed to build retry policy", "error", err)
		return err
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: retryPolicy.StartToCloseTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	activity := &activities.HarvestPollerDriftReconcileActivity{}
	err = workflow.ExecuteActivity(ctx, activity.ReconcileHarvestPollerDrift).Get(ctx, nil)
	if err != nil {
		logger.Error("harvest drift reconcile workflow activity failed", "error", err)
		return err
	}
	logger.Info("harvest drift reconcile workflow completed successfully")
	return nil
}
