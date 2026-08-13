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

// BackupVaultDeleteHandler implements Handler for BackupVault delete operations.
type BackupVaultDeleteHandler struct{}

// NewBackupVaultDeleteHandler returns the handler that reverts backup vault state for stale delete jobs.
func NewBackupVaultDeleteHandler() *BackupVaultDeleteHandler {
	return &BackupVaultDeleteHandler{}
}

// JobTypes enumerates the job types supported by the backup vault delete handler.
func (h *BackupVaultDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeleteBackupVault,
	}
}

// Handle reverts backup vault state from DELETING to previous state for stale delete jobs.
func (h *BackupVaultDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: delete job lacks backup vault resource UUID; skipping cleanup")
		return nil
	}

	backupVault, err := storage.GetBackupVault(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: backup vault not found for delete cleanup")
			return nil
		}
		return fmt.Errorf("load backup vault for delete cleanup: %w", err)
	}

	// Only revert if backup vault is in DELETING state
	if backupVault.LifeCycleState != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: backup vault %s not in DELETING state (%s); skipping delete cleanup", backupVault.UUID, backupVault.LifeCycleState)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to READY
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for backup vault %s, defaulting to READY", backupVault.UUID)
		previousState = datamodel.LifeCycleStateREADY
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	if _, err := storage.UpdateBackupVaultState(ctx, backupVault, previousState, previousStateDetails); err != nil {
		return fmt.Errorf("revert backup vault %s state to %s: %w", backupVault.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted backup vault %s from DELETING to %s", backupVault.UUID, previousState)
	return nil
}
