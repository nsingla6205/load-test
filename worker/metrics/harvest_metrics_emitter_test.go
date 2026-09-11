package metrics

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/metricsinterface"
)

func TestPrometheusHarvestMetricsEmitter(t *testing.T) {
	runStart := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.FixedZone("IST", 19800))
	tests := []struct {
		name     string
		exercise func(*PrometheusHarvestMetricsEmitter)
		verify   func(*testing.T)
	}{
		{
			name: "snapshot",
			exercise: func(emitter *PrometheusHarvestMetricsEmitter) {
				emitter.SetHarvestDriftSnapshot(metricsinterface.HarvestDriftStageDetected, metricsinterface.HarvestDriftSnapshotMetrics{
					OrphanCount:       3,
					DBWithoutPVCCount: 2,
					PVCWithoutDBCount: 1,
				})
			},
			verify: func(t *testing.T) {
				assertGaugeValue(t, harvestPollerOrphanCount.WithLabelValues(metricsinterface.HarvestDriftStageDetected), 3)
				assertGaugeValue(t, harvestPollerMismatchCount.WithLabelValues(
					metricsinterface.HarvestMismatchKindDBWithoutPVC,
					metricsinterface.HarvestDriftStageDetected,
				), 2)
				assertGaugeValue(t, harvestPollerMismatchCount.WithLabelValues(
					metricsinterface.HarvestMismatchKindPVCWithoutDB,
					metricsinterface.HarvestDriftStageDetected,
				), 1)
			},
		},
		{
			name: "last success",
			exercise: func(emitter *PrometheusHarvestMetricsEmitter) {
				emitter.SetHarvestLastSuccessfulReconcile(runStart)
			},
			verify: func(t *testing.T) {
				assertGaugeValue(t, harvestPollerLastSuccess.WithLabelValues(), float64(runStart.UTC().Unix()))
			},
		},
		{
			name: "retry backlog",
			exercise: func(emitter *PrometheusHarvestMetricsEmitter) {
				emitter.SetHarvestRetryBacklog(4, 2)
			},
			verify: func(t *testing.T) {
				assertGaugeValue(t, harvestPollerRetryBacklog.WithLabelValues(metricsinterface.HarvestRetryStatePending), 4)
				assertGaugeValue(t, harvestPollerRetryBacklog.WithLabelValues(metricsinterface.HarvestRetryStateExhausted), 2)
			},
		},
		{
			name: "invalid labels and zero values",
			exercise: func(emitter *PrometheusHarvestMetricsEmitter) {
				emitter.SetHarvestDriftSnapshot("invalid", metricsinterface.HarvestDriftSnapshotMetrics{OrphanCount: 1})
				emitter.SetHarvestLastSuccessfulReconcile(time.Time{})
				emitter.SetHarvestRetryBacklog(1, 2)
			},
			verify: func(t *testing.T) {
				assertGaugeValue(t, harvestPollerRetryBacklog.WithLabelValues(metricsinterface.HarvestRetryStatePending), 1)
				assertGaugeValue(t, harvestPollerRetryBacklog.WithLabelValues(metricsinterface.HarvestRetryStateExhausted), 2)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.NoError(t, RegisterHarvestMetrics())
			resetHarvestMetrics()

			tt.exercise(NewPrometheusHarvestMetricsEmitter())
			tt.verify(t)
		})
	}
}

func resetHarvestMetrics() {
	harvestPollerOrphanCount.Reset()
	harvestPollerMismatchCount.Reset()
	harvestPollerRetryBacklog.Reset()
	harvestPollerLastSuccess.Reset()
}

func assertGaugeValue(t *testing.T, gauge interface{ Write(*dto.Metric) error }, expected float64) {
	t.Helper()

	var metric dto.Metric
	require.NoError(t, gauge.Write(&metric))
	assert.Equal(t, expected, metric.GetGauge().GetValue())
}
