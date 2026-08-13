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

// SnapshotHandler implements Handler for snapshot resources.
type SnapshotHandler struct{}

// NewSnapshotHandler returns the handler that cleans up snapshot resources in VCP.
func NewSnapshotHandler() *SnapshotHandler {
	return &SnapshotHandler{}
}

// JobTypes enumerates the job types supported by the snapshot handler.
func (h *SnapshotHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateSnapshot,
	}
}

// Handle removes snapshot artifacts from VCP for the job.
func (h *SnapshotHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks snapshot resource UUID; skipping cleanup")
		return nil
	}

	if _, err := storage.DeleteSnapshot(ctx, job.JobAttributes.ResourceUUID); err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: snapshot already deleted in VCP")
			return nil
		}
		return fmt.Errorf("delete snapshot from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: snapshot %s removed from VCP", job.JobAttributes.ResourceUUID)
	return nil
}
