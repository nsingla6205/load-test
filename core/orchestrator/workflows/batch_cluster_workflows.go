package workflows

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/models"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// BatchClusterUpgradeWorkflowParams holds the parameters for the batch cluster upgrade workflow
type BatchClusterUpgradeWorkflowParams struct {
	BatchJobID       string                   `json:"batchJobId"`
	SelectedClusters []models.SelectedCluster `json:"selectedClusters"`
	TargetVersion    string                   `json:"targetVersion"`
	VSAImagePath     string                   `json:"vsaImagePath"`
	VSAImageName     string                   `json:"vsaImageName"`
	MediatorName     string                   `json:"mediatorName"`
	ForceUpgrade     bool                     `json:"forceUpgrade"`
	MaxConcurrency   int                      `json:"maxConcurrency"`
	FailureThreshold int                      `json:"failureThreshold"`
	Metadata         map[string]string        `json:"metadata,omitempty"`
}

// ChildUpgradeResult holds the result of a single child cluster upgrade
type ChildUpgradeResult struct {
	ClusterID string `json:"clusterId"`
	JobID     string `json:"jobId"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

type batchClusterUpgradeWorkflow struct {
	BaseWorkflow
}

var _ WorkflowInterface = &batchClusterUpgradeWorkflow{}

// BatchClusterUpgradeWorkflow is the parent workflow that orchestrates batch cluster upgrades
// by spawning child workflows for each selected cluster with concurrency control.
func BatchClusterUpgradeWorkflow(ctx workflow.Context, params *BatchClusterUpgradeWorkflowParams) error {
	batchWF := new(batchClusterUpgradeWorkflow)
	err := batchWF.Setup(ctx, params)
	if err != nil {
		return err
	}
	batchWF.Status = WorkflowStatusRunning

	_, customErr := batchWF.Run(ctx, params)
	if customErr != nil {
		batchWF.Status = WorkflowStatusFailed
		return customErr
	}

	batchWF.Status = WorkflowStatusCompleted
	return nil
}

func (wf *batchClusterUpgradeWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	params := input.(*BatchClusterUpgradeWorkflowParams)
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.CustomerID = "system"
	wf.Status = WorkflowStatusCreated
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID": wf.ID,
		"batchJobID": params.BatchJobID,
	})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*WorkflowStatus, error) {
		return &WorkflowStatus{
			ID:         wf.ID,
			Status:     wf.Status,
			CustomerID: wf.CustomerID,
		}, nil
	})
}

func (wf *batchClusterUpgradeWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	params := args[0].(*BatchClusterUpgradeWorkflowParams)

	wf.Logger.Info("Starting batch cluster upgrade workflow",
		"batchJobID", params.BatchJobID,
		"totalClusters", len(params.SelectedClusters),
		"maxConcurrency", params.MaxConcurrency,
		"failureThreshold", params.FailureThreshold)

	// Activity options for status updates
	batchActivity := &activities.BatchClusterUpgradeActivity{}
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts:        3,
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	statusCtx := workflow.WithActivityOptions(ctx, ao)

	// Update batch job status to IN_PROGRESS
	err := workflow.ExecuteActivity(statusCtx, batchActivity.UpdateBatchJobStatusActivity,
		params.BatchJobID, string(models.UpgradeStatusInProgress), "").Get(statusCtx, nil)
	if err != nil {
		wf.Logger.Error("Failed to update batch job status to IN_PROGRESS", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, err)
	}

	// Process clusters in waves bounded by MaxConcurrency.
	results := make([]ChildUpgradeResult, len(params.SelectedClusters))
	failureCount := 0
	successCount := 0
	skippedCount := 0
	aborted := false

	// Process in batches based on MaxConcurrency
	for batchStart := 0; batchStart < len(params.SelectedClusters) && !aborted; batchStart += params.MaxConcurrency {
		batchEnd := batchStart + params.MaxConcurrency
		if batchEnd > len(params.SelectedClusters) {
			batchEnd = len(params.SelectedClusters)
		}

		currentBatch := params.SelectedClusters[batchStart:batchEnd]

		wf.Logger.Info("Processing batch of clusters",
			"batchStart", batchStart,
			"batchEnd", batchEnd,
			"batchSize", len(currentBatch))

		waveClusters := make([]activities.BatchUpgradeWaveCluster, len(currentBatch))
		for i, cluster := range currentBatch {
			waveClusters[i] = activities.BatchUpgradeWaveCluster{
				SelectedCluster: cluster,
				JobID:           generateChildUpgradeJobID(params.BatchJobID, cluster.ClusterID),
			}
		}

		var preparedClusters []activities.PreparedClusterUpgrade
		prepareErr := workflow.ExecuteActivity(
			statusCtx,
			batchActivity.PrepareBatchUpgradeWaveActivity,
			&activities.PrepareBatchUpgradeWaveParams{
				BatchUpgradeID:     params.BatchJobID,
				Clusters:           waveClusters,
				TargetVersion:      params.TargetVersion,
				VSABuildImage:      params.VSAImageName,
				MediatorBuildImage: params.MediatorName,
				ForceUpgrade:       params.ForceUpgrade,
			},
		).Get(statusCtx, &preparedClusters)

		// Launch every successfully prepared child before waiting on any child.
		futures := make([]workflow.ChildWorkflowFuture, len(currentBatch))
		if prepareErr != nil {
			wf.Logger.Error("Failed to prepare batch upgrade wave", "error", prepareErr)
			for i, cluster := range currentBatch {
				results[batchStart+i] = ChildUpgradeResult{
					ClusterID: cluster.ClusterID,
					Success:   false,
					Error:     "Failed to prepare batch upgrade wave: " + prepareErr.Error(),
				}
				failureCount++
			}
		} else if len(preparedClusters) != len(currentBatch) {
			prepareErr = fmt.Errorf("prepared %d of %d clusters", len(preparedClusters), len(currentBatch))
			wf.Logger.Error("Batch upgrade wave preparation returned an unexpected result", "error", prepareErr)
			for i, cluster := range currentBatch {
				results[batchStart+i] = ChildUpgradeResult{
					ClusterID: cluster.ClusterID,
					Success:   false,
					Error:     "Failed to prepare batch upgrade wave: " + prepareErr.Error(),
				}
				failureCount++
			}
		} else {
			for i, prepared := range preparedClusters {
				cluster := currentBatch[i]
				if prepared.Error != "" || prepared.Pool == nil {
					preparationError := prepared.Error
					if preparationError == "" {
						preparationError = "prepared pool is missing"
					}
					wf.Logger.Error("Failed to prepare child upgrade",
						"clusterId", cluster.ClusterID,
						"error", preparationError)
					results[batchStart+i] = ChildUpgradeResult{
						ClusterID: cluster.ClusterID,
						JobID:     prepared.JobID,
						Success:   false,
						Error:     preparationError,
					}
					failureCount++
					continue
				}

				childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
					WorkflowID:        BatchChildUpgradeWorkflowID(params.BatchJobID, cluster.ClusterID),
					ParentClosePolicy: enums.PARENT_CLOSE_POLICY_ABANDON,
				})
				childParams := &ClusterUpgradeWorkflowParams{
					JobID:             prepared.JobID,
					ClusterID:         cluster.ClusterID,
					TargetVersion:     params.TargetVersion,
					VSAImagePath:      params.VSAImagePath,
					VSAImageName:      params.VSAImageName,
					MediatorImageName: params.MediatorName,
					ForceUpgrade:      params.ForceUpgrade,
					Metadata:          params.Metadata,
					Pool:              prepared.Pool,
				}
				futures[i] = workflow.ExecuteChildWorkflow(childCtx, ClusterUpgradeWorkflow, childParams)
			}
		}

		if params.FailureThreshold > 0 && failureCount >= params.FailureThreshold {
			aborted = true
		}

		// Always drain every child that was started in this wave.
		for i, future := range futures {
			if future == nil {
				continue
			}

			cluster := currentBatch[i]
			childErr := future.Get(ctx, nil)

			if childErr != nil {
				wf.Logger.Error("Child cluster upgrade failed",
					"clusterId", cluster.ClusterID, "error", childErr)
				results[batchStart+i] = ChildUpgradeResult{
					ClusterID: cluster.ClusterID,
					Success:   false,
					Error:     childErr.Error(),
				}
				failureCount++
				if params.FailureThreshold > 0 && failureCount >= params.FailureThreshold {
					aborted = true
				}
			} else {
				wf.Logger.Info("Child cluster upgrade completed successfully",
					"clusterId", cluster.ClusterID)
				results[batchStart+i] = ChildUpgradeResult{
					ClusterID: cluster.ClusterID,
					Success:   true,
				}
				successCount++
			}
		}

		if aborted {
			wf.Logger.Warn("Failure threshold reached, aborting future batch upgrade waves",
				"failureCount", failureCount,
				"failureThreshold", params.FailureThreshold)
			for i := batchEnd; i < len(params.SelectedClusters); i++ {
				results[i] = ChildUpgradeResult{
					ClusterID: params.SelectedClusters[i].ClusterID,
					Success:   false,
					Error:     "Skipped: failure threshold reached",
				}
				skippedCount++
			}
		}
	}

	// Determine final batch status
	finalStatus := string(models.UpgradeStatusCompleted)
	finalError := ""
	if failureCount > 0 {
		finalStatus = string(models.UpgradeStatusFailed)
		finalError = fmt.Sprintf("%d of %d cluster upgrades failed", failureCount, len(params.SelectedClusters))
	}
	if aborted {
		finalStatus = string(models.UpgradeStatusFailed)
		finalError = fmt.Sprintf("Batch upgrade aborted: failure threshold (%d) reached. Succeeded: %d, Failed: %d, Skipped: %d",
			params.FailureThreshold, successCount, failureCount, skippedCount)
	}

	// Update batch job with final counts and status
	err = workflow.ExecuteActivity(statusCtx, batchActivity.CompleteBatchJobActivity,
		params.BatchJobID, finalStatus, finalError, successCount, failureCount, skippedCount).Get(statusCtx, nil)
	if err != nil {
		wf.Logger.Error("Failed to update batch job final status", "error", err)
		return nil, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("update batch job final status: %w", err))
	}

	wf.Logger.Info("Batch cluster upgrade workflow completed",
		"batchJobID", params.BatchJobID,
		"status", finalStatus,
		"succeeded", successCount,
		"failed", failureCount,
		"skipped", skippedCount)

	if failureCount > 0 {
		return results, vsaerrors.NewVCPError(vsaerrors.ErrInternalServerError, fmt.Errorf("%s", finalError))
	}

	return results, nil
}

// generateChildUpgradeJobID returns a UUID that is stable across Temporal
// replays and retries for the same batch and cluster.
func generateChildUpgradeJobID(batchJobID, clusterID string) string {
	name := fmt.Sprintf("batch-cluster-upgrade:%s:%s", batchJobID, clusterID)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}

// BatchChildUpgradeWorkflowID returns the Temporal workflow ID of the child
// upgrade started for a cluster in a batch. Child job rows store the batch and
// cluster IDs rather than this ID, so supervisors outside the workflow must
// rebuild it here to describe the execution.
func BatchChildUpgradeWorkflowID(batchJobID, clusterID string) string {
	return fmt.Sprintf("%s-child-%s", batchJobID, clusterID)
}
