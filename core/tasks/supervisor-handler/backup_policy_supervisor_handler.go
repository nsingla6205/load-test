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

// BackupPolicyHandler implements Handler for BackupPolicy create operations.
type BackupPolicyHandler struct{}

// NewBackupPolicyHandler returns the handler that cleans up backup policy resources in VCP.
func NewBackupPolicyHandler() *BackupPolicyHandler {
	return &BackupPolicyHandler{}
}

// JobTypes enumerates the job types supported by the backup policy handler.
func (h *BackupPolicyHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateBackupPolicy,
	}
}

// Handle removes backup policy artifacts from VCP for the job.
func (h *BackupPolicyHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks backup policy resource UUID; skipping cleanup")
		return nil
	}

	if _, err := storage.DeleteBackupPolicy(ctx, job.JobAttributes.ResourceUUID); err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: backup policy already deleted in VCP")
			return nil
		}
		return fmt.Errorf("delete backup policy from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: backup policy %s removed from VCP", job.JobAttributes.ResourceUUID)
	return nil
}
