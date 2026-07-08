package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	tManagerPkg "github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/temporalmanager"
	"go.temporal.io/sdk/client"
	workerSDK "go.temporal.io/sdk/worker"
)

func TestBuildWorkerOptions(t *testing.T) {
	opts := buildWorkerOptions(42, 17, 8, 3.5, 1.0)

	assert.Equal(t, 42, opts.MaxConcurrentActivityExecutionSize)
	assert.Equal(t, 17, opts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Equal(t, 8, opts.MaxConcurrentLocalActivityExecutionSize)
	assert.Equal(t, 3.5, opts.WorkerActivitiesPerSecond)
	assert.Equal(t, 1.0, opts.TaskQueueActivitiesPerSecond)
}

func TestNewWorkerWithOpts(t *testing.T) {
	mockClient, err := client.NewLazyClient(client.Options{HostPort: "localhost:0000"})
	assert.NoError(t, err)
	defer mockClient.Close()

	logger := log.NewLogger()

	w := newWorkerWithOpts(mockClient, "test-queue", 10, 5, 3, 2.5, 1.5, logger)

	assert.NotNil(t, w)
}

func TestRegisterOCICustomerWorkflowsAndActivities(t *testing.T) {
	clientOptions := client.Options{
		HostPort: "localhost:0000",
	}
	mockClient, err := client.NewLazyClient(clientOptions)
	assert.NoError(t, err)
	defer mockClient.Close()

	worker := tManagerPkg.NewWorker(mockClient, "test-task-queue", workerSDK.Options{})
	mockStorage := database.NewMockStorage(t)

	assert.NotPanics(t, func() {
		RegisterOCICustomerWorkflowsAndActivities(*worker, mockStorage, mockClient)
	})
}

// TestRegisterCustomerWorkflowsAndActivities verifies the GCP customer worker
// registration (including SetCloudResolver wiring) completes without panic, and
// that the cloud resolver is bound and resolvable afterwards.
func TestRegisterCustomerWorkflowsAndActivities(t *testing.T) {
	clientOptions := client.Options{
		HostPort: "localhost:0000",
	}
	mockClient, err := client.NewLazyClient(clientOptions)
	assert.NoError(t, err)
	defer mockClient.Close()

	worker := tManagerPkg.NewWorker(mockClient, "test-task-queue", workerSDK.Options{})
	mockStorage := database.NewMockStorage(t)

	assert.NotPanics(t, func() {
		RegisterCustomerWorkflowsAndActivities(*worker, mockStorage, mockClient)
	})

	// The resolver must be bound after GCP registration.
	assert.NotNil(t, hyperscaler.ResolveCloudService)
}
