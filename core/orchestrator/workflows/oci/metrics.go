package oci

import (
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/metrics"
	"go.temporal.io/sdk/workflow"
)

const (
	// Workflow names.
	wfCreatePool = "oci_create_pool"
	wfDeletePool = "oci_delete_pool"
	wfCreateSVM  = "oci_create_svm"
	wfDeleteSVM  = "oci_delete_svm"
	wfUpdateSVM  = "oci_update_svm"
	wfUpdatePool = "oci_update_pool"

	// Pool workflow stages.
	stageVLMDeploy         = "vlm_deploy"
	stageSaveNodeDetails   = "save_node_details"
	stageMarkReady         = "mark_ready"
	stageVLMDelete         = "vlm_delete"
	stageDBCleanup         = "db_cleanup"
	stageVLMUpdate         = "vlm_update"

	stageDBPersistFinal    = "db_persist_final"

	// SVM workflow stages (workflow label disambiguates shared stage names).
	stageParseVlmConfig     = "parse_vlm_config"
	stageGetOntapAdminCreds = "get_ontap_admin_creds"
	stageGetSVMAdminCreds   = "get_svm_admin_creds"
	stageVLMCreateSVM       = "vlm_create_svm"
	stageSaveSVMLif         = "save_svm_lif"
	stageVLMDeleteSVM       = "vlm_delete_svm"
	stageSoftDeleteSVM      = "soft_delete_svm"
	stagePersistPoolVLMCfg  = "persist_pool_vlm_config"
	stageVLMModifySVM       = "vlm_modify_svm"
	stageMarkSVMReady       = "mark_svm_ready"

	// Result values.
	resultSuccess = "success"
	resultFailure = "failure"

	// Queue types.
	queueCustomer = "customer"
)

var workflowStageTotal = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "oci_workflow_stage_total",
		Help: "Per-stage success/failure",
	},
	[]string{"workflow", "queue_type", "stage", "result"},
)

var workflowDurationSeconds = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "oci_workflow_duration_seconds",
		Help:    "Start-to-terminal duration for a workflow",
		Buckets: []float64{5, 30, 60, 120, 300, 600, 900, 1200, 1800, 3600},
	},
	[]string{"workflow", "region", "queue_type"},
)

func init() {
	collectors := []prometheus.Collector{workflowStageTotal, workflowDurationSeconds}
	metrics.Registry.MustRegister(collectors...)
	for _, c := range collectors {
		if err := prometheus.Register(c); err != nil {
			slog.Warn("oci/metrics: skipping default-registry registration", "error", err)
		}
	}
}

func emitStage(ctx workflow.Context, wfName, queueType, stage, result string) {
	_ = workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		workflowStageTotal.WithLabelValues(wfName, queueType, stage, result).Inc()
		return nil
	}).Get(nil)
}

func emitDuration(ctx workflow.Context, wfName, queueType string, start time.Time) {
	elapsed := workflow.Now(ctx).Sub(start).Seconds()
	_ = workflow.SideEffect(ctx, func(_ workflow.Context) interface{} {
		workflowDurationSeconds.WithLabelValues(wfName, metrics.Region(), queueType).Observe(elapsed)
		return nil
	}).Get(nil)
}
