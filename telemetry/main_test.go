package main

import (
	"context"
	"errors"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	metricscommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	telementrymonitoring "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/monitoring"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func testWorkerContexts(t *testing.T) (context.Context, context.Context, context.CancelFunc) {
	t.Helper()
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	jobCtx, cancelJob := context.WithCancel(context.Background())
	t.Cleanup(cancelPoll)
	t.Cleanup(cancelJob)
	return pollCtx, jobCtx, cancelPoll
}

func TestRunWorkerOnce_ReturnsFalseOnNormalExit(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)
	logger := log.NewLogger()
	panicked := runWorkerOnce(pollCtx, jobCtx, logger, "test-worker", func(context.Context, context.Context) error {
		return nil
	})
	assert.False(t, panicked)
}

func TestRunWorkerOnce_ReturnsTrueOnPanic(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)
	logger := log.NewLogger()
	panicked := runWorkerOnce(pollCtx, jobCtx, logger, "test-worker", func(context.Context, context.Context) error {
		panic("escaped worker panic")
	})
	assert.True(t, panicked)
}

func TestRunWorkerOnce_ReturnsFalseOnError(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)
	logger := log.NewLogger()
	panicked := runWorkerOnce(pollCtx, jobCtx, logger, "test-worker", func(context.Context, context.Context) error {
		return errors.New("worker exited")
	})
	assert.False(t, panicked)
}

func TestGoJobWorker_ExitsAfterNormalReturn(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)

	logger := log.NewLogger()
	var wg sync.WaitGroup
	var calls atomic.Int32
	done := make(chan struct{})

	goJobWorker(pollCtx, jobCtx, &wg, logger, "test-worker", func(context.Context, context.Context) error {
		if calls.Add(1) == 1 {
			close(done)
		}
		return nil
	})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker function was not invoked")
	}

	wg.Wait()
	assert.Equal(t, int32(1), calls.Load())
}

func TestGoJobWorker_StopsOnContextCancel(t *testing.T) {
	pollCtx, jobCtx, cancelPoll := testWorkerContexts(t)
	logger := log.NewLogger()

	var wg sync.WaitGroup
	var calls atomic.Int32
	started := make(chan struct{})

	goJobWorker(pollCtx, jobCtx, &wg, logger, "test-worker", func(workerPoll, _ context.Context) error {
		calls.Add(1)
		close(started)
		<-workerPoll.Done()
		return workerPoll.Err()
	})

	<-started
	cancelPoll()
	wg.Wait()

	assert.Equal(t, int32(1), calls.Load())
}

func TestGoJobWorker_DoesNotRestartOnCanceledError(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)
	logger := log.NewLogger()

	var wg sync.WaitGroup
	var calls atomic.Int32
	done := make(chan struct{})

	goJobWorker(pollCtx, jobCtx, &wg, logger, "test-worker", func(context.Context, context.Context) error {
		if calls.Add(1) == 1 {
			close(done)
		}
		return context.Canceled
	})

	<-done
	wg.Wait()
	assert.Equal(t, int32(1), calls.Load())
}

func TestGoJobWorker_RestartsAfterEscapedPanic(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)
	logger := log.NewLogger()

	var wg sync.WaitGroup
	var calls atomic.Int32
	secondCall := make(chan struct{})

	goJobWorker(pollCtx, jobCtx, &wg, logger, "test-worker", func(context.Context, context.Context) error {
		if calls.Add(1) == 1 {
			panic("escaped worker panic")
		}
		close(secondCall)
		return nil
	})

	select {
	case <-secondCall:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not restart after panic")
	}

	wg.Wait()
	assert.Equal(t, int32(2), calls.Load())
}

func TestGoJobWorker_DoesNotRestartAfterNonCanceledError(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)
	logger := log.NewLogger()

	var wg sync.WaitGroup
	var calls atomic.Int32
	done := make(chan struct{})

	goJobWorker(pollCtx, jobCtx, &wg, logger, "test-worker", func(context.Context, context.Context) error {
		if calls.Add(1) == 1 {
			close(done)
		}
		return errors.New("worker exited")
	})

	<-done
	wg.Wait()
	assert.Equal(t, int32(1), calls.Load())
}

func TestGoJobWorker_DoesNotRestartAfterPollCancel(t *testing.T) {
	pollCtx, jobCtx, cancelPoll := testWorkerContexts(t)
	logger := log.NewLogger()

	var wg sync.WaitGroup
	var calls atomic.Int32
	started := make(chan struct{})

	goJobWorker(pollCtx, jobCtx, &wg, logger, "test-worker", func(context.Context, context.Context) error {
		calls.Add(1)
		close(started)
		panic("escaped worker panic")
	})

	<-started
	cancelPoll()
	wg.Wait()

	require.Equal(t, int32(1), calls.Load())
}

func TestHTTPServerNilReturnCancelsPollCtx(t *testing.T) {
	signalCtx, stopSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()

	eg, pollCtx := errgroup.WithContext(signalCtx)
	eg.Go(func() error {
		defer stopSignal()
		return nil
	})

	select {
	case <-pollCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("pollCtx was not cancelled after HTTP nil exit")
	}
	require.NoError(t, eg.Wait())
}

func TestHTTPServerFailureReturnsNonZeroExitCode(t *testing.T) {
	signalCtx, stopSignal := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignal()

	eg, _ := errgroup.WithContext(signalCtx)
	eg.Go(func() error {
		defer stopSignal()
		return errors.New("listen failed")
	})

	err := eg.Wait()
	require.Error(t, err)
}

func TestStartJobWorkers_RegistersAllWorkerTypes(t *testing.T) {
	pollCtx, jobCtx, _ := testWorkerContexts(t)
	logger := log.NewLogger()
	var wg sync.WaitGroup

	var started []string
	recordStarter := func(_ context.Context, _ context.Context, _ *sync.WaitGroup, _ log.Logger, name string, _ func(context.Context, context.Context) error) {
		started = append(started, name)
	}

	cfg := &metricscommon.TelemetryConfig{
		NumWorkersPerformance:  1,
		NumWorkersUsage:        1,
		NumWorkersCollection:   1,
		NumWorkersBizOps:       1,
		NumWorkersBillingRetry: 1,
	}
	queue := utils.NewQueueWithConfig(nil, nil, &telementrymonitoring.MockMetricsRecorder{}, utils.JobQueueConfig{SweeperEnabled: false})

	total := startJobWorkers(pollCtx, jobCtx, &wg, logger, queue, cfg, recordStarter)
	assert.Equal(t, 5, total)
	assert.ElementsMatch(t, []string{
		"performance-0", "usage-0", "collection-0", "bizops-0", "billing-retry-0",
	}, started)
}

func TestStartJobWorkers_StartsSweeperWhenEnabled(t *testing.T) {
	pollCtx, cancelPoll := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelPoll()
	jobCtx := context.Background()
	logger := log.NewLogger()
	var wg sync.WaitGroup

	queue := utils.NewQueueWithConfig(nil, nil, &telementrymonitoring.MockMetricsRecorder{}, utils.JobQueueConfig{
		SweeperEnabled:  true,
		SweeperInterval: 0,
	})

	total := startJobWorkers(pollCtx, jobCtx, &wg, logger, queue, &metricscommon.TelemetryConfig{}, goJobWorker)
	assert.Equal(t, 1, total)

	wg.Wait()
}

func TestLogTelemetryWorkerStartup(t *testing.T) {
	logTelemetryWorkerStartup(log.NewLogger(), &metricscommon.TelemetryConfig{
		NumWorkersPerformance:  1,
		NumWorkersUsage:        2,
		NumWorkersCollection:   3,
		NumWorkersBizOps:       4,
		NumWorkersBillingRetry: 5,
	})
}

func TestLogShutdownDrainResult(t *testing.T) {
	logger := log.NewLogger()
	logShutdownDrainResult(logger, shutdownResult{graceful: true, elapsed: time.Second})
	logShutdownDrainResult(logger, shutdownResult{graceful: false, elapsed: time.Second})
}

func TestRunShutdownCoordinatorAsync(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	done := runShutdownCoordinatorAsync(shutdownCoordinatorConfig{
		logger:          log.NewLogger(),
		budget:          time.Second,
		httpTimeout:     50 * time.Millisecond,
		finalizeReserve: 100 * time.Millisecond,
		cancelJobs:      func() {},
		workerWG:        &wg,
		pollCtx:         pollCtx,
	})

	cancelPoll()
	result := <-done
	assert.True(t, result.graceful)
}
