package backgroundworkflows

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	middlewarelog "github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	workflowengineutil "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

type poolOperationLogFields struct {
	CorrelationID string
	TemporalID    string
	PoolUUID      string
	Operation     string
	WorkflowID    string
}

func capturePoolOperationLogFields(ctx context.Context) (poolOperationLogFields, error) {
	fields, _ := ctx.Value(middleware.TemporalSLoggerKey).(middlewarelog.Fields)
	return poolOperationLogFields{
		CorrelationID: stringField(fields, string(middleware.RequestCorrelationID)),
		TemporalID:    contextString(ctx, temporalCorrelationIDKey),
		PoolUUID:      stringField(fields, "poolUUID"),
		Operation:     stringField(fields, "poolOperation"),
		WorkflowID:    stringField(fields, "workflowID"),
	}, nil
}

func contextString(ctx context.Context, key string) string {
	value, _ := ctx.Value(key).(string)
	return value
}

func stringField(fields middlewarelog.Fields, key string) string {
	value, _ := fields[key].(string)
	return value
}

func poolOperationLoggingTestWorkflow(ctx workflow.Context) (poolOperationLogFields, error) {
	const poolUUID = "pool-uuid"
	ctx, _ = withPoolOperationLogging(ctx, poolUUID, poolOperationCertificateRotation)
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	var fields poolOperationLogFields
	err := workflow.ExecuteActivity(ctx, capturePoolOperationLogFields).Get(ctx, &fields)
	return fields, err
}

func nestedPoolOperationLoggingTestWorkflow(ctx workflow.Context) (poolOperationLogFields, error) {
	ctx, _ = withPoolOperationLogging(ctx, "pool-uuid", poolOperationPasswordRotation)
	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	})

	var fields poolOperationLogFields
	err := workflow.ExecuteActivity(ctx, capturePoolOperationLogFields).Get(ctx, &fields)
	return fields, err
}

func parentPoolOperationLoggingTestWorkflow(ctx workflow.Context) (poolOperationLogFields, error) {
	ctx, _ = withPoolOperationLogging(ctx, "pool-uuid", poolOperationCertificateRotation)
	ctx = withInheritedPoolOperationCorrelation(ctx)

	var fields poolOperationLogFields
	err := workflow.ExecuteChildWorkflow(ctx, nestedPoolOperationLoggingTestWorkflow).Get(ctx, &fields)
	return fields, err
}

func TestWithPoolOperationLoggingPropagatesStableFieldsToActivities(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{
		workflowengineutil.NewContextMapPropagator(),
	})
	env.RegisterWorkflow(poolOperationLoggingTestWorkflow)
	env.RegisterActivity(capturePoolOperationLogFields)

	env.ExecuteWorkflow(poolOperationLoggingTestWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var fields poolOperationLogFields
	require.NoError(t, env.GetWorkflowResult(&fields))
	require.NotEmpty(t, fields.CorrelationID)
	require.Equal(t, fields.CorrelationID, fields.TemporalID)
	require.Equal(t, fields.CorrelationID, fields.WorkflowID)
	require.Equal(t, "pool-uuid", fields.PoolUUID)
	require.Equal(t, poolOperationCertificateRotation, fields.Operation)
}

func TestNestedPoolOperationInheritsParentCorrelationID(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	env.SetContextPropagators([]workflow.ContextPropagator{
		workflowengineutil.NewContextMapPropagator(),
	})
	env.RegisterWorkflow(parentPoolOperationLoggingTestWorkflow)
	env.RegisterWorkflow(nestedPoolOperationLoggingTestWorkflow)
	env.RegisterActivity(capturePoolOperationLogFields)

	env.ExecuteWorkflow(parentPoolOperationLoggingTestWorkflow)

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())

	var fields poolOperationLogFields
	require.NoError(t, env.GetWorkflowResult(&fields))
	require.NotEmpty(t, fields.CorrelationID)
	require.Equal(t, fields.CorrelationID, fields.TemporalID)
	require.NotEqual(t, fields.CorrelationID, fields.WorkflowID)
	require.Equal(t, "pool-uuid", fields.PoolUUID)
	require.Equal(t, poolOperationPasswordRotation, fields.Operation)
}
