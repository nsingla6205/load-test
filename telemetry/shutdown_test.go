package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metricscommon "github.com/vcp-vsa-control-Plane/vsa-control-plane/telemetry/common"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestLoadShutdownDurationsDefaultsViaConfig(t *testing.T) {
	t.Setenv("TELEMETRY_SHUTDOWN_BUDGET", "")
	t.Setenv("TELEMETRY_HTTP_SHUTDOWN_TIMEOUT", "")

	config := metricscommon.LoadConfig()
	assert.Equal(t, metricscommon.DefaultShutdownBudget, config.ShutdownBudget)
	assert.Equal(t, metricscommon.DefaultHTTPShutdownTimeout, config.HTTPShutdownTimeout)
}

func TestEffectiveFinalizeReserve(t *testing.T) {
	tests := []struct {
		name      string
		budget    time.Duration
		reserve   time.Duration
		effective time.Duration
		clamped   bool
	}{
		{name: "within budget", budget: 10 * time.Second, reserve: 3 * time.Second, effective: 3 * time.Second},
		{name: "reserve exceeds budget", budget: 100 * time.Millisecond, reserve: 500 * time.Millisecond, effective: 100 * time.Millisecond, clamped: true},
		{name: "negative reserve", budget: time.Second, reserve: -time.Second, effective: 0, clamped: true},
		{name: "zero reserve", budget: time.Second, reserve: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			effective, clamped := effectiveFinalizeReserve(tt.budget, tt.reserve)
			assert.Equal(t, tt.effective, effective)
			assert.Equal(t, tt.clamped, clamped)
		})
	}
}

func TestRunShutdownCoordinator_BudgetSmallerThanFinalizeReserve(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	cancelObserved := make(chan struct{})
	_, cancelJobs := context.WithCancel(context.Background())
	go func() {
		<-cancelObserved
	}()

	budget := 100 * time.Millisecond
	reserve := 500 * time.Millisecond

	done := make(chan shutdownResult, 1)
	go func() {
		done <- runShutdownCoordinator(shutdownCoordinatorConfig{
			logger:          log.NewLogger(),
			budget:          budget,
			httpTimeout:     10 * time.Millisecond,
			finalizeReserve: reserve,
			cancelJobs: func() {
				close(cancelObserved)
				cancelJobs()
			},
			workerWG: &wg,
			pollCtx:  pollCtx,
		})
	}()

	cancelPoll()
	result := <-done
	require.False(t, result.graceful)

	select {
	case <-cancelObserved:
	default:
		t.Fatal("cancelJobs was not invoked")
	}
}

func TestWaitUntilCompletes(t *testing.T) {
	done := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(done)
	}()
	assert.True(t, waitUntil(done, time.Second))
}

func TestWaitUntilTimesOut(t *testing.T) {
	done := make(chan struct{})
	assert.False(t, waitUntil(done, 20*time.Millisecond))
}

func TestRunShutdownCoordinatorGraceful(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	jobCtx, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-pollCtx.Done()
	}()

	srv := &http.Server{Addr: "127.0.0.1:0"}
	done := make(chan shutdownResult, 1)
	go func() {
		done <- runShutdownCoordinator(shutdownCoordinatorConfig{
			logger:          log.NewLogger(),
			httpServer:      srv,
			budget:          2 * time.Second,
			httpTimeout:     100 * time.Millisecond,
			finalizeReserve: 500 * time.Millisecond,
			cancelJobs:      cancelJobs,
			workerWG:        &wg,
			pollCtx:         pollCtx,
			workerCount:     1,
		})
	}()

	cancelPoll()
	result := <-done
	require.True(t, result.graceful)
	assert.NoError(t, jobCtx.Err())
}

func TestRunShutdownCoordinatorForced(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	jobCancelled := make(chan struct{})
	jobCtx, cancelJobs := context.WithCancel(context.Background())
	go func() {
		<-jobCtx.Done()
		close(jobCancelled)
	}()

	done := make(chan shutdownResult, 1)
	go func() {
		done <- runShutdownCoordinator(shutdownCoordinatorConfig{
			logger:          log.NewLogger(),
			budget:          300 * time.Millisecond,
			httpTimeout:     50 * time.Millisecond,
			finalizeReserve: 150 * time.Millisecond,
			cancelJobs:      cancelJobs,
			workerWG:        &wg,
			pollCtx:         pollCtx,
		})
	}()

	cancelPoll()
	result := <-done
	require.False(t, result.graceful)

	select {
	case <-jobCancelled:
	case <-time.After(time.Second):
		t.Fatal("cancelJobs was not invoked")
	}
}

func TestRunShutdownCoordinatorUsesSingleBudgetNotAdditive(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	jobCancelled := make(chan struct{})
	_, cancelJobs := context.WithCancel(context.Background())
	go func() {
		<-jobCancelled
	}()

	budget := 250 * time.Millisecond

	done := make(chan shutdownResult, 1)
	go func() {
		done <- runShutdownCoordinator(shutdownCoordinatorConfig{
			logger:          log.NewLogger(),
			budget:          budget,
			httpTimeout:     10 * time.Millisecond,
			finalizeReserve: 100 * time.Millisecond,
			cancelJobs: func() {
				cancelJobs()
				close(jobCancelled)
			},
			workerWG: &wg,
			pollCtx:  pollCtx,
		})
	}()

	cancelPoll()
	result := <-done
	require.False(t, result.graceful)

	select {
	case <-jobCancelled:
	case <-time.After(time.Second):
		t.Fatal("cancelJobs was not invoked")
	}
}

func TestRunShutdownCoordinator_DelayedShutdownPreservesFullBudget(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())
	_, cancelJobs := context.WithCancel(context.Background())
	defer cancelJobs()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer ts.Close()

	var wg sync.WaitGroup

	budget := 200 * time.Millisecond
	done := make(chan shutdownResult, 1)
	go func() {
		done <- runShutdownCoordinator(shutdownCoordinatorConfig{
			logger:          log.NewLogger(),
			httpServer:      ts.Config,
			budget:          budget,
			httpTimeout:     50 * time.Millisecond,
			finalizeReserve: 50 * time.Millisecond,
			cancelJobs:      cancelJobs,
			workerWG:        &wg,
			pollCtx:         pollCtx,
		})
	}()

	time.Sleep(300 * time.Millisecond)
	signalAt := time.Now()
	cancelPoll()

	result := <-done
	assert.Less(t, result.elapsed, budget+100*time.Millisecond)
	assert.Less(t, time.Since(signalAt), budget+100*time.Millisecond)
}

func TestRunShutdownCoordinator_ConcurrentHTTPDoesNotDelayForcedCancel(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	cancelObserved := make(chan struct{}, 1)
	_, cancelJobs := context.WithCancel(context.Background())

	handlerStarted := make(chan struct{})
	handlerRelease := make(chan struct{})
	handlerDone := make(chan struct{})
	var releaseOnce sync.Once
	releaseHandler := func() {
		releaseOnce.Do(func() {
			close(handlerRelease)
		})
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(handlerStarted)
		<-handlerRelease
		close(handlerDone)
	}))
	defer ts.Close()
	defer releaseHandler()

	wrappedCancel := func() {
		cancelObserved <- struct{}{}
		cancelJobs()
	}

	requestDone := make(chan struct{})
	go func() {
		resp, err := ts.Client().Get(ts.URL)
		if err == nil {
			_ = resp.Body.Close()
		}
		close(requestDone)
	}()

	select {
	case <-handlerStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler did not start")
	}

	coordinatorReady := make(chan struct{})
	done := make(chan shutdownResult, 1)
	go func() {
		close(coordinatorReady)
		done <- runShutdownCoordinator(shutdownCoordinatorConfig{
			logger:          log.NewLogger(),
			httpServer:      ts.Config,
			budget:          300 * time.Millisecond,
			httpTimeout:     250 * time.Millisecond,
			finalizeReserve: 150 * time.Millisecond,
			cancelJobs:      wrappedCancel,
			workerWG:        &wg,
			pollCtx:         pollCtx,
		})
	}()

	<-coordinatorReady
	cancelPoll()

	select {
	case <-cancelObserved:
	case <-time.After(time.Second):
		t.Fatal("cancelJobs was not invoked")
	}

	select {
	case <-handlerDone:
		t.Fatal("HTTP shutdown completed before forced cancellation")
	default:
	}

	releaseHandler()
	result := <-done
	require.False(t, result.graceful)

	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not finish")
	}
}

func TestRunShutdownCoordinator_HTTPTimeoutClampedToRemainingBudget(t *testing.T) {
	pollCtx, cancelPoll := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer ts.Close()

	done := make(chan shutdownResult, 1)
	go func() {
		done <- runShutdownCoordinator(shutdownCoordinatorConfig{
			logger:          log.NewLogger(),
			httpServer:      ts.Config,
			budget:          80 * time.Millisecond,
			httpTimeout:     time.Second,
			finalizeReserve: 40 * time.Millisecond,
			cancelJobs:      func() {},
			workerWG:        &wg,
			pollCtx:         pollCtx,
		})
	}()

	cancelPoll()
	result := <-done
	require.False(t, result.graceful)
}

func TestWaitForHTTPShutdown_LogsShutdownError(t *testing.T) {
	httpDone := make(chan error, 1)
	httpDone <- errors.New("shutdown failed")
	waitForHTTPShutdown(shutdownCoordinatorConfig{logger: log.NewLogger()}, httpDone, time.Now().Add(50*time.Millisecond))
}

func TestWaitForHTTPShutdown_TimesOutBeforeHardDeadline(t *testing.T) {
	httpDone := make(chan error)
	waitForHTTPShutdown(shutdownCoordinatorConfig{logger: log.NewLogger()}, httpDone, time.Now().Add(20*time.Millisecond))
}

func TestWaitForHTTPShutdown_LogsErrorAfterDeadline(t *testing.T) {
	httpDone := make(chan error, 1)
	httpDone <- errors.New("shutdown failed")
	waitForHTTPShutdown(shutdownCoordinatorConfig{logger: log.NewLogger()}, httpDone, time.Now().Add(-time.Millisecond))
}
