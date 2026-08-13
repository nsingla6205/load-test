package supervisorhandler

import (
	"context"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
)

// ActiveDirectoryCreateHandler implements Handler for Active Directory create operations.
type ActiveDirectoryCreateHandler struct{}

// NewActiveDirectoryCreateHandler returns the handler that fails AD state for stale create jobs.
func NewActiveDirectoryCreateHandler() *ActiveDirectoryCreateHandler {
	return &ActiveDirectoryCreateHandler{}
}

// JobTypes enumerates the job types supported by the AD create handler.
func (h *ActiveDirectoryCreateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateActiveDirectory,
	}
}

// Handle repairs an Active Directory left in CREATING by a stale create job.
// Unlike delete, a timed-out create is always transitioned to ERROR (never reverted): the create workflow
// may have partially provisioned external resources (SDE/secret), so there is no safe previous state to
// restore. Both NEW-state timeouts (the workflow never started) and PROCESSING-state timeouts (the workflow
// started then timed out) resolve to ERROR so the resource stops being stuck in CREATING.
func (h *ActiveDirectoryCreateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
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
		logger.Warnf("workflow-supervisor-task: create job lacks Active Directory resource UUID; skipping cleanup")
		return nil
	}

	ad, err := getActiveDirectoryForRepair(ctx, job, storage, logger, datamodel.LifeCycleStateCreating, "create")
	if err != nil || ad == nil {
		return err
	}

	ad.State = datamodel.LifeCycleStateError
	ad.StateDetails = datamodel.LifeCycleStateCreationErrorDetails
	if _, err := storage.UpdateActiveDirectory(ctx, ad); err != nil {
		return fmt.Errorf("transition Active Directory %s to ERROR: %w", ad.UUID, err)
	}

	logger.Infof("workflow-supervisor-task: Active Directory %s transitioned from CREATING to ERROR due to workflow timeout", ad.UUID)
	return nil
}
