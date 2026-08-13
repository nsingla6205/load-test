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

// SnapshotDeleteHandler implements Handler for Snapshot delete operations.
type SnapshotDeleteHandler struct{}

// NewSnapshotDeleteHandler returns the handler that reverts snapshot state for stale delete jobs.
func NewSnapshotDeleteHandler() *SnapshotDeleteHandler {
	return &SnapshotDeleteHandler{}
}

// JobTypes enumerates the job types supported by the snapshot delete handler.
func (h *SnapshotDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeleteSnapshot,
	}
}

// Handle reverts snapshot state from DELETING to previous state for stale delete jobs.
func (h *SnapshotDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: delete job lacks snapshot resource UUID; skipping cleanup")
		return nil
	}

	accountID := int64(0)
	volumeID := int64(0)
	if job.JobAttributes.PayloadAttributes != nil {
		if accID, ok := job.JobAttributes.PayloadAttributes["account_id"].(float64); ok {
			accountID = int64(accID)
		}
		if volID, ok := job.JobAttributes.PayloadAttributes["volume_id"].(float64); ok {
			volumeID = int64(volID)
		}
	}

	snapshot, err := storage.GetSnapshotByUUID(ctx, job.JobAttributes.ResourceUUID, accountID, volumeID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: snapshot not found for delete cleanup")
			return nil
		}
		return fmt.Errorf("load snapshot for delete cleanup: %w", err)
	}

	// Only revert if snapshot is in DELETING state
	if snapshot.State != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: snapshot %s not in DELETING state (%s); skipping delete cleanup", snapshot.UUID, snapshot.State)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to READY
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for snapshot %s, defaulting to READY", snapshot.UUID)
		previousState = datamodel.LifeCycleStateREADY
		previousStateDetails = datamodel.LifeCycleStateReadyDetails
	}

	snapshot.State = previousState
	snapshot.StateDetails = previousStateDetails
	if _, err := storage.UpdateSnapshot(ctx, snapshot); err != nil {
		return fmt.Errorf("revert snapshot %s state to %s: %w", snapshot.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted snapshot %s from DELETING to %s", snapshot.UUID, previousState)
	return nil
}
