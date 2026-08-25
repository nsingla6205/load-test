package backgroundworkflows

import (
	"fmt"
	"time"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/backgroundactivities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	// expertModeVolumeSyncParentListPageSize is how many pools the parent workflow lists per
	// ListOntapModePoolsPaginated call. Each page is reconciled by a single batch child
	// workflow, so this also bounds that child's size; pagination keeps both the parent's and
	// the child's event history compact even at thousands of pools.
	expertModeVolumeSyncParentListPageSize = 100

	// expertModeVolumeSyncActivityConcurrency caps the number of per-pool sync activities
	// running in parallel inside a single batch child (the effective sync concurrency).
	expertModeVolumeSyncActivityConcurrency = 10

	// expertModeVolumeSyncPoolActivityTimeout bounds a single per-pool sync activity. The
	// activity must not exceed this even on the largest pool; the heartbeat below detects
	// stalls earlier.
	expertModeVolumeSyncPoolActivityTimeout = 10 * time.Minute
)

// ExpertModeVolumeSyncParentWorkflow lists ONTAP-mode pools in pages and reconciles each
// page through batch child workflows. Per-batch failures are aggregated into the result
// counters; they do not fail the parent.
func ExpertModeVolumeSyncParentWorkflow(ctx workflow.Context) (*GenericParentWorkflowResult, error) {
	logger := workflow.GetLogger(ctx)

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	// Activity options for the parent's pool-listing activity. These are short DB reads;
	// reuse the shared background-workflow timeouts.
	listActivityCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: time.Duration(backgroundWorkflowStartToCloseTimeoutSec) * time.Second,
		HeartbeatTimeout:    time.Duration(backgroundWorkflowHeartbeatTimeoutSec) * time.Second,
		RetryPolicy:         buildExpertModeVolumeSyncRetryPolicy(retryPolicy),
	})

	syncActivity := &backgroundactivities.SyncExpertModeVolumesActivity{}

	logger.Info("Starting parent workflow 'expert-mode-volume-sync-parent'")

	var totalProcessed, totalSuccessful, totalFailed int

	// Page through ONTAP-mode pools until a short (or empty) page signals the end. This
	// avoids a separate, racy count query and naturally handles the zero-pool case.
	for offset := 0; ; offset += expertModeVolumeSyncParentListPageSize {
		var pools []*database.PoolIdentifier
		if err := workflow.ExecuteActivity(listActivityCtx, syncActivity.ListOntapModePoolsPaginated, offset, expertModeVolumeSyncParentListPageSize).Get(ctx, &pools); err != nil {
			logger.Error("Parent workflow 'expert-mode-volume-sync-parent' -> list pools failed", "offset", offset, "error", err)
			return nil, err
		}
		if len(pools) == 0 {
			break
		}

		logger.Info("Parent workflow 'expert-mode-volume-sync-parent' -> listed pools", "count", len(pools), "offset", offset)

		processed, successful, failed := runExpertModeVolumeSyncBatch(ctx, offset, pools)
		totalProcessed += processed
		totalSuccessful += successful
		totalFailed += failed

		if len(pools) < expertModeVolumeSyncParentListPageSize {
			break
		}
	}

	logger.Info("Parent workflow 'expert-mode-volume-sync-parent' completed",
		"totalProcessed", totalProcessed, "successful", totalSuccessful, "failed", totalFailed)

	return &GenericParentWorkflowResult{
		TotalItemsProcessed: totalProcessed,
		TotalSuccessful:     totalSuccessful,
		TotalFailed:         totalFailed,
	}, nil
}

// SyncExpertModeVolumesForPoolBatchWorkflow reconciles expert-mode volumes for a batch of
// pools by running per-pool sync activities in parallel waves of size
// expertModeVolumeSyncActivityConcurrency. Per-pool failures are recorded in the return
// value but do not fail the batch.
func SyncExpertModeVolumesForPoolBatchWorkflow(ctx workflow.Context, poolIdentifiers []*database.PoolIdentifier) (*backgroundactivities.SyncExpertModeVolumesBatchReturnValue, error) {
	logger := workflow.GetLogger(ctx)
	if len(poolIdentifiers) == 0 {
		return &backgroundactivities.SyncExpertModeVolumesBatchReturnValue{}, nil
	}

	retryPolicy, err := workflows.PopulateRetryPolicyParams()
	if err != nil {
		return nil, err
	}

	ctx = workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: expertModeVolumeSyncPoolActivityTimeout,
		HeartbeatTimeout:    retryPolicy.HeartBeatTimeout,
		RetryPolicy:         buildExpertModeVolumeSyncRetryPolicy(retryPolicy),
	})

	syncActivity := &backgroundactivities.SyncExpertModeVolumesActivity{}
	result := &backgroundactivities.SyncExpertModeVolumesBatchReturnValue{
		TotalProcessed: len(poolIdentifiers),
	}

	logger.Info("Starting batch workflow for expert mode volume sync", "totalPools", len(poolIdentifiers))

	for i := 0; i < len(poolIdentifiers); i += expertModeVolumeSyncActivityConcurrency {
		end := i + expertModeVolumeSyncActivityConcurrency
		if end > len(poolIdentifiers) {
			end = len(poolIdentifiers)
		}
		wave := poolIdentifiers[i:end]

		futures := make([]workflow.Future, len(wave))
		for j, pool := range wave {
			futures[j] = workflow.ExecuteActivity(ctx, syncActivity.SyncExpertModeVolumesForPoolActivity, pool)
		}

		for j, future := range futures {
			pool := wave[j]
			if err := future.Get(ctx, nil); err != nil {
				logger.Error("Failed to sync expert mode volumes for pool", "pool", pool.Name, "error", err)
				result.Failed++
				result.FailedResourceNames = append(result.FailedResourceNames, pool.Name)
				result.FailedResourceErrors = append(result.FailedResourceErrors, backgroundactivities.ParentChildWorkflowError{
					ResourceName: pool.Name,
					Error:        err.Error(),
				})
			} else {
				result.Successful++
			}
		}
	}

	if result.Failed > 0 {
		logger.Warn("Expert mode volume sync batch workflow failed for pools", "failedCount", result.Failed, "pools", result.FailedResourceNames)
	}
	logger.Info("Expert mode volume sync batch workflow completed",
		"total", result.TotalProcessed, "successful", result.Successful, "failed", result.Failed)

	return result, nil
}

// runExpertModeVolumeSyncBatch reconciles one page of pools via a single batch child
// workflow and returns the page's processed/successful/failed counts. A child that errors
// counts all of the page's pools as failed but does not fail the parent workflow.
func runExpertModeVolumeSyncBatch(ctx workflow.Context, pageOffset int, pools []*database.PoolIdentifier) (totalProcessed, successful, failed int) {
	total := len(pools)
	if total == 0 {
		return 0, 0, 0
	}

	logger := workflow.GetLogger(ctx)
	parentID := workflow.GetInfo(ctx).WorkflowExecution.ID
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID: fmt.Sprintf("%s-page-%d", parentID, pageOffset),
	})
	logger.Info("Parent workflow 'expert-mode-volume-sync-parent' -> reconciling page", "pageOffset", pageOffset, "pools", total)

	var batchResult backgroundactivities.SyncExpertModeVolumesBatchReturnValue
	if err := workflow.ExecuteChildWorkflow(childCtx, SyncExpertModeVolumesForPoolBatchWorkflow, pools).Get(ctx, &batchResult); err != nil {
		logger.Error("Parent workflow 'expert-mode-volume-sync-parent' -> page failed", "pageOffset", pageOffset, "error", err)
		return total, 0, total
	}
	return total, batchResult.Successful, batchResult.Failed
}

// buildExpertModeVolumeSyncRetryPolicy converts a shared WorkflowRetryPolicy into a
// Temporal RetryPolicy with the standard PanicError opt-out so transient ONTAP/DB errors
// (timeouts, brief 5xx, dropped connections) recover within the same sync cycle.
func buildExpertModeVolumeSyncRetryPolicy(rp *workflows.WorkflowRetryPolicy) *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		InitialInterval:        rp.InitialInterval,
		BackoffCoefficient:     rp.BackoffCoefficient,
		MaximumInterval:        rp.MaximumInterval,
		MaximumAttempts:        int32(rp.MaximumAttempts),
		NonRetryableErrorTypes: []string{"PanicError"},
	}
}
