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

// VolumeDeleteHandler implements Handler for Volume delete operations.
type VolumeDeleteHandler struct{}

// NewVolumeDeleteHandler returns the handler that reverts volume state for stale delete jobs.
func NewVolumeDeleteHandler() *VolumeDeleteHandler {
	return &VolumeDeleteHandler{}
}

// JobTypes enumerates the job types supported by the volume delete handler.
func (h *VolumeDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeleteVolume,
		datamodel.JobTypeDeleteLargeVolume,
		datamodel.JobTypeFlexCacheDeleteVolume,
	}
}

// Handle reverts volume state from DELETING to previous state for stale delete jobs (NEW state),
// or transitions to ERROR state for PROCESSING state timeouts.
func (h *VolumeDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
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

	if job.State == string(datamodel.JobsStatePROCESSING) {
		return h.handleProcessingTimeout(ctx, job, storage, logger)
	}

	return h.handleNewStateTimeout(ctx, job, storage, logger)
}

// handleProcessingTimeout handles timeout for delete jobs in PROCESSING state.
// It transitions the volume from DELETING to ERROR state.
func (h *VolumeDeleteHandler) handleProcessingTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	volume, err := storage.GetVolume(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: volume already deleted in VCP")
			return nil
		}
		return fmt.Errorf("load volume for PROCESSING timeout: %w", err)
	}

	if volume.State != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: volume not in DELETING state (%s); skipping state transition", volume.State)
		return nil
	}

	err = storage.UpdateVolumeFields(ctx, volume.UUID, map[string]interface{}{
		"state":         datamodel.LifeCycleStateError,
		"state_details": datamodel.LifeCycleStateDeletionErrorDetails,
	})
	if err != nil {
		return fmt.Errorf("update volume state to ERROR: %w", err)
	}

	logger.Infof("workflow-supervisor-task: volume %s transitioned from DELETING to ERROR due to workflow timeout", volume.UUID)
	return nil
}

// handleNewStateTimeout handles timeout for delete jobs in NEW state.
// It reverts the volume from DELETING to its previous state.
func (h *VolumeDeleteHandler) handleNewStateTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	volume, err := storage.GetVolume(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: volume already deleted in VCP")
			return nil
		}
		return fmt.Errorf("load volume: %w", err)
	}

	if volume.State != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: volume not in DELETING state (%s); skipping revert", volume.State)
		return nil
	}

	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

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

	logger.Infof("workflow-supervisor-task: volume %s reverted from DELETING to %s", volume.UUID, previousState)
	return nil
}
