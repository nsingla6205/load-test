package backgroundworkflows

import (
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	middlewarelog "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengineutil "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/workflow"
)

const (
	poolOperationCertificateRotation = "certificate_rotation"
	poolOperationCAReconciliation    = "ca_reconciliation"
	poolOperationPasswordRotation    = "password_rotation"
	poolOperationAuthReconciliation  = "auth_type_1_to_2_reconciliation"
	inheritPoolOperationCorrelation  = "inheritPoolOperationCorrelation"
	temporalCorrelationIDKey         = "x-correlation-id"
)

// withPoolOperationLogging assigns one stable correlation ID to a per-pool
// workflow. The sequencer creates a unique workflow ID for every queued pool
// operation, so that ID is suitable for tracing the workflow and all activities
// from start to finish without sharing an ID with another pool.
func withPoolOperationLogging(
	ctx workflow.Context,
	poolUUID string,
	operation string,
) (workflow.Context, middlewarelog.Logger) {
	workflowID := workflow.GetInfo(ctx).WorkflowExecution.ID
	correlationID := workflowID
	if existing, ok := ctx.Value(middleware.TemporalSLoggerKey).(middlewarelog.Fields); ok {
		inherit, _ := existing[inheritPoolOperationCorrelation].(bool)
		inheritedCorrelationID, _ := existing[string(middleware.RequestCorrelationID)].(string)
		if inherit && inheritedCorrelationID != "" {
			correlationID = inheritedCorrelationID
		}
	}
	fields := map[string]interface{}{
		string(middleware.RequestCorrelationID): correlationID,
		"poolUUID":                              poolUUID,
		"poolOperation":                         operation,
		"workflowID":                            workflowID,
		inheritPoolOperationCorrelation:         false,
	}
	ctx = workflowengineutil.AddExtraLoggerFields(ctx, fields)
	ctx = workflow.WithValue(ctx, temporalCorrelationIDKey, correlationID)

	return ctx, workflowengineutil.GetLogger(ctx)
}

func withInheritedPoolOperationCorrelation(ctx workflow.Context) workflow.Context {
	return workflowengineutil.AddExtraLoggerFields(ctx, map[string]interface{}{
		inheritPoolOperationCorrelation: true,
	})
}
