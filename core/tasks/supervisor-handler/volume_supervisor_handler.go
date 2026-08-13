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

// VolumeHandler implements Handler for Volume resources.
type VolumeHandler struct{}

// NewVolumeHandler returns the handler that cleans up volume resources in VCP.
func NewVolumeHandler() *VolumeHandler {
	return &VolumeHandler{}
}

// JobTypes enumerates the job types supported by the volume handler.
func (h *VolumeHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateVolume,
		datamodel.JobTypeCreateLargeVolume,
		datamodel.JobTypeFlexCacheCreateVolume,
		datamodel.JobTypeFlexCacheEstablishPeering,
	}
}

// Handle removes volume artifacts from VCP for the job when the supervisor
// detects a timeout in NEW state. Create volume is not eligible for PROCESSING
// state timeout handling.
func (h *VolumeHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
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
		logger.Warnf("workflow-supervisor-task: job lacks volume resource UUID; skipping cleanup")
		return nil
	}

	return h.handleNewStateTimeout(ctx, job, storage, logger)
}

// handleNewStateTimeout handles timeout for jobs in NEW state.
// It deletes the volume from VCP database.
func (h *VolumeHandler) handleNewStateTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	if _, err := storage.DeleteVolumeAndChildResources(ctx, job.JobAttributes.ResourceUUID); err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: volume already deleted in VCP")
			return nil
		}
		return fmt.Errorf("delete volume from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: volume %s removed from VCP", job.JobAttributes.ResourceUUID)
	return nil
}
