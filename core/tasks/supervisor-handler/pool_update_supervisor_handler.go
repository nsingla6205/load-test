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

// PoolUpdateHandler implements Handler for Pool update operations.
type PoolUpdateHandler struct{}

// NewPoolUpdateHandler returns the handler that reverts pool state for stale update jobs.
func NewPoolUpdateHandler() *PoolUpdateHandler {
	return &PoolUpdateHandler{}
}

// JobTypes enumerates the job types supported by the pool update handler.
func (h *PoolUpdateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeUpdatePool,
		datamodel.JobTypeUpdateLargePool,
		datamodel.JobTypeEncryptPool,
	}
}

// Handle reverts pool state from UPDATING to previous state for stale update jobs.
func (h *PoolUpdateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if event != EventTimeout {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks pool resource UUID; skipping cleanup")
		return nil
	}

	pool, err := storage.GetPoolByUUID(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: pool already deleted in VCP")
			return nil
		}
		return fmt.Errorf("load pool: %w", err)
	}

	// Only revert if pool is in UPDATING state
	if pool.State != datamodel.LifeCycleStateUpdating {
		logger.Warnf("workflow-supervisor-task: pool not in UPDATING state (%s); skipping revert", pool.State)
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

	logger.Infof("workflow-supervisor-task: pool %s reverted from UPDATING to %s", pool.UUID, previousState)
	return nil
}
