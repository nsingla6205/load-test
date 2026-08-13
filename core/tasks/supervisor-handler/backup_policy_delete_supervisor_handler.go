package supervisorhandler

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// BackupPolicyDeleteHandler implements Handler for BackupPolicy delete operations.
type BackupPolicyDeleteHandler struct{}

// NewBackupPolicyDeleteHandler returns the handler that reverts backup policy state for stale delete jobs.
func NewBackupPolicyDeleteHandler() *BackupPolicyDeleteHandler {
	return &BackupPolicyDeleteHandler{}
}

// JobTypes enumerates the job types supported by the backup policy delete handler.
func (h *BackupPolicyDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeleteBackupPolicy,
	}
}

// Handle reverts backup policy state from DELETING to previous state for stale delete jobs.
func (h *BackupPolicyDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: delete job lacks backup policy resource UUID; skipping cleanup")
		return nil
	}

	backupPolicy, err := storage.GetBackupPolicyByUUIDAndOwnerID(ctx, job.JobAttributes.ResourceUUID, job.AccountID.Int64)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: backup policy not found for delete cleanup")
			return nil
		}
		return fmt.Errorf("load backup policy for delete cleanup: %w", err)
	}

	// Only revert if backup policy is in DELETING state
	if backupPolicy.LifeCycleState != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: backup policy %s not in DELETING state (%s); skipping delete cleanup", backupPolicy.UUID, backupPolicy.LifeCycleState)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to READY
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for backup policy %s, defaulting to READY", backupPolicy.UUID)
		previousState = datamodel.LifeCycleStateREADY
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	updates := map[string]interface{}{
		"life_cycle_state":         previousState,
		"life_cycle_state_details": previousStateDetails,
	}
	if _, err := storage.UpdateBackupPolicy(ctx, backupPolicy.UUID, updates); err != nil {
		return fmt.Errorf("revert backup policy %s state to %s: %w", backupPolicy.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted backup policy %s from DELETING to %s", backupPolicy.UUID, previousState)
	return nil
}
