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

// BackupVaultHandler implements Handler for BackupVault create operations.
type BackupVaultHandler struct{}

// NewBackupVaultHandler returns the handler that cleans up backup vault resources in VCP.
func NewBackupVaultHandler() *BackupVaultHandler {
	return &BackupVaultHandler{}
}

// JobTypes enumerates the job types supported by the backup vault handler.
func (h *BackupVaultHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateBackupVault,
	}
}

// Handle removes backup vault artifacts from VCP for the job.
func (h *BackupVaultHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks backup vault resource UUID; skipping cleanup")
		return nil
	}

	if _, err := storage.DeleteBackupVaultInVCP(ctx, job.JobAttributes.ResourceUUID); err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: backup vault already deleted in VCP")
			return nil
		}
		return fmt.Errorf("delete backup vault from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: backup vault %s removed from VCP", job.JobAttributes.ResourceUUID)
	return nil
}
