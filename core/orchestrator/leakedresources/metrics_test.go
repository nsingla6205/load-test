package leakedresources

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources/model"
	"go.opentelemetry.io/otel/attribute"
)

type localMockReporter struct {
	mock.Mock
}

func (m *localMockReporter) Report(ctx context.Context, records []model.LeakRecord) error {
	args := m.Called(ctx, records)
	return args.Error(0)
}

func resetLeakedCountsForTest() {
	resetLeakedCounts()
}

func TestMetricsReporter_Report_AggregatesCountsBySafeDimensions(t *testing.T) {
	resetLeakedCountsForTest()
	r := NewMetricsReporter()

	records := []model.LeakRecord{
		{
			ResourceType: model.ResourceTypePool,
			ResourceID:   "pool-a",
			ResourceName: "pool-a-name",
			Reason:       "in_vcp_not_in_ccfe",
			ProjectID:    "p1",
			Region:       "us-central1",
		},
		{
			ResourceType: model.ResourceTypePool,
			ResourceID:   "pool-a",
			ResourceName: "pool-a-name",
			Reason:       "in_vcp_not_in_ccfe",
			ProjectID:    "p1",
			Region:       "us-central1",
		},
		{
			ResourceType: model.ResourceTypePool,
			ResourceID:   "pool-b",
			ResourceName: "pool-b-name",
			Reason:       "in_vcp_not_in_ccfe",
			ProjectID:    "p1",
			Region:       "us-central1",
		},
		{
			ResourceType: model.ResourceTypeVolume,
			ResourceID:   "vol-1",
			ResourceName: "vol-one",
			Reason:       "volume_orphan_pool_missing",
		},
	}

	err := r.Report(context.Background(), records)
	assert.NoError(t, err)

	leakedMetricsMu.RLock()
	defer leakedMetricsMu.RUnlock()
	assert.Equal(t, int64(2), leakedCountsByKey[leakedCountKey{
		resourceType: "pool",
		reason:       "in_vcp_not_in_ccfe",
		projectID:    "p1",
		region:       "us-central1",
		resourceID:   "pool-a",
		resourceName: "pool-a-name",
	}])
	assert.Equal(t, int64(1), leakedCountsByKey[leakedCountKey{
		resourceType: "pool",
		reason:       "in_vcp_not_in_ccfe",
		projectID:    "p1",
		region:       "us-central1",
		resourceID:   "pool-b",
		resourceName: "pool-b-name",
	}])
	assert.Equal(t, int64(1), leakedCountsByKey[leakedCountKey{
		resourceType: "volume",
		reason:       "volume_orphan_pool_missing",
		projectID:    "",
		region:       "",
		resourceID:   "vol-1",
		resourceName: "vol-one",
	}])
}

// TestMetricsReporter_Report_AccumulatesWithinRun verifies that multiple Report
// calls within a single run (per-detector reporting) accumulate into the gauge
// snapshot instead of clobbering each other.
func TestMetricsReporter_Report_AccumulatesWithinRun(t *testing.T) {
	resetLeakedCountsForTest()
	r := NewMetricsReporter()

	err := r.Report(context.Background(), []model.LeakRecord{
		{
			ResourceType: model.ResourceTypePool,
			ResourceID:   "pool-1",
			ResourceName: "p-one",
			Reason:       "in_vcp_not_in_ccfe",
		},
	})
	assert.NoError(t, err)

	err = r.Report(context.Background(), []model.LeakRecord{
		{
			ResourceType: model.ResourceTypeSnapshot,
			ResourceID:   "snap-2",
			ResourceName: "snap-two",
			Reason:       "snapshot_orphan_volume_missing",
		},
	})
	assert.NoError(t, err)

	leakedMetricsMu.RLock()
	defer leakedMetricsMu.RUnlock()
	// Both detectors' records coexist within the run (accumulate, not replace).
	assert.Len(t, leakedCountsByKey, 2)
	assert.Equal(t, int64(1), leakedCountsByKey[leakedCountKey{
		resourceType: "pool",
		reason:       "in_vcp_not_in_ccfe",
		resourceID:   "pool-1",
		resourceName: "p-one",
	}])
	assert.Equal(t, int64(1), leakedCountsByKey[leakedCountKey{
		resourceType: "snapshot",
		reason:       "snapshot_orphan_volume_missing",
		resourceID:   "snap-2",
		resourceName: "snap-two",
	}])
}

// TestMetricsReporter_BeginRun_ResetsPreviousRunState verifies that BeginRun
// clears the previous run's snapshot so resources no longer leaked drop off the
// gauge, while records reported after BeginRun form the new run's snapshot.
func TestMetricsReporter_BeginRun_ResetsPreviousRunState(t *testing.T) {
	resetLeakedCountsForTest()
	r := NewMetricsReporter()

	err := r.Report(context.Background(), []model.LeakRecord{
		{
			ResourceType: model.ResourceTypePool,
			ResourceID:   "pool-1",
			ResourceName: "p-one",
			Reason:       "in_vcp_not_in_ccfe",
		},
	})
	assert.NoError(t, err)

	// New run: reset first, then report only the snapshot record.
	r.BeginRun(context.Background())
	err = r.Report(context.Background(), []model.LeakRecord{
		{
			ResourceType: model.ResourceTypeSnapshot,
			ResourceID:   "snap-2",
			ResourceName: "snap-two",
			Reason:       "snapshot_orphan_volume_missing",
		},
	})
	assert.NoError(t, err)

	leakedMetricsMu.RLock()
	defer leakedMetricsMu.RUnlock()
	assert.Len(t, leakedCountsByKey, 1)
	assert.Equal(t, int64(1), leakedCountsByKey[leakedCountKey{
		resourceType: "snapshot",
		reason:       "snapshot_orphan_volume_missing",
		projectID:    "",
		region:       "",
		resourceID:   "snap-2",
		resourceName: "snap-two",
	}])
}

// TestMultiReporter_BeginRun_PropagatesToRunAwareChildren verifies MultiReporter
// forwards BeginRun to children that hold per-run state (MetricsReporter) so a
// per-detector MultiReporter(log, metrics) still resets correctly.
func TestMultiReporter_BeginRun_PropagatesToRunAwareChildren(t *testing.T) {
	resetLeakedCountsForTest()
	multi := NewMultiReporter(LogReporter{}, NewMetricsReporter())

	err := multi.Report(context.Background(), []model.LeakRecord{
		{ResourceType: model.ResourceTypePool, ResourceID: "pool-1", Reason: "in_vcp_not_in_ccfe"},
	})
	assert.NoError(t, err)

	leakedMetricsMu.RLock()
	assert.Len(t, leakedCountsByKey, 1)
	leakedMetricsMu.RUnlock()

	multi.BeginRun(context.Background())

	leakedMetricsMu.RLock()
	defer leakedMetricsMu.RUnlock()
	assert.Empty(t, leakedCountsByKey)
}

func TestMultiReporter_Report_PropagatesError(t *testing.T) {
	ok := &localMockReporter{}
	fail := &localMockReporter{}
	ok.On("Report", context.Background(), mockAnyLeakRecords()).Return(nil).Once()
	fail.On("Report", context.Background(), mockAnyLeakRecords()).Return(errors.New("boom")).Once()

	r := NewMultiReporter(ok, fail)
	err := r.Report(context.Background(), []model.LeakRecord{{ResourceType: model.ResourceTypePool, Reason: "x"}})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
	ok.AssertExpectations(t)
	fail.AssertExpectations(t)
}

func TestRecordMonitoringRun_InitializesCounter(t *testing.T) {
	recordMonitoringRun(context.Background(), "success")
	assert.NotNil(t, monitoringRunsCounter)
}

func TestRecordMonitoringRun_EmptyStatusDefaultsToError(t *testing.T) {
	recordMonitoringRun(context.Background(), "")
	assert.NotNil(t, monitoringRunsCounter)
}

func TestCurrentLeakCountObservations_IncludesOptionalDimensionsConditionally(t *testing.T) {
	resetLeakedCountsForTest()
	updateLeakedCounts([]model.LeakRecord{
		{
			ResourceType: model.ResourceTypePool,
			ResourceID:   "pid-1",
			ResourceName: "pool-one",
			Reason:       "in_vcp_not_in_ccfe",
			ProjectID:    "proj-1",
			Region:       "us-central1",
		},
		{
			ResourceType: model.ResourceTypeVolume,
			ResourceID:   "vid-1",
			ResourceName: "v-one",
			Reason:       "volume_orphan_pool_missing",
		},
	})

	observations := currentLeakCountObservations()
	assert.Len(t, observations, 2)

	containsObs := func(expectedCount int64, attrs map[string]string) bool {
		for _, o := range observations {
			if o.count != expectedCount {
				continue
			}
			m := map[string]string{}
			for _, kv := range o.attrs {
				if kv.Value.Type() == attribute.STRING {
					m[string(kv.Key)] = kv.Value.AsString()
				}
			}
			match := true
			for k, v := range attrs {
				if m[k] != v {
					match = false
					break
				}
			}
			if match && len(m) == len(attrs) {
				return true
			}
		}
		return false
	}

	assert.True(t, containsObs(1, map[string]string{
		"resource_type": "pool",
		"reason":        "in_vcp_not_in_ccfe",
		"project_id":    "proj-1",
		"region":        "us-central1",
		"resource_id":   "pid-1",
		"resource_name": "pool-one",
	}))
	assert.True(t, containsObs(1, map[string]string{
		"resource_type": "volume",
		"reason":        "volume_orphan_pool_missing",
		"resource_id":   "vid-1",
		"resource_name": "v-one",
	}))
}

func mockAnyLeakRecords() []model.LeakRecord {
	return []model.LeakRecord{{ResourceType: model.ResourceTypePool, Reason: "x"}}
}
