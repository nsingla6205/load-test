package util

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/suite"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	orchestrator "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/wf-orchestrator"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type LoggerExtractorTestSuite struct {
	suite.Suite
	testsuite.WorkflowTestSuite
}

func TestLoggerExtractorSuite(t *testing.T) {
	suite.Run(t, new(LoggerExtractorTestSuite))
}

func (s *LoggerExtractorTestSuite) TestGetLoggerFromAPIContext() {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, log.Fields{"key": "value"})

	logger := GetLogger(ctx)
	s.NotNil(logger)
}

func (s *LoggerExtractorTestSuite) TestGetLoggerFromAPIContextNoLogger() {
	ctx := context.Background()
	apiCtx := apiContext{ctx: ctx}
	logger, err := apiCtx.extractLogger()
	s.Error(err)
	s.Nil(logger)
	s.EqualError(err, "no logger found in api context")
}

func (s *LoggerExtractorTestSuite) TestGetLoggerFromAPIContextWithMainThreadID() {
	ctx := context.Background()
	mockLogger := log.NewLogger()
	ctx = context.WithValue(ctx, middleware.ContexMainThreadID, mockLogger)

	apiCtx := apiContext{ctx: ctx}
	logger, err := apiCtx.extractLogger()
	s.NoError(err)
	s.NotNil(logger)
	s.Equal(mockLogger, logger)
}

func (s *LoggerExtractorTestSuite) TestGetLoggerFromWorkflowContext() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(s.CheckNoLoggerInWorkflowContext)

	s.True(env.IsWorkflowCompleted())
	s.Error(env.GetWorkflowError())
}

func (s *LoggerExtractorTestSuite) CheckNoLoggerInWorkflowContext(ctx workflow.Context) error {
	logger := GetLogger(ctx)
	if logger != nil {
		return errors.New("logger found but was supposed to be not present")
	}
	return nil
}

func (s *LoggerExtractorTestSuite) TestAddExtraLoggerFields() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		// Add extra fields
		ctx = AddExtraLoggerFields(ctx, map[string]interface{}{"key": "value", "another": 123})
		// Retrieve fields to verify
		loggerFields, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields)
		s.True(ok)
		s.Equal("value", loggerFields["key"])
		s.Equal(123, loggerFields["another"])
		return nil
	})
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *LoggerExtractorTestSuite) TestAddExtraLoggerFieldsDoesNotMutateParentMap() {
	env := s.NewTestWorkflowEnvironment()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		parentFields := log.Fields{
			"batchJobID": "batch-1",
			string(middleware.RequestCorrelationID): "parent-corr",
		}
		ctx = workflow.WithValue(ctx, middleware.TemporalSLoggerKey, parentFields)

		childCtx := AddExtraLoggerFields(ctx, map[string]interface{}{
			"childJobID":                            "child-1",
			"clusterID":                             "cluster-1",
			string(middleware.RequestCorrelationID): "child-corr",
		})

		childFields, ok := childCtx.Value(middleware.TemporalSLoggerKey).(log.Fields)
		s.True(ok)
		s.Equal("child-1", childFields["childJobID"])
		s.Equal("cluster-1", childFields["clusterID"])
		s.Equal("child-corr", childFields[string(middleware.RequestCorrelationID)])
		s.Equal("batch-1", childFields["batchJobID"])

		// Parent map must remain unchanged (no in-place mutation / bleed-back).
		s.Equal("parent-corr", parentFields[string(middleware.RequestCorrelationID)])
		s.NotContains(parentFields, "childJobID")
		s.NotContains(parentFields, "clusterID")

		parentFromCtx, ok := ctx.Value(middleware.TemporalSLoggerKey).(log.Fields)
		s.True(ok)
		s.Equal("parent-corr", parentFromCtx[string(middleware.RequestCorrelationID)])
		s.NotContains(parentFromCtx, "childJobID")
		return nil
	})
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *LoggerExtractorTestSuite) TestWorkflowContextExtractLoggerFromContextSLoggerKey() {
	env := s.NewTestWorkflowEnvironment()
	mockL := log.NewLogger()
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, middleware.ContextSLoggerKey, mockL)
		wc := workflowContext{ctx: ctx}
		l, err := wc.extractLogger()
		s.NoError(err)
		s.Equal(mockL, l)
		return nil
	})
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

func (s *LoggerExtractorTestSuite) TestWorkflowContextExtractLoggerFromTemporalFieldsWithOPC() {
	env := s.NewTestWorkflowEnvironment()
	opcKey := string(middleware.OPCRequestIDHeaderName)
	env.ExecuteWorkflow(func(ctx workflow.Context) error {
		ctx = workflow.WithValue(ctx, middleware.TemporalSLoggerKey, log.Fields{
			opcKey:        "req-id-1",
			"traceMethod": "GET",
			"traceURL":    "/pools",
			"other":       "hidden",
		})
		wc := workflowContext{ctx: ctx}
		l, err := wc.extractLogger()
		s.NoError(err)
		s.NotNil(l)
		return nil
	})
	s.True(env.IsWorkflowCompleted())
	s.NoError(env.GetWorkflowError())
}

// TestAPIContextFromGoWorkflowsActivityContext covers the gap that left VCM
// activity logs with no fields at all: go-workflows dispatches activities with
// a plain context carrying only the propagator's string keys, which none of the
// earlier lookups match.
func (s *LoggerExtractorTestSuite) TestAPIContextFromGoWorkflowsActivityContext() {
	ctx := context.WithValue(context.Background(), vlmCorrelationIDKey, "corr-1")
	ctx = context.WithValue(ctx, vlmDeploymentIDKey, "deploy-1")

	ac := apiContext{ctx: ctx}
	l, err := ac.extractLogger()
	s.NoError(err)
	s.NotNil(l)

	fields := propagatedRequestFields(ctx)
	s.Equal("corr-1", fields[orchestrator.RequestCorrelationID])
	s.Equal("deploy-1", fields[orchestrator.RequestDeploymentID])
}

func (s *LoggerExtractorTestSuite) TestPropagatedRequestFieldsEmptyWithoutIDs() {
	s.Empty(propagatedRequestFields(context.Background()))
	s.Empty(propagatedRequestFields(context.WithValue(context.Background(), vlmCorrelationIDKey, "")))
}

// The propagated-id fallback is last, so a context that already carries a bound
// request logger (the Temporal-backed VCP path) keeps resolving to that logger.
func (s *LoggerExtractorTestSuite) TestAPIContextPrefersBoundLoggerOverPropagatedIDs() {
	bound := log.NewLogger().WithFields("requestFields", log.Fields{"requestCorrelationID": "corr-bound"})

	ctx := context.WithValue(context.Background(), vlmCorrelationIDKey, "corr-propagated")
	ctx = context.WithValue(ctx, middleware.ContextSLoggerKey, bound)

	l, err := apiContext{ctx: ctx}.extractLogger()
	s.NoError(err)
	s.Same(bound, l)
}

func (s *LoggerExtractorTestSuite) TestAPIContextTemporalFieldsWithOPC() {
	ctx := context.Background()
	opcKey := string(middleware.OPCRequestIDHeaderName)
	ctx = context.WithValue(ctx, middleware.TemporalSLoggerKey, log.Fields{
		opcKey:        "opc-1",
		"traceMethod": "POST",
		"traceURL":    "/api",
	})
	ac := apiContext{ctx: ctx}
	l, err := ac.extractLogger()
	s.NoError(err)
	s.NotNil(l)
}
