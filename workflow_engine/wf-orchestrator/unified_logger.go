package orchestrator

import (
	"errors"
	"time"
)

// UnifiedLogger is the engine-neutral logger interface produced by the
// workflow logger factories and consumed by workflow code.
type UnifiedLogger interface {
	// WithFields returns a new logger with the given fields added.
	WithFields(fields Fields) UnifiedLogger
	With(fields Fields) UnifiedLogger
	WithError(err error) UnifiedLogger
	WithTime(t time.Time) UnifiedLogger
	Info(msg string, keyvals ...any)
	Error(msg string, keyvals ...any)
	Debug(msg string, keyvals ...any)
	Warn(msg string, keyvals ...any)

	Printf(format string, args ...any)
	Print(args ...any)
	Println(args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	Fatalln(args ...any)
	Panic(args ...any)
	Panicf(format string, args ...any)
	Panicln(args ...any)
	Infof(format string, args ...any)
	Errorf(format string, args ...any)
	Debugf(format string, args ...any)
	Warnf(format string, args ...any)
	Trace(args ...any)
	Tracef(format string, args ...any)
	Traceln(args ...any)
	Warnln(args ...any)
	Errorln(args ...any)
	Debugln(args ...any)
	Infoln(args ...any)
}

const (
	RequestCorrelationID string = "requestCorrelationID"
	RequestDeploymentID  string = "deploymentID"
	// ErrorField is the log field key used by WithError to carry an error.
	ErrorField string = "error"
	// TimeField is the log field key used by WithTime to carry a timestamp.
	TimeField string = "time"
)

// WorkflowLoggerFactory creates a UnifiedLogger for a backend-specific
// workflow context (e.g. a Temporal or go-workflows workflow.Context).
type WorkflowLoggerFactory func(ctx interface{}) (UnifiedLogger, error)

var workflowLoggerFactory WorkflowLoggerFactory

// ChainWorkflowLoggerFactory prepends a factory ahead of any previously
// registered factory. Used by temporalbackend and gowfbackend so both
// backends can coexist in one binary: each backend's context type is handled
// by the first factory that recognizes it.
func ChainWorkflowLoggerFactory(factory WorkflowLoggerFactory) {
	prev := workflowLoggerFactory
	workflowLoggerFactory = func(ctx interface{}) (UnifiedLogger, error) {
		if logger, err := factory(ctx); err == nil {
			return logger, nil
		}
		if prev != nil {
			return prev(ctx)
		}
		return nil, errors.New("no logger found in workflow context")
	}
}

// WorkflowLoggerFromContext resolves a backend-specific workflow context to a
// UnifiedLogger using the registered factory chain.
func WorkflowLoggerFromContext(ctx interface{}) (UnifiedLogger, error) {
	if workflowLoggerFactory == nil {
		return nil, errors.New("no workflow logger factory registered")
	}
	return workflowLoggerFactory(ctx)
}
