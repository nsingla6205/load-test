package util

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	vcplog "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	commonpb "go.temporal.io/api/common/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const loggerInterceptorTestMessage = "logger interceptor test"

type recordedLogEntry struct {
	message   string
	keyValues []interface{}
}

type recordingTemporalLogger struct {
	mu      sync.Mutex
	entries []recordedLogEntry
}

func (l *recordingTemporalLogger) Debug(string, ...interface{}) {}
func (l *recordingTemporalLogger) Warn(string, ...interface{})  {}
func (l *recordingTemporalLogger) Error(string, ...interface{}) {}

func (l *recordingTemporalLogger) Info(message string, keyValues ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, recordedLogEntry{message: message, keyValues: keyValues})
}

func (l *recordingTemporalLogger) entry(message string) (recordedLogEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, entry := range l.entries {
		if entry.message == message {
			return entry, true
		}
	}
	return recordedLogEntry{}, false
}

func logFields(t *testing.T, entry recordedLogEntry) map[string]interface{} {
	t.Helper()
	fields := make(map[string]interface{}, len(entry.keyValues)/2)
	for i := 0; i+1 < len(entry.keyValues); i += 2 {
		key, ok := entry.keyValues[i].(string)
		require.True(t, ok)
		fields[key] = entry.keyValues[i+1]
	}
	return fields
}

func propagatedLoggerHeader(t *testing.T, fields vcplog.Fields) *commonpb.Header {
	t.Helper()
	ctx := context.WithValue(context.Background(), middleware.TemporalSLoggerKey, fields)
	writer := makeStubHeaderReaderWriter()
	require.NoError(t, NewContextMapPropagator().Inject(ctx, writer))
	return &commonpb.Header{Fields: writer.storage}
}

func executeLoggerWorkflow(t *testing.T, workflowID, message string, fields vcplog.Fields, workflowFn func(workflow.Context) error) map[string]interface{} {
	t.Helper()
	logger := &recordingTemporalLogger{}
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(logger)
	suite.SetContextPropagators([]workflow.ContextPropagator{NewContextMapPropagator()})
	suite.SetHeader(propagatedLoggerHeader(t, fields))
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{NewLoggerInterceptor()},
	})
	env.SetStartWorkflowOptions(client.StartWorkflowOptions{ID: workflowID})

	env.ExecuteWorkflow(workflowFn)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	entry, found := logger.entry(message)
	require.True(t, found)
	return logFields(t, entry)
}

func loggerInterceptorLogWorkflow(ctx workflow.Context) error {
	workflow.GetLogger(ctx).Info(loggerInterceptorTestMessage)
	return nil
}

func loggerInterceptorCallerFieldsWorkflow(ctx workflow.Context) error {
	GetLoggerWithFields(ctx, "jobType", "DELETE_VOLUME", "WorkflowID", "spoofed", string(middleware.AuthorizationToken), "must-not-be-logged").
		Info(loggerInterceptorTestMessage)
	return nil
}

func TestLoggerInterceptor_GetLogger_AddsAllowlistedPropagatedFields(t *testing.T) {
	fields := executeLoggerWorkflow(t, "logger-interceptor-allowlist", loggerInterceptorTestMessage, vcplog.Fields{
		string(middleware.RequestCorrelationID): "correlation-123",
		string(middleware.RequestID):            "request-123",
		projectLogField:                         "project-123",
		customerIDLogField:                      "customer-123",
		volumeUUIDLogField:                      "vol-123",
		poolUUIDLogField:                        "pool-123",
		jobIDLogField:                           "job-123",
		"traceMethod":                           "POST",
		"traceURL":                              "https://example.test/volumes?token=secret",
		"WorkflowID":                            "spoofed-workflow",
		"workflowID":                            "spoofed-workflow",
		string(middleware.AuthorizationToken):   "must-not-be-logged",
		"password":                              "must-not-be-logged",
	}, loggerInterceptorLogWorkflow)

	assert.Equal(t, "correlation-123", fields[string(middleware.CorrelationIDName)])
	assert.Equal(t, "request-123", fields[string(middleware.RequestID)])
	assert.Equal(t, "project-123", fields[projectLogField])
	assert.Equal(t, "customer-123", fields[customerIDLogField])
	assert.Equal(t, "vol-123", fields[volumeUUIDLogField])
	assert.Equal(t, "pool-123", fields[poolUUIDLogField])
	assert.Equal(t, "job-123", fields[jobIDLogField])
	assert.NotContains(t, fields, "traceMethod")
	assert.NotContains(t, fields, "traceURL")
	assert.NotContains(t, fields, "WorkflowID")
	assert.NotContains(t, fields, "workflowID")
	assert.NotContains(t, fields, string(middleware.AuthorizationToken))
	assert.NotContains(t, fields, "password")
}

func TestLoggerInterceptor_GetLogger_DropsEmptyValues(t *testing.T) {
	fields := executeLoggerWorkflow(t, "logger-interceptor-empty", loggerInterceptorTestMessage, vcplog.Fields{
		string(middleware.RequestCorrelationID): "correlation-123",
		string(middleware.RequestID):            "",
		projectLogField:                         "",
		customerIDLogField:                      nil,
		volumeUUIDLogField:                      "vol-123",
	}, loggerInterceptorLogWorkflow)

	assert.Equal(t, "correlation-123", fields[string(middleware.CorrelationIDName)])
	assert.Equal(t, "vol-123", fields[volumeUUIDLogField])
	assert.NotContains(t, fields, string(middleware.RequestID))
	assert.NotContains(t, fields, projectLogField)
	assert.NotContains(t, fields, customerIDLogField)
}

func TestLoggerInterceptor_PropagatedFieldsDoNotLeakAcrossExecutions(t *testing.T) {
	first := executeLoggerWorkflow(t, "logger-interceptor-exec-1", "execution-one", vcplog.Fields{
		string(middleware.RequestCorrelationID): "correlation-1",
		customerIDLogField:                      "customer-1",
		volumeUUIDLogField:                      "vol-1",
	}, func(ctx workflow.Context) error {
		workflow.GetLogger(ctx).Info("execution-one")
		return nil
	})
	second := executeLoggerWorkflow(t, "logger-interceptor-exec-2", "execution-two", vcplog.Fields{
		string(middleware.RequestCorrelationID): "correlation-2",
		customerIDLogField:                      "customer-2",
		volumeUUIDLogField:                      "vol-2",
	}, func(ctx workflow.Context) error {
		workflow.GetLogger(ctx).Info("execution-two")
		return nil
	})

	assert.Equal(t, "correlation-1", first[string(middleware.CorrelationIDName)])
	assert.Equal(t, "customer-1", first[customerIDLogField])
	assert.Equal(t, "vol-1", first[volumeUUIDLogField])

	assert.Equal(t, "correlation-2", second[string(middleware.CorrelationIDName)])
	assert.Equal(t, "customer-2", second[customerIDLogField])
	assert.Equal(t, "vol-2", second[volumeUUIDLogField])
	assert.NotEqual(t, first[string(middleware.CorrelationIDName)], second[string(middleware.CorrelationIDName)])
	assert.NotEqual(t, first[volumeUUIDLogField], second[volumeUUIDLogField])
}

func TestGetLoggerWithFields_AddsCallerFieldsWithoutTemporalKeys(t *testing.T) {
	fields := executeLoggerWorkflow(t, "logger-interceptor-caller-fields", loggerInterceptorTestMessage, vcplog.Fields{
		string(middleware.RequestCorrelationID): "correlation-123",
	}, loggerInterceptorCallerFieldsWorkflow)

	assert.Equal(t, "correlation-123", fields[string(middleware.CorrelationIDName)])
	assert.Equal(t, "DELETE_VOLUME", fields["jobType"])
	assert.NotContains(t, fields, "WorkflowID")
	assert.NotContains(t, fields, "workflowID")
	assert.NotContains(t, fields, string(middleware.AuthorizationToken))
}

func TestLoggerInterceptor_GetLogger_WithoutRequestFields(t *testing.T) {
	logger := &recordingTemporalLogger{}
	var suite testsuite.WorkflowTestSuite
	suite.SetLogger(logger)
	env := suite.NewTestWorkflowEnvironment()
	env.SetWorkerOptions(worker.Options{
		Interceptors: []interceptor.WorkerInterceptor{NewLoggerInterceptor()},
	})

	env.ExecuteWorkflow(loggerInterceptorLogWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	entry, found := logger.entry(loggerInterceptorTestMessage)
	require.True(t, found)
	assert.Empty(t, entry.keyValues)
}
