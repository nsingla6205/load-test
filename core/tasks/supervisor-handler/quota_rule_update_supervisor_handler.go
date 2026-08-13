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

// QuotaRuleUpdateHandler implements Handler for QuotaRule update operations.
type QuotaRuleUpdateHandler struct{}

// NewQuotaRuleUpdateHandler returns the handler that reverts quota rule state for stale update jobs.
func NewQuotaRuleUpdateHandler() *QuotaRuleUpdateHandler {
	return &QuotaRuleUpdateHandler{}
}

// JobTypes enumerates the job types supported by the quota rule update handler.
func (h *QuotaRuleUpdateHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeUpdateQuotaRule,
	}
}

// Handle reverts quota rule state from UPDATING to previous state for stale update jobs.
func (h *QuotaRuleUpdateHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if !IsActionableEvent(event) {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: update job lacks quota rule resource UUID; skipping cleanup")
		return nil
	}

	quotaRule, err := storage.GetQuotaRuleByUUID(ctx, job.JobAttributes.ResourceUUID, job.AccountID.Int64)
	if err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: quota rule not found for update cleanup")
			return nil
		}
		return fmt.Errorf("load quota rule for update cleanup: %w", err)
	}

	// Only revert if quota rule is in UPDATING state
	if quotaRule.State != datamodel.LifeCycleStateUpdating {
		logger.Infof("workflow-supervisor-task: quota rule %s not in UPDATING state (%s); skipping update cleanup", quotaRule.UUID, quotaRule.State)
		return nil
	}

	// Use stored previous state from job attributes, with a fallback to READY
	previousState := job.JobAttributes.PreviousState
	previousStateDetails := job.JobAttributes.PreviousStateDetails

	if previousState == "" {
		logger.Warnf("workflow-supervisor-task: previous state not found in job attributes for quota rule %s, defaulting to READY", quotaRule.UUID)
		previousState = datamodel.LifeCycleStateREADY
		previousStateDetails = datamodel.LifeCycleStateAvailableDetails
	}

	quotaRule.State = previousState
	quotaRule.StateDetails = previousStateDetails

	if _, err := storage.UpdateQuotaRule(ctx, quotaRule); err != nil {
		return fmt.Errorf("revert quota rule %s state to %s: %w", quotaRule.UUID, previousState, err)
	}

	logger.Infof("workflow-supervisor-task: reverted quota rule %s from UPDATING to %s", quotaRule.UUID, previousState)
	return nil
}
