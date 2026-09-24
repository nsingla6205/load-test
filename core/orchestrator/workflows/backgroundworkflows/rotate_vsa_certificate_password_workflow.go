package backgroundworkflows

import (
	"fmt"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	// CertificateRotationChildWorkflowTimeout is the timeout for each certificate rotation child workflow
	// Default: 5 minutes
	CertificateRotationChildWorkflowTimeout = time.Duration(env.GetInt("CERTIFICATE_ROTATION_CHILD_WORKFLOW_TIMEOUT_MINUTES", 5)) * time.Minute

	// PasswordRotationChildWorkflowTimeout is the timeout for each password rotation child workflow
	// Default: 5 minutes
	PasswordRotationChildWorkflowTimeout = time.Duration(env.GetInt("PASSWORD_ROTATION_CHILD_WORKFLOW_TIMEOUT_MINUTES", 5)) * time.Minute

	// CertificateRotationBatchSize is the batch size for listing and queueing certificate rotation (pools per list call).
	// Default: 10 pools per batch
	CertificateRotationBatchSize = env.GetInt("CERTIFICATE_ROTATION_BATCH_SIZE", 10)

	// PasswordRotationBatchSize is the batch size for listing and queueing password rotation (pools per list call).
	// Default: 10 pools per batch
	PasswordRotationBatchSize = env.GetInt("PASSWORD_ROTATION_BATCH_SIZE", 10)
)

// RotateVsaCertificateAndPasswordWorkflow rotates certificates and passwords used for VSA communication
// This workflow uses the global workflow timeout (WORKFLOW_GLOBAL_TIMEOUT_MINUTES) like other workflows.
// Pool listing is batched to avoid Temporal activity result size limit.
func RotateVsaCertificateAndPasswordWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	// Read environment variables at runtime for better testability
	certificateRotationEnabled := env.GetBool("ENABLE_VSA_CERTIFICATE_ROTATION", false)
	passwordRotationEnabled := env.GetBool("ENABLE_VSA_PASSWORD_ROTATION", false)
	authType1PasswordRotationEnabled := env.GetBool("ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION", false)

	if !certificateRotationEnabled && !passwordRotationEnabled {
		logger.Debug("Certificate and password rotation disabled. Skipping workflow execution.")
		return nil
	}

	// Set up activity options with retry policy for rotation activities
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

	rotateCertificateActivity := &backgroundactivities.RotateVcpToVsaCertificateActivity{}
	controlWorkflowActivity := &backgroundactivities.ControlWorkflowActivity{}

	certBatchSize := CertificateRotationBatchSize
	if certBatchSize <= 0 {
		certBatchSize = 10
	}
	passwordBatchSize := PasswordRotationBatchSize
	if passwordBatchSize <= 0 {
		passwordBatchSize = 10
	}

	totalCertPoolsProcessed := 0
	totalPasswordPoolsProcessed := 0
	certificateRotationFailed := false
	passwordRotationFailed := false

	// Certificate rotation: list and process in batches (avoids Temporal activity result size limit)
	if certificateRotationEnabled {
		logger.Info("Starting certificate rotation for auth type 2 pools (batched listing)")
		certOffset := 0
		certBatchNumber := 0
		for {
			certBatchNumber++
			var certBatchResult *backgroundactivities.ListPoolsBatchResult
			certListFuture := workflow.ExecuteActivity(ctx, rotateCertificateActivity.ListPoolsWithCertificateAuth, certOffset, certBatchSize)
			if err := certListFuture.Get(ctx, &certBatchResult); err != nil {
				logger.Error("ListPoolsWithCertificateAuth failed", "error", err)
				return fmt.Errorf("certificate pool listing: %w", err)
			}
			certPools := certBatchResult.Pools
			hasMoreCert := certBatchResult.HasMore
			if len(certPools) == 0 && certBatchNumber == 1 {
				logger.Info("No pools with certificate authentication found. Skipping certificate rotation.")
				break
			}
			logger.Info("Certificate pool batch retrieved", "batchNumber", certBatchNumber, "count", len(certPools), "hasMore", hasMoreCert)

			if len(certPools) > 0 {
				var batchFutures []workflow.Future
				var batchPoolUUIDs []string
				for _, pool := range certPools {
					future := workflow.ExecuteActivity(ctx, controlWorkflowActivity.ExecutePoolCertificateRotationSequentially,
						pool.UUID, CertificateRotationChildWorkflowTimeout)
					batchFutures = append(batchFutures, future)
					batchPoolUUIDs = append(batchPoolUUIDs, pool.UUID)
				}
				for batchIndex, future := range batchFutures {
					if err := future.Get(ctx, nil); err != nil {
						certificateRotationFailed = true
						logger.Error("Failed to queue certificate rotation for pool", "poolUUID", batchPoolUUIDs[batchIndex], "error", err)
					}
				}
				totalCertPoolsProcessed += len(certPools)
			}
			if !hasMoreCert {
				break
			}
			certOffset += certBatchSize
		}
		if totalCertPoolsProcessed > 0 {
			logger.Info("Certificate rotation list/queue complete", "totalPools", totalCertPoolsProcessed)
		}
	} else {
		logger.Debug("Certificate rotation disabled. Skipping certificate rotation.")
	}

	// Password rotation: list and process in batches (avoids Temporal activity result size limit)
	if passwordRotationEnabled && authType1PasswordRotationEnabled {
		logger.Info("Starting password rotation for auth type 1 pools (batched listing)")
		passwordOffset := 0
		passwordBatchNumber := 0
		for {
			passwordBatchNumber++
			var passwordBatchResult *backgroundactivities.ListPoolsBatchResult
			passwordListFuture := workflow.ExecuteActivity(ctx, rotateCertificateActivity.ListPoolsWithPasswordAuth, passwordOffset, passwordBatchSize)
			if err := passwordListFuture.Get(ctx, &passwordBatchResult); err != nil {
				logger.Error("ListPoolsWithPasswordAuth failed", "error", err)
				return fmt.Errorf("password pool listing: %w", err)
			}
			passwordPools := passwordBatchResult.Pools
			hasMorePassword := passwordBatchResult.HasMore
			if len(passwordPools) == 0 && passwordBatchNumber == 1 {
				logger.Info("No pools with password authentication found. Skipping password rotation.")
				break
			}
			logger.Info("Password pool batch retrieved", "batchNumber", passwordBatchNumber, "count", len(passwordPools), "hasMore", hasMorePassword)

			if len(passwordPools) > 0 {
				var batchFutures []workflow.Future
				var batchPoolUUIDs []string
				for _, pool := range passwordPools {
					future := workflow.ExecuteActivity(ctx, controlWorkflowActivity.ExecutePoolPasswordRotationSequentially,
						pool.UUID, PasswordRotationChildWorkflowTimeout)
					batchFutures = append(batchFutures, future)
					batchPoolUUIDs = append(batchPoolUUIDs, pool.UUID)
				}
				for batchIndex, future := range batchFutures {
					if err := future.Get(ctx, nil); err != nil {
						passwordRotationFailed = true
						logger.Error("Failed to queue password rotation for pool", "poolUUID", batchPoolUUIDs[batchIndex], "error", err)
					}
				}
				totalPasswordPoolsProcessed += len(passwordPools)
			}
			if !hasMorePassword {
				break
			}
			passwordOffset += passwordBatchSize
		}
		if totalPasswordPoolsProcessed > 0 {
			logger.Info("Password rotation list/queue complete", "totalPools", totalPasswordPoolsProcessed)
		}
	} else {
		if !passwordRotationEnabled {
			logger.Debug("Password rotation disabled. Skipping password rotation.")
		} else if !authType1PasswordRotationEnabled {
			logger.Info("AuthType USERNAME_PWD_SEC_MGR password rotation disabled via ENABLE_VSA_AUTHTYPE1_PASSWORD_ROTATION=false.")
		}
	}

	// Log summary
	if certificateRotationFailed || passwordRotationFailed {
		var warningMessages []string
		if certificateRotationFailed {
			warningMessages = append(warningMessages, "certificate rotation failed to queue for one or more pools")
			logger.Warn("Some certificate rotation operations failed to queue", "totalPools", totalCertPoolsProcessed)
		} else if totalCertPoolsProcessed > 0 {
			logger.Info("Certificate rotation successfully queued for all auth type 2 pools", "totalPools", totalCertPoolsProcessed)
		}
		if passwordRotationFailed {
			warningMessages = append(warningMessages, "password rotation failed to queue for one or more pools")
			logger.Warn("Some password rotation operations failed to queue", "totalPools", totalPasswordPoolsProcessed)
		} else if totalPasswordPoolsProcessed > 0 {
			logger.Info("Password rotation successfully queued for all auth type 1 pools", "totalPools", totalPasswordPoolsProcessed)
		}
		if len(warningMessages) > 0 {
			logger.Warn("Some rotation operations failed to queue", "warnings", strings.Join(warningMessages, "; "))
		}
	} else {
		if totalCertPoolsProcessed > 0 {
			logger.Info("Certificate rotation successfully queued for all auth type 2 pools", "totalPools", totalCertPoolsProcessed)
		}
		if totalPasswordPoolsProcessed > 0 {
			logger.Info("Password rotation successfully queued for all auth type 1 pools", "totalPools", totalPasswordPoolsProcessed)
		}
	}

	logger.Info("Certificate and password rotation workflow completed successfully - all child workflows have been triggered")
	return nil
}
