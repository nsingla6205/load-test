package supervisorhandler

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// getActiveDirectoryForRepair loads the Active Directory referenced by a stale job and returns it only when
// it is still in expectedState (the in-progress state the timed-out workflow should have left behind).
// It returns (nil, nil) when there is nothing to repair: the job lacks an account ID (tenant isolation
// guard), the AD no longer exists, or the AD has already moved out of expectedState. opName ("create" /
// "delete") is used only for log wording so both create- and delete-repair paths share one implementation.
func getActiveDirectoryForRepair(
	ctx context.Context,
	job *datamodel.Job,
	storage database.Storage,
	logger log.Logger,
	expectedState string,
	opName string,
) (*datamodel.ActiveDirectory, error) {
	// Scope the lookup to the job's owning account to preserve tenant isolation. The AD factories always
	// stamp the job with the resource owner's account ID, so an empty/zero account here means the job is
	// malformed and we refuse to act rather than fall back to an account-wildcard query.
	if !job.AccountID.Valid {
		logger.Warnf("workflow-supervisor-task: %s job lacks account ID; skipping cleanup to preserve tenant isolation", opName)
		return nil, nil
	}

	ad, err := storage.GetActiveDirectoryByUuidAndAccountId(ctx, job.JobAttributes.ResourceUUID, job.AccountID.Int64)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: Active Directory not found for %s cleanup", opName)
			return nil, nil
		}
		return nil, fmt.Errorf("load Active Directory for %s cleanup: %w", opName, err)
	}

	if ad.State != expectedState {
		logger.Warnf("workflow-supervisor-task: Active Directory %s not in %s state (%s); skipping %s cleanup", ad.UUID, expectedState, ad.State, opName)
		return nil, nil
	}

	return ad, nil
}
