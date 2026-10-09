package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/metricsinterface"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	tManagerPkg "github.com/vcp-vsa-control-Plane/vsa-control-plane/worker/temporalmanager"
	"go.temporal.io/sdk/client"
	workerSDK "go.temporal.io/sdk/worker"
)

type harvestMetricsRecorder struct {
	detected  metricsinterface.HarvestDriftSnapshotMetrics
	remaining metricsinterface.HarvestDriftSnapshotMetrics
	pending   int
	exhausted int
	last      time.Time
}

type harvestMetricsRestoreRecorder struct {
	*harvestMetricsRecorder
	restored chan struct{}
}

func (r *harvestMetricsRestoreRecorder) SetHarvestLastSuccessfulReconcile(runStart time.Time) {
	r.harvestMetricsRecorder.SetHarvestLastSuccessfulReconcile(runStart)
	select {
	case r.restored <- struct{}{}:
	default:
	}
}

func (r *harvestMetricsRecorder) SetHarvestDriftSnapshot(stage string, snapshot metricsinterface.HarvestDriftSnapshotMetrics) {
	if stage == metricsinterface.HarvestDriftStageDetected {
		r.detected = snapshot
		return
	}
	r.remaining = snapshot
}

func (r *harvestMetricsRecorder) SetHarvestLastSuccessfulReconcile(runStart time.Time) {
	r.last = runStart
}

func (r *harvestMetricsRecorder) SetHarvestRetryBacklog(pending, exhausted int) {
	r.pending = pending
	r.exhausted = exhausted
}

func TestBuildWorkerOptions(t *testing.T) {
	opts := buildWorkerOptions(42, 17, 8, 3.5, 1.0, defaultDeadlockDetectionTimeout, workerSDK.BlockWorkflow)

	assert.Equal(t, 42, opts.MaxConcurrentActivityExecutionSize)
	assert.Equal(t, 17, opts.MaxConcurrentWorkflowTaskExecutionSize)
	assert.Equal(t, 8, opts.MaxConcurrentLocalActivityExecutionSize)
	assert.Equal(t, 3.5, opts.WorkerActivitiesPerSecond)
	assert.Equal(t, 1.0, opts.TaskQueueActivitiesPerSecond)
	assert.Len(t, opts.Interceptors, 1)
	assert.NotNil(t, opts.Interceptors[0])
	assert.Equal(t, defaultDeadlockDetectionTimeout, opts.DeadlockDetectionTimeout)
	assert.Equal(t, workerSDK.BlockWorkflow, opts.WorkflowPanicPolicy)
}

func TestBuildWorkerOptions_ZeroDeadlockTimeoutUsesDefault(t *testing.T) {
	opts := buildWorkerOptions(1, 1, 1, 1, 1, 0, workerSDK.BlockWorkflow)
	assert.Equal(t, defaultDeadlockDetectionTimeout, opts.DeadlockDetectionTimeout)
}

func TestBuildWorkerOptions_FailWorkflowPanicPolicy(t *testing.T) {
	opts := buildWorkerOptions(1, 1, 1, 1, 1, defaultDeadlockDetectionTimeout, workerSDK.FailWorkflow)
	assert.Equal(t, workerSDK.FailWorkflow, opts.WorkflowPanicPolicy)
}

func TestParseWorkflowPanicPolicy(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    workerSDK.WorkflowPanicPolicy
		wantErr bool
	}{
		{name: "empty defaults to BlockWorkflow", input: "", want: workerSDK.BlockWorkflow},
		{name: "BlockWorkflow", input: "BlockWorkflow", want: workerSDK.BlockWorkflow},
		{name: "blockworkflow lowercase", input: "blockworkflow", want: workerSDK.BlockWorkflow},
		{name: "FailWorkflow", input: "FailWorkflow", want: workerSDK.FailWorkflow},
		{name: "failworkflow lowercase", input: " failworkflow ", want: workerSDK.FailWorkflow},
		{name: "invalid", input: "RetryWorkflow", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWorkflowPanicPolicy(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWorkflowPanicPolicyName(t *testing.T) {
	assert.Equal(t, workflowPanicPolicyBlock, workflowPanicPolicyName(workerSDK.BlockWorkflow))
	assert.Equal(t, workflowPanicPolicyFail, workflowPanicPolicyName(workerSDK.FailWorkflow))
}

// mustParseWorkflowPanicPolicy exits the process on invalid input, so only the
// accepted values are covered here.
func TestMustParseWorkflowPanicPolicy(t *testing.T) {
	logger := log.NewLogger()

	assert.Equal(t, workerSDK.BlockWorkflow, mustParseWorkflowPanicPolicy("", logger))
	assert.Equal(t, workerSDK.BlockWorkflow, mustParseWorkflowPanicPolicy(workflowPanicPolicyBlock, logger))
	assert.Equal(t, workerSDK.FailWorkflow, mustParseWorkflowPanicPolicy(workflowPanicPolicyFail, logger))
}

func TestNewWorkerWithOpts(t *testing.T) {
	mockClient, err := client.NewLazyClient(client.Options{HostPort: "localhost:0000"})
	assert.NoError(t, err)
	defer mockClient.Close()

	logger := log.NewLogger()

	w := newWorkerWithOpts(mockClient, "test-queue", 10, 5, 3, 2.5, 1.5, defaultDeadlockDetectionTimeout, workerSDK.BlockWorkflow, logger)

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

func TestRestoreHarvestMetrics(t *testing.T) {
	runStart := time.Date(2026, time.August, 19, 6, 0, 0, 0, time.UTC)
	state := metricsinterface.HarvestReconcileState{
		LastSuccessfulRunStartAt: runStart,
		DetectedSnapshot:         metricsinterface.HarvestDriftSnapshotMetrics{OrphanCount: 2},
		RemainingSnapshot:        metricsinterface.HarvestDriftSnapshotMetrics{DBWithoutPVCCount: 1},
		PendingRetries:           3,
		ExhaustedRetries:         1,
	}
	value, err := json.Marshal(state)
	require.NoError(t, err)

	storage := database.NewMockStorage(t)
	storage.On("GetAppConfig", mock.Anything, metricsinterface.HarvestReconcileStateAppConfigKey).
		Return(&datamodel.AppConfig{Value: string(value)}, nil)
	recorder := &harvestMetricsRecorder{}

	restoreHarvestMetrics(context.Background(), storage, recorder, log.NewLogger())

	assert.Equal(t, state.DetectedSnapshot, recorder.detected)
	assert.Equal(t, state.RemainingSnapshot, recorder.remaining)
	assert.Equal(t, state.PendingRetries, recorder.pending)
	assert.Equal(t, state.ExhaustedRetries, recorder.exhausted)
	assert.Equal(t, runStart, recorder.last)
}

func TestRestoreHarvestMetricsCheckpointErrors(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		debugLog  bool
		errorText string
	}{
		{
			name:     "not found checkpoints use debug logging",
			err:      vsaerrors.NewVCPError(vsaerrors.ErrResourceNotFound, nil),
			debugLog: true,
		},
		{
			name:      "unexpected checkpoint errors use warning logging",
			err:       errors.New("database unavailable"),
			errorText: "database unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := database.NewMockStorage(t)
			storage.On("GetAppConfig", mock.Anything, metricsinterface.HarvestReconcileStateAppConfigKey).
				Return(nil, tt.err)
			logger := &log.MockLogger{}
			if tt.debugLog {
				logger.On("Debug", "Harvest metrics checkpoint unavailable", "error", mock.Anything).Once()
			} else {
				logger.On("Warn", "Harvest metrics checkpoint unavailable", "error", tt.errorText).Once()
			}

			restoreHarvestMetrics(context.Background(), storage, &harvestMetricsRecorder{}, logger)

			if tt.debugLog {
				logger.AssertNotCalled(t, "Warn", mock.Anything, mock.Anything, mock.Anything)
			} else {
				logger.AssertNotCalled(t, "Debug", mock.Anything, mock.Anything, mock.Anything)
			}
			logger.AssertExpectations(t)
		})
	}
}

func TestRestoreHarvestMetricsPeriodically(t *testing.T) {
	runStart := time.Date(2026, time.August, 19, 6, 0, 0, 0, time.UTC)
	value, err := json.Marshal(metricsinterface.HarvestReconcileState{
		LastSuccessfulRunStartAt: runStart,
	})
	require.NoError(t, err)

	storage := database.NewMockStorage(t)
	storage.On("GetAppConfig", mock.Anything, metricsinterface.HarvestReconcileStateAppConfigKey).
		Return(&datamodel.AppConfig{Value: string(value)}, nil).Once()
	recorder := &harvestMetricsRestoreRecorder{
		harvestMetricsRecorder: &harvestMetricsRecorder{},
		restored:               make(chan struct{}, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		restoreHarvestMetricsPeriodically(ctx, storage, recorder, log.NewLogger(), 100*time.Millisecond, harvestMetricsRestoreTimeout)
		close(done)
	}()

	select {
	case <-recorder.restored:
	case <-time.After(time.Second):
		t.Fatal("periodic Harvest metrics restore did not run")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("periodic Harvest metrics restore did not stop")
	}

	assert.Equal(t, runStart, recorder.last)
	storage.AssertExpectations(t)
}

func TestRestoreHarvestMetrics_TimesOutHungGetAppConfig(t *testing.T) {
	storage := database.NewMockStorage(t)
	storage.EXPECT().GetAppConfig(mock.Anything, metricsinterface.HarvestReconcileStateAppConfigKey).
		RunAndReturn(func(ctx context.Context, key string) (*datamodel.AppConfig, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}).Once()
	logger := &log.MockLogger{}
	logger.On("Warn", "Harvest metrics checkpoint unavailable", "error", mock.Anything).Once()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	restoreHarvestMetrics(ctx, storage, &harvestMetricsRecorder{}, logger)

	storage.AssertExpectations(t)
	logger.AssertExpectations(t)
}
