package backgroundactivities

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/common"
	workflowEngine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"
)

func TestExecutePoolCAReconciliationSequentially_UsesTransitionScopedIDAndExplicitCA(t *testing.T) {
	const (
		poolUUID             = "pool-uuid"
		desiredCAURI         = "project/pool/ca"
		transitionWorkflowID = "reconcile-vsa-ca-on-change-3-abcdef123456"
	)
	timeout := 10 * time.Minute
	var capturedSequenceOptions client.StartWorkflowOptions
	var capturedChildOptions workflow.ChildWorkflowOptions
	var capturedArgs []interface{}

	err := executePoolCAReconciliationSequentially(
		context.Background(),
		poolUUID,
		desiredCAURI,
		transitionWorkflowID,
		timeout,
		func() (client.Client, error) { return nil, nil },
		func(
			_ client.Client,
			_ context.Context,
			sequenceOptions client.StartWorkflowOptions,
			workflowFunction interface{},
			childOptions workflow.ChildWorkflowOptions,
			workflowArgs ...interface{},
		) error {
			capturedSequenceOptions = sequenceOptions
			capturedChildOptions = childOptions
			capturedArgs = workflowArgs
			assert.Equal(t, common.ReconcilePoolCAWorkflowName, workflowFunction)
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, fmt.Sprintf(common.PoolOperationsSeq, poolUUID), capturedSequenceOptions.ID)
	assert.Equal(t, workflowengine.CustomerTaskQueue, capturedSequenceOptions.TaskQueue)
	assert.Equal(t, "PoolCAReconciliation_"+poolUUID+"_"+transitionWorkflowID, capturedChildOptions.WorkflowID)
	assert.Equal(t, timeout, capturedChildOptions.WorkflowRunTimeout)
	assert.Equal(t, []interface{}{poolUUID, desiredCAURI}, capturedArgs)
}

func TestControlWorkflowQueueHelpersReturnDependencyErrors(t *testing.T) {
	fetchError := func() (client.Client, error) {
		return nil, assert.AnError
	}
	queueError := func(
		client.Client,
		context.Context,
		client.StartWorkflowOptions,
		interface{},
		workflow.ChildWorkflowOptions,
		...interface{},
	) error {
		return assert.AnError
	}
	fetchSuccess := func() (client.Client, error) {
		return nil, nil
	}

	tests := []struct {
		name string
		run  func(func() (client.Client, error), ExecuteWorkflowSequentiallyFunc) error
	}{
		{
			name: "forced rotation",
			run: func(fetch func() (client.Client, error), queue ExecuteWorkflowSequentiallyFunc) error {
				return executePoolForcedCertificateRotationSequentially(
					context.Background(),
					"pool-1",
					time.Minute,
					fetch,
					queue,
				)
			},
		},
		{
			name: "CA reconciliation",
			run: func(fetch func() (client.Client, error), queue ExecuteWorkflowSequentiallyFunc) error {
				return executePoolCAReconciliationSequentially(
					context.Background(),
					"pool-1",
					"project/pool/ca",
					"transition-1",
					time.Minute,
					fetch,
					queue,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name+" fetch failure", func(t *testing.T) {
			require.ErrorContains(t, test.run(fetchError, nil), "get Temporal client")
		})
		t.Run(test.name+" queue failure", func(t *testing.T) {
			require.ErrorIs(t, test.run(fetchSuccess, queueError), assert.AnError)
		})
	}
}

func TestControlWorkflowActivity_CertificateMaintenanceQueueWrappers(t *testing.T) {
	originalFetchTemporalClient := workflowengine.FetchTemporalClient
	originalExecuteWorkflowSequentially := ExecuteWorkflowSequentially
	t.Cleanup(func() {
		workflowengine.FetchTemporalClient = originalFetchTemporalClient
		ExecuteWorkflowSequentially = originalExecuteWorkflowSequentially
	})

	type queuedWorkflow struct {
		workflowName interface{}
		workflowID   string
		args         []interface{}
	}
	var queued []queuedWorkflow
	workflowengine.FetchTemporalClient = func() (client.Client, error) {
		return nil, nil
	}
	ExecuteWorkflowSequentially = func(
		_ client.Client,
		_ context.Context,
		_ client.StartWorkflowOptions,
		workflowName interface{},
		childOptions workflow.ChildWorkflowOptions,
		args ...interface{},
	) error {
		queued = append(queued, queuedWorkflow{
			workflowName: workflowName,
			workflowID:   childOptions.WorkflowID,
			args:         args,
		})
		return nil
	}

	activity := &ControlWorkflowActivity{}
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(activity.ExecutePoolForcedCertificateRotationSequentially)
	env.RegisterActivity(activity.ExecuteTrackedPoolForcedCertificateRotationSequentially)
	env.RegisterActivity(activity.ExecutePoolCAReconciliationSequentially)
	env.RegisterActivity(activity.ExecuteTrackedPoolCAReconciliationSequentially)
	_, err := env.ExecuteActivity(
		activity.ExecutePoolForcedCertificateRotationSequentially,
		"pool-1",
		time.Minute,
	)
	require.NoError(t, err)
	_, err = env.ExecuteActivity(
		activity.ExecuteTrackedPoolForcedCertificateRotationSequentially,
		"pool-2",
		"transition-2",
		"job-2",
		time.Minute,
	)
	require.NoError(t, err)
	_, err = env.ExecuteActivity(
		activity.ExecutePoolCAReconciliationSequentially,
		"pool-3",
		"project/pool/ca",
		"transition-3",
		time.Minute,
	)
	require.NoError(t, err)
	_, err = env.ExecuteActivity(
		activity.ExecuteTrackedPoolCAReconciliationSequentially,
		"pool-4",
		"project/pool/ca",
		"transition-4",
		"job-4",
		time.Minute,
	)
	require.NoError(t, err)

	require.Len(t, queued, 4)
	assert.Equal(t, common.ForceRotatePoolCertificateWorkflowName, queued[0].workflowName)
	assert.Contains(t, queued[0].workflowID, "PoolForcedCertRotation_pool-1_")
	assert.Equal(t, []interface{}{"pool-1"}, queued[0].args)
	assert.Equal(t, common.TrackedForceRotatePoolCertificateWorkflowName, queued[1].workflowName)
	assert.Equal(t, "PoolForcedCertRotation_pool-2_transition-2", queued[1].workflowID)
	assert.Equal(t, []interface{}{"pool-2", "job-2"}, queued[1].args)
	assert.Equal(t, common.ReconcilePoolCAWorkflowName, queued[2].workflowName)
	assert.Equal(t, "PoolCAReconciliation_pool-3_transition-3", queued[2].workflowID)
	assert.Equal(t, []interface{}{"pool-3", "project/pool/ca"}, queued[2].args)
	assert.Equal(t, common.TrackedReconcilePoolCAWorkflowName, queued[3].workflowName)
	assert.Equal(t, "PoolCAReconciliation_pool-4_transition-4", queued[3].workflowID)
	assert.Equal(t, []interface{}{"pool-4", "project/pool/ca", "job-4"}, queued[3].args)
}

func TestControlWorkflowActivity_ExecutePoolCertificateRotationSequentially(t *testing.T) {
	// Save original function
	origFetchTemporalClient := workflowengine.FetchTemporalClient
	defer func() { workflowengine.FetchTemporalClient = origFetchTemporalClient }()

	// Save original ExecuteWorkflowSequentially
	origExecuteWorkflowSequentially := ExecuteWorkflowSequentially
	defer func() { ExecuteWorkflowSequentially = origExecuteWorkflowSequentially }()

	t.Run("Success", func(t *testing.T) {
		poolUUID := "test-pool-uuid"
		workflowTimeout := 5 * time.Minute

		// Mock temporal client
		mockClient := workflowEngine.NewMockTemporalTestClient(t)
		workflowengine.FetchTemporalClient = func() (client.Client, error) {
			return mockClient, nil
		}

		// Mock ExecuteWorkflowSequentially
		executeCalled := false
		ExecuteWorkflowSequentially = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			executeCalled = true
			// Verify control workflow ID format
			expectedControlWfID := "Pool_test-pool-uuid_Ops_All"
			assert.Equal(t, expectedControlWfID, sequenceWfOptions.ID)
			assert.Equal(t, workflowengine.CustomerTaskQueue, sequenceWfOptions.TaskQueue)

			// Verify child workflow options
			assert.Equal(t, workflowengine.CustomerTaskQueue, wfOptions.TaskQueue)
			assert.Equal(t, workflowTimeout, wfOptions.WorkflowRunTimeout)

			// Verify workflow args
			assert.Len(t, wfArgs, 1)
			assert.Equal(t, poolUUID, wfArgs[0])

			return nil
		}

		activity := &ControlWorkflowActivity{}
		err := activity.ExecutePoolCertificateRotationSequentially(context.Background(), poolUUID, workflowTimeout)

		assert.NoError(t, err)
		assert.True(t, executeCalled, "ExecuteWorkflowSequentially should have been called")
	})

	t.Run("TemporalClientError", func(t *testing.T) {
		poolUUID := "test-pool-uuid"
		workflowTimeout := 5 * time.Minute

		// Mock temporal client to return error
		workflowengine.FetchTemporalClient = func() (client.Client, error) {
			return nil, assert.AnError
		}

		activity := &ControlWorkflowActivity{}
		err := activity.ExecutePoolCertificateRotationSequentially(context.Background(), poolUUID, workflowTimeout)

		assert.Error(t, err)
	})
}

func TestControlWorkflowActivity_ExecutePoolPasswordRotationSequentially(t *testing.T) {
	// Save original function
	origFetchTemporalClient := workflowengine.FetchTemporalClient
	defer func() { workflowengine.FetchTemporalClient = origFetchTemporalClient }()

	// Save original ExecuteWorkflowSequentially
	origExecuteWorkflowSequentially := ExecuteWorkflowSequentially
	defer func() { ExecuteWorkflowSequentially = origExecuteWorkflowSequentially }()

	t.Run("Success", func(t *testing.T) {
		poolUUID := "test-pool-uuid"
		workflowTimeout := 5 * time.Minute

		// Mock temporal client
		mockClient := workflowEngine.NewMockTemporalTestClient(t)
		workflowengine.FetchTemporalClient = func() (client.Client, error) {
			return mockClient, nil
		}

		// Mock ExecuteWorkflowSequentially
		executeCalled := false
		ExecuteWorkflowSequentially = func(temporal client.Client, ctx context.Context, sequenceWfOptions client.StartWorkflowOptions, wfFunction interface{}, wfOptions workflow.ChildWorkflowOptions, wfArgs ...interface{}) error {
			executeCalled = true
			// Verify control workflow ID format
			expectedControlWfID := "Pool_test-pool-uuid_Ops_All"
			assert.Equal(t, expectedControlWfID, sequenceWfOptions.ID)
			assert.Equal(t, workflowengine.CustomerTaskQueue, sequenceWfOptions.TaskQueue)

			// Verify child workflow options
			assert.Equal(t, workflowengine.CustomerTaskQueue, wfOptions.TaskQueue)
			assert.Equal(t, workflowTimeout, wfOptions.WorkflowRunTimeout)

			// Verify workflow args
			assert.Len(t, wfArgs, 1)
			assert.Equal(t, poolUUID, wfArgs[0])

			return nil
		}

		activity := &ControlWorkflowActivity{}
		err := activity.ExecutePoolPasswordRotationSequentially(context.Background(), poolUUID, workflowTimeout)

		assert.NoError(t, err)
		assert.True(t, executeCalled, "ExecuteWorkflowSequentially should have been called")
	})

	t.Run("TemporalClientError", func(t *testing.T) {
		poolUUID := "test-pool-uuid"
		workflowTimeout := 5 * time.Minute

		// Mock temporal client to return error
		workflowengine.FetchTemporalClient = func() (client.Client, error) {
			return nil, assert.AnError
		}

		activity := &ControlWorkflowActivity{}
		err := activity.ExecutePoolPasswordRotationSequentially(context.Background(), poolUUID, workflowTimeout)

		assert.Error(t, err)
	})
}
