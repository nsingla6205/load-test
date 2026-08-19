package common

import (
	"context"
	"fmt"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// temporalCorrelationIDKey matches workflow_engine/util context propagator and VLM.
const temporalCorrelationIDKey = "x-correlation-id"

// ResolveUpgradeCorrelationID returns the inbound request correlation ID when
// present on middleware.CorrelationContextKey (header or string), otherwise
// falls back to fallbackID (typically the upgrade job UUID).
//
// It intentionally does not call utils.GetCoRelationIDFromContext, which can
// synthesize a random ID (and mutate logger fields) when TemporalSLoggerKey is
// present without requestCorrelationID.
func ResolveUpgradeCorrelationID(ctx context.Context, fallbackID string) string {
	switch v := ctx.Value(middleware.CorrelationContextKey).(type) {
	case http.Header:
		if correlationID := v.Get(string(middleware.CorrelationIDName)); correlationID != "" {
			return correlationID
		}
	case string:
		if v != "" {
			return v
		}
	}
	return fallbackID
}

// BuildBatchChildUpgradeCorrelationID returns the per-child correlation ID
// batchJobUUID-child-clusterID, matching the Temporal child WorkflowID so log
// explorers and Temporal UI share one ID while retaining the batch UUID prefix
// for end-to-end batch tracing.
func BuildBatchChildUpgradeCorrelationID(batchJobID, clusterID string) string {
	return fmt.Sprintf("%s-child-%s", batchJobID, clusterID)
}

// WithUpgradeCorrelationContext attaches correlationID to the Go context so
// Temporal propagation and structured logs carry it into the workflow.
func WithUpgradeCorrelationContext(ctx context.Context, correlationID string) context.Context {
	loggerFields := log.Fields{}
	if existing, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		for k, v := range existing {
			loggerFields[k] = v
		}
	}
	loggerFields[string(middleware.RequestCorrelationID)] = correlationID

	ctx = context.WithValue(ctx, middleware.CorrelationContextKey, correlationID)
	ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)
	ctx = context.WithValue(ctx, temporalCorrelationIDKey, correlationID)
	return ctx
}
