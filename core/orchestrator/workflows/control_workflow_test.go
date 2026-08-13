package workflows

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	workflowEngineMock "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func WorkflowTest() {}

func enableSequenceResilience(t *testing.T) {
	t.Helper()
	original := sequenceResilienceEnabled
	sequenceResilienceEnabled = true
	t.Cleanup(func() { sequenceResilienceEnabled = original })
}

func newWorkflowTestEnv() *testsuite.TestWorkflowEnvironment {
	var ts testsuite.WorkflowTestSuite
	return ts.NewTestWorkflowEnvironment()
}

func assertWorkflowSuccess(t *testing.T, env *testsuite.TestWorkflowEnvironment) {
	t.Helper()
	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func signalChild(env *testsuite.TestWorkflowEnvironment, delay time.Duration, name, arg string) {
	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(Signal, SignalWorkflowParams{
			Function: name,
			Args:     []interface{}{arg},
		})
	}, delay)
}

// =============================================================================
// Legacy SequenceWorkflow tests (feature flag OFF)
// =============================================================================

func TestExecuteWorkflowSequentially_Success(t *testing.T) {
	origFlag := sequenceResilienceEnabled
	sequenceResilienceEnabled = false
	defer func() { sequenceResilienceEnabled = origFlag }()

	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	ctx := context.Background()
	timeout, pErr := time.ParseDuration(SequenceWorkflowTimeout)
	if pErr != nil {
		timeout = workflowengine.GetWorkflowGlobalTimeout()
	}

	env.RegisterActivity(&activities.CommonActivities{})
	env.RegisterActivity(&activities.PoolActivity{})

	temporal.EXPECT().SignalWithStartWorkflow(
		ctx,
		"test-sequence-workflow-id",
		Signal,
		SignalWorkflowParams{
			Function: "WorkflowTest",
			Options: workflow.ChildWorkflowOptions{
				TaskQueue:             workflowengine.CustomerTaskQueue,
				WorkflowID:            "test-workflow-id",
				WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
				WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
			},
		},
		client.StartWorkflowOptions{
			ID:                 "test-sequence-workflow-id",
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowRunTimeout: timeout,
		},
		mock.Anything,
	).Return(nil, nil)

	err := ExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			ID: "test-sequence-workflow-id",
		},
		WorkflowTest,
		workflow.ChildWorkflowOptions{
			WorkflowID:            "test-workflow-id",
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
	)

	assert.NoError(t, err)

	// Assert workflow execution
	assert.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestExecuteWorkflowSequentially_SignalError(t *testing.T) {
	origFlag := sequenceResilienceEnabled
	sequenceResilienceEnabled = false
	defer func() { sequenceResilienceEnabled = origFlag }()

	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	ctx := context.Background()
	timeout, pErr := time.ParseDuration(SequenceWorkflowTimeout)
	if pErr != nil {
		timeout = workflowengine.GetWorkflowGlobalTimeout()
	}

	temporal.EXPECT().SignalWithStartWorkflow(
		ctx,
		"test-sequence-workflow-id",
		Signal,
		SignalWorkflowParams{
			Function: "WorkflowTest",
			Options: workflow.ChildWorkflowOptions{
				TaskQueue:             workflowengine.CustomerTaskQueue,
				WorkflowID:            "test-workflow-id",
				WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
				WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
			},
		},
		client.StartWorkflowOptions{
			ID:                 "test-sequence-workflow-id",
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowRunTimeout: timeout,
		},
		mock.Anything,
	).Return(nil, errors.New("signal error"))

	err := ExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			ID:        "test-sequence-workflow-id",
			TaskQueue: workflowengine.CustomerTaskQueue,
		},
		WorkflowTest,
		workflow.ChildWorkflowOptions{
			WorkflowID:            "test-workflow-id",
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
	)

	assert.Error(t, err)

	// Assert workflow execution
	env.AssertExpectations(t)
}

func TestExecuteWorkflowSequentially_ValidationError_sequenceWorkflowIDNil(t *testing.T) {
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	ctx := context.Background()

	err := ExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			ID:        "",
			TaskQueue: workflowengine.CustomerTaskQueue,
		},
		WorkflowTest,
		workflow.ChildWorkflowOptions{
			WorkflowID:            "test-workflow-id",
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
	)

	assert.Error(t, err)
	assert.Equal(t,
		"Invalid parameters for sequence workflow execution, error: sequence workflow ID cannot be empty",
		err.Error(),
	)

	// Assert workflow execution
	env.AssertExpectations(t)
}

func TestExecuteWorkflowSequentially_ValidationError_ExecutionWorkflowIDNil(t *testing.T) {
	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	ctx := context.Background()

	err := ExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			ID:        "test-sequence-workflow-id",
			TaskQueue: workflowengine.CustomerTaskQueue,
		},
		WorkflowTest,
		workflow.ChildWorkflowOptions{
			WorkflowID:            "",
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
	)

	assert.Error(t, err)
	assert.Equal(t,
		"Invalid parameters for sequence workflow execution, error: execution workflow ID cannot be empty",
		err.Error(),
	)

	// Assert workflow execution
	env.AssertExpectations(t)
}

func TestSequenceWorkflow_Legacy_ExecutesChildWorkflowOnSignal(t *testing.T) {
	origFlag := sequenceResilienceEnabled
	sequenceResilienceEnabled = false
	defer func() { sequenceResilienceEnabled = origFlag }()

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(Signal, SignalWorkflowParams{
			Function: "DummyChildWorkflow",
			Args:     []interface{}{"arg1"},
			Options:  workflow.ChildWorkflowOptions{},
		})
	}, time.Second)

	env.RegisterWorkflowWithOptions(func(ctx workflow.Context, arg string) error {
		return nil
	}, workflow.RegisterOptions{Name: "DummyChildWorkflow"})

	env.ExecuteWorkflow(SequenceWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func TestSequenceWorkflow_Legacy_ChildWorkflowError(t *testing.T) {
	origFlag := sequenceResilienceEnabled
	sequenceResilienceEnabled = false
	defer func() { sequenceResilienceEnabled = origFlag }()

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.RegisterDelayedCallback(func() {
		env.SignalWorkflow(Signal, SignalWorkflowParams{
			Function: "ErrorChildWorkflow",
			Args:     []interface{}{"arg1"},
			Options:  workflow.ChildWorkflowOptions{},
		})
	}, time.Second)

	env.RegisterWorkflowWithOptions(func(ctx workflow.Context, arg string) error {
		return errors.New("child error")
	}, workflow.RegisterOptions{Name: "ErrorChildWorkflow"})

	env.ExecuteWorkflow(SequenceWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func TestSequenceWorkflow_Legacy_ExitsOnTimeout(t *testing.T) {
	origFlag := sequenceResilienceEnabled
	sequenceResilienceEnabled = false
	defer func() { sequenceResilienceEnabled = origFlag }()

	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()

	env.ExecuteWorkflow(SequenceWorkflow)

	assert.True(t, env.IsWorkflowCompleted())
	assert.NoError(t, env.GetWorkflowError())
}

func TestExecuteWorkflowSequentially_InvalidTimeoutFallsBackToGlobalTimeout(t *testing.T) {
	origFlag := sequenceResilienceEnabled
	sequenceResilienceEnabled = false
	defer func() { sequenceResilienceEnabled = origFlag }()

	originalTimeout := SequenceWorkflowTimeout
	defer func() { SequenceWorkflowTimeout = originalTimeout }()

	SequenceWorkflowTimeout = "invalid-duration"

	temporal := workflowEngineMock.NewMockTemporalTestClient(t)
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestWorkflowEnvironment()
	ctx := context.Background()

	expectedTimeout := workflowengine.GetWorkflowGlobalTimeout()

	temporal.EXPECT().SignalWithStartWorkflow(
		ctx,
		"test-sequence-workflow-id",
		Signal,
		SignalWorkflowParams{
			Function: "WorkflowTest",
			Options: workflow.ChildWorkflowOptions{
				TaskQueue:             workflowengine.CustomerTaskQueue,
				WorkflowID:            "test-workflow-id",
				WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
				WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
			},
		},
		client.StartWorkflowOptions{
			ID:                 "test-sequence-workflow-id",
			TaskQueue:          workflowengine.CustomerTaskQueue,
			WorkflowRunTimeout: expectedTimeout,
		},
		mock.Anything,
	).Return(nil, nil)

	err := ExecuteWorkflowSequentially(
		temporal,
		ctx,
		client.StartWorkflowOptions{
			ID: "test-sequence-workflow-id",
		},
		WorkflowTest,
		workflow.ChildWorkflowOptions{
			WorkflowID:            "test-workflow-id",
			WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			WorkflowRunTimeout:    workflowengine.GetWorkflowGlobalTimeout(),
		},
	)

	assert.NoError(t, err)
	env.AssertExpectations(t)
}

func registerNoopChild(env *testsuite.TestWorkflowEnvironment, name string) {
	env.RegisterWorkflowWithOptions(func(ctx workflow.Context, arg string) error { return nil }, workflow.RegisterOptions{Name: name})
}

func TestSequenceWorkflow_Resilient(t *testing.T) {
	enableSequenceResilience(t)

	t.Run("executes child on signal", func(t *testing.T) {
		env := newWorkflowTestEnv()
		registerNoopChild(env, "DummyChild")
		signalChild(env, time.Second, "DummyChild", "arg1")
		env.ExecuteWorkflow(SequenceWorkflowV2, SequenceWorkflowInput{})
		assertWorkflowSuccess(t, env)
	})

	t.Run("exits on idle timeout", func(t *testing.T) {
		env := newWorkflowTestEnv()
		env.ExecuteWorkflow(SequenceWorkflowV2, SequenceWorkflowInput{})
		assertWorkflowSuccess(t, env)
	})

	t.Run("child failure does not block queue", func(t *testing.T) {
		env := newWorkflowTestEnv()
		env.RegisterWorkflowWithOptions(func(ctx workflow.Context, arg string) error {
			return errors.New("simulated child failure")
		}, workflow.RegisterOptions{Name: "FailingChild"})
		registerNoopChild(env, "SuccessChild")
		signalChild(env, 500*time.Millisecond, "FailingChild", "arg1")
		signalChild(env, time.Second, "SuccessChild", "arg2")
		env.ExecuteWorkflow(SequenceWorkflowV2, SequenceWorkflowInput{})
		assertWorkflowSuccess(t, env)
	})

	t.Run("continue as new when suggested", func(t *testing.T) {
		env := newWorkflowTestEnv()
		registerNoopChild(env, "QuickChild")
		env.RegisterDelayedCallback(func() {
			for _, a := range []string{"1", "2", "3"} {
				env.SignalWorkflow(Signal, SignalWorkflowParams{Function: "QuickChild", Args: []interface{}{a}})
			}
			env.SetContinueAsNewSuggested(true)
		}, 500*time.Millisecond)
		env.ExecuteWorkflow(SequenceWorkflowV2, SequenceWorkflowInput{})
		require.Error(t, env.GetWorkflowError())
		assert.Contains(t, env.GetWorkflowError().Error(), "continue as new")
	})

	t.Run("restored queue processes before suggested CAN", func(t *testing.T) {
		env := newWorkflowTestEnv()
		var executed int
		env.RegisterWorkflowWithOptions(func(ctx workflow.Context, arg string) error {
			executed++
			return nil
		}, workflow.RegisterOptions{Name: "RestoredChild"})
		env.SetContinueAsNewSuggested(true)
		env.ExecuteWorkflow(SequenceWorkflowV2, SequenceWorkflowInput{
			State: &SequenceWorkflowState{
				PendingQueue: []SignalWorkflowParams{{Function: "RestoredChild", Args: []interface{}{"restored-arg"}}},
				RunStartedAt: time.Now(),
			},
		})
		assert.Equal(t, 1, executed)
		require.Error(t, env.GetWorkflowError())
	})

	t.Run("multiple signals drained", func(t *testing.T) {
		env := newWorkflowTestEnv()
		var order []string
		env.RegisterWorkflowWithOptions(func(ctx workflow.Context, arg string) error {
			order = append(order, arg)
			return nil
		}, workflow.RegisterOptions{Name: "OrderedChild"})
		env.RegisterDelayedCallback(func() {
			for i := 0; i < 5; i++ {
				env.SignalWorkflow(Signal, SignalWorkflowParams{Function: "OrderedChild", Args: []interface{}{fmt.Sprintf("signal-%d", i)}})
			}
		}, 500*time.Millisecond)
		env.ExecuteWorkflow(SequenceWorkflowV2, SequenceWorkflowInput{})
		assertWorkflowSuccess(t, env)
		assert.Len(t, order, 5)
	})
}

func TestSequenceWorkflow_FeatureFlagOff_UsesLegacy(t *testing.T) {
	orig := sequenceResilienceEnabled
	sequenceResilienceEnabled = false
	t.Cleanup(func() { sequenceResilienceEnabled = orig })

	env := newWorkflowTestEnv()
	registerNoopChild(env, "LegacyChild")
	signalChild(env, time.Second, "LegacyChild", "legacy")
	env.ExecuteWorkflow(SequenceWorkflow)
	assertWorkflowSuccess(t, env)
}

