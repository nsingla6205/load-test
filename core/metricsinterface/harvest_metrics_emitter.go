package metricsinterface

import "time"

const (
	// HarvestReconcileStateAppConfigKey stores the latest committed reconcile observation.
	HarvestReconcileStateAppConfigKey = "harvest_poller_drift_reconcile_last_successful_run_start_at"

	// HarvestDriftStageDetected identifies the pre-reconcile drift snapshot.
	HarvestDriftStageDetected = "detected"
	// HarvestDriftStageRemaining identifies the post-reconcile drift snapshot.
	HarvestDriftStageRemaining = "remaining"

	// HarvestMismatchKindDBWithoutPVC identifies an eligible DB mapping with no Harvest config (farm PVC or config bucket).
	HarvestMismatchKindDBWithoutPVC = "db_without_pvc"
	// HarvestMismatchKindPVCWithoutDB identifies Harvest config that is not an eligible DB mapping (for example a wrong lease).
	HarvestMismatchKindPVCWithoutDB = "pvc_without_db"

	// HarvestRetryStatePending identifies retries that remain eligible.
	HarvestRetryStatePending = "pending"
	// HarvestRetryStateExhausted identifies retries that reached their limit.
	HarvestRetryStateExhausted = "exhausted"
)

// HarvestDriftSnapshotMetrics contains one complete Harvest drift snapshot.
type HarvestDriftSnapshotMetrics struct {
	OrphanCount       int `json:"orphan_count"`
	DBWithoutPVCCount int `json:"db_without_pvc_count"`
	PVCWithoutDBCount int `json:"pvc_without_db_count"`
}

// HarvestReconcileState is the durable state of a completed reconciliation observation.
type HarvestReconcileState struct {
	LastSuccessfulRunStartAt time.Time                   `json:"last_successful_run_start_at"`
	DetectedSnapshot         HarvestDriftSnapshotMetrics `json:"detected_snapshot"`
	RemainingSnapshot        HarvestDriftSnapshotMetrics `json:"remaining_snapshot"`
	PendingRetries           int                         `json:"pending_retries"`
	ExhaustedRetries         int                         `json:"exhausted_retries"`
}

// HarvestMetricsEmitter emits bounded Harvest observability metrics.
type HarvestMetricsEmitter interface {
	SetHarvestDriftSnapshot(stage string, snapshot HarvestDriftSnapshotMetrics)
	SetHarvestLastSuccessfulReconcile(runStart time.Time)
	SetHarvestRetryBacklog(pending, exhausted int)
}

// NoOpHarvestMetricsEmitter discards Harvest metrics.
type NoOpHarvestMetricsEmitter struct{}

func (n *NoOpHarvestMetricsEmitter) SetHarvestDriftSnapshot(string, HarvestDriftSnapshotMetrics) {
}

func (n *NoOpHarvestMetricsEmitter) SetHarvestLastSuccessfulReconcile(time.Time) {}

func (n *NoOpHarvestMetricsEmitter) SetHarvestRetryBacklog(int, int) {}
