package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// upgradeJobRef identifies an upgrade job for the shared PENDING -> IN_PROGRESS
// claim. LogKey and Label carry each caller's existing log vocabulary so log
// field names and message wording stay stable per activity.
type upgradeJobRef struct {
	Kind   database.UpgradeJobKind
	UUID   string
	LogKey string
	Label  string
}

// claimPendingUpgradeJob atomically transitions a PENDING upgrade job to
// IN_PROGRESS. A job already in IN_PROGRESS is claimed successfully so the
// activity stays idempotent; failing to claim therefore means the job is
// missing or already in a terminal state, and retrying cannot change that.
func claimPendingUpgradeJob(ctx context.Context, se database.Storage, ref upgradeJobRef) error {
	logger := util.GetLogger(ctx)
	failureMsg := fmt.Sprintf("Failed to transition %s to IN_PROGRESS", ref.Label)

	transitioned, err := se.TransitionPendingUpgradeJobToInProgress(ctx, ref.Kind, ref.UUID, time.Now())
	if err != nil {
		logger.Error(failureMsg, ref.LogKey, ref.UUID, "error", err)
		return vsaerrors.WrapAsTemporalApplicationError(err)
	}
	if !transitioned {
		err := fmt.Errorf("cannot claim %s %s: job is missing or not in %s/%s state",
			ref.Label, ref.UUID, models.UpgradeStatusPending, models.UpgradeStatusInProgress)
		logger.Error(failureMsg, ref.LogKey, ref.UUID, "error", err)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrJobFailed, err),
		)
	}

	logger.Info(fmt.Sprintf("Successfully updated %s status", ref.Label),
		ref.LogKey, ref.UUID, "status", string(models.UpgradeStatusInProgress))
	return nil
}

// upgradeJobNotFoundError reports a missing upgrade job the same way
// claimPendingUpgradeJob reports an unclaimable one: non-retryable, because the
// row cannot reappear on a later attempt.
func upgradeJobNotFoundError(cause error) error {
	return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
		vsaerrors.NewVCPError(vsaerrors.ErrJobFailed, cause),
	)
}
