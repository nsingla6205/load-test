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

// BackupVaultUpdateHandler implements Handler for BackupVault update operations.
type BackupVaultUpdateHandler struct{}

// NewBackupVaultUpdateHandler returns the handler that reverts backup vault state for stale update jobs.
func NewBackupVaultUpdateHandler() *BackupVaultUpdateHandler {
	return &BackupVaultUpdateHandler{}
}

// JobTypes enumerates the job types supported by the backup vault update handler.
func (h *BackupVaultUpdateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeUpdateBackupVault,
	}
}

// Handle reverts backup vault state from UPDATING to previous state for stale update jobs.
func (h *BackupVaultUpdateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: update job lacks backup vault resource UUID; skipping cleanup")
		return nil
	}

	backupVault, err := storage.GetBackupVault(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: backup vault not found for update cleanup")
			return nil
		}
		return fmt.Errorf("load backup vault for update cleanup: %w", err)
	}

	// Only revert if backup vault is in UPDATING state
	if backupVault.LifeCycleState != datamodel.LifeCycleStateUpdating {
		logger.Infof("workflow-supervisor-task: backup vault %s not in UPDATING state (%s); skipping update cleanup", backupVault.UUID, backupVault.LifeCycleState)
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

	logger.Infof("workflow-supervisor-task: reverted backup vault %s from UPDATING to %s", backupVault.UUID, previousState)
	return nil
}
