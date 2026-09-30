package util

import (
	"context"
	"errors"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/workflow"
)

type contextHandler interface {
	extractLogger() (log.Logger, error)
}

type apiContext struct {
	ctx context.Context
}

func (c apiContext) extractLogger() (log.Logger, error) {
	if logger, ok := c.ctx.Value(middleware.ContextSLoggerKey).(log.Logger); ok {
		return logger, nil
	}
	if logger, ok := c.ctx.Value(middleware.ContexMainThreadID).(log.Logger); ok {
		return logger, nil
	}

	if loggerFields, ok := c.ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		logger := log.NewLogger().WithFields("requestFields", requestFieldsForStructuredLog(loggerFields))
		return logger, nil
	}

	// go-workflows dispatches activities with a plain context.Context carrying
	// only the propagator's string keys, so none of the lookups above match.
	// Rebuild the request fields from those keys instead of falling through to
	// a bare global logger.
	if fields := propagatedRequestFields(c.ctx); len(fields) > 0 {
		return log.NewLogger().WithFields("requestFields", fields), nil
	}
	return nil, errors.New("no logger found in api context")
}

// propagatedRequestFields builds the request-field map from the ids carried by
// the workflow context propagators. It returns an empty map when the context
// carries none of them.
func propagatedRequestFields(ctx context.Context) log.Fields {
	fields := log.Fields{}
	if id, ok := ctx.Value(vlmCorrelationIDKey).(string); ok && id != "" {
		fields[orchestrator.RequestCorrelationID] = id
	}
	if id, ok := ctx.Value(vlmDeploymentIDKey).(string); ok && id != "" {
		fields[orchestrator.RequestDeploymentID] = id
	}
	return fields
}

type workflowContext struct {
	ctx workflow.Context
}

// extractLogger extracts the logger from the workflow context
func (c workflowContext) extractLogger() (log.Logger, error) {
	if logger, ok := c.ctx.Value(middleware.ContextSLoggerKey).(log.Logger); ok {
		return logger, nil
	}

	if loggerFields, ok := c.ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		logger := log.NewLogger().WithFields("requestFields", requestFieldsForStructuredLog(loggerFields))
		return logger, nil
	}
	return nil, errors.New("no logger found in workflow context")
}

func GetLogger(ctx interface{}) log.Logger {
	var ctxHandler contextHandler
	switch ctxReceived := ctx.(type) {
	case context.Context:
		ctxHandler = apiContext{
			ctx: ctxReceived,
		}
	case workflow.Context:
		ctxHandler = workflowContext{
			ctx: ctxReceived,
		}
	}

	// Unrecognized context types (e.g. the orchestrator's engine-agnostic
	// *Context) leave ctxHandler nil; return a fresh logger instead of
	// dereferencing a nil interface and panicking.
	if ctxHandler == nil {
		return log.NewLogger()
	}

	logger, err := ctxHandler.extractLogger()
	if err != nil {
		newLogger := log.NewLogger()
		return newLogger
	}
	return logger
}

// AddExtraLoggerFields returns a workflow context with additional logger fields.
// Existing TemporalSLoggerKey fields are copied before mutation so child/sibling
// contexts cannot overwrite each other or bleed back into the parent map.
func AddExtraLoggerFields(ctx workflow.Context, keyValMap map[string]interface{}) workflow.Context {
	cloned := log.Fields{}
	if existing, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		for k, v := range existing {
			cloned[k] = v
		}
	}
	for key, val := range keyValMap {
		cloned[key] = val
	}
	return workflow.WithValue(ctx, middleware.TemporalSLoggerKey, cloned)
}

// requestFieldsForStructuredLog limits JSON requestFields for OCI: when opc-request-id is present
// in propagated fields, log only opc-request-id, traceMethod, and traceURL. Other services keep the
// full field map.
func requestFieldsForStructuredLog(fields log.Fields) log.Fields {
	opcKey := string(middleware.OPCRequestIDHeaderName)
	if opc, ok := fields[opcKey].(string); ok && opc != "" {
		out := log.Fields{opcKey: opc}
		if m, ok := fields["traceMethod"].(string); ok && m != "" {
			out["traceMethod"] = m
		}
		if u, ok := fields["traceURL"].(string); ok && u != "" {
			out["traceURL"] = u
		}
		return out
	}
	return fields
}
