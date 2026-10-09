package util

import (
	"strings"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	vcplog "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.temporal.io/sdk/interceptor"
	temporallog "go.temporal.io/sdk/log"
	"go.temporal.io/sdk/workflow"
)

const (
	projectLogField    = "project"
	customerIDLogField = "customerID"
	volumeUUIDLogField = "volumeUUID"
	poolUUIDLogField   = "poolUUID"
	jobIDLogField      = "jobID"
)

// NewLoggerInterceptor returns a worker interceptor that enriches Temporal
// workflow loggers with propagated VCP request fields.
func NewLoggerInterceptor() interceptor.WorkerInterceptor {
	return &loggerInterceptor{}
}

type loggerInterceptor struct {
	interceptor.WorkerInterceptorBase
}

func (i *loggerInterceptor) InterceptWorkflow(
	_ workflow.Context,
	next interceptor.WorkflowInboundInterceptor,
) interceptor.WorkflowInboundInterceptor {
	return &loggerWorkflowInboundInterceptor{
		WorkflowInboundInterceptorBase: interceptor.WorkflowInboundInterceptorBase{Next: next},
	}
}

type loggerWorkflowInboundInterceptor struct {
	interceptor.WorkflowInboundInterceptorBase
}

func (i *loggerWorkflowInboundInterceptor) Init(outbound interceptor.WorkflowOutboundInterceptor) error {
	return i.Next.Init(&loggerWorkflowOutboundInterceptor{
		WorkflowOutboundInterceptorBase: interceptor.WorkflowOutboundInterceptorBase{Next: outbound},
	})
}

type loggerWorkflowOutboundInterceptor struct {
	interceptor.WorkflowOutboundInterceptorBase
}

func (i *loggerWorkflowOutboundInterceptor) GetLogger(ctx workflow.Context) temporallog.Logger {
	// Temporal's workflow logger already carries WorkflowID, RunID, WorkflowType,
	// and Attempt. Do not add a second workflow ID key.
	logger := i.Next.GetLogger(ctx)
	fields, ok := ctx.Value(middleware.TemporalSLoggerKey).(vcplog.Fields)
	if !ok {
		return logger
	}

	keyValues := temporalLoggerFields(fields)
	if len(keyValues) == 0 {
		return logger
	}
	return temporallog.With(logger, keyValues...)
}

// GetLoggerWithFields returns the Temporal workflow logger with extra key/value
// pairs. The interceptor still attaches the allowlisted fields already on the
// workflow context; callers pass additional pairs here for one log line.
// Fields that should appear on every later workflow.GetLogger call belong on
// the context via AddExtraLoggerFields.
//
// Temporal's outbound interceptor GetLogger(ctx) cannot take extra arguments, so
// workflows that need more than the context fields should call this helper.
func GetLoggerWithFields(ctx workflow.Context, extraFields ...interface{}) temporallog.Logger {
	return withLoggerFields(workflow.GetLogger(ctx), extraFields...)
}

func withLoggerFields(logger temporallog.Logger, extraFields ...interface{}) temporallog.Logger {
	sanitized := sanitizeLoggerFields(extraFields)
	if len(sanitized) == 0 {
		return logger
	}
	return temporallog.With(logger, sanitized...)
}

func temporalLoggerFields(fields vcplog.Fields) []interface{} {
	keyValues := make([]interface{}, 0, len(fields)*2)
	seen := make(map[string]struct{}, len(fields))
	appendField := func(outputKey string, value interface{}) {
		if skipLoggerField(outputKey) || value == nil || value == "" {
			return
		}
		if _, exists := seen[outputKey]; exists {
			return
		}
		seen[outputKey] = struct{}{}
		keyValues = append(keyValues, outputKey, value)
	}

	// Standard request fields only. traceMethod, traceURL, and any future
	// middleware keys stay off the workflow logger.
	if value, ok := fields[string(middleware.RequestCorrelationID)]; ok {
		appendField(string(middleware.CorrelationIDName), value)
	}
	if value, ok := fields[string(middleware.RequestID)]; ok {
		appendField(string(middleware.RequestID), value)
	}
	if value, ok := fields[projectLogField]; ok {
		appendField(projectLogField, value)
	}
	// Workflow-domain fields are copied only when a workflow put them on the
	// context with AddExtraLoggerFields. They are not derived from the HTTP map.
	for _, key := range []string{customerIDLogField, volumeUUIDLogField, poolUUIDLogField, jobIDLogField} {
		if value, ok := fields[key]; ok {
			appendField(key, value)
		}
	}
	return keyValues
}

func sanitizeLoggerFields(extraFields []interface{}) []interface{} {
	if len(extraFields) < 2 {
		return nil
	}
	sanitized := make([]interface{}, 0, len(extraFields))
	for i := 0; i+1 < len(extraFields); i += 2 {
		key, ok := extraFields[i].(string)
		if !ok || skipLoggerField(key) {
			continue
		}
		value := extraFields[i+1]
		if value == nil || value == "" {
			continue
		}
		sanitized = append(sanitized, key, value)
	}
	return sanitized
}

func skipLoggerField(key string) bool {
	switch key {
	case "WorkflowID", "RunID", "WorkflowType", "Attempt", "workflowID":
		// Temporal already owns these. Copying them produces a second value
		// for the same identifier.
		return true
	}
	lower := strings.ToLower(key)
	return strings.Contains(lower, "password") ||
		strings.Contains(lower, "secret") ||
		strings.Contains(lower, "authorization") ||
		strings.Contains(lower, "token")
}
