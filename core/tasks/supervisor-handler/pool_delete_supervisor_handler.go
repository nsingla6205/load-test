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

// PoolDeleteHandler implements Handler for Pool delete operations.
type PoolDeleteHandler struct{}

// NewPoolDeleteHandler returns the handler that reverts pool state for stale delete jobs.
func NewPoolDeleteHandler() *PoolDeleteHandler {
	return &PoolDeleteHandler{}
}

// JobTypes enumerates the job types supported by the pool delete handler.
func (h *PoolDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeletePool,
		datamodel.JobTypeDeleteLargePool,
	}
}

// Handle reverts pool state from DELETING to previous state for stale delete jobs (NEW state),
// or transitions to ERROR state for PROCESSING state timeouts.
func (h *PoolDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
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
		logger.Warnf("workflow-supervisor-task: job lacks pool resource UUID; skipping cleanup")
		return nil
	}

	// Handle PROCESSING state timeout: update pool state to ERROR
	if job.State == string(datamodel.JobsStatePROCESSING) {
		return h.handleProcessingTimeout(ctx, job, storage, logger)
	}

	// Handle NEW state timeout: revert pool state to previous state (existing behavior)
	return h.handleNewStateTimeout(ctx, job, storage, logger)
}

// handleProcessingTimeout handles timeout for delete jobs in PROCESSING state.
// It transitions the pool from DELETING to ERROR state.
func (h *PoolDeleteHandler) handleProcessingTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	pool, err := storage.GetPoolByUUID(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: pool %s already deleted in VCP", job.JobAttributes.ResourceUUID)
			return nil
		}
		return fmt.Errorf("load pool for PROCESSING timeout: %w", err)
	}

	// Only transition if pool is in DELETING state
	if pool.State != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: pool %s not in DELETING state (%s); skipping state transition", pool.UUID, pool.State)
		return nil
	}

	_, err = storage.UpdatePoolState(ctx, pool, datamodel.LifeCycleStateError, datamodel.LifeCycleStateDeletionErrorDetails)
	if err != nil {
		return fmt.Errorf("update pool state to ERROR: %w", err)
	}

	logger.Infof("workflow-supervisor-task: pool %s transitioned from DELETING to ERROR due to workflow timeout", pool.UUID)
	return nil
}

// handleNewStateTimeout handles timeout for delete jobs in NEW state.
// It reverts the pool from DELETING to its previous state.
func (h *PoolDeleteHandler) handleNewStateTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	pool, err := storage.GetPoolByUUID(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: pool already deleted in VCP")
			return nil
		}
		return fmt.Errorf("load pool: %w", err)
	}

	// Only revert if pool is in DELETING state
	if pool.State != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: pool not in DELETING state (%s); skipping revert", pool.State)
		return nil
	}

	// Get previous state from job attributes, with fallback
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	// Fallback if previous state not stored (for backward compatibility with old jobs)
	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not stored in job attributes, using default AVAILABLE")
		previousState = datamodel.LifeCycleStateAvailable
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	_, err = storage.UpdatePoolState(ctx, pool, previousState, previousStateDetails)
	if err != nil {
		return fmt.Errorf("revert pool state to %s: %w", previousState, err)
	}

	logger.Infof("workflow-supervisor-task: pool %s reverted from DELETING to %s", pool.UUID, previousState)
	return nil
}
