package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/activity"
)

// ociTempPasswordLength matches the OCI pool-creation password generation.
const ociTempPasswordLength = 12

// CheckOciAdminPasswordResetConsumed reports whether the temporary ociadmin
// password was consumed since `since`: a successful password-change audit record
// by `username` for its own account exists at or after `since`, via the CLI
// (`security login password -username <user>`) or REST
// (`POST /api/security/authentication/password` with body "name" = <user>).
func (j *PoolActivity) CheckOciAdminPasswordResetConsumed(ctx context.Context, pool *datamodel.Pool, username string, since time.Time) (bool, error) {
	activity.RecordHeartbeat(ctx, "Starting CheckOciAdminPasswordResetConsumed activity")
	logger := util.GetLogger(ctx)

	if pool == nil || pool.PoolCredentials == nil {
		return false, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("pool and pool credentials are required to check the OCI admin password reset state")))
	}
	if username == "" {
		return false, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("expert-mode username is required to check the OCI admin password reset state for pool %s", pool.UUID)))
	}
	if since.IsZero() {
		return false, vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("temporary-password creation time is required to check the OCI admin password reset state for pool %s", pool.UUID)))
	}

	pwdProvider, err := j.resolveExpertModePasswordProvider(ctx, pool)
	if err != nil {
		return false, err
	}

	activity.RecordHeartbeat(ctx, "Querying ONTAP audit messages for password reset")
	consumed, err := pwdProvider.HasLoginPasswordResetSuccess(ctx, username, since)
	if err != nil {
		logger.Error("Failed to query ONTAP audit messages for OCI admin password reset", "poolUUID", pool.UUID,
			"username", username, "error", err, "detail", vsaerrors.ExtractCustomError(err).GetDetailMessage())
		return false, vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Checked OCI admin temporary-password reset state", "poolUUID", pool.UUID, "username", username,
		"since", since.Format(time.RFC3339), "consumed", consumed)
	activity.RecordHeartbeat(ctx, "Finished CheckOciAdminPasswordResetConsumed activity")
	return consumed, nil
}

// GenerateAndSetOciAdminTempPassword sets a fresh strong password for the ociadmin
// user on the pool's ONTAP cluster. VerifyOciTempPasswordsWorkflow schedules it only
// when replaying histories started before the ociTempPasswordExpireOnElapse
// version; do not remove it while such executions can still exist.
func (j *PoolActivity) GenerateAndSetOciAdminTempPassword(ctx context.Context, pool *datamodel.Pool, clusterName, username string) error {
	activity.RecordHeartbeat(ctx, "Starting GenerateAndSetOciAdminTempPassword activity")
	logger := util.GetLogger(ctx)

	if pool == nil || pool.PoolCredentials == nil {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("pool and pool credentials are required to rotate the OCI admin password")))
	}
	if clusterName == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("cluster name is required to rotate the OCI admin password for pool %s", pool.UUID)))
	}
	if username == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("expert-mode username is required to rotate the OCI admin password for pool %s", pool.UUID)))
	}

	pwdProvider, err := j.resolveExpertModePasswordProvider(ctx, pool)
	if err != nil {
		return err
	}

	newPassword, err := utils.GenerateStrongPassword(ociTempPasswordLength)
	if err != nil {
		logger.Error("Failed to generate a new OCI admin password", "poolUUID", pool.UUID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, err))
	}

	activity.RecordHeartbeat(ctx, "Setting rotated OCI admin password on ONTAP")
	if err := pwdProvider.ResetLoginPassword(ctx, clusterName, username, newPassword); err != nil {
		logger.Error("Failed to set rotated OCI admin password on ONTAP", "poolUUID", pool.UUID, "username", username,
			"error", err, "detail", vsaerrors.ExtractCustomError(err).GetDetailMessage())
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Rotated OCI admin password on ONTAP", "poolUUID", pool.UUID, "username", username, "cluster", clusterName)
	activity.RecordHeartbeat(ctx, "Finished GenerateAndSetOciAdminTempPassword activity")
	return nil
}

// ClearTempPasswordState marks the pool's temporary password inactive while
// preserving its creation timestamp for bookkeeping.
func (j *PoolActivity) ClearTempPasswordState(ctx context.Context, pool *datamodel.Pool) error {
	activity.RecordHeartbeat(ctx, "Starting ClearTempPasswordState activity")
	logger := util.GetLogger(ctx)

	if pool == nil || pool.UUID == "" {
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrInputValidationError,
				fmt.Errorf("pool with a valid UUID is required to clear the temporary password state")))
	}

	var createdAt *time.Time
	if pool.ExternalTempCredentials != nil {
		createdAt = pool.ExternalTempCredentials.TempPasswordCreatedAt
	}
	updates := map[string]interface{}{
		"external_temp_credentials": datamodel.ExternalTempCredentials{
			TempPasswordActive:    false,
			TempPasswordCreatedAt: createdAt,
		},
	}

	activity.RecordHeartbeat(ctx, "Clearing temporary password state on pool")
	if err := j.SE.UpdatePoolFields(ctx, pool.UUID, updates); err != nil {
		logger.Error("Failed to clear temporary password state on pool", "poolUUID", pool.UUID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}

	logger.Info("Cleared temporary password state on pool", "poolUUID", pool.UUID)
	activity.RecordHeartbeat(ctx, "Finished ClearTempPasswordState activity")
	return nil
}
