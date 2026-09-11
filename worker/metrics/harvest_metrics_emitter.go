package metrics

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/metricsinterface"
)

var _ metricsinterface.HarvestMetricsEmitter = (*PrometheusHarvestMetricsEmitter)(nil)

// PrometheusHarvestMetricsEmitter emits Harvest metrics to Prometheus.
type PrometheusHarvestMetricsEmitter struct{}

// NewPrometheusHarvestMetricsEmitter creates a Harvest Prometheus metrics emitter.
func NewPrometheusHarvestMetricsEmitter() *PrometheusHarvestMetricsEmitter {
	return &PrometheusHarvestMetricsEmitter{}
}

// SetHarvestDriftSnapshot records a complete Harvest orphan and DB/PVC mismatch snapshot.
func (p *PrometheusHarvestMetricsEmitter) SetHarvestDriftSnapshot(
	stage string,
	snapshot metricsinterface.HarvestDriftSnapshotMetrics,
) {
	if !isHarvestDriftStage(stage) {
		return
	}

	harvestPollerOrphanCount.WithLabelValues(stage).Set(float64(snapshot.OrphanCount))
	harvestPollerMismatchCount.WithLabelValues(
		metricsinterface.HarvestMismatchKindDBWithoutPVC,
		stage,
	).Set(float64(snapshot.DBWithoutPVCCount))
	harvestPollerMismatchCount.WithLabelValues(
		metricsinterface.HarvestMismatchKindPVCWithoutDB,
		stage,
	).Set(float64(snapshot.PVCWithoutDBCount))
}

// SetHarvestLastSuccessfulReconcile records the start time of a committed reconcile run.
func (p *PrometheusHarvestMetricsEmitter) SetHarvestLastSuccessfulReconcile(runStart time.Time) {
	if runStart.IsZero() {
		return
	}
	harvestPollerLastSuccess.WithLabelValues().Set(float64(runStart.UTC().Unix()))
}

// SetHarvestRetryBacklog records pending and exhausted retry counts.
func (p *PrometheusHarvestMetricsEmitter) SetHarvestRetryBacklog(pending, exhausted int) {
	harvestPollerRetryBacklog.WithLabelValues(metricsinterface.HarvestRetryStatePending).Set(float64(pending))
	harvestPollerRetryBacklog.WithLabelValues(metricsinterface.HarvestRetryStateExhausted).Set(float64(exhausted))
}

func isHarvestDriftStage(stage string) bool {
	return stage == metricsinterface.HarvestDriftStageDetected ||
		stage == metricsinterface.HarvestDriftStageRemaining
}
