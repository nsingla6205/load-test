package backgroundworkflows

import (
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/leakedresources/resourcescope"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/env"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	fetchCCFEBackupVaultsActivityStartToCloseTimeoutSec = env.GetUint64("FETCH_CCFE_BACKUP_VAULTS_ACTIVITY_START_TO_CLOSE_TIMEOUT_SEC", 300)
	fetchCCFEBackupVaultsActivityHeartbeatTimeoutSec    = env.GetUint64("FETCH_CCFE_BACKUP_VAULTS_ACTIVITY_HEARTBEAT_TIMEOUT_SEC", 120)
	fetchCCFEBackupVaultsActivityMaxAttempts            = env.GetInt("FETCH_CCFE_BACKUP_VAULTS_ACTIVITY_MAX_ATTEMPTS", 3)
)

// FetchCCFEBackupVaultsWorkflow returns the live CCFE backup vault
// snapshot for a single project across multiple locations. It fans out
// one FetchBackupVaultsActivity per location in parallel and aggregates
// the per-location results into a map keyed by location.
//
// Invoked synchronously by the leaked-resources backup vault detector
// once per VCP project per tick. Running on the background worker pod
// ensures the service account has the iam.serviceAccounts.
// implicitDelegation permission required for the hydration token.
//
// Per-location failures are tolerated: a location whose activity
// exhausts its retries is logged and omitted from the returned map.
// The caller treats a missing key as "skip the diff for that pair".
func FetchCCFEBackupVaultsWorkflow(ctx workflow.Context, projectID string, locations []string) (map[string][]resourcescope.CachedBackupVault, error) {
	requestID := utils.RandomUUID()
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID":                            workflow.GetInfo(ctx).WorkflowExecution.ID,
		"requestID":                             requestID,
		string(middleware.RequestCorrelationID): requestID,
		"projectID":                             projectID,
		"locationCount":                         len(locations),
	})
	logger := workflow.GetLogger(ctx)

	if len(locations) == 0 {
		logger.Info("FetchCCFEBackupVaultsWorkflow: no locations supplied; returning empty map", "projectID", projectID)
		return map[string][]resourcescope.CachedBackupVault{}, nil
	}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(fetchCCFEBackupVaultsActivityStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(fetchCCFEBackupVaultsActivityHeartbeatTimeoutSec) * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        retryPolicy.InitialInterval,
			BackoffCoefficient:     retryPolicy.BackoffCoefficient,
			MaximumInterval:        retryPolicy.MaximumInterval,
			MaximumAttempts:        int32(fetchCCFEBackupVaultsActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	act := &backgroundactivities.FetchBackupVaultsActivity{}

	futures := make([]workflow.Future, len(locations))
	for i, loc := range locations {
		futures[i] = workflow.ExecuteActivity(ctx, act.FetchBackupVaults, projectID, loc)
	}

	result := make(map[string][]resourcescope.CachedBackupVault, len(locations))
	failedCount := 0
	for i, f := range futures {
		var vaults []resourcescope.CachedBackupVault
		if err := f.Get(ctx, &vaults); err != nil {
			logger.Warn("FetchCCFEBackupVaultsWorkflow: activity failed after retries",
				"projectID", projectID, "location", locations[i], "error", err)
			failedCount++
			continue
		}
		result[locations[i]] = vaults
	}

	logger.Info("FetchCCFEBackupVaultsWorkflow: completed",
		"projectID", projectID, "locations", len(locations), "successful", len(result), "failed", failedCount)
	return result, nil
}
