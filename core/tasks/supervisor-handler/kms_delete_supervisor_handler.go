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

// KmsDeleteHandler implements Handler for KMS config delete operations.
type KmsDeleteHandler struct{}

// NewKmsDeleteHandler returns the handler that reverts KMS config state for stale delete jobs.
func NewKmsDeleteHandler() *KmsDeleteHandler {
	return &KmsDeleteHandler{}
}

// JobTypes enumerates the job types supported by the KMS delete handler.
func (h *KmsDeleteHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeDeleteKmsConfig,
	}
}

// Handle reverts KMS config state from DELETING to previous state for stale delete jobs.
func (h *KmsDeleteHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: delete job lacks KMS config resource UUID; skipping cleanup")
		return nil
	}

	kmsConfig, err := storage.GetKmsConfig(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: KMS config not found for delete cleanup")
			return nil
		}
		return fmt.Errorf("load KMS config for delete cleanup: %w", err)
	}

	// Only revert if KMS config is in DELETING state
	if kmsConfig.State != datamodel.LifeCycleStateDeleting {
		logger.Warnf("workflow-supervisor-task: KMS config %s not in DELETING state (%s); skipping delete cleanup", kmsConfig.UUID, kmsConfig.State)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to CREATED
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for KMS config %s, defaulting to CREATED", kmsConfig.UUID)
		previousState = datamodel.LifeCycleStateCreated
		previousStateDetails = datamodel.LifeCycleStateCreatedDetails
	}

	if _, err := storage.UpdateKmsConfigState(ctx, kmsConfig.UUID, previousState, previousStateDetails); err != nil {
		return fmt.Errorf("revert KMS config %s state to %s: %w", kmsConfig.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted KMS config %s from DELETING to %s", kmsConfig.UUID, previousState)
	return nil
}
