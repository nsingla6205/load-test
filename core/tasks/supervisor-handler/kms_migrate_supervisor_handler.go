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

// KmsMigrateHandler implements Handler for KMS config migrate operations.
type KmsMigrateHandler struct{}

// NewKmsMigrateHandler returns the handler that reverts KMS config state for stale migrate jobs.
func NewKmsMigrateHandler() *KmsMigrateHandler {
	return &KmsMigrateHandler{}
}

// JobTypes enumerates the job types supported by the KMS migrate handler.
func (h *KmsMigrateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeMigrateKmsConfig,
	}
}

// Handle reverts KMS config state from MIGRATING to previous state for stale migrate jobs.
func (h *KmsMigrateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: migrate job lacks KMS config resource UUID; skipping cleanup")
		return nil
	}

	kmsConfig, err := storage.GetKmsConfig(ctx, job.JobAttributes.ResourceUUID)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: KMS config not found for migrate cleanup")
			return nil
		}
		return fmt.Errorf("load KMS config for migrate cleanup: %w", err)
	}

	// Only revert if KMS config is in MIGRATING state
	if kmsConfig.State != datamodel.LifeCycleStateMigrating {
		logger.Warnf("workflow-supervisor-task: KMS config %s not in MIGRATING state (%s); skipping migrate cleanup", kmsConfig.UUID, kmsConfig.State)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to READY
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for KMS config %s, defaulting to READY", kmsConfig.UUID)
		previousState = datamodel.LifeCycleStateREADY
		previousStateDetails = datamodel.LifeCycleStateReadyDetails
	}

	if _, err := storage.UpdateKmsConfigState(ctx, kmsConfig.UUID, previousState, previousStateDetails); err != nil {
		return fmt.Errorf("revert KMS config %s state to %s: %w", kmsConfig.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted KMS config %s from MIGRATING to %s", kmsConfig.UUID, previousState)
	return nil
}
