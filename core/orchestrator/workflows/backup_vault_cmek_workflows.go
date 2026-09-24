package workflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// backupVaultCmekRotationWorkflow orchestrates CMEK rotation for all backups
// under a backup vault by invoking per-bucket activities and finally updating
// CMEK metadata in VCP and SDE.
type backupVaultCmekRotationWorkflow struct {
	BaseWorkflow
}

var _ WorkflowInterface = &backupVaultCmekRotationWorkflow{}

// RotateCmekBackupsWorkflow is the Temporal entry point used by the orchestrator.
func RotateCmekBackupsWorkflow(ctx workflow.Context, params *common.BackupVaultParams, backupVault *datamodel.BackupVault, primaryKeyVersion string) error {
	logger := workflow.GetLogger(ctx)
	wf := new(backupVaultCmekRotationWorkflow)
	if err := wf.Setup(ctx, params); err != nil {
		return ConvertToVSAError(err)
	}
	if err := wf.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return ConvertToVSAError(err)
	}
	wf.Status = WorkflowStatusRunning
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil); err != nil {
		return ConvertToVSAError(err)
	}
	_, customErr := wf.Run(ctx, backupVault, params, primaryKeyVersion)
	if customErr != nil {
		wf.Status = WorkflowStatusFailed
		if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr); err != nil {
			logger.Error("Error when updating the job status for CMEK rotation workflow", "error", err)
		}
		return ConvertToVSAError(customErr)
	}

	wf.Status = WorkflowStatusCompleted
	if err := wf.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil); err != nil {
		logger.Error("Error when updating the job status for CMEK rotation workflow", "error", err)
		return ConvertToVSAError(err)
	}

	return nil
}

// Setup initialises workflow metadata and status query handler.
func (wf *backupVaultCmekRotationWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	backupVaultParams := input.(*common.BackupVaultParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = backupVaultParams.AccountName
	wf.Status = WorkflowStatusCreated
	// Set the query handler in a non-blocking way
	err := workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
	if err != nil {
		return err
	}
	return nil
}

// Run performs the actual CMEK rotation logic by iterating buckets and invoking
// per-bucket rotation activities followed by vault metadata updates.
func (wf *backupVaultCmekRotationWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	backupVault := args[0].(*datamodel.BackupVault)
	bvCommonParams := args[1].(*common.BackupVaultParams)
	primaryKeyVersion := args[2].(string)
	useVCPRegion := !utils.IsCVPHostConfigured()

	backupVaultActivity := &activities.BackupVaultActivity{}

	// Helper to best-effort hydrate FAILED encryption state back to the
	// source-region VCP backup vault for CRB vaults, keeping the old key
	// version (no BackupsPrimaryKeyVersion change).
	hydrateSourceFailed := func() {
		if backupVault.BackupVaultType != activities.CrossRegionBackupType || backupVault.SourceRegionName == nil {
			return
		}
		sourceParams := *bvCommonParams
		sourceParams.BackupRegion = backupVault.SourceRegionName
		if backupVault.ExternalUUID != nil && *backupVault.ExternalUUID != "" {
			sourceParams.BackupVaultID = *backupVault.ExternalUUID
		}

		failedVault := *backupVault
		if failedVault.CmekAttributes == nil {
			failedVault.CmekAttributes = &datamodel.CmekAttributes{}
		}
		stateFailed := datamodel.EncryptionStateFailed
		failedVault.CmekAttributes.EncryptionState = &stateFailed

		_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateRemoteBackupVaultInVCP, &sourceParams, &failedVault).Get(ctx, nil)
	}

	retryPolicy, err := PopulateRetryPolicyParams()
	if err != nil {
		return nil, ConvertToVSAError(err)
	}
	timeout, err := time.ParseDuration(StartToCloseTimeoutCmekBackupRotate)
	if err != nil {
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
	rotateAo := workflow.ActivityOptions{
		StartToCloseTimeout: timeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(retryPolicy.MaximumAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)
	rotateCtx := workflow.WithActivityOptions(ctx, rotateAo)

	// Ensure vault state is restored in case of unexpected errors.
	defer func() {
		if err != nil {
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultStateInCaseOfError, backupVault, datamodel.LifeCycleStateREADY, datamodel.LifeCycleStateAvailableDetails).Get(ctx, nil)
		}
	}()

	// Mark encryption state as IN_PROGRESS in VCP before starting bucket rotations.
	inProgressState := datamodel.EncryptionStateInProgress
	err = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, backupVault, inProgressState).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to update backup vault encryption state to IN_PROGRESS in VCP",
			"backupVaultUUID", backupVault.UUID, "error", err)
		return nil, ConvertToVSAError(fmt.Errorf("UpdateBackupVaultEncryptionStateInVCPActivity failed for IN_PROGRESS: %w", err))
	}

	if backupVault.BackupVaultType == activities.CrossRegionBackupType && backupVault.SourceRegionName != nil {
		sourceParams := *bvCommonParams
		sourceParams.BackupRegion = backupVault.SourceRegionName
		if backupVault.ExternalUUID != nil && *backupVault.ExternalUUID != "" {
			sourceParams.BackupVaultID = *backupVault.ExternalUUID
		}

		inProgressVault := *backupVault
		if inProgressVault.CmekAttributes == nil {
			inProgressVault.CmekAttributes = &datamodel.CmekAttributes{}
		}
		inProgState := datamodel.EncryptionStateInProgress
		inProgressVault.CmekAttributes.EncryptionState = &inProgState
		inProgressVault.CmekAttributes.BackupsPrimaryKeyVersion = nil

		_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateRemoteBackupVaultInVCP, &sourceParams, &inProgressVault).Get(ctx, nil)
	}

	// Step 1: Rotate VCP buckets first. These are the buckets tracked in VCP;
	// SDE-managed buckets are rotated separately by SDE/CBS.
	var bucketNames []string
	for _, bd := range backupVault.BucketDetails {
		if bd == nil || bd.BucketName == "" {
			continue
		}
		bucketNames = append(bucketNames, bd.BucketName)
	}

	// Sequential, fail-fast bucket rotation in VCP. If any bucket rotation
	// fails, mark VCP encryption state as FAILED and abort without invoking SDE.
	for _, bucketName := range bucketNames {
		err = workflow.ExecuteActivity(rotateCtx, backupVaultActivity.RotateBucketCmekActivity, bucketName, primaryKeyVersion, bvCommonParams.OwnerID, backupVault.UUID).Get(rotateCtx, nil)
		if err != nil {
			logger.Error("Failed to rotate CMEK for bucket",
				"bucketName", bucketName,
				"primaryKeyVersion", primaryKeyVersion,
				"backupVaultUUID", backupVault.UUID,
				"backupVaultName", backupVault.Name,
				"error", err)
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.EmitCmekRotationFailureMetric, bucketName, bvCommonParams.OwnerID, backupVault.UUID, "bucket_rotation_failed").Get(ctx, nil)
			failedState := datamodel.EncryptionStateFailed
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, backupVault, failedState).Get(ctx, nil)
			hydrateSourceFailed()
			return nil, ConvertToVSAError(fmt.Errorf("RotateBucketCmekActivity failed for bucket %s: %w", bucketName, err))
		}
	}

	// Step 2: When CVP_HOST is set (SDE present), start SDE rotation and wait for completion. In a VCP-only region (CVP_HOST unset), only VCP rotation runs.
	var sdeSucceeded bool = true
	if !useVCPRegion && backupVault.ServiceType != datamodel.ServiceTypeCrossProject {
		var jwtToken string
		err = workflow.ExecuteActivity(ctx, activities.CommonActivities.GetAuthJWTToken, bvCommonParams.AccountName).Get(ctx, &jwtToken)
		if err != nil {
			logger.Error("Failed to get auth JWT token for SDE CMEK rotation",
				"backupVaultUUID", backupVault.UUID, "error", err)
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.EmitCmekRotationFailureMetric, "", bvCommonParams.OwnerID, backupVault.UUID, "sde_auth_failed").Get(ctx, nil)
			failedState := datamodel.EncryptionStateFailed
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, backupVault, failedState).Get(ctx, nil)
			hydrateSourceFailed()
			return nil, ConvertToVSAError(fmt.Errorf("GetAuthJWTToken failed: %w", err))
		}
		ctx = workflow.WithValue(ctx, middleware.AuthorizationToken, jwtToken)
		rotateCtx = workflow.WithValue(rotateCtx, middleware.AuthorizationToken, jwtToken)

		err = workflow.ExecuteActivity(ctx, backupVaultActivity.StartSDECmekRotationForBackupVault, bvCommonParams, primaryKeyVersion).Get(ctx, nil)
		if err != nil {
			logger.Error("Failed to start SDE CMEK rotation for backup vault",
				"backupVaultUUID", backupVault.UUID, "error", err)
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.EmitCmekRotationFailureMetric, "", bvCommonParams.OwnerID, backupVault.UUID, "sde_rotation_start_failed").Get(ctx, nil)
			failedState := datamodel.EncryptionStateFailed
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, backupVault, failedState).Get(ctx, nil)
			hydrateSourceFailed()
			return nil, ConvertToVSAError(fmt.Errorf("StartSDECmekRotationForBackupVault failed: %w", err))
		}

		// Scoped to this activity only: it heartbeats on every poll, whereas
		// RotateBucketCmekActivity on rotateCtx does not and would be killed by a
		// heartbeat timeout. Without it a worker lost mid-poll goes undetected until
		// StartToCloseTimeout (days), leaving the job in PROCESSING.
		waitCtx := workflow.WithHeartbeatTimeout(rotateCtx, retryPolicy.HeartBeatTimeout)
		err = workflow.ExecuteActivity(waitCtx, backupVaultActivity.WaitForSDECmekRotationCompletion, bvCommonParams).Get(waitCtx, &sdeSucceeded)
		if err != nil {
			logger.Error("Failed while waiting for SDE CMEK rotation completion",
				"backupVaultUUID", backupVault.UUID, "error", err)
			sdeSucceeded = false
		}

		if !sdeSucceeded {
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.EmitCmekRotationFailureMetric, "", bvCommonParams.OwnerID, backupVault.UUID, "sde_rotation_failed").Get(ctx, nil)
			failedState := datamodel.EncryptionStateFailed
			_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, backupVault, failedState).Get(ctx, nil)
			hydrateSourceFailed()
			return nil, ConvertToVSAError(fmt.Errorf("SDE CMEK rotation failed for backup vault %s", backupVault.UUID))
		}
	}

	// Step 3: VCP (and SDE when !useVCPRegion) rotations have succeeded; update VCP CMEK metadata
	// with the new key version and mark encryption state COMPLETED.
	err = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultCmekInVCPActivity, backupVault, primaryKeyVersion).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to update backup vault CMEK metadata in VCP",
			"backupVaultUUID", backupVault.UUID, "primaryKeyVersion", primaryKeyVersion, "error", err)
		_ = workflow.ExecuteActivity(ctx, backupVaultActivity.EmitCmekRotationFailureMetric, "", bvCommonParams.OwnerID, backupVault.UUID, "metadata_update_failed").Get(ctx, nil)
		failedState := datamodel.EncryptionStateFailed
		_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateBackupVaultEncryptionStateInVCPActivity, backupVault, failedState).Get(ctx, nil)
		hydrateSourceFailed()
		return nil, ConvertToVSAError(fmt.Errorf("UpdateBackupVaultCmekInVCPActivity failed: %w", err))
	}

	// For cross-region backup vaults, mirror SDE's hydration by updating the
	// source-region VCP backup vault once rotation has completed successfully in
	// the destination region and VCP metadata has been updated.
	if backupVault.BackupVaultType == activities.CrossRegionBackupType && backupVault.SourceRegionName != nil {
		sourceParams := *bvCommonParams
		sourceParams.BackupRegion = backupVault.SourceRegionName
		// As with the FAILED hydration helper, use ExternalUUID (source vault
		// UUID) when calling the source-region VCP instance so that we update
		// the correct backup vault record.
		if backupVault.ExternalUUID != nil && *backupVault.ExternalUUID != "" {
			sourceParams.BackupVaultID = *backupVault.ExternalUUID
		}

		// Build a hydrated view of the destination vault with COMPLETED state and
		// the new backups primary key version, and propagate it to the source
		// region.
		completedVault := *backupVault
		if completedVault.CmekAttributes == nil {
			completedVault.CmekAttributes = &datamodel.CmekAttributes{}
		}
		stateCompleted := datamodel.EncryptionStateCompleted
		completedVault.CmekAttributes.EncryptionState = &stateCompleted
		completedVault.CmekAttributes.BackupsPrimaryKeyVersion = &primaryKeyVersion

		_ = workflow.ExecuteActivity(ctx, backupVaultActivity.UpdateRemoteBackupVaultInVCP, &sourceParams, &completedVault).Get(ctx, nil)
	}
	return nil, nil
}
