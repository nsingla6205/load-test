package tasks

import (
	"context"
	"sync"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	poolDegradedGaugeName = "vcp.pool.degraded"
	// Three 30-second health-sync intervals allow runner ownership to move
	// between pods without immediately dropping the previous runner's series.
	poolDegradedStaleAfter = 90 * time.Second
)

type poolDegradedKey struct {
	poolID       string
	deploymentID string
}

type poolDegradedObservation struct {
	key   poolDegradedKey
	value int64
}

var (
	poolDegradedOnce    sync.Once
	poolDegradedMu      sync.RWMutex
	poolDegradedState   = map[poolDegradedKey]int64{}
	poolDegradedLastRun time.Time
	poolDegradedLogger  = log.NewLogger()
)

func initPoolDegradedGauge() {
	poolDegradedOnce.Do(func() {
		meter := otel.Meter("github.com/vcp-vsa-control-Plane/vsa-control-plane/core/tasks")
		gauge, err := meter.Int64ObservableGauge(
			poolDegradedGaugeName,
			metric.WithDescription("1 while the pool is DEGRADED, 0 while it is READY."),
		)
		if err != nil {
			poolDegradedLogger.Error("Failed to create pool degraded gauge", "error", err)
			return
		}
		_, err = meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
			for _, observation := range currentPoolDegradedObservations(time.Now()) {
				observer.ObserveInt64(gauge, observation.value, metric.WithAttributes(
					attribute.String("pool_id", observation.key.poolID),
					attribute.String("deployment_id", observation.key.deploymentID),
				))
			}
			return nil
		}, gauge)
		if err != nil {
			poolDegradedLogger.Error("Failed to register pool degraded gauge callback", "error", err)
		}
	})
}

// observePoolState records the current degraded gauge for one pool.
// READY is 0, DEGRADED is 1. Any other state drops the series.
func observePoolState(pool *database.PoolIdentifier, state string) {
	if pool == nil || pool.UUID == "" {
		return
	}
	initPoolDegradedGauge()
	key := poolDegradedKey{poolID: pool.UUID, deploymentID: pool.DeploymentName}
	poolDegradedMu.Lock()
	defer poolDegradedMu.Unlock()
	switch state {
	case datamodel.LifeCycleStateDegraded:
		poolDegradedState[key] = 1
	case datamodel.LifeCycleStateREADY:
		poolDegradedState[key] = 0
	default:
		delete(poolDegradedState, key)
	}
}

func seedPoolDegradedGauge(pools []*database.PoolIdentifier) {
	for _, pool := range pools {
		if pool != nil {
			observePoolState(pool, pool.State)
		}
	}
}

// prunePoolDegradedGauge drops series for pools that were not in this sync.
// Pools skipped after listing intentionally keep their previous value because
// the sync did not obtain a newer health signal for them.
func prunePoolDegradedGauge(pools []*database.PoolIdentifier) {
	keep := make(map[poolDegradedKey]struct{}, len(pools))
	for _, pool := range pools {
		if pool != nil && pool.UUID != "" {
			keep[poolDegradedKey{poolID: pool.UUID, deploymentID: pool.DeploymentName}] = struct{}{}
		}
	}
	poolDegradedMu.Lock()
	defer poolDegradedMu.Unlock()
	for key := range poolDegradedState {
		if _, ok := keep[key]; !ok {
			delete(poolDegradedState, key)
		}
	}
}

func beginPoolDegradedRun(now time.Time) {
	poolDegradedMu.Lock()
	defer poolDegradedMu.Unlock()
	if !poolDegradedLastRun.IsZero() && now.Sub(poolDegradedLastRun) > poolDegradedStaleAfter {
		poolDegradedState = map[poolDegradedKey]int64{}
	}
}

func markPoolDegradedRunComplete(now time.Time) {
	poolDegradedMu.Lock()
	defer poolDegradedMu.Unlock()
	poolDegradedLastRun = now
}

func currentPoolDegradedObservations(now time.Time) []poolDegradedObservation {
	poolDegradedMu.RLock()
	defer poolDegradedMu.RUnlock()
	if poolDegradedLastRun.IsZero() || now.Sub(poolDegradedLastRun) > poolDegradedStaleAfter {
		return nil
	}
	observations := make([]poolDegradedObservation, 0, len(poolDegradedState))
	for key, value := range poolDegradedState {
		observations = append(observations, poolDegradedObservation{key: key, value: value})
	}
	return observations
}
