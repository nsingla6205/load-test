package orchestrator

import (
	"context"
	"net/http"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

// CorrelationIDFromContext resolves the inbound request's correlation id.
//
// The HTTP logging middleware and the workflow context propagators store the
// same id under different keys: the middleware uses the typed
// middleware.CorrelationContextKey (and mirrors it into the logger field map
// under TemporalSLoggerKey), while the propagators read the plain-string
// CorrelationIDKey. This resolves all of them, preferring the propagator key so
// an already-bridged context is returned unchanged. It returns "" when the
// context carries no correlation id.
func CorrelationIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(CorrelationIDKey).(string); ok && id != "" {
		return id
	}
	switch v := ctx.Value(middleware.CorrelationContextKey).(type) {
	case string:
		if v != "" {
			return v
		}
	case http.Header:
		if id := v.Get(string(middleware.CorrelationIDName)); id != "" {
			return id
		}
	}
	if fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		if id, ok := fields[RequestCorrelationID].(string); ok && id != "" {
			return id
		}
	}
	return ""
}

// DeploymentIDFromContext returns the deployment id on the propagator key, or
// "" when absent.
func DeploymentIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(DeploymentIDKey).(string); ok {
		return id
	}
	return ""
}

// WithCorrelationID stores correlationID under the key the workflow context
// propagators read. An empty id leaves ctx unchanged.
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	if correlationID == "" {
		return ctx
	}
	return context.WithValue(ctx, CorrelationIDKey, correlationID)
}

// WithDeploymentID stores deploymentID under the key the workflow context
// propagators read. An empty id leaves ctx unchanged.
func WithDeploymentID(ctx context.Context, deploymentID string) context.Context {
	if deploymentID == "" {
		return ctx
	}
	return context.WithValue(ctx, DeploymentIDKey, deploymentID)
}

// WithRequestContext promotes the inbound correlation id onto the propagator
// key so a workflow dispatched from ctx — and every activity and child workflow
// it schedules — logs under the caller's correlation id. It is a no-op when the
// id is already on the propagator key or when the context carries none.
func WithRequestContext(ctx context.Context) context.Context {
	return WithCorrelationID(ctx, CorrelationIDFromContext(ctx))
}
