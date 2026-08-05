package main

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
)

type shutdownResult struct {
	graceful bool
	elapsed  time.Duration
}

type shutdownCoordinatorConfig struct {
	logger          log.Logger
	httpServer      *http.Server
	budget          time.Duration
	httpTimeout     time.Duration
	finalizeReserve time.Duration
	cancelJobs      context.CancelFunc
	workerWG        *sync.WaitGroup
	pollCtx         context.Context
	workerCount     int
}

// effectiveFinalizeReserve clamps finalizeReserve to [0, budget] so a reserve
// larger than the shutdown budget does not place softDeadline in the past.
func effectiveFinalizeReserve(budget, reserve time.Duration) (effective time.Duration, clamped bool) {
	switch {
	case reserve < 0:
		return 0, true
	case reserve > budget:
		return budget, true
	default:
		return reserve, false
	}
}

func runShutdownCoordinator(cfg shutdownCoordinatorConfig) shutdownResult {
	<-cfg.pollCtx.Done()
	start := time.Now()

	hardDeadline := start.Add(cfg.budget)
	finalizeReserve, reserveClamped := effectiveFinalizeReserve(cfg.budget, cfg.finalizeReserve)
	softDeadline := hardDeadline.Add(-finalizeReserve)

	if reserveClamped {
		cfg.logger.Warn("shutdown_finalize_reserve_clamped",
			"requested", cfg.finalizeReserve.String(),
			"effective", finalizeReserve.String(),
			"budget", cfg.budget.String())
	}

	cfg.logger.Info("shutdown_phase=draining",
		"budget", cfg.budget.String(),
		"finalize_reserve", finalizeReserve.String(),
		"remaining", time.Until(hardDeadline).String())

	httpDone := make(chan error, 1)
	go func() {
		httpTimeout := cfg.httpTimeout
		if remaining := time.Until(hardDeadline); httpTimeout > remaining {
			httpTimeout = remaining
		}
		if httpTimeout <= 0 || cfg.httpServer == nil {
			httpDone <- nil
			return
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), httpTimeout)
		defer cancel()
		httpDone <- cfg.httpServer.Shutdown(shutdownCtx)
	}()

	workersDone := make(chan struct{})
	go func() {
		cfg.workerWG.Wait()
		close(workersDone)
	}()

	graceful := waitUntil(workersDone, time.Until(softDeadline))
	if graceful {
		waitForHTTPShutdown(cfg, httpDone, hardDeadline)
		cfg.logger.Info("shutdown_phase=graceful",
			"workers", cfg.workerCount,
			"elapsed", time.Since(start).String(),
			"remaining", time.Until(hardDeadline).String())
		return shutdownResult{graceful: true, elapsed: time.Since(start)}
	}

	cfg.logger.Warn("shutdown_phase=forced",
		"elapsed", time.Since(start).String(),
		"remaining", time.Until(hardDeadline).String())
	cfg.cancelJobs()
	_ = waitUntil(workersDone, time.Until(hardDeadline))
	waitForHTTPShutdown(cfg, httpDone, hardDeadline)

	return shutdownResult{graceful: false, elapsed: time.Since(start)}
}

func waitForHTTPShutdown(cfg shutdownCoordinatorConfig, httpDone <-chan error, hardDeadline time.Time) {
	remaining := time.Until(hardDeadline)
	if remaining <= 0 {
		select {
		case err := <-httpDone:
			if err != nil {
				cfg.logger.Error("HTTP shutdown failed", "error", err.Error())
			}
		default:
		}
		return
	}

	timer := time.NewTimer(remaining)
	defer timer.Stop()
	select {
	case err := <-httpDone:
		if err != nil {
			cfg.logger.Error("HTTP shutdown failed", "error", err.Error())
		}
	case <-timer.C:
		cfg.logger.Warn("HTTP shutdown did not complete before hard deadline")
	}
}

func waitUntil(done <-chan struct{}, timeout time.Duration) bool {
	if timeout <= 0 {
		return false
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
