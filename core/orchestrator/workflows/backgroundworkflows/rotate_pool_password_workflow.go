package backgroundworkflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RotatePoolPasswordWorkflow is a child workflow that handles password rotation for a single pool
func RotatePoolPasswordWorkflow(ctx workflow.Context, poolUUID string) (workflowErr error) {
	ctx, logger := withPoolOperationLogging(ctx, poolUUID, poolOperationPasswordRotation)
	logger.Info("Pool password rotation started")
	defer func() {
		if workflowErr != nil {
			logger.Error("Pool password rotation finished", "outcome", "failure", "error", workflowErr)
			return
		}
		logger.Info("Pool password rotation finished", "outcome", "success")
	}()

	// Check if password rotation is enabled via feature flag
	passwordRotationEnabled := env.GetBool("ENABLE_VSA_PASSWORD_ROTATION", false)
	if !passwordRotationEnabled {
		logger.Info("Password rotation is disabled via ENABLE_VSA_PASSWORD_ROTATION=false", "poolUUID", poolUUID)
		return nil
	}

	// Set up activity options with retry policy for rotation activities
	// Rotation activities can be retried once (MaximumAttempts = 2: 1 initial attempt + 1 retry)
	// Activity timeout is set to 5 minutes
	retryPolicy, err := workflows.PopulateRotationRetryPolicyParams()
	if err != nil {
		return err
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

	// Initialize the password rotation activity
	passwordActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}

	// Step 0: Get pool context once to avoid redundant database queries
	logger.Info("Step 0: Getting pool context to avoid redundant database queries", "poolUUID", poolUUID)
	var poolContext *backgroundactivities.PoolContext
	err = workflow.ExecuteActivity(ctx, passwordActivity.GetPoolContext, poolUUID).Get(ctx, &poolContext)
	if err != nil {
		logger.Error("GetPoolContext activity failed", "error", err, "poolUUID", poolUUID)
		return err
	}

	// Step 1: Execute password rotation with pre-fetched pool context
	logger.Info("Step 1: Executing password rotation with pool context", "poolUUID", poolUUID)
	err = workflow.ExecuteActivity(ctx, passwordActivity.RotatePoolPasswordWithContext, poolContext).Get(ctx, nil)
	if err != nil {
		logger.Error("RotatePoolPasswordWithContext activity failed", "error", err, "poolUUID", poolUUID)
		return err
	}

	return nil
}
