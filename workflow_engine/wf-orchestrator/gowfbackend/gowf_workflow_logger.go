package gowfbackend

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// GowfWorkflowLogger adapts the repo slog wrapper logger to
// orchestrator.UnifiedLogger for go-workflows execution.
type GowfWorkflowLogger struct {
	logger log.Logger
	fields orchestrator.Fields
}

// contextValues is the value-lookup surface common to context.Context and
// go-workflows' workflow.Context, so field assembly is testable without
// standing up a workflow runtime.
type contextValues interface {
	Value(key interface{}) interface{}
}

// requestLogFields assembles the request-scoped fields carried on a workflow
// context by the propagators.
func requestLogFields(ctx contextValues) orchestrator.Fields {
	fields := orchestrator.Fields{}
	// The propagated request-fields map carries the whole ingress field set
	// (requestID, traceMethod and traceURL alongside the correlation id), so
	// workflow lines match what the HTTP access logs for the same request emit.
	// Read it first; the dedicated id keys below are more specific and win.
	if propagated, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields); ok {
		for k, v := range propagated {
			fields[k] = v
		}
	}
	if corrID, ok := ctx.Value(orchestrator.CorrelationIDKey).(string); ok && corrID != "" {
		fields[orchestrator.RequestCorrelationID] = corrID
	}
	if deploymentID, ok := ctx.Value(orchestrator.DeploymentIDKey).(string); ok && deploymentID != "" {
		fields[orchestrator.RequestDeploymentID] = deploymentID
	}
	return fields
}

// WorkflowLoggerFromGowfContext returns a logger for a go-workflows workflow.Context.
func WorkflowLoggerFromGowfContext(ctx gowf.Context) (orchestrator.UnifiedLogger, error) {
	// A correctly-typed go-workflows context always yields a logger. Missing
	// correlation/deployment IDs simply produce a base logger with empty
	// fields; the error path is reserved for type mismatch (handled by the
	// factory) so ChainWorkflowLoggerFactory does not fall through to another
	// backend when this backend is in fact the right one.
	return (&GowfWorkflowLogger{logger: log.NewLogger()}).WithFields(requestLogFields(ctx)), nil
}

func (l *GowfWorkflowLogger) WithFields(fields orchestrator.Fields) orchestrator.UnifiedLogger {
	merged := make(orchestrator.Fields, len(l.fields)+len(fields))
	maps.Copy(merged, l.fields)
	maps.Copy(merged, fields)
	return &GowfWorkflowLogger{logger: l.logger, fields: merged}
}

func (l *GowfWorkflowLogger) With(fields orchestrator.Fields) orchestrator.UnifiedLogger {
	return l.WithFields(fields)
}

// WithError attaches the error message under the "error" field so it is emitted
// with every subsequent log line. A nil error is a no-op.
func (l *GowfWorkflowLogger) WithError(err error) orchestrator.UnifiedLogger {
	if err == nil {
		return l
	}
	return l.WithFields(orchestrator.Fields{orchestrator.ErrorField: err.Error()})
}

// WithTime attaches an explicit timestamp under the "time" field.
func (l *GowfWorkflowLogger) WithTime(t time.Time) orchestrator.UnifiedLogger {
	return l.WithFields(orchestrator.Fields{orchestrator.TimeField: t})
}

// bound returns the underlying logger with the accumulated fields attached.
// Request-scoped fields are grouped under "requestFields" so VCM log lines have
// the same shape as the Temporal-backed VCP services and a single Cloud Logging
// query matches both. Error and timestamp stay top-level per ADR-0011.
func (l *GowfWorkflowLogger) bound() log.Logger {
	if len(l.fields) == 0 {
		return l.logger
	}
	requestFields, topLevel := partitionFields(l.fields)
	logger := l.logger
	if len(requestFields) > 0 {
		logger = logger.WithFields("requestFields", requestFields)
	}
	if len(topLevel) > 0 {
		logger = logger.With(topLevel)
	}
	return logger
}

// partitionFields splits the accumulated fields into those that belong in the
// nested "requestFields" group and those that stay top-level.
func partitionFields(fields orchestrator.Fields) (requestFields, topLevel log.Fields) {
	requestFields = make(log.Fields, len(fields))
	topLevel = log.Fields{}
	for k, v := range fields {
		switch k {
		case orchestrator.ErrorField, orchestrator.TimeField:
			topLevel[k] = v
		default:
			requestFields[k] = v
		}
	}
	return requestFields, topLevel
}

func (l *GowfWorkflowLogger) Info(msg string, keyvals ...any) {
	l.bound().Info(msg, keyvals...)
}
func (l *GowfWorkflowLogger) Error(msg string, keyvals ...any) {
	l.bound().Error(msg, keyvals...)
}
func (l *GowfWorkflowLogger) Debug(msg string, keyvals ...any) {
	l.bound().Debug(msg, keyvals...)
}
func (l *GowfWorkflowLogger) Warn(msg string, keyvals ...any) {
	l.bound().Warn(msg, keyvals...)
}

func (l *GowfWorkflowLogger) Infof(format string, args ...any) {
	l.bound().Info(fmt.Sprintf(format, args...))
}
func (l *GowfWorkflowLogger) Errorf(format string, args ...any) {
	l.bound().Error(fmt.Sprintf(format, args...))
}
func (l *GowfWorkflowLogger) Debugf(format string, args ...any) {
	l.bound().Debug(fmt.Sprintf(format, args...))
}
func (l *GowfWorkflowLogger) Warnf(format string, args ...any) {
	l.bound().Warn(fmt.Sprintf(format, args...))
}

func (l *GowfWorkflowLogger) Printf(format string, args ...any) { l.Info(fmt.Sprintf(format, args...)) }
func (l *GowfWorkflowLogger) Print(args ...any)                 { l.Info(fmt.Sprint(args...)) }
func (l *GowfWorkflowLogger) Println(args ...any) {
	l.Info(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *GowfWorkflowLogger) Fatal(args ...any) {
	m := fmt.Sprint(args...)
	l.Error(m)
	panic(m)
}
func (l *GowfWorkflowLogger) Fatalf(format string, args ...any) {
	m := fmt.Sprintf(format, args...)
	l.Error(m)
	panic(m)
}
func (l *GowfWorkflowLogger) Fatalln(args ...any) {
	m := strings.TrimRight(fmt.Sprintln(args...), "\n")
	l.Error(m)
	panic(m)
}
func (l *GowfWorkflowLogger) Panic(args ...any) {
	m := fmt.Sprint(args...)
	l.Error(m)
	panic(m)
}
func (l *GowfWorkflowLogger) Panicf(format string, args ...any) {
	m := fmt.Sprintf(format, args...)
	l.Error(m)
	panic(m)
}
func (l *GowfWorkflowLogger) Panicln(args ...any) {
	m := strings.TrimRight(fmt.Sprintln(args...), "\n")
	l.Error(m)
	panic(m)
}
func (l *GowfWorkflowLogger) Trace(args ...any) { l.Debug(fmt.Sprint(args...)) }
func (l *GowfWorkflowLogger) Tracef(format string, args ...any) {
	l.Debug(fmt.Sprintf(format, args...))
}
func (l *GowfWorkflowLogger) Traceln(args ...any) {
	l.Debug(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *GowfWorkflowLogger) Warnln(args ...any) {
	l.Warn(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *GowfWorkflowLogger) Errorln(args ...any) {
	l.Error(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *GowfWorkflowLogger) Debugln(args ...any) {
	l.Debug(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *GowfWorkflowLogger) Infoln(args ...any) {
	l.Info(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}

// GowfWorkflowLoggerFactory resolves a go-workflows workflow.Context to orchestrator.UnifiedLogger.
func GowfWorkflowLoggerFactory(ctx interface{}) (orchestrator.UnifiedLogger, error) {
	wfCtx, ok := ctx.(gowf.Context)
	if !ok {
		return nil, errors.New("not a go-workflows workflow context")
	}
	return WorkflowLoggerFromGowfContext(wfCtx)
}
