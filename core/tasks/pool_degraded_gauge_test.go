package tasks

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
)

func resetPoolDegradedGaugeTestState(t *testing.T) {
	t.Helper()
	poolDegradedMu.Lock()
	poolDegradedState = map[poolDegradedKey]int64{}
	poolDegradedLastRun = time.Time{}
	poolDegradedMu.Unlock()
}

func TestPoolDegradedGaugeRecordsCurrentState(t *testing.T) {
	resetPoolDegradedGaugeTestState(t)
	t.Cleanup(func() { resetPoolDegradedGaugeTestState(t) })
	degraded := &database.PoolIdentifier{UUID: "pool-1", DeploymentName: "dep-1"}
	ready := &database.PoolIdentifier{UUID: "pool-2", DeploymentName: "dep-2"}

	observePoolState(degraded, datamodel.LifeCycleStateDegraded)
	observePoolState(ready, datamodel.LifeCycleStateREADY)

	assert.Equal(t, int64(1), poolDegradedState[poolDegradedKey{poolID: "pool-1", deploymentID: "dep-1"}])
	assert.Equal(t, int64(0), poolDegradedState[poolDegradedKey{poolID: "pool-2", deploymentID: "dep-2"}])

	observePoolState(degraded, datamodel.LifeCycleStateREADY)
	assert.Equal(t, int64(0), poolDegradedState[poolDegradedKey{poolID: "pool-1", deploymentID: "dep-1"}])

	observePoolState(degraded, datamodel.LifeCycleStateDeleting)
	_, ok := poolDegradedState[poolDegradedKey{poolID: "pool-1", deploymentID: "dep-1"}]
	assert.False(t, ok)
}

func TestSeedPoolDegradedGaugeUsesDatabaseState(t *testing.T) {
	resetPoolDegradedGaugeTestState(t)
	t.Cleanup(func() { resetPoolDegradedGaugeTestState(t) })
	pools := []*database.PoolIdentifier{
		{UUID: "pool-1", DeploymentName: "dep-1", State: datamodel.LifeCycleStateDegraded},
		{UUID: "pool-2", DeploymentName: "dep-2", State: datamodel.LifeCycleStateREADY},
	}

	seedPoolDegradedGauge(pools)

	assert.Equal(t, int64(1), poolDegradedState[poolDegradedKey{poolID: "pool-1", deploymentID: "dep-1"}])
	assert.Equal(t, int64(0), poolDegradedState[poolDegradedKey{poolID: "pool-2", deploymentID: "dep-2"}])
}

func TestPoolDegradedGaugeExpiresAfterRunnerBecomesStale(t *testing.T) {
	resetPoolDegradedGaugeTestState(t)
	t.Cleanup(func() { resetPoolDegradedGaugeTestState(t) })
	now := time.Now()
	observePoolState(&database.PoolIdentifier{UUID: "pool-1", DeploymentName: "dep-1"}, datamodel.LifeCycleStateDegraded)
	markPoolDegradedRunComplete(now)

	assert.Len(t, currentPoolDegradedObservations(now.Add(poolDegradedStaleAfter)), 1)
	assert.Empty(t, currentPoolDegradedObservations(now.Add(poolDegradedStaleAfter+time.Nanosecond)))
}

func TestBeginPoolDegradedRunClearsOnlyStaleState(t *testing.T) {
	tests := []struct {
		name          string
		elapsed       time.Duration
		expectedCount int
	}{
		{name: "fresh previous runner", elapsed: poolDegradedStaleAfter, expectedCount: 1},
		{name: "stale previous runner", elapsed: poolDegradedStaleAfter + time.Nanosecond, expectedCount: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetPoolDegradedGaugeTestState(t)
			t.Cleanup(func() { resetPoolDegradedGaugeTestState(t) })
			lastRun := time.Now()
			observePoolState(&database.PoolIdentifier{UUID: "pool-1", DeploymentName: "dep-1"}, datamodel.LifeCycleStateDegraded)
			markPoolDegradedRunComplete(lastRun)

			beginPoolDegradedRun(lastRun.Add(test.elapsed))

			assert.Len(t, poolDegradedState, test.expectedCount)
		})
	}
}

func TestPrunePoolDegradedGaugeUsesFullKey(t *testing.T) {
	resetPoolDegradedGaugeTestState(t)
	t.Cleanup(func() { resetPoolDegradedGaugeTestState(t) })
	oldDeployment := &database.PoolIdentifier{UUID: "pool-1", DeploymentName: "old-deployment"}
	currentDeployment := &database.PoolIdentifier{UUID: "pool-1", DeploymentName: "current-deployment"}
	observePoolState(oldDeployment, datamodel.LifeCycleStateDegraded)
	observePoolState(currentDeployment, datamodel.LifeCycleStateREADY)

	prunePoolDegradedGauge([]*database.PoolIdentifier{currentDeployment})

	_, oldPresent := poolDegradedState[poolDegradedKey{poolID: "pool-1", deploymentID: "old-deployment"}]
	assert.False(t, oldPresent)
	assert.Equal(t, int64(0), poolDegradedState[poolDegradedKey{poolID: "pool-1", deploymentID: "current-deployment"}])
}
