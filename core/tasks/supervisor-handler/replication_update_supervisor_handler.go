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

// ReplicationUpdateHandler implements Handler for VolumeReplication update operations.
type ReplicationUpdateHandler struct{}

// NewReplicationUpdateHandler returns the handler that reverts replication state for stale update jobs.
func NewReplicationUpdateHandler() *ReplicationUpdateHandler {
	return &ReplicationUpdateHandler{}
}

// JobTypes enumerates the job types supported by the replication update handler.
func (h *ReplicationUpdateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeUpdateVolumeReplicationInternal,
		datamodel.JobTypeUpdateVolumeReplication,
		datamodel.JobTypeUpdateVolumeReplicationAttributes,
	}
}

// Handle reverts replication state from UPDATING to previous state for stale update jobs.
func (h *ReplicationUpdateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: update job lacks replication resource UUID; skipping cleanup")
		return nil
	}

	replication, err := storage.GetVolumeReplication(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: replication not found for update cleanup")
			return nil
		}
		return fmt.Errorf("load replication for update cleanup: %w", err)
	}

	// Only revert if replication is in UPDATING state
	if replication.State != datamodel.LifeCycleStateUpdating {
		logger.Infof("workflow-supervisor-task: replication %s not in UPDATING state (%s); skipping update cleanup", replication.UUID, replication.State)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to AVAILABLE
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for replication %s, defaulting to AVAILABLE", replication.UUID)
		previousState = datamodel.LifeCycleStateAvailable
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	replication.State = previousState
	replication.StateDetails = previousStateDetails
	if err := storage.UpdateVolumeReplicationStates(ctx, replication); err != nil {
		return fmt.Errorf("revert replication %s state to %s: %w", replication.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted replication %s from UPDATING to %s", replication.UUID, previousState)
	return nil
}
