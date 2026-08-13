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

// PoolHandler implements Handler for Pool resources.
type PoolHandler struct{}

// NewPoolHandler returns the handler that cleans up pool resources in VCP.
func NewPoolHandler() *PoolHandler {
	return &PoolHandler{}
}

// JobTypes enumerates the job types supported by the pool handler.
func (h *PoolHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreatePool,
		datamodel.JobTypeCreateLargePool,
	}
}

// Handle removes pool artifacts from VCP for the job when the supervisor
// detects a timeout in NEW state. Create pool is not eligible for PROCESSING
// state timeout handling.
func (h *PoolHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
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

	return h.handleNewStateTimeout(ctx, job, storage, logger)
}

// handleNewStateTimeout handles timeout for jobs in NEW state.
// It deletes the pool from VCP database.
func (h *PoolHandler) handleNewStateTimeout(ctx context.Context, job *datamodel.Job, storage database.Storage, logger log.Logger) error {
	pool, err := storage.GetPoolByUUID(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: pool already deleted in VCP")
			return nil
		}
		return fmt.Errorf("load pool: %w", err)
	}

	if err := storage.DeletePool(ctx, pool); err != nil {
		return fmt.Errorf("delete pool from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: pool %s removed from VCP", pool.UUID)
	return nil
}
