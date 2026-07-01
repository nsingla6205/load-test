package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

func TestRunWorkerOnce_ReturnsFalseOnNormalExit(t *testing.T) {
	logger := log.NewLogger()
	panicked := runWorkerOnce(context.Background(), logger, "test-worker", func(context.Context) error {
		return nil
	})
	assert.False(t, panicked)
}

func TestRunWorkerOnce_ReturnsTrueOnPanic(t *testing.T) {
	logger := log.NewLogger()
	panicked := runWorkerOnce(context.Background(), logger, "test-worker", func(context.Context) error {
		panic("escaped worker panic")
	})
	assert.True(t, panicked)
}

func TestRunWorkerOnce_ReturnsFalseOnError(t *testing.T) {
	logger := log.NewLogger()
	panicked := runWorkerOnce(context.Background(), logger, "test-worker", func(context.Context) error {
		return errors.New("worker exited")
	})
	assert.False(t, panicked)
}

func TestGoJobWorker_ExitsAfterNormalReturn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewLogger()
	var calls atomic.Int32
	done := make(chan struct{})

	goJobWorker(ctx, logger, "test-worker", func(context.Context) error {
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

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
}

func TestGoJobWorker_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	logger := log.NewLogger()

	var calls atomic.Int32
	started := make(chan struct{})

	goJobWorker(ctx, logger, "test-worker", func(workerCtx context.Context) error {
		calls.Add(1)
		close(started)
		<-workerCtx.Done()
		return workerCtx.Err()
	})

	<-started
	cancel()

	require.Eventually(t, func() bool {
		return calls.Load() == 1
	}, 2*time.Second, 20*time.Millisecond)
}

func TestGoJobWorker_DoesNotRestartOnCanceledError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewLogger()
	var calls atomic.Int32
	done := make(chan struct{})

	goJobWorker(ctx, logger, "test-worker", func(context.Context) error {
		if calls.Add(1) == 1 {
			close(done)
		}
		return context.Canceled
	})

	<-done
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
}

func TestGoJobWorker_RestartsAfterEscapedPanic(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewLogger()
	var calls atomic.Int32
	secondCall := make(chan struct{})

	goJobWorker(ctx, logger, "test-worker", func(context.Context) error {
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

	assert.Equal(t, int32(2), calls.Load())
}

func TestGoJobWorker_DoesNotRestartAfterNonCanceledError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := log.NewLogger()
	var calls atomic.Int32
	done := make(chan struct{})

	goJobWorker(ctx, logger, "test-worker", func(context.Context) error {
		if calls.Add(1) == 1 {
			close(done)
		}
		return errors.New("worker exited")
	})

	<-done
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, int32(1), calls.Load())
}
