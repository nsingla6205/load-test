package supervisorhandler

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ActiveDirectoryDeleteHandler implements Handler for Active Directory delete operations.
type ActiveDirectoryDeleteHandler struct{}

// NewActiveDirectoryDeleteHandler returns the handler that reverts AD state for stale delete jobs.
func NewActiveDirectoryDeleteHandler() *ActiveDirectoryDeleteHandler {
	return &ActiveDirectoryDeleteHandler{}
}

// JobTypes enumerates the job types supported by the AD delete handler.
func (h *ActiveDirectoryDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeleteActiveDirectory,
	}
}

// Handle repairs an Active Directory left in DELETING by a stale delete job.
// For NEW-state timeouts (the workflow never started) it reverts the AD to its previous state, since the
// delete never began. For PROCESSING-state timeouts (the workflow started then timed out) it moves the AD
// to ERROR, preserving the delete intent rather than incorrectly flipping it back to READY.
func (h *ActiveDirectoryDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		"jobState":                              job.State,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: delete job lacks Active Directory resource UUID; skipping cleanup")
		return nil
	}

	if job.State == string(datamodel.JobsStatePROCESSING) {
		return h.handleProcessingTimeout(ctx, job, storage, logger)
	}

	return h.handleNewStateTimeout(ctx, job, storage, logger)
}

// handleProcessingTimeout handles timeout for delete jobs in PROCESSING state by transitioning the AD from
// DELETING to ERROR. The delete workflow started but did not finish, so the delete intent is preserved.
func (h *ActiveDirectoryDeleteHandler) handleProcessingTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	ad, err := getActiveDirectoryForRepair(ctx, job, storage, logger, datamodel.LifeCycleStateDeleting, "delete")
	if err != nil || ad == nil {
		return err
	}

	ad.State = datamodel.LifeCycleStateError
	ad.StateDetails = datamodel.LifeCycleStateDeletionErrorDetails
	if _, err := storage.UpdateActiveDirectory(ctx, ad); err != nil {
		return fmt.Errorf("transition Active Directory %s to ERROR: %w", ad.UUID, err)
	}

	logger.Infof("workflow-supervisor-task: Active Directory %s transitioned from DELETING to ERROR due to workflow timeout", ad.UUID)
	return nil
}

// handleNewStateTimeout handles timeout for delete jobs in NEW state by reverting the AD from DELETING back
// to its previous state (captured on the job), since the delete workflow never began. Falls back to ERROR
// when no previous state was stored.
func (h *ActiveDirectoryDeleteHandler) handleNewStateTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	ad, err := getActiveDirectoryForRepair(ctx, job, storage, logger, datamodel.LifeCycleStateDeleting, "delete")
	if err != nil || ad == nil {
		return err
	}

	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for Active Directory %s, defaulting to ERROR", ad.UUID)
		previousState = datamodel.LifeCycleStateError
		previousStateDetails = datamodel.LifeCycleStateDeletionErrorDetails
	} else if !datamodel.IsAllowedADRollbackState(previousState) {
		// Defend against a corrupted/tampered job record: only restore allow-listed states, otherwise ERROR.
		logger.Warnf("workflow-supervisor-task: previous state %q for Active Directory %s is not a permitted rollback target; defaulting to ERROR", previousState, ad.UUID)
		previousState = datamodel.LifeCycleStateError
		previousStateDetails = datamodel.LifeCycleStateDeletionErrorDetails
	}

	ad.State = previousState
	ad.StateDetails = previousStateDetails
	if _, err := storage.UpdateActiveDirectory(ctx, ad); err != nil {
		return fmt.Errorf("revert Active Directory %s state to %s: %w", ad.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted Active Directory %s from DELETING to %s", ad.UUID, previousState)
	return nil
}
