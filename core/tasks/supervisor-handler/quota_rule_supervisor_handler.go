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

// QuotaRuleHandler implements Handler for QuotaRule create operations.
type QuotaRuleHandler struct{}

// NewQuotaRuleHandler returns the handler that cleans up quota rule resources in VCP.
func NewQuotaRuleHandler() *QuotaRuleHandler {
	return &QuotaRuleHandler{}
}

// JobTypes enumerates the job types supported by the quota rule handler.
func (h *QuotaRuleHandler) JobTypes() []datamodel.JobType {
	return []datamodel.JobType{
		datamodel.JobTypeCreateQuotaRule,
	}
}

// Handle removes quota rule artifacts from VCP for the job.
func (h *QuotaRuleHandler) Handle(ctx context.Context, job *datamodel.Job, event Event, storage database.Storage) error {
	if event != EventTimeout {
		return nil
	}

	logger := util.GetLogger(ctx).With(log.Fields{
		"jobUUID":                               job.UUID,
		"jobType":                               job.Type,
		string(middleware.RequestCorrelationID): utils.GetCoRelationIDFromContext(ctx),
	})

	if job.JobAttributes == nil || job.JobAttributes.ResourceUUID == "" {
		logger.Warnf("workflow-supervisor-task: job lacks quota rule resource UUID; skipping cleanup")
		return nil
	}

	if _, err := storage.DeleteQuotaRule(ctx, job.JobAttributes.ResourceUUID); err != nil {
		if vsaerrors.IsNotFoundErr(err) {
			logger.Warnf("workflow-supervisor-task: quota rule already deleted in VCP")
			return nil
		}
		return fmt.Errorf("delete quota rule from VCP: %w", err)
	}

	logger.Infof("workflow-supervisor-task: quota rule %s removed from VCP", job.JobAttributes.ResourceUUID)
	return nil
}
