package temporalbackend

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/workflow"
)

// TemporalWorkflowLogger adapts the repo slog wrapper logger to
// orchestrator.UnifiedLogger for Temporal execution.
type TemporalWorkflowLogger struct {
	logger log.Logger
	fields orchestrator.Fields
}

// NewTemporalWorkflowLoggerForTest builds a TemporalWorkflowLogger for unit tests.
func NewTemporalWorkflowLoggerForTest(l log.Logger, fields orchestrator.Fields) *TemporalWorkflowLogger {
	if fields == nil {
		fields = orchestrator.Fields{}
	}
	return &TemporalWorkflowLogger{logger: l, fields: fields}
}

// TemporalWorkflowLoggerFactory resolves a Temporal workflow.Context to orchestrator.UnifiedLogger.
func TemporalWorkflowLoggerFactory(ctx interface{}) (orchestrator.UnifiedLogger, error) {
	wfCtx, ok := ctx.(workflow.Context)
	if !ok {
		return nil, errors.New("not a workflow context")
	}
	return WorkflowLoggerFromTemporalContext(wfCtx)
}

// WorkflowLoggerFromTemporalContext returns a logger for a Temporal workflow.Context.
func WorkflowLoggerFromTemporalContext(ctx workflow.Context) (orchestrator.UnifiedLogger, error) {
	fields := orchestrator.Fields{}
	if corrID, ok := ctx.Value(orchestrator.CorrelationIDKey).(string); ok {
		fields[orchestrator.RequestCorrelationID] = corrID
	}
	if deploymentID, ok := ctx.Value(orchestrator.DeploymentIDKey).(string); ok {
		fields[orchestrator.RequestDeploymentID] = deploymentID
	}
	// A correctly-typed Temporal context always yields a logger. Missing
	// correlation/deployment IDs simply produce a base logger with empty
	// fields; the error path is reserved for type mismatch (handled by the
	// factory) so ChainWorkflowLoggerFactory does not fall through to another
	// backend when this backend is in fact the right one.
	return (&TemporalWorkflowLogger{logger: log.NewLogger()}).WithFields(fields), nil
}

func (l *TemporalWorkflowLogger) WithFields(fields orchestrator.Fields) orchestrator.UnifiedLogger {
	newFields := make(orchestrator.Fields, len(l.fields)+len(fields))
	maps.Copy(newFields, l.fields)
	maps.Copy(newFields, fields)
	return &TemporalWorkflowLogger{logger: l.logger, fields: newFields}
}

func (l *TemporalWorkflowLogger) With(fields orchestrator.Fields) orchestrator.UnifiedLogger {
	return l.WithFields(fields)
}

// WithError attaches the error message under the "error" field so it is emitted
// with every subsequent log line. A nil error is a no-op.
func (l *TemporalWorkflowLogger) WithError(err error) orchestrator.UnifiedLogger {
	if err == nil {
		return l
	}
	return l.WithFields(orchestrator.Fields{orchestrator.ErrorField: err.Error()})
}

// WithTime attaches an explicit timestamp under the "time" field.
func (l *TemporalWorkflowLogger) WithTime(t time.Time) orchestrator.UnifiedLogger {
	return l.WithFields(orchestrator.Fields{orchestrator.TimeField: t})
}

func (l *TemporalWorkflowLogger) keyvals() []any {
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

func (l *TemporalWorkflowLogger) Info(msg string, keyvals ...any) {
	l.logger.Info(msg, append(l.keyvals(), keyvals...)...)
}
func (l *TemporalWorkflowLogger) Error(msg string, keyvals ...any) {
	l.logger.Error(msg, append(l.keyvals(), keyvals...)...)
}
func (l *TemporalWorkflowLogger) Debug(msg string, keyvals ...any) {
	l.logger.Debug(msg, append(l.keyvals(), keyvals...)...)
}
func (l *TemporalWorkflowLogger) Warn(msg string, keyvals ...any) {
	l.logger.Warn(msg, append(l.keyvals(), keyvals...)...)
}

func (l *TemporalWorkflowLogger) Infof(format string, args ...any) {
	l.logger.Info(fmt.Sprintf(format, args...), l.keyvals()...)
}
func (l *TemporalWorkflowLogger) Errorf(format string, args ...any) {
	l.logger.Error(fmt.Sprintf(format, args...), l.keyvals()...)
}
func (l *TemporalWorkflowLogger) Debugf(format string, args ...any) {
	l.logger.Debug(fmt.Sprintf(format, args...), l.keyvals()...)
}
func (l *TemporalWorkflowLogger) Warnf(format string, args ...any) {
	l.logger.Warn(fmt.Sprintf(format, args...), l.keyvals()...)
}

func (l *TemporalWorkflowLogger) Printf(format string, args ...any) {
	l.Info(fmt.Sprintf(format, args...))
}
func (l *TemporalWorkflowLogger) Print(args ...any) { l.Info(fmt.Sprint(args...)) }
func (l *TemporalWorkflowLogger) Println(args ...any) {
	l.Info(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *TemporalWorkflowLogger) Fatal(args ...any) {
	m := fmt.Sprint(args...)
	l.Error(m)
	panic(m)
}
func (l *TemporalWorkflowLogger) Fatalf(format string, args ...any) {
	m := fmt.Sprintf(format, args...)
	l.Error(m)
	panic(m)
}
func (l *TemporalWorkflowLogger) Fatalln(args ...any) {
	m := strings.TrimRight(fmt.Sprintln(args...), "\n")
	l.Error(m)
	panic(m)
}
func (l *TemporalWorkflowLogger) Panic(args ...any) {
	m := fmt.Sprint(args...)
	l.Error(m)
	panic(m)
}
func (l *TemporalWorkflowLogger) Panicf(format string, args ...any) {
	m := fmt.Sprintf(format, args...)
	l.Error(m)
	panic(m)
}
func (l *TemporalWorkflowLogger) Panicln(args ...any) {
	m := strings.TrimRight(fmt.Sprintln(args...), "\n")
	l.Error(m)
	panic(m)
}
func (l *TemporalWorkflowLogger) Trace(args ...any) { l.Debug(fmt.Sprint(args...)) }
func (l *TemporalWorkflowLogger) Tracef(format string, args ...any) {
	l.Debug(fmt.Sprintf(format, args...))
}
func (l *TemporalWorkflowLogger) Traceln(args ...any) {
	l.Debug(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *TemporalWorkflowLogger) Warnln(args ...any) {
	l.Warn(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *TemporalWorkflowLogger) Errorln(args ...any) {
	l.Error(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *TemporalWorkflowLogger) Debugln(args ...any) {
	l.Debug(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
func (l *TemporalWorkflowLogger) Infoln(args ...any) {
	l.Info(strings.TrimRight(fmt.Sprintln(args...), "\n"))
}
