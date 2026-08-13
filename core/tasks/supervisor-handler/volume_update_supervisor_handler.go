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

// VolumeUpdateHandler implements Handler for Volume update operations.
type VolumeUpdateHandler struct{}

// NewVolumeUpdateHandler returns the handler that reverts volume state for stale update jobs.
func NewVolumeUpdateHandler() *VolumeUpdateHandler {
	return &VolumeUpdateHandler{}
}

// JobTypes enumerates the job types supported by the volume update handler.
func (h *VolumeUpdateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeUpdateVolume,
		datamodel.JobTypeUpdateVolumeInReplication,
	}
}

// Handle reverts volume state from UPDATING to previous state for stale update jobs.
func (h *VolumeUpdateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks volume resource UUID; skipping cleanup")
		return nil
	}

	volume, err := storage.GetVolume(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: volume already deleted in VCP")
			return nil
		}
		return fmt.Errorf("load volume: %w", err)
	}

	// Only revert if volume is in UPDATING state
	if volume.State != datamodel.LifeCycleStateUpdating {
		logger.Warnf("workflow-supervisor-task: volume not in UPDATING state (%s); skipping revert", volume.State)
		return nil
	}

	// Get previous state from job attributes, with fallback
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	// Fallback if previous state not stored (for backward compatibility with old jobs)
	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not stored in job attributes, using default READY")
		previousState = datamodel.LifeCycleStateREADY
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	err = storage.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
		"state":         previousState,
		"state_details": previousStateDetails,
	})
	if err != nil {
		return fmt.Errorf("revert volume state to %s: %w", previousState, err)
	}

	logger.Infof("workflow-supervisor-task: volume %s reverted from UPDATING to %s", volume.UUID, previousState)
	return nil
}
