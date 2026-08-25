package expertMode

import (
	"errors"
	"fmt"

	"github.com/vcp-vsa-control-Plane/vsa-control-plane/clients/vlm"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities"
	expertmodeactivities "github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/activities/expert_mode_activities"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/core/orchestrator/workflows"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/database/datamodel"
	database "github.com/vcp-vsa-control-Plane/vsa-control-plane/database/vcp"
	hyperscalermodels "github.com/vcp-vsa-control-Plane/vsa-control-plane/hyperscaler/models"
	vsaerrors "github.com/vcp-vsa-control-Plane/vsa-control-plane/lib/errors"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/utils/middleware"
	workflowengine "github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/temporal"
	"github.com/vcp-vsa-control-Plane/vsa-control-plane/workflow_engine/util"
	"go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	rbacPoolBatchSize = 5 // Number of pools to process in parallel per batch
)

// failedPoolInfo tracks a pool that failed during RBAC update
type failedPoolInfo struct {
	poolUUID string
	err      error
}

// updateRBACForPoolsWorkflow implements the WorkflowInterface for checking and updating RBAC hashes for pools.
type updateRBACForPoolsWorkflow struct {
	workflows.BaseWorkflow
	SE *database.Storage
}

// Enforcing the WorkflowInterface on updateRBACForPoolsWorkflow
var _ workflows.WorkflowInterface = &updateRBACForPoolsWorkflow{}

// UpdateRbacForSinglePoolWorkflow refreshes RBAC for a single pool identified by UUID,
// reusing the same child workflow (UpdateSinglePoolRbacChildWorkflow) as the bulk workflow.
func UpdateRbacForSinglePoolWorkflow(ctx workflow.Context, poolId string) error {
	rbacWF := new(updateRBACForPoolsWorkflow)
	err := rbacWF.Setup(ctx, nil)
	if err != nil {
		return workflows.ConvertToVSAError(err)
	}

	if err = rbacWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return workflows.ConvertToVSAError(err)
	}
	rbacWF.Status = workflows.WorkflowStatusRunning
	err = rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		rbacWF.Status = workflows.WorkflowStatusFailed
		originalErr := err
		updateErr := rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), originalErr)
		if updateErr != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status to Error for UpdateRbacForSinglePoolWorkflow", "error", updateErr)
		}
		return workflows.ConvertToVSAError(originalErr)
	}

	_, customErr := rbacWF.RunForSinglePool(ctx, poolId)
	if customErr != nil {
		rbacWF.Status = workflows.WorkflowStatusFailed
		updateErr := rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if updateErr != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status to Error for UpdateRbacForSinglePoolWorkflow", "error", updateErr)
		}
		return workflows.ConvertToVSAError(customErr)
	}

	rbacWF.Status = workflows.WorkflowStatusCompleted
	err = rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		return err
	}
	return nil
}

// UpdateRbacForPoolsWorkflow iterates through all active ONTAP mode pools and compares
// the hash of the latest RBAC file from GCS bucket for their ONTAP version to determine if an update is required.
func UpdateRbacForPoolsWorkflow(ctx workflow.Context) error {
	rbacWF := new(updateRBACForPoolsWorkflow)
	err := rbacWF.Setup(ctx, nil)
	if err != nil {
		return workflows.ConvertToVSAError(err)
	}

	if err = rbacWF.EnsureJobState(ctx, datamodel.JobsStateNEW); err != nil {
		return workflows.ConvertToVSAError(err)
	}
	rbacWF.Status = workflows.WorkflowStatusRunning
	err = rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStatePROCESSING), nil)
	if err != nil {
		rbacWF.Status = workflows.WorkflowStatusFailed
		// Preserve the original error - try to update job status to ERROR (best effort)
		// but return the original error to maintain consistency with error handling at lines 62-70
		originalErr := err
		updateErr := rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), originalErr)
		if updateErr != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status to Error for UpdateRbacForPoolsWorkflow", "error", updateErr)
		}
		return workflows.ConvertToVSAError(originalErr)
	}
	_, customErr := rbacWF.Run(ctx)
	if customErr != nil {
		rbacWF.Status = workflows.WorkflowStatusFailed
		// Preserve the original error - try to update job status to ERROR (best effort)
		// but always return the original error to maintain consistency with error handling at lines 58-65
		updateErr := rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStateERROR), customErr)
		if updateErr != nil {
			workflow.GetLogger(ctx).Error("Failed to update job status to Error for UpdateRbacForPoolsWorkflow", "error", updateErr)
		}
		return workflows.ConvertToVSAError(customErr)
	}
	rbacWF.Status = workflows.WorkflowStatusCompleted
	err = rbacWF.UpdateJobStatus(ctx, string(datamodel.JobsStateDONE), nil)
	if err != nil {
		return err
	}
	return nil
}

func (wf *updateRBACForPoolsWorkflow) Setup(ctx workflow.Context, input interface{}) error {
	info := workflow.GetInfo(ctx)
	wf.ID = info.WorkflowExecution.ID
	wf.Status = workflows.WorkflowStatusCreated

	// Get correlationID from context if available, otherwise generate a new one
	correlationID := utils.RandomUUID()
	if existingCorrelationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx); err == nil {
		correlationID = existingCorrelationID
	}

	// Add workflowID and correlationID to logger fields so they propagate to child workflows
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID":                            wf.ID,
		string(middleware.RequestCorrelationID): correlationID,
	})
	logger := util.GetLogger(ctx)
	wf.Logger = logger

	return workflow.SetQueryHandler(ctx, "status", func() (*workflows.WorkflowStatus, error) {
		return &workflows.WorkflowStatus{
			ID:     wf.ID,
			Status: wf.Status,
		}, nil
	})
}

// UpdateSinglePoolRbacChildWorkflow is a child workflow that processes a single pool's RBAC update
func UpdateSinglePoolRbacChildWorkflow(ctx workflow.Context, poolDetails expertmodeactivities.PoolDetailsWithRbacHash) error {
	// Get correlationID from context (guaranteed to be set by parent workflow)
	correlationID := utils.RandomUUID()
	if existingCorrelationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx); err == nil {
		correlationID = existingCorrelationID
	}

	// Add workflow ID, request ID, and correlation ID to context for VLM workflows
	workflowInfo := workflow.GetInfo(ctx)
	ctx = util.AddExtraLoggerFields(ctx, map[string]interface{}{
		"workflowID":                            workflowInfo.WorkflowExecution.ID,
		"requestID":                             utils.RandomUUID(),
		string(middleware.RequestCorrelationID): correlationID,
	})
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		TaskQueue:           workflowengine.BackgroundTaskQueue,
		StartToCloseTimeout: workflows.RbacActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        workflows.RbacActivityInitialBackoff,
			BackoffCoefficient:     2.0,
			MaximumInterval:        workflows.RbacActivityMaxInterval,
			MaximumAttempts:        int32(workflows.RbacActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	poolActivity := &activities.PoolActivity{}

	// Get pool by UUID first - we need it to get the ONTAP version for the file path
	var pool *datamodel.Pool
	err := workflow.ExecuteActivity(ctx, rbacActivity.GetPoolByUUID, poolDetails.PoolUUID).Get(ctx, &pool)
	if err != nil {
		logger.Error("Failed to get pool for pool uuid", "poolUUID", poolDetails.PoolUUID, "error", err)
		return err
	}

	// Initialize bucket file details with ONTAP version from pool
	var ontapVersion string
	if pool.BuildInfo != nil && pool.BuildInfo.OntapVersion != "" {
		ontapVersion = pool.BuildInfo.OntapVersion
	} else {
		logger.Error("Pool does not have ONTAP version in BuildInfo", "poolUUID", poolDetails.PoolUUID)
		return vsaerrors.WrapAsNonRetryableTemporalApplicationError(
			vsaerrors.NewVCPError(vsaerrors.ErrBadRequest, errors.New("pool missing ONTAP version")))
	}

	bucketFileDetails := &hyperscalermodels.BucketFileDetails{
		BucketName:     activities.ExpertModeRbacBucketName,
		FileUrl:        utils.GenerateRbacFilePath(activities.ExpertModeRbacFilePath, ontapVersion),
		FileHashSHA256: poolDetails.LatestRbacHash,
	}

	err = workflow.ExecuteActivity(ctx, poolActivity.ValidateRbacHash, ontapVersion, bucketFileDetails).Get(ctx, nil)
	if err != nil {
		logger.Error("Failed to validate rbac hash for version", "ontapVersion", ontapVersion, "error", err)
		return err
	}

	// Get ONTAP credentials
	var ontapCredentials *vlm.OntapCredentials
	err = workflow.ExecuteActivity(ctx, poolActivity.GetOnTapCredentials, pool).Get(ctx, &ontapCredentials)
	if err != nil {
		logger.Error("Failed to get ontap credentials", "error", err)
		return err
	}

	// Get expert mode credentials
	var expertModeCredentials *vlm.OntapCredentials
	err = workflow.ExecuteActivity(ctx, poolActivity.GetExpertModeCredentials, pool).Get(ctx, &expertModeCredentials)
	if err != nil {
		logger.Error("Failed to get expert mode credentials", "error", err)
		return err
	}

	// Parse VLM config
	var vlmConfig *vlm.VLMConfig
	err = workflow.ExecuteActivity(ctx, poolActivity.ParseVlmConfig, pool).Get(ctx, &vlmConfig)
	if err != nil {
		logger.Error("Failed to parse vlm config", "error", err)
		return err
	}

	// Prepare create VSA expert mode request
	createVSAExpertModeUserReq := &vlm.OntapExpertModeUserConfig{}
	err = workflow.ExecuteActivity(ctx, poolActivity.PrepareCreateVSAExpertModeReq, *vlmConfig, *ontapCredentials, *expertModeCredentials, pool, bucketFileDetails).Get(ctx, createVSAExpertModeUserReq)
	if err != nil {
		logger.Error("Failed to create ONTAP expert mode request", "error", err)
		return err
	}

	// Create VSA expert mode user
	vsaClientWorkflowManager := vlm.NewVSAClientWorkflowManager()
	ontapExpertModeUserResponse, err := vsaClientWorkflowManager.CreateVSAExpertModeUser(ctx, createVSAExpertModeUserReq)
	if err != nil {
		logger.Error("Failed to update VSA expert mode user", "poolUUID", poolDetails.PoolUUID, "error", err)
		return err
	}

	// Update RBAC file checksum
	bucketFileDetails.FileHashSHA256 = ontapExpertModeUserResponse.RbacFileChecksum

	// Update RBAC checksum in pool
	if err = workflow.ExecuteActivity(ctx, poolActivity.UpdateRbacCheckSumInPool, pool, bucketFileDetails).Get(ctx, nil); err != nil {
		logger.Error("Failed to update RBAC for pool uuid", "poolUUID", poolDetails.PoolUUID, "error", err)
		return err
	}

	return nil
}

func (wf *updateRBACForPoolsWorkflow) Run(ctx workflow.Context, args ...interface{}) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: workflows.RbacActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        workflows.RbacActivityInitialBackoff,
			BackoffCoefficient:     2.0,
			MaximumInterval:        workflows.RbacActivityMaxInterval,
			MaximumAttempts:        int32(workflows.RbacActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 1: Get all active ONTAP mode pools
	var pools []*datamodel.Pool
	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}
	if err := workflow.ExecuteActivity(ctx, rbacActivity.ListActiveExpertModePools).Get(ctx, &pools); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if len(pools) == 0 {
		// Valid scenario: no active expert mode pools exist. Complete successfully with no work.
		logger.Info("No active ONTAP pools found; skipping RBAC update workflow")
		return nil, nil
	}

	// Get pools grouped by ONTAP version with RBAC hash
	var poolsByVersion map[string][]expertmodeactivities.PoolDetailWithCurrentHash
	if err := workflow.ExecuteActivity(ctx, rbacActivity.GetPoolsDetailsByOntapVersion, pools).Get(ctx, &poolsByVersion); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	var poolListNeedUpdate []expertmodeactivities.PoolDetailsWithRbacHash
	if err := workflow.ExecuteActivity(ctx, rbacActivity.GetLatestRbacHashForAllOntapVersion, poolsByVersion).Get(ctx, &poolListNeedUpdate); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if len(poolListNeedUpdate) == 0 {
		// No pools need updating
		logger.Info("No pools need RBAC update")
		return nil, nil
	}

	// Track failed pools and their errors
	var failedPools []failedPoolInfo

	// Process pools in batches with parallel execution within each batch
	batchSize := rbacPoolBatchSize
	totalBatches := (len(poolListNeedUpdate) + batchSize - 1) / batchSize // Ceiling division
	for i := 0; i < len(poolListNeedUpdate); i += batchSize {
		end := i + batchSize
		if end > len(poolListNeedUpdate) {
			end = len(poolListNeedUpdate)
		}

		batch := poolListNeedUpdate[i:end]
		currentBatchNum := (i / batchSize) + 1
		logger.Info("Processing batch of pools for RBAC update", "batchStart", i+1, "batchEnd", end, "batchSize", len(batch), "batchNum", currentBatchNum, "totalBatches", totalBatches)

		// Start all child workflows in the current batch in parallel
		var futures []workflow.ChildWorkflowFuture
		for _, poolDetails := range batch {
			childWorkflowID := workflow.GetInfo(ctx).WorkflowExecution.ID + "-pool-" + poolDetails.PoolUUID
			childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
				WorkflowID:            childWorkflowID,
				TaskQueue:             workflowengine.BackgroundTaskQueue,
				WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
			})

			// Get correlationID from parent context and add it to child context, this ensures the child workflow receives the parent's correlationID
			correlationID := utils.RandomUUID()
			if existingCorrelationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx); err == nil {
				correlationID = existingCorrelationID
			}

			// Add correlationID to child workflow context so it propagates to the child workflow
			childCtx = util.AddExtraLoggerFields(childCtx, map[string]interface{}{
				string(middleware.RequestCorrelationID): correlationID,
			})

			future := workflow.ExecuteChildWorkflow(childCtx, UpdateSinglePoolRbacChildWorkflow, poolDetails)
			futures = append(futures, future)
		}

		// Track completion status and errors for each future by index
		// Using slices to track which futures have completed and their results
		// Each goroutine writes to a different index, which is safe in Temporal workflows
		completed := make([]bool, len(futures))
		futureErrors := make([]error, len(futures))

		// Use workflow.Go to process each future concurrently
		for j, future := range futures {
			f := future
			idx := j

			workflow.Go(ctx, func(gCtx workflow.Context) {
				err := f.Get(gCtx, nil)
				// Store error for this specific future by index
				// Each goroutine writes to a different index, avoiding data races
				futureErrors[idx] = err
				completed[idx] = true
				if err != nil {
					logger.Error("Failed to execute child workflow for pool uuid", "poolUUID", batch[idx].PoolUUID, "error", err)
				}
			})
		}

		// Yield control and wait for the entire batch to finish using workflow.Await, this yields control to Temporal while waiting, preventing deadlock detection
		err := workflow.Await(ctx, func() bool {
			return countCompletedFutures(completed) == len(futures)
		})
		if err != nil {
			// workflow.Await returned an error (e.g., context cancelled), mark all remaining incomplete pools as failed
			completedCount := countCompletedFutures(completed)
			logger.Error("workflow.Await returned error for batch", "batchStart", i+1, "batchEnd", end, "error", err, "completed", completedCount, "total", len(futures))
			continue
		}

		// Collect failures from completed futures and verify all futures completed
		for j := 0; j < len(futures); j++ {
			if !completed[j] {
				// Future didn't complete - mark as failed
				logger.Warn("Future did not complete for pool uuid", "index", j, "poolUUID", batch[j].PoolUUID)
				failedPools = append(failedPools, failedPoolInfo{
					poolUUID: batch[j].PoolUUID,
					err:      errors.New("child workflow did not complete - workflow may have been cancelled or timed out"),
				})
			} else if futureErrors[j] != nil {
				// Future completed but with an error - track the failure
				failedPools = append(failedPools, failedPoolInfo{
					poolUUID: batch[j].PoolUUID,
					err:      futureErrors[j],
				})
			}
		}

		logger.Info("Completed batch of pools for RBAC update", "batchStart", i+1, "batchEnd", end)
	}

	// Return error if any pools failed
	if len(failedPools) > 0 {
		// Build detailed error message with all failed pools
		errorMsg := fmt.Sprintf("RBAC update failed for %d pool(s): ", len(failedPools))
		for i, failed := range failedPools {
			if i > 0 {
				errorMsg += "; "
			}
			errorMsg += fmt.Sprintf("pool %s: %v", failed.poolUUID, failed.err)
		}
		logger.Error("RBAC update workflow completed with failures", "error", errorMsg)
		return nil, workflows.ConvertToVSAError(errors.New(errorMsg))
	}

	logger.Info("RBAC update workflow completed successfully for all pools", "poolCount", len(poolListNeedUpdate))
	return nil, nil
}

// RunForSinglePool looks up a single pool by UUID and runs the RBAC update child workflow for it.
func (wf *updateRBACForPoolsWorkflow) RunForSinglePool(ctx workflow.Context, poolId string) (interface{}, *vsaerrors.CustomError) {
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: workflows.RbacActivityStartToClose,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:        workflows.RbacActivityInitialBackoff,
			BackoffCoefficient:     2.0,
			MaximumInterval:        workflows.RbacActivityMaxInterval,
			MaximumAttempts:        int32(workflows.RbacActivityMaxAttempts),
			NonRetryableErrorTypes: []string{"PanicError"},
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	rbacActivity := &expertmodeactivities.RBACUpdateActivity{}

	// Step 1: Resolve pool by UUID
	var pool *datamodel.Pool
	if err := workflow.ExecuteActivity(ctx, rbacActivity.GetPoolByUUID, poolId).Get(ctx, &pool); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Step 2: Extract ONTAP version details for the single pool
	var poolsByVersion map[string][]expertmodeactivities.PoolDetailWithCurrentHash
	if err := workflow.ExecuteActivity(ctx, rbacActivity.GetSinglePoolVersionDetails, pool).Get(ctx, &poolsByVersion); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	// Step 3: Check if pool needs RBAC update
	var poolListNeedUpdate []expertmodeactivities.PoolDetailsWithRbacHash
	if err := workflow.ExecuteActivity(ctx, rbacActivity.GetLatestRbacHashForAllOntapVersion, poolsByVersion).Get(ctx, &poolListNeedUpdate); err != nil {
		return nil, workflows.ConvertToVSAError(err)
	}

	if len(poolListNeedUpdate) == 0 {
		logger.Info("Pool does not need RBAC update", "poolID", poolId)
		return nil, nil
	}

	// Step 4: Execute the child workflow for the single pool
	poolDetails := poolListNeedUpdate[0]
	childWorkflowID := workflow.GetInfo(ctx).WorkflowExecution.ID + "-pool-" + poolDetails.PoolUUID
	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:            childWorkflowID,
		TaskQueue:             workflowengine.BackgroundTaskQueue,
		WorkflowIDReusePolicy: enums.WORKFLOW_ID_REUSE_POLICY_REJECT_DUPLICATE,
	})

	correlationID := utils.RandomUUID()
	if existingCorrelationID, err := utils.GetCorrelationIDFromWorkflowContextLoggerFields(ctx); err == nil {
		correlationID = existingCorrelationID
	}
	childCtx = util.AddExtraLoggerFields(childCtx, map[string]interface{}{
		string(middleware.RequestCorrelationID): correlationID,
	})

	if err := workflow.ExecuteChildWorkflow(childCtx, UpdateSinglePoolRbacChildWorkflow, poolDetails).Get(ctx, nil); err != nil {
		logger.Error("Failed to execute child workflow for pool", "poolUUID", poolDetails.PoolUUID, "error", err)
		return nil, workflows.ConvertToVSAError(err)
	}

	logger.Info("RBAC update workflow completed successfully for pool", "poolUUID", poolDetails.PoolUUID)
	return nil, nil
}

// countCompletedFutures counts the number of completed futures in the provided slice
func countCompletedFutures(completed []bool) int {
	count := 0
	for _, c := range completed {
		if c {
			count++
		}
	}
	return count
}
