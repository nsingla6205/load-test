package temporalmanager

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

// SampleWorkflow is a simple workflow that appends a suffix to the input string.
func SampleWorkflow(ctx workflow.Context, input string) (string, error) {
	// Set workflow options (e.g., timeout)
	options := workflow.ActivityOptions{
		StartToCloseTimeout: time.Minute,
	}
	ctx = workflow.WithActivityOptions(ctx, options)

	// Perform a simple operation
	result := input + "_processed"

	// Simulate some delay
	err := workflow.Sleep(ctx, 2*time.Second)
	if err != nil {
		return "", err
	}

	return result, nil
}

// SampleActivity is a simple activity that appends a suffix to the input string.
func SampleActivity(input string) (string, error) {
	// Perform a simple operation
	result := input + "_processed"
	return result, nil
}

func newLazyTestClient(t *testing.T) client.Client {
	t.Helper()
	c, err := client.NewLazyClient(client.Options{
		HostPort: "localhost:0000", // non-existent port — lazy client; no connection is made
	})
	if err != nil {
		t.Fatalf("failed to create lazy test client: %v", err)
	}
	return c
}

func TestNewWorker(t *testing.T) {
	t.Run("creates worker successfully with default options", func(t *testing.T) {
		mockClient := newLazyTestClient(t)
		defer mockClient.Close()

		w := NewWorker(mockClient, "test-task-queue", worker.Options{})

		assert.NotNil(t, w)
		assert.Equal(t, mockClient, w.GetClient())
		assert.NotNil(t, w.GetWorker())

		w.RegisterWorkflow(SampleWorkflow)
		w.RegisterActivity(SampleActivity)

		err := w.Run()
		assert.NotNil(t, err) // expected: fails to connect to non-existent Temporal server

		w.Stop()
	})

	t.Run("wires all concurrency options and panic policy into worker", func(t *testing.T) {
		mockClient := newLazyTestClient(t)
		defer mockClient.Close()

		want := worker.Options{
			MaxConcurrentActivityExecutionSize:      42,
			MaxConcurrentWorkflowTaskExecutionSize:  17,
			MaxConcurrentLocalActivityExecutionSize: 8,
			WorkerActivitiesPerSecond:               3.5,
			TaskQueueActivitiesPerSecond:            1.0,
			WorkflowPanicPolicy:                     worker.FailWorkflow,
		}

		var captured worker.Options
		orig := newWorkerFn
		newWorkerFn = func(c client.Client, taskQueue string, opts worker.Options) worker.Worker {
			captured = opts
			return orig(c, taskQueue, opts)
		}
		defer func() { newWorkerFn = orig }()

		w := NewWorker(mockClient, "test-task-queue", want)
		assert.NotNil(t, w)

		assert.Equal(t, want.MaxConcurrentActivityExecutionSize, captured.MaxConcurrentActivityExecutionSize)
		assert.Equal(t, want.MaxConcurrentWorkflowTaskExecutionSize, captured.MaxConcurrentWorkflowTaskExecutionSize)
		assert.Equal(t, want.MaxConcurrentLocalActivityExecutionSize, captured.MaxConcurrentLocalActivityExecutionSize)
		assert.Equal(t, want.WorkerActivitiesPerSecond, captured.WorkerActivitiesPerSecond)
		assert.Equal(t, want.TaskQueueActivitiesPerSecond, captured.TaskQueueActivitiesPerSecond)
		assert.Equal(t, worker.FailWorkflow, captured.WorkflowPanicPolicy)
	})
}
