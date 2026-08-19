package workflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"go.temporal.io/sdk/workflow"
)

// resolveUpgradeWorkflowCorrelationID prefers an explicit correlation ID, then a
// propagated logger field, then fallbackID (typically JobID or BatchJobID).
func resolveUpgradeWorkflowCorrelationID(ctx workflow.Context, explicitID, fallbackID string) string {
	if explicitID != "" {
		return explicitID
	}
	if existing, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx); err == nil && existing != "" {
		return existing
	}
	return fallbackID
}
