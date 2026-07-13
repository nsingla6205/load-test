package api

import (
	"context"
	"errors"
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/workflowquery"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/serviceerror"
)


type existingWorkflow struct {
	Found  bool
	Status workflowquery.WorkflowStatus
	Result workflowquery.Result
}

func (h *Handler) lookupExistingWorkflow(ctx context.Context, opcWorkRequestID string) (existingWorkflow, error) {
	if h == nil || h.TemporalClient == nil || opcWorkRequestID == "" {
		return existingWorkflow{}, nil
	}

	res, err := workflowQueryFn(ctx, h.TemporalClient, opcWorkRequestID, "")
	if err != nil {
		if isWorkflowNotFound(err) {
			return existingWorkflow{Found: false}, nil
		}
		util.GetLogger(ctx).Error("idempotency lookup failed", "workflowID", opcWorkRequestID, "error", err)
		return existingWorkflow{}, err
	}

	return existingWorkflow{Found: true, Status: res.Status, Result: res}, nil
}

// isWorkflowNotFound reports whether err indicates the workflow id has never been started.
func isWorkflowNotFound(err error) bool {
	var notFound *serviceerror.NotFound
	return errors.As(err, &notFound)
}

func isTerminalFailure(status workflowquery.WorkflowStatus) bool {
	return status == workflowquery.WorkflowStatusFailed || status == workflowquery.WorkflowStatusTimedOut
}

func isCompleted(status workflowquery.WorkflowStatus) bool {
	return status == workflowquery.WorkflowStatusCompleted
}

func (e existingWorkflow) failureMessage() string {
	if e.Result.Error != nil && strings.TrimSpace(e.Result.Error.Message) != "" {
		return e.Result.Error.Message
	}
	return "previous request with this opc-work-request-id failed; retry with a new opc-work-request-id"
}
