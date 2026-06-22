package gowfbackend

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	gowf "github.com/cschleiden/go-workflows/workflow"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
)

// GowfWorkflowLogger adapts the repo slog wrapper logger to
// orchestrator.UnifiedLogger for go-workflows execution.
type GowfWorkflowLogger struct {
	logger log.Logger
	fields orchestrator.Fields
}

// WorkflowLoggerFromGowfContext returns a logger for a go-workflows workflow.Context.
func WorkflowLoggerFromGowfContext(ctx gowf.Context) (orchestrator.UnifiedLogger, error) {
	fields := orchestrator.Fields{}
	if corrID, ok := ctx.Value(orchestrator.CorrelationIDKey).(string); ok {
		fields[orchestrator.RequestCorrelationID] = corrID
	}
	if deploymentID, ok := ctx.Value(orchestrator.DeploymentIDKey).(string); ok {
		fields[orchestrator.RequestDeploymentID] = deploymentID
	}
	// A correctly-typed go-workflows context always yields a logger. Missing
	// correlation/deployment IDs simply produce a base logger with empty
	// fields; the error path is reserved for type mismatch (handled by the
	// factory) so ChainWorkflowLoggerFactory does not fall through to another
	// backend when this backend is in fact the right one.
	return (&GowfWorkflowLogger{logger: log.NewLogger()}).WithFields(fields), nil
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

func (l *GowfWorkflowLogger) keyvals() []any {
	keys := make([]string, 0, len(l.fields))
	for k := range l.fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := make([]any, 0, len(keys)*2)
	for _, k := range keys {
		kv = append(kv, k, l.fields[k])
	}
	return kv
}

func (l *GowfWorkflowLogger) Info(msg string, keyvals ...any) {
	l.logger.Info(msg, append(l.keyvals(), keyvals...)...)
}
func (l *GowfWorkflowLogger) Error(msg string, keyvals ...any) {
	l.logger.Error(msg, append(l.keyvals(), keyvals...)...)
}
func (l *GowfWorkflowLogger) Debug(msg string, keyvals ...any) {
	l.logger.Debug(msg, append(l.keyvals(), keyvals...)...)
}
func (l *GowfWorkflowLogger) Warn(msg string, keyvals ...any) {
	l.logger.Warn(msg, append(l.keyvals(), keyvals...)...)
}

func (l *GowfWorkflowLogger) Infof(format string, args ...any) {
	l.logger.Info(fmt.Sprintf(format, args...), l.keyvals()...)
}
func (l *GowfWorkflowLogger) Errorf(format string, args ...any) {
	l.logger.Error(fmt.Sprintf(format, args...), l.keyvals()...)
}
func (l *GowfWorkflowLogger) Debugf(format string, args ...any) {
	l.logger.Debug(fmt.Sprintf(format, args...), l.keyvals()...)
}
func (l *GowfWorkflowLogger) Warnf(format string, args ...any) {
	l.logger.Warn(fmt.Sprintf(format, args...), l.keyvals()...)
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
