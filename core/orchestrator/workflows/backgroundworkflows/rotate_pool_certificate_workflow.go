package backgroundworkflows

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// RotatePoolCertificateWorkflow is a child workflow that handles certificate rotation for a single pool.
// It only rotates when the certificate is actually due (remaining-lifetime and issuing-CA checks pass).
func RotatePoolCertificateWorkflow(ctx workflow.Context, poolUUID string) error {
	return rotatePoolCertificate(ctx, poolUUID, false, "")
}

// ForceRotatePoolCertificateWorkflow rotates the pool certificate without consulting the
// remaining-lifetime eligibility check, and without chaining password rotation.
//
// It exists for on-demand operator and CD verification runs where a freshly issued
// certificate would never be considered due for rotation. It is gated by
// ENABLE_FORCED_CERTIFICATE_ROTATION on the entry workflow
// (ForcePoolCertificateRotationWorkflow) so it cannot be triggered on environments
// that have not opted in.
func ForceRotatePoolCertificateWorkflow(ctx workflow.Context, poolUUID string) error {
	return rotatePoolCertificate(ctx, poolUUID, true, "")
}

// TrackedForceRotatePoolCertificateWorkflow rotates a pool and updates its child job.
func TrackedForceRotatePoolCertificateWorkflow(ctx workflow.Context, poolUUID, childJobID string) error {
	return rotatePoolCertificate(ctx, poolUUID, true, childJobID)
}

// rotatePoolCertificate holds the shared certificate rotation logic for both the
// scheduled and certificate-only forced entry points.
func rotatePoolCertificate(
	ctx workflow.Context,
	poolUUID string,
	forceCertRotationOnly bool,
	childJobID string,
) (workflowErr error) {
	ctx, logger := withPoolOperationLogging(ctx, poolUUID, poolOperationCertificateRotation)
	logger.Info("Pool certificate rotation started", "forceCertRotationOnly", forceCertRotationOnly)
	defer func() {
		if workflowErr != nil {
			logger.Error("Pool certificate rotation finished", "outcome", "failure", "error", workflowErr)
			return
		}
		logger.Info("Pool certificate rotation finished", "outcome", "success")
	}()

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
	if childJobID != "" {
		if err := updateCertificateMaintenanceJob(ctx, childJobID, datamodel.JobsStatePROCESSING, nil); err != nil {
			return fmt.Errorf("mark forced pool rotation job processing: %w", err)
		}
		defer func() {
			workflowErr = finishCertificateMaintenanceJob(ctx, childJobID, workflowErr)
		}()
	}

	// Initialize the certificate rotation activity
	certificateActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}

	// Step 0: Get pool context once to avoid redundant database queries
	var poolContext *backgroundactivities.PoolContext
	err = workflow.ExecuteActivity(ctx, certificateActivity.GetPoolContext, poolUUID).Get(ctx, &poolContext)
	if err != nil {
		logger.Error("GetPoolContext activity failed", "error", err)
		return err
	}

	// Validate poolContext to prevent nil pointer dereference
	if poolContext == nil || poolContext.Pool == nil {
		logger.Error("PoolContext or Pool is nil after GetPoolContext", "poolUUID", poolUUID)
		return fmt.Errorf("pool context or pool is nil for pool UUID: %s", poolUUID)
	}

	// Helper function to safely get pool name
	getPoolName := func() string {
		if poolContext != nil && poolContext.Pool != nil {
			return poolContext.Pool.Name
		}
		return "unknown"
	}

	// Step 1: Check if certificate rotation is needed before proceeding.
	// A forced run skips this gate so that an on-demand rotation is observable even when
	// the current certificate still has most of its lifetime left. The activity re-derives
	// eligibility on its own, so the decision has to travel with the pool context rather
	// than stopping at the workflow boundary.
	if forceCertRotationOnly {
		poolContext.Force = true
		logger.Info("Forced rotation requested, skipping remaining-lifetime eligibility check", "poolUUID", poolUUID, "poolName", getPoolName())
	} else {
		var needsRotation bool
		err = workflow.ExecuteActivity(ctx, certificateActivity.CertificateNeedsRotation, poolUUID).Get(ctx, &needsRotation)
		if err != nil {
			logger.Error("CertificateNeedsRotation activity failed", "poolUUID", poolUUID, "poolName", getPoolName(), "error", err)
			return err
		}

		if !needsRotation {
			logger.Info("Certificate does not need rotation, skipping both certificate and password rotation", "poolUUID", poolUUID, "poolName", getPoolName())
			return nil
		}

		logger.Info("Certificate needs rotation, proceeding with certificate rotation", "poolUUID", poolUUID, "poolName", getPoolName())
	}

	// Step 2: Execute certificate rotation with pre-fetched pool context
	logger.Info("Starting certificate rotation", "poolUUID", poolUUID, "poolName", getPoolName())
	err = workflow.ExecuteActivity(ctx, certificateActivity.RotatePoolCertificateWithContext, poolContext).Get(ctx, nil)
	if err != nil {
		logger.Error("Certificate rotation failed", "poolUUID", poolUUID, "poolName", getPoolName(), "error", err)
		// Emit Prometheus metric for certificate rotation failure
		errorType := "unknown"
		if err != nil {
			errorType = err.Error()
		}
		_ = workflow.ExecuteActivity(ctx, backgroundactivities.EmitCertificateRotationFailureMetric, poolUUID, getPoolName(), "certificate_rotation", errorType).Get(ctx, nil)
		// A forced run is an explicit, single-purpose request: surface the failure to the
		// caller instead of masking it behind an unrelated password rotation.
		if forceCertRotationOnly {
			return err
		}
		// Even if certificate rotation fails, we should still try password rotation
		// because the certificate was due for rotation
		logger.Warn("Certificate rotation failed, but will still attempt password rotation since certificate was due for rotation", "poolUUID", poolUUID, "poolName", getPoolName())
	} else {
		logger.Info("Certificate rotation completed successfully", "poolUUID", poolUUID, "poolName", getPoolName())
	}

	// A forced run rotates the certificate only. The certificate was not due, so the
	// password is not due either and chaining password rotation would change credentials
	// the caller never asked to touch.
	if forceCertRotationOnly {
		logger.Info("Forced certificate rotation completed, skipping password rotation", "poolUUID", poolUUID, "poolName", getPoolName())
		return nil
	}

	// Step 3: Password rotation for certificate pools (AuthType USER_CERTIFICATE)
	// This happens regardless of whether certificate rotation succeeded or failed,
	// as long as the certificate was due for rotation

	// Re-check pool state before password rotation to handle race conditions where
	// pool delete might have been triggered after the initial state check
	var updatedPoolContext *backgroundactivities.PoolContext
	err = workflow.ExecuteActivity(ctx, certificateActivity.GetPoolContext, poolUUID).Get(ctx, &updatedPoolContext)
	if err != nil {
		logger.Warn("Failed to re-fetch pool context before password rotation, proceeding with cached context", "poolUUID", poolUUID, "error", err)
		updatedPoolContext = poolContext // Fallback to cached context
	} else if updatedPoolContext != nil && updatedPoolContext.Pool != nil {
		// Check if pool state has changed to DELETING or CREATING
		if updatedPoolContext.Pool.State == "DELETING" {
			logger.Warn("Pool is in DELETING state, skipping password rotation to avoid conflicts with pool deletion", "poolUUID", poolUUID, "poolName", updatedPoolContext.Pool.Name)
			return nil
		}
		if updatedPoolContext.Pool.State == "CREATING" {
			logger.Warn("Pool is in CREATING state, skipping password rotation", "poolUUID", poolUUID, "poolName", updatedPoolContext.Pool.Name)
			return nil
		}
		logger.Info("Pool state verified before password rotation", "poolUUID", poolUUID, "poolName", updatedPoolContext.Pool.Name, "state", updatedPoolContext.Pool.State)
	}

	logger.Info("Starting password rotation for certificate pool (certificate was due for rotation)", "poolUUID", poolUUID, "poolName", getPoolName())

	// Execute password rotation child workflow for certificate pools
	// The password rotation workflow will check environment variables internally
	passwordCtx := withInheritedPoolOperationCorrelation(ctx)
	passwordWorkflowFuture := workflow.ExecuteChildWorkflow(passwordCtx, RotatePoolPasswordWorkflow, poolUUID)
	err = passwordWorkflowFuture.Get(passwordCtx, nil)
	if err != nil {
		logger.Error("Password rotation failed for certificate pool", "poolUUID", poolUUID, "poolName", getPoolName(), "error", err)
		// Emit Prometheus metric for password rotation failure
		errorType := "unknown"
		if err != nil {
			errorType = err.Error()
		}
		_ = workflow.ExecuteActivity(ctx, backgroundactivities.EmitPasswordRotationFailureMetric, poolUUID, getPoolName(), "password_rotation", errorType).Get(ctx, nil)
		// Don't fail the entire workflow if password rotation fails
		// Log the error but continue
		logger.Warn("Password rotation failed, but certificate rotation workflow will complete", "poolUUID", poolUUID, "poolName", getPoolName())
	} else {
		logger.Info("Password rotation completed successfully for certificate pool", "poolUUID", poolUUID, "poolName", getPoolName())
	}

	return nil
}

// CertificateGenerationResponse represents the response from certificate generation
type CertificateGenerationResponse struct {
	CertificateID string      `json:"certificate_id"`
	SecretID      string      `json:"secret_id"`
	Certificate   interface{} `json:"certificate"`
	Secret        interface{} `json:"secret"`
}
