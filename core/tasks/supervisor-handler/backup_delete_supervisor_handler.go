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

// BackupDeleteHandler implements Handler for Backup delete operations.
type BackupDeleteHandler struct{}

// NewBackupDeleteHandler returns the handler that reverts backup state for stale delete jobs.
func NewBackupDeleteHandler() *BackupDeleteHandler {
	return &BackupDeleteHandler{}
}

// JobTypes enumerates the job types supported by the backup delete handler.
func (h *BackupDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeleteBackup,
	}
}

// Handle reverts backup state from DELETING to previous state for stale delete jobs.
func (h *BackupDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: delete job lacks backup resource UUID; skipping cleanup")
		return nil
	}

	backupVaultUUID := ""
	accountName := ""
	if job.JobAttributes.PayloadAttributes != nil {
		if bvUUID, ok := job.JobAttributes.PayloadAttributes["backup_vault_uuid"].(string); ok {
			backupVaultUUID = bvUUID
		}
		if accName, ok := job.JobAttributes.PayloadAttributes["account_name"].(string); ok {
			accountName = accName
		}
	}

	backup, err := storage.GetBackup(ctx, backupVaultUUID, job.JobAttributes.ResourceUUID, accountName)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: backup not found for delete cleanup")
			return nil
		}
		return fmt.Errorf("load backup for delete cleanup: %w", err)
	}

	// Only revert if backup is in DELETING state
	if backup.State != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: backup %s not in DELETING state (%s); skipping delete cleanup", backup.UUID, backup.State)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to AVAILABLE
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for backup %s, defaulting to AVAILABLE", backup.UUID)
		previousState = datamodel.LifeCycleStateAvailable
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	backup.State = previousState
	backup.StateDetails = previousStateDetails
	if _, err := storage.UpdateBackupState(ctx, backup); err != nil {
		return fmt.Errorf("revert backup %s state to %s: %w", backup.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted backup %s from DELETING to %s", backup.UUID, previousState)
	return nil
}
