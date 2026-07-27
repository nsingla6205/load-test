package background_kms_workflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// Failure type labels for metrics
	failureTypePoolMigration = "pool_migration"
)

// RotateKmsKeyChildWorkflow is a child workflow that orchestrates KMS key rotation
// It is idempotent and can resume from any failure point
// Each activity is idempotent and checks actual state (DB, GCP) before performing work
func RotateKmsKeyChildWorkflow(ctx workflow.Context, serviceAccount *datamodel.ServiceAccount, kmsConfig *datamodel.KmsConfig) error {
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID":         workflow.GetInfo(ctx).WorkflowExecution.ID,
		"serviceAccountUUID": serviceAccount.UUID,
		"kmsConfigID":        kmsConfig.UUID,
	})
	logger := util.GetLogger(ctx)
	logger.Info("Starting KMS key rotation child workflow for service account")

	// Set up activity options
	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		logger.Error("Failed to populate retry policy", "Error", err)
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

	rotateKmsSAKeyActivity := &backgroundactivities.RotateKmsSAKeyActivity{}

	// Phase 1: Validate rotation is needed
	// Activity is idempotent - checks actual state, not workflow state
	logger.Info("Validating key rotation requirements")
	var validationResult *backgroundactivities.ValidateKeyRotationRequiredResult
	err = workflow.ExecuteActivity(ctx, rotateKmsSAKeyActivity.ValidateKeyRotationRequiredActivity, serviceAccount.UUID, kmsConfig.UUID).Get(ctx, &validationResult)
	if err != nil {
		logger.Error("ValidateKeyRotationRequiredActivity failed", "Error", err)
		return err
	}

	if !validationResult.RotationRequired {
		logger.Info("Key rotation not required", "Reason", validationResult.Reason)
		return nil // Exit workflow - rotation not needed
	}

	logger.Info("Key rotation validation passed",
		"currentKeyID", validationResult.CurrentKeyID,
		"reason", validationResult.Reason)

	// Update service account reference with fresh data from validation
	serviceAccount = validationResult.ServiceAccount
	currentKeyID := validationResult.CurrentKeyID

	// Acquire the lock at service-account scope so configs sharing one SA cannot mint keys concurrently.
	logger.Info("Acquiring KMS rotation lock", "serviceAccountUUID", serviceAccount.UUID)
	_, releaseLock, err := workflows.WithKmsRotationLock(ctx, rotateKmsSAKeyActivity, serviceAccount.UUID)
	if err != nil {
		logger.Error("AcquireKmsRotationLockActivity failed", "Error", err)
		return err
	}
	defer releaseLock()

	// Phase 2: Create new service account key in GCP (if not already exists)
	// Activity is idempotent - checks if new key already exists in keys array before creating
	logger.Info("Creating new service account key in GCP")

	var createKeyResult *backgroundactivities.CreateServiceAccountKeyResult
	err = workflow.ExecuteActivity(ctx, rotateKmsSAKeyActivity.CreateServiceAccountKeyActivity, serviceAccount.UUID, kmsConfig, currentKeyID).Get(ctx, &createKeyResult)
	if err != nil {
		logger.Error("CreateServiceAccountKeyActivity failed", "Error", err)
		// Note: Key limit metrics are emitted directly in CreateServiceAccountKeyActivity
		return err
	}

	if createKeyResult.KeyExists {
		logger.Info("New key already exists - using existing key",
			"newKeyID", createKeyResult.NewKeyID)
	} else {
		logger.Info("Successfully created new key in GCP",
			"newKeyID", createKeyResult.NewKeyID,
			"gcpKeyName", createKeyResult.GcpKeyName)
	}

	// Phase 3: Store new key in DB
	// Activity is idempotent - checks if key already exists in keys array
	logger.Info("Storing new key in database")
	err = workflow.ExecuteActivity(ctx, rotateKmsSAKeyActivity.StoreNewKeyInDBActivity, serviceAccount.UUID, createKeyResult.NewKeyID, createKeyResult.NewKeyData, currentKeyID).Get(ctx, nil)
	if err != nil {
		logger.Error("StoreNewKeyInDBActivity failed", "Error", err)
		return err
	}
	logger.Info("Successfully stored new key in database", "newKeyID", createKeyResult.NewKeyID)

	// Release lock so we don't hold it during pool migration; defer handles release only on early return above
	releaseLock()

	// Phase 4: Get pools for migration
	// This is a read operation - always safe to re-execute
	logger.Info("Batching pools for migration")
	var pools []*datamodel.Pool
	err = workflow.ExecuteActivity(ctx, rotateKmsSAKeyActivity.BatchPoolsForServiceAccountActivity, serviceAccount.UUID).Get(ctx, &pools)
	if err != nil {
		logger.Error("BatchPoolsForServiceAccountActivity failed", "Error", err)
		return err
	}
	logger.Info("Batched pools for migration", "poolCount", len(pools))

	// Phase 5: Migrate each pool (one at a time for now)
	// Each migration activity is idempotent - updating ONTAP with same key multiple times is safe
	logger.Info("Starting pool migration", "totalPools", len(pools))

	// MigratePoolToNewKeyActivity is idempotent:
	// - Updating ONTAP with the same key multiple times is safe (no-op)
	// - The activity returns success/failure in result, not as error (allows other pools to continue)

	// Get old key data for migration
	oldKeyData := serviceAccount.ServiceAccountPasswordLocation

	// Track migration results
	successfulMigrations := 0
	failedMigrations := 0

	// Pass encrypted key data to activity - decryption happens inside activity to avoid logging passwords in Temporal
	for _, pool := range pools {
		logger.Info("Migrating pool to new key", "poolUUID", pool.UUID, "poolName", pool.Name)
		var migrationResult *backgroundactivities.SvmMigrationResult
		err = workflow.ExecuteActivity(ctx, rotateKmsSAKeyActivity.MigratePoolToNewKeyActivity, pool.UUID, createKeyResult.NewKeyData, oldKeyData, createKeyResult.NewKeyID).Get(ctx, &migrationResult)
		if err != nil {
			logger.Error("MigratePoolToNewKeyActivity failed", "poolUUID", pool.UUID, "error", err)
			failedMigrations++
			// Why we have adopted this approach of not reverting failed pool key-rotations back to older keys:
			// Consider this situation where one pool's key-rotation has failed --
			// K0 (Pool0 - older key, failed rotation) ; K1 (Pool1 - newer key, successful rotation)
			// Now if we tried to revert Pool1 back to the older K0 -- but if the revert itself were to fail (say, the revert activity fails) ...
			// K0 (Pool0 - older key) ; K1 (Pool1 - newer key, failed revert)
			// -- This leaves with us with two keys, and if we were to extend this with multiple pools over multiple iterations, we would end up with multiple keys
			continue // Continue with other pools - don't fail entire workflow
		}

		if !migrationResult.Success {
			logger.Warn("Pool migration failed",
				"poolUUID", pool.UUID,
				"svmUUID", migrationResult.SvmUUID,
				"error", migrationResult.Error)
			failedMigrations++
			// Continue with other pools - don't fail entire workflow
		} else {
			// Why we have adopted an approach to have at the most two keys
			// Consider this situation where one pool's key-rotation has failed --
			// K0 (Pool0 - old key, failed rotation); K0 (Pool1 - old key, failed rotation); K1 (Pool2 - new key, successful rotation)
			// On the next iteration, if we tried to rotate to a newer key --
			// K0 (Pool0 - old key, failed rotation again) ; K2 (Pool1 - newer key, successful rotation); K1 (Pool2 - older key from previous rotation, failed rotation to newer key)
			//  -- This leads us to three keys and if such failures were to be repeated over a number of pools, we would end up with multiple keys and gcp has limitation of 10 keys per service account
			logger.Info("Successfully migrated pool to new key",
				"poolUUID", pool.UUID,
				"svmUUID", migrationResult.SvmUUID,
				"newKeyID", createKeyResult.NewKeyID)
			successfulMigrations++
		}
	}

	// Phase 6: Complete rotation only if all pools migrated successfully
	logger.Info("Checking migration status",
		"totalPools", len(pools),
		"successful", successfulMigrations,
		"failed", failedMigrations)

	// We have adopted an approach where we only move forward (update pool to newer key or not to update at all);
	// We shall not revert any pool upon failure because reverting a pool to an older key itself might fail
	// This enables us to have at the most two keys at any given time: one being designated as primary
	if failedMigrations > 0 {
		// Some pools failed - keep both keys, don't complete rotation
		logger.Warn("Key rotation partially complete - some pools failed migration",
			"successfulPools", successfulMigrations,
			"failedPools", failedMigrations,
			"totalPools", len(pools))
		logger.Info("Keeping both keys active - failed pools can retry later. Rotation will remain in progress.")
		// Emit rotation failure metric for this KMS config
		_ = workflow.ExecuteActivity(ctx, backgroundactivities.EmitKmsRotationFailureMetric, kmsConfig.UUID, serviceAccount.ServiceAccountEmail, failureTypePoolMigration).Get(ctx, nil)
		return nil // Exit without completing - both keys remain active
	}

	// All pools migrated successfully - complete the rotation
	logger.Info("All pools migrated successfully - completing key rotation")
	err = workflow.ExecuteActivity(ctx, rotateKmsSAKeyActivity.CompleteKeyRotationActivity, serviceAccount.UUID, kmsConfig.UUID, createKeyResult.NewKeyID, currentKeyID).Get(ctx, nil)
	if err != nil {
		logger.Error("CompleteKeyRotationActivity failed", "Error", err)
		return err
	}
	logger.Info("Successfully completed key rotation",
		"newKeyID", createKeyResult.NewKeyID,
		"oldKeyID", currentKeyID)

	// Delete the old key from GCP
	logger.Info("Deleting old key from GCP")
	err = workflow.ExecuteActivity(ctx, rotateKmsSAKeyActivity.DeleteOldSAKeyFromGCPActivity, serviceAccount.UUID, kmsConfig.UUID, currentKeyID).Get(ctx, nil)
	if err != nil {
		logger.Warn("DeleteOldSAKeyFromGCPActivity failed", "Error", err)
		// Non-fatal - key deletion can be retried later, rotation is already complete
		// Log warning but don't fail the workflow
	}
	logger.Info("KMS key rotation child workflow completed")

	return nil
}
