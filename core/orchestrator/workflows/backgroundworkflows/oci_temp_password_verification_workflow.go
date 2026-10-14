package backgroundworkflows

import (
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	ociworkflows "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows/oci"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"go.temporal.io/sdk/workflow"
)

const (
	ociTempPasswordVerificationMetricWorkflow   = "oci_temp_password_verification"
	ociTempPasswordVerificationMetricStage      = "verify_temp_password"
	ociTempPasswordVerificationMetricQueue      = "customer"
	ociTempPasswordVerificationMetricFailure    = "failure"
	defaultOCITempPasswordVerificationBatchSize = 10

	// ociTempPasswordExpireOnElapseChangeID versions the switch from rotating an
	// unconsumed temporary password to a fresh generated one, to expiring it.
	ociTempPasswordExpireOnElapseChangeID = "ociTempPasswordExpireOnElapse"
	ociTempPasswordExpireOnElapseVersion  = workflow.Version(1)
)

var (
	ociExpertModeUsername      = env.GetString("OCI_EXPERT_MODE_USERNAME", "ociadmin")
	ociTempPasswordResetWindow = env.OCITempPasswordResetWindow()
)

func VerifyOciTempPasswordsWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)

	if !env.EnableOCITempPasswordResetCron {
		logger.Debug("OCI temp-password reset cron disabled (ENABLE_OCI_TEMP_PASSWORD_RESET_CRON=false). Skipping workflow execution.")
		return nil
	}

	ao, err := rotationActivityOptions()
	if err != nil {
		return err
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	verifyActivity := &backgroundactivities.OciTempPasswordVerificationActivity{}
	poolActivity := &activities.PoolActivity{}

	batchSize := env.OCITempPasswordVerificationBatchSize
	if batchSize <= 0 {
		batchSize = defaultOCITempPasswordVerificationBatchSize
	}

	logger.Info("Starting OCI temp-password verification (batched listing)")
	totalPoolsProcessed := 0
	offset := 0
	batchNumber := 0
	for {
		batchNumber++
		var batchResult *backgroundactivities.ListPoolsBatchResult
		listFuture := workflow.ExecuteActivity(ctx, verifyActivity.ListOciPoolsWithActiveTempPassword, offset, batchSize)
		if err := listFuture.Get(ctx, &batchResult); err != nil {
			logger.Error("ListOciPoolsWithActiveTempPassword failed", "error", err)
			return fmt.Errorf("oci temp-password pool listing: %w", err)
		}

		pools := batchResult.Pools
		hasMore := batchResult.HasMore
		if len(pools) == 0 && batchNumber == 1 {
			logger.Info("No OCI pools with an active temporary password found. Skipping temp-password verification.")
			break
		}
		logger.Info("OCI temp-password pool batch retrieved", "batchNumber", batchNumber, "count", len(pools), "hasMore", hasMore)

		for _, pool := range pools {
			// Isolate per-pool failures so one bad pool does not abort the sweep.
			if err := verifyOciPoolTempPassword(ctx, poolActivity, pool); err != nil {
				logger.Error("OCI temp-password verification failed for pool", "poolUUID", pool.UUID, "error", err)
				ociworkflows.EmitStage(ctx,
					ociTempPasswordVerificationMetricWorkflow,
					ociTempPasswordVerificationMetricQueue,
					ociTempPasswordVerificationMetricStage,
					ociTempPasswordVerificationMetricFailure)
			}
			totalPoolsProcessed++
		}

		if !hasMore {
			break
		}
		offset += batchSize
	}

	if totalPoolsProcessed > 0 {
		logger.Info("OCI temp-password verification sweep completed", "totalPools", totalPoolsProcessed)
	}
	return nil
}

func verifyOciPoolTempPassword(ctx workflow.Context, poolActivity *activities.PoolActivity, pool *datamodel.Pool) error {
	logger := workflow.GetLogger(ctx)

	if pool == nil || pool.ExternalTempCredentials == nil || !pool.ExternalTempCredentials.TempPasswordActive {
		return nil
	}
	poolUUID := pool.UUID
	createdAt := pool.ExternalTempCredentials.TempPasswordCreatedAt
	if createdAt == nil {
		// Active flag without a timestamp: no window to evaluate, skip rather than
		// expire blindly.
		logger.Warn("Pool has an active temporary password but no creation timestamp; skipping", "poolUUID", poolUUID)
		return nil
	}

	// Resolve the ONTAP cluster name from the VLM config.
	var vlmConfig *vlm.VLMConfig
	if err := workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &vlmConfig); err != nil {
		logger.Error("Failed to parse VLM config", "poolUUID", poolUUID, "error", err)
		return err
	}
	clusterName := vlmConfig.VsaCluster.ClusterName
	if clusterName == "" {
		return vsaerrors.NewVCPError(vsaerrors.ErrResourceEmptyError,
			fmt.Errorf("cluster name is empty in VLM config for pool %q", poolUUID))
	}

	// Did the caller consume (reset) the temporary password?
	var consumed bool
	if err := workflow.ExecuteActivity(ctx, poolActivity.CheckOciAdminPasswordResetConsumed,
		pool, ociExpertModeUsername, *createdAt).Get(ctx, &consumed); err != nil {
		logger.Error("Failed to check temp-password reset state", "poolUUID", poolUUID, "error", err)
		return err
	}
	if consumed {
		if err := workflow.ExecuteActivity(ctx, poolActivity.ClearTempPasswordState, pool).Get(ctx, nil); err != nil {
			logger.Error("Failed to clear temp-password state", "poolUUID", poolUUID, "error", err)
			return err
		}
		logger.Info("Temporary password was consumed by the caller; cleared active flag", "poolUUID", poolUUID)
		return nil
	}

	// Not consumed — is it still within its reset window?
	elapsed := workflow.Now(ctx).Sub(createdAt.UTC())
	if elapsed < ociTempPasswordResetWindow {
		logger.Info("Temporary password unconsumed but still within window; nothing to do",
			"poolUUID", poolUUID, "elapsed", elapsed.String(), "window", ociTempPasswordResetWindow.String())
		return nil
	}

	// Window elapsed — expire the temporary password so it must be changed on
	// next login, and mark the temp password inactive so the cron does not
	// process this pool again.
	logger.Info("Temporary password unconsumed past its window; expiring",
		"poolUUID", poolUUID, "elapsed", elapsed.String(), "window", ociTempPasswordResetWindow.String())

	// Histories started before the expire change must replay the rotation they
	// already scheduled, or Temporal fails them with a non-determinism error.
	elapsedAction := poolActivity.ExpireOciAdminPasswordOnVSA
	expireVersion := workflow.GetVersion(ctx, ociTempPasswordExpireOnElapseChangeID,
		workflow.DefaultVersion, ociTempPasswordExpireOnElapseVersion)
	if expireVersion == workflow.DefaultVersion {
		elapsedAction = poolActivity.GenerateAndSetOciAdminTempPassword
	}

	if err := workflow.ExecuteActivity(ctx, elapsedAction,
		pool, clusterName, ociExpertModeUsername).Get(ctx, nil); err != nil {
		logger.Error("Failed to expire temp password", "poolUUID", poolUUID, "error", err)
		return err
	}

	if err := workflow.ExecuteActivity(ctx, poolActivity.ClearTempPasswordState, pool).Get(ctx, nil); err != nil {
		logger.Error("Failed to clear expired temp-password state", "poolUUID", poolUUID, "error", err)
		return err
	}

	logger.Info("Expired unconsumed temporary password and marked it inactive", "poolUUID", poolUUID, "cluster", clusterName)
	return nil
}
