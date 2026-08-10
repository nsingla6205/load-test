package util

import (
	"context"
	"errors"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
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
	return nil, errors.New("no logger found in api context")
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

func AddExtraLoggerFields(ctx workflow.Context, keyValMap map[string]interface{}) workflow.Context {
	for key, val := range keyValMap {
		if loggerFields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
			loggerFields[key] = val
			ctx = workflow.WithValue(ctx, middleware.TemporalSLoggerKey, loggerFields)
		} else {
			// If the logger fields do not exist, create a new one with the provided key and value.
			// This will be useful for background workflows where the logger fields might not be initialized.
			newLoggerFields := log.Fields{key: val}
			ctx = workflow.WithValue(ctx, middleware.TemporalSLoggerKey, newLoggerFields)
		}
	}
	return ctx
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
