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

// BackupHandler implements Handler for Backup resources.
type BackupHandler struct{}

// NewBackupHandler returns the handler that cleans up backup resources in VCP.
func NewBackupHandler() *BackupHandler {
	return &BackupHandler{}
}

// JobTypes enumerates the job types supported by the backup handler.
func (h *BackupHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateBackup,
		datamodel.JobTypeCreateScheduledBackup,
	}
}

// Handle removes backup artifacts from VCP for the job.
func (h *BackupHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks backup resource UUID; skipping cleanup")
		return nil
	}

	if _, err := storage.DeleteBackup(ctx, job.JobAttributes.ResourceUUID); err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: backup already deleted in VCP")
			return nil
		}
		return fmt.Errorf("delete backup from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: backup %s removed from VCP", job.JobAttributes.ResourceUUID)
	return nil
}
